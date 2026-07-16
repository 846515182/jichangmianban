package service

import (
	"errors"
	"time"

	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
)

// TicketService 工单服务
type TicketService struct {
	ticketRepo *repo.TicketRepo
	userRepo   *repo.UserRepo
	adminRepo  *repo.AdminRepo
}

// NewTicketService 创建工单服务
func NewTicketService(tr *repo.TicketRepo, ur *repo.UserRepo, ar *repo.AdminRepo) *TicketService {
	return &TicketService{ticketRepo: tr, userRepo: ur, adminRepo: ar}
}

// CreateTicketInput 创建工单入参
type CreateTicketInput struct {
	UserID   string
	Subject  string
	Content  string
	Category string
	Priority string
}

// CreateTicket 创建工单(同时记录首条 reply)
func (s *TicketService) CreateTicket(in *CreateTicketInput) (*model.Ticket, error) {
	if in.UserID == "" || in.Subject == "" {
		return nil, errors.New("缺少必填字段")
	}
	if in.Category == "" {
		in.Category = "other"
	}
	if in.Priority == "" {
		in.Priority = "normal"
	}
	u, err := s.userRepo.GetByID(in.UserID)
	if err != nil {
		return nil, errors.New("用户不存在")
	}
	now := time.Now()
	t := &model.Ticket{
		UserID:       in.UserID,
		Subject:      in.Subject,
		Content:      in.Content,
		Category:     in.Category,
		Priority:     in.Priority,
		Status:       model.TicketStatusOpen,
		LastReplyBy:  model.TicketReplyByUser,
		LastReplyAt:  &now,
	}
	if err := s.ticketRepo.CreateTicket(t); err != nil {
		return nil, err
	}
	// 首条回复作为内容快照
	first := &model.TicketReply{
		TicketID:     t.ID,
		ReplyType:    model.TicketReplyByUser,
		ReplierID:    u.ID,
		ReplierName:  u.Username,
		Content:      in.Content,
		CreatedAt:    now,
	}
	if err := s.ticketRepo.AddReply(first); err != nil {
		// 创建成功但首条回复失败, 不阻断(整体事务由 handler 控制)
		return t, nil
	}
	return t, nil
}

// ListUserTickets 用户自己的工单列表
func (s *TicketService) ListUserTickets(userID string, page, size int, status string) ([]model.Ticket, int64, error) {
	return s.ticketRepo.ListTicketsByUser(userID, page, size, status)
}

// ListAllTickets 管理员查询所有工单(含用户名)
func (s *TicketService) ListAllTickets(page, size int, status, userID, keyword string) ([]repo.TicketListItem, int64, error) {
	return s.ticketRepo.ListTickets(page, size, status, userID, keyword)
}

// GetTicketDetail 获取工单详情(含回复)
func (s *TicketService) GetTicketDetail(ticketID, currentUserID, currentRole string) (*model.Ticket, []model.TicketReply, error) {
	t, err := s.ticketRepo.GetTicketByID(ticketID)
	if err != nil {
		return nil, nil, err
	}
	// 用户只能看自己的工单
	if currentRole == "user" && t.UserID != currentUserID {
		return nil, nil, errors.New("无权访问此工单")
	}
	replies, err := s.ticketRepo.ListReplies(ticketID)
	if err != nil {
		return nil, nil, err
	}
	return t, replies, nil
}

// ReplyInput 回复入参
type ReplyInput struct {
	TicketID  string
	ReplyType string // "user" / "admin" / "system"
	ReplierID string
	Content   string
}

// Reply 回复工单
// - 用户回复: status 维持 open; 管理员看到后置 replied
// - 管理员回复: status 立即置 replied
// - system: 不变
func (s *TicketService) Reply(in *ReplyInput) (*model.TicketReply, error) {
	if in.TicketID == "" || in.Content == "" {
		return nil, errors.New("缺少必填字段")
	}
	if in.ReplyType == "" {
		return nil, errors.New("回复类型不能为空")
	}
	t, err := s.ticketRepo.GetTicketByID(in.TicketID)
	if err != nil {
		return nil, errors.New("工单不存在")
	}
	if t.Status == model.TicketStatusClosed {
		return nil, errors.New("工单已关闭, 无法回复")
	}
	// 解析 replier_name
	var name string
	if in.ReplyType == model.TicketReplyByAdmin {
		if a, err := s.adminRepo.GetByID(in.ReplierID); err == nil {
			name = a.Username
		}
	} else if in.ReplyType == model.TicketReplyByUser {
		if u, err := s.userRepo.GetByID(in.ReplierID); err == nil {
			name = u.Username
		}
	} else {
		name = "system"
	}
	r := &model.TicketReply{
		TicketID:     in.TicketID,
		ReplyType:    in.ReplyType,
		ReplierID:    in.ReplierID,
		ReplierName:  name,
		Content:      in.Content,
	}
	if err := s.ticketRepo.AddReply(r); err != nil {
		return nil, err
	}
	// 更新工单状态: 管理员回复 -> replied; 用户回复 -> open(等待管理员)
	newStatus := t.Status
	if in.ReplyType == model.TicketReplyByAdmin {
		newStatus = model.TicketStatusReplied
	} else if in.ReplyType == model.TicketReplyByUser {
		// 用户再次回复, 重置为 open
		if t.Status == model.TicketStatusReplied {
			newStatus = model.TicketStatusOpen
		}
	}
	_ = s.ticketRepo.UpdateTicketStatus(t.ID, newStatus, in.ReplyType)
	return r, nil
}

// CloseTicket 关闭工单(管理员/用户均可, 关闭后无法继续回复)
func (s *TicketService) CloseTicket(ticketID, currentUserID, currentRole string) error {
	t, err := s.ticketRepo.GetTicketByID(ticketID)
	if err != nil {
		return err
	}
	if currentRole == "user" && t.UserID != currentUserID {
		return errors.New("无权操作此工单")
	}
	if t.Status == model.TicketStatusClosed {
		return nil
	}
	return s.ticketRepo.CloseTicket(ticketID)
}
