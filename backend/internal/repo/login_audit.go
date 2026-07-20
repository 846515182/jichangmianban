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

// ListAll 查询所有审计日志(按类型/关键字/成功状态过滤)
// 修复 P1: 旧版只支持 targetType 过滤, 管理端无法搜索 IP/位置, 也无法按成功/失败筛选,
// 前端只能在前端过滤当前页数据, 第 21 条之后的记录永远搜不到。现加 keyword + success 参数。
func (r *LoginAuditRepo) ListAll(targetType, keyword string, success *bool, page, size int) ([]model.LoginAudit, int64, error) {
	var list []model.LoginAudit
	var total int64
	q := r.db.Model(&model.LoginAudit{})
	if targetType != "" {
		q = q.Where("target_type = ?", targetType)
	}
	if keyword != "" {
		// 模糊匹配 IP / location / target_id, ILIKE 大小写不敏感
		q = q.Where("ip ILIKE ? OR location ILIKE ? OR target_id ILIKE ?", "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
	}
	if success != nil {
		q = q.Where("success = ?", *success)
	}
	q.Count(&total)
	if err := q.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListByTarget 按目标类型和ID查询
func (r *LoginAuditRepo) ListByTarget(targetType, targetID string, page, size int) ([]model.LoginAudit, int64, error) {
	return r.List(page, size, targetType, targetID)
}

// GetDB 暴露底层 db 句柄
func (r *LoginAuditRepo) GetDB() *gorm.DB {
	return r.db
}
