package model

import "time"

// InviteCode 邀请码
type InviteCode struct {
	ID        uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Code      string     `gorm:"size:32;uniqueIndex;not null" json:"code"`
	CreatedBy string     `gorm:"type:uuid;not null;index" json:"created_by"` // users.id 是 uuid
	MaxUses   int        `gorm:"not null;default:1" json:"max_uses"`
	UsedCount int        `gorm:"not null;default:0" json:"used_count"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Disabled  bool       `gorm:"not null;default:false" json:"disabled"`
	Note      string     `gorm:"size:200" json:"note,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

func (InviteCode) TableName() string { return "invite_codes" }

// InviteCodeUse 邀请码使用记录
// 修正: IP 字段由 INET 改为 VARCHAR(45), 兼容 IPv4 (15) + IPv6 (45)
type InviteCodeUse struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	InviteCodeID uint64    `gorm:"not null;index" json:"invite_code_id"`
	Code         string    `gorm:"size:32;not null;index" json:"code"`
	UserID       string    `gorm:"type:uuid;not null;index" json:"user_id"` // users.id 是 uuid
	UsedAt       time.Time `gorm:"not null" json:"used_at"`
	IP           string    `gorm:"size:45" json:"ip,omitempty"` // 修正
	UA           string    `gorm:"size:512" json:"ua,omitempty"`
}

func (InviteCodeUse) TableName() string { return "invite_code_uses" }

// EmailEvent 邮件发送事件
type EmailEvent struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    *string   `gorm:"type:uuid;index" json:"user_id,omitempty"` // users.id 是 uuid
	Email     string    `gorm:"size:255;not null;index" json:"email"`
	EventType string    `gorm:"size:32;not null;index" json:"event_type"`
	CodeHash  string    `gorm:"size:128" json:"-"`
	SentAt    time.Time `gorm:"not null" json:"sent_at"`
	Success   bool      `gorm:"not null" json:"success"`
	ErrorMsg  string    `gorm:"size:500" json:"error_msg,omitempty"`
}

func (EmailEvent) TableName() string { return "email_events" }
