package userpref

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 仓储层：接口 + GORM 实现（Service 不感知 gin/gorm，devices/purchase 同款分层）。
// 接口收窄到本域所需，测试以内存 fake 实现替身（fakedb/fakeRepo 项目约定）。

// ViewRepo 保存视图仓储。所有方法强制携带 userID——用户隔离硬规则在仓储签名层面
// 固化，杜绝调用方遗漏（计划 §2.2）。
type ViewRepo interface {
	// ListByPage 同页签视图列表（默认视图置顶，余按更新时间倒序）。
	ListByPage(ctx context.Context, userID int64, pageKey string) ([]SavedView, error)
	// GetByID 按 (id, user_id) 取行；不存在（含非本人）返回 (nil, nil)。
	GetByID(ctx context.Context, userID, id int64) (*SavedView, error)
	// NameExists 同 (user_id, page_key) 下重名检查（excludeID>0 时排除自身，改名场景）。
	NameExists(ctx context.Context, userID int64, pageKey, name string, excludeID int64) (bool, error)
	// Create 新建（含 is_default=true 场景，服务层已在同事务内清旧默认）。
	Create(ctx context.Context, v *SavedView) error
	// Update 全量保存已加载并修改的行。
	Update(ctx context.Context, v *SavedView) error
	// Delete 按 (id, user_id) 删除，返回影响行数。
	Delete(ctx context.Context, userID, id int64) (int64, error)
	// ClearDefault 清除同 (user_id, page_key) 下 excludeID 之外的默认标记
	//（「设为默认」单事务内调用，计划 §2.2）。
	ClearDefault(ctx context.Context, userID int64, pageKey string, excludeID int64) error
	// InTx 事务边界：fn 内操作共用同一事务，出错整体回滚。
	InTx(ctx context.Context, fn func(ViewRepo) error) error
}

// PrefRepo 用户偏好仓储。
type PrefRepo interface {
	// ListByUser 取当前用户偏好；keys 非空时仅取白名单内给定键（键已由服务层校验）。
	ListByUser(ctx context.Context, userID int64, keys []string) ([]Preference, error)
	// Upsert 复合主键冲突即更新（pref_value/updated_at）。
	Upsert(ctx context.Context, p *Preference) error
}

// ---- GORM 实现 ----

type gormViewRepo struct{ db *gorm.DB }

// NewViewRepo GORM 视图仓储。
func NewViewRepo(db *gorm.DB) ViewRepo { return gormViewRepo{db: db} }

func (r gormViewRepo) ListByPage(ctx context.Context, userID int64, pageKey string) ([]SavedView, error) {
	var rows []SavedView
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND page_key = ?", userID, pageKey).
		Order("is_default DESC, updated_at DESC, id DESC").
		Find(&rows).Error
	return rows, err
}

func (r gormViewRepo) GetByID(ctx context.Context, userID, id int64) (*SavedView, error) {
	var row SavedView
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND id = ?", userID, id).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r gormViewRepo) NameExists(ctx context.Context, userID int64, pageKey, name string, excludeID int64) (bool, error) {
	var n int64
	q := r.db.WithContext(ctx).Model(&SavedView{}).
		Where("user_id = ? AND page_key = ? AND name = ?", userID, pageKey, name)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	err := q.Count(&n).Error
	return n > 0, err
}

func (r gormViewRepo) Create(ctx context.Context, v *SavedView) error {
	return r.db.WithContext(ctx).Create(v).Error
}

func (r gormViewRepo) Update(ctx context.Context, v *SavedView) error {
	return r.db.WithContext(ctx).Save(v).Error
}

func (r gormViewRepo) Delete(ctx context.Context, userID, id int64) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("user_id = ? AND id = ?", userID, id).
		Delete(&SavedView{})
	return res.RowsAffected, res.Error
}

func (r gormViewRepo) ClearDefault(ctx context.Context, userID int64, pageKey string, excludeID int64) error {
	return r.db.WithContext(ctx).Model(&SavedView{}).
		Where("user_id = ? AND page_key = ? AND is_default AND id <> ?", userID, pageKey, excludeID).
		Update("is_default", false).Error
}

func (r gormViewRepo) InTx(ctx context.Context, fn func(ViewRepo) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(gormViewRepo{db: tx})
	})
}

type gormPrefRepo struct{ db *gorm.DB }

// NewPrefRepo GORM 偏好仓储。
func NewPrefRepo(db *gorm.DB) PrefRepo { return gormPrefRepo{db: db} }

func (r gormPrefRepo) ListByUser(ctx context.Context, userID int64, keys []string) ([]Preference, error) {
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if len(keys) > 0 {
		q = q.Where("pref_key IN ?", keys)
	}
	var rows []Preference
	err := q.Order("pref_key").Find(&rows).Error
	return rows, err
}

func (r gormPrefRepo) Upsert(ctx context.Context, p *Preference) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "pref_key"}},
			DoUpdates: clause.AssignmentColumns([]string{"pref_value", "updated_at"}),
		}).
		Create(p).Error
}
