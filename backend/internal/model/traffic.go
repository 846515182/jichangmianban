package model

import (
	"time"

	"gorm.io/gorm"
)

// TrafficLog 流量日志（对应 traffic_logs 表，按月分区）
type TrafficLog struct {
	ID            int64     `gorm:"autoIncrement;primaryKey" json:"id"`
	UserID        string    `gorm:"type:uuid;index" json:"user_id"`
	NodeID        string    `gorm:"type:uuid;index" json:"node_id"`
	UploadBytes   int64     `gorm:"type:bigint;default:0" json:"upload_bytes"`
	DownloadBytes int64     `gorm:"type:bigint;default:0" json:"download_bytes"`
	LogTime       time.Time `gorm:"type:timestamptz;not null" json:"log_time"`
	LogDate       string    `gorm:"type:date;index" json:"log_date"`
}

// TableName ...
func (TrafficLog) TableName() string {
	return "traffic_logs"
}

// BeforeCreate 创建前自动从 LogTime 生成 LogDate（UTC YYYY-MM-DD）
// 修复: grpc 流量上报代码未设置 LogDate 导致插入空字符串到 date 列报错
func (l *TrafficLog) BeforeCreate(tx *gorm.DB) (err error) {
	if l.LogDate == "" {
		l.LogDate = l.LogTime.UTC().Format("2006-01-02")
	}
	return nil
}
