package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Referral 邀请关系
type Referral struct {
	ID          string     `gorm:"type:uuid;primaryKey" json:"id"`
	InviterID   string     `gorm:"type:uuid;index;not null" json:"inviter_id"`   // 邀请人ID
	InviteeID   string     `gorm:"type:uuid;uniqueIndex;not null" json:"invitee_id"` // 被邀请人ID(唯一, 每人只能被邀请一次)
	OrderID     *string    `gorm:"type:uuid" json:"order_id,omitempty"`           // 产生返利的订单ID
	RewardCents int64      `gorm:"type:bigint;default:0" json:"reward_cents"`     // 返利金额(分)
	Status      string     `gorm:"type:varchar(16);default:'pending';index" json:"status"` // pending/completed/expired
	RewardAt    *time.Time `json:"reward_at,omitempty"`                          // 返利发放时间
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func (Referral) TableName() string {
	return "referrals"
}

func (r *Referral) BeforeCreate(tx *gorm.DB) (err error) {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	return nil
}

// 邀请状态常量
const (
	ReferralStatusPending   = "pending"
	ReferralStatusCompleted = "completed"
	ReferralStatusExpired   = "expired"
)

// ReferralReward 返利记录(每笔返利明细, 用于对账和用户展示)
type ReferralReward struct {
	ID               string    `gorm:"type:uuid;primaryKey" json:"id"`
	UserID           string    `gorm:"type:uuid;index;not null" json:"user_id"`            // 获得返利的用户ID
	OrderID          string    `gorm:"type:uuid;index;not null" json:"order_id"`           // 产生返利的订单ID
	InviteeID        string    `gorm:"type:uuid;not null" json:"invitee_id"`                // 被邀请人ID
	AmountCents      int64     `gorm:"type:bigint;not null" json:"amount_cents"`            // 返利金额(分)
	OrderAmountCents int64     `gorm:"type:bigint;not null" json:"order_amount_cents"`      // 订单实付金额(分)
	RewardRate       float64   `gorm:"type:decimal(5,2);not null" json:"reward_rate"`      // 返利比例(如 0.1 = 10%)
	Description      string    `gorm:"type:varchar(255)" json:"description"`
	CreatedAt        time.Time `gorm:"index" json:"created_at"`
}

func (ReferralReward) TableName() string {
	return "referral_rewards"
}

func (r *ReferralReward) BeforeCreate(tx *gorm.DB) (err error) {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	return nil
}
