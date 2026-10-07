package idempotency

// 测试替身：内存版 Repo（fakeRepo 项目约定，无 PostgreSQL/Redis/网络依赖）。
// TryInsert 以互斥锁内的 check-and-insert 忠实模拟 DB 唯一索引仲裁语义
//（uk_idempotency_keys：同复合键并发插入恰一方成功），并发测试（-race）据此断言
// 「并发同键只允许一个执行」；Complete/Release/PreemptExpired 的 PROCESSING 守卫
// 与 GORM 实现同形；TryInsert 落 created_at/updated_at=now（忠实模拟 GORM
// autoCreateTime / 列 DEFAULT now()——TTL 惰性回收判定依赖该列，零值时间会被
// 误判为过期租约，必须与真实落库行为同形）。

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/stockflow/server/internal/database"
)

type fakeRepo struct {
	mu   sync.Mutex
	rows map[string]*Key // 复合键 "key|user|endpoint" → 行
	seq  int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{rows: map[string]*Key{}}
}

func compositeKey(key string, userID int64, endpoint string) string {
	return key + "|" + strconv.FormatInt(userID, 10) + "|" + endpoint
}

func (r *fakeRepo) TryInsert(_ context.Context, k *Key) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ck := compositeKey(k.Key, k.UserID, k.Endpoint)
	if _, ok := r.rows[ck]; ok {
		return false, nil // 唯一索引命中：占用失败
	}
	r.seq++
	k.ID = r.seq
	k.CreatedAt = database.Now() // GORM autoCreateTime 同形（惰性回收 TTL 判定依据）
	k.UpdatedAt = k.CreatedAt
	cp := *k
	r.rows[ck] = &cp
	return true, nil
}

func (r *fakeRepo) Find(_ context.Context, key string, userID int64, endpoint string) (*Key, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, ok := r.rows[compositeKey(key, userID, endpoint)]
	if !ok {
		return nil, nil
	}
	cp := *row
	return &cp, nil
}

func (r *fakeRepo) Complete(_ context.Context, id, updatedBy int64, snapshot []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for ck, row := range r.rows {
		if row.ID == id && row.Status == StatusProcessing {
			row.Status = StatusCompleted
			row.ResponseSnapshot = append(jsonb(nil), snapshot...)
			row.UpdatedBy = updatedBy
			r.rows[ck] = row
			return nil
		}
	}
	return nil // 守卫 0 行（已释放/已收尾）静默——与 GORM 实现同语义
}

func (r *fakeRepo) Release(_ context.Context, id int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for ck, row := range r.rows {
		if row.ID == id && row.Status == StatusProcessing {
			delete(r.rows, ck)
			return 1, nil
		}
	}
	return 0, nil
}

func (r *fakeRepo) PreemptExpired(_ context.Context, id, updatedBy int64, requestHash string, cutoff time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for ck, row := range r.rows {
		// 守卫三条件（id + status + created_at < cutoff）与 GORM 实现同形；
		// 胜出行改写绑定本次请求、租约自接管时刻重新起算。
		if row.ID == id && row.Status == StatusProcessing && row.CreatedAt.Time.Before(cutoff) {
			row.RequestHash = requestHash
			row.ResponseSnapshot = jsonb("{}")
			row.CreatedAt = database.Now()
			row.UpdatedAt = row.CreatedAt
			row.UpdatedBy = updatedBy
			r.rows[ck] = row
			return 1, nil
		}
	}
	return 0, nil // 守卫失利（已收尾/已被并发方接管/未超时）：0 行与 GORM 同语义
}

// backdateCreatedAt 将指定占用行的 created_at 回拨 d（模拟收尾失败/硬崩溃后滞留
// PROCESSING 的占用行——TTL 惰性回收测试入口）。
func (r *fakeRepo) backdateCreatedAt(key string, userID int64, endpoint string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if row, ok := r.rows[compositeKey(key, userID, endpoint)]; ok {
		row.CreatedAt = database.JSONTime{Time: time.Now().Add(-d)}
		r.rows[compositeKey(key, userID, endpoint)] = row
	}
}

// Compile-time 接口守卫。
var _ Repo = (*fakeRepo)(nil)
