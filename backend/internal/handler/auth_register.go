package handler

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"nexus-panel/internal/app"
	"nexus-panel/internal/response"
	"nexus-panel/internal/service"
)

// AuthRegisterHandler 用户注册处理器(独立于 AuthHandler, 便于后续扩展邀请码等)
type AuthRegisterHandler struct {
	registerSvc *service.UserRegisterService
}

// NewAuthRegisterHandler 创建用户注册处理器
func NewAuthRegisterHandler(r *service.UserRegisterService) *AuthRegisterHandler {
	return &AuthRegisterHandler{registerSvc: r}
}

// registerRequest 注册请求
type registerRequest struct {
	Username   string `json:"username" binding:"required"`
	Password   string `json:"password" binding:"required"`
	Email      string `json:"email"`
	InviteCode string `json:"invite_code"`
	// [E-fix 2026-07-14] 图形验证码 (从 GetCaptcha 拉取, 后端校验)
	CaptchaID  string `json:"captcha_id"`
	CaptchaCode string `json:"captcha_code"`
}

// Register [45] POST /api/v1/auth/register
// 用户注册, 默认状态 active, traffic_limit=0(需购买套餐)
// 限制: 同一 IP 每小时最多注册 3 个账号
// 邀请码: 走数据库 invite_codes 表 (ConsumeInviteCode 事务内消费)
func (h *AuthRegisterHandler) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}

	// [E-fix 2026-07-14] 图形验证码校验 (防脚本批量注册)
	if !VerifyCaptcha(c, req.CaptchaID, req.CaptchaCode) {
		response.FailMsg(c, response.CodeParamError, "图形验证码错误或已过期")
		return
	}

	// [E-fix 2026-07-14] 若 email 为空, 生成占位邮箱 uuid@placeholder.local
	// 避免 uq_users_email_lower 唯一索引冲突; 用户后续可在修改邮箱流程中绑定真实邮箱
	if strings.TrimSpace(req.Email) == "" {
		req.Email = uuid.New().String() + "@placeholder.local"
	}

	inviteRequired := app.Get().Cfg.InviteCodeRequired // 从配置中心读取
	if inviteRequired && req.InviteCode == "" {
		response.FailMsg(c, response.CodeParamError, "注册需要有效的邀请码")
		return
	}

	// IP 注册频率限制
	ip := c.ClientIP()
	rdb := app.Get().RDB
	if rdb != nil {
		key := fmt.Sprintf("register:ip:%s", ip)
		count, err := rdb.Incr(c.Request.Context(), key).Result()
		if err == nil {
			if count == 1 {
				rdb.Expire(c.Request.Context(), key, 1*time.Hour)
			}
			if count > 3 {
				response.FailMsg(c, response.CodeRateLimit, "该 IP 注册过于频繁，请稍后再试")
				return
			}
		}
	}

	// [S9 fix 2026-07-14] 事务内: 创建用户 + 消费邀请码 (任一失败回滚)
	db := h.registerSvc.GetUserRepo().GetDB()
	u, err := h.registerWithTx(c, db, &req, ip)
	if err != nil {
		if errors.Is(err, service.ErrDuplicate) {
			response.Fail(c, response.CodeDuplicate)
			return
		}
		if errors.Is(err, service.ErrInviteCodeInvalid) ||
			errors.Is(err, service.ErrInviteCodeExhausted) ||
			errors.Is(err, service.ErrInviteCodeExpired) {
			response.FailMsg(c, response.CodeParamError, err.Error())
			return
		}
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}

	response.OK(c, gin.H{
		"id":       u.ID,
		"username": u.Username,
		"email":    u.Email,
		"status":   u.Status,
	})
}

// registerWithTx 在事务内完成用户创建 + 邀请码消费
func (h *AuthRegisterHandler) registerWithTx(c *gin.Context, db *gorm.DB, req *registerRequest, ip string) (*service.RegisteredUser, error) {
	tx := db.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	u, err := h.registerSvc.Register(&service.RegisterInput{
		Username: req.Username,
		Password: req.Password,
		Email:    req.Email,
		DB:       tx,
	})
	if err != nil {
		return nil, err
	}

	if req.InviteCode != "" {
		ua := c.Request.UserAgent()
		if _, err := ConsumeInviteCode(tx, req.InviteCode, u.ID, ip, ua); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	committed = true
	return u, nil
}
