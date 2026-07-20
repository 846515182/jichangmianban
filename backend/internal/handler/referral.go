package handler

import (
	"github.com/gin-gonic/gin"

	"nexus-panel/internal/middleware"
	"nexus-panel/internal/response"
	"nexus-panel/internal/service"
)

// ReferralHandler 邀请返利处理器(用户端)
type ReferralHandler struct {
	referralSvc *service.ReferralService
}

// NewReferralHandler 创建邀请返利处理器
func NewReferralHandler(r *service.ReferralService) *ReferralHandler {
	return &ReferralHandler{referralSvc: r}
}

// GetMyInviteCode [U] GET /api/v1/referral/invite-code
// 获取我的邀请码(没有则自动生成)
func (h *ReferralHandler) GetMyInviteCode(c *gin.Context) {
	uid := middleware.GetUserID(c)
	code, err := h.referralSvc.GetOrCreateInviteCode(uid)
	if err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OK(c, gin.H{
		"invite_code": code,
		"share_url":   "", // 前端可自行拼接: https://domain.com/?ref=code
	})
}

// GetReferralStats [U] GET /api/v1/referral/stats
// 获取我的邀请统计
func (h *ReferralHandler) GetReferralStats(c *gin.Context) {
	uid := middleware.GetUserID(c)
	total, completed, totalReward, err := h.referralSvc.GetStats(uid)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, gin.H{
		"total_invited":   total,
		"completed_count": completed,
		"total_reward":    totalReward, // 分
	})
}

// ListInvitations [U] GET /api/v1/referral/invitations
// 分页查询我发出的邀请列表
func (h *ReferralHandler) ListInvitations(c *gin.Context) {
	uid := middleware.GetUserID(c)
	page, size := parsePage(c)
	list, total, err := h.referralSvc.ListInvitations(uid, page, size)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, gin.H{"list": list, "total": total})
}

// ListRewards [U] GET /api/v1/referral/rewards
// 分页查询我的返利记录
func (h *ReferralHandler) ListRewards(c *gin.Context) {
	uid := middleware.GetUserID(c)
	page, size := parsePage(c)
	list, total, err := h.referralSvc.ListRewards(uid, page, size)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, gin.H{"list": list, "total": total})
}

// bindInviteCodeRequest 绑定邀请码请求
type bindInviteCodeRequest struct {
	InviteCode string `json:"invite_code" binding:"required"`
}

// BindInviteCode [U] POST /api/v1/referral/bind
// 绑定邀请码(只能绑定一次, 注册后未首单前可绑定)
func (h *ReferralHandler) BindInviteCode(c *gin.Context) {
	uid := middleware.GetUserID(c)
	var req bindInviteCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	if err := h.referralSvc.BindInviter(uid, req.InviteCode); err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OKMsg(c, "邀请码绑定成功")
}
