package handler

import (
	"errors"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"nexus-panel/internal/response"
	"nexus-panel/internal/service"
)

// PlanHandler 套餐处理器
type PlanHandler struct {
	planSvc *service.PlanService
}

// NewPlanHandler 创建套餐处理器
func NewPlanHandler(ps *service.PlanService) *PlanHandler {
	return &PlanHandler{planSvc: ps}
}

// AdminPlanList [28] GET /api/v1/admin/plans
// 管理端套餐列表(含禁用)
func (h *PlanHandler) AdminPlanList(c *gin.Context) {
	page, size := parsePage(c)
	keyword := c.Query("keyword")
	list, total, err := h.planSvc.ListPlans(page, size, keyword)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	// 附加 node_count
	result := make([]gin.H, 0, len(list))
	for _, p := range list {
		count, _ := h.planSvc.CountNodesByPlanID(p.ID)
		result = append(result, gin.H{
			"id":                  p.ID,
			"name":                p.Name,
			"description":         p.Description,
			"features":            p.Features,
			"limitations":         p.Limitations,
			"traffic_limit":       p.TrafficLimit,
			"duration_days":       p.DurationDays,
			"price_cents":         p.PriceCents,
			"original_price_cents": p.OriginalPriceCents,
			"device_limit":        p.DeviceLimit,
			"sort_order":          p.SortOrder,
			"is_enabled":          p.IsEnabled,
			"node_count":          count,
		})
	}
	response.OK(c, gin.H{"list": result, "total": total})
}

// AdminPlanCreate [29] POST /api/v1/admin/plans
func (h *PlanHandler) AdminPlanCreate(c *gin.Context) {
	var in service.CreatePlanInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	p, err := h.planSvc.CreatePlan(&in)
	if err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OK(c, p)
}

// AdminPlanUpdate [30] PUT /api/v1/admin/plans/:id
func (h *PlanHandler) AdminPlanUpdate(c *gin.Context) {
	id := c.Param("id")
	var in service.UpdatePlanInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	p, err := h.planSvc.UpdatePlan(id, &in)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OK(c, p)
}

// AdminPlanDelete [31] DELETE /api/v1/admin/plans/:id
func (h *PlanHandler) AdminPlanDelete(c *gin.Context) {
	id := c.Param("id")
	if err := h.planSvc.DeletePlan(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.Fail(c, response.CodeServerError)
		return
	}
	response.OKMsg(c, "已删除")
}

// UserPlanList [32] GET /api/v1/plans
// 用户端只返回启用的套餐
func (h *PlanHandler) UserPlanList(c *gin.Context) {
	list, err := h.planSvc.ListEnabledPlans()
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	// 附加 node_count
	result := make([]gin.H, 0, len(list))
	for _, p := range list {
		count, _ := h.planSvc.CountNodesByPlanID(p.ID)
		result = append(result, gin.H{
			"id":                  p.ID,
			"name":                p.Name,
			"description":         p.Description,
			"features":            p.Features,
			"limitations":         p.Limitations,
			"traffic_limit":       p.TrafficLimit,
			"duration_days":       p.DurationDays,
			"price_cents":         p.PriceCents,
			"original_price_cents": p.OriginalPriceCents,
			"device_limit":        p.DeviceLimit,
			"sort_order":          p.SortOrder,
			"is_enabled":          p.IsEnabled,
			"node_count":          count,
		})
	}
	response.OK(c, gin.H{"list": result, "total": len(result)})
}
