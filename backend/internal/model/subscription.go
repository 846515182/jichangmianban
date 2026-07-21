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
	// 修复 P1-repo-subscription: DB 为 BOOLEAN(见 001_init.sql), 旧版用 string 会触发
	// 类型转换风险(scan bool 到 string 报错 / 写入字符串导致 BOOLEAN 解析失败)。改为 bool 对齐。
	DisableURI bool       `gorm:"type:boolean;default:false" json:"disable_uri"`
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
