package handler

import (
	"errors"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"nexus-panel/internal/middleware"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/response"
	"nexus-panel/internal/security"
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

	// P2-8: 移除 maxPageSize=1000 死代码, parsePage 已将 size 上限设为 200, 此判断永不触发
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
	// [P1-删除审计] 失效用户 JWT, 软删后旧 token 在过期前仍可调用 /user/* 接口(虽然 GetByID 返回 NotFound, 但 token 有效是安全隐患)
	invalidateUserTokens(id)
	// 触发所有节点配置刷新，确保已删除用户的凭证从节点 Xray 配置中移除
	_ = h.nodeRepo.TouchAllEnabled()
	response.OKMsg(c, "已删除")
}

// UserHardDelete [管理员] 物理删除用户(彻底清理, 仅用于测试数据)
// 与 UserDelete(软删除)不同:
//   - 软删除: is_deleted=true + username/email 加后缀释放索引, 记录保留可审计
//   - 硬删除: 物理从数据库删除, 释放所有索引, 重新注册同 username/email 不冲突
//
// 适用场景: 测试账号清理、误注册账号清理。生产账号慎用(数据不可恢复)。
// 级联清理: traffic_logs, user_nodes, subscriptions(物理删); orders(软删保留审计)
// 修复 CRITICAL 2026-07-19: 解决"软删后 email 唯一索引仍占用, 重新注册报重复"问题
// 修复 P1-5: 增加防自删检查(管理员不能删除自己关联的 user 账号)
func (h *AdminUserHandler) UserHardDelete(c *gin.Context) {
	id := c.Param("id")

	// 修复 P1-5: 防自删 - 管理员不能删除自己(通过 user_id 关联)
	// 说明: admin 与 user 是分表存储, 但若管理员有同名 user 账号或 ID 关联,
	// 防自删可避免误操作; 同时拒绝删除当前登录主体
	claims := middleware.GetClaims(c)
	if claims != nil {
		// 当前登录为 user 角色时, 禁止删除自己的 user 账号
		if claims.Role == security.RoleUser && claims.UserID == id {
			response.FailMsg(c, response.CodeServerError, "不能删除当前登录的账号")
			return
		}
	}

	// 先校验用户存在
	_, err := h.userRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.Fail(c, response.CodeDBError)
		return
	}
	// 说明: model.User 无 Role 字段, 所有 user 表记录均为普通用户, 无需检查特权角色
	// admin 账号存储在 admin 表, 不会通过此接口被删除

	// 安全检查: 存在未完结订单时拒绝硬删, 订单数据必须保留
	if h.orderSvc != nil {
		if has, err := h.orderSvc.HasActiveOrders(id); err == nil && has {
			response.FailMsg(c, response.CodeServerError, "该用户存在未完结订单, 请先处理订单后再删除")
			return
		}
	}
	if err := h.userService.HardDeleteUser(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	// [P1-删除审计] 失效用户 JWT, 硬删后旧 token 仍有效是安全隐患
	invalidateUserTokens(id)
	// 触发所有节点配置刷新, 确保已删除用户的凭证从节点 Xray 配置中移除
	_ = h.nodeRepo.TouchAllEnabled()
	response.OKMsg(c, "已彻底删除(物理清除, 数据不可恢复)")
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
		"failed":        failed,
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

// toggleStatusRequest 切换用户状态请求体
// 修复 P1-3 TOCTOU: 原代码不读请求体, 直接基于 DB 当前值 toggle,
// 并发操作会导致结果反转(两个请求同时读到 disabled, 都改为 active, 但用户期望一开一关)
// 现改为前端必须发送明确目标 status, 后端按目标设置, 不再 toggle
type toggleStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=active disabled"`
}

// UserToggleStatus 设置用户启用/禁用状态
// 修复 P1-3: 改为读请求体目标 status, 不再 toggle, 消除 TOCTOU 竞态
func (h *AdminUserHandler) UserToggleStatus(c *gin.Context) {
	id := c.Param("id")
	var req toggleStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}

	// 先校验用户存在, 避免 DisableUser/EnableUser 内部报错被吞
	u, err := h.userRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.Fail(c, response.CodeDBError)
		return
	}

	// 按请求体目标 status 设置(不再 toggle)
	if req.Status == "disabled" {
		if err := h.userService.DisableUser(id); err != nil {
			response.Fail(c, response.CodeServerError)
			return
		}
	} else {
		if err := h.userService.EnableUser(id); err != nil {
			response.Fail(c, response.CodeServerError)
			return
		}
	}
	_ = u // u 仅用于存在性校验
	h.nodeRepo.TouchAllEnabled()
	response.OK(c, gin.H{"status": req.Status})
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
