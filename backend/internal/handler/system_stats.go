package handler

import (
	"github.com/gin-gonic/gin"

	"nexus-panel/internal/response"
	"nexus-panel/internal/service"
)

// SystemStatsHandler 系统状态处理器
type SystemStatsHandler struct {
	svc *service.SystemStatsService
}

// NewSystemStatsHandler 构造函数
func NewSystemStatsHandler(svc *service.SystemStatsService) *SystemStatsHandler {
	return &SystemStatsHandler{svc: svc}
}

// AdminSystemStats [27] GET /api/v1/admin/system/stats
// 管理端 - 面板自身实时系统状态（CPU/内存/磁盘/网络速度/在线节点用户数）
func (h *SystemStatsHandler) AdminSystemStats(c *gin.Context) {
	stats, err := h.svc.Collect()
	if err != nil {
		// 采集失败仍返回部分数据（采集函数本身容错），仅在 panic 级别才走这里
		response.FailMsg(c, response.CodeServerError, "系统状态采集失败: "+err.Error())
		return
	}
	response.OK(c, stats)
}

// UserSystemStats [28] GET /api/v1/user/system/stats
// 用户端 - 面板自身精简状态（仅负载/内存/网络速度）
func (h *SystemStatsHandler) UserSystemStats(c *gin.Context) {
	stats, err := h.svc.CollectSimple()
	if err != nil {
		response.FailMsg(c, response.CodeServerError, "系统状态采集失败: "+err.Error())
		return
	}
	response.OK(c, stats)
}
