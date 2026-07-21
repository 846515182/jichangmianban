package handler

import (
	"github.com/gin-gonic/gin"

	"nexus-panel/internal/response"
	"nexus-panel/internal/service"
)

// AdminReferralHandler 邀请返利管理处理器
type AdminReferralHandler struct {
	referralSvc *service.ReferralService
}

// NewAdminReferralHandler 创建管理端邀请返利处理器
func NewAdminReferralHandler(r *service.ReferralService) *AdminReferralHandler {
	return &AdminReferralHandler{referralSvc: r}
}

// referralConfigRequest 返利配置请求
type referralConfigRequest struct {
	Enabled   bool    `json:"enabled"`
	Rate      float64 `json:"rate" binding:"required,min=0,max=1"`
	MaxReward int64   `json:"max_reward_cents"`
}

// GetReferralConfig [A] GET /api/v1/admin/referral/config
// 获取返利配置
func (h *AdminReferralHandler) GetReferralConfig(c *gin.Context) {
	enabled, rate, maxReward := h.referralSvc.GetReferralConfig()
	response.OK(c, gin.H{
		"enabled":          enabled,
		"rate":             rate,
		"max_reward_cents": maxReward,
	})
}

// UpdateReferralConfig [A] PUT /api/v1/admin/referral/config
// 更新返利配置
func (h *AdminReferralHandler) UpdateReferralConfig(c *gin.Context) {
	var req referralConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	if err := h.referralSvc.SetReferralConfig(req.Enabled, req.Rate, req.MaxReward); err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OKMsg(c, "配置已更新")
}

// AdminReferralList [A] GET /api/v1/admin/referrals
// 分页查询全部邀请关系(支持按邀请人/被邀请人筛选)
// 修复 P1-admin_referral: 旧版只返回 config 不返回真实数据, 现返回真实分页列表
func (h *AdminReferralHandler) AdminReferralList(c *gin.Context) {
	page, size := parsePage(c)
	list, total, err := h.referralSvc.ListAllInvitations(page, size)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, gin.H{"list": list, "total": total, "page": page, "size": size})
}

// AdminReferralRewards [A] GET /api/v1/admin/referral/rewards
// 分页查询全部返利记录(管理端对账用)
// 修复 P1-admin_referral: 旧版只返回空列表, 现返回真实分页数据
func (h *AdminReferralHandler) AdminReferralRewards(c *gin.Context) {
	page, size := parsePage(c)
	list, total, err := h.referralSvc.ListAllRewards(page, size)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, gin.H{"list": list, "total": total, "page": page, "size": size})
}
