package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Order 订单模型
type Order struct {
	ID            string     `gorm:"type:uuid;primaryKey" json:"id"`
	OrderNo       string     `gorm:"type:varchar(64);uniqueIndex;not null" json:"order_no"` // 订单号 NP+时间戳+随机
	UserID        string     `gorm:"type:uuid;index;not null" json:"user_id"`
	PlanID        string     `gorm:"type:uuid;not null" json:"plan_id"`
	PlanName      string     `gorm:"type:varchar(128)" json:"plan_name"`                     // 冗余快照
	AmountCents   int64      `gorm:"type:bigint;not null" json:"amount_cents"`               // 实付金额(分)
	Status        string     `gorm:"type:varchar(16);default:'pending';index" json:"status"` // pending/paid/cancelled/refunded/expired
	PaymentMethod string     `gorm:"type:varchar(32)" json:"payment_method"`                 // epay_alipay/epay_wechat
	TradeNo       string     `gorm:"type:varchar(128)" json:"trade_no"`                      // 第三方支付流水号
	CouponID      string     `gorm:"type:uuid" json:"coupon_id,omitempty"`                   // 使用的优惠券ID(审计)
	CouponCode    string     `gorm:"type:varchar(32)" json:"coupon_code,omitempty"`          // 优惠券码快照(审计)
	PaidAt        *time.Time `json:"paid_at,omitempty"`
	ExpiredAt     time.Time  `json:"expired_at"` // 订单过期时间(15分钟)
	IsDeleted     bool       `gorm:"column:is_deleted;default:false" json:"-"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// TableName 指定表名
func (Order) TableName() string {
	return "orders"
}

// BeforeCreate 创建前生成 UUID
func (o *Order) BeforeCreate(tx *gorm.DB) (err error) {
	if o.ID == "" {
		o.ID = uuid.New().String()
	}
	return nil
}

// 订单状态常量
const (
	OrderStatusPending   = "pending"
	OrderStatusPaid      = "paid"
	OrderStatusCancelled = "cancelled"
	OrderStatusRefunded  = "refunded"
	OrderStatusExpired   = "expired"
)

// Coupon 优惠券
type Coupon struct {
	ID             string     `gorm:"type:uuid;primaryKey" json:"id"`
	Code           string     `gorm:"type:varchar(32);uniqueIndex;not null" json:"code"`
	Type           string     `gorm:"type:varchar(16);not null" json:"type"`         // percent(百分比) / fixed(固定金额)
	Value          int64      `gorm:"type:bigint;not null" json:"value"`             // percent: 1-100(%)  fixed: 金额(分)
	MinAmountCents int64      `gorm:"type:bigint;default:0" json:"min_amount_cents"` // 最低消费(分)
	MaxUses        int        `gorm:"type:int;default:0" json:"max_uses"`            // 0=不限
	UsedCount      int        `gorm:"type:int;default:0" json:"used_count"`
	ExpireAt       *time.Time `json:"expire_at,omitempty"`
	IsEnabled      bool       `gorm:"column:is_enabled;default:true" json:"is_enabled"`
	IsDeleted      bool       `gorm:"column:is_deleted;default:false" json:"-"`
	CreatedAt      time.Time  `json:"created_at"`
}

// TableName 指定表名
func (Coupon) TableName() string {
	return "coupons"
}

// BeforeCreate 创建前生成 UUID
func (c *Coupon) BeforeCreate(tx *gorm.DB) (err error) {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	return nil
}

// 优惠券类型常量
const (
	CouponTypePercent = "percent"
	CouponTypeFixed   = "fixed"
)
