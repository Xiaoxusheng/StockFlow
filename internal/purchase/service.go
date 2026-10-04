package purchase

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// Service 采购入库域业务层（architecture.md §1：状态机/事务边界/校验所在层）。
// 依赖以窄接口注入（Repository + 跨域消费接口，ports.go），单元测试以内存替身替换。
//
// 事务边界（plan §10）：每个业务动作 = 单外层事务——单据状态守卫迁移 → 库存原语
// （经 StockGateway，传 tx 组合）→ document_approvals/异常联动 → middleware.Audit
// → COMMIT；任何步骤失败整体回滚。
type Service struct {
	repo Repository
	opt  options
}

// NewService 构建业务层（router 装配入口：purchase.RegisterRoutes 内部经此构造，
// 或集成工程师按需自行构造后注入 Option）。
func NewService(repo Repository, opts ...Option) *Service {
	s := &Service{repo: repo}
	for _, opt := range opts {
		opt(&s.opt)
	}
	return s
}

// Actor 操作者上下文（handler 自 gin 上下文提取后传入 Service，供审计归因）。
type Actor struct {
	UserID    int64
	Username  string
	IsSuper   bool
	RequestID string
	IP        string
	UserAgent string
	Method    string
	Path      string
}

// stockActor 转 stock.Actor（库存原语归因）。
func (a Actor) stockActor() stock.Actor {
	return stock.Actor{
		ID: a.UserID, Name: a.Username, RequestID: a.RequestID,
		IP: a.IP, UserAgent: a.UserAgent, Method: a.Method, Path: a.Path,
	}
}

// auditEntry 构造 purchase 域审计条目骨架（module=purchase；快照由调用方补充；
// 审计与业务同事务写入，失败即业务失败整体回滚——architecture.md §4）。
func (a Actor) auditEntry(objectType string, objectID int64, action string) middleware.AuditEntry {
	return middleware.AuditEntry{
		Module:       "purchase",
		ObjectType:   objectType,
		Action:       action,
		ObjectID:     objectID,
		OperatorID:   a.UserID,
		OperatorName: a.Username,
		RequestID:    a.RequestID,
		IP:           a.IP,
		UserAgent:    a.UserAgent,
		Method:       a.Method,
		Path:         a.Path,
		Success:      true,
	}
}

// tx 单外层事务执行（database.Tx：fn 返回错误整体回滚）。
func (s *Service) tx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return database.Tx(ctx, s.repo.DB(), fn)
}

// ---- 单号（business-flow §13.1：统一编号引擎，禁止自增 ID 当业务单号）----

// docRule 冻结注册表规则读取（PO/IN/RC/QC/PW 均在 internal/docnum 冻结注册表内；
// 缺失即编程错误，启动/调用期快速失败）。
func docRule(prefix string) docnum.Rule {
	r, ok := docnum.RuleFor(prefix)
	if !ok {
		panic("purchase: docnum 冻结注册表缺少前缀 " + prefix)
	}
	return r
}

// ---- 通用校验助手（api.md §4：后端完整校验，失败必须带 details）----

func invalidParam(field, reason string) *response.Error {
	return response.NewError(response.CodeInvalidParam, map[string]any{"field": field, "reason": reason})
}

// requireGateway 必需跨域服务缺位检查（fail-closed，plan §4.3 规则①）。
func (s *Service) requireStock() error {
	if s.opt.stock == nil {
		return response.NewError(ErrStockGatewayMissing, nil)
	}
	return nil
}

func (s *Service) requireChecker() error {
	if s.opt.suppliers == nil || s.opt.warehouses == nil || s.opt.skuAttrs == nil {
		return response.NewError(ErrCheckerMissing, nil)
	}
	return nil
}

// skuFlagsOf 读取 SKU 三开关（found=false → ErrSKUNotFound 语义的参数错误）。
func (s *Service) skuFlagsOf(ctx context.Context, skuID int64) (SKUFlags, error) {
	if s.opt.skuAttrs == nil {
		return SKUFlags{}, response.NewError(ErrCheckerMissing, nil)
	}
	f, found, err := s.opt.skuAttrs.GetFlags(ctx, skuID)
	if err != nil {
		return SKUFlags{}, response.NewError(response.CodeInternalError, map[string]any{
			"reason": "SKU 属性校验失败", "sku_id": skuID, "error": err.Error(),
		})
	}
	if !found {
		return SKUFlags{}, invalidParam("sku_id", fmt.Sprintf("SKU %d 不存在或已删除", skuID))
	}
	return f, nil
}

// guardRows 条件更新影响行数 0 → 数据层守卫未生效（并发竞争；plan §10.2）。
func guardRows(n int64, err error) error {
	if err != nil {
		return err
	}
	if n == 0 {
		return guardMiss
	}
	return nil
}

var errGuardMiss = guardMiss // 服务层 errors.Is 判定用别名

// errReplayConflict 收货幂等键并发冲突哨兵（唯一索引 uk_receipts_idempotency 兜底，
// 事务回滚后转重放路径，plan §8.5 同款两段式）。
var errReplayConflict = errors.New("purchase: 收货幂等键并发冲突")

// normalizePage 列表分页归一（缺省 1/20 与 response.ParsePage 缺省一致）。
func normalizePage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	return page, pageSize
}
