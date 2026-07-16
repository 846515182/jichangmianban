package repo

import (
	"time"

	"gorm.io/gorm"

	"nexus-panel/internal/model"
)

// CouponRepo 优惠券仓储
type CouponRepo struct {
	db *gorm.DB
}

// NewCouponRepo 创建优惠券仓储
func NewCouponRepo(db *gorm.DB) *CouponRepo {
	return &CouponRepo{db: db}
}

// GetByID 按 ID 查询(过滤软删除)
func (r *CouponRepo) GetByID(id string) (*model.Coupon, error) {
	var c model.Coupon
	if err := r.db.Where("id = ? AND is_deleted = false", id).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// GetByCode 按优惠券码查询有效且未过期的优惠券
func (r *CouponRepo) GetByCode(code string) (*model.Coupon, error) {
	var c model.Coupon
	q := r.db.Where("code = ? AND is_deleted = false AND is_enabled = true", code)
	if err := q.First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// List 分页查询
func (r *CouponRepo) List(page, size int, keyword string) ([]model.Coupon, int64, error) {
	var list []model.Coupon
	var total int64
	q := r.db.Model(&model.Coupon{}).Where("is_deleted = false")
	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("code LIKE ?", like)
	}
	q.Count(&total)
	if err := q.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// Create 创建优惠券
func (r *CouponRepo) Create(c *model.Coupon) error {
	return r.db.Create(c).Error
}

// Update 更新优惠券
func (r *CouponRepo) Update(c *model.Coupon) error {
	return r.db.Save(c).Error
}

// SoftDelete 软删除
func (r *CouponRepo) SoftDelete(id string) error {
	return r.db.Model(&model.Coupon{}).Where("id = ? AND is_deleted = false", id).
		Update("is_deleted", true).Error
}

// IncrUsed 增加已使用次数
func (r *CouponRepo) IncrUsed(id string) error {
	result := r.db.Model(&model.Coupon{}).
		Where("id = ? AND is_deleted = false AND is_enabled = true AND (max_uses = 0 OR used_count < max_uses)", id).
		UpdateColumn("used_count", gorm.Expr("used_count + ?", 1))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// IncrUsedSafe 原子增加优惠券使用次数（同时校验过期和次数限制）
func (r *CouponRepo) IncrUsedSafe(id string, now time.Time) error {
	result := r.db.Model(&model.Coupon{}).
		Where("id = ? AND is_deleted = false AND is_enabled = true AND (max_uses = 0 OR used_count < max_uses) AND (expire_at IS NULL OR expire_at > ?)", id, now).
		UpdateColumn("used_count", gorm.Expr("used_count + ?", 1))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// DecrUsedSafe 原子减少优惠券使用次数(取消/退款时回退, 不低于0)
func (r *CouponRepo) DecrUsedSafe(id string) error {
	result := r.db.Model(&model.Coupon{}).
		Where("id = ? AND used_count > 0", id).
		UpdateColumn("used_count", gorm.Expr("used_count - ?", 1))
	if result.Error != nil {
		return result.Error
	}
	return nil
}

// DecrUsedSafeTx 事务内减少优惠券使用次数
func (r *CouponRepo) DecrUsedSafeTx(tx *gorm.DB, id string) error {
	result := tx.Model(&model.Coupon{}).
		Where("id = ? AND used_count > 0", id).
		UpdateColumn("used_count", gorm.Expr("used_count - ?", 1))
	if result.Error != nil {
		return result.Error
	}
	return nil
}

// IncrUsedSafeTx 事务内原子增加优惠券使用次数
func (r *CouponRepo) IncrUsedSafeTx(tx *gorm.DB, id string, now time.Time) error {
	result := tx.Model(&model.Coupon{}).
		Where("id = ? AND is_deleted = false AND is_enabled = true AND (max_uses = 0 OR used_count < max_uses) AND (expire_at IS NULL OR expire_at > ?)", id, now).
		UpdateColumn("used_count", gorm.Expr("used_count + ?", 1))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// IsValid 校验优惠券是否在有效期范围内
func (r *CouponRepo) IsValid(c *model.Coupon, now time.Time) bool {
	if c == nil {
		return false
	}
	if !c.IsEnabled {
		return false
	}
	if c.ExpireAt != nil && c.ExpireAt.Before(now) {
		return false
	}
	if c.MaxUses > 0 && c.UsedCount >= c.MaxUses {
		return false
	}
	return true
}

// ToggleStatus toggles coupon enabled/disabled status
func (r *CouponRepo) ToggleStatus(id string, enabled bool) error {
	return r.db.Model(&model.Coupon{}).Where("id = ? AND is_deleted = false", id).
		Update("is_enabled", enabled).Error
}
