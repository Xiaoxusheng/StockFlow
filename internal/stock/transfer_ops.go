package stock

// 跨仓调拨原语入参值类型（backend-m2-plan §8.1；自 internal/inventory/transfer.go
// 原样搬迁，别名承接路径与九原语一致——stockops 消费接口签名只引用本包值类型，
// plan §3"域包消费 inventory 原语的唯一合法 import 面"）。

// TransferOutOp 调拨出库（源仓）入参。语义 = Deduct 的 TRANSFER_OUT 口径：
// 携带 APPROVED 阶段的调拨预占锁（LockID>0 时核销锁定记录）。
type TransferOutOp struct {
	Key            RowKey `json:"key"`
	Qty            Qty    `json:"qty"`
	LockID         int64  `json:"lock_id"` // 调拨预占锁（M2 调拨域恒携带；0=不核销锁定记录）
	SerialNo       string `json:"serial_no"`
	Source         Source `json:"source"`
	Actor          Actor  `json:"actor"`
	IdempotencyKey string `json:"idempotency_key"`
	Remark         string `json:"remark"`
}

// TransferInOp 调拨到货（目标仓）入参。行可创建（needZoneShelf=true：
// 目标 zone/shelf 必填，inventory-rules §3 五维）。
type TransferInOp struct {
	Key            RowKey `json:"key"`
	Qty            Qty    `json:"qty"`
	SerialNo       string `json:"serial_no"`
	Source         Source `json:"source"`
	Actor          Actor  `json:"actor"`
	IdempotencyKey string `json:"idempotency_key"`
	Remark         string `json:"remark"`
}
