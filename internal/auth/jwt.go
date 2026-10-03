package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/stockflow/server/internal/response"
)

// Access JWT（plan §7.2：HS256，claims 含 uid/sid）。
// 仅承载身份指针（uid+sid），权限/数据范围一律以 Redis 会话快照为准（可踢下线、可刷新），
// 避免 2h 内 claims 漂移不可控（plan §13.3 已知限制的控制面）。

// accessClaims JWT 载荷。uid=用户 ID；sid=Redis 会话 ID（sf:session:{sid}）。
type accessClaims struct {
	UID int64  `json:"uid"`
	SID string `json:"sid"`
	jwt.RegisteredClaims
}

// issueAccessToken 签发 HS256 Access Token，返回 token 与过期时刻。
func issueAccessToken(cfg runtimeConfig, uid int64, sid string) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(cfg.accessTTL)
	claims := accessClaims{
		UID: uid,
		SID: sid,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    cfg.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			Subject:   fmt.Sprintf("%d", uid),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(cfg.jwtSecret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("签发 access token 失败: %w", err)
	}
	return token, exp, nil
}

// parseAccessToken 解析并校验 Access Token（算法白名单 + 签名 + 过期 + issuer + exp 必填）。
// 过期与无效分别返回 AUTH_TOKEN_EXPIRED / AUTH_TOKEN_INVALID（permission.md §5：
// 前端请求层据此自动刷新或跳登录）。
func parseAccessToken(cfg runtimeConfig, raw string) (uid int64, sid string, err error) {
	if raw == "" {
		return 0, "", response.NewError(ErrTokenInvalid, nil)
	}
	token, err := jwt.ParseWithClaims(raw, &accessClaims{}, func(t *jwt.Token) (any, error) {
		return cfg.jwtSecret, nil
	},
		jwt.WithValidMethods([]string{"HS256"}), // 拒绝 alg 混淆（none/RS256 伪造）
		jwt.WithIssuer(cfg.issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !token.Valid {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return 0, "", response.NewError(ErrTokenExpired, nil)
		}
		return 0, "", response.NewError(ErrTokenInvalid, nil)
	}
	claims, ok := token.Claims.(*accessClaims)
	if !ok || claims.UID <= 0 || claims.SID == "" {
		return 0, "", response.NewError(ErrTokenInvalid, nil)
	}
	return claims.UID, claims.SID, nil
}
