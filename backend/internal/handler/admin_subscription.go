package handler

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"nexus-panel/internal/app"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/response"
	"nexus-panel/internal/service"
)

// AdminSubscriptionHandler 管理端订阅管理
type AdminSubscriptionHandler struct {
	subRepo *repo.SubscriptionRepo
	subSvc  *service.SubscribeService
}

func NewAdminSubscriptionHandler(subRepo *repo.SubscriptionRepo, subSvc *service.SubscribeService) *AdminSubscriptionHandler {
	return &AdminSubscriptionHandler{subRepo: subRepo, subSvc: subSvc}
}

// List 订阅列表(带订阅链接)
func (h *AdminSubscriptionHandler) List(c *gin.Context) {
	page, size := parsePage(c)
	keyword := c.Query("keyword")
	list, total, err := h.subRepo.List(page, size, keyword)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}

	// 生成每个用户的订阅链接
	baseURL := getBaseURL(c)
	type item struct {
		repo.SubscriptionWithUser
		SubscribeURL string `json:"subscribe_url"`
	}
	result := make([]item, 0, len(list))
	for _, s := range list {
		url, _ := h.subSvc.GenerateSignedURL(s.UserID, baseURL, c.ClientIP())
		result = append(result, item{SubscriptionWithUser: s, SubscribeURL: url})
	}
	response.OK(c, gin.H{"list": result, "total": total})
}

// GetByUserID 查询指定用户的订阅(带订阅链接)
func (h *AdminSubscriptionHandler) GetByUserID(c *gin.Context) {
	userID := c.Param("id")
	sub, err := h.subRepo.GetByUserID(userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 40400, "msg": "用户无订阅记录"})
		return
	}
	baseURL := getBaseURL(c)
	url, _ := h.subSvc.GenerateSignedURL(userID, baseURL, c.ClientIP())
	response.OK(c, gin.H{
		"id":            sub.ID,
		"user_id":       sub.UserID,
		"sub_token":     sub.SubToken,
		"sub_type":      sub.SubType,
		"expires_at":    sub.ExpiresAt,
		"created_at":    sub.CreatedAt,
		"subscribe_url": url,
	})
}

// getBaseURL 从请求头或配置获取面板 baseURL
func getBaseURL(c *gin.Context) string {
	if domain := app.Get().Cfg.PanelDomain; domain != "" {
		return domain
	}
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, c.Request.Host)
}
