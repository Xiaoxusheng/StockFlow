package storage

// 文件中心存储层单测——不依赖 PostgreSQL/Redis/网络（backend-m3-plan §14 红线）。
// 覆盖：白名单交叉校验矩阵、服务端重命名与路径生成、大小上限、路径穿越防御、
// 原子保存与幂等删除、files 模型与 000011 DDL 列对齐。

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stockflow/server/internal/response"
)

// 测试用最小文件内容（http.DetectContentType 可稳定识别的魔数）。
var (
	pngContent = append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, make([]byte, 64)...)
	pdfContent = []byte("%PDF-1.4\n%test content for sniffing\n")
	zipContent = []byte("PK\x03\x04fake-ooxml-container-content")
	csvContent = []byte("sku_code,name\nSKU-001,测试商品\n")
	exeContent = append([]byte{'M', 'Z'}, make([]byte, 64)...) // Windows PE 头，白名单外
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "files"))
	if err != nil {
		t.Fatalf("创建存储失败: %v", err)
	}
	return s
}

func TestWhitelistMatrix(t *testing.T) {
	cases := []struct {
		name    string
		ext     string
		content []byte
		wantOK  bool
	}{
		{"xlsx 容器嗅探为 zip", ".xlsx", zipContent, true},
		{"pdf", ".pdf", pdfContent, true},
		{"png", ".png", pngContent, true},
		{"jpg", ".jpg", pngContent, false}, // jpeg 内容伪装 png：嗅探 image/png ≠ image/jpeg，交叉校验拒绝
		{"csv 嗅探为 text/plain", ".csv", csvContent, true},
		{"txt", ".txt", csvContent, true},
		{"zip", ".zip", zipContent, true},
		{"可执行文件白名单外", ".exe", exeContent, false},
		{"旧版 xls 不收（excelize 不支持且无可靠嗅探）", ".xls", exeContent, false},
		{"无扩展名", "", csvContent, false},
	}
	for _, c := range cases {
		ext := normalizeExtension("upload" + c.ext)
		if got := extensionAllowed(ext) && mimeAllowedFor(ext, sniffMIME(c.content)); got != c.wantOK {
			t.Fatalf("%s: 校验结果 %v，期望 %v", c.name, got, c.wantOK)
		}
	}
}

func TestSaveGeneratesServerSideNameAndPath(t *testing.T) {
	s := newTestStore(t)
	got, err := s.Save(bytes.NewReader(pngContent), "用户 原始(名).png", 1<<20)
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	// 服务端重命名：uuid + 白名单扩展名（api §5）
	if !strings.HasSuffix(got.StoredName, ".png") || len(got.StoredName) != 36+len(".png") {
		t.Fatalf("存储名应为 uuid.png，实际 %q", got.StoredName)
	}
	// 原始文件名任何部分不得进入存储路径（api §5 红线）
	if strings.Contains(got.StoragePath, "用户") || strings.Contains(got.StoragePath, "原始") {
		t.Fatalf("存储路径不得含用户原始名: %q", got.StoragePath)
	}
	// 路径形如 yyyyMM/uuid.ext（yyyyMM = 6 位数字）
	parts := strings.Split(got.StoragePath, "/")
	if len(parts) != 2 || len(parts[0]) != 6 || !isDigits(parts[0]) || parts[1] != got.StoredName {
		t.Fatalf("存储路径应为 yyyyMM/uuid.ext，实际 %q", got.StoragePath)
	}
	if got.Size != int64(len(pngContent)) || got.MimeType != "image/png" || got.FileType != ".png" {
		t.Fatalf("登记信息不符: %+v", got)
	}
	// 物理落点存在且内容一致
	f, info, err := s.Open(got.StoragePath)
	if err != nil {
		t.Fatalf("打开已存文件失败: %v", err)
	}
	defer f.Close()
	if info.Size() != int64(len(pngContent)) {
		t.Fatalf("落盘大小不符: %d", info.Size())
	}
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

func TestSaveEnforcesSizeLimitAndCleansTemp(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Save(bytes.NewReader(zipContent), "big.xlsx", 4)
	if err == nil {
		t.Fatal("超上限保存应失败")
	}
	var berr *response.Error
	if !errors.As(err, &berr) || berr.Error() != "STORAGE_FILE_TOO_LARGE: 文件超出大小上限" {
		t.Fatalf("应返回 STORAGE_FILE_TOO_LARGE，实际 %v", err)
	}
	// 临时文件不残留（原子保存：失败即清理，正式目录内无任何落盘物）
	var visited int
	root := s.Root()
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, _ error) error {
		if !d.IsDir() {
			visited++
		}
		return nil
	})
	if visited != 0 {
		t.Fatalf("失败保存不应残留任何文件，实际 %d 个", visited)
	}
}

func TestSaveRejectsDisallowedTypes(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(bytes.NewReader(exeContent), "evil.exe", 1<<20); err == nil {
		t.Fatal("白名单外扩展名应拒绝")
	}
	// MIME 交叉校验：png 内容伪装 xlsx
	if _, err := s.Save(bytes.NewReader(pngContent), "fake.xlsx", 1<<20); err == nil {
		t.Fatal("嗅探 MIME 与扩展名不匹配应拒绝（xlsx 期望 zip 容器）")
	}
}

func TestOpenRejectsPathTraversal(t *testing.T) {
	s := newTestStore(t)
	for _, p := range []string{
		"../etc/passwd",
		"a/../../b",
		"..\\..\\windows\\win.ini",
		"/../../etc/passwd",
		"./",
		"",
		"\x00bad",
	} {
		if _, _, err := s.Open(p); err == nil {
			t.Fatalf("路径 %q 应被拒绝（防路径穿越）", p)
		}
	}
}

func TestOpenMissingReturnsNotFound(t *testing.T) {
	s := newTestStore(t)
	_, _, err := s.Open("209901/not-exist.png")
	var rerr *response.Error
	if !errors.As(err, &rerr) || rerr.Error() != "STORAGE_FILE_NOT_FOUND: 文件不存在或已被清理" {
		t.Fatalf("缺失文件应返回 STORAGE_FILE_NOT_FOUND，实际 %v", err)
	}
}

func TestRemoveIdempotent(t *testing.T) {
	s := newTestStore(t)
	got, err := s.Save(bytes.NewReader(pdfContent), "a.pdf", 1<<20)
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if err := s.Remove(got.StoragePath); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, _, err := s.Open(got.StoragePath); err == nil {
		t.Fatal("删除后打开应失败")
	}
	if err := s.Remove(got.StoragePath); err != nil {
		t.Fatalf("重复删除应幂等成功，实际 %v", err)
	}
}

func TestNewStoreGuards(t *testing.T) {
	if _, err := NewStore("   "); err == nil {
		t.Fatal("空存储根应拒绝")
	}
	s, err := NewStore(filepath.Join(t.TempDir(), "r"))
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if s.Root() == "" {
		t.Fatal("Root 应返回绝对路径")
	}
}

func TestSaveDefaultLimitWhenUnset(t *testing.T) {
	s := newTestStore(t)
	// maxBytes<=0 取 DefaultUploadMaxBytes：小文件正常保存（不因零值直接拒绝——
	// 零值兜底是部署配置缺位的防线，不应破坏合法小文件）。
	if _, err := s.Save(bytes.NewReader(csvContent), "a.csv", 0); err != nil {
		t.Fatalf("maxBytes 零值应兜底默认上限，实际 %v", err)
	}
	// 超过默认上限（20MB）的内容在兜底路径下仍被拦截。
	big := bytes.Repeat([]byte("a"), DefaultUploadMaxBytes+1)
	if _, err := s.Save(bytes.NewReader(big), "a.csv", 0); err == nil {
		t.Fatal("默认上限内超大文件应拒绝")
	}
}

func TestFileModelMatchesDDLColumns(t *testing.T) {
	// files 模型列标签与 000011 DDL 对齐（存储层模型为登记/查询的统一落点）。
	want := []string{
		"id", "file_name", "stored_name", "storage_path", "mime_type", "file_type",
		"size_bytes", "module", "business_no", "uploader_id", "uploader_name",
		"expires_at", "deleted_at", "created_at", "updated_at", "created_by", "updated_by",
	}
	got := map[string]bool{}
	tpe := reflect.TypeOf(File{})
	for i := 0; i < tpe.NumField(); i++ {
		tag := tpe.Field(i).Tag.Get("gorm")
		for _, part := range strings.Split(tag, ";") {
			if name, ok := strings.CutPrefix(part, "column:"); ok {
				got[name] = true
			}
		}
	}
	for _, col := range want {
		if !got[col] {
			t.Fatalf("File 模型缺少列标签 column:%s（000011 DDL 契约）", col)
		}
	}
	if (File{}).TableName() != "files" {
		t.Fatal("File 模型表名应为 files")
	}
}
