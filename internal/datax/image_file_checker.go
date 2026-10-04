package datax

// ImageFileCheckService 图片文件有效性校验（returns.ImageFileChecker 窄接口的实现侧
// 导出——文件中心 files 表属本域数据面，router 装配注入 returns 异常图片挂接校验；
// backend-m1-plan §4.3 消费方窄接口机制，先例 internal/masterdata/m2readers.go）。
//
// 判定口径：文件存在（软删行由 GORM 作用域自动排除）、未过期（过期件由 file_cleanup
// 物理清理，软删前即不可挂接）且 MIME 为 image/*（api.md §5 图片白名单语义）。

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/storage"
)

// ImageFileCheckService 文件中心图片校验实现。
type ImageFileCheckService struct {
	db *gorm.DB
}

// NewImageFileChecker 构建（router 装配：returns.WithImageFileChecker(datax.NewImageFileChecker(db))）。
func NewImageFileChecker(db *gorm.DB) *ImageFileCheckService {
	return &ImageFileCheckService{db: db}
}

// IsImageFile 实现 returns.ImageFileChecker。db 故障返回错误（消费方 fail-closed）。
func (s *ImageFileCheckService) IsImageFile(ctx context.Context, fileID int64) (bool, error) {
	var f storage.File
	err := s.db.WithContext(ctx).Where("id = ?", fileID).Take(&f).Error
	if err == gorm.ErrRecordNotFound {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("校验图片文件 %d 失败: %w", fileID, err)
	}
	if f.ExpiresAt != nil && !f.ExpiresAt.IsZero() && f.ExpiresAt.Before(time.Now()) {
		return false, nil // 已过期（挂接指向死链无意义）
	}
	return strings.HasPrefix(f.MimeType, "image/"), nil
}
