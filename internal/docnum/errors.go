package docnum

import (
	"net/http"

	"github.com/stockflow/server/internal/response"
)

// docnum 平台错误码（architecture.md §2 模块命名空间 DOCNUM_*，统一经 internal/response
// 注册与输出；编号引擎为单据创建同事务内的平台设施，规则/事务误用均为编程错误）。
var (
	// ErrRuleInvalid 编号规则非法（前缀/位宽/重置周期超出冻结值域；规则为代码内
	// 冻结注册表值——backend-m2-plan §4.1，触发即编程错误）。
	ErrRuleInvalid = response.Register("DOCNUM_RULE_INVALID", "单据编号规则非法", http.StatusInternalServerError)
	// ErrTxRequired 取号未在业务事务内（plan §4.1：流水发放必须与单据创建同事务）。
	ErrTxRequired = response.Register("DOCNUM_TX_REQUIRED", "单据编号发放必须在业务事务内进行", http.StatusInternalServerError)
	// ErrNumberConflict 重试耗尽仍无法取得可用单号（历史遗留同格式单号碰撞）。
	ErrNumberConflict = response.Register("DOCNUM_NUMBER_CONFLICT", "单据编号生成冲突，请重试", http.StatusConflict)
)
