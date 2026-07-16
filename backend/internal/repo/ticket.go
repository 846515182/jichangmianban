package repo

import (
	"gorm.io/gorm"

	"nexus-panel/internal/model"
)

// TicketRepo 工单仓储
type TicketRepo struct {
	db *gorm.DB
}

// NewTicketRepo 创建工单仓储
func NewTicketRepo(db *gorm.DB) *TicketRepo {
	return &TicketRepo{db: db}
}

// CreateTicket 创建工单
func (r *TicketRepo) CreateTicket(t *model.Ticket) error {
	return r.db.Create(t).Error
}

// GetTicketByID 按 ID 查询(过滤软删除)
func (r *TicketRepo) GetTicketByID(id string) (*model.Ticket, error) {
	var t model.Ticket
	if err := r.db.Where("id = ? AND is_deleted = false", id).First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTicketsByUser 分页查询用户的工单
func (r *TicketRepo) ListTicketsByUser(userID string, page, size int, status string) ([]model.Ticket, int64, error) {
	var list []model.Ticket
	var total int64
	q := r.db.Model(&model.Ticket{}).Where("user_id = ? AND is_deleted = false", userID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	q.Count(&total)
	if err := q.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// TicketListItem 工单列表项(含用户名, 供管理端展示)
type TicketListItem struct {
	model.Ticket
	Username string `gorm:"column:username" json:"username"`
}

// ListTickets 分页查询所有工单(管理端, JOIN users 带出 username)
// 支持 status / user_id / keyword(subject 模糊) 过滤
func (r *TicketRepo) ListTickets(page, size int, status, userID, keyword string) ([]TicketListItem, int64, error) {
	var list []TicketListItem
	var total int64
	q := r.db.Model(&model.Ticket{}).Where("tickets.is_deleted = false")
	if status != "" {
		q = q.Where("tickets.status = ?", status)
	}
	if userID != "" {
		q = q.Where("tickets.user_id = ?", userID)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("tickets.subject LIKE ?", like)
	}
	q.Count(&total)
	if err := q.
		Select("tickets.*, users.username as username").
		Joins("LEFT JOIN users ON users.id = tickets.user_id").
		Order("tickets.created_at DESC").
		Offset((page - 1) * size).Limit(size).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// UpdateTicket 全量更新
func (r *TicketRepo) UpdateTicket(t *model.Ticket) error {
	return r.db.Save(t).Error
}

// UpdateTicketStatus 仅更新状态(配合最后回复时间)
func (r *TicketRepo) UpdateTicketStatus(id, status, lastReplyBy string) error {
	updates := map[string]interface{}{
		"status":        status,
		"last_reply_by": lastReplyBy,
		"updated_at":    gorm.Expr("NOW()"),
	}
	return r.db.Model(&model.Ticket{}).Where("id = ? AND is_deleted = false", id).Updates(updates).Error
}

// CloseTicket 关闭工单
func (r *TicketRepo) CloseTicket(id string) error {
	return r.db.Model(&model.Ticket{}).Where("id = ? AND is_deleted = false", id).
		Updates(map[string]interface{}{
			"status":     model.TicketStatusClosed,
			"closed_at":  gorm.Expr("NOW()"),
			"updated_at": gorm.Expr("NOW()"),
		}).Error
}

// AddReply 增加回复
func (r *TicketRepo) AddReply(reply *model.TicketReply) error {
	return r.db.Create(reply).Error
}

// ListReplies 查询工单所有回复(按时间正序)
func (r *TicketRepo) ListReplies(ticketID string) ([]model.TicketReply, error) {
	var list []model.TicketReply
	if err := r.db.Where("ticket_id = ? AND is_deleted = false", ticketID).
		Order("created_at ASC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
