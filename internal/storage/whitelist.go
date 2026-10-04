// 上传类型白名单（api.md §5：扩展名白名单 + MIME 白名单交叉校验）。
//
// M3 上传类型：图片、Excel、PDF、附件（api.md §5）。Excel 仅收 .xlsx——excelize v2
// 不支持旧版 .xls 二进制格式，且 BIFF/CFB 容器无可靠嗅探魔数，纳入白名单会弱化
// 交叉校验（导入模板/错误 Excel/导出产物均为 excelize 生成的 xlsx）。
// xlsx 为 OOXML 容器（PK zip 魔数），http.DetectContentType 嗅探结果为 application/zip。
package storage

import (
	"net/http"
	"path/filepath"
	"strings"
)

// sniffHeaderLen MIME 嗅探所需头部字节数（http.DetectContentType 规范值）。
const sniffHeaderLen = 512

// allowedExtensions 扩展名 → 允许的嗅探 MIME 集合（交叉校验双向约束：
// 扩展名不在表内拒绝；嗅探 MIME 不在对应集合拒绝）。
var allowedExtensions = map[string][]string{
	".xlsx": {"application/zip"}, // OOXML 容器（zip 魔数）
	".pdf":  {"application/pdf"},
	".png":  {"image/png"},
	".jpg":  {"image/jpeg"},
	".jpeg": {"image/jpeg"},
	".gif":  {"image/gif"},
	".webp": {"image/webp"},
	".bmp":  {"image/bmp"},
	".csv":  {"text/plain", "text/csv"}, // CSV 嗅探为 text/plain（charset 参数已剥离）
	".txt":  {"text/plain"},
	".zip":  {"application/zip"},
}

// extensionAllowed 扩展名是否在白名单。
func extensionAllowed(ext string) bool {
	_, ok := allowedExtensions[ext]
	return ok
}

// mimeAllowedFor 扩展名下嗅探 MIME 是否匹配白名单。
func mimeAllowedFor(ext, sniffed string) bool {
	allowed, ok := allowedExtensions[ext]
	if !ok {
		return false
	}
	for _, m := range allowed {
		if m == sniffed {
			return true
		}
	}
	return false
}

// normalizeExtension 从原始文件名提取小写扩展名（无扩展名返回空串）。
// 仅用于白名单校验与 files.file_type 归类——存储名/路径不使用该值的任何部分
// （api §5：服务端重命名，禁止用户输入参与路径）。
func normalizeExtension(originalName string) string {
	ext := strings.ToLower(filepath.Ext(originalName))
	if i := strings.Index(ext, ";"); i >= 0 { // 极端名如 "a.xlsx;type=x" 不放行
		return ""
	}
	return ext
}

// sniffMIME 嗅探文件头并剥离 charset 参数（text/plain; charset=utf-8 → text/plain）。
func sniffMIME(head []byte) string {
	mt := http.DetectContentType(head)
	if i := strings.Index(mt, ";"); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	return mt
}
