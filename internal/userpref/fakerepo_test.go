package userpref

// 测试替身：内存版 ViewRepo/PrefRepo（fakeRepo 项目约定，无 PostgreSQL/Redis/网络
// 依赖；同包测试串行复用，禁用 t.Parallel）。InTx 以自身重入模拟事务边界——
// 服务层「清旧默认 → 写新行」的调用次序由此可断言；部分唯一索引兜底属 DB 层语义，
// 不在本替身职责内（T5 以服务层清除结果断言唯一默认）。

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/stockflow/server/internal/database"
)

type fakeViewRepo struct {
	mu      sync.Mutex
	nextID  int64
	rows    map[int64]*SavedView
	txDepth int
}

func newFakeViewRepo() *fakeViewRepo {
	return &fakeViewRepo{rows: map[int64]*SavedView{}}
}

func (r *fakeViewRepo) ListByPage(_ context.Context, userID int64, pageKey string) ([]SavedView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []SavedView
	for _, v := range r.rows {
		if v.UserID == userID && v.PageKey == pageKey {
			out = append(out, *v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDefault != out[j].IsDefault {
			return out[i].IsDefault
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func (r *fakeViewRepo) GetByID(_ context.Context, userID, id int64) (*SavedView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.rows[id]
	if !ok || v.UserID != userID {
		return nil, nil
	}
	cp := *v
	return &cp, nil
}

func (r *fakeViewRepo) NameExists(_ context.Context, userID int64, pageKey, name string, excludeID int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, v := range r.rows {
		if v.UserID == userID && v.PageKey == pageKey && v.Name == name && id != excludeID {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeViewRepo) Create(_ context.Context, v *SavedView) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	v.ID = database.ID(r.nextID)
	now := database.JSONTime{Time: time.Now()}
	v.CreatedAt = now
	v.UpdatedAt = now
	cp := *v
	r.rows[v.ID.Int64()] = &cp
	return nil
}

func (r *fakeViewRepo) Update(_ context.Context, v *SavedView) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.rows[v.ID.Int64()]; !ok {
		return nil
	}
	v.UpdatedAt = database.JSONTime{Time: time.Now()}
	cp := *v
	r.rows[v.ID.Int64()] = &cp
	return nil
}

func (r *fakeViewRepo) Delete(_ context.Context, userID, id int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.rows[id]
	if !ok || v.UserID != userID {
		return 0, nil
	}
	delete(r.rows, id)
	return 1, nil
}

func (r *fakeViewRepo) ClearDefault(_ context.Context, userID int64, pageKey string, excludeID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, v := range r.rows {
		if v.UserID == userID && v.PageKey == pageKey && v.IsDefault && id != excludeID {
			v.IsDefault = false
		}
	}
	return nil
}

func (r *fakeViewRepo) InTx(ctx context.Context, fn func(ViewRepo) error) error {
	r.mu.Lock()
	r.txDepth++
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.txDepth--
		r.mu.Unlock()
	}()
	return fn(r)
}

type fakePrefRepo struct {
	mu   sync.Mutex
	rows map[int64]map[string]Preference
}

func newFakePrefRepo() *fakePrefRepo {
	return &fakePrefRepo{rows: map[int64]map[string]Preference{}}
}

func (r *fakePrefRepo) ListByUser(_ context.Context, userID int64, keys []string) ([]Preference, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	var out []Preference
	for k, p := range r.rows[userID] {
		if len(want) > 0 && !want[k] {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PrefKey < out[j].PrefKey })
	return out, nil
}

func (r *fakePrefRepo) Upsert(_ context.Context, p *Preference) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	perUser, ok := r.rows[p.UserID]
	if !ok {
		perUser = map[string]Preference{}
		r.rows[p.UserID] = perUser
	}
	if prev, ok := perUser[p.PrefKey]; ok {
		p.CreatedAt = prev.CreatedAt
	} else {
		p.CreatedAt = database.JSONTime{Time: time.Now()}
	}
	p.UpdatedAt = database.JSONTime{Time: time.Now()}
	perUser[p.PrefKey] = *p
	return nil
}
