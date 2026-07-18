package handler

import (
	"errors"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/response"
	"nexus-panel/internal/service"
)

// AdminUserHandler 管理端用户处理器
type AdminUserHandler struct {
	userService *service.UserService
	userRepo    *repo.UserRepo
	subSvc      *service.SubscribeService
	subRepo     *repo.SubscriptionRepo
	orderSvc    *service.OrderService
	nodeRepo    *repo.NodeRepo
}

// NewAdminUserHandler 创建管理端用户处理器
func NewAdminUserHandler(s *service.UserService, r *repo.UserRepo, ss *service.SubscribeService, sr *repo.SubscriptionRepo, os *service.OrderService, nr *repo.NodeRepo) *AdminUserHandler {
	return &AdminUserHandler{userService: s, userRepo: r, subSvc: ss, subRepo: sr, orderSvc: os, nodeRepo: nr}
}

// UserList 用户列表(带订阅链接)
func (h *AdminUserHandler) UserList(c *gin.Context) {
	page, size := parsePage(c)
	keyword := c.Query("keyword")

	// 安全修复: 限制单次查询最大条数，防止大量数据导出
	const maxPageSize = 1000
	if size > maxPageSize {
		size = maxPageSize
	}

	list, total, err := h.userRepo.List(page, size, keyword)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}

	// 为每个用户生成订阅链接
	baseURL := getBaseURL(c)
	type userWithSub struct {
		*model.User
		SubscribeURL string `json:"subscribe_url"`
		SubToken     string `json:"sub_token"`
	}
	result := make([]userWithSub, 0, len(list))
	for i := range list {
		u := &list[i]
		item := userWithSub{User: u}
		if sub, err := h.subRepo.GetByUserID(u.ID); err == nil {
			item.SubToken = sub.SubToken
			url, _ := h.subSvc.GenerateSignedURL(u.ID, baseURL, c.ClientIP())
			item.SubscribeURL = url
		}
		result = append(result, item)
	}
	response.OK(c, gin.H{"list": result, "total": total})
}

// UserCreate 创建用户
func (h *AdminUserHandler) UserCreate(c *gin.Context) {
	var in service.CreateUserInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	u, err := h.userService.CreateUser(&in)
	if err != nil {
		if errors.Is(err, service.ErrDuplicate) {
			response.Fail(c, response.CodeDuplicate)
			return
		}
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OK(c, u)
}

// UserUpdate 更新用户
func (h *AdminUserHandler) UserUpdate(c *gin.Context) {
	id := c.Param("id")
	var in service.UpdateUserInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	u, err := h.userService.UpdateUser(id, &in)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OK(c, u)
}

// UserDelete 删除用户
func (h *AdminUserHandler) UserDelete(c *gin.Context) {
	id := c.Param("id")
	// 安全修复(P1): 存在未完结订单时拒绝删除, 避免产生孤儿订单
	if h.orderSvc != nil {
		if has, err := h.orderSvc.HasActiveOrders(id); err == nil && has {
			response.FailMsg(c, response.CodeServerError, "该用户存在未完结订单，请先处理订单后再删除")
			return
		}
	}
	if err := h.userService.DeleteUser(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.Fail(c, response.CodeServerError)
		return
	}
	// 同时禁用订阅，防止删除后通过公开订阅链接继续使用
	_ = h.subRepo.DisableByUserID(id)
	// 触发所有节点配置刷新，确保已删除用户的凭证从节点 Xray 配置中移除
	_ = h.nodeRepo.TouchAllEnabled()
	response.OKMsg(c, "已删除")
}

// UserImport 批量导入用户
func (h *AdminUserHandler) UserImport(c *gin.Context) {
	var in service.BatchCreateUserInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	success, failed := h.userService.BatchCreate(&in)
	response.OK(c, gin.H{
		"success_count": len(success),
		"failed_count":  len(failed),
		"success":       success,
		"failed":         failed,
	})
}

// UserResetTraffic 重置用户流量
func (h *AdminUserHandler) UserResetTraffic(c *gin.Context) {
	id := c.Param("id")
	if err := h.userService.ResetTraffic(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.Fail(c, response.CodeServerError)
		return
	}
	response.OKMsg(c, "流量已重置")
}

// UserToggleStatus 切换用户启用/禁用状态
func (h *AdminUserHandler) UserToggleStatus(c *gin.Context) {
	id := c.Param("id")
	u, err := h.userRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.Fail(c, response.CodeDBError)
		return
	}
	if u.Status == "disabled" {
		if err := h.userService.EnableUser(id); err != nil {
			response.Fail(c, response.CodeServerError)
			return
		}
		h.nodeRepo.TouchAllEnabled()
		response.OK(c, gin.H{"status": "active"})
		return
	}
	if err := h.userService.DisableUser(id); err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	h.nodeRepo.TouchAllEnabled()
	response.OK(c, gin.H{"status": "disabled"})
}

// activatePlanRequest 开通套餐请求
type activatePlanRequest struct {
	PlanID string `json:"plan_id" binding:"required"`
}

// UserActivatePlan 管理员手动给用户开通套餐（无需支付）
func (h *AdminUserHandler) UserActivatePlan(c *gin.Context) {
	id := c.Param("id")
	var req activatePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	if h.orderSvc == nil {
		response.FailMsg(c, response.CodeServerError, "订单服务不可用")
		return
	}
	if err := h.orderSvc.SetUserPlan(id, req.PlanID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OKMsg(c, "套餐已开通")
}
