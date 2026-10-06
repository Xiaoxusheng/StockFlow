package idempotency

import (
	"context"

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
