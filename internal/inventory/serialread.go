package inventory

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// serialread 序列号台账跨域只读查询（M2 修复轮新增文件——既有文件冻结不触碰，
// plan §2.3 判据 1/7：消费方经 StockGateway 窄接口 + router 注入消费本方法，
// 禁止域包旁路 SELECT serial_numbers）。

// SerialStates 按序列号集合读取台账当前状态（inventory-rules §8：出库必须
// 逐序列号校验"存在 + 属该 SKU + IN_STOCK + 位于指定库位"后方可 SerialEvent
// 核销）。必须在调用方业务事务句柄 tx 上执行——同事务内"读台账 → 校验 →
// SerialEvent"原子（tx 为 nil 直接拒绝，杜绝落到根连接池退化为无锁读的静默竞态）。
//
// 渗透修复：查询加 FOR UPDATE 行锁（持有至调用方事务提交）。原普通 SELECT 无锁，
// 并发两订单可同时读到 IN_STOCK 并双双通过校验核销同一序列号（一物两卖）；加锁后
// 后到事务在本查询处阻塞至先到事务提交，重读（READ COMMITTED）看到核销后状态，
// 校验 fail-closed 拒绝；写侧 updateSerial 另有 WHERE status=? CAS 守卫兜底。
// 调用方确认（2026-10）：仅 returns 域经 router/returnsStockGateway 在业务事务内消费。
//
// 返回按传入顺序外的任意顺序排列（调用方按 SerialNo 索引）；不存在或 skuID>0
// 且 SKU 不匹配的序列号不出现在结果中——调用方以缺失判定 fail-closed。
func (s *Service) SerialStates(ctx context.Context, tx *gorm.DB, skuID int64, serials []string) ([]stock.SerialState, error) {
	if len(serials) == 0 {
		return nil, nil
	}
	if tx == nil {
		return nil, response.NewError(response.CodeInternalError, map[string]any{
			"reason": "SerialStates 必须在业务事务内执行（FOR UPDATE 行锁以调用方事务持有至提交）",
		})
	}
	q := tx.WithContext(ctx).Model(&SerialNumber{}).
		Select("serial_no, sku_id, batch_id, warehouse_id, bin_id, status").
		Where("serial_no IN ?", serials).
		Clauses(clause.Locking{Strength: "UPDATE"}) // FOR UPDATE：与同事务 SerialEvent 核销原子
	if skuID > 0 {
		q = q.Where("sku_id = ?", skuID)
	}
	var rows []SerialNumber
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]stock.SerialState, 0, len(rows))
	for _, r := range rows {
		out = append(out, stock.SerialState{
			SerialNo: r.SerialNo, SKUID: r.SKUID, BatchID: r.BatchID,
			WarehouseID: r.WarehouseID, BinID: r.BinID, Status: r.Status,
		})
	}
	return out, nil
}
