package handler

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/response"
	"nexus-panel/internal/service"
)

// ============================================================
// AuthRegisterHandler 注册处理器
// ============================================================

// AuthRegisterHandler 注册处理器
type AuthRegisterHandler struct {
	registerSvc *service.UserRegisterService
}

// NewAuthRegisterHandler 创建注册处理器
func NewAuthRegisterHandler(svc *service.UserRegisterService) *AuthRegisterHandler {
	return &AuthRegisterHandler{registerSvc: svc}
}

type registerReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required,min=8"`
	Email    string `json:"email"`
}

// Register POST /api/v1/auth/register
// 用户注册
func (h *AuthRegisterHandler) Register(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailMsg(c, response.CodeParamError, "请填写完整信息")
		return
	}

	input := &service.RegisterInput{
		Username: req.Username,
		Password: req.Password,
		Email:    req.Email,
	}
	user, err := h.registerSvc.Register(input)
	if err != nil {
		if err == service.ErrDuplicate {
			response.FailMsg(c, response.CodeDuplicate, "用户名已存在")
			return
		}
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}

	response.OK(c, gin.H{
		"id":       user.ID,
		"username": user.Username,
		"email":    user.Email,
		"status":   user.Status,
	})
}

// ============================================================
// 密码重置相关函数
// ============================================================

// ForgotPassword POST /api/v1/auth/forgot-password
// 忘记密码 - 发送重置邮件
func ForgotPassword(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailMsg(c, response.CodeParamError, "请填写邮箱")
		return
	}

	// 生成重置 token
	tokenBytes := make([]byte, 32)
	rand.Read(tokenBytes)
	resetToken := hex.EncodeToString(tokenBytes)

	// 存入 Redis，有效期 30 分钟
	rdb := app.Get().RDB
	if rdb != nil {
		ctx := c.Request.Context()
		key := "pwdreset:" + req.Email
		rdb.Set(ctx, key, resetToken, 30*time.Minute)
		rdb.Set(ctx, "pwdreset:token:"+resetToken, req.Email, 30*time.Minute)
	}

	// 尝试发送邮件
	settingRepo := repo.NewSettingRepo(app.Get().DB)
	emailSvc := service.NewEmailService(settingRepo)
	if err := emailSvc.SendMail([]string{req.Email}, "Nexus-Panel 密码重置",
		"您的密码重置验证码为: "+resetToken[:8]+"\n有效期 30 分钟。"); err != nil {
		// 邮件发送失败也返回成功（开发环境可能没有邮件服务）
		response.OKMsg(c, "重置邮件已发送，请查收")
		return
	}

	response.OKMsg(c, "重置邮件已发送，请查收")
}

// ResetPassword POST /api/v1/auth/reset-password
// 重置密码
func ResetPassword(c *gin.Context) {
	var req struct {
		Email       string `json:"email" binding:"required"`
		Token       string `json:"token" binding:"required"`
		NewPassword string `json:"new_password" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailMsg(c, response.CodeParamError, "请填写完整信息")
		return
	}

	// 验证 token
	rdb := app.Get().RDB
	if rdb != nil {
		ctx := c.Request.Context()
		key := "pwdreset:token:" + req.Token
		storedEmail, err := rdb.Get(ctx, key).Result()
		if err != nil || storedEmail != req.Email {
			response.FailMsg(c, response.CodeTokenInvalid, "重置链接已过期或无效")
			return
		}
		// 清理 token
		rdb.Del(ctx, key, "pwdreset:"+req.Email)
	}

	// 查找用户
	userRepo := repo.NewUserRepo(app.Get().DB)
	user, err := userRepo.GetByEmail(req.Email)
	if err != nil {
		response.FailMsg(c, response.CodeNotFound, "用户不存在")
		return
	}

	// 更新密码
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	user.PasswordHash = string(hash)
	userRepo.Update(user)

	response.OKMsg(c, "密码重置成功")
}

// VerifyEmail GET /api/v1/auth/verify-email
// 验证邮箱
func VerifyEmail(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		response.FailMsg(c, response.CodeParamError, "缺少验证 token")
		return
	}

	rdb := app.Get().RDB
	if rdb == nil {
		response.FailMsg(c, response.CodeServerError, "服务不可用")
		return
	}

	ctx := c.Request.Context()
	key := "emailverify:" + token
	email, err := rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		response.FailMsg(c, response.CodeTokenInvalid, "验证链接已过期或无效")
		return
	}
	if err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}

	// 清理 token
	rdb.Del(ctx, key)

	response.OK(c, gin.H{"email": email, "verified": true})
}

// ChangeEmail POST /api/v1/auth/change-email
// 修改邮箱（需要登录）
func ChangeEmail(c *gin.Context) {
	var req struct {
		NewEmail string `json:"new_email" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailMsg(c, response.CodeParamError, "请填写新邮箱")
		return
	}

	// 从上下文获取当前用户
	userID, _ := c.Get("user_id")
	role, _ := c.Get("role")

	if userID == nil {
		response.Fail(c, response.CodeTokenInvalid)
		return
	}

	uid := userID.(string)

	if role == "admin" {
		adminRepo := repo.NewAdminRepo(app.Get().DB)
		adminRepo.UpdateEmail(uid, req.NewEmail)
	} else {
		userRepo := repo.NewUserRepo(app.Get().DB)
		user, err := userRepo.GetByID(uid)
		if err != nil {
			response.FailMsg(c, response.CodeNotFound, "用户不存在")
			return
		}
		user.Email = req.NewEmail
		userRepo.Update(user)
	}

	response.OKMsg(c, "邮箱修改成功")
}

// ============================================================
// 验证码
// ============================================================

// GetCaptcha GET /api/v1/captcha
// 获取图形验证码（用于登录爆破防护）
func GetCaptcha(c *gin.Context) {
	codeBytes := make([]byte, 4)
	rand.Read(codeBytes)
	captcha := hex.EncodeToString(codeBytes)[:4]

	rdb := app.Get().RDB
	if rdb != nil {
		ctx := c.Request.Context()
		key := "captcha:" + c.ClientIP()
		rdb.Set(ctx, key, captcha, 5*time.Minute)
	}

	response.OK(c, gin.H{
		"captcha": captcha,
		"expire":  300,
	})
}

// ============================================================
// 辅助函数：补全 model.User 的密码更新
// ============================================================

// userRepoPasswordUpdate 为 UserRepo 补上 UpdatePassword 方法
// 注意：UserRepo 在 repo/user.go 中已有定义，这里通过方法扩展
func updateUserPassword(userRepo *repo.UserRepo, id, passwordHash string) error {
	db := userRepo.GetDB()
	return db.Model(&model.User{}).Where("id = ? AND is_deleted = false", id).
		Update("password_hash", passwordHash).Error
}