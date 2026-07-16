package repo

import (
	"time"

	"gorm.io/gorm"

	"nexus-panel/internal/model"
)

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
	return r.db.Save(a).Error
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
func (r *AdminRepo) UpdateLastLogin(id, ip string, t time.Time) error {
	return r.db.Model(&model.Admin{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"last_login_at": t,
			"last_login_ip": ip,
		}).Error
}

// GetDB 暴露底层 db 句柄
func (r *AdminRepo) GetDB() *gorm.DB {
	return r.db
}
