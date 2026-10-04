package returns

// 异常图片挂接（business-flow §11.2 异常图片能力，2026-10-04 立项——
// ExceptionView.image_refs 此前"有字段无写入路径"，创建恒空数组）。
//
// 两段式消费契约（与文件中心挂接，Pad/PC 拍照取证接线前置）：
//  1. 先经文件中心上传（POST /api/files，建议 module=exception、business_no=异常单号）
//     取得文件 ID 集合；
//  2. POST /api/exceptions/{id}/images 提交 file_ids → 校验（存在/未过期/image/*，
//     经 ImageFileChecker 窄接口读文件域数据面）→ 追加 image_refs（jsonb 去重、累计
//     ≤20，对齐 masterdata 商品图片上限）+ 追加处理记录（append-only）+ 审计，同事务。
//
// 产物引用形态："/api/files/{id}/download"（与文件中心 FileItem.DownloadURL 同源，
// 消费方按既有认证下载通路取图）。
// 生命周期：OPEN/ASSIGNED/PROCESSING/PENDING_REVIEW 可挂接（处理过程取证）；
// RESOLVED/CLOSED 为生命周期终点拒绝。检查器未注入 fail-closed（plan §3.1 规则①）。

import (
	"context"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// ExceptionImageInput 图片挂接入参。
type ExceptionImageInput struct {
	// FileIDs 文件中心上传返回的文件 ID（database.ID 反序列化同时接受字符串与数字，
	// 对齐全仓「业务 ID 字符串」约定）。
	FileIDs []database.ID `json:"file_ids"`
}

// exceptionImageLimit 图片挂接累计上限（对齐 masterdata validateImageURLs ≤20 张）。
const exceptionImageLimit = 20

// exceptionImageRef 图片文件 → 引用 URL（文件中心下载通路同源）。
func exceptionImageRef(fileID int64) string {
	return "/api/files/" + strconv.FormatInt(fileID, 10) + "/download"
}

// AttachExceptionImages 挂接图片取证。
func (s *Service) AttachExceptionImages(ctx context.Context, actor Actor, id int64, in ExceptionImageInput) (*ExceptionView, error) {
	if s.imageFiles == nil {
		return nil, response.NewError(ErrGatewayRequired, map[string]any{
			"reader": "ImageFileChecker", "reason": "图片校验服务未装配（router 注入缺位）",
		})
	}
	if len(in.FileIDs) == 0 {
		return nil, response.NewError(ErrExceptionImagesInvalid, map[string]any{
			"field": "file_ids", "reason": "至少一个文件 ID",
		})
	}
	// 去重（保序）与值域校验；单次提交也受累计上限约束。
	seen := map[int64]bool{}
	ids := make([]int64, 0, len(in.FileIDs))
	for _, fid := range in.FileIDs {
		fidInt := fid.Int64()
		if fidInt <= 0 {
			return nil, response.NewError(ErrExceptionImagesInvalid, map[string]any{
				"field": "file_ids", "reason": "文件 ID 必须为正整数",
			})
		}
		if seen[fidInt] {
			continue
		}
		seen[fidInt] = true
		ids = append(ids, fidInt)
	}
	if len(ids) > exceptionImageLimit {
		return nil, response.NewError(ErrExceptionImagesInvalid, map[string]any{
			"field": "file_ids", "reason": "单次最多 20 个文件",
		})
	}
	for _, fid := range ids {
		ok, err := s.imageFiles.IsImageFile(ctx, fid)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, response.NewError(ErrExceptionImagesInvalid, map[string]any{
				"field": "file_ids", "reason": "文件不存在、已过期或非图片", "file_id": fid,
			})
		}
	}

	var view *ExceptionView
	err := s.repo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		e, err := s.repo.FindExceptionForUpdate(tx, id)
		if err != nil {
			return err
		}
		if e == nil {
			return response.NewError(ErrExceptionNotFound, map[string]any{"exception_id": id})
		}
		switch e.Status {
		case ExceptionStatusOpen, ExceptionStatusAssigned, ExceptionStatusProcessing, ExceptionStatusPendingReview:
		default:
			return response.NewError(ErrExceptionImagesClosed, map[string]any{"status": e.Status})
		}
		existing := map[string]bool{}
		refs := make([]string, 0, len(e.ImageRefList())+len(ids))
		for _, r := range e.ImageRefList() {
			r = strings.TrimSpace(r)
			if r == "" || existing[r] {
				continue
			}
			existing[r] = true
			refs = append(refs, r)
		}
		added := make([]string, 0, len(ids))
		for _, fid := range ids {
			ref := exceptionImageRef(fid)
			if existing[ref] {
				continue
			}
			existing[ref] = true
			refs = append(refs, ref)
			added = append(added, ref)
		}
		if len(refs) > exceptionImageLimit {
			return response.NewError(ErrExceptionImagesInvalid, map[string]any{
				"field": "file_ids", "reason": "累计挂接图片不能超过 20 张",
			})
		}
		if len(added) == 0 {
			// 全部重复：幂等无变化，不写处理记录/审计，直接返回现状。
			view = newExceptionView(e)
			return nil
		}
		if err := s.repo.UpdateExceptionImageRefs(tx, id, refs); err != nil {
			return err
		}
		rec := HandleRecord{
			At: database.Now(), Action: "attach_images",
			ByID: actor.UserID, ByName: actor.Username,
			Note: "挂接图片取证：" + strings.Join(added, ", "),
		}
		if err := s.repo.AppendHandleRecord(tx, id, rec); err != nil {
			return err
		}
		// 返回视图同步（AppendHandleRecord 在库内追加，本行内存快照补齐，避免响应缺记录）。
		e.ImageRefs = marshalJSONB(refs)
		e.HandleRecords = marshalJSONB(append(e.HandleRecordList(), rec))
		e2 := actor.auditEntry("exception", id, "attach_images")
		e2.Success = true
		e2.After = map[string]any{"added": added, "total": len(refs)}
		if err := middleware.Audit(tx, e2); err != nil {
			return err
		}
		view = newExceptionView(e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}
