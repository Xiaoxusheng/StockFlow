package auth

import (
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/stockflow/server/internal/response"
)

// 密码安全（architecture.md §6：bcrypt，禁止明文与弱哈希；plan §7.3：cost 12）。
// BcryptCost 与 internal/database.BcryptCost 同值（seed 与本域共用同一强度）。
const BcryptCost = 12

// 密码策略边界（plan §7.3：长度 ≥ 8 且含字母+数字）。
// maxPasswordLen 取 bcrypt 72 字节输入上限：超出既会报错也会被静默截断，宁可显式拒绝。
const (
	minPasswordLen = 8
	maxPasswordLen = 72
)

// dummyHash 固定 bcrypt 哈希（"sf-dummy-password"）：用户不存在时也执行同代价比较，
// 抹平响应时间差，防账号枚举（plan §7.3 统一 AUTH_CREDENTIALS_INVALID 的配套措施）。
var dummyHash = mustDummyHash()

func mustDummyHash() string {
	h, err := bcrypt.GenerateFromPassword([]byte("sf-dummy-password"), BcryptCost)
	if err != nil {
		panic("auth: 生成防枚举假哈希失败: " + err.Error())
	}
	return string(h)
}

// HashPassword 生成 bcrypt 哈希（cost 12）。
func HashPassword(plain string) (string, error) {
	if err := ValidatePassword(plain); err != nil {
		return "", err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), BcryptCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// VerifyPassword 安全比较（bcrypt 内置恒定时间比较 + 版本/成本解析）。
func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// ValidatePasswordStrength 强密码策略校验（plan §7.3）：
// 长度 8-72 且同时包含字母与数字。失败返回 AUTH_PASSWORD_WEAK（带 details）。
func ValidatePasswordStrength(pw string) error {
	if utf8.RuneCountInString(pw) < minPasswordLen || len(pw) > maxPasswordLen {
		return response.NewError(ErrPasswordWeak, map[string]any{
			"min_length": minPasswordLen, "max_length": maxPasswordLen,
		})
	}
	var hasLetter, hasDigit bool
	for _, r := range pw {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return response.NewError(ErrPasswordWeak, map[string]any{
			"reason": "必须同时包含字母与数字",
		})
	}
	return nil
}

// ValidatePassword 供 Service 使用；与 Strength 等价（当前策略单一，保留分层以便扩展）。
func ValidatePassword(pw string) error { return ValidatePasswordStrength(pw) }
