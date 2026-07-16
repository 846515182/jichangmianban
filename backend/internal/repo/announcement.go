package repo

import (
	"gorm.io/gorm"

	"nexus-panel/internal/model"
)

// AnnouncementRepo 公告仓储
type AnnouncementRepo struct {
	db *gorm.DB
}

// NewAnnouncementRepo 创建公告仓储
func NewAnnouncementRepo(db *gorm.DB) *AnnouncementRepo {
	return &AnnouncementRepo{db: db}
}

// ListPublished 查询已发布公告(置顶优先，按发布时间倒序)
func (r *AnnouncementRepo) ListPublished(limit int) ([]model.Announcement, error) {
	var list []model.Announcement
	q := r.db.Where("is_deleted = false").Order("is_pinned DESC, published_at DESC, created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&list).Error; err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Pinned = list[i].IsPinned
	}
	return list, nil
}

// List 分页查询全部公告
func (r *AnnouncementRepo) List(page, size int) ([]model.Announcement, int64, error) {
	var list []model.Announcement
	var total int64
	q := r.db.Model(&model.Announcement{}).Where("is_deleted = false")
	q.Count(&total)
	if err := q.Order("is_pinned DESC, created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	for i := range list {
		list[i].Pinned = list[i].IsPinned
	}
	return list, total, nil
}

// Create 创建公告
func (r *AnnouncementRepo) Create(a *model.Announcement) error {
	a.Pinned = a.IsPinned
	return r.db.Create(a).Error
}

// GetByID 按 ID 查询(过滤软删除)
func (r *AnnouncementRepo) GetByID(id string) (*model.Announcement, error) {
	var a model.Announcement
	if err := r.db.Where("id = ? AND is_deleted = false", id).First(&a).Error; err != nil {
		return nil, err
	}
	a.Pinned = a.IsPinned
	return &a, nil
}

// Update 更新公告
func (r *AnnouncementRepo) Update(a *model.Announcement) error {
	a.Pinned = a.IsPinned
	return r.db.Save(a).Error
}

// SoftDelete 软删除公告
func (r *AnnouncementRepo) SoftDelete(id string) error {
	return r.db.Model(&model.Announcement{}).Where("id = ? AND is_deleted = false", id).
		Update("is_deleted", true).Error
}

// SetPinned 设置/取消置顶
func (r *AnnouncementRepo) SetPinned(id string, pinned bool) error {
	return r.db.Model(&model.Announcement{}).Where("id = ? AND is_deleted = false", id).
		Update("is_pinned", pinned).Error
}
