package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// User 用户模型
type User struct {
	ID            string     `gorm:"type:uuid;primaryKey" json:"id"`
	Username      string     `gorm:"type:varchar(64);uniqueIndex;not null" json:"username"`
	PasswordHash  string     `gorm:"type:varchar(255);not null" json:"-"`
	Email         string     `gorm:"type:varchar(128)" json:"email"`
	EmailVerified bool       `gorm:"column:email_verified;default:false" json:"email_verified"`
	TrafficLimit  int64      `gorm:"type:bigint;default:0" json:"traffic_limit"`  // 流量配额(字节)
	TrafficUsed   int64      `gorm:"type:bigint;default:0" json:"traffic_used"`   // 已用流量(字节)
	UploadBytes   int64      `gorm:"type:bigint;default:0" json:"upload_bytes"`   // 上行字节
	DownloadBytes int64      `gorm:"type:bigint;default:0" json:"download_bytes"` // 下行字节
	ExpiredAt     *time.Time `gorm:"column:expired_at" json:"expired_at,omitempty"`
	Status        string     `gorm:"type:varchar(16);default:'active';not null" json:"status"` // active / disabled / expired
	LockUntil     *time.Time `gorm:"column:lock_until" json:"lock_until,omitempty"`
	PlanID        *string    `gorm:"type:uuid" json:"plan_id,omitempty"`   // 当前套餐ID(nil=无套餐, 避免空串写入uuid列报错)
	InviteCode    string     `gorm:"type:varchar(16)" json:"invite_code"`    // 邀请码(唯一, 永久有效, NULL=未生成)
	Remark        string     `gorm:"type:varchar(255)" json:"remark"`
	IsDeleted     bool       `gorm:"column:is_deleted;default:false" json:"-"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// TableName 指定表名
func (User) TableName() string {
	return "users"
}

// BeforeCreate 创建前生成 UUID
func (u *User) BeforeCreate(tx *gorm.DB) (err error) {
	if u.ID == "" {
		u.ID = uuid.New().String()
	}
	return nil
}
