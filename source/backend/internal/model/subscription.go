package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Subscription 订阅模型
type Subscription struct {
	ID         string     `gorm:"type:uuid;primaryKey" json:"id"`
	UserID     string     `gorm:"type:uuid;index;not null" json:"user_id"`
	SubToken   string     `gorm:"type:varchar(64);uniqueIndex;not null" json:"sub_token"`
	SubType    string     `gorm:"type:varchar(32)" json:"sub_type"` // clash / sing-box / v2ray / sip008
	DisableURI string     `gorm:"type:varchar(255)" json:"disable_uri"`
	ExpiresAt  *time.Time `gorm:"column:expires_at" json:"expires_at,omitempty"`
	IsDeleted  bool       `gorm:"column:is_deleted;default:false" json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
}

// TableName 指定表名
func (Subscription) TableName() string {
	return "subscriptions"
}

// BeforeCreate 创建前生成 UUID
func (s *Subscription) BeforeCreate(tx *gorm.DB) (err error) {
	if s.ID == "" {
		s.ID = uuid.New().String()
	}
	return nil
}
