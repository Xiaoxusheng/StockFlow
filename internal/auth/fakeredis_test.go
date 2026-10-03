package auth

// 测试替身：内存版 redisStore（ask 约束：单测不依赖 Redis/网络，用接口替身）。
// 仅实现本域用到的六条命令语义：GET/SET/DEL/INCR/EXPIRE/SCAN。

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type fakeRedis struct {
	mu      sync.Mutex
	str     map[string]string
	ttl     map[string]time.Duration
	expired map[string]int // Expire 调用计数
	seq     int64          // SCAN 稳定序
	errGet  bool           // 注入故障：Get 失败（fail-closed 路径用）
	errSet  bool
	errIncr bool // 注入故障：Incr 失败（登录失败计数 fail-open 路径用）
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{str: map[string]string{}, ttl: map[string]time.Duration{}, expired: map[string]int{}}
}

func (f *fakeRedis) Get(_ context.Context, key string) *redis.StringCmd {
	cmd := redis.NewStringCmd(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errGet {
		cmd.SetErr(fmt.Errorf("fake redis: get 故障注入"))
		return cmd
	}
	v, ok := f.str[key]
	if !ok {
		cmd.SetErr(redis.Nil)
		return cmd
	}
	cmd.SetVal(v)
	return cmd
}

func (f *fakeRedis) Set(_ context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errSet {
		cmd.SetErr(fmt.Errorf("fake redis: set 故障注入"))
		return cmd
	}
	var raw string
	switch v := value.(type) {
	case string:
		raw = v
	case []byte:
		raw = string(v)
	default:
		b, err := json.Marshal(value)
		if err != nil {
			cmd.SetErr(err)
			return cmd
		}
		raw = string(b)
	}
	f.str[key] = raw
	f.ttl[key] = expiration
	cmd.SetVal("OK")
	return cmd
}

func (f *fakeRedis) Del(_ context.Context, keys ...string) *redis.IntCmd {
	cmd := redis.NewIntCmd(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	n := int64(0)
	for _, k := range keys {
		if _, ok := f.str[k]; ok {
			delete(f.str, k)
			delete(f.ttl, k)
			n++
		}
	}
	cmd.SetVal(n)
	return cmd
}

func (f *fakeRedis) Incr(_ context.Context, key string) *redis.IntCmd {
	cmd := redis.NewIntCmd(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errIncr {
		cmd.SetErr(fmt.Errorf("fake redis: incr 故障注入"))
		return cmd
	}
	cur := int64(0)
	if v, ok := f.str[key]; ok {
		fmt.Sscanf(v, "%d", &cur)
	}
	cur++
	f.str[key] = fmt.Sprintf("%d", cur)
	cmd.SetVal(cur)
	return cmd
}

func (f *fakeRedis) Expire(_ context.Context, key string, expiration time.Duration) *redis.BoolCmd {
	cmd := redis.NewBoolCmd(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.str[key]; !ok {
		cmd.SetVal(false)
		return cmd
	}
	f.ttl[key] = expiration
	f.expired[key]++
	cmd.SetVal(true)
	return cmd
}

func (f *fakeRedis) Scan(_ context.Context, _ uint64, match string, _ int64) *redis.ScanCmd {
	cmd := redis.NewScanCmd(context.Background(), nil) // 内存替身只经 SetVal 返回，cmdable 永不执行
	f.mu.Lock()
	keys := make([]string, 0, len(f.str))
	for k := range f.str {
		if ok, _ := pathMatch(match, k); ok {
			keys = append(keys, k)
		}
	}
	f.mu.Unlock()
	sort.Strings(keys)
	cmd.SetVal(keys, 0) // 单页返回（内存替身无游标）
	return cmd
}

// pathMatch 极简 glob（仅支持前缀*），满足 sf:session:* 匹配。
func pathMatch(pattern, s string) (bool, error) {
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(s, strings.TrimSuffix(pattern, "*")), nil
	}
	return pattern == s, nil
}

// ---- 断言辅助 ----

func (f *fakeRedis) has(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.str[key]
	return ok
}

func (f *fakeRedis) val(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.str[key]
}

func (f *fakeRedis) intVal(key string) int64 {
	var n int64
	fmt.Sscanf(f.val(key), "%d", &n)
	return n
}
