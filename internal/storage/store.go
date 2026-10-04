// 本地存储实现：原子保存（临时文件 + rename）、流式打开、物理删除。
// 大文件必须 Stream（go-dev-standard 规则 11）：Save 以 LimitReader 计数落盘，
// Open 返回 *os.File 由调用方 http.ServeContent 流式响应，禁止整读内存。
package storage

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/stockflow/server/internal/response"
)

// DefaultUploadMaxBytes 默认上传上限（plan §3.2 storage.upload_max_bytes=20MB；
// 调用方传 maxBytes<=0 时兜底，正常路径应显式传入配置值）。
const DefaultUploadMaxBytes = 20 << 20

// Store 本地文件存储（storage.root 可配置——plan §3.2；OSS 留接口不做，plan §3.2 注）。
type Store struct {
	root string // 绝对路径
}

// StoredFile 保存结果（files 表登记所需字段；mime_type 取嗅探值）。
type StoredFile struct {
	StoredName  string // uuid.ext
	StoragePath string // 相对存储根：yyyyMM/stored_name
	Size        int64
	MimeType    string // 嗅探 MIME（已剥离 charset 参数）
	FileType    string // 白名单扩展名（含点，小写）
}

// NewStore 创建存储并确保根目录存在（/ready 文件系统检查的探测基础）。
func NewStore(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("storage: 存储根目录不能为空")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("storage: 存储根目录解析失败: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("storage: 存储根目录创建失败: %w", err)
	}
	return &Store{root: abs}, nil
}

// Root 返回存储根绝对路径（health 检查/清理任务使用）。
func (s *Store) Root() string { return s.root }

// Save 校验并保存上传流：扩展名白名单 → 嗅探 MIME 交叉校验 → 大小上限（LimitReader
// 复核，双保险）→ 服务端重命名（uuid.ext）→ yyyyMM 相对路径 → 临时文件 + rename 原子落盘。
// originalName 仅用于白名单校验与登记展示，任何部分都不参与存储路径（api §5）。
func (s *Store) Save(r io.Reader, originalName string, maxBytes int64) (StoredFile, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultUploadMaxBytes
	}
	ext := normalizeExtension(originalName)
	if !extensionAllowed(ext) {
		return StoredFile{}, response.NewError(ErrTypeNotAllowed, map[string]any{
			"filename": originalName,
			"reason":   "extension_not_allowed",
		})
	}

	head := make([]byte, sniffHeaderLen)
	n, err := io.ReadFull(r, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return StoredFile{}, fmt.Errorf("storage: 读取上传流失败: %w", err)
	}
	sniffed := sniffMIME(head[:n])
	if !mimeAllowedFor(ext, sniffed) {
		return StoredFile{}, response.NewError(ErrTypeNotAllowed, map[string]any{
			"filename":     originalName,
			"sniffed_mime": sniffed,
			"reason":       "mime_mismatch",
		})
	}

	storedName := uuid.NewString() + ext
	relPath := path.Join(time.Now().Format("200601"), storedName) // 相对路径，斜杠规范形
	full, err := s.resolve(relPath)
	if err != nil {
		return StoredFile{}, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return StoredFile{}, response.NewError(ErrStoreBroken, nil)
	}

	tmp, err := os.CreateTemp(filepath.Dir(full), "sf-upload-*")
	if err != nil {
		return StoredFile{}, response.NewError(ErrStoreBroken, nil)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmp != nil { // 成功 rename 后置 nil，避免误删正式文件
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(head[:n]); err != nil {
		return StoredFile{}, response.NewError(ErrStoreBroken, nil)
	}
	// 大小上限复核（双保险：请求层 MaxBytesReader 之外的存储侧最后一道闸）。
	limited := io.LimitReader(r, maxBytes-int64(n)+1)
	size, err := io.Copy(tmp, limited)
	if err != nil {
		return StoredFile{}, response.NewError(ErrStoreBroken, nil)
	}
	total := int64(n) + size
	if total > maxBytes {
		return StoredFile{}, response.NewError(ErrFileTooLarge, map[string]any{
			"limit_bytes": maxBytes,
			"actual":      total,
		})
	}
	if err := tmp.Sync(); err != nil {
		return StoredFile{}, response.NewError(ErrStoreBroken, nil)
	}
	if err := tmp.Close(); err != nil {
		tmp = nil // 已关闭，defer 不再重复 Close（Remove 兜底清理）
		_ = os.Remove(tmpName)
		return StoredFile{}, response.NewError(ErrStoreBroken, nil)
	}
	if err := os.Rename(tmpName, full); err != nil {
		tmp = nil
		_ = os.Remove(tmpName)
		return StoredFile{}, response.NewError(ErrStoreBroken, nil)
	}
	tmp = nil
	return StoredFile{
		StoredName:  storedName,
		StoragePath: relPath,
		Size:        total,
		MimeType:    sniffed,
		FileType:    ext,
	}, nil
}

// Open 按登记的相对路径打开物理文件（流式读取；调用方负责 Close）。
// 路径穿越防御：先清洗再强制收敛于存储根内，越界一律 ErrInvalidPath。
func (s *Store) Open(relPath string) (*os.File, os.FileInfo, error) {
	full, err := s.resolve(relPath)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, response.NewError(ErrFileNotFound, map[string]any{"path": relPath})
		}
		return nil, nil, response.NewError(ErrStoreBroken, nil)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, response.NewError(ErrStoreBroken, nil)
	}
	if info.IsDir() {
		_ = f.Close()
		return nil, nil, response.NewError(ErrInvalidPath, map[string]any{"path": relPath})
	}
	return f, info, nil
}

// Remove 物理删除（file_cleanup / 手动删除后置清理；文件不存在视为已清理，幂等成功）。
func (s *Store) Remove(relPath string) error {
	full, err := s.resolve(relPath)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("storage: 物理删除失败（%s）: %w", relPath, err)
	}
	return nil
}

// resolve 相对路径 → 存储根内绝对路径；越界/穿越嫌疑拒绝。
func (s *Store) resolve(relPath string) (string, error) {
	if strings.Contains(relPath, "\x00") {
		return "", response.NewError(ErrInvalidPath, map[string]any{"path": relPath})
	}
	// 以 "/" 规范化：前导 "/" 强制按相对路径清洗（剔除 ..、. 等），再转平台分隔符。
	clean := path.Clean("/" + strings.ReplaceAll(relPath, "\\", "/"))
	clean = strings.TrimPrefix(clean, "/")
	full := filepath.Join(s.root, filepath.FromSlash(clean))
	if full != s.root && !strings.HasPrefix(full, s.root+string(os.PathSeparator)) {
		return "", response.NewError(ErrInvalidPath, map[string]any{"path": relPath})
	}
	if full == s.root { // 解析为根目录本身（目录操作不在此层）
		return "", response.NewError(ErrInvalidPath, map[string]any{"path": relPath})
	}
	return full, nil
}
