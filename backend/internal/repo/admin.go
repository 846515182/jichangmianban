package repo

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
)

// clearAdminRoleCache 清除管理员 RBAC 角色缓存。
// 兜底: 管理员角色/状态/删除变更后, 即使调用方(handler)忘记清缓存,
// 仓储层也会保证旧 token 在 30s 缓存窗口后无法继续使用 super_admin 权限。
func clearAdminRoleCache(adminID string) {
	if adminID == "" {
		return
	}
	container := app.Get()
	if container == nil || container.RDB == nil {
		return
	}
	_ = container.RDB.Del(context.Background(), fmt.Sprintf("rbac:role:%s", adminID)).Err()
}

// AdminRepo 管理员仓储
type AdminRepo struct {
	db *gorm.DB
}

// NewAdminRepo 创建管理员仓储
func NewAdminRepo(db *gorm.DB) *AdminRepo {
	return &AdminRepo{db: db}
}

// GetByID 按 ID 查询
func (r *AdminRepo) GetByID(id string) (*model.Admin, error) {
	var a model.Admin
	if err := r.db.Where("id = ? AND is_deleted = false", id).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// FindByUsername 按用户名查询(含已删除)
func (r *AdminRepo) FindByUsername(username string) (*model.Admin, error) {
	var a model.Admin
	if err := r.db.Where("username = ?", username).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// GetByUsername 按用户名查询(过滤软删除)
func (r *AdminRepo) GetByUsername(username string) (*model.Admin, error) {
	var a model.Admin
	if err := r.db.Where("username = ? AND is_deleted = false", username).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// Create 创建管理员
func (r *AdminRepo) Create(a *model.Admin) error {
	return r.db.Create(a).Error
}

// Update 更新管理员
func (r *AdminRepo) Update(a *model.Admin) error {
	if err := r.db.Save(a).Error; err != nil {
		return err
	}
	clearAdminRoleCache(a.ID)
	return nil
}

// UpdatePassword 更新密码哈希
func (r *AdminRepo) UpdatePassword(id, passwordHash string) error {
	return r.db.Model(&model.Admin{}).Where("id = ? AND is_deleted = false", id).
		Update("password_hash", passwordHash).Error
}

// UpdateEmail 更新邮箱
func (r *AdminRepo) UpdateEmail(id, email string) error {
	return r.db.Model(&model.Admin{}).Where("id = ? AND is_deleted = false", id).
		Update("email", email).Error
}

// UpdateLastLogin 更新最后登录信息
// 修复 P1-repo-软删除过滤: 加 AND is_deleted = false, 避免为已软删的管理员更新登录信息
func (r *AdminRepo) UpdateLastLogin(id, ip string, t time.Time) error {
	return r.db.Model(&model.Admin{}).Where("id = ? AND is_deleted = false", id).
		Updates(map[string]interface{}{
			"last_login_at": t,
			"last_login_ip": ip,
		}).Error
}

// List 分页查询管理员列表(过滤软删除)
func (r *AdminRepo) List(page, size int, keyword string) ([]model.Admin, int64, error) {
	var list []model.Admin
	var total int64
	q := r.db.Model(&model.Admin{}).Where("is_deleted = false")
	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("username ILIKE ? OR email ILIKE ?", like, like)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := q.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// SoftDelete 软删除管理员
func (r *AdminRepo) SoftDelete(id string) error {
	if err := r.db.Model(&model.Admin{}).Where("id = ? AND is_deleted = false", id).
		Update("is_deleted", true).Error; err != nil {
		return err
	}
	clearAdminRoleCache(id)
	return nil
}

// UpdateStatus 更新管理员状态(active/disabled)
func (r *AdminRepo) UpdateStatus(id, status string) error {
	if err := r.db.Model(&model.Admin{}).Where("id = ? AND is_deleted = false", id).
		Update("status", status).Error; err != nil {
		return err
	}
	clearAdminRoleCache(id)
	return nil
}

// UpdateRole 更新管理员角色
func (r *AdminRepo) UpdateRole(id, role string) error {
	if err := r.db.Model(&model.Admin{}).Where("id = ? AND is_deleted = false", id).
		Update("role", role).Error; err != nil {
		return err
	}
	clearAdminRoleCache(id)
	return nil
}

// UpdateLockUntil 更新管理员锁定时间
func (r *AdminRepo) UpdateLockUntil(id string, lockUntil *time.Time) error {
	return r.db.Model(&model.Admin{}).Where("id = ? AND is_deleted = false", id).
		Update("lock_until", lockUntil).Error
}
