package repo

import (
	"gorm.io/gorm"

	"nexus-panel/internal/model"
)

// LoginAuditRepo 登录审计仓储
type LoginAuditRepo struct {
	db *gorm.DB
}

// NewLoginAuditRepo 创建登录审计仓储
func NewLoginAuditRepo(db *gorm.DB) *LoginAuditRepo {
	return &LoginAuditRepo{db: db}
}

// Create 记录登录审计
func (r *LoginAuditRepo) Create(a *model.LoginAudit) error {
	return r.db.Create(a).Error
}

// List 分页查询登录审计
func (r *LoginAuditRepo) List(page, size int, targetType, targetID string) ([]model.LoginAudit, int64, error) {
	var list []model.LoginAudit
	var total int64
	q := r.db.Model(&model.LoginAudit{})
	if targetType != "" {
		q = q.Where("target_type = ?", targetType)
	}
	if targetID != "" {
		q = q.Where("target_id = ?", targetID)
	}
	q.Count(&total)
	if err := q.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// GetDB 暴露底层 db 句柄
func (r *LoginAuditRepo) GetDB() *gorm.DB {
	return r.db
}