package auth

// 单元测试：JWT 签发 / 过期 / 篡改（ask 指定交付项；不依赖数据库与网络）。

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestJWTRoundtrip(t *testing.T) {
	cfg := testCfg()
	token, exp, err := issueAccessToken(cfg, 42, "sid-abc")
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.WithinDuration(t, time.Now().Add(cfg.accessTTL), exp, time.Minute)

	uid, sid, err := parseAccessToken(cfg, token)
	require.NoError(t, err)
	require.EqualValues(t, 42, uid)
	require.Equal(t, "sid-abc", sid)
}

func TestJWTExpired(t *testing.T) {
	cfg := testCfg()
	cfg.accessTTL = -time.Minute // 已过期
	token, _, err := issueAccessToken(cfg, 1, "sid-1")
	require.NoError(t, err)

	_, _, err = parseAccessToken(cfg, token)
	require.Error(t, err)
	require.Equal(t, "AUTH_TOKEN_EXPIRED", codeOf(t, err))
}

func TestJWTTampered(t *testing.T) {
	cfg := testCfg()
	token, _, err := issueAccessToken(cfg, 1, "sid-1")
	require.NoError(t, err)

	// 篡改签名段中段字符（避开 base64 尾部 padding 位不改变解码字节的陷阱）。
	b := []byte(token)
	mid := len(b) * 3 / 4
	orig := b[mid]
	if orig == 'A' {
		b[mid] = 'B'
	} else {
		b[mid] = 'A'
	}
	_, _, err = parseAccessToken(cfg, string(b))
	require.Error(t, err)
	require.Equal(t, "AUTH_TOKEN_INVALID", codeOf(t, err))

	// 载荷篡改（伪造 uid）：签名不匹配，必须拒绝。
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	payload := []byte(parts[1])
	payload[len(payload)-3] ^= 0x01
	forged := parts[0] + "." + string(payload) + "." + parts[2]
	_, _, err = parseAccessToken(cfg, forged)
	require.Error(t, err)
	require.Equal(t, "AUTH_TOKEN_INVALID", codeOf(t, err))
}

func TestJWTWrongSecret(t *testing.T) {
	token, _, err := issueAccessToken(testCfg(), 1, "sid-1")
	require.NoError(t, err)

	other := testCfg()
	other.jwtSecret = []byte("another-secret-0123456789abcdef")
	_, _, err = parseAccessToken(other, token)
	require.Error(t, err)
	require.Equal(t, "AUTH_TOKEN_INVALID", codeOf(t, err))
}

func TestJWTAlgConfusionRejected(t *testing.T) {
	cfg := testCfg()
	claims := accessClaims{UID: 1, SID: "sid-1", RegisteredClaims: jwt.RegisteredClaims{
		Issuer:    cfg.issuer,
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}}

	// HS512（同为 HMAC 但不在白名单）必须拒绝。
	tok512 := jwt.NewWithClaims(jwt.SigningMethodHS512, claims)
	s512, err := tok512.SignedString(cfg.jwtSecret)
	require.NoError(t, err)
	_, _, err = parseAccessToken(cfg, s512)
	require.Equal(t, "AUTH_TOKEN_INVALID", codeOf(t, err))

	// alg=none 必须拒绝。
	tokNone := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	sNone, err := tokNone.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)
	_, _, err = parseAccessToken(cfg, sNone)
	require.Equal(t, "AUTH_TOKEN_INVALID", codeOf(t, err))
}

func TestJWTMissingClaimsRejected(t *testing.T) {
	cfg := testCfg()
	// 缺 sid 的合法签名 token：uid/sid 指针不完整即拒绝（会话双轨依赖 sid）。
	claims := accessClaims{UID: 1, RegisteredClaims: jwt.RegisteredClaims{
		Issuer:    cfg.issuer,
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := token.SignedString(cfg.jwtSecret)
	require.NoError(t, err)
	_, _, err = parseAccessToken(cfg, s)
	require.Equal(t, "AUTH_TOKEN_INVALID", codeOf(t, err))
}

func TestJWTEmptyAndGarbage(t *testing.T) {
	cfg := testCfg()
	_, _, err := parseAccessToken(cfg, "")
	require.Equal(t, "AUTH_TOKEN_INVALID", codeOf(t, err))

	_, _, err = parseAccessToken(cfg, strings.Repeat("x", 64))
	require.Equal(t, "AUTH_TOKEN_INVALID", codeOf(t, err))
}
