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
		"enabled":           enabled,
		"rate":              rate,
		"max_reward_cents":  maxReward,
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
func (h *AdminReferralHandler) AdminReferralList(c *gin.Context) {
	// 注: ListByInviterID 是按邀请人查, 管理端全量查询需要 repo 层补充 ListAll
	// 当前先返回邀请码配置和统计, 后续可扩展
	page, size := parsePage(c)
	_ = page
	_ = size
	// 暂返回配置信息 + 全量统计(从设置中读取)
	enabled, rate, maxReward := h.referralSvc.GetReferralConfig()
	response.OK(c, gin.H{
		"config": gin.H{
			"enabled":          enabled,
			"rate":             rate,
			"max_reward_cents": maxReward,
		},
	})
}

// AdminReferralRewards [A] GET /api/v1/admin/referral/rewards
// 分页查询全部返利记录(管理端对账用)
func (h *AdminReferralHandler) AdminReferralRewards(c *gin.Context) {
	page, size := parsePage(c)
	userID := c.Query("user_id")
	_ = userID
	// 注: 完整的管理端列表需要 repo 层补充 ListAllRewards 方法
	// 当前先返回空列表 + 分页框架, 避免接口不存在
	list := []interface{}{}
	response.OK(c, gin.H{"list": list, "total": 0, "page": page, "size": size})
}
