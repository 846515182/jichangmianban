package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Plan 套餐模型
type Plan struct {
	ID                 string    `gorm:"type:uuid;primaryKey" json:"id"`
	Name               string    `gorm:"type:varchar(128);not null" json:"name"`
	Description        string    `gorm:"type:text" json:"description"`
	Features           string    `gorm:"type:text" json:"features"`      // 优点(JSON 数组字符串)
	Limitations        string    `gorm:"type:text" json:"limitations"`   // 缺点/限制(JSON 数组字符串)
	TrafficLimit       int64     `gorm:"type:bigint;default:0" json:"traffic_limit"`        // 流量配额(字节), 0=不限
	DurationDays       int       `gorm:"type:int;default:30" json:"duration_days"`          // 有效期天数, 0=不限
	PriceCents         int64     `gorm:"type:bigint;not null" json:"price_cents"`           // 价格(分)
	OriginalPriceCents int64     `gorm:"type:bigint;default:0" json:"original_price_cents"` // 原价(分, 用于显示划线价)
	DeviceLimit        int       `gorm:"type:int;default:3" json:"device_limit"`            // 设备数限制, 0=不限
	SortOrder          int       `gorm:"type:int;default:0" json:"sort_order"`              // 排序
	IsEnabled          bool      `gorm:"column:is_enabled;default:true" json:"is_enabled"`
	IsDeleted          bool      `gorm:"column:is_deleted;default:false" json:"-"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// TableName 指定表名
func (Plan) TableName() string {
	return "plans"
}

// BeforeCreate 创建前生成 UUID
func (p *Plan) BeforeCreate(tx *gorm.DB) (err error) {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	return nil
}
