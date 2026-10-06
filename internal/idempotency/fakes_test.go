package idempotency

// 测试替身：内存版 Repo（fakeRepo 项目约定，无 PostgreSQL/Redis/网络依赖）。
// TryInsert 以互斥锁内的 check-and-insert 忠实模拟 DB 唯一索引仲裁语义
//（uk_idempotency_keys：同复合键并发插入恰一方成功），并发测试（-race）据此断言
// 「并发同键只允许一个执行」；Complete/Release 的 PROCESSING 守卫与 GORM 实现同形。

import (
	"context"
	"strconv"
	"sync"
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

// Compile-time 接口守卫。
var _ Repo = (*fakeRepo)(nil)
