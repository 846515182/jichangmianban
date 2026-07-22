package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Node 节点模型
type Node struct {
	ID            string         `gorm:"type:uuid;primaryKey" json:"id"`
	Name          string         `gorm:"type:varchar(128);not null" json:"name"`
	CountryCode   string         `gorm:"type:varchar(8)" json:"country_code"`
	Protocol      string         `gorm:"type:varchar(32);not null" json:"protocol"`
	ServerAddress string         `gorm:"type:varchar(255);not null" json:"server_address"`
	Port          int            `gorm:"type:int;not null" json:"port"`
	ServerConfig  datatypes.JSON `gorm:"type:jsonb" json:"server_config"`
	TrafficLimit  int64          `gorm:"type:bigint;default:0" json:"traffic_limit"`
	TrafficUsed   int64          `gorm:"type:bigint;default:0" json:"traffic_used"`
	IsEnabled     bool           `gorm:"column:is_enabled;default:true" json:"is_enabled"`
	NodeToken     string         `gorm:"type:varchar(64);uniqueIndex;not null" json:"node_token"`
	GrpcPort      int            `gorm:"type:int;default:50051" json:"grpc_port"`
	LastSeenAt    *time.Time     `gorm:"column:last_seen_at" json:"last_seen_at,omitempty"`
	Online        bool           `gorm:"default:false" json:"online"`
	Version       string         `gorm:"type:varchar(32)" json:"version"`
	// [节点容量管理] 智能负载调度 + 自动踢人保护
	// MaxClients: 节点最大用户数, 0=不限(不参与调度)
	// MaxBandwidthMbps: 节点最大带宽Mbps, 0=不限
	// CpuThreshold: CPU超载阈值%, 默认80, 超过则视为满载
	// LoadStatus: 负载状态 idle/normal/busy/full, 由心跳评分实时更新
	MaxClients       int    `gorm:"type:int;default:0" json:"max_clients"`
	MaxBandwidthMbps int    `gorm:"type:int;default:0" json:"max_bandwidth_mbps"`
	CpuThreshold     int    `gorm:"type:int;default:80" json:"cpu_threshold"`
	LoadStatus       string `gorm:"type:varchar(20);default:'idle'" json:"load_status"`
	IsDeleted        bool   `gorm:"column:is_deleted;default:false" json:"-"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (Node) TableName() string { return "nodes" }

func (n *Node) BeforeCreate(tx *gorm.DB) (err error) {
	if n.ID == "" {
		n.ID = uuid.New().String()
	}
	return nil
}

type UserNode struct {
	ID        string    `gorm:"type:uuid;primaryKey" json:"id"`
	UserID    string    `gorm:"type:uuid;index;not null" json:"user_id"`
	NodeID    string    `gorm:"type:uuid;index;not null" json:"node_id"`
	IsDeleted bool      `gorm:"column:is_deleted;default:false" json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (UserNode) TableName() string { return "user_nodes" }

func (un *UserNode) BeforeCreate(tx *gorm.DB) (err error) {
	if un.ID == "" {
		un.ID = uuid.New().String()
	}
	return nil
}
