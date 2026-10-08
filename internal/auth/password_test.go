package auth

// 单元测试：bcrypt 哈希与强密码策略（ask 指定交付项；表驱动）。

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestBcryptRoundtrip(t *testing.T) {
	hash, err := HashPassword("Passw0rd!")
	require.NoError(t, err)
	require.True(t, VerifyPassword(hash, "Passw0rd!"))
	require.False(t, VerifyPassword(hash, "Passw0rd?"))
	require.NotEqual(t, "Passw0rd!", hash) // 禁明文（architecture.md §6）

	cost, err := bcrypt.Cost([]byte(hash))
	require.NoError(t, err)
	require.Equal(t, bcryptCost, cost) // 与生效成本一致（生产=BcryptCost，测试=MinCost）
	require.Equal(t, 12, BcryptCost)   // plan §7.3：生产强度恒为 cost 12
}

func TestPasswordPolicy(t *testing.T) {
	cases := []struct {
		name string
		pw   string
		ok   bool
	}{
		{"正常_字母数字", "Passw0rd", true},
		{"正常_8位下限", "Abcd1234", true},
		{"正常_含Unicode字母", "密码Abc12x", true},
		{"过短", "Ab1x", false},
		{"纯数字", "12345678", false},
		{"纯字母", "abcdefgh", false},
		{"空", "", false},
		{"超72字节", string(make([]byte, 73)) + "a1", false},
		{"恰72字节_满足复杂度", "A1" + strings.Repeat("a0", 35), true}, // 2+70=72
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidatePasswordStrength(c.pw)
			if c.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestPasswordPolicyBoundary72(t *testing.T) {
	// 72 字节且满足复杂度：允许；73 字节：拒绝（bcrypt 输入上限，防静默截断）。
	ok72 := "A1" + string([]byte{0x61, 0x62}[0]) // 3 字节起步
	pw := ok72
	for len(pw) < 72 {
		if len(pw)%2 == 0 {
			pw += "a"
		} else {
			pw += "1"
		}
	}
	require.Len(t, pw, 72)
	require.NoError(t, ValidatePasswordStrength(pw))

	pw73 := pw + "a"
	require.Error(t, ValidatePasswordStrength(pw73))
}

func TestDummyHashTimingSafety(t *testing.T) {
	// 防枚举假哈希是合法 bcrypt 哈希：可正常参与同代价比较（抹平时序），
	// 对任意错误密码不匹配；它不对应任何真实账户，仅用于未知用户登录路径。
	require.False(t, VerifyPassword(dummyHash, "anything"))
	require.True(t, VerifyPassword(dummyHash, "sf-dummy-password"))
}

func TestHashPasswordRejectsWeak(t *testing.T) {
	_, err := HashPassword("short")
	require.Error(t, err)
}
