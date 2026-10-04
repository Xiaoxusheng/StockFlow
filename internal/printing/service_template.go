package printing

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// 打印模板业务（printing.md §2 模板体系：类型/页面尺寸/字段绑定/条码/二维码/状态；
// plan §7.1：CRUD + copy + status，fields 绑定校验为预设键子集，symbology 仅标签类
// 必填，模板不存 HTML）。

// TemplateSaveInput 模板创建/修改入参（fields 为绑定键数组——存储与展示文案由
// 后端预设注册表派生，前端对齐轮回对 PrintTemplateSavePayload.fields 形态）。
type TemplateSaveInput struct {
	Name             string   `json:"name"`
	ObjectType       string   `json:"object_type"`
	Paper            string   `json:"paper"`
	BarcodeSymbology string   `json:"barcode_symbology"`
	QRCodeEnabled    bool     `json:"qrcode_enabled"`
	Fields           []string `json:"fields"`
	HeaderText       string   `json:"header_text"`
	Remark           string   `json:"remark"`
}

// nameMaxLen / headerTextMaxLen 列宽上限（DDL varchar(128)/varchar(255)；超限
// INSERT 必败，提前 4xx 并带 details——api.md §4）。
const (
	nameMaxLen       = 128
	headerTextMaxLen = 255
)

// validateTemplateInput 模板入参校验（创建与修改共用）：
//   - name 非空 ≤128；objectType 九值域；paper 五值域；
//   - symbology：标签类必填、单据类必须为空（plan §7.1"symbology 仅标签类必填" +
//     前端口径"单据类为单据二维码，无需码制"——单值约束避免双码制歧义）；
//   - fields ⊆ object_type 预设键子集（plan §7.1，未知键 4xx PRINT_FIELDS_INVALID）。
func validateTemplateInput(in TemplateSaveInput) (fields map[string]string, symbology *string, err error) {
	if in.Name == "" {
		return nil, nil, paramError("name", "不能为空")
	}
	if len(in.Name) > nameMaxLen {
		return nil, nil, paramError("name", "长度不能超过 "+fmt.Sprint(nameMaxLen))
	}
	if !ObjectTypeValid(in.ObjectType) {
		return nil, nil, response.NewError(ErrObjectTypeInvalid, map[string]any{"object_type": in.ObjectType})
	}
	if !PaperValid(in.Paper) {
		return nil, nil, response.NewError(ErrPaperInvalid, map[string]any{"paper": in.Paper})
	}
	if IsLabelObjectType(in.ObjectType) {
		if in.BarcodeSymbology == "" {
			return nil, nil, response.NewError(ErrSymbologyInvalid, map[string]any{
				"object_type": in.ObjectType, "reason": "标签类模板必须选择条码码制",
			})
		}
	} else if in.BarcodeSymbology != "" {
		return nil, nil, response.NewError(ErrSymbologyInvalid, map[string]any{
			"object_type": in.ObjectType, "reason": "单据类模板不使用条码码制（单据二维码经 qrcode_enabled）",
		})
	}
	if in.BarcodeSymbology != "" && !TemplateSymbologyValid(in.BarcodeSymbology) {
		return nil, nil, response.NewError(ErrSymbologyInvalid, map[string]any{"barcode_symbology": in.BarcodeSymbology})
	}
	if len(in.HeaderText) > headerTextMaxLen {
		return nil, nil, paramError("header_text", "长度不能超过 "+fmt.Sprint(headerTextMaxLen))
	}
	fields, err = validateFields(in.ObjectType, in.Fields)
	if err != nil {
		return nil, nil, err
	}
	if in.BarcodeSymbology != "" {
		s := in.BarcodeSymbology
		symbology = &s
	}
	return fields, symbology, nil
}

// applyInput 入参 → 模型可变字段。
func applyInput(t *PrintTemplate, in TemplateSaveInput, fields map[string]string, symbology *string, by int64) {
	t.Name = in.Name
	t.ObjectType = in.ObjectType
	t.Paper = in.Paper
	t.BarcodeSymbology = symbology
	t.QRCodeEnabled = in.QRCodeEnabled
	if len(fields) == 0 {
		t.Fields = jsonb("{}")
	} else {
		t.Fields = marshalJSONB(fields)
	}
	t.HeaderText = in.HeaderText
	t.Remark = in.Remark
	t.UpdatedBy = by
}

// CreateTemplate 新建模板（创建恒 ENABLED——前端先行契约口径，启停走专用端点；
// 审计 action=create，plan §13.8）。
func (s *Service) CreateTemplate(ctx context.Context, actor Actor, in TemplateSaveInput) (*TemplateView, error) {
	fields, symbology, err := validateTemplateInput(in)
	if err != nil {
		return nil, err
	}
	t := &PrintTemplate{Status: StatusEnabled, CreatedBy: actor.UserID}
	applyInput(t, in, fields, symbology, actor.UserID)
	err = withTx(ctx, s.repo, func(tx *gorm.DB) error {
		if err := s.repo.InsertTemplate(tx, t); err != nil {
			return err
		}
		return middleware.Audit(tx, auditWithSnapshots(
			actor.auditEntry("print_template", t.ID.Int64(), "create"), in, nil, templateView(t)))
	})
	if err != nil {
		return nil, err
	}
	v := templateView(t)
	return &v, nil
}

// UpdateTemplate 修改模板（全量可变列；审计 action=update 带 before/after——
// architecture.md §8.1 关键更新必带快照）。
func (s *Service) UpdateTemplate(ctx context.Context, actor Actor, id int64, in TemplateSaveInput) (*TemplateView, error) {
	fields, symbology, err := validateTemplateInput(in)
	if err != nil {
		return nil, err
	}
	before, err := s.loadTemplateOrError(ctx, id)
	if err != nil {
		return nil, err
	}
	beforeView := templateView(before)
	after := *before
	applyInput(&after, in, fields, symbology, actor.UserID)
	err = withTx(ctx, s.repo, func(tx *gorm.DB) error {
		if err := s.repo.UpdateTemplate(tx, &after); err != nil {
			return err
		}
		return middleware.Audit(tx, auditWithSnapshots(
			actor.auditEntry("print_template", id, "update"), in, beforeView, templateView(&after)))
	})
	if err != nil {
		return nil, err
	}
	v := templateView(&after)
	return &v, nil
}

// copySuffix 副本名标识（plan §7.1：后端生成副本，名称追加副本标识）。
const copySuffix = "（副本）"

// CopyTemplate 复制模板：复制全部可变字段，名称追加副本标识（超列宽截断至
// nameMaxLen 字节内），状态恒 ENABLED（副本独立启停）。
func (s *Service) CopyTemplate(ctx context.Context, actor Actor, id int64) (*TemplateView, error) {
	src, err := s.loadTemplateOrError(ctx, id)
	if err != nil {
		return nil, err
	}
	t := &PrintTemplate{
		Name:             copyName(src.Name),
		ObjectType:       src.ObjectType,
		Paper:            src.Paper,
		BarcodeSymbology: src.BarcodeSymbology,
		QRCodeEnabled:    src.QRCodeEnabled,
		Fields:           append(jsonb(nil), src.Fields...),
		HeaderText:       src.HeaderText,
		Status:           StatusEnabled,
		Remark:           src.Remark,
		CreatedBy:        actor.UserID,
		UpdatedBy:        actor.UserID,
	}
	err = withTx(ctx, s.repo, func(tx *gorm.DB) error {
		if err := s.repo.InsertTemplate(tx, t); err != nil {
			return err
		}
		return middleware.Audit(tx, auditWithSnapshots(
			actor.auditEntry("print_template", t.ID.Int64(), "copy"),
			map[string]any{"source_id": id, "name": t.Name}, nil, templateView(t)))
	})
	if err != nil {
		return nil, err
	}
	v := templateView(t)
	return &v, nil
}

// copyName 生成副本名：原名+副本标识，总长截断至列宽内（按 rune 截断防多字节切半）。
func copyName(name string) string {
	full := name + copySuffix
	if len(full) <= nameMaxLen {
		return full
	}
	runes := []rune(name)
	for len(string(runes))+len(copySuffix) > nameMaxLen && len(runes) > 0 {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + copySuffix
}

// SetTemplateStatus 启停（printing.md §2：停用即不可被新任务选用——CreateTask
// 拒绝 DISABLED 模板）。同状态幂等成功；并发状态变更守卫 0 行 → 409（plan §13.2）。
func (s *Service) SetTemplateStatus(ctx context.Context, actor Actor, id int64, status string) (*TemplateView, error) {
	if status != StatusEnabled && status != StatusDisabled {
		return nil, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "status", "reason": "必须为 ENABLED 或 DISABLED",
		})
	}
	t, err := s.loadTemplateOrError(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.Status == status {
		v := templateView(t)
		return &v, nil
	}
	err = withTx(ctx, s.repo, func(tx *gorm.DB) error {
		n, err := s.repo.UpdateTemplateStatus(tx, id, t.Status, status, actor.UserID)
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrTemplateStatusConflict, map[string]any{
				"template_id": id, "from": t.Status, "to": status, "reason": "状态已被并发变更",
			})
		}
		return middleware.Audit(tx, auditWithSnapshots(
			actor.auditEntry("print_template", id, "status"),
			map[string]any{"status": status}, templateView(t), nil))
	})
	if err != nil {
		return nil, err
	}
	t.Status = status
	v := templateView(t)
	return &v, nil
}

// GetTemplate 模板详情。
func (s *Service) GetTemplate(ctx context.Context, id int64) (*TemplateView, error) {
	t, err := s.loadTemplateOrError(ctx, id)
	if err != nil {
		return nil, err
	}
	v := templateView(t)
	return &v, nil
}

// ListTemplates 模板列表（强制分页）。
func (s *Service) ListTemplates(ctx context.Context, f TemplateFilter) ([]TemplateView, int64, error) {
	if f.Page <= 0 || f.PageSize <= 0 {
		return nil, 0, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "page", "reason": "分页参数缺失（列表接口强制分页）",
		})
	}
	if f.ObjectType != "" && !ObjectTypeValid(f.ObjectType) {
		return nil, 0, response.NewError(ErrObjectTypeInvalid, map[string]any{"object_type": f.ObjectType})
	}
	if f.Status != "" && f.Status != StatusEnabled && f.Status != StatusDisabled {
		return nil, 0, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "status", "reason": "必须为 ENABLED 或 DISABLED",
		})
	}
	rows, total, err := s.repo.ListTemplates(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	items := make([]TemplateView, 0, len(rows))
	for _, t := range rows {
		items = append(items, templateView(t))
	}
	return items, total, nil
}

// loadTemplateOrError 装载模板或返回 404。
func (s *Service) loadTemplateOrError(ctx context.Context, id int64) (*PrintTemplate, error) {
	t, err := s.repo.FindTemplate(ctx, id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, response.NewError(ErrTemplateNotFound, map[string]any{"template_id": id})
	}
	return t, nil
}
