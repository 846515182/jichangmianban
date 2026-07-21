package repo

import (
	"time"

	"gorm.io/gorm"

	"nexus-panel/internal/model"
)

// SubscriptionRepo 订阅仓储
type SubscriptionRepo struct {
	db *gorm.DB
}

// NewSubscriptionRepo 创建订阅仓储
func NewSubscriptionRepo(db *gorm.DB) *SubscriptionRepo {
	return &SubscriptionRepo{db: db}
}

// GetByToken 按 sub_token 查询(过滤软删除)
func (r *SubscriptionRepo) GetByToken(token string) (*model.Subscription, error) {
	var s model.Subscription
	if err := r.db.Where("sub_token = ? AND is_deleted = false", token).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

// GetByUserID 按用户 ID 查询订阅
func (r *SubscriptionRepo) GetByUserID(userID string) (*model.Subscription, error) {
	var s model.Subscription
	if err := r.db.Where("user_id = ? AND is_deleted = false", userID).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

// Create 创建订阅
func (r *SubscriptionRepo) Create(s *model.Subscription) error {
	return r.db.Create(s).Error
}

// Update 更新订阅
func (r *SubscriptionRepo) Update(s *model.Subscription) error {
	return r.db.Save(s).Error
}

// DisableByUserID 禁用指定用户的所有订阅（软删除）
func (r *SubscriptionRepo) DisableByUserID(userID string) error {
	return r.db.Model(&model.Subscription{}).
		Where("user_id = ? AND is_deleted = false", userID).
		Update("is_deleted", true).Error
}

// ExistsByToken 判断 token 是否已存在
func (r *SubscriptionRepo) ExistsByToken(token string) (bool, error) {
	var n int64
	err := r.db.Model(&model.Subscription{}).Where("sub_token = ? AND is_deleted = false", token).Count(&n).Error
	return n > 0, err
}

// SubscriptionWithUser 订阅+用户信息(用于管理端列表)
type SubscriptionWithUser struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	SubToken  string     `json:"sub_token"`
	SubType   string     `json:"sub_type"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	Username  string     `json:"username"`
	Email     string     `json:"email"`
	Status    string     `json:"status"`
	ExpiredAt *time.Time `json:"user_expired_at,omitempty"`
}

// List 分页查询所有订阅(带用户信息)
// 修复 P1-repo-软删除过滤: JOIN users 后加 AND users.is_deleted = false,
// 避免查出已软删除用户的订阅(管理端列表会显示孤儿订阅)
func (r *SubscriptionRepo) List(page, size int, keyword string) ([]SubscriptionWithUser, int64, error) {
	var total int64
	q := r.db.Table("subscriptions").
		Select("subscriptions.id, subscriptions.user_id, subscriptions.sub_token, subscriptions.sub_type, subscriptions.expires_at, subscriptions.created_at, users.username, users.email, users.status, users.expired_at").
		Joins("LEFT JOIN users ON users.id = subscriptions.user_id AND users.is_deleted = false").
		Where("subscriptions.is_deleted = false")
	if keyword != "" {
		q = q.Where("users.username ILIKE ? OR users.email ILIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []SubscriptionWithUser
	if err := q.Order("subscriptions.created_at DESC").
		Offset((page - 1) * size).Limit(size).Scan(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
