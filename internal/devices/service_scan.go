package devices

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
)

// 统一条码解析（scanner.md §5、backend-m3-plan §8.3——只识别不执行业务）。
//
// 匹配器管线（顺序冻结）：
//  0. SFQR 协议码（qr-code.md §6：前缀 "SFQR|" 即锁型 → parseSfqr——格式/版本/类型
//     错误显式返回 SFQR_*，失败不落回；载荷合法按 sku_code 查 SKU，未命中/软删/
//     停用 → SKU_NOT_FOUND。优先级最高：同一字符串即使被注册为条码/单号也走本分支）
//  1. 单据号（docnum frozenRules 业务前缀 15 值分派 4 域 DocFinder——docPrefixOwners
//     单一映射表；LED/ADJ/IMP/EXP/PT 不可扫，不入映射表）
//  2. SKU 条码（barcodes 唯一索引）
//  3. 库位码（多仓命中返回列表）
//  4. 序列号（唯一命中）
//  5. 批次码（跨 SKU 命中返回列表）
//  6. 全未命中 → UNKNOWN_BARCODE（scanner §6.1 字面）；命中类型但对象不存在/停用
//     → SKU_NOT_FOUND/BIN_NOT_FOUND/ORDER_NOT_FOUND/TASK_NOT_FOUND。
//
// 扫码审计（devices.md §13.2）：命中或失败均写 scan_logs；写失败记 error 日志并降级
// 放行（resolve 为只读识别非业务事务，plan §8.3）。设备端调用携带设备上下文
// （device_id/device_code），Web HID 以用户 JWT 调用（device 列 NULL，user 归因）。
//
// 去重窗口（scanner.md §6.6，DedupWindow）：仅作 duplicate 响应注记，不抑制识别；
// scan_logs 照常落库（append-only 不丢事件）。

// ResolveItem 单个识别对象（scanner.md §5.3 响应形态 {type,id,code,name}；多命中时
// items 携带全量列表由前端选择——plan §8.3 歧义消解）。
type ResolveItem struct {
	Type        string      `json:"type"`
	ID          database.ID `json:"id"`
	Code        string      `json:"code"`
	Name        string      `json:"name"`
	DocKind     string      `json:"doc_kind,omitempty"`
	WarehouseID int64       `json:"warehouse_id,omitempty"`
	Status      string      `json:"status,omitempty"`
}

// ResolveResult resolve 响应。
type ResolveResult struct {
	Type        string        `json:"type"`
	ID          database.ID   `json:"id"` // 多命中未定位时为 0（000013 DDL 注同口径）
	Code        string        `json:"code"`
	Name        string        `json:"name"`
	DocKind     string        `json:"doc_kind,omitempty"`
	WarehouseID int64         `json:"warehouse_id,omitempty"`
	Status      string        `json:"status,omitempty"`
	Items       []ResolveItem `json:"items,omitempty"` // 仅多命中携带
	Duplicate   bool          `json:"duplicate"`       // §6.6 去重窗口标记（不抑制识别）
}

// ResolveInput resolve 请求（plan §8.3：{code}；symbology/page 为可选上下文，
// 落 scan_logs 对应列——000013 DDL 注）。
type ResolveInput struct {
	Code      string `json:"code"`
	Symbology string `json:"symbology"`
	Page      string `json:"page"`
}

// ResolveIdentity 调用方归因（handler 组装：设备端经 DeviceContext，用户端经
// auth.CurrentUser——scan_logs 设备/操作者双轨归因，plan §8.3）。
type ResolveIdentity struct {
	UserID      int64
	Username    string
	IP          string
	WarehouseID int64 // 作业仓（设备绑定仓；用户端 0=未定位）
	DeviceID    int64 // 0=Web HID 场景（scan_logs.device_id 落 NULL）
	DeviceCode  string
}

// maxResolveCodeLen 条码长度上限（scan_logs.raw_code varchar(255)）。
const maxResolveCodeLen = 255

// Resolve 统一解析入口：管线识别 → 去重注记 → scan_logs 审计（降级放行）。
// 业务错误（UNKNOWN_BARCODE 等）同样先落审计再返回。
func (s *Service) Resolve(ctx context.Context, ident ResolveIdentity, in ResolveInput) (*ResolveResult, error) {
	in.Code = strings.TrimSpace(in.Code)
	if in.Code == "" || len(in.Code) > maxResolveCodeLen {
		return nil, response.NewError(ErrCodeInvalid, ginH{"limit": maxResolveCodeLen})
	}
	in.Symbology = strings.TrimSpace(in.Symbology)
	in.Page = strings.TrimSpace(in.Page)

	res, resErr := s.resolvePipeline(ctx, in.Code)

	// 去重窗口（§6.6）：同码 + 同页面 + 同来源（设备或用户）极短窗口内标记重复事件；
	// 识别结果不受影响（只识别不执行——业务幂等在业务 API，plan §8.3 口径）。
	if res != nil {
		source := ident.DeviceCode
		if source == "" {
			source = ident.Username
		}
		res.Duplicate = !s.dedup.Mark(DedupKey(source, in.Page, in.Code, ""))
	}

	// scan_logs 审计：命中或失败均落库；写失败降级放行（plan §8.3，任务偏离记"方案偏离6"）。
	s.writeScanLog(ctx, ident, in, res, resErr)

	return res, resErr
}

// resolvePipeline 匹配器管线（顺序冻结，plan §8.3 + qr-code.md §6 第 0 段）。
func (s *Service) resolvePipeline(ctx context.Context, code string) (*ResolveResult, error) {
	// 0. SFQR 协议码（qr-code.md §6）：前缀命中即锁型——格式/版本/类型错误显式返回
	// SFQR_*，禁止把残缺 SFQR 当普通条码静默落回后续匹配器（约束 2）；载荷合法按
	// sku_code 查 SKU，未命中/软删/停用 → SKU_NOT_FOUND（与条码分支同口径）。
	// 优先级最高：同一字符串即使被注册为 SKU 条码/单号前缀也走本分支（§3.2
	// 命名空间保留的运行时保证）。scan_logs 审计照常（错误也落，errorCodeOf 通用）。
	if IsSfqrCode(code) {
		if s.sfqrSkus == nil {
			return nil, response.NewError(ErrReaderRequired, ginH{"matcher": "sfqr"})
		}
		skuCode, err := parseSfqr(code)
		if err != nil {
			return nil, err
		}
		hit, found, err := s.sfqrSkus.FindBySkuCode(ctx, skuCode)
		if err != nil {
			return nil, err
		}
		if !found || (hit.Status != "" && hit.Status != "ENABLED") {
			return nil, response.NewError(ErrSKUNotFound, ginH{"code": skuCode})
		}
		return singleResult(hitToItem("sku", hit)), nil
	}

	// 1. 单据号：前缀命中冻结映射即锁定类型——对象不存在也按 *_NOT_FOUND 返回
	// （不落回后续匹配器：单号前缀是类型信号，plan §8.3 条 1/6）。
	if prefix, ok := docPrefixOf(code); ok {
		finder := s.docFinderFor(prefix)
		if finder == nil {
			return nil, response.NewError(ErrReaderRequired, ginH{"prefix": prefix})
		}
		hit, found, err := finder.FindByNo(ctx, code)
		if err != nil {
			return nil, err
		}
		if !found {
			if taskDocKinds[prefix] {
				return nil, response.NewError(ErrTaskNotFound, ginH{"code": code})
			}
			return nil, response.NewError(ErrOrderNotFound, ginH{"code": code})
		}
		item := hitToItem("doc", hit)
		item.DocKind = prefix
		return singleResult(item), nil
	}

	// 2. SKU 条码：barcodes 唯一索引精确命中。
	if s.skuBarcodes == nil {
		return nil, response.NewError(ErrReaderRequired, ginH{"matcher": "sku"})
	}
	hit, found, err := s.skuBarcodes.FindByBarcode(ctx, code)
	if err != nil {
		return nil, err
	}
	if found {
		if hit.Status != "" && hit.Status != "ENABLED" {
			// 命中类型但对象停用 → SKU_NOT_FOUND（plan §8.3 条 6）。
			return nil, response.NewError(ErrSKUNotFound, ginH{"code": code})
		}
		return singleResult(hitToItem("sku", hit)), nil
	}

	// 3. 库位码：多仓同码返回列表（歧义消解——plan §8.3）。
	if s.bins == nil {
		return nil, response.NewError(ErrReaderRequired, ginH{"matcher": "bin"})
	}
	hits, err := s.bins.FindByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if len(hits) > 0 {
		if res, ok := multiOrSingle("bin", hits); ok {
			return res, nil
		}
		// 命中但全部停用 → BIN_NOT_FOUND。
		return nil, response.NewError(ErrBinNotFound, ginH{"code": code})
	}

	// 4. 序列号：全局唯一命中。
	if s.serials == nil {
		return nil, response.NewError(ErrReaderRequired, ginH{"matcher": "serial"})
	}
	hit, found, err = s.serials.FindSerial(ctx, code)
	if err != nil {
		return nil, err
	}
	if found {
		return singleResult(hitToItem("serial", hit)), nil
	}

	// 5. 批次码：跨 SKU 命中返回列表。
	if s.batches == nil {
		return nil, response.NewError(ErrReaderRequired, ginH{"matcher": "batch"})
	}
	hits, err = s.batches.FindBatch(ctx, code)
	if err != nil {
		return nil, err
	}
	if len(hits) > 0 {
		if res, ok := multiOrSingle("batch", hits); ok {
			return res, nil
		}
	}

	// 6. 全未命中。
	return nil, response.NewError(ErrUnknownBarcode, ginH{"code": code})
}

// docPrefixOf 解析单号前缀（{prefix}-{YYYYMMDD}-{seq} 格式首段；须命中冻结映射才
// 视为单据类型信号——LED/ADJ/IMP/EXP/PT 等不在映射表，返回 false 落后续匹配器）。
func docPrefixOf(code string) (string, bool) {
	i := strings.IndexByte(code, '-')
	if i <= 0 {
		return "", false
	}
	prefix := code[:i]
	if _, ok := docPrefixOwners[prefix]; ok {
		return prefix, true
	}
	return "", false
}

// docFinderFor 按前缀取分派 DocFinder（plan §12.2 前缀→实现映射）。
func (s *Service) docFinderFor(prefix string) DocFinder {
	switch docPrefixOwners[prefix] {
	case "purchase":
		return s.purchaseDocs
	case "sales":
		return s.salesDocs
	case "stockops":
		return s.stockopsDocs
	case "returns":
		return s.returnsDocs
	}
	return nil
}

// hitToItem 匹配器命中 → 响应对象。
func hitToItem(typ string, h Hit) ResolveItem {
	return ResolveItem{
		Type:        typ,
		ID:          database.ID(h.ID),
		Code:        h.Code,
		Name:        h.Name,
		WarehouseID: h.WarehouseID,
		Status:      h.Status,
		DocKind:     h.DocKind,
	}
}

// singleResult 单命中响应（items 省略——多命中才携带，前端契约区分单/多命中）。
func singleResult(item ResolveItem) *ResolveResult {
	return &ResolveResult{
		Type:        item.Type,
		ID:          item.ID,
		Code:        item.Code,
		Name:        item.Name,
		DocKind:     item.DocKind,
		WarehouseID: item.WarehouseID,
		Status:      item.Status,
	}
}

// multiOrSingle 多命中响应组装：全部停用返回 ok=false（调用方转 *_NOT_FOUND）；
// 单命中退化为普通结果；多命中 items 携带全量、顶层 ID=0（多命中未定位——
// 000013 DDL 注 resolve_id 口径）。
func multiOrSingle(typ string, hits []Hit) (*ResolveResult, bool) {
	live := make([]ResolveItem, 0, len(hits))
	for _, h := range hits {
		if h.Status == "" || h.Status == "ENABLED" {
			live = append(live, hitToItem(typ, h))
		}
	}
	if len(live) == 0 {
		return nil, false
	}
	if len(live) == 1 {
		return singleResult(live[0]), true
	}
	first := live[0]
	return &ResolveResult{
		Type:        typ,
		ID:          0,
		Code:        first.Code,
		Name:        first.Name,
		WarehouseID: first.WarehouseID,
		Items:       live,
	}, true
}

// errorCodeOf 提取业务错误码字符串（scan_logs.error_code 列）：response.Error 的
// Error() 形态稳定为 "CODE: message"（internal/response/errors.go），取前缀段；
// 非业务错误归一 COMMON_INTERNAL_ERROR。
func errorCodeOf(err error) string {
	var e *response.Error
	if errors.As(err, &e) {
		s := e.Error()
		if i := strings.Index(s, ": "); i > 0 {
			return s[:i]
		}
		return s
	}
	return "COMMON_INTERNAL_ERROR"
}

// writeScanLog scan_logs 审计落库：命中或失败均写；写失败记 error 日志并降级放行
// （plan §8.3）。设备路径同步挂接 devices.last_scan_at（同为降级路径，失败不影响识别）。
func (s *Service) writeScanLog(ctx context.Context, ident ResolveIdentity, in ResolveInput, res *ResolveResult, resErr error) {
	now := s.nowFn()
	l := &ScanLog{
		UserID:      ident.UserID,
		Username:    truncate(ident.Username, 64),
		IP:          truncate(ident.IP, 64),
		WarehouseID: ident.WarehouseID,
		RawCode:     truncate(in.Code, maxResolveCodeLen),
		Symbology:   truncate(in.Symbology, 32),
		Page:        truncate(in.Page, 128),
		Success:     resErr == nil,
		CreatedAt:   database.JSONTime{Time: now},
	}
	if ident.DeviceID > 0 {
		did := ident.DeviceID
		dc := ident.DeviceCode
		l.DeviceID = &did
		l.DeviceCode = &dc
	}
	if res != nil {
		l.ResolveType = res.Type
		l.ResolveID = int64(res.ID)
		l.ResolveCode = truncate(res.Code, 128)
	}
	if resErr != nil {
		l.ErrorCode = errorCodeOf(resErr)
	}
	if err := s.repo.InsertScanLog(s.repo.DB().WithContext(ctx), l); err != nil {
		if s.logger != nil {
			s.logger.Error("scan_logs 写入失败（resolve 降级放行）",
				zap.String("raw_code", l.RawCode),
				zap.Int64("device_id", ident.DeviceID),
				zap.Int64("user_id", ident.UserID),
				zap.Error(err))
		}
		return
	}
	if ident.DeviceID > 0 {
		// 设备路径挂接 last_scan_at（devices.md §7.1 最后扫码时间列）。
		if err := s.repo.UpdateDeviceLastScan(ctx, ident.DeviceID, now); err != nil && s.logger != nil {
			s.logger.Error("devices.last_scan_at 更新失败（降级）", zap.Int64("device_id", ident.DeviceID), zap.Error(err))
		}
	}
}

// ListScanLogs 扫码日志分页查询（devices:scanlog:list——管理端 devices.md §7.1
// 查看扫码日志；数据权限按仓库范围过滤）。
func (s *Service) ListScanLogs(ctx context.Context, f ScanLogFilter) ([]*ScanLog, int64, error) {
	return s.repo.ListScanLogs(ctx, f)
}

// ListDeviceLogs 设备日志分页查询（GET /api/devices/{id}/logs——plan §8.1）。
func (s *Service) ListDeviceLogs(ctx context.Context, f DeviceLogFilter, scope WarehouseScope) ([]*DeviceLog, int64, error) {
	// 数据权限（permission.md §4）：设备日志沿设备仓库归属做可见性校验（fail-closed）。
	if _, err := s.loadVisibleDevice(ctx, f.DeviceID, scope); err != nil {
		return nil, 0, err
	}
	return s.repo.ListDeviceLogs(ctx, f)
}

// timeNow 时钟暴露（测试注入用；生产恒 wall clock）。
func (s *Service) timeNow() time.Time { return s.nowFn() }
