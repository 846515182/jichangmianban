package handler

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/gin-gonic/gin"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/response"
)

// ListInviteCodes GET /api/v1/admin/invite-codes
// 查询所有邀请码
func ListInviteCodes(c *gin.Context) {
	db := app.Get().DB
	var list []model.InviteCode
	if err := db.Order("created_at DESC").Find(&list).Error; err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	response.OK(c, gin.H{"list": list})
}

// CreateInviteCode POST /api/v1/admin/invite-codes
// 创建邀请码
func CreateInviteCode(c *gin.Context) {
	var req struct {
		MaxUses   int    `json:"max_uses"`
		ExpiresAt string `json:"expires_at"` // RFC3339 格式
		Note      string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		req.MaxUses = 1
	}

	// 生成随机邀请码
	codeBytes := make([]byte, 8)
	rand.Read(codeBytes)
	code := hex.EncodeToString(codeBytes)[:16]

	createdBy := ""
	if uid, ok := c.Get("user_id"); ok {
		createdBy = uid.(string)
	}

	ic := model.InviteCode{
		Code:      code,
		CreatedBy: createdBy,
		MaxUses:   req.MaxUses,
		UsedCount: 0,
		Disabled:  false,
		Note:      req.Note,
	}
	if req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err == nil {
			ic.ExpiresAt = &t
		}
	}
	if req.MaxUses <= 0 {
		ic.MaxUses = 1
	}

	db := app.Get().DB
	if err := db.Create(&ic).Error; err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}

	response.OK(c, gin.H{"code": ic})
}

// DisableInviteCode POST /api/v1/admin/invite-codes/:id/disable
// 禁用邀请码
func DisableInviteCode(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Fail(c, response.CodeParamError)
		return
	}

	db := app.Get().DB
	if err := db.Model(&model.InviteCode{}).Where("id = ?", id).
		Update("disabled", true).Error; err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}

	response.OKMsg(c, "邀请码已禁用")
}