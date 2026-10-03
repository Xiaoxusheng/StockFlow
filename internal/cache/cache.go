// Package cache go-redis v9 客户端与键规范助手（backend-m1-plan §2）。
// 键规范：sf:{module}:{key}，如 sf:session:{sid}、sf:loginfail:{username}（plan §7.2/§7.3）。
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/stockflow/server/internal/config"
)

// ErrDisabled redis 关闭（cfg.Enabled=false）时助手返回，调用方可 errors.Is 判断。
var ErrDisabled = errors.New("cache: redis 未启用")

// New 创建 go-redis v9 客户端；cfg.Enabled=false 返回 (nil, nil)（调用方按可 nil 处理）。
// 创建后立即 Ping（deployment.md §3：失败快速报错退出）。
func New(cfg config.RedisConfig) (*redis.Client, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	cli := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	if err := cli.Ping(ctx).Err(); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("Redis 连通性检查失败: %w", err)
	}
	return cli, nil
}

// pingTimeout 启动连通性检查超时。
const pingTimeout = 3 * time.Second

// Key 键规范 sf:{module}:{key}。
func Key(module, key string) string {
	return "sf:" + module + ":" + key
}

// GetJSON 取值并反序列化；键不存在返回 (false, nil)。cli 为 nil 时返回 ErrDisabled。
func GetJSON(ctx context.Context, cli *redis.Client, key string, dest any) (bool, error) {
	if cli == nil {
		return false, ErrDisabled
	}
	raw, err := cli.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(raw), dest); err != nil {
		return false, fmt.Errorf("cache: 反序列化 %s 失败: %w", key, err)
	}
	return true, nil
}

// SetJSON 序列化写入；ttl<=0 表示不过期（除会话外的键一律显式 TTL，谨慎使用不过期）。
func SetJSON(ctx context.Context, cli *redis.Client, key string, val any, ttl time.Duration) error {
	if cli == nil {
		return ErrDisabled
	}
	raw, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return cli.Set(ctx, key, raw, ttl).Err()
}

// Incr 原子自增（登录失败计数等），配合 cli.Expire 设置 TTL 使用。
func Incr(ctx context.Context, cli *redis.Client, key string) (int64, error) {
	if cli == nil {
		return 0, ErrDisabled
	}
	return cli.Incr(ctx, key).Result()
}
