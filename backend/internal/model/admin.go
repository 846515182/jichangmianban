package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Admin 管理员模型
type Admin struct {
	ID           string     `gorm:"type:uuid;primaryKey" json:"id"`
	Username     string     `gorm:"type:varchar(64);uniqueIndex;not null" json:"username"`
	PasswordHash string     `gorm:"type:varchar(255);not null" json:"-"`
	Email        string     `gorm:"type:varchar(128)" json:"email"`
	Role         string     `gorm:"type:varchar(32);default:'admin';not null" json:"role"` // super_admin / admin
	Status       string     `gorm:"type:varchar(16);default:'active';not null" json:"status"` // active / disabled
	LockUntil    *time.Time `gorm:"column:lock_until" json:"lock_until,omitempty"`
	LastLoginAt  *time.Time `gorm:"column:last_login_at" json:"last_login_at,omitempty"`
	LastLoginIP  string     `gorm:"type:varchar(64)" json:"last_login_ip"`
	IsDeleted    bool       `gorm:"column:is_deleted;default:false" json:"-"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// TableName 指定表名
func (Admin) TableName() string {
	return "admins"
}

// BeforeCreate 创建前生成 UUID
func (a *Admin) BeforeCreate(tx *gorm.DB) (err error) {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	return nil
}
