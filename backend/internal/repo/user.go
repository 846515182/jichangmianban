package repo

import (
	"time"

	"gorm.io/gorm"

	"nexus-panel/internal/model"
)

// UserRepo 用户仓储
type UserRepo struct {
	db *gorm.DB
}

// NewUserRepo 创建用户仓储
func NewUserRepo(db *gorm.DB) *UserRepo {
	return &UserRepo{db: db}
}

// GetByID 按 ID 查询(过滤软删除)
func (r *UserRepo) GetByID(id string) (*model.User, error) {
	var u model.User
	if err := r.db.Where("id = ? AND is_deleted = false", id).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// GetByUsername 按用户名查询
func (r *UserRepo) GetByUsername(username string) (*model.User, error) {
	var u model.User
	if err := r.db.Where("username = ? AND is_deleted = false", username).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// GetByEmail 按邮箱查询
func (r *UserRepo) GetByEmail(email string) (*model.User, error) {
	var u model.User
	if err := r.db.Where("email = ? AND is_deleted = false", email).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// List 分页查询(支持关键字搜索)
func (r *UserRepo) List(page, size int, keyword string) ([]model.User, int64, error) {
	var list []model.User
	var total int64
	q := r.db.Model(&model.User{}).Where("is_deleted = false")
	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("username LIKE ? OR email LIKE ? OR remark LIKE ?", like, like, like)
	}
	q.Count(&total)
	if err := q.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// Create 创建用户
func (r *UserRepo) Create(u *model.User) error {
	return r.db.Create(u).Error
}

// CreateBatch 批量创建用户(导入)
func (r *UserRepo) CreateBatch(users []*model.User) error {
	if len(users) == 0 {
		return nil
	}
	return r.db.CreateInBatches(users, 100).Error
}

// Update 更新用户
func (r *UserRepo) Update(u *model.User) error {
	return r.db.Save(u).Error
}

// SoftDelete 软删除
func (r *UserRepo) SoftDelete(id string) error {
	// 用户名追加时间戳后缀以释放唯一索引，同时 status=disabled 阻止订阅拉取
	return r.db.Model(&model.User{}).Where("id = ? AND is_deleted = false", id).
		Updates(map[string]interface{}{
			"is_deleted": true,
			"status":     "disabled",
			"username":   gorm.Expr("username || '_del_' || to_char(now(), 'YYYYMMDDHH24MISS')"),
		}).Error
}

// ResetTraffic 重置流量
// 同时将 status 从 traffic_exhausted 恢复为 active，否则用户即使流量清零也无法使用代理
// (ListActiveForPlans 仅返回 status='active' 的用户，超额用户不会下发到节点)
func (r *UserRepo) ResetTraffic(id string) error {
	return r.db.Model(&model.User{}).Where("id = ? AND is_deleted = false", id).
		Updates(map[string]interface{}{
			"traffic_used":   0,
			"upload_bytes":   0,
			"download_bytes": 0,
			"status":         "active",
		}).Error
}

// UpdateStatus 更新状态(禁用/启用)
func (r *UserRepo) UpdateStatus(id, status string) error {
	return r.db.Model(&model.User{}).Where("id = ? AND is_deleted = false", id).
		Update("status", status).Error
}

// AddTraffic 增加用户流量统计(原子操作)
func (r *UserRepo) AddTraffic(id string, upload, download int64) error {
	return r.db.Model(&model.User{}).Where("id = ? AND is_deleted = false", id).
		Updates(map[string]interface{}{
			"upload_bytes":   gorm.Expr("upload_bytes + ?", upload),
			"download_bytes": gorm.Expr("download_bytes + ?", download),
			"traffic_used":   gorm.Expr("traffic_used + ?", upload+download),
		}).Error
}

// CountByExpired 统计已过期用户数
func (r *UserRepo) CountByExpired(now time.Time) (int64, error) {
	var n int64
	err := r.db.Model(&model.User{}).
		Where("is_deleted = false AND expired_at IS NOT NULL AND expired_at < ?", now).
		Count(&n).Error
	return n, err
}

// CountActive 统计活跃用户数
func (r *UserRepo) CountActive() (int64, error) {
	var n int64
	err := r.db.Model(&model.User{}).Where("is_deleted = false AND status = 'active'").Count(&n).Error
	return n, err
}

// ListActive 查询所有未删除、active、未过期、未超额的用户(节点凭证下发用)
// 修复 BIZ-FATAL-01: 原实现不过滤 expired_at 和流量超限，导致到期用户仍可连接代理
func (r *UserRepo) ListActive() ([]model.User, error) {
	var list []model.User
	err := r.db.Where("is_deleted = false AND status = 'active'").
		Where("expired_at IS NULL OR expired_at > NOW()").
		Where("traffic_limit = 0 OR traffic_used < traffic_limit").
		Find(&list).Error
	return list, err
}

// ListActiveForPlans 查询 plan_id 命中给定列表的活跃用户(用于 node_plan_bindings)
// 用户状态过滤: 未删除 / active / 未过期 / 未超额
func (r *UserRepo) ListActiveForPlans(planIDs []string) ([]model.User, error) {
	if len(planIDs) == 0 {
		return nil, nil
	}
	var list []model.User
	err := r.db.Where("is_deleted = false AND status = 'active' AND plan_id IN ?", planIDs).
		Where("expired_at IS NULL OR expired_at > NOW()").
		Where("traffic_limit = 0 OR traffic_used < traffic_limit").
		Find(&list).Error
	return list, err
}

// ExpireOverdueUsers 将已过期但 status 仍为 active 的用户标记为 expired
// 供定时任务调用，修复 BIZ-FATAL-01
func (r *UserRepo) ExpireOverdueUsers(now time.Time) (int64, error) {
	result := r.db.Model(&model.User{}).
		Where("is_deleted = false AND status = 'active'").
		Where("expired_at IS NOT NULL AND expired_at < ?", now).
		Update("status", "expired")
	return result.RowsAffected, result.Error
}

// MarkAllTrafficExhausted [BIZ-FATAL-02 fix 2026-07-16]
// 兜底检测所有超额用户: status='active' 且 traffic_limit>0 且 traffic_used>=traffic_limit
// 把它们标记为 traffic_exhausted, 配合 ListActiveForPlans 的过滤
// 实现"超额自动停服", 即使节点 agent 上报缺失/异常也能兜底。
func (r *UserRepo) MarkAllTrafficExhausted() (int64, error) {
	result := r.db.Exec(`
		UPDATE users
		SET status = 'traffic_exhausted', updated_at = NOW()
		WHERE is_deleted = false
		  AND status = 'active'
		  AND traffic_limit > 0
		  AND traffic_used >= traffic_limit
	`)
	return result.RowsAffected, result.Error
}

// ListByIDs 按 ID 列表批量查询用户
func (r *UserRepo) ListByIDs(ids []string) ([]model.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var list []model.User
	err := r.db.Where("is_deleted = false AND id IN ?", ids).Find(&list).Error
	return list, err
}

// AddTrafficTx 在指定事务内累加用户流量统计
func (r *UserRepo) AddTrafficTx(tx *gorm.DB, id string, upload, download int64) error {
	return tx.Model(&model.User{}).Where("id = ? AND is_deleted = false", id).
		Updates(map[string]interface{}{
			"upload_bytes":   gorm.Expr("upload_bytes + ?", upload),
			"download_bytes": gorm.Expr("download_bytes + ?", download),
			"traffic_used":   gorm.Expr("traffic_used + ?", upload+download),
		}).Error
}

// CountAll 统计全部用户数
func (r *UserRepo) CountAll() (int64, error) {
	var n int64
	err := r.db.Model(&model.User{}).Where("is_deleted = false").Count(&n).Error
	return n, err
}


// CreateTx [S9 fix 2026-07-14] 在指定事务内创建用户
func (r *UserRepo) CreateTx(tx *gorm.DB, u *model.User) error {
	return tx.Create(u).Error
}


// GetDB [S9 fix 2026-07-14] 暴露底层 db 句柄, 用于 service 层传入事务
func (r *UserRepo) GetDB() *gorm.DB {
	return r.db
}

// CreateInDB [S9 fix 2026-07-14] 在指定 db (可传事务) 内创建用户
func (r *UserRepo) CreateInDB(db *gorm.DB, u *model.User) error {
	if db == nil {
		db = r.db
	}
	return db.Create(u).Error
}
