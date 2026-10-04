// Package storage 文件中心存储层（backend-m3-plan §6.4、excel §7、api.md §5 文件上传安全）。
//
// 职责：files 表 GORM 模型、上传类型白名单（扩展名 + MIME 交叉校验）、服务端重命名
// （uuid + 白名单扩展名）、相对路径生成（yyyyMM/stored_name，防路径穿越）、本地存储根
// （storage.root 可配置）的原子保存与流式打开/删除。业务编排（上传端点、files 登记
// 事务、下载审计、清理任务）由 internal/datax 承接（plan §2.1 Scope X）。
//
// 安全口径（api.md §5 全清单）：
//   - 扩展名白名单 + http.DetectContentType 嗅探 MIME 交叉校验（双向不匹配即拒绝）；
//   - 大小上限由调用方传入（storage.upload_max_bytes），保存时再以 LimitReader 复核（双保险）；
//   - 存储名/存储路径一律服务端生成，用户原始文件名仅用于白名单校验与展示（绝不参与路径）；
//   - 相对路径解析前清洗并强制收敛于存储根内（防路径穿越）。
package storage

import (
	"net/http"

	"github.com/stockflow/server/internal/response"
)

// 域错误码（统一经 internal/response 注册，plan §2.3 判据 6）。
var (
	// ErrTypeNotAllowed 文件类型不在白名单（扩展名或嗅探 MIME 不匹配——api §5 交叉校验）。
	ErrTypeNotAllowed = response.Register("STORAGE_TYPE_NOT_ALLOWED", "文件类型不在允许范围", http.StatusBadRequest)
	// ErrFileTooLarge 文件超出大小上限。
	ErrFileTooLarge = response.Register("STORAGE_FILE_TOO_LARGE", "文件超出大小上限", http.StatusRequestEntityTooLarge)
	// ErrInvalidPath 存储路径非法（路径穿越嫌疑或不在存储根内）。
	ErrInvalidPath = response.Register("STORAGE_INVALID_PATH", "存储路径非法", http.StatusBadRequest)
	// ErrFileNotFound 物理文件不存在（files 行存在但存储对象缺失——一致性异常）。
	ErrFileNotFound = response.Register("STORAGE_FILE_NOT_FOUND", "文件不存在或已被清理", http.StatusNotFound)
	// ErrStoreBroken 存储根不可用（创建/写入失败）。
	ErrStoreBroken = response.Register("STORAGE_STORE_BROKEN", "文件存储不可用", http.StatusInternalServerError)
)
