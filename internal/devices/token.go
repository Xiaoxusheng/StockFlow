package devices

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/stockflow/server/internal/response"
)

// 设备令牌（backend-m3-plan §8.2 冻结协议，与用户 JWT 并行的最小设备凭证）：
//   - JWT HS256，claims {did, code, typ, tver}，TTL 365 天（独立评审收敛项：
//     30 天无刷新端点会使全部设备每月集体离线；失效语义由 token_version 承担）；
//   - issuer=stockflow-devices 与用户令牌（stockflow-auth）隔离——令牌族互不通用；
//   - DeviceAuthRequired：验签 + devices 行 status=ENABLED AND activated AND
//     token_version 匹配（每次请求查 DB——设备端点低频可接受，plan §8.2）。
//
// 密钥装配沿用 auth 域环境变量先例（internal/auth/config.go）：SF_DEVICES_JWT_SECRET，
// release 模式缺失即 panic 快速失败；debug/test 降级进程内随机密钥（重启即失效）。
// devices 包无法读取 auth 包内 runtimeConfig（未导出），独立解析同一模式（非第二套
// 认证机制——设备令牌协议为 plan §8.2 冻结的并行凭证族）。

const (
	// TokenTTL 设备令牌有效期（plan §8.2 冻结 365 天）。
	TokenTTL = 365 * 24 * time.Hour
	// TokenIssuer 设备令牌签发方（与 auth.jwtIssuer 隔离）。
	TokenIssuer = "stockflow-devices"
	// tokenMinSecretLen 最低密钥长度（对齐 auth minJWTSecretLen=32，安全渗透修复：
	// 16 字节弱密钥可离线爆破，HS256 破出后可伪造任意设备令牌）。
	tokenMinSecretLen = 32
	// envTokenSecret 设备令牌签名密钥环境变量（SF_ 前缀与 viper 约定一致）。
	envTokenSecret = "SF_DEVICES_JWT_SECRET"
)

// deviceClaims 设备 JWT 载荷。did=设备 ID；tver=签发时设备行 token_version（撤销判定锚点）。
type deviceClaims struct {
	DeviceID int64  `json:"did"`
	Code     string `json:"code"`
	Type     string `json:"typ"`
	Version  int    `json:"tver"`
	jwt.RegisteredClaims
}

// tokenManager 设备令牌签发与验证（Service 持有；时钟可注入供单测）。
type tokenManager struct {
	secret []byte
	now    func() time.Time
	ttl    time.Duration
}

// tokenSecretClassCount 密钥字符类别数（字母/数字/符号；口径对齐 internal/auth
// jwtSecretClassCount——单一类别即使长度达标也属弱口令式密钥）。
func tokenSecretClassCount(secret string) int {
	var letter, digit, symbol bool
	for _, r := range secret {
		switch {
		case unicode.IsLetter(r):
			letter = true
		case unicode.IsDigit(r):
			digit = true
		default:
			symbol = true
		}
	}
	n := 0
	for _, hit := range []bool{letter, digit, symbol} {
		if hit {
			n++
		}
	}
	return n
}

// resolveTokenSecret 解析设备令牌签名密钥（模式对齐 internal/auth/config.go:97-113：
// 弱密钥拒绝；release 缺失 fail-fast；debug/test 进程内随机——重启即全部设备令牌失效，
// 设备经重新激活恢复）。
func resolveTokenSecret() ([]byte, error) {
	s := os.Getenv(envTokenSecret)
	switch {
	case len(s) > 0 && len(s) < tokenMinSecretLen:
		return nil, fmt.Errorf("%s 长度不足 %d 字节，拒绝弱密钥启动", envTokenSecret, tokenMinSecretLen)
	case len(s) >= tokenMinSecretLen:
		// 字符类别校验对齐 internal/auth/config.go jwtSecretClassCount：单一类别密钥
		// 即使长度达标也属弱口令式密钥，拒绝启动。
		if tokenSecretClassCount(s) < 2 {
			return nil, fmt.Errorf("%s 仅含单一字符类别（字母/数字/符号），拒绝弱密钥启动", envTokenSecret)
		}
		return []byte(s), nil
	case gin.Mode() == gin.ReleaseMode:
		return nil, fmt.Errorf("%s 未设置：release 模式禁止以未知密钥启动（deployment.md §1/§3；debug 模式才降级为进程内随机密钥）", envTokenSecret)
	default:
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("生成临时设备令牌密钥失败: %w", err)
		}
		return []byte(base64.RawURLEncoding.EncodeToString(raw)), nil
	}
}

// newTokenManager 构造令牌管理器（密钥非法返回错误，RegisterRoutes 启动期 panic 快速失败）。
func newTokenManager() (*tokenManager, error) {
	secret, err := resolveTokenSecret()
	if err != nil {
		return nil, err
	}
	return &tokenManager{secret: secret, now: time.Now, ttl: TokenTTL}, nil
}

// Issue 为已激活设备签发设备令牌（返回 token 与过期时刻）。
func (m *tokenManager) Issue(d *Device) (string, time.Time, error) {
	now := m.now()
	exp := now.Add(m.ttl)
	claims := deviceClaims{
		DeviceID: int64(d.ID),
		Code:     d.Code,
		Type:     d.Type,
		Version:  d.TokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    TokenIssuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			Subject:   d.Code,
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("签发设备令牌失败: %w", err)
	}
	return token, exp, nil
}

// Parse 解析并校验设备令牌（算法白名单 + 签名 + 过期 + issuer + exp 必填；
// 过期与无效统一 ErrDeviceTokenInvalid——设备端无自动刷新，重新激活路径由
// devices.md §6.2 流程承担）。
func (m *tokenManager) Parse(raw string) (*deviceClaims, error) {
	if raw == "" {
		return nil, response.NewError(ErrDeviceTokenInvalid, nil)
	}
	token, err := jwt.ParseWithClaims(raw, &deviceClaims{}, func(t *jwt.Token) (any, error) {
		return m.secret, nil
	},
		jwt.WithValidMethods([]string{"HS256"}), // 拒绝 alg 混淆（对齐 auth/jwt.go）
		jwt.WithIssuer(TokenIssuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !token.Valid {
		return nil, response.NewError(ErrDeviceTokenInvalid, nil)
	}
	claims, ok := token.Claims.(*deviceClaims)
	if !ok || claims.DeviceID <= 0 || claims.Code == "" || claims.Version < 1 {
		return nil, response.NewError(ErrDeviceTokenInvalid, nil)
	}
	return claims, nil
}

// tokenIssuerProbe 无验签提取 JWT iss（仅用于双轨路由判定——决定令牌应走设备验证还是
// 用户验证链，不用于信任任何内容；设备路径随后全量验签）。
func tokenIssuerProbe(raw string) string {
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	var claims jwt.RegisteredClaims
	if _, _, err := parser.ParseUnverified(raw, &claims); err != nil {
		return ""
	}
	return claims.Issuer
}

// ---- 激活码（一次性、时效、防重放——plan §8.2）----

// activationTTL 激活码有效期（plan §8.2 冻结 15 分钟）。
const activationTTL = 15 * time.Minute

// newActivationToken 生成一次性激活 token：crypto/rand 32 字节 → URL 安全 Base64
// （43 字符，无填充——粘贴/二维码均友好）。
func newActivationToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("生成激活码失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// hashActivationToken 激活 token 哈希（服务端仅存哈希——000013 DDL 注：不存明文，
// architecture §6 日志红线同源）。
func hashActivationToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
