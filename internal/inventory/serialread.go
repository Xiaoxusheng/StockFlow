package inventory

import (
	"context"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/stock"
)

// serialread 序列号台账跨域只读查询（M2 修复轮新增文件——既有文件冻结不触碰，
// plan §2.3 判据 1/7：消费方经 StockGateway 窄接口 + router 注入消费本方法，
// 禁止域包旁路 SELECT serial_numbers）。

// SerialStates 按序列号集合读取台账当前状态（inventory-rules §8：出库必须
// 逐序列号校验"存在 + 属该 SKU + IN_STOCK + 位于指定库位"后方可 SerialEvent
// 核销）。必须在调用方业务事务句柄 tx 上执行——同事务内"读台账 → 校验 →
// SerialEvent"原子，否则读到的状态在核销前可被并发出库改变（SerialEvent 的
// serialForUpdate 行锁以同一 tx 串行化）。
//
// 返回按传入顺序外的任意顺序排列（调用方按 SerialNo 索引）；不存在或 skuID>0
// 且 SKU 不匹配的序列号不出现在结果中——调用方以缺失判定 fail-closed。
func (s *Service) SerialStates(ctx context.Context, tx *gorm.DB, skuID int64, serials []string) ([]stock.SerialState, error) {
	if len(serials) == 0 {
		return nil, nil
	}
	db := s.db
	if tx != nil {
		db = tx
	}
	q := db.WithContext(ctx).Model(&SerialNumber{}).
		Select("serial_no, sku_id, batch_id, warehouse_id, bin_id, status").
		Where("serial_no IN ?", serials)
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
