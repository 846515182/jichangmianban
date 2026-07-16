package repo

import (
        "nexus-panel/internal/model"
        "gorm.io/gorm"
)

// AdminActionRepo 管理员操作审计仓储
type AdminActionRepo struct {
        db *gorm.DB
}

func NewAdminActionRepo(db *gorm.DB) *AdminActionRepo {
        return &AdminActionRepo{db: db}
}

func (r *AdminActionRepo) Create(a *model.AdminAction) error {
        return r.db.Create(a).Error
}

func (r *AdminActionRepo) List(page, size int, action, adminID string) ([]model.AdminAction, int64, error) {
        var list []model.AdminAction
        var total int64
        q := r.db.Model(&model.AdminAction{})
        if action != "" {
                q = q.Where("action = ?", action)
        }
        if adminID != "" {
                q = q.Where("admin_id = ?", adminID)
        }
        q.Count(&total)
        if err := q.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
                return nil, 0, err
        }
        return list, total, nil
}

// CleanupBefore 清理指定天数之前的审计日志
// 安全说明: 此函数仅供定时任务使用，必须由超级管理员权限调用
// 禁止暴露给普通管理员或用户接口，防止日志篡改
func (r *AdminActionRepo) CleanupBefore(days int) (int64, error) {
	result := r.db.Where("created_at < NOW() - INTERVAL '1 day' * ?", days).Delete(&model.AdminAction{})
	return result.RowsAffected, result.Error
}
