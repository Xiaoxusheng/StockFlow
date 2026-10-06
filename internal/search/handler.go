package search

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
)

// HTTP 层（禁止直连数据库，architecture §1）：参数解析/校验、逐 type 权限过滤、
// 数据权限快照注入、统一信封（api.md §2）。业务 ID 字符串形态、时间
// YYYY-MM-DD HH:mm:ss（database.JSONTime）。

// 参数契约常量（efficiency-layer-phase1 §2.1）。
const (
	minQLen  = 2  // len(q)<2 或空查询直接返回空 groups，不执行任何 SQL
	maxQLen  = 64 // q 长度上限（超长 400，防超长 ILIKE 与日志噪音）
	defLimit = 5  // 每组条数缺省
	maxLimit = 20 // 每组条数上限（超出收口）
)

// Item 搜索条目（api.md §2 业务 ID 字符串形态）。
type Item struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	Title     string            `json:"title"`
	Code      string            `json:"code"`
	Status    string            `json:"status"`
	Summary   string            `json:"summary"`
	UpdatedAt database.JSONTime `json:"updated_at"`

	// rank 匹配档位（组装列，不出 JSON；doc 多分支合并排序用）。
	rank int `json:"-"`
}

// Group 类型分组（count=该 type 权限面内匹配总数；items 受 limit 收口）。
type Group struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	Count int64  `json:"count"`
	Items []Item `json:"items"`
}

// result 响应体（groups 恒非 nil，空结果序列化为 []）。
type result struct {
	Groups []Group `json:"groups"`
}

// Service 搜索服务（分组编排：types 过滤 → 逐 type 权限过滤 → 查询合并）。
type Service struct {
	repo Repository
}

// NewService 构建服务。
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// SearchQuery 服务入参（HasPerm 由 handler 以 auth.HasPermission 注入，测试可替换）。
type SearchQuery struct {
	Q             string
	Types         []string
	AllWarehouses bool
	WarehouseIDs  []int64
	WHFilter      int64 // warehouse_id 收窄过滤器（0=不过滤）
	Limit         int
	HasPerm       func(code string) bool
}

// Search 分组搜索（零写语句；错误逐层透传由 handler 走统一信封）。
func (s *Service) Search(ctx context.Context, q SearchQuery) ([]Group, error) {
	f := filter{q: q.Q, all: q.AllWarehouses, whIDs: q.WarehouseIDs, wh: q.WHFilter, limit: q.Limit}
	groups := make([]Group, 0)
	idx := make(map[string]int)
	for i := range units {
		u := units[i]
		if len(q.Types) > 0 && !containsStr(q.Types, u.typ) {
			continue
		}
		if q.HasPerm != nil && !q.HasPerm(u.perm) {
			continue // 无对应权限码的 type 不查询不出组（§2.1）
		}
		rows, total, err := s.repo.SearchUnit(ctx, u, f)
		if err != nil {
			return nil, err
		}
		if total == 0 || len(rows) == 0 {
			continue // 空结果（含越界仓库零结果）不出组
		}
		gi, ok := idx[u.typ]
		if !ok {
			gi = len(groups)
			idx[u.typ] = gi
			groups = append(groups, Group{Type: u.typ, Title: u.groupTitle, Items: make([]Item, 0, len(rows))})
		}
		groups[gi].Count += total // doc 多分支合计为该 type 匹配总数
		for _, r := range rows {
			groups[gi].Items = append(groups[gi].Items, Item{
				ID:        strconv.FormatInt(r.ID, 10),
				Type:      u.typ,
				Title:     r.Title,
				Code:      r.Code,
				Status:    r.Status,
				Summary:   r.Summary,
				UpdatedAt: r.UpdatedAt,
				rank:      r.MatchRank,
			})
		}
	}
	// doc 多分支合并：按 精确>前缀>包含、updated_at DESC 归一排序后截断到 limit
	// （单元查询已各自 LIMIT，合并集可能超出）。
	for gi := range groups {
		if len(groups[gi].Items) > q.Limit {
			items := groups[gi].Items
			sort.SliceStable(items, func(a, b int) bool {
				if items[a].rank != items[b].rank {
					return items[a].rank < items[b].rank
				}
				return items[a].UpdatedAt.After(items[b].UpdatedAt.Time)
			})
			groups[gi].Items = items[:q.Limit]
		}
	}
	return groups, nil
}

// containsStr 字符串集合含值判定。
func containsStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// handler 搜索 HTTP 层。
type handler struct {
	svc *Service
	// perm 权限判定注入点（nil 时用 auth.HasPermission；测试替身替换用，包内私有）。
	perm func(c *gin.Context, code string) bool
}

// permOf 权限判定（AuthRequired 之后调用；HasPermission 与 RequirePermission 同构语义）。
func (h *handler) permOf(c *gin.Context, code string) bool {
	if h.perm != nil {
		return h.perm(c, code)
	}
	return auth.HasPermission(c, code)
}

// @Summary GET /api/search
// @Tags 搜索
// @Produce json
// @Param q query string true "关键词（2-64 字符；<2 返回空 groups 不做 SQL）"
// @Param types query string false "类型过滤（逗号分隔，sku/product/barcode/batch/serial/bin/warehouse/customer/supplier/doc/logistics）"
// @Param warehouse_id query int false "仓库收窄过滤器（与数据权限求交，越界按无结果处理）"
// @Param limit query int false "每组条数（缺省 5，上限 20）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/search [get]
func (h *handler) search(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if n := utf8.RuneCountInString(q); n > maxQLen {
		response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{
			"field": "q", "reason": "长度上限 " + strconv.Itoa(maxQLen) + " 字符",
		}))
		return
	}
	// 空查询/长度<2 短路：直接空 groups，不执行任何 SQL（§2.1）。
	if utf8.RuneCountInString(q) < minQLen {
		response.OK(c, result{Groups: []Group{}})
		return
	}
	types, ok := parseTypes(c)
	if !ok {
		return
	}
	wh, ok := parseInt64(c, "warehouse_id", true)
	if !ok {
		return
	}
	limit, ok := parseLimit(c)
	if !ok {
		return
	}
	if _, ok := auth.CurrentUser(c); !ok {
		// AuthRequired 未挂载/未通过的编程错误防御分支 fail-closed。
		response.Err(c, response.NewError(response.CodeUnauthorized, nil))
		return
	}
	all, ids := auth.WarehouseScope(c)
	groups, err := h.svc.Search(c.Request.Context(), SearchQuery{
		Q:             q,
		Types:         types,
		AllWarehouses: all,
		WarehouseIDs:  ids,
		WHFilter:      wh,
		Limit:         limit,
		HasPerm: func(code string) bool {
			return h.permOf(c, code)
		},
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result{Groups: groups})
}

// parseTypes types 参数（逗号分隔白名单；非法值 400，空=全部）。
func parseTypes(c *gin.Context) ([]string, bool) {
	raw := strings.TrimSpace(c.Query("types"))
	if raw == "" {
		return nil, true
	}
	whitelist := validTypes()
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !whitelist[p] {
			response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{
				"field": "types", "reason": "含未知类型：" + p,
			}))
			return nil, false
		}
		if !containsStr(out, p) {
			out = append(out, p)
		}
	}
	return out, true
}

// parseInt64 可选整数查询参数（required=false 且缺省返回 0；非法 400）。
func parseInt64(c *gin.Context, key string, positive bool) (int64, bool) {
	raw := c.Query(key)
	if raw == "" {
		return 0, true
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || (positive && v <= 0) {
		response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{
			"field": key, "reason": "必须为正整数",
		}))
		return 0, false
	}
	return v, true
}

// parseLimit 每组条数（缺省 5，上限 20 收口；非法/非正 400）。
func parseLimit(c *gin.Context) (int, bool) {
	raw := c.Query("limit")
	if raw == "" {
		return defLimit, true
	}
	v, ok := parseInt64(c, "limit", true)
	if !ok {
		return 0, false
	}
	if v > maxLimit {
		return maxLimit, true
	}
	return int(v), true
}

// RegisterRoutes 搜索域路由（三参冻结形态，先例 reports.RegisterRoutes；由 router
// 装配挂 AuthRequired 保护组——efficiency-layer-phase1 §4.1 端点级只挂认证，
// 组内逐 type 权限过滤在 handler 完成）。
//
//	GET /api/search   认证（无端点级权限码）   全局业务搜索
//
// 挂载建议：router.go protected 组一行 `search.RegisterRoutes(protected, db, rdb)`
// （protected 已 Use auth.AuthRequired，勿在公共组重复挂认证中间件）。
// fail-fast：db 为 nil 即 panic（杜绝带病启动，plan §3.1 规则①）。
func RegisterRoutes(rg *gin.RouterGroup, db *gorm.DB, rdb *redis.Client) {
	_ = rdb // 冻结签名占位：本域无 Redis 消费点
	if db == nil {
		panic("search 装配失败: db 为 nil（router 必须注入 GORM 句柄）")
	}
	h := &handler{svc: NewService(newRepository(db))}
	rg.GET("/search", h.search)
}
