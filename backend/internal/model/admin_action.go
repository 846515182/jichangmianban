package model

import (
        "time"

        "github.com/google/uuid"
        "gorm.io/gorm"
)

// AdminAction 管理员操作审计日志
type AdminAction struct {
        ID         string    `gorm:"type:uuid;primaryKey" json:"id"`
        AdminID    string    `gorm:"type:uuid;index;not null" json:"admin_id"`
        AdminName  string    `gorm:"type:varchar(64);not null" json:"admin_name"`
        Action     string    `gorm:"type:varchar(64);not null;index" json:"action"`
        TargetType string    `gorm:"type:varchar(32);index" json:"target_type"`
        TargetID   string    `gorm:"type:varchar(64)" json:"target_id"`
	Detail     string    `gorm:"type:text" json:"detail"`
	IP         string    `gorm:"type:varchar(64)" json:"ip"`
	Success    bool      `gorm:"default:true;index" json:"success"`
	CreatedAt  time.Time `json:"created_at"`
}

func (AdminAction) TableName() string {
        return "admin_actions"
}

func (a *AdminAction) BeforeCreate(tx *gorm.DB) (err error) {
        if a.ID == "" {
                a.ID = uuid.New().String()
        }
        return nil
}
