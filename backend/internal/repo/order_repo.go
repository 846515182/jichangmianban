package repo

import (
	"time"

	"gorm.io/gorm"

	"nexus-panel/internal/model"
)

// OrderRepo 订单仓储
type OrderRepo struct {
	db *gorm.DB
}

// NewOrderRepo 创建订单仓储
func NewOrderRepo(db *gorm.DB) *OrderRepo {
	return &OrderRepo{db: db}
}

// GetByID 按 ID 查询(过滤软删除)
func (r *OrderRepo) GetByID(id string) (*model.Order, error) {
	var o model.Order
	if err := r.db.Where("id = ? AND is_deleted = false", id).First(&o).Error; err != nil {
		return nil, err
	}
	return &o, nil
}

// GetByOrderNo 按订单号查询
func (r *OrderRepo) GetByOrderNo(orderNo string) (*model.Order, error) {
	var o model.Order
	if err := r.db.Where("order_no = ? AND is_deleted = false", orderNo).First(&o).Error; err != nil {
		return nil, err
	}
	return &o, nil
}

// ListByUserID 分页查询用户订单
func (r *OrderRepo) ListByUserID(userID string, page, size int) ([]model.Order, int64, error) {
	var list []model.Order
	var total int64
	q := r.db.Model(&model.Order{}).Where("user_id = ? AND is_deleted = false", userID)
	q.Count(&total)
	if err := q.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAll 分页查询全部订单(支持按状态/用户ID过滤)
func (r *OrderRepo) ListAll(page, size int, status, userID string) ([]model.Order, int64, error) {
	var list []model.Order
	var total int64
	q := r.db.Model(&model.Order{}).Where("is_deleted = false")
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	q.Count(&total)
	if err := q.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// Create 创建订单
func (r *OrderRepo) Create(o *model.Order) error {
	return r.db.Create(o).Error
}

// Update 更新订单
func (r *OrderRepo) Update(o *model.Order) error {
	return r.db.Save(o).Error
}

// UpdateStatus 更新订单状态
func (r *OrderRepo) UpdateStatus(id, status string) error {
	return r.db.Model(&model.Order{}).Where("id = ? AND is_deleted = false", id).
		Update("status", status).Error
}

// SoftDelete 软删除
func (r *OrderRepo) SoftDelete(id string) error {
	return r.db.Model(&model.Order{}).Where("id = ? AND is_deleted = false", id).
		Update("is_deleted", true).Error
}

// DB 返回底层 *gorm.DB(事务用)
func (r *OrderRepo) DB() *gorm.DB {
	return r.db
}

// ListExpired 列出已过期但仍为 pending 的订单
func (r *OrderRepo) ListExpired(now time.Time) ([]model.Order, error) {
	var list []model.Order
	if err := r.db.Where("is_deleted = false AND status = ? AND expired_at < ?", model.OrderStatusPending, now).
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
