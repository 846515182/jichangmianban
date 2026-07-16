package handler

import (
	"github.com/gin-gonic/gin"

	"nexus-panel/internal/middleware"
	"nexus-panel/internal/response"
	"nexus-panel/internal/security"
	"nexus-panel/internal/service"
)

// TicketHandler 工单处理器(用户端 + 管理端合并, 内部按 role 区分权限)
type TicketHandler struct {
	ticketSvc *service.TicketService
}

// NewTicketHandler 创建工单处理器
func NewTicketHandler(s *service.TicketService) *TicketHandler {
	return &TicketHandler{ticketSvc: s}
}

// createTicketRequest 创建工单请求
type createTicketRequest struct {
	Subject  string `json:"subject" binding:"required,max=255"`
	Content  string `json:"content" binding:"required"`
	Category string `json:"category"`
	Priority string `json:"priority"`
}

// UserCreateTicket [POST] /api/v1/user/tickets
// 用户创建工单
func (h *TicketHandler) UserCreateTicket(c *gin.Context) {
	uid := middleware.GetUserID(c)
	var req createTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	t, err := h.ticketSvc.CreateTicket(&service.CreateTicketInput{
		UserID:   uid,
		Subject:  req.Subject,
		Content:  req.Content,
		Category: req.Category,
		Priority: req.Priority,
	})
	if err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OK(c, t)
}

// UserListTickets [GET] /api/v1/user/tickets
// 用户的工单列表
func (h *TicketHandler) UserListTickets(c *gin.Context) {
	uid := middleware.GetUserID(c)
	page, size := parsePage(c)
	status := c.Query("status")
	list, total, err := h.ticketSvc.ListUserTickets(uid, page, size, status)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, gin.H{"list": list, "total": total})
}

// UserGetTicket [GET] /api/v1/user/tickets/:id
// 用户查看工单详情(含回复)
func (h *TicketHandler) UserGetTicket(c *gin.Context) {
	uid := middleware.GetUserID(c)
	id := c.Param("id")
	t, replies, err := h.ticketSvc.GetTicketDetail(id, uid, security.RoleUser)
	if err != nil {
		response.FailMsg(c, response.CodeNotFound, err.Error())
		return
	}
	response.OK(c, gin.H{"ticket": t, "replies": replies})
}

// userReplyRequest 用户回复工单
type userReplyRequest struct {
	Content string `json:"content" binding:"required"`
}

// UserReplyTicket [POST] /api/v1/user/tickets/:id/reply
// 用户回复工单
func (h *TicketHandler) UserReplyTicket(c *gin.Context) {
	uid := middleware.GetUserID(c)
	id := c.Param("id")
	var req userReplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	// 先校验是否是自己的工单
	_, _, err := h.ticketSvc.GetTicketDetail(id, uid, security.RoleUser)
	if err != nil {
		response.FailMsg(c, response.CodeNotFound, err.Error())
		return
	}
	reply, err := h.ticketSvc.Reply(&service.ReplyInput{
		TicketID:  id,
		ReplyType: "user",
		ReplierID: uid,
		Content:   req.Content,
	})
	if err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OK(c, reply)
}

// UserCloseTicket [POST] /api/v1/user/tickets/:id/close
// 用户关闭工单
func (h *TicketHandler) UserCloseTicket(c *gin.Context) {
	uid := middleware.GetUserID(c)
	id := c.Param("id")
	if err := h.ticketSvc.CloseTicket(id, uid, security.RoleUser); err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OKMsg(c, "已关闭")
}

// AdminListTickets [GET] /api/v1/admin/tickets
// 管理员查看所有工单
func (h *TicketHandler) AdminListTickets(c *gin.Context) {
	page, size := parsePage(c)
	status := c.Query("status")
	userID := c.Query("user_id")
	keyword := c.Query("keyword")
	list, total, err := h.ticketSvc.ListAllTickets(page, size, status, userID, keyword)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, gin.H{"list": list, "total": total})
}

// AdminGetTicket [GET] /api/v1/admin/tickets/:id
// 管理员查看工单详情(含回复)
func (h *TicketHandler) AdminGetTicket(c *gin.Context) {
	aid := middleware.GetUserID(c)
	id := c.Param("id")
	t, replies, err := h.ticketSvc.GetTicketDetail(id, aid, security.RoleAdmin)
	if err != nil {
		response.FailMsg(c, response.CodeNotFound, err.Error())
		return
	}
	response.OK(c, gin.H{"ticket": t, "replies": replies})
}

// adminReplyRequest 管理员回复工单
type adminReplyRequest struct {
	Content string `json:"content" binding:"required"`
}

// AdminReplyTicket [POST] /api/v1/admin/tickets/:id/reply
// 管理员回复工单
func (h *TicketHandler) AdminReplyTicket(c *gin.Context) {
	aid := middleware.GetUserID(c)
	id := c.Param("id")
	var req adminReplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	reply, err := h.ticketSvc.Reply(&service.ReplyInput{
		TicketID:  id,
		ReplyType: "admin",
		ReplierID: aid,
		Content:   req.Content,
	})
	if err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OK(c, reply)
}

// AdminCloseTicket [POST] /api/v1/admin/tickets/:id/close
// 管理员关闭工单
func (h *TicketHandler) AdminCloseTicket(c *gin.Context) {
	aid := middleware.GetUserID(c)
	id := c.Param("id")
	if err := h.ticketSvc.CloseTicket(id, aid, security.RoleAdmin); err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OKMsg(c, "已关闭")
}

// ReplyAlias 兼容老路径 /api/v1/tickets/:id/reply
// 根据当前 role 决定是用户还是管理员回复
func (h *TicketHandler) ReplyAlias(c *gin.Context) {
	role := middleware.GetRole(c)
	uid := middleware.GetUserID(c)
	id := c.Param("id")
	var req userReplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	if role == security.RoleAdmin {
		// 管理员
		reply, err := h.ticketSvc.Reply(&service.ReplyInput{
			TicketID:  id,
			ReplyType: "admin",
			ReplierID: uid,
			Content:   req.Content,
		})
		if err != nil {
			response.FailMsg(c, response.CodeServerError, err.Error())
			return
		}
		response.OK(c, reply)
		return
	}
	// 用户
	// 先校验是否是自己的工单
	_, _, err := h.ticketSvc.GetTicketDetail(id, uid, security.RoleUser)
	if err != nil {
		response.FailMsg(c, response.CodeNotFound, err.Error())
		return
	}
	reply, err := h.ticketSvc.Reply(&service.ReplyInput{
		TicketID:  id,
		ReplyType: "user",
		ReplierID: uid,
		Content:   req.Content,
	})
	if err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OK(c, reply)
}

// CloseAlias 兼容老路径 /api/v1/tickets/:id/close
// 根据当前 role 决定权限校验
func (h *TicketHandler) CloseAlias(c *gin.Context) {
	role := middleware.GetRole(c)
	uid := middleware.GetUserID(c)
	id := c.Param("id")
	if err := h.ticketSvc.CloseTicket(id, uid, role); err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OKMsg(c, "已关闭")
}
