package datax

// 文件中心单测（excel §7、api.md §5、plan §6.4；内存替身 + 临时存储根）：
//   - 上传安全矩阵（扩展名/MIME 交叉校验经 internal/storage 白名单、大小上限、
//     module/business_no 参数防御）；
//   - 列表过滤 + 分页；下载/预览守卫（软删/过期/非图片）；软删除幂等；
//   - 下载审计（module=file, action=download——excel §3 下载记录）。

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stockflow/server/internal/response"
)

// xlsxMagic 最小 OOXML 头（PK zip 魔数——storage MIME 交叉校验命中 application/zip）。
var xlsxMagic = append([]byte{0x50, 0x4B, 0x03, 0x04}, make([]byte, 32)...)

// pngMagic PNG 头。
var pngMagic = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89, 0x00, 0x00, 0x00,
	0x0A, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
}

// TestFileUploadSecurity 上传安全矩阵（api.md §5 全清单；白名单实现为 internal/storage）。
func TestFileUploadSecurity(t *testing.T) {
	e := newEnv(t)
	actor := testActor()

	// 合法 xlsx → 登记成功，存储名/路径服务端生成。
	item, err := e.svc.UploadFile(context.Background(), actor, FileUploadInput{
		Module: ModuleProduct, BusinessNo: "IMP-20260101-000001",
		File: fileHeader(t, "我的 商品.xlsx", xlsxMagic),
	})
	if err != nil {
		t.Fatalf("合法上传失败: %v", err)
	}
	if item.FileName != "我的 商品.xlsx" || item.FileType != ".xlsx" {
		t.Fatalf("登记元信息不符: %+v", item)
	}
	if !strings.HasSuffix(item.DownloadURL, "/download") || strings.Contains(item.DownloadURL, "我的") {
		t.Fatalf("下载地址不得携带用户原始文件名: %s", item.DownloadURL)
	}
	// 物理文件存在且路径为 yyyyMM/uuid 形态。
	rec, err := e.repo.FindFile(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("登记行查询失败: %v", err)
	}
	if rec.StoredName == "我的 商品.xlsx" || !strings.Contains(rec.StoragePath, "/") {
		t.Fatalf("服务端重命名/路径生成不符: stored=%s path=%s", rec.StoredName, rec.StoragePath)
	}
	if _, _, err := e.store.Open(rec.StoragePath); err != nil {
		t.Fatalf("物理文件缺失: %v", err)
	}

	// 扩展名白名单外 → 拒绝。
	_, err = e.svc.UploadFile(context.Background(), actor, FileUploadInput{
		Module: ModuleProduct, File: fileHeader(t, "malware.exe", xlsxMagic)})
	requireErrCode(t, err, "STORAGE_TYPE_NOT_ALLOWED")

	// MIME 交叉不匹配（xlsx 扩展名 + PNG 内容）→ 拒绝。
	_, err = e.svc.UploadFile(context.Background(), actor, FileUploadInput{
		Module: ModuleProduct, File: fileHeader(t, "伪装.xlsx", pngMagic)})
	requireErrCode(t, err, "STORAGE_TYPE_NOT_ALLOWED")

	// 大小上限（本 env 1MB）→ 拒绝。
	big := make([]byte, 1<<20+1)
	copy(big, xlsxMagic)
	_, err = e.svc.UploadFile(context.Background(), actor, FileUploadInput{
		Module: ModuleProduct, File: fileHeader(t, "big.xlsx", big)})
	requireErrCode(t, err, "STORAGE_FILE_TOO_LARGE")

	// module/business_no 参数防御。
	_, err = e.svc.UploadFile(context.Background(), actor, FileUploadInput{
		Module: "", File: fileHeader(t, "a.xlsx", xlsxMagic)})
	requireErrCode(t, err, "DATAX_MODULE_PARAM_INVALID")
	_, err = e.svc.UploadFile(context.Background(), actor, FileUploadInput{
		Module: strings.Repeat("m", 65), File: fileHeader(t, "a.xlsx", xlsxMagic)})
	requireErrCode(t, err, "DATAX_MODULE_PARAM_INVALID")
	_, err = e.svc.UploadFile(context.Background(), actor, FileUploadInput{
		Module: "datax/../evil", File: fileHeader(t, "a.xlsx", xlsxMagic)})
	requireErrCode(t, err, "DATAX_MODULE_PARAM_INVALID")
	_, err = e.svc.UploadFile(context.Background(), actor, FileUploadInput{
		Module: ModuleProduct, BusinessNo: "bad no!", File: fileHeader(t, "a.xlsx", xlsxMagic)})
	requireErrCode(t, err, "DATAX_MODULE_PARAM_INVALID")
	// 缺文件部件。
	_, err = e.svc.UploadFile(context.Background(), actor, FileUploadInput{Module: ModuleProduct})
	requireErrCode(t, err, "DATAX_FILE_REQUIRED")
}

// TestFileListAndDelete 列表过滤/分页与软删除（excel §7；删除=软删+审计，plan §6.4）。
func TestFileListAndDelete(t *testing.T) {
	e := newEnv(t)
	actor := testActor()
	up := func(module, bno, name string) *FileItem {
		item, err := e.svc.UploadFile(context.Background(), actor, FileUploadInput{
			Module: module, BusinessNo: bno, File: fileHeader(t, name, xlsxMagic)})
		if err != nil {
			t.Fatalf("上传失败: %v", err)
		}
		return item
	}
	a := up(ModuleProduct, "IMP-1", "a.xlsx")
	up(ModuleSKU, "IMP-1", "b.xlsx")
	up(ModuleProduct, "IMP-2", "c.xlsx")

	// module 过滤。
	_, total, err := e.svc.ListFiles(context.Background(), FileListFilter{
		Module: ModuleProduct, Page: 1, PageSize: 10})
	if err != nil || total != 2 {
		t.Fatalf("module 过滤不符: %v %d", err, total)
	}
	// business_no 过滤。
	_, total, err = e.svc.ListFiles(context.Background(), FileListFilter{
		BusinessNo: "IMP-2", Page: 1, PageSize: 10})
	if err != nil || total != 1 {
		t.Fatalf("business_no 过滤不符: %v %d", err, total)
	}
	// 分页。
	_, total, err = e.svc.ListFiles(context.Background(), FileListFilter{Page: 1, PageSize: 2})
	if err != nil || total != 3 {
		t.Fatalf("总数不符: %v %d", err, total)
	}
	// 删除 → 软删 + 审计；再删幂等 0 行 → 404；删除后下载拒绝。
	if err := e.svc.DeleteFile(context.Background(), actor, a.ID, nil); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if err := e.svc.DeleteFile(context.Background(), actor, a.ID, nil); err == nil {
		t.Fatalf("重复删除应 404 族拒绝")
	} else {
		// 已删行由可见性闸拒绝（软删 404 DATAX_FILE_DELETED）。
		requireErrCode(t, err, "DATAX_FILE_DELETED")
	}
	if _, err := e.svc.GetFileDownload(context.Background(), a.ID, nil); err == nil {
		t.Fatalf("已删文件下载应拒绝")
	}
	found := false
	for _, act := range e.spy.actions() {
		if act == "delete" {
			found = true
		}
	}
	if !found {
		t.Fatalf("删除审计缺位: %v", e.spy.actions())
	}
}

// TestFileDownloadAndPreviewGuard 下载/预览守卫（过期/预览仅图片——plan §6.4）。
func TestFileDownloadAndPreviewGuard(t *testing.T) {
	e := newEnv(t)
	actor := testActor()
	img, err := e.svc.UploadFile(context.Background(), actor, FileUploadInput{
		Module: ModuleProduct, File: fileHeader(t, "图.png", pngMagic)})
	if err != nil {
		t.Fatalf("图片上传失败: %v", err)
	}
	doc, err := e.svc.UploadFile(context.Background(), actor, FileUploadInput{
		Module: ModuleProduct, File: fileHeader(t, "文档.xlsx", xlsxMagic)})
	if err != nil {
		t.Fatalf("文档上传失败: %v", err)
	}

	// 图片可预览、xlsx 预览拒绝（仅图片原样返回，不做缩略图）。
	pv, err := e.svc.GetFilePreview(context.Background(), img.ID, nil)
	if err != nil {
		t.Fatalf("图片预览失败: %v", err)
	}
	_ = pv.Body.Close()
	_, err = e.svc.GetFilePreview(context.Background(), doc.ID, nil)
	requireErrCode(t, err, "DATAX_PREVIEW_UNSUPPORTED")

	// 下载成功 + 下载审计（module=file, action=download——excel §3）。
	dl, err := e.svc.GetFileDownload(context.Background(), doc.ID, nil)
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	_ = dl.Body.Close()
	e.svc.AuditFileDownload(context.Background(), actor, doc.ID, doc.FileName)
	found := false
	for _, entry := range e.spy.all() {
		if entry.Action == "download" && entry.Module == "file" && entry.ObjectID == doc.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("下载审计缺位: want_id=%d all=%+v", doc.ID, e.spy.all())
	}

	// 过期文件：下载拒绝（excel §5 过期自动标记；清理前即不可见）。
	if err := e.repoSoftExpire(doc.ID); err != nil {
		t.Fatalf("构造过期失败: %v", err)
	}
	_, err = e.svc.GetFileDownload(context.Background(), doc.ID, nil)
	requireErrCode(t, err, "DATAX_FILE_EXPIRED")
	pv2, err := e.svc.GetFilePreview(context.Background(), img.ID, nil)
	if err != nil {
		t.Fatalf("未过期图片预览应可用: %v", err)
	}
	_ = pv2.Body.Close()
	_ = img
}

// TestFileUploadAudit 上传审计（module=datax 域外文件为 module=file 的写操作）。
func TestFileUploadAudit(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.UploadFile(context.Background(), testActor(), FileUploadInput{
		Module: "datax", File: fileHeader(t, "a.xlsx", xlsxMagic)}); err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	found := false
	for _, act := range e.spy.actions() {
		if act == "upload" {
			found = true
		}
	}
	if !found {
		t.Fatalf("上传审计缺位: %v", e.spy.actions())
	}
}

// repoSoftExpire 测试助手：把文件登记行的 expires_at 置为过去（模拟保留期到期）。
func (e *env) repoSoftExpire(id int64) error {
	e.repo.mu.Lock()
	defer e.repo.mu.Unlock()
	fi, ok := e.repo.files[id]
	if !ok {
		return response.NewError(ErrFileNotFound, nil)
	}
	past := time.Now().Add(-time.Hour)
	fi.ExpiresAt = &past
	return nil
}

// TestFileScopeVisibility 文件中心数据权限（plan §6.4"数据权限沿用仓库范围，file 列表
// Service 层过滤"）：范围受限用户可见 = 本人上传 ∪ 导出任务仓库范围快照相交产物 ∪
// 导入任务创建人产物；其余不可见且单文件读取按不存在处理（防 ID 枚举）。
func TestFileScopeVisibility(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := testActor() // UserID 42

	// 普通附件（上传人 42）：受限用户（77，仅 8 号仓）不可见；本人可见。
	att, err := e.svc.UploadFile(ctx, owner, FileUploadInput{
		Module: ModuleProduct, File: fileHeader(t, "附件.xlsx", xlsxMagic)})
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	// 改造为导出产物（EXP-1，导出任务仓库快照 = 仅 9 号仓，创建人 42）。
	e.repo.mu.Lock()
	fi := e.repo.files[att.ID]
	fi.Module = "datax"
	fi.BusinessNo = "EXP-1"
	e.repo.exportTasks[1] = &ExportTask{
		ID: 1, ExportNo: "EXP-1", Status: TaskStatusSuccess,
		Params:    JSONMap{"all_warehouses": false, "warehouse_ids": []any{float64(9)}},
		CreatedBy: 42, UpdatedBy: 42,
	}
	e.repo.mu.Unlock()

	wh9 := &FileScope{WarehouseIDs: []int64{9}, UserID: 77}
	wh8 := &FileScope{WarehouseIDs: []int64{8}, UserID: 77}
	all := &FileScope{All: true, UserID: 77}

	// 列表：全量范围可见；9 号仓用户经导出快照相交可见；8 号仓用户不可见；上传人本人可见。
	for sc, want := range map[*FileScope]int64{all: 1, wh9: 1, wh8: 0, {UserID: 42}: 1} {
		_, total, err := e.svc.ListFiles(ctx, FileListFilter{Page: 1, PageSize: 10, Scope: sc})
		if err != nil {
			t.Fatalf("列表查询失败: %v", err)
		}
		if total != want {
			t.Fatalf("范围 %+v 期望 total=%d，得到 %d", sc, want, total)
		}
	}
	// 单文件：8 号仓用户下载/删除按不存在处理；9 号仓用户可下载。
	if _, err := e.svc.GetFileDownload(ctx, att.ID, wh8); err == nil {
		t.Fatalf("跨范围下载应拒绝，得到 nil")
	} else {
		requireErrCode(t, err, "DATAX_FILE_NOT_FOUND")
	}
	if err := e.svc.DeleteFile(ctx, Actor{UserID: 77}, att.ID, wh8); err == nil {
		t.Fatalf("跨范围删除应拒绝，得到 nil")
	}
	dl, err := e.svc.GetFileDownload(ctx, att.ID, wh9)
	if err != nil {
		t.Fatalf("快照相交下载失败: %v", err)
	}
	_ = dl.Body.Close()

	// 导入任务产物（IMP-1，创建人 77、上传人 42）：创建人 77 可见（非上传人），
	// 其余受限用户不可见。
	e.repo.mu.Lock()
	fi2 := *fi
	fi2.ID = 999
	fi2.BusinessNo = "IMP-1"
	fi2.DeletedAt.Valid = false
	e.repo.files[999] = &fi2
	e.repo.importTasks[2] = &ImportTask{ID: 2, ImportNo: "IMP-1", Status: TaskStatusPartial, CreatedBy: 77, UpdatedBy: 77}
	e.repo.mu.Unlock()
	if _, total, err := e.svc.ListFiles(ctx, FileListFilter{Page: 1, PageSize: 10, Scope: &FileScope{WarehouseIDs: []int64{9}, UserID: 77}}); err != nil || total != 2 {
		t.Fatalf("导入创建人（77）应经创建人规则可见 IMP 产物: %v %d", err, total)
	}
	if _, total, err := e.svc.ListFiles(ctx, FileListFilter{Page: 1, PageSize: 10, Scope: &FileScope{WarehouseIDs: []int64{9}, UserID: 88}}); err != nil || total != 1 {
		t.Fatalf("非创建人（88）不应见 IMP 产物: %v %d", err, total)
	}
}
