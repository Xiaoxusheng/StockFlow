package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/stockflow/server/internal/cache"
	"github.com/stockflow/server/internal/response"
)

// 登录图像验证码（防自动化喷洒/撞库的第一道人机闸；与 plan §7.3 失败锁定互补）：
//
//	签发 GET /api/auth/captcha（公开端点）→ SVG 图 + captcha_id，答案存 Redis
//	sf:captcha:{id}（TTL 默认 3m，SF_AUTH_CAPTCHA_TTL 可配），校验通过即删除（一次性）。
//	校验在 handler 层（handleLogin）先于 svc.Login 执行——Service.Login 不感知验证码，
//	服务层既有 42 处登录测试契约零波及；速率锁定（loginGuard）与人机闸职责分层。
//
// 安全口径：
//   - 校验失败/过期/Redis 故障一律 fail-closed（拒绝登录）——验证码是安全闸而非限流
//     计数，fail-open 等于没有；Redis 不可用时签发同样失败，登录页图片本身无法展示，
//     "认证不可用"是如实状态（与会话存储强依赖 Redis 的既有口径一致）。
//   - 答案比较用 constant-time（subtle.ConstantTimeCompare），防时序侧信道。
//   - 字符集剔除易混淆字形（0O1ILI|），降低人工辨认失败率。

const (
	// captchaCodeLen 验证码字符数。
	captchaCodeLen = 4
	// captchaChars 无易混淆字符集（去 0O1IiLl|）。
	captchaChars = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
)

// captchaKey Redis 键：sf:captcha:{id}。
func captchaKey(id string) string { return cache.Key("captcha", id) }

// CaptchaChallenge 签发结果（image 为 data URL，<img src> 直用）。
type CaptchaChallenge struct {
	CaptchaID string `json:"captcha_id"`
	Image     string `json:"image"` // data:image/svg+xml;base64,...
	ExpiresIn int64  `json:"expires_in"`
}

// captchaTTL 配置态有效期（resolveRuntimeConfig 落 cfg.captchaTTL）。
func (s *Service) captchaTTL() time.Duration {
	if s.cfg.captchaTTL > 0 {
		return s.cfg.captchaTTL
	}
	return defaultCaptchaTTL
}

// IssueLoginCaptcha 签发验证码：随机 id + 随机码，答案写 Redis（TTL），SVG 以 data URL 返回。
func (s *Service) IssueLoginCaptcha(ctx context.Context) (*CaptchaChallenge, error) {
	if s.rdb == nil {
		return nil, response.NewError(response.CodeInternalError, map[string]any{
			"reason": "验证码服务不可用（Redis 未装配）",
		})
	}
	id, err := newOpaqueToken()
	if err != nil {
		return nil, err
	}
	code, err := newCaptchaCode()
	if err != nil {
		return nil, err
	}
	ttl := s.captchaTTL()
	if err := s.rdb.Set(ctx, captchaKey(id), strings.ToUpper(code), ttl).Err(); err != nil {
		return nil, response.NewError(response.CodeInternalError, map[string]any{
			"reason": "验证码签发失败", "error": err.Error(),
		})
	}
	svg := buildCaptchaSVG(code)
	return &CaptchaChallenge{
		CaptchaID: id,
		Image:     "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg)),
		ExpiresIn: int64(ttl / time.Second),
	}, nil
}

// VerifyLoginCaptcha 校验并消费验证码（一次性：命中即删，重放无效）。
// 服务层公开给 handler 调用；Redis 读失败 fail-closed（见文件头安全口径）。
// 错误码 ErrCaptchaInvalid 注册于 errors.go。
func (s *Service) VerifyLoginCaptcha(ctx context.Context, id, code string) error {
	if s.rdb == nil {
		return response.NewError(response.CodeInternalError, map[string]any{
			"reason": "验证码服务不可用（Redis 未装配）",
		})
	}
	id = strings.TrimSpace(id)
	code = strings.ToUpper(strings.TrimSpace(code))
	if id == "" || code == "" {
		return response.NewError(ErrCaptchaInvalid, nil)
	}
	raw, err := s.rdb.Get(ctx, captchaKey(id)).Result()
	if errors.Is(err, redis.Nil) {
		return response.NewError(ErrCaptchaInvalid, nil)
	}
	if err != nil {
		// Redis 故障：安全闸 fail-closed（不比对、不删除）。
		return response.NewError(response.CodeInternalError, map[string]any{
			"reason": "验证码校验失败", "error": err.Error(),
		})
	}
	// 先消费后比对：无论答案对错，该 id 均作废（防同一验证码批量试答案）。
	if err := s.rdb.Del(ctx, captchaKey(id)).Err(); err != nil {
		return response.NewError(response.CodeInternalError, map[string]any{
			"reason": "验证码消费失败", "error": err.Error(),
		})
	}
	if subtleEqual(raw, code) {
		return nil
	}
	return response.NewError(ErrCaptchaInvalid, nil)
}

// subtleEqual 恒定时间字符串比较（长度不同立即假，不泄露前缀匹配位）。
func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// newCaptchaCode 随机验证码（crypto/rand，无易混淆字符集）。
func newCaptchaCode() (string, error) {
	b := make([]byte, captchaCodeLen)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(captchaChars))))
		if err != nil {
			return "", fmt.Errorf("生成验证码失败: %w", err)
		}
		b[i] = captchaChars[n.Int64()]
	}
	return string(b), nil
}

// buildCaptchaSVG 渲染 SVG 验证码（零第三方依赖）：字符随机旋转/错位/配色 +
// 干扰线与噪点；底色透明（由登录页表面色衬托，Light/Dark 自适应）。
// 尺寸 96×38 与前端展示框一致。
func buildCaptchaSVG(code string) string {
	var sb strings.Builder
	sb.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="96" height="38" viewBox="0 0 96 38">`)

	// 干扰线 3 条：随机起止 + 随机弱色。
	for i := 0; i < 3; i++ {
		x1, y1 := randRange(2, 20), randRange(6, 32)
		x2, y2 := randRange(76, 94), randRange(6, 32)
		sb.WriteString(fmt.Sprintf(
			`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1" opacity="0.45"/>`,
			x1, y1, x2, y2, captchaNoiseColor(),
		))
	}

	// 字符：均分横向槽位，随机纵向偏移 ±3、旋转 ±24°、大小 20~24。
	slot := 96 / captchaCodeLen
	for i, ch := range code {
		size := 20 + randRange(0, 4)
		x := i*slot + slot/2 + randRange(-3, 3)
		y := 25 + randRange(-3, 3)
		rot := randRange(-24, 24)
		sb.WriteString(fmt.Sprintf(
			`<text x="%d" y="%d" font-family="Arial,Helvetica,sans-serif" font-size="%d" font-weight="700" fill="%s" text-anchor="middle" transform="rotate(%d %d %d)">%s</text>`,
			x, y, size, captchaGlyphColor(i), rot, x, y, string(ch),
		))
	}

	// 噪点 12 个。
	for i := 0; i < 12; i++ {
		sb.WriteString(fmt.Sprintf(
			`<circle cx="%d" cy="%d" r="1" fill="%s" opacity="0.5"/>`,
			randRange(2, 94), randRange(2, 36), captchaNoiseColor(),
		))
	}
	sb.WriteString(`</svg>`)
	return sb.String()
}

// randRange [min, max] 闭区间随机整数。
func randRange(min, max int) int {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	if err != nil {
		return min
	}
	return min + int(n.Int64())
}

// captchaGlyphColor 字符配色（蓝系主色家族，避免纯黑死板；与主题无关的中间调保证
// Light/Dark 均可读——底色透明、字符取中明度蓝青系）。
func captchaGlyphColor(i int) string {
	colors := []string{"#2563eb", "#0891b2", "#7c3aed", "#0369a1", "#4f46e5", "#0f766e"}
	return colors[i%len(colors)]
}

// captchaNoiseColor 干扰元素弱色。
func captchaNoiseColor() string {
	colors := []string{"#94a3b8", "#cbd5e1", "#a5b4fc"}
	return colors[randRange(0, len(colors)-1)]
}
