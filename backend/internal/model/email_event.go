package model

import "time"

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
