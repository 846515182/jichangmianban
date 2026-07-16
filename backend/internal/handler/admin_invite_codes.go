package handler

import (
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"nexus-panel/internal/middleware"
	"nexus-panel/internal/model"
	"nexus-panel/internal/service"
)

const inviteCodeChars = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

func generateInviteCode(length int) string {
	out := make([]byte, length)
	for i := 0; i < length; i++ {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(inviteCodeChars))))
		out[i] = inviteCodeChars[n.Int64()]
	}
	return string(out)
}

// CreateInviteCode admin: 生成邀请码
func CreateInviteCode(c *gin.Context) {
	var req struct {
		MaxUses   int    `json:"max_uses" binding:"min=1"`
		ExpiresIn int    `json:"expires_in"`
		Note      string `json:"note" binding:"max=200"`
		Length    int    `json:"length" binding:"min=8,max=16"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 40000, "msg": "参数错误"})
		return
	}
	if req.Length == 0 {
		req.Length = 12
	}
	if req.MaxUses == 0 {
		req.MaxUses = -1
	}
	uid := middleware.GetUserID(c)
	var expiresAt *time.Time
	if req.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(req.ExpiresIn) * time.Hour)
		expiresAt = &t
	}
	ic := model.InviteCode{
		Code:      generateInviteCode(req.Length),
		CreatedBy: uid,
		MaxUses:   req.MaxUses,
		ExpiresAt: expiresAt,
		Note:      req.Note,
	}
	if err := getDB().Create(&ic).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "生成失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": ic})
}

// ListInviteCodes admin: 列出邀请码
func ListInviteCodes(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	var total int64
	var codes []model.InviteCode
	db := getDB()
	db.Model(&model.InviteCode{}).Count(&total)
	db.Order("id DESC").Limit(size).Offset((page - 1) * size).Find(&codes)
	c.JSON(http.StatusOK, gin.H{
		"code": 0, "msg": "ok",
		"data": gin.H{"list": codes, "total": total, "page": page, "size": size},
	})
}

// DisableInviteCode admin: 禁用邀请码
func DisableInviteCode(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	if err := getDB().Model(&model.InviteCode{}).Where("id = ?", id).
		Update("disabled", true).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "操作失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
}


// ConsumeInviteCode [S9 fix 2026-07-14] 在事务内消费邀请码
// 1) 校验存在 + 未禁用 + 未过期 + 未超额
// 2) 原子递增 used_count (带条件防止超额)
// 3) 记录使用明细 invite_code_uses
// userID 为 uuid 字符串; 返回 invite_codes.ID (uint64)
func ConsumeInviteCode(tx *gorm.DB, code, userID, ip, ua string) (uint64, error) {
	if tx == nil {
		return 0, errors.New("ConsumeInviteCode: tx 不能为空")
	}
	if code == "" {
		return 0, service.ErrInviteCodeInvalid
	}
	if userID == "" {
		return 0, errors.New("userID 不能为空")
	}
	var ic model.InviteCode
	if err := tx.Where("code = ? AND disabled = false", code).First(&ic).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, service.ErrInviteCodeInvalid
		}
		return 0, err
	}
	if ic.ExpiresAt != nil && ic.ExpiresAt.Before(time.Now()) {
		return 0, service.ErrInviteCodeExpired
	}
	if ic.MaxUses > 0 && ic.UsedCount >= ic.MaxUses {
		return 0, service.ErrInviteCodeExhausted
	}
	// 原子递增, 条件防止并发超额
	res := tx.Model(&model.InviteCode{}).
		Where("id = ? AND disabled = false AND (max_uses = -1 OR used_count < max_uses)", ic.ID).
		Update("used_count", gorm.Expr("used_count + 1"))
	if res.Error != nil {
		return 0, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, service.ErrInviteCodeExhausted
	}
	// 记录使用明细 (UA 截断到 512)
	uaTrunc := ua
	if len(uaTrunc) > 512 {
		uaTrunc = uaTrunc[:512]
	}
	use := model.InviteCodeUse{
		InviteCodeID: ic.ID,
		Code:         ic.Code,
		UserID:       userID,
		UsedAt:       time.Now(),
		IP:           ip,
		UA:           uaTrunc,
	}
	if err := tx.Create(&use).Error; err != nil {
		return 0, err
	}
	return ic.ID, nil
}
