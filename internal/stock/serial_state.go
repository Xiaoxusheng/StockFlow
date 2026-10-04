package stock

// SerialState 序列号台账当前状态只读投影（M2 修复轮新增；叶子契约包值类型——
// 出库类业务（销售发货 / 采购退货出库）逐件校验台账的数据源，inventory-rules §8.2
// "出库必须逐个序列号操作"）。
//
// 只读投影：不携带 last_source_* 追溯指针（追溯走各域 Trace 投影），仅承载
// 出库校验所需六字段。实现方 inventory.Service.SerialStates（事务内 SELECT），
// 消费方经各自 StockGateway 窄接口引用本类型（plan §2.3 判据 7：跨域读不走
// 旁路 SELECT）。
type SerialState struct {
	SerialNo    string
	SKUID       int64
	BatchID     int64
	WarehouseID int64
	BinID       int64
	Status      string
}
