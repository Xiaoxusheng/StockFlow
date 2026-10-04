package inventory

import (
	"context"
	"strings"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// Service 全系统库存与流水的唯一变更入口（backend-m1-plan §8.2、inventory-rules §1）。
//
// 硬性约束（inventory-rules §1/§5/§9.3）：
//   - 禁止任何包直接写库存族六表（plan §4.2 判据 3，M2 业务域只能经本 Service）；
//   - 每个变更方法 = 单事务：定位并锁定库存行（SELECT ... FOR UPDATE）→ 业务层+数据层
//     双重数量校验（inventory-rules §9.2）→ 原子条件 UPDATE（WHERE 数量条件满足，
//     影响行数为 0 判失败回滚）→ 同事务写 append-only 流水 → 同事务写操作日志 → COMMIT；
//   - 任何步骤失败整体回滚（architecture.md §4.1）。
//
// 事务组合：tx 传 nil 时方法自建事务；M2 业务域把"业务单据状态更新 + 库存变更"
// 放进同一外层事务时传入 tx 即可（plan §8.3）。
type Service struct {
	db   *gorm.DB
	rdb  *redis.Client // 预留：M2 幂等键快速路径/热点查询缓存；M1 正确性逻辑不依赖缓存
	repo *repository

	skuChecker SKUChecker
	binChecker BinChecker
}

// NewService 构造库存 Service（M2 域经 router 装配以消费接口注入，plan §4.3；
// router 的 RegisterRoutes 内部同样经此构造）。
func NewService(db *gorm.DB, rdb *redis.Client, opts ...Option) *Service {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	return &Service{
		db:         db,
		rdb:        rdb,
		repo:       &repository{db: db},
		skuChecker: o.skuChecker,
		binChecker: o.binChecker,
	}
}

// Actor/RowKey/Source/MutationResult/LedgerRef 与九个原语 Op 结构已别名承接
// internal/stock（aliases.go，backend-m2-plan §3/§8.3 条 4），本文件不再重复定义。

// rowKeyDetails 五维键的结构化 details（api.md §4：校验失败必须带 details）。
func rowKeyDetails(k RowKey, reason string) map[string]any {
	return map[string]any{
		"reason":       reason,
		"warehouse_id": k.WarehouseID, "zone_id": k.ZoneID, "shelf_id": k.ShelfID,
		"bin_id": k.BinID, "sku_id": k.SKUID, "batch_id": k.BatchID,
	}
}

// notEnoughErr 库存不足错误（plan §8.3：details 带具体 SKU/库位/需求量与可用量）。
func notEnoughErr(key RowKey, reason string, need, avail Qty) error {
	d := rowKeyDetails(key, reason)
	d["need"] = need.String()
	d["available"] = avail.String()
	return response.NewError(ErrNotEnough, d)
}

// stockSnapshot 操作日志 before/after 快照（architecture.md §8.1 关键更新必带）。
type stockSnapshot struct {
	RowID int64      `json:"row_id"`
	State StockState `json:"state"`
}

// ---- 值域（与迁移 CHECK 约束同源）----

// lockTypes 锁定类型（inventory-rules §4 五类；迁移 chk_inventory_locks_lock_type）。
var lockTypes = map[string]bool{
	"ORDER_HOLD": true, "COUNT_FREEZE": true, "QC_FREEZE": true,
	"MANUAL_FREEZE": true, "EXCEPTION_FREEZE": true,
}

// lockTargetState 锁定类型 → 目标状态列：
//   - ORDER_HOLD（订单占用）→ locked（business-flow §7.2：审核预占，锁定库存）；
//   - COUNT_FREEZE/QC_FREEZE/MANUAL_FREEZE/EXCEPTION_FREEZE → frozen
//     （inventory-rules §2：质检冻结/人工冻结/异常冻结属"冻结库存"状态）。
func lockTargetState(lockType string) (StateColumn, bool) {
	switch lockType {
	case "ORDER_HOLD":
		return ColLocked, true
	case "COUNT_FREEZE", "QC_FREEZE", "MANUAL_FREEZE", "EXCEPTION_FREEZE":
		return ColFrozen, true
	}
	return ColAvailable, false
}

// adjustTypes 调整类型（business-flow §11.1 值域；迁移 chk_inventory_adjustments_adjust_type）。
var adjustTypes = map[string]bool{"盘盈": true, "盘亏": true, "损耗": true, "报废": true, "其他": true}

// adjustDirection 调整方向：盘盈为增，其余为减。
func adjustDirection(adjustType string) int {
	if adjustType == "盘盈" {
		return 1
	}
	return -1
}

// serialStatuses 序列号状态（迁移 chk_serial_numbers_status）。
var serialStatuses = map[string]bool{
	"IN_STOCK": true, "LOCKED": true, "OUTBOUND": true, "RETURNED": true, "FROZEN": true,
}

// changeTypes 流水业务类型（迁移 chk_inventory_ledgers_change_type；plan §8.4）。
var changeTypes = map[string]bool{
	"INBOUND": true, "OUTBOUND": true, "TRANSFER_OUT": true, "TRANSFER_IN": true,
	"LOCK": true, "RELEASE": true, "MOVE": true,
	"INSPECT_PASS": true, "INSPECT_DEFECTIVE": true, "ADJUST": true,
}

// ---- 入参校验（api.md §4：后端完整校验）----

func errParam(field, reason string) error {
	return response.NewError(response.CodeInvalidParam, map[string]any{"field": field, "reason": reason})
}

func validateQty(q Qty, field string) error {
	if !q.IsPositive() {
		return response.NewError(ErrQtyInvalid, map[string]any{"field": field, "value": q.String(), "reason": "必须为正数"})
	}
	return nil
}

func validateRowKey(k RowKey, needZoneShelf bool) error {
	if k.WarehouseID <= 0 || k.BinID <= 0 || k.SKUID <= 0 {
		return errParam("row_key", "warehouse_id/bin_id/sku_id 必须为正整数")
	}
	if k.BatchID < 0 {
		return errParam("row_key", "batch_id 必须为 >=0（0=非批次 SKU）")
	}
	if needZoneShelf && (k.ZoneID <= 0 || k.ShelfID <= 0) {
		return errParam("row_key", "行创建类变更必须携带 zone_id/shelf_id")
	}
	return nil
}

func validateSource(s Source) error {
	if strings.TrimSpace(s.Type) == "" || strings.TrimSpace(s.No) == "" {
		return response.NewError(ErrSourceRequired, map[string]any{
			"reason": "库存变更必须携带来源单据类型与单号（inventory-rules §5）",
		})
	}
	return nil
}

func validateIdempotencyKey(key string) error {
	if len(key) > 128 {
		return errParam("idempotency_key", "长度不能超过 128")
	}
	return nil
}

// ---- 跨域校验（plan §4.3；fail-closed：缺位拒绝执行，杜绝静默跳过业务校验）----

func (s *Service) checkSKU(ctx context.Context, skuID int64) error {
	if s.skuChecker == nil {
		return response.NewError(ErrCheckerMissing, map[string]any{
			"reason": "router 未注入 WithSKUChecker（plan §4.3 规则①）",
		})
	}
	ok, err := s.skuChecker.ExistsActive(ctx, skuID)
	if err != nil {
		return response.NewError(response.CodeInternalError, map[string]any{"reason": "SKU 校验失败", "error": err.Error()})
	}
	if !ok {
		return response.NewError(ErrSKUNotFound, map[string]any{"sku_id": skuID})
	}
	return nil
}

func (s *Service) checkBin(ctx context.Context, key RowKey) error {
	if s.binChecker == nil {
		return response.NewError(ErrCheckerMissing, map[string]any{
			"reason": "router 未注入 WithBinChecker（plan §4.3 规则①）",
		})
	}
	ok, err := s.binChecker.ExistsActive(ctx, key.WarehouseID, key.BinID)
	if err != nil {
		return response.NewError(response.CodeInternalError, map[string]any{"reason": "库位校验失败", "error": err.Error()})
	}
	if !ok {
		return response.NewError(ErrBinNotFound, map[string]any{"warehouse_id": key.WarehouseID, "bin_id": key.BinID})
	}
	return nil
}

// checkSKUAndBin 行创建类变更的业务关系校验（api.md §4）。
func (s *Service) checkSKUAndBin(ctx context.Context, key RowKey) error {
	if err := s.checkSKU(ctx, key.SKUID); err != nil {
		return err
	}
	return s.checkBin(ctx, key)
}

// ---- 幂等内核（plan §8.5：重复 key 返回既有结果，不重复扣减）----

// mutate 变更统一内核：幂等预检 → 核心变更（事务）→ 唯一冲突兜底重放。
// 幂等的最终准绳是 inventory_ledgers.idempotency_key 部分唯一索引
// （迁移 000005，并发同键由数据库裁决）；预检只是快路径。
//
// 多行变更（MOVE）仅在首条流水记录幂等键（部分唯一索引约束每键至多一行）。
// 外层事务（M2 组合）场景下唯一冲突上抛给调用方整体回滚，重试将命中重放路径。
func (s *Service) mutate(ctx context.Context, outer *gorm.DB, idemKey string, lockAttrs *lockSourceAttr, core func(tx *gorm.DB) (MutationResult, error)) (MutationResult, error) {
	if idemKey != "" {
		led, found, err := findLedgerByIdempotencyKey(s.db.WithContext(ctx), idemKey)
		if err != nil {
			return MutationResult{}, err
		}
		if found {
			return s.replayResult(ctx, led, lockAttrs)
		}
	}

	var res MutationResult
	run := func(tx *gorm.DB) error {
		r, err := core(tx)
		if err != nil {
			return err
		}
		res = r
		return nil
	}
	var err error
	if outer != nil {
		err = run(outer)
	} else {
		err = s.db.WithContext(ctx).Transaction(run)
	}
	if err != nil && idemKey != "" && pgUniqueViolation(err, "uk_inventory_ledgers_idempotency_key") {
		// 并发同键：本事务已回滚，返回既有结果（plan §8.5）。
		led, found, ferr := findLedgerByIdempotencyKey(s.db.WithContext(ctx), idemKey)
		if ferr == nil && found {
			return s.replayResult(ctx, led, lockAttrs)
		}
	}
	return res, err
}

// lockSourceAttr Lock 重放时回查锁记录所需的属性（plan §6.2：按来源单据幂等查重）。
type lockSourceAttr struct {
	whID, binID, skuID, batchID    int64
	lockType, sourceType, sourceNo string
}

// replayResult 幂等重放结果：既有流水 + （Lock 场景）既有锁记录。
func (s *Service) replayResult(ctx context.Context, led *InventoryLedger, lockAttrs *lockSourceAttr) (MutationResult, error) {
	res := MutationResult{
		Replay: true,
		Ledger: LedgerRef{ID: led.ID.Int64(), LedgerNo: led.LedgerNo},
	}
	if lockAttrs != nil {
		lock, found, err := findLockBySource(s.db.WithContext(ctx), lockAttrs.whID, lockAttrs.binID,
			lockAttrs.skuID, lockAttrs.batchID, lockAttrs.lockType, lockAttrs.sourceType, lockAttrs.sourceNo)
		if err != nil {
			return MutationResult{}, err
		}
		if found {
			res.LockID = lock.ID.Int64()
		}
	}
	return res, nil
}

// ---- 审计（plan §4.4：inventory Service 全部变更方法必须写 operation_logs，同事务）----

func auditStock(tx *gorm.DB, action string, objectID int64, actor Actor, req any, before, after StockState) error {
	return middleware.Audit(tx, middleware.AuditEntry{
		Module: "inventory", ObjectType: "inventory", ObjectID: objectID,
		OperatorID: actor.ID, OperatorName: actor.Name,
		RequestID: actor.RequestID, IP: actor.IP, UserAgent: actor.UserAgent,
		Method: actor.Method, Path: actor.Path,
		Success: true, Request: req,
		Before: stockSnapshot{RowID: objectID, State: before},
		After:  stockSnapshot{RowID: objectID, State: after},
	})
}

// ---- 流水构造与写入（plan §8.4 冻结口径）----

// buildLedger 构造流水：qty_before/qty_change/qty_after 记录受影响状态列
// （status_from 所指列）的三态；total 变化型操作 total 同步 ±qty_change
// （由 applyDelta 的成对增量保证），change_type 表达口径差异。
// serialNo 为 M2 追溯接入点（backend-m2-plan §8.3 条 1，M1 遗留 F13）：模型字段与
// insertLedger 的 serial_no 列早已存在，只缺构造赋值——单件操作的原语携带序列号，
// 批量操作传空串（流水形态不变）。
func buildLedger(row *Inventory, changeType string, src Source, actor Actor,
	statusFrom, statusTo StateColumn, before, change Qty, idemKey, remark, serialNo string) *InventoryLedger {
	zoneID, shelfID := row.ZoneID, row.ShelfID
	l := &InventoryLedger{
		SKUID:        row.SKUID,
		WarehouseID:  row.WarehouseID,
		ZoneID:       &zoneID,
		ShelfID:      &shelfID,
		BinID:        row.BinID,
		BatchID:      row.BatchID,
		SerialNo:     serialNo,
		ChangeType:   changeType,
		BusinessType: src.Type,
		BusinessNo:   src.No,
		StatusFrom:   statusFrom.String(),
		StatusTo:     statusTo.String(),
		QtyBefore:    before,
		QtyChange:    change,
		QtyAfter:     before.Add(change),
		OperatorID:   actor.ID,
		OperatorName: actor.Name,
		RequestID:    actor.RequestID,
		Remark:       remark,
	}
	if idemKey != "" {
		l.IdempotencyKey = &idemKey
	}
	return l
}

// writeLedger 写流水；单号经 internal/docnum 统一编号引擎发放（business-flow §13.1、
// plan §4.1/§8.3 条 2——LED 前缀 ResetAll 承接 M1 存量口径），历史遗留同格式单号
// 撞唯一索引（uk_inventory_ledgers_ledger_no）时取下一号重试。
// 幂等键冲突不在此重试——上抛给 mutate 兜底重放。
func writeLedger(tx *gorm.DB, l *InventoryLedger) error {
	rule, _ := docnum.RuleFor("LED")
	for range insertRetryLimit {
		no, err := docnum.Next(context.Background(), tx, rule)
		if err != nil {
			return err
		}
		l.LedgerNo = no
		if err := insertLedger(tx, l); err == nil {
			return nil
		} else if pgUniqueViolation(err, "uk_inventory_ledgers_ledger_no") {
			continue
		} else {
			return err
		}
	}
	return response.NewError(ErrLedgerNumberConflict, nil)
}

// guardFailed 数据层守卫未生效（影响行数 0）或 UPDATE 出错时的统一失败。
func guardFailed(err error, key RowKey, reason string) error {
	if err != nil {
		return err
	}
	return response.NewError(ErrNotEnough, rowKeyDetails(key, reason))
}

// ---- 变更原语（plan §8.2 冻结方法集）----

// Putaway 上架增加（行不存在则创建；行创建类变更需 SKU/库位存在性校验）。
func (s *Service) Putaway(ctx context.Context, tx *gorm.DB, op PutawayOp) (MutationResult, error) {
	if err := validateRowKey(op.Key, true); err != nil {
		return MutationResult{}, err
	}
	if err := validateQty(op.Qty, "qty"); err != nil {
		return MutationResult{}, err
	}
	if err := validateSource(op.Source); err != nil {
		return MutationResult{}, err
	}
	idemKey := strings.TrimSpace(op.IdempotencyKey)
	if err := validateIdempotencyKey(idemKey); err != nil {
		return MutationResult{}, err
	}
	stateCol := ColAvailable
	if op.RequireInspect {
		stateCol = ColPendingInspect
	}
	return s.mutate(ctx, tx, idemKey, nil, func(tx *gorm.DB) (MutationResult, error) {
		if err := s.checkSKUAndBin(ctx, op.Key); err != nil {
			return MutationResult{}, err
		}
		init := StockState{Total: op.Qty}
		if op.RequireInspect {
			init.PendingInspect = op.Qty
		} else {
			init.Available = op.Qty
		}
		row, created, err := ensureRow(tx, op.Key, op.Actor, init)
		if err != nil {
			return MutationResult{}, err
		}
		// 流水三态口径：qty_before 为本次变更前的真实状态——新建行变更前为全零。
		before := StockState{}
		if !created {
			// 盘点冻结守卫（ErrRowCountFrozen）：冻结期行 total 必须恒定（business-flow
			// §10.2"冻结范围"）；新建行不在任何冻结快照内，无需守卫。
			if err := rejectCountFrozen(tx, op.Key); err != nil {
				return MutationResult{}, err
			}
			before = row.State()
			// 存量行：增量 UPDATE（成对：total 与状态列同增，恒等式由构造成立）。
			n, err := applyDelta(tx, row.ID.Int64(), op.Actor.ID, op.Qty,
				[]colDelta{{col: stateCol, delta: op.Qty}}, nil)
			if err != nil || n == 0 {
				return MutationResult{}, guardFailed(err, op.Key, "库存行更新未生效")
			}
		}
		after, err := reloadRow(tx, row.ID.Int64())
		if err != nil {
			return MutationResult{}, err
		}
		led := buildLedger(after, "INBOUND", op.Source, op.Actor, stateCol, stateCol,
			stateCol.getOf(before), op.Qty, idemKey, op.Remark, op.SerialNo)
		if err := writeLedger(tx, led); err != nil {
			return MutationResult{}, err
		}
		if err := auditStock(tx, "putaway", row.ID.Int64(), op.Actor, op, before, after.State()); err != nil {
			return MutationResult{}, err
		}
		return MutationResult{Ledger: LedgerRef{ID: led.ID.Int64(), LedgerNo: led.LedgerNo}}, nil
	})
}

// Lock 预占/冻结：available → locked（订单占用）或 frozen（盘点/质检/人工/异常冻结），
// 并落 inventory_locks 记录（来源单据/类型/数量/操作人/时间，inventory-rules §4.1）。
// 分配与预占只允许使用可用库存；同来源同类型的 ACTIVE 锁存在时幂等返回（不重复预占）。
func (s *Service) Lock(ctx context.Context, tx *gorm.DB, op LockOp) (MutationResult, error) {
	if err := validateRowKey(op.Key, false); err != nil {
		return MutationResult{}, err
	}
	if err := validateQty(op.Qty, "qty"); err != nil {
		return MutationResult{}, err
	}
	if !lockTypes[op.LockType] {
		return MutationResult{}, response.NewError(ErrLockTypeInvalid, map[string]any{"lock_type": op.LockType})
	}
	if err := validateSource(op.Source); err != nil {
		return MutationResult{}, err
	}
	idemKey := strings.TrimSpace(op.IdempotencyKey)
	if err := validateIdempotencyKey(idemKey); err != nil {
		return MutationResult{}, err
	}
	attrs := &lockSourceAttr{whID: op.Key.WarehouseID, binID: op.Key.BinID, skuID: op.Key.SKUID,
		batchID: op.Key.BatchID, lockType: op.LockType, sourceType: op.Source.Type, sourceNo: op.Source.No}
	return s.mutate(ctx, tx, idemKey, attrs, func(tx *gorm.DB) (MutationResult, error) {
		row, err := locateRowForUpdate(tx, op.Key)
		if err != nil {
			return MutationResult{}, err
		}
		if row == nil {
			return MutationResult{}, response.NewError(ErrRecordNotFound, rowKeyDetails(op.Key, "库存行不存在"))
		}
		// 同来源同类型已存在 ACTIVE 锁 → 幂等返回（plan §6.2 idx(source_type, source_no)
		// "按来源单据幂等查重"；防止同一单据重复预占导致超卖，inventory-rules §4）。
		// 查重必须在库存行锁之后：并发同 (来源,类型) Lock 被行锁串行化，后到者在此处
		// 才能看到先到者已提交的 ACTIVE 锁——锁外查重双方都会在对方提交前读到"无锁"，
		// 随后各自落一条 ACTIVE 锁，同一单据重复预占双倍可用库存（idx 为普通索引，
		// 数据库层无唯一约束兜底）。
		existing, found, err := findLockBySource(tx, op.Key.WarehouseID, op.Key.BinID,
			op.Key.SKUID, op.Key.BatchID, op.LockType, op.Source.Type, op.Source.No)
		if err != nil {
			return MutationResult{}, err
		}
		if found && existing.Status == "ACTIVE" {
			led, ledFound, lerr := findLockCreationLedger(tx, *attrs)
			if lerr != nil {
				return MutationResult{}, lerr
			}
			res := MutationResult{Replay: true, LockID: existing.ID.Int64()}
			if ledFound {
				res.Ledger = LedgerRef{ID: led.ID.Int64(), LedgerNo: led.LedgerNo}
			}
			return res, nil
		}
		before := row.State()
		target, _ := lockTargetState(op.LockType)
		// 业务层校验（inventory-rules §9.2 双重校验之一）：带 details 的精确错误。
		if before.Available.Sub(op.Qty).IsNegative() {
			return MutationResult{}, notEnoughErr(op.Key, "可用库存不足", op.Qty, before.Available)
		}
		// 数据层守卫（最后防线）：WHERE available_qty >= n，影响行数 0 即失败。
		n, err := applyDelta(tx, row.ID.Int64(), op.Actor.ID, 0,
			[]colDelta{{col: target, delta: op.Qty}, {col: ColAvailable, delta: op.Qty.Neg()}},
			[]colGuard{{col: ColAvailable, min: op.Qty}})
		if err != nil || n == 0 {
			return MutationResult{}, guardFailed(err, op.Key, "可用库存不足（条件更新未生效）")
		}
		after, err := reloadRow(tx, row.ID.Int64())
		if err != nil {
			return MutationResult{}, err
		}
		lockID, err := insertLock(tx, &InventoryLock{
			WarehouseID: op.Key.WarehouseID, BinID: op.Key.BinID, SKUID: op.Key.SKUID,
			BatchID: op.Key.BatchID, LockType: op.LockType,
			SourceType: op.Source.Type, SourceNo: op.Source.No,
			Qty: op.Qty, Remark: op.Remark, CreatedBy: op.Actor.ID, UpdatedBy: op.Actor.ID,
		})
		if err != nil {
			return MutationResult{}, err
		}
		led := buildLedger(after, "LOCK", op.Source, op.Actor, ColAvailable, target,
			before.Available, op.Qty.Neg(), idemKey, op.Remark, "")
		if err := writeLedger(tx, led); err != nil {
			return MutationResult{}, err
		}
		if err := auditStock(tx, "lock", row.ID.Int64(), op.Actor, op, before, after.State()); err != nil {
			return MutationResult{}, err
		}
		return MutationResult{Ledger: LedgerRef{ID: led.ID.Int64(), LedgerNo: led.LedgerNo}, LockID: lockID}, nil
	})
}

// findLockCreationLedger 回查锁记录创建时的 LOCK 流水（来源幂等重放路径的结果补全）。
func findLockCreationLedger(tx *gorm.DB, attrs lockSourceAttr) (*InventoryLedger, bool, error) {
	var row InventoryLedger
	err := tx.Raw(`
		SELECT id, ledger_no FROM inventory_ledgers
		WHERE change_type = 'LOCK' AND business_type = ? AND business_no = ?
		  AND sku_id = ? AND warehouse_id = ? AND bin_id = ? AND batch_id = ?
		ORDER BY id DESC LIMIT 1`,
		attrs.sourceType, attrs.sourceNo, attrs.skuID, attrs.whID, attrs.binID, attrs.batchID).Scan(&row).Error
	if err != nil {
		return nil, false, err
	}
	if row.ID == 0 {
		return nil, false, nil
	}
	return &row, true, nil
}

// ReleaseLock 释放锁定/解冻：locked/frozen → available（inventory-rules §4.2：必须由
// 明确业务动作触发并生成流水）；锁记录 qty 递减，清零转 RELEASED 并记 released_at/by。
func (s *Service) ReleaseLock(ctx context.Context, tx *gorm.DB, op ReleaseLockOp) (MutationResult, error) {
	if op.LockID <= 0 {
		return MutationResult{}, errParam("lock_id", "必须为正整数")
	}
	if err := validateQty(op.Qty, "qty"); err != nil {
		return MutationResult{}, err
	}
	if err := validateSource(op.Source); err != nil {
		return MutationResult{}, err
	}
	idemKey := strings.TrimSpace(op.IdempotencyKey)
	if err := validateIdempotencyKey(idemKey); err != nil {
		return MutationResult{}, err
	}
	return s.mutate(ctx, tx, idemKey, nil, func(tx *gorm.DB) (MutationResult, error) {
		lock, err := lockForUpdate(tx, op.LockID)
		if err != nil {
			return MutationResult{}, err
		}
		if lock == nil || lock.Status != "ACTIVE" || lock.Qty.Sub(op.Qty).IsNegative() {
			return MutationResult{}, response.NewError(ErrLockNotFound, map[string]any{
				"lock_id": op.LockID, "reason": "锁定记录不存在、已完结或释放量超出锁存量",
			})
		}
		key := RowKey{WarehouseID: lock.WarehouseID, BinID: lock.BinID, SKUID: lock.SKUID, BatchID: lock.BatchID}
		row, err := locateRowForUpdate(tx, key)
		if err != nil {
			return MutationResult{}, err
		}
		if row == nil {
			return MutationResult{}, response.NewError(ErrRecordNotFound, rowKeyDetails(key, "库存行不存在"))
		}
		before := row.State()
		fromCol, _ := lockTargetState(lock.LockType)
		if fromCol.getOf(before).Sub(op.Qty).IsNegative() {
			return MutationResult{}, notEnoughErr(key, "锁定库存不足", op.Qty, fromCol.getOf(before))
		}
		n, err := applyDelta(tx, row.ID.Int64(), op.Actor.ID, 0,
			[]colDelta{{col: ColAvailable, delta: op.Qty}, {col: fromCol, delta: op.Qty.Neg()}},
			[]colGuard{{col: fromCol, min: op.Qty}})
		if err != nil || n == 0 {
			return MutationResult{}, guardFailed(err, key, "锁定库存不足（条件更新未生效）")
		}
		after, err := reloadRow(tx, row.ID.Int64())
		if err != nil {
			return MutationResult{}, err
		}
		// 锁记录拆分：部分释放保留 ACTIVE 并递减 qty；清零转 RELEASED
		// （qty 不写 0——迁移 chk_inventory_locks_qty_positive 要求 qty>0，
		// 终态由 status 表达，释放量以流水为准）。
		remaining := lock.Qty.Sub(op.Qty)
		lock.ReleasedBy = op.Actor.ID
		if remaining.IsZero() {
			lock.Status = "RELEASED"
			lock.ReleasedAt = database.Now()
		} else {
			lock.Qty = remaining
		}
		if err := updateLockAfterSplit(tx, lock, op.Actor.ID); err != nil {
			return MutationResult{}, err
		}
		led := buildLedger(after, "RELEASE", op.Source, op.Actor, fromCol, ColAvailable,
			fromCol.getOf(before), op.Qty.Neg(), idemKey, op.Remark, "")
		if err := writeLedger(tx, led); err != nil {
			return MutationResult{}, err
		}
		if err := auditStock(tx, "release-lock", row.ID.Int64(), op.Actor, op, before, after.State()); err != nil {
			return MutationResult{}, err
		}
		return MutationResult{Ledger: LedgerRef{ID: led.ID.Int64(), LedgerNo: led.LedgerNo}, LockID: op.LockID}, nil
	})
}

// Deduct 出库扣减：核销锁定（locked、total 同减，business-flow §7.2/§8.5——
// 正式扣减发生在发货完成时）；携带 LockID 时同步核销锁定记录（部分核销保留 ACTIVE）。
func (s *Service) Deduct(ctx context.Context, tx *gorm.DB, op DeductOp) (MutationResult, error) {
	if err := validateRowKey(op.Key, false); err != nil {
		return MutationResult{}, err
	}
	if err := validateQty(op.Qty, "qty"); err != nil {
		return MutationResult{}, err
	}
	if err := validateSource(op.Source); err != nil {
		return MutationResult{}, err
	}
	idemKey := strings.TrimSpace(op.IdempotencyKey)
	if err := validateIdempotencyKey(idemKey); err != nil {
		return MutationResult{}, err
	}
	return s.mutate(ctx, tx, idemKey, nil, func(tx *gorm.DB) (MutationResult, error) {
		row, err := locateRowForUpdate(tx, op.Key)
		if err != nil {
			return MutationResult{}, err
		}
		if row == nil {
			return MutationResult{}, response.NewError(ErrRecordNotFound, rowKeyDetails(op.Key, "库存行不存在"))
		}
		// 盘点冻结守卫（ErrRowCountFrozen）：冻结期拒绝出库扣减——total 变化会使
		// 盘点差异（实盘 - 冻结快照）双重计账（business-flow §10.2"冻结范围"）。
		if err := rejectCountFrozen(tx, op.Key); err != nil {
			return MutationResult{}, err
		}
		before := row.State()
		if before.Locked.Sub(op.Qty).IsNegative() {
			return MutationResult{}, notEnoughErr(op.Key, "锁定库存不足", op.Qty, before.Locked)
		}
		n, err := applyDelta(tx, row.ID.Int64(), op.Actor.ID, op.Qty.Neg(),
			[]colDelta{{col: ColLocked, delta: op.Qty.Neg()}},
			[]colGuard{{col: ColLocked, min: op.Qty}})
		if err != nil || n == 0 {
			return MutationResult{}, guardFailed(err, op.Key, "锁定库存不足（条件更新未生效）")
		}
		after, err := reloadRow(tx, row.ID.Int64())
		if err != nil {
			return MutationResult{}, err
		}
		if op.LockID > 0 {
			if err := s.consumeLock(tx, op.LockID, op.Qty, op.Key, op.Actor); err != nil {
				return MutationResult{}, err
			}
		}
		led := buildLedger(after, "OUTBOUND", op.Source, op.Actor, ColLocked, ColLocked,
			before.Locked, op.Qty.Neg(), idemKey, op.Remark, op.SerialNo)
		if err := writeLedger(tx, led); err != nil {
			return MutationResult{}, err
		}
		if err := auditStock(tx, "deduct", row.ID.Int64(), op.Actor, op, before, after.State()); err != nil {
			return MutationResult{}, err
		}
		return MutationResult{Ledger: LedgerRef{ID: led.ID.Int64(), LedgerNo: led.LedgerNo}}, nil
	})
}

// consumeLock 核销锁定记录：部分核销递减 qty，清零转 CONSUMED（行锁内计算，
// ACTIVE 守卫落库；qty 不写 0——迁移 chk_inventory_locks_qty_positive 要求 qty>0）。
func (s *Service) consumeLock(tx *gorm.DB, lockID int64, qty Qty, key RowKey, actor Actor) error {
	lock, err := lockForUpdate(tx, lockID)
	if err != nil {
		return err
	}
	if lock == nil || lock.Status != "ACTIVE" ||
		lock.WarehouseID != key.WarehouseID || lock.BinID != key.BinID ||
		lock.SKUID != key.SKUID || lock.BatchID != key.BatchID {
		return response.NewError(ErrLockNotFound, map[string]any{
			"lock_id": lockID, "reason": "锁定记录不存在、已完结或与库存行不匹配",
		})
	}
	if lock.Qty.Sub(qty).IsNegative() {
		return response.NewError(ErrLockNotFound, map[string]any{
			"lock_id": lockID, "reason": "核销量超出锁存量",
			"lock_qty": lock.Qty.String(), "deduct_qty": qty.String(),
		})
	}
	remaining := lock.Qty.Sub(qty)
	if remaining.IsZero() {
		lock.Status = "CONSUMED"
	} else {
		lock.Qty = remaining
	}
	return updateLockAfterSplit(tx, lock, actor.ID)
}

// MoveBin 仓内移库：源行 available/total 同减，目标行 available/total 同增；
// 源/目标各写一条 MOVE 流水（幂等键仅记于首条，见 mutate 注释）。
func (s *Service) MoveBin(ctx context.Context, tx *gorm.DB, op MoveBinOp) (MutationResult, error) {
	if err := validateRowKey(op.From, true); err != nil {
		return MutationResult{}, err
	}
	if err := validateRowKey(op.To, true); err != nil {
		return MutationResult{}, err
	}
	if op.From.WarehouseID != op.To.WarehouseID || op.From.SKUID != op.To.SKUID || op.From.BatchID != op.To.BatchID {
		return MutationResult{}, response.NewError(ErrMoveCrossWarehouse, map[string]any{
			"reason": "仓内移库不允许跨仓库或变更 SKU/批次（跨仓属调拨域）",
		})
	}
	if op.From.BinID == op.To.BinID {
		return MutationResult{}, response.NewError(ErrMoveSameLocation, nil)
	}
	if err := validateQty(op.Qty, "qty"); err != nil {
		return MutationResult{}, err
	}
	if err := validateSource(op.Source); err != nil {
		return MutationResult{}, err
	}
	idemKey := strings.TrimSpace(op.IdempotencyKey)
	if err := validateIdempotencyKey(idemKey); err != nil {
		return MutationResult{}, err
	}
	return s.mutate(ctx, tx, idemKey, nil, func(tx *gorm.DB) (MutationResult, error) {
		// 固定加锁顺序防死锁（plan §8.3）：先无锁定位两行 id，再按 id 升序逐行加锁。
		fromID, err := locateRowID(tx, op.From)
		if err != nil {
			return MutationResult{}, err
		}
		if fromID == 0 {
			return MutationResult{}, response.NewError(ErrRecordNotFound, rowKeyDetails(op.From, "源库存行不存在"))
		}
		toID, err := locateRowID(tx, op.To)
		if err != nil {
			return MutationResult{}, err
		}
		// 按 id 升序加锁；目标行不存在时先锁源行再创建目标行（创建经唯一键兜底并发）。
		firstID, secondID := fromID, toID
		if toID > 0 && toID < fromID {
			firstID, secondID = toID, fromID
		}
		firstRow, err := lockRowByID(tx, firstID)
		if err != nil {
			return MutationResult{}, err
		}
		var secondRow *Inventory
		if secondID > 0 {
			secondRow, err = lockRowByID(tx, secondID)
			if err != nil {
				return MutationResult{}, err
			}
		}
		fromRow, toRow := firstRow, secondRow
		if fromRow == nil || fromRow.ID.Int64() != fromID {
			// firstID 是目标行（toID < fromID 分支）：secondRow 才是源行。
			fromRow, toRow = secondRow, firstRow
		}
		if toRow == nil {
			toRow, _, err = ensureRow(tx, op.To, op.Actor, StockState{})
			if err != nil {
				return MutationResult{}, err
			}
		}
		// 盘点冻结守卫（ErrRowCountFrozen）：源/目标行任一处于盘点冻结期即拒绝——
		// 两行 total 都会变化（business-flow §10.2"冻结范围"）。
		if err := rejectCountFrozen(tx, rowKeyOf(fromRow)); err != nil {
			return MutationResult{}, err
		}
		if err := rejectCountFrozen(tx, rowKeyOf(toRow)); err != nil {
			return MutationResult{}, err
		}
		before := fromRow.State()
		if before.Available.Sub(op.Qty).IsNegative() {
			return MutationResult{}, notEnoughErr(op.From, "可用库存不足", op.Qty, before.Available)
		}
		n, err := applyDelta(tx, fromRow.ID.Int64(), op.Actor.ID, op.Qty.Neg(),
			[]colDelta{{col: ColAvailable, delta: op.Qty.Neg()}},
			[]colGuard{{col: ColAvailable, min: op.Qty}})
		if err != nil || n == 0 {
			return MutationResult{}, guardFailed(err, op.From, "可用库存不足（条件更新未生效）")
		}
		n, err = applyDelta(tx, toRow.ID.Int64(), op.Actor.ID, op.Qty,
			[]colDelta{{col: ColAvailable, delta: op.Qty}}, nil)
		if err != nil || n == 0 {
			return MutationResult{}, guardFailed(err, op.To, "目标库存行更新未生效")
		}
		afterFrom, err := reloadRow(tx, fromID)
		if err != nil {
			return MutationResult{}, err
		}
		afterTo, err := reloadRow(tx, toRow.ID.Int64())
		if err != nil {
			return MutationResult{}, err
		}
		ledFrom := buildLedger(afterFrom, "MOVE", op.Source, op.Actor, ColAvailable, ColAvailable,
			before.Available, op.Qty.Neg(), idemKey, op.Remark, "")
		if err := writeLedger(tx, ledFrom); err != nil {
			return MutationResult{}, err
		}
		ledTo := buildLedger(afterTo, "MOVE", op.Source, op.Actor, ColAvailable, ColAvailable,
			afterTo.AvailableQty.Sub(op.Qty), op.Qty, "", op.Remark, "")
		if err := writeLedger(tx, ledTo); err != nil {
			return MutationResult{}, err
		}
		if err := auditStock(tx, "move-bin", fromID, op.Actor, op, before, afterFrom.State()); err != nil {
			return MutationResult{}, err
		}
		return MutationResult{Ledger: LedgerRef{ID: ledFrom.ID.Int64(), LedgerNo: ledFrom.LedgerNo}}, nil
	})
}

// InspectResult 待检处理：pending_inspect → available（合格）或 defective（不良，
// 质检不合格转入，inventory-rules §2）。
func (s *Service) InspectResult(ctx context.Context, tx *gorm.DB, op InspectResultOp) (MutationResult, error) {
	if err := validateRowKey(op.Key, false); err != nil {
		return MutationResult{}, err
	}
	if err := validateQty(op.Qty, "qty"); err != nil {
		return MutationResult{}, err
	}
	if err := validateSource(op.Source); err != nil {
		return MutationResult{}, err
	}
	idemKey := strings.TrimSpace(op.IdempotencyKey)
	if err := validateIdempotencyKey(idemKey); err != nil {
		return MutationResult{}, err
	}
	target := ColAvailable
	changeType := "INSPECT_PASS"
	if !op.Pass {
		target = ColDefective
		changeType = "INSPECT_DEFECTIVE"
	}
	return s.mutate(ctx, tx, idemKey, nil, func(tx *gorm.DB) (MutationResult, error) {
		row, err := locateRowForUpdate(tx, op.Key)
		if err != nil {
			return MutationResult{}, err
		}
		if row == nil {
			return MutationResult{}, response.NewError(ErrRecordNotFound, rowKeyDetails(op.Key, "库存行不存在"))
		}
		before := row.State()
		if before.PendingInspect.Sub(op.Qty).IsNegative() {
			return MutationResult{}, notEnoughErr(op.Key, "待检库存不足", op.Qty, before.PendingInspect)
		}
		n, err := applyDelta(tx, row.ID.Int64(), op.Actor.ID, 0,
			[]colDelta{{col: ColPendingInspect, delta: op.Qty.Neg()}, {col: target, delta: op.Qty}},
			[]colGuard{{col: ColPendingInspect, min: op.Qty}})
		if err != nil || n == 0 {
			return MutationResult{}, guardFailed(err, op.Key, "待检库存不足（条件更新未生效）")
		}
		after, err := reloadRow(tx, row.ID.Int64())
		if err != nil {
			return MutationResult{}, err
		}
		led := buildLedger(after, changeType, op.Source, op.Actor, ColPendingInspect, target,
			before.PendingInspect, op.Qty.Neg(), idemKey, op.Remark, op.SerialNo)
		if err := writeLedger(tx, led); err != nil {
			return MutationResult{}, err
		}
		if err := auditStock(tx, "inspect-result", row.ID.Int64(), op.Actor, op, before, after.State()); err != nil {
			return MutationResult{}, err
		}
		return MutationResult{Ledger: LedgerRef{ID: led.ID.Int64(), LedgerNo: led.LedgerNo}}, nil
	})
}

// Adjust 调整单执行：盘盈 available/total 同增（目标行可创建），盘亏/损耗/报废/其他
// available/total 同减（须有足额可用）；写 inventory_adjustments（EXECUTED）+ ADJUST 流水。
// 盘点差异执行（盘盈/盘亏）亦经本原语落账。
func (s *Service) Adjust(ctx context.Context, tx *gorm.DB, op AdjustOp) (MutationResult, error) {
	if err := validateRowKey(op.Key, true); err != nil {
		return MutationResult{}, err
	}
	if !adjustTypes[op.AdjustType] {
		return MutationResult{}, response.NewError(ErrAdjustTypeInvalid, map[string]any{"adjust_type": op.AdjustType})
	}
	if err := validateQty(op.Qty, "qty"); err != nil {
		return MutationResult{}, err
	}
	if strings.TrimSpace(op.Reason) == "" {
		return MutationResult{}, response.NewError(ErrAdjustReasonRequired, nil)
	}
	if err := validateSource(op.Source); err != nil {
		return MutationResult{}, err
	}
	idemKey := strings.TrimSpace(op.IdempotencyKey)
	if err := validateIdempotencyKey(idemKey); err != nil {
		return MutationResult{}, err
	}
	increase := adjustDirection(op.AdjustType) > 0
	return s.mutate(ctx, tx, idemKey, nil, func(tx *gorm.DB) (MutationResult, error) {
		var row *Inventory
		var err error
		if increase {
			if err := s.checkSKUAndBin(ctx, op.Key); err != nil {
				return MutationResult{}, err
			}
			row, _, err = ensureRow(tx, op.Key, op.Actor, StockState{})
		} else {
			row, err = locateRowForUpdate(tx, op.Key)
		}
		if err != nil {
			return MutationResult{}, err
		}
		if row == nil {
			return MutationResult{}, response.NewError(ErrRecordNotFound, rowKeyDetails(op.Key, "库存行不存在"))
		}
		// 盘点冻结守卫（ErrRowCountFrozen）：调整改变行 total，冻结期拒绝
		// （盘点差异执行在解冻之后，不受影响——CompleteCount 先 ReleaseLock 再 Adjust）。
		if err := rejectCountFrozen(tx, op.Key); err != nil {
			return MutationResult{}, err
		}
		before := row.State()
		delta := op.Qty
		guards := []colGuard(nil)
		if !increase {
			if before.Available.Sub(op.Qty).IsNegative() {
				return MutationResult{}, notEnoughErr(op.Key, "可用库存不足", op.Qty, before.Available)
			}
			delta = op.Qty.Neg()
			guards = []colGuard{{col: ColAvailable, min: op.Qty}}
		}
		n, err := applyDelta(tx, row.ID.Int64(), op.Actor.ID, delta,
			[]colDelta{{col: ColAvailable, delta: delta}}, guards)
		if err != nil || n == 0 {
			return MutationResult{}, guardFailed(err, op.Key, "可用库存不足（条件更新未生效）")
		}
		after, err := reloadRow(tx, row.ID.Int64())
		if err != nil {
			return MutationResult{}, err
		}
		// 调整单落账（plan §8.3 条 3）：ExistingAdjustmentID>0 时守卫更新既有
		// APPROVED 调整单为 EXECUTED（审批流复用）；否则执行即落账（M1 形态）。
		// 两条路径都经原语返回 adjustment_no（MutationResult.AdjustmentNo），
		// 调用方（盘点差异执行）不再按 qty+type 精确匹配回查。
		adjNo, err := writeAdjustment(tx, &InventoryAdjustment{
			WarehouseID: op.Key.WarehouseID, SKUID: op.Key.SKUID, BinID: op.Key.BinID,
			BatchID: op.Key.BatchID, AdjustType: op.AdjustType, Qty: op.Qty, Reason: op.Reason,
			ExecutedBy: op.Actor.ID, ExecutedAt: database.Now(),
			CreatedBy: op.Actor.ID, UpdatedBy: op.Actor.ID,
		}, op.ExistingAdjustmentID, op.Actor.ID)
		if err != nil {
			return MutationResult{}, err
		}
		led := buildLedger(after, "ADJUST", op.Source, op.Actor, ColAvailable, ColAvailable,
			before.Available, delta, idemKey, op.Remark, op.SerialNo)
		if err := writeLedger(tx, led); err != nil {
			return MutationResult{}, err
		}
		if err := auditStock(tx, "adjust", row.ID.Int64(), op.Actor, op, before, after.State()); err != nil {
			return MutationResult{}, err
		}
		return MutationResult{
			Ledger:       LedgerRef{ID: led.ID.Int64(), LedgerNo: led.LedgerNo},
			AdjustmentNo: adjNo,
		}, nil
	})
}

// rejectCountFrozen 盘点冻结守卫：行上存在 ACTIVE COUNT_FREEZE 时拒绝改变 total 的
// 库存变更（business-flow §10.2"冻结范围"、inventory-rules §4"盘点锁定"——冻结期
// 行总量恒定，盘点差异（实盘 - 冻结快照）才是真实的盘点盈亏；否则冻结期间锁定库存
// 发货扣减/入库上架造成的 total 变化会被差异调整二次计账）。只读 SELECT，调用方已持
// 库存行锁（locateRowForUpdate/ensureRow 之后），冻结检查与后续变更同事务串行化。
func rejectCountFrozen(tx *gorm.DB, key RowKey) error {
	frozen, err := hasActiveCountFreeze(tx, key)
	if err != nil {
		return err
	}
	if frozen {
		return response.NewError(ErrRowCountFrozen, rowKeyDetails(key, "库存行盘点冻结中（COUNT_FREEZE），解冻前不可变更 total"))
	}
	return nil
}

// rowKeyOf 由库存行取五维键（MoveBin 双行守卫用）。
func rowKeyOf(row *Inventory) RowKey {
	return RowKey{WarehouseID: row.WarehouseID, ZoneID: row.ZoneID, ShelfID: row.ShelfID,
		BinID: row.BinID, SKUID: row.SKUID, BatchID: row.BatchID}
}

// ---- 批次与序列号 ----

// EnsureBatch 建立或返回既有批次（唯一键 sku_id+batch_no，天然幂等、首写为准不覆盖；
// M2 入库域采集批次时调用——批次表写入口同样仅限本 Service，plan §4.2 判据 3）。
func (s *Service) EnsureBatch(ctx context.Context, tx *gorm.DB, op BatchOp) (batchID int64, created bool, err error) {
	if op.SKUID <= 0 {
		return 0, false, errParam("sku_id", "必须为正整数")
	}
	if strings.TrimSpace(op.BatchNo) == "" {
		return 0, false, errParam("batch_no", "不能为空")
	}
	if op.CostPrice.IsNegative() {
		return 0, false, errParam("cost_price", "不能为负数")
	}
	err = withTx(ctx, s.db, tx, func(tx *gorm.DB) error {
		if err := s.checkSKU(ctx, op.SKUID); err != nil {
			return err
		}
		existing, ferr := findBatchByNo(tx, op.SKUID, op.BatchNo)
		if ferr != nil {
			return ferr
		}
		if existing != nil {
			batchID = existing.ID.Int64()
			return nil
		}
		id, ferr := insertBatch(tx, &Batch{
			SKUID: op.SKUID, BatchNo: op.BatchNo, SupplierID: op.SupplierID,
			ProductionDate: op.ProductionDate, InboundDate: op.InboundDate, ExpiryDate: op.ExpiryDate,
			CostPrice: op.CostPrice, Remark: op.Remark, CreatedBy: op.Actor.ID, UpdatedBy: op.Actor.ID,
		})
		if ferr != nil && pgUniqueViolation(ferr, "uk_batches_sku_batch_no") {
			// 并发首建竞态：回读既有批次。
			existing, ferr = findBatchByNo(tx, op.SKUID, op.BatchNo)
			if ferr == nil && existing != nil {
				batchID = existing.ID.Int64()
				return nil
			}
		}
		if ferr != nil {
			return ferr
		}
		batchID, created = id, true
		return middleware.Audit(tx, middleware.AuditEntry{
			Module: "inventory", ObjectType: "batch", ObjectID: id,
			OperatorID: op.Actor.ID, OperatorName: op.Actor.Name,
			RequestID: op.Actor.RequestID, IP: op.Actor.IP, UserAgent: op.Actor.UserAgent,
			Method: op.Actor.Method, Path: op.Actor.Path,
			Success: true, Request: op, After: op,
		})
	})
	return batchID, created, err
}

// SerialEvent 记录序列号生命周期状态变化：新建（如入库采集）或状态迁移，
// 每次变化更新 last_source_type/last_source_no/last_event_at 与位置；
// 完整历史经 operation_logs（同事务写入）与 M2 业务单据追溯。
// 序列号全局唯一（serial_no）；存量行 SKU 归属不一致即拒绝（ErrSerialSKUMismatch）。
// 注意：本原语只维护序列号台账，不改变库存数量——数量变化必须与库存原语
// （Putaway/Deduct/...）由调用方组合在同一事务（tx 非 nil）内完成。
func (s *Service) SerialEvent(ctx context.Context, tx *gorm.DB, op SerialOp) (serialID int64, created bool, err error) {
	if strings.TrimSpace(op.SerialNo) == "" {
		return 0, false, errParam("serial_no", "不能为空")
	}
	if op.SKUID <= 0 {
		return 0, false, errParam("sku_id", "必须为正整数")
	}
	if !serialStatuses[op.Status] {
		return 0, false, response.NewError(ErrSerialStatusInvalid, map[string]any{"status": op.Status})
	}
	if err := validateSource(op.Source); err != nil {
		return 0, false, err
	}
	err = withTx(ctx, s.db, tx, func(tx *gorm.DB) error {
		if err := s.checkSKU(ctx, op.SKUID); err != nil {
			return err
		}
		row, ferr := serialForUpdate(tx, op.SerialNo)
		if ferr != nil {
			return ferr
		}
		now := database.Now()
		if row == nil {
			row = &SerialNumber{
				SerialNo: op.SerialNo, SKUID: op.SKUID, BatchID: op.BatchID,
				WarehouseID: op.WarehouseID, BinID: op.BinID, Status: op.Status,
				LastSourceType: op.Source.Type, LastSourceNo: op.Source.No, LastEventAt: now,
				CreatedBy: op.Actor.ID, UpdatedBy: op.Actor.ID,
			}
			if ferr := insertSerial(tx, row); ferr != nil {
				if !pgUniqueViolation(ferr, "uk_serial_numbers_serial_no") {
					return ferr
				}
				// 并发首建竞态：重读后按更新路径收敛。
				if row, ferr = serialForUpdate(tx, op.SerialNo); ferr != nil {
					return ferr
				}
				if row == nil {
					return response.NewError(response.CodeConflict, map[string]any{"reason": "序列号并发创建失败"})
				}
			} else {
				serialID, created = row.ID.Int64(), true
				return auditSerial(tx, op, *row, serialID)
			}
		}
		if row.SKUID != op.SKUID {
			return response.NewError(ErrSerialSKUMismatch, map[string]any{
				"serial_no": op.SerialNo, "existing_sku_id": row.SKUID, "request_sku_id": op.SKUID,
			})
		}
		row.BatchID = op.BatchID
		row.WarehouseID = op.WarehouseID
		row.BinID = op.BinID
		row.Status = op.Status
		row.LastSourceType = op.Source.Type
		row.LastSourceNo = op.Source.No
		row.LastEventAt = now
		row.UpdatedBy = op.Actor.ID
		if ferr := updateSerial(tx, row); ferr != nil {
			return ferr
		}
		serialID = row.ID.Int64()
		return auditSerial(tx, op, *row, serialID)
	})
	return serialID, created, err
}

// auditSerial 序列号状态变化审计（inventory-rules §8.3）。
func auditSerial(tx *gorm.DB, op SerialOp, row SerialNumber, serialID int64) error {
	return middleware.Audit(tx, middleware.AuditEntry{
		Module: "inventory", ObjectType: "serial_number", ObjectID: serialID,
		OperatorID: op.Actor.ID, OperatorName: op.Actor.Name,
		RequestID: op.Actor.RequestID, IP: op.Actor.IP, UserAgent: op.Actor.UserAgent,
		Method: op.Actor.Method, Path: op.Actor.Path,
		Success: true, Request: op,
		After: map[string]any{
			"serial_no": row.SerialNo, "sku_id": row.SKUID, "batch_id": row.BatchID,
			"warehouse_id": row.WarehouseID, "bin_id": row.BinID, "status": row.Status,
			"source_type": row.LastSourceType, "source_no": row.LastSourceNo, "event_at": row.LastEventAt,
		},
	})
}
