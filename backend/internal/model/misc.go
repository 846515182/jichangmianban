package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Announcement 公告
type Announcement struct {
	ID         string     `gorm:"type:uuid;primaryKey" json:"id"`
	Title      string     `gorm:"type:varchar(255);not null" json:"title"`
	Content    string     `gorm:"type:text" json:"content"`
	IsPinned   bool       `gorm:"column:is_pinned;default:false" json:"is_pinned"`
	Pinned     bool       `gorm:"-" json:"pinned"`     // 冗余 is_pinned(兼容老前端)
	IsDeleted  bool       `gorm:"column:is_deleted;default:false" json:"-"`
	PublishedAt *time.Time `gorm:"column:published_at" json:"published_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// TableName 指定表名
func (Announcement) TableName() string {
	return "announcements"
}

// BeforeCreate 创建前生成 UUID
func (a *Announcement) BeforeCreate(tx *gorm.DB) (err error) {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	return nil
}

// Setting 系统设置(key-value)
type Setting struct {
	Key       string    `gorm:"type:varchar(128);primaryKey" json:"key"`
	Value     []byte    `gorm:"type:jsonb" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 指定表名
func (Setting) TableName() string {
	return "settings"
}

// LoginAudit 登录审计(bigserial 主键)
type LoginAudit struct {
	ID         int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	TargetType string    `gorm:"type:varchar(16);not null" json:"target_type"` // admin / user
	TargetID   string    `gorm:"type:uuid;index" json:"target_id"`
	IP         string    `gorm:"type:varchar(64)" json:"ip"`
	UserAgent  string    `gorm:"type:varchar(255)" json:"user_agent"`
	Location   string    `gorm:"type:varchar(128)" json:"location"`
	Success    bool      `json:"success"`
	CreatedAt  time.Time `gorm:"index" json:"created_at"`
}

// TableName 指定表名
func (LoginAudit) TableName() string {
	return "login_audit"
}
