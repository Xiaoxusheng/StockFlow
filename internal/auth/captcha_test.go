package auth

// 登录图像验证码单测（captcha.go；经 fakeRedis 接口替身，不依赖 Redis/网络）：
// 签发形态 → 正确校验一次性消费 → 错误/过期/重放拒绝 → Redis 故障 fail-closed
// → SVG 形态 sanity（字符数/尺寸/data URL 前缀）。

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// issueCaptcha 签发并返回 id 与明文答案（测试需读答案：fakeRedis 直读键值）。
func issueCaptcha(t *testing.T, svc *Service, rdb *fakeRedis) (id, code string) {
	t.Helper()
	ch, err := svc.IssueLoginCaptcha(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, ch.CaptchaID)
	require.NotEmpty(t, ch.Image)
	require.Positive(t, ch.ExpiresIn)
	raw := rdb.val(captchaKey(ch.CaptchaID))
	require.NotEmpty(t, raw, "验证码答案必须落 Redis")
	return ch.CaptchaID, raw
}

func TestCaptchaIssueShape(t *testing.T) {
	svc, _, rdb := newServiceFixture(t)
	ch, err := svc.IssueLoginCaptcha(context.Background())
	require.NoError(t, err)
	require.Len(t, ch.CaptchaID, 43) // newOpaqueToken：32 字节 → base64url 43 字符
	require.True(t, strings.HasPrefix(ch.Image, "data:image/svg+xml;base64,"))
	require.EqualValues(t, defaultCaptchaTTL/time.Second, ch.ExpiresIn)

	// SVG sanity：解码后含 4 个字符 glyph、尺寸 96x38。
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ch.Image, "data:image/svg+xml;base64,"))
	require.NoError(t, err)
	svg := string(raw)
	require.Contains(t, svg, `width="96"`)
	require.Contains(t, svg, `height="38"`)
	require.Equal(t, captchaCodeLen, strings.Count(svg, "<text"))
	// 答案落在无易混淆字符集内。
	code := rdb.val(captchaKey(ch.CaptchaID))
	require.NotEmpty(t, code)
	for _, ch := range code {
		require.Contains(t, captchaChars, string(ch))
	}
}

func TestCaptchaVerifyConsumesOnce(t *testing.T) {
	svc, _, rdb := newServiceFixture(t)
	id, code := issueCaptcha(t, svc, rdb)

	// 首次校验通过并消费。
	require.NoError(t, svc.VerifyLoginCaptcha(context.Background(), id, code))
	require.False(t, rdb.has(captchaKey(id)), "校验后必须删除（一次性）")

	// 重放（同 id 同答案）必须拒绝。
	err := svc.VerifyLoginCaptcha(context.Background(), id, code)
	require.Equal(t, "AUTH_CAPTCHA_INVALID", codeOf(t, err))
}

func TestCaptchaVerifyWrongAndMissing(t *testing.T) {
	svc, _, rdb := newServiceFixture(t)
	id, code := issueCaptcha(t, svc, rdb)

	// 错误答案：拒绝且消费（防同一验证码批量试答案）。
	err := svc.VerifyLoginCaptcha(context.Background(), id, strings.Repeat("A", len(code)))
	require.Equal(t, "AUTH_CAPTCHA_INVALID", codeOf(t, err))
	require.False(t, rdb.has(captchaKey(id)), "错误尝试后验证码必须作废")

	// 空参 / 未知 id。
	err = svc.VerifyLoginCaptcha(context.Background(), "", code)
	require.Equal(t, "AUTH_CAPTCHA_INVALID", codeOf(t, err))
	err = svc.VerifyLoginCaptcha(context.Background(), "unknown-id", code)
	require.Equal(t, "AUTH_CAPTCHA_INVALID", codeOf(t, err))
}

func TestCaptchaRedisFailureFailClosed(t *testing.T) {
	svc, _, rdb := newServiceFixture(t)
	id, code := issueCaptcha(t, svc, rdb)

	// Get 故障注入：校验必须拒绝（fail-closed，验证码是安全闸不是限流计数）。
	rdb.errGet = true
	err := svc.VerifyLoginCaptcha(context.Background(), id, code)
	require.Equal(t, "COMMON_INTERNAL_ERROR", codeOf(t, err))
	rdb.errGet = false

	// 签发故障：Set 失败 → 内部错误，不返回半截凭证。
	rdb.errSet = true
	_, err = svc.IssueLoginCaptcha(context.Background())
	require.Error(t, err)
}

func TestCaptchaCodeCaseInsensitive(t *testing.T) {
	svc, _, rdb := newServiceFixture(t)
	id, code := issueCaptcha(t, svc, rdb)
	// 用户输入小写：规范化为大写后比对。
	require.NoError(t, svc.VerifyLoginCaptcha(context.Background(), id, strings.ToLower(code)))
}
