package idempotency

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/stockflow/server/internal/database"
)

// 仓储层（architecture.md §1：Service → Repository → Database；接口收窄到本域所需，
// 测试以内存替身实现——fakeRepo 项目约定，userpref/purchase 同款分层）。
//
// 并发仲裁的唯一真相源是 DB 唯一索引 uk_idempotency_keys(key,user_id,endpoint)：
// TryInsert 以 INSERT ... ON CONFLICT DO NOTHING 实现「恰一方占用」，应用层不引入
// 锁/检查-再插入等第二套判定（go-dev-standard：不为并发语义制造双真相源）。

// Repo 幂等键仓储。
type Repo interface {
	// TryInsert 尝试占用执行权（INSERT ON CONFLICT DO NOTHING）。返回 true =
	// 本请求占用成功（唯一索引未命中，行以 PROCESSING 落库）；false = 键已被占用。
	TryInsert(ctx context.Context, k *Key) (bool, error)
	// Find 读占用行（占用失败后判定回放/冲突/换载荷）；不存在返回 (nil, nil)。
	Find(ctx context.Context, key string, userID int64, endpoint string) (*Key, error)
	// Complete 占用行落响应快照并置 COMPLETED（守卫 status=PROCESSING——与并发
	// Release 竞态时 0 行静默，快照以先收尾者为准）。
	Complete(ctx context.Context, id, updatedBy int64, snapshot []byte) error
	// Release 释放占用行（仅 PROCESSING；已 COMPLETED 行不受影响）。返回释放行数。
	Release(ctx context.Context, id int64) (int64, error)
	// PreemptExpired 原子抢占滞留 PROCESSING 的过期占用行（TTL 惰性回收唯一路径）。
	// 单条 UPDATE 参数化守卫 id + status=PROCESSING + created_at < cutoff，并发抢占方
	// 恰一方 RowsAffected=1 判胜负；胜出行被改写绑定本次请求（request_hash/updated_by
	// 落库、response_snapshot 归零、created_at 刷新——租约自接管时刻重新起算，接管方
	// 执行期内不会被后续重试再次抢占）。返回 1 = 抢占胜出；0 = 失利（原持有者恰已
	// 收尾成 COMPLETED 或并发方先到）。
	PreemptExpired(ctx context.Context, id, updatedBy int64, requestHash string, cutoff time.Time) (int64, error)
}

// NewRepo GORM 幂等键仓储。
func NewRepo(db *gorm.DB) Repo { return gormRepo{db: db} }

type gormRepo struct{ db *gorm.DB }

func (r gormRepo) TryInsert(ctx context.Context, k *Key) (bool, error) {
	res := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(k)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r gormRepo) Find(ctx context.Context, key string, userID int64, endpoint string) (*Key, error) {
	var row Key
	err := r.db.WithContext(ctx).
		Where("key = ? AND user_id = ? AND endpoint = ?", key, userID, endpoint).
		First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r gormRepo) Complete(ctx context.Context, id, updatedBy int64, snapshot []byte) error {
	return r.db.WithContext(ctx).Model(&Key{}).
		Where("id = ? AND status = ?", id, StatusProcessing).
		Updates(map[string]any{
			"status":            StatusCompleted,
			"response_snapshot": jsonb(snapshot),
			"updated_at":        database.Now(),
			"updated_by":        updatedBy,
		}).Error
}

func (r gormRepo) Release(ctx context.Context, id int64) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("id = ? AND status = ?", id, StatusProcessing).
		Delete(&Key{})
	return res.RowsAffected, res.Error
}

func (r gormRepo) PreemptExpired(ctx context.Context, id, updatedBy int64, requestHash string, cutoff time.Time) (int64, error) {
	// 单条原子 UPDATE：created_at < cutoff 守卫使并发抢占方恰一方胜出（后到方在
	// 行锁上重估谓词时 created_at 已被刷新为 now，判定 0 行）。
	res := r.db.WithContext(ctx).Model(&Key{}).
		Where("id = ? AND status = ? AND created_at < ?", id, StatusProcessing, cutoff).
		Updates(map[string]any{
			"request_hash":      requestHash,
			"response_snapshot": jsonb("{}"), // PROCESSING 态快照归零（000024 列缺省同形）
			"created_at":        database.Now(),
			"updated_at":        database.Now(),
			"updated_by":        updatedBy,
		})
	return res.RowsAffected, res.Error
}
