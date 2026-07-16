package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Ticket 工单(用户向管理员发起的问题)
type Ticket struct {
	ID         string `gorm:"type:uuid;primaryKey" json:"id"`
	UserID     string `gorm:"type:uuid;index;not null" json:"user_id"`
	Subject    string `gorm:"type:varchar(255);not null" json:"subject"`   // 主题
	Content    string `gorm:"type:text" json:"content"`                     // 首次发起时的内容
	Category   string `gorm:"type:varchar(32);default:'other'" json:"category"` // bug/question/refund/other
	Priority   string `gorm:"type:varchar(16);default:'normal'" json:"priority"` // low/normal/high
	Status     string `gorm:"type:varchar(16);default:'open';index" json:"status"` // open/replied/closed
	// LastReplyBy 记录最后回复角色, 便于前端展示标签(用户/管理员)
	LastReplyBy string     `gorm:"type:varchar(16)" json:"last_reply_by"`
	LastReplyAt *time.Time `json:"last_reply_at,omitempty"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	IsDeleted   bool       `gorm:"column:is_deleted;default:false" json:"-"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// TableName 指定表名
func (Ticket) TableName() string {
	return "tickets"
}

// BeforeCreate 创建前生成 UUID
func (t *Ticket) BeforeCreate(tx *gorm.DB) (err error) {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	return nil
}

// TicketReply 工单回复
type TicketReply struct {
	ID         string `gorm:"type:uuid;primaryKey" json:"id"`
	TicketID   string `gorm:"type:uuid;index;not null" json:"ticket_id"`
	// ReplyType: user(用户回复) / admin(管理员回复) / system(系统消息)
	ReplyType  string `gorm:"type:varchar(16);not null" json:"reply_type"`
	ReplierID  string `gorm:"type:uuid" json:"replier_id"`              // 用户ID或管理员ID
	ReplierName string `gorm:"type:varchar(64)" json:"replier_name"`   // 冗余快照, 便于显示
	Content    string `gorm:"type:text" json:"content"`
	IsDeleted  bool   `gorm:"column:is_deleted;default:false" json:"-"`
	CreatedAt  time.Time `json:"created_at"`
}

// TableName 指定表名
func (TicketReply) TableName() string {
	return "ticket_replies"
}

// BeforeCreate 创建前生成 UUID
func (r *TicketReply) BeforeCreate(tx *gorm.DB) (err error) {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	return nil
}

// 工单相关常量
const (
	TicketStatusOpen    = "open"
	TicketStatusReplied = "replied"
	TicketStatusClosed  = "closed"

	TicketReplyByUser   = "user"
	TicketReplyByAdmin  = "admin"
	TicketReplyBySystem = "system"
)
