package handler

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"nexus-panel/internal/app"
	"nexus-panel/internal/middleware"
	"nexus-panel/internal/model"
)

// ============================================================
// 工具函数 (兼容实现, 若项目已有 util 包可直接引用)
// ============================================================

// secureRandInt 加密随机整数 [0, n)
func secureRandInt(n int) (int, error) {
	if n <= 0 {
		return 0, errors.New("invalid range")
	}
	max := big.NewInt(int64(n))
	i, err := rand.Int(rand.Reader, max)
	if err != nil {
		return 0, err
	}
	return int(i.Int64()), nil
}

// getenv 读取环境变量, 带默认值
func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// globalDB / globalRedis 已移除，直接使用 app.Get()

func getDB() *gorm.DB {
	if a := app.Get(); a != nil {
		return a.DB
	}
	return nil
}

func getRedis() *redis.Client {
	if a := app.Get(); a != nil {
		return a.RDB
	}
	return nil
}

// getEmailService 工厂方法
func getEmailService() *EmailService {
	return NewEmailService(getDB(), getRedis())
}

// randomToken 生成 n 位随机 token
func randomToken(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := 0; i < n; i++ {
		idx, _ := secureRandInt(len(letters))
		b[i] = letters[idx]
	}
	return string(b)
}

// getFrontendBase 获取前端地址 (用于拼接邮件链接)
func getFrontendBase() string {
	return strings.TrimRight(getenv("FRONTEND_BASE", "https://panel.example.com"), "/")
}

// invalidateUserTokens 失效某用户所有 token (重置密码后调用)
// 修复 F1: 原实现使用从未赋值的包级变量 globalRedis(恒 nil)导致空指针 panic;
// 且参数为 uint64, 但 User.ID 为 string UUID, 调用方传入 ParseUint 解析失败的 0,
// 即使不 panic 也无法正确失效 token。改为 string uid 并复用 bumpTokenVersion 体系
// (与 ChangePassword/LogoutAll 一致, key=tokver:<role>:<id>)。
func invalidateUserTokens(uid string) {
	if uid == "" {
		return
	}
	// bumpTokenVersion 在 auth.go 中实现 (同 package), 提升 tokver:user:<uid>,
	// 旧 access/refresh token 在下次中间件校验时因 ver 不匹配而被拒绝。
	_ = bumpTokenVersion(context.Background(), uid, "user")
}

// ============================================================
// 业务 handler
// ============================================================

// VerifyEmail 邮箱激活 (用户点邮件里的链接)
// 修复 H1+F1: 原实现 email:verify:link: 全后端无写入点(死接口), 且 strconv.ParseUint
// 解析 UUID 失败导致 uid=0 命中 0 行。现已:
//  1. SendVerifyCode(type=verify) 在发码的同时写 email:verify:link:<token> 并发送链接邮件
//  2. 此处直接以字符串 UID 更新 email_verified
func VerifyEmail(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 40000, "msg": "缺少 token"})
		return
	}
	rdb := getRedis()
	if rdb == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "缓存服务不可用"})
		return
	}
	userIDStr, err := rdb.Get(c.Request.Context(), "email:verify:link:"+token).Result()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 40005, "msg": "链接无效或已过期"})
		return
	}
	result := getDB().Model(&model.User{}).Where("id = ? AND is_deleted = false", userIDStr).
		Update("email_verified", true)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "激活失败"})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusOK, gin.H{"code": 40005, "msg": "账号不存在或已删除"})
		return
	}
	rdb.Del(c.Request.Context(), "email:verify:link:"+token)
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "邮箱已激活"})
}

// ForgotPassword 申请重置 (发送邮件)
func ForgotPassword(c *gin.Context) {
	var req struct {
		Email       string `json:"email" binding:"required,email"`
		// [E-fix 2026-07-14] 图形验证码 (防脚本批量申请重置)
		CaptchaID   string `json:"captcha_id"`
		CaptchaCode string `json:"captcha_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 40000, "msg": "参数错误"})
		return
	}
	if !VerifyCaptcha(c, req.CaptchaID, req.CaptchaCode) {
		c.JSON(http.StatusOK, gin.H{"code": 40010, "msg": "图形验证码错误或已过期"})
		return
	}
	email := normalizeEmail(req.Email)
	rdb := getRedis()
	key := "reset:limit:" + email
	if ttl, _ := rdb.TTL(c.Request.Context(), key).Result(); ttl > 4*time.Minute {
		c.JSON(http.StatusOK, gin.H{"code": 40004, "msg": "请求过于频繁"})
		return
	}
	rdb.Set(c.Request.Context(), key, 1, 5*time.Minute)

	var u model.User
	err := getDB().Where("email = ?", email).First(&u).Error
	if err == nil && u.ID != "" {
		token := randomToken(48)
		rdb.Set(c.Request.Context(), "reset:token:"+token, u.ID, 30*time.Minute)
		link := getFrontendBase() + "/reset-password?token=" + token
		if getenv("EMAIL_DEBUG", "") == "1" {
			c.JSON(http.StatusOK, gin.H{
				"code": 0,
				"msg":  "ok (debug 模式,链接见 data)",
				"data": gin.H{"reset_url": link},
			})
			return
		}
		// 修复: 原实现非 debug 模式下只生成链接存入 Redis 却从不发送邮件,
		// 导致用户永远收不到密码重置链接 (密码重置功能在生产环境完全失效)。
		// 此处异步发送重置邮件, 仍返回统一提示以防通过该接口枚举已注册邮箱。
		es := getEmailService()
		go func(emailTo, linkStr string) {
			subject := "【Nexus-Panel】密码重置"
			body := fmt.Sprintf("您正在重置密码,请点击以下链接完成重置(30 分钟内有效):\n%s\n如非本人操作请忽略本邮件,并建议尽快登录修改密码。", linkStr)
			if err := es.send(emailTo, subject, body); err != nil {
				log.Printf("[email] 重置邮件发送失败 email=%s: %v", emailTo, err)
			}
		}(email, link)
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "如果该邮箱已注册,重置链接已发送",
	})
}

// ResetPassword 使用 token 重置
func ResetPassword(c *gin.Context) {
	var req struct {
		Token       string `json:"token" binding:"required"`
		NewPassword string `json:"new_password" binding:"required,min=8,max=64"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 40000, "msg": "参数错误"})
		return
	}
	rdb := getRedis()
	if rdb == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "缓存服务不可用"})
		return
	}
	userIDStr, err := rdb.Get(c.Request.Context(), "reset:token:"+req.Token).Result()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 40005, "msg": "链接无效或已过期"})
		return
	}
	// 修复 F1: User.ID 为 string UUID(model/user.go), 原代码 strconv.ParseUint 解析 UUID 必然失败(uid=0),
	// 后续 Where("id=?",0) 命中 0 行, 密码实际未更新却返回成功。直接以字符串 UID 更新。
	if !strongEnough(req.NewPassword) {
		c.JSON(http.StatusOK, gin.H{"code": 40000, "msg": "密码必须同时包含字母和数字"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("[AUTH] bcrypt hash failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "密码哈希失败"})
		return
	}
	result := getDB().Model(&model.User{}).Where("id = ? AND is_deleted = false", userIDStr).
		Update("password_hash", string(hash))
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "重置失败"})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusOK, gin.H{"code": 40005, "msg": "账号不存在或已删除"})
		return
	}
	rdb.Del(c.Request.Context(), "reset:token:"+req.Token)
	// 失效该用户所有已签发 token, 防止重置后旧 token 仍可用
	invalidateUserTokens(userIDStr)
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "密码已重置,请重新登录"})
}

// ChangeEmail 已登录用户换绑邮箱
func ChangeEmail(c *gin.Context) {
	var req struct {
		NewEmail    string `json:"new_email" binding:"required,email"`
		VerifyCode  string `json:"verify_code" binding:"required,len=6"`
		OldPassword string `json:"old_password" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 40000, "msg": "参数错误"})
		return
	}
	uid := middleware.GetUserID(c)
	newEmail := normalizeEmail(req.NewEmail)
	es := getEmailService()
	if !es.VerifyEmailCode(c.Request.Context(), newEmail, "change", req.VerifyCode) {
		c.JSON(http.StatusOK, gin.H{"code": 40003, "msg": "验证码错误或已过期"})
		return
	}
	var u model.User
	if err := getDB().Where("id = ?", uid).First(&u).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "用户不存在"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.OldPassword)); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 40105, "msg": "原密码错误"})
		return
	}
	if err := getDB().Model(&model.User{}).Where("id = ?", uid).
		Updates(map[string]interface{}{
			"email":          newEmail,
			"email_verified": true,
		}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "换绑失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "邮箱已换绑"})
}

// 注: ChangePassword 已在 auth.go 中实现, 本文件不重复定义

// strongEnough 密码强度 (字母 + 数字)
func strongEnough(p string) bool {
	hasLetter, hasDigit := false, false
	for _, ch := range p {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z':
			hasLetter = true
		case ch >= '0' && ch <= '9':
			hasDigit = true
		}
	}
	return hasLetter && hasDigit
}
