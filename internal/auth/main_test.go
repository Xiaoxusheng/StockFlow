package auth

// 测试包统一下调 bcrypt 成本（唯一的包级测试装配点）。
//
// 原因：cost 12 单次哈希在 -race 下约 8s——race 插桩会记录 blowfish 的每一次 S 盒访问，
// 把这块 CPU 密集代码放慢一个量级。auth 包有 70+ 次 seedUser/HashPassword 与登录比较，
// 累计超过 `go test` 默认的 10m 包超时（CI test 关卡实测 604s 超时，lint 之外的第二个红）。
//
// 生产强度不受影响：BcryptCost 仍为 12，password_test.go 显式断言该常量，
// HashPassword 的生效成本断言改为比对 bcryptCost。

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestMain(m *testing.M) {
	bcryptCost = bcrypt.MinCost
	// dummyHash 在包变量初始化时按 cost 12 生成，这里按低成本重建，
	// 使「未知用户登录」路径（service_auth.go 的防枚举比较）在测试中同样廉价。
	dummyHash = mustDummyHash()
	os.Exit(m.Run())
}
