package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/service"
)

// EmailService 邮件服务
type EmailService struct {
	DB    *gorm.DB
	Redis *redis.Client
	svc   *service.EmailService
}

func NewEmailService(db *gorm.DB, rdb *redis.Client) *EmailService {
	// 委托给 service.EmailService: 统一从数据库 notification 设置(优先) +
	// 环境变量 SMTP_* (回退) 读取配置, 并复用支持 465 隐式 TLS / STARTTLS 的发送实现。
	var svc *service.EmailService
	if a := app.Get(); a != nil {
		svc = service.NewEmailService(repo.NewSettingRepo(db), a.Cfg)
	}
	return &EmailService{DB: db, Redis: rdb, svc: svc}
}

const (
	emailVerifyTTL = 10 * time.Minute
	emailResetTTL  = 30 * time.Minute
)

// GenerateVerifyCode 生成 6 位数字验证码
func GenerateVerifyCode() string {
	const digits = "0123456789"
	code := make([]byte, 6)
	for i := 0; i < 6; i++ {
		n, _ := rand.Int(rand.Reader, big.NewInt(10))
		code[i] = digits[n.Int64()]
	}
	return string(code)
}

func hashCode(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}

// SendVerifyCode 同步发送验证码 (API 入口, 限频保护)
// 修复 H1: type=verify 时走"邮箱激活链接"流程(写 email:verify:link:<token> 并发送链接邮件),
// 使 GET /api/v1/auth/verify-email?token=xxx 能正常激活邮箱;
// type=change 仍走 6 位验证码流程(供 ChangeEmail 使用)。
func (s *EmailService) SendVerifyCode(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
		Type  string `json:"type" binding:"required,oneof=verify change"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"code": 40000, "msg": "参数错误"})
		return
	}
	email := normalizeEmail(req.Email)
	rdb := s.Redis
	key := "email:limit:" + email + ":" + req.Type
	if ttl, _ := rdb.TTL(c.Request.Context(), key).Result(); ttl > 50*time.Second {
		c.JSON(200, gin.H{"code": 40004, "msg": "请求过于频繁,请稍后再试"})
		return
	}
	rdb.Set(c.Request.Context(), key, 1, time.Minute)

	// type=verify: 发送激活链接
	if req.Type == "verify" {
		if err := s.SendVerifyLink(c.Request.Context(), email); err != nil {
			c.JSON(500, gin.H{"code": 500, "msg": "发送失败: " + err.Error()})
			return
		}
		c.JSON(200, gin.H{
			"code": 0,
			"msg":  "激活链接已发送,请查收邮箱",
			"data": gin.H{"expire_in": int(emailResetTTL.Seconds())},
		})
		return
	}

	// type=change: 发送 6 位验证码
	if err := s.SendVerifyCodeAsync("", email, req.Type); err != nil {
		c.JSON(500, gin.H{"code": 500, "msg": "发送失败: " + err.Error()})
		return
	}
	respData := gin.H{"expire_in": 600}
	if os.Getenv("EMAIL_DEBUG") == "1" {
		if saved, err := rdb.Get(c.Request.Context(), "email:code:"+req.Type+":"+email).Result(); err == nil {
			respData["code"] = saved
			respData["debug"] = true
		}
	}
	c.JSON(200, gin.H{
		"code": 0,
		"msg":  "验证码已发送,请查收邮箱",
		"data": respData,
	})
}

// SendVerifyLink 生成邮箱激活链接并写 Redis + 发邮件
// 修复 H1: 补全 email:verify:link:<token> 的写入点, key=value 为 token->user.ID(string UUID)
// 仅对已注册且未激活的用户发送; 未注册用户静默成功(防用户枚举)。
func (s *EmailService) SendVerifyLink(ctx context.Context, email string) error {
	email = normalizeEmail(email)
	// 仅查 id 与 email_verified(model.User 未映射 email_verified, 走原生 Select)
	var row struct {
		ID            string
		EmailVerified bool
	}
	err := s.DB.Table("users").
		Select("id, email_verified").
		Where("email = ? AND is_deleted = false", email).
		Take(&row).Error
	if err != nil {
		// 用户不存在: 静默返回成功, 防止通过该接口枚举已注册邮箱
		return nil
	}
	if row.EmailVerified {
		return nil // 已激活, 不重复发送
	}
	token := randomToken(48)
	if err := s.Redis.Set(ctx, "email:verify:link:"+token, row.ID, emailResetTTL).Err(); err != nil {
		return err
	}
	link := getFrontendBase() + "/verify-email?token=" + token
	go func(emailTo, linkStr, userID string) {
		subject := "【Nexus-Panel】邮箱激活"
		body := fmt.Sprintf("请点击以下链接激活邮箱(30 分钟内有效):\n%s\n如非本人操作请忽略本邮件。", linkStr)
		if os.Getenv("EMAIL_DEBUG") == "1" {
			log.Printf("[EMAIL_DEBUG] 激活链接 收件人=%s userID=%s link=%s", emailTo, userID, linkStr)
			return
		}
		if err := s.send(emailTo, subject, body); err != nil {
			log.Printf("[email] 激活链接发送失败 userID=%s: %v", userID, err)
		}
	}(email, link, row.ID)
	return nil
}

// SendVerifyCodeAsync 异步发送验证码 (供注册成功/换绑流程调用)
func (s *EmailService) SendVerifyCodeAsync(userID string, email, typ string) error {
	if typ == "" {
		typ = "verify"
	}
	email = normalizeEmail(email)
	code := GenerateVerifyCode()
	codeHash := hashCode(code)
	ev := model.EmailEvent{
		Email:     email,
		EventType: "verify_" + typ,
		CodeHash:  codeHash,
		SentAt:    time.Now(),
		Success:   false,
	}
	if userID != "" {
		// userID 是 uint64, 但 users.id 是 uuid, 这里用 string 形式保存
		// 注: 调用方需将 uuid 字符串传入 (当前由 auth_register 传入 u.ID 是 uuid 字符串)
		sid := userID
		ev.UserID = &sid
	}
	if err := s.DB.Create(&ev).Error; err != nil {
		log.Printf("[email] 写事件失败: %v", err)
	}
	ctx := context.Background()
	if err := s.Redis.Set(ctx, "email:code:"+typ+":"+email, code, emailVerifyTTL).Err(); err != nil {
		log.Printf("[email] 存 redis 失败: %v", err)
		return err
	}
	go func(emailTo, codePlain string, evID uint64) {
		subject := "【Nexus-Panel】邮箱验证码"
		body := fmt.Sprintf("您的验证码是: %s,10 分钟内有效。请勿泄露给他人。", codePlain)
		var sendErr error
		if os.Getenv("EMAIL_DEBUG") == "1" {
			log.Printf("[EMAIL_DEBUG] 收件人: %s | 验证码: %s | 主题: %s", emailTo, codePlain, subject)
		} else {
			sendErr = s.send(emailTo, subject, body)
		}
		if sendErr != nil {
			log.Printf("[email] 发送失败 evID=%d: %v", evID, sendErr)
			s.DB.Model(&model.EmailEvent{}).Where("id = ?", evID).
				Updates(map[string]interface{}{"success": false, "error_msg": sendErr.Error()})
		} else {
			s.DB.Model(&model.EmailEvent{}).Where("id = ?", evID).Update("success", true)
		}
	}(email, code, ev.ID)
	return nil
}

// send 真实 SMTP 发送 (委托给 service.EmailService, 统一数据库/环境变量配置 + TLS)
func (s *EmailService) send(to, subject, body string) error {
	if s.svc == nil {
		return fmt.Errorf("邮件服务未初始化")
	}
	return s.svc.SendMail([]string{to}, subject, body)
}

// normalizeEmail 邮箱归一化 (lowercase + trim)
func normalizeEmail(e string) string {
	e = trimSpace(e)
	out := make([]byte, len(e))
	for i := 0; i < len(e); i++ {
		c := e[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		out[i] = c
	}
	return string(out)
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// VerifyEmailCode 校验验证码
func (s *EmailService) VerifyEmailCode(ctx context.Context, email, typ, code string) bool {
	saved, err := s.Redis.Get(ctx, "email:code:"+typ+":"+email).Result()
	if err != nil {
		return false
	}
	// 恒定时间比较, 避免验证码比较时序侧信道 (验证码短时有效, 仍遵循最佳实践)
	if subtle.ConstantTimeCompare([]byte(saved), []byte(code)) != 1 {
		return false
	}
	s.Redis.Del(ctx, "email:code:"+typ+":"+email)
	return true
}
