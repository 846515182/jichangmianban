package handler

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"nexus-panel/internal/app"
	"nexus-panel/internal/middleware"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/response"
	"nexus-panel/internal/security"
	"nexus-panel/internal/service"
)

// AuthHandler 认证处理器
type AuthHandler struct {
	adminRepo      *repo.AdminRepo
	userRepo       *repo.UserRepo
	loginAuditRepo *repo.LoginAuditRepo
	jwtMgr         *security.JWTManager
	emailSvc       *service.EmailService // P0-2: 用于发送邮箱验证码
}

// bcryptCost 与 service.user_register_service / service.user_service 保持一致,
// 避免改密码产生的哈希 cost 比注册时弱(P2 fix 2026-07-19)
const bcryptCost = 12

// NewAuthHandler 创建认证处理器
func NewAuthHandler(a *repo.AdminRepo, u *repo.UserRepo, la *repo.LoginAuditRepo, jwtMgr *security.JWTManager, emailSvc *service.EmailService) *AuthHandler {
	return &AuthHandler{adminRepo: a, userRepo: u, loginAuditRepo: la, jwtMgr: jwtMgr, emailSvc: emailSvc}
}

// loginRequest 登录请求
type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	Target   string `json:"target"` // admin / user，默认 admin
}

// tokenResponse token 响应
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Role         string `json:"role"`
}

// Login [1] POST /api/v1/auth/login
// 支持管理员与用户登录，target 默认 admin
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	if req.Target == "" {
		req.Target = security.RoleAdmin
	}
	ip := c.ClientIP()
	ua := c.GetHeader("User-Agent")
	ctx := c.Request.Context()

	switch req.Target {
	case security.RoleAdmin:
		h.adminLogin(c, ctx, &req, ip, ua)
	case security.RoleUser:
		h.userLogin(c, ctx, &req, ip, ua)
	default:
		response.Fail(c, response.CodeParamError)
	}
}

// isAccountLocked 检查账户是否被锁定(LockUntil 字段生效)
func isAccountLocked(lockUntil *time.Time) bool {
	return lockUntil != nil && lockUntil.After(time.Now())
}

// adminLogin 管理员登录
//
// 修复 CRITICAL 2026-07-19: 账号锁定计数与 user 隔离
// 事故: 前端 loginAuto 会先试 admin 再试 user, 单次登录操作产生 2 次失败计数
// (admin 失败 + user 失败), 都计入同一个 loginfail:acc:{username} key,
// 导致用户试 2-3 次密码就被锁(loginlock:acc:{username}), 永远登不上。
//
// 修复: admin 登录的失败计数 key 用 "admin:{username}", 与 user 的 "{username}"
// 完全隔离。admin 尝试失败(loginAuto 的预期行为)不消耗 user 账号的失败次数。
// IP 维度仍共享(防爆破), 但账号维度独立。
func (h *AuthHandler) adminLogin(c *gin.Context, ctx context.Context, req *loginRequest, ip, ua string) {
	// admin 账号维度的 key 加 "admin:" 前缀, 与 user 账号隔离
	adminAccKey := "admin:" + req.Username

	// P0-LoginBrute: 先检查锁定状态再查 DB, 避免攻击者用大量不存在的用户名打 DB / 枚举。
	// 检查账号锁定(admin 账号维度 + IP 维度)
	// P0-LoginIP: 同时检查 IP 维度, 防分布式撞库
	if locked, _ := middleware.CheckLoginLocked(ctx, ip, adminAccKey); locked {
		response.Fail(c, response.CodeAccountLocked)
		return
	}
	admin, err := h.adminRepo.GetByUsername(req.Username)
	if err != nil {
		// 记录失败(用 admin: 前缀的 key, 不影响 user 账号)
		middleware.RecordLoginFail(ctx, ip, adminAccKey)
		h.recordAudit(ctx, "admin", "", req.Username, ip, ua, false)
		response.Fail(c, response.CodeAccountPwdError)
		return
	}
	// P0-LockUntil: 校验管理员账户锁定时间
	if isAccountLocked(admin.LockUntil) {
		response.Fail(c, response.CodeAccountLocked)
		return
	}
	if admin.Status == "disabled" {
		response.Fail(c, response.CodeAccountDisabled)
		return
	}
	// 校验密码
	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)); err != nil {
		_, locked := middleware.RecordLoginFail(ctx, ip, adminAccKey)
		h.recordAudit(ctx, "admin", admin.ID, admin.Username, ip, ua, false)
		if locked {
			response.Fail(c, response.CodeAccountLocked)
			return
		}
		response.Fail(c, response.CodeAccountPwdError)
		return
	}
	// 登录成功
	middleware.RecordLoginSuccess(ctx, ip, adminAccKey)
	now := time.Now()
	_ = h.adminRepo.UpdateLastLogin(admin.ID, ip, now)
	h.recordAudit(ctx, "admin", admin.ID, admin.Username, ip, ua, true)

	role := admin.Role
	if role == "" {
		role = security.RoleAdmin
	}
	ver := getCurrentTokenVersion(c.Request.Context(), admin.ID, role)
	access, refresh, err := h.jwtMgr.GenerateTokenPairWithVer(admin.ID, admin.Username, role, ver)
	if err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	c.JSON(http.StatusOK, response.Result{
		Code: response.CodeSuccess,
		Data: tokenResponse{
			AccessToken:  access,
			RefreshToken: refresh,
			ExpiresIn:    int64(app.Get().Cfg.JWTAccessTTL.Seconds()),
			Role:         role,
		},
		Msg: response.Msg(response.CodeSuccess),
	})
}

// userLogin 用户登录
func (h *AuthHandler) userLogin(c *gin.Context, ctx context.Context, req *loginRequest, ip, ua string) {
	// P0-LoginBrute: 先检查锁定状态再查 DB, 避免攻击者用大量不存在的用户名打 DB / 枚举。
	// P0-LoginIP: 同时检查账号维度 + IP 维度锁定
	if locked, _ := middleware.CheckLoginLocked(ctx, ip, req.Username); locked {
		response.Fail(c, response.CodeAccountLocked)
		return
	}
	user, err := h.userRepo.GetByUsername(req.Username)
	if err != nil {
		middleware.RecordLoginFail(ctx, ip, req.Username)
		h.recordAudit(ctx, "user", "", req.Username, ip, ua, false)
		response.Fail(c, response.CodeAccountPwdError)
		return
	}
	// P0-LockUntil: 校验用户账户锁定时间
	if isAccountLocked(user.LockUntil) {
		response.Fail(c, response.CodeAccountLocked)
		return
	}
	if user.Status == "disabled" {
		response.Fail(c, response.CodeAccountDisabled)
		return
	}
	// 过期用户仍允许登录(以便续费)，但标记为 expired 状态并提示续费
	isExpired := user.ExpiredAt != nil && user.ExpiredAt.Before(time.Now())
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		_, locked := middleware.RecordLoginFail(ctx, ip, req.Username)
		h.recordAudit(ctx, "user", user.ID, user.Username, ip, ua, false)
		if locked {
			response.Fail(c, response.CodeAccountLocked)
			return
		}
		response.Fail(c, response.CodeAccountPwdError)
		return
	}
	// 异步更新过期状态(不影响登录流程)
	if isExpired && user.Status != "disabled" {
		go h.userRepo.UpdateStatus(user.ID, "expired")
	}
	middleware.RecordLoginSuccess(ctx, ip, req.Username)
	h.recordAudit(ctx, "user", user.ID, user.Username, ip, ua, true)

	ver := getCurrentTokenVersion(c.Request.Context(), user.ID, security.RoleUser)
	access, refresh, err := h.jwtMgr.GenerateTokenPairWithVer(user.ID, user.Username, security.RoleUser, ver)
	if err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	msg := response.Msg(response.CodeSuccess)
	if isExpired {
		msg = "账号已到期，请续费后使用"
	}
	c.JSON(http.StatusOK, response.Result{
		Code: response.CodeSuccess,
		Data: tokenResponse{
			AccessToken:  access,
			RefreshToken: refresh,
			ExpiresIn:    int64(app.Get().Cfg.JWTAccessTTL.Seconds()),
			Role:         security.RoleUser,
		},
		Msg: msg,
	})
}

// refreshRequest 刷新请求
type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// Refresh [2] POST /api/v1/auth/refresh
// 刷新令牌: 旧 refresh_token 加入 Redis 黑名单(轮换)，签发新的 token 对
//
// P0-A2 (TOCTOU 修复): 原实现"Exists 检查 → 生成新 token → Set 写黑名单"非原子,
// 并发重放可在 Exists 通过后、Set 写入前多次换取新 token。
// 改为 SetNX 原子占用: 同一 refresh_token 全局只能成功一次, 第二次直接拒绝。
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	rdb := app.Get().RDB
	if rdb == nil {
		// 无 Redis 无法做防重放, fail-closed
		response.Fail(c, response.CodeServerError)
		return
	}
	ctx := c.Request.Context()
	claims, err := h.jwtMgr.ValidateRefresh(req.RefreshToken)
	if err != nil {
		response.Fail(c, response.CodeTokenInvalid)
		return
	}
	// 修复 P0-1: 校验 token 版本号, 防止用户改密/注销后旧 refresh token 仍可换新 access token
	currentVer := getCurrentTokenVersion(ctx, claims.UserID, claims.Role)
	if claims.TokenVer != currentVer {
		response.Fail(c, response.CodeTokenInvalid)
		return
	}
	// P0-A2: 用 SetNX 原子化"检查+占用", 避免并发重放
	// TTL 取 refresh token 剩余有效期, 过期后 key 自动清理(届时 token 也已失效)
	refreshTTL := time.Until(claims.ExpiresAt.Time)
	if refreshTTL <= 0 {
		response.Fail(c, response.CodeTokenInvalid)
		return
	}
	blacklistKey := "jwtblack:" + req.RefreshToken
	ok, err := rdb.SetNX(ctx, blacklistKey, "1", refreshTTL).Result()
	if err != nil {
		log.Printf("[Auth] refresh blacklist SetNX error: %v", err)
		response.Fail(c, response.CodeServerError)
		return
	}
	if !ok {
		// 已被使用过(并发重放或重放攻击)
		response.FailMsg(c, response.CodeTokenInvalid, "refresh token 已使用")
		return
	}
	// 签发新的 token 对(轮换) - 携带当前 token 版本号, 防止注销所有设备后用户用旧 refresh 续命
	access, refresh, err := h.jwtMgr.GenerateTokenPairWithVer(claims.UserID, claims.Username, claims.Role, currentVer)
	if err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	// P0-X2: refresh 成功也记录审计(便于追踪 token 轮换行为)
	h.recordAudit(ctx, claims.Role, claims.UserID, claims.Username, c.ClientIP(), c.GetHeader("User-Agent"), true)
	response.OK(c, tokenResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int64(app.Get().Cfg.JWTAccessTTL.Seconds()),
		Role:         claims.Role,
	})
}

// logoutReq Logout 请求体
// P0-A3: 增加 refresh_token 字段, Logout 时同时吊销 refresh token (避免登出后仍可换新 access token)
type logoutReq struct {
	RefreshToken string `json:"refresh_token"`
}

// Logout [3] POST /api/v1/auth/logout
// 将当前 access token 加入 Redis 黑名单(剩余有效期内)
// P0-A1: 复用 middleware.ExtractToken 提取 token, 保证与中间件入黑名单时使用的 key 一致
//        (原实现手动切片 authHeader[7:] 在大小写/多空格场景下与中间件不一致, 导致黑名单 key 不匹配, 等同未吊销)
// P0-A3: 同时把请求体中携带的 refresh_token 加入黑名单, 防止登出后旧 refresh token 继续换新 access token
func (h *AuthHandler) Logout(c *gin.Context) {
	// 解析请求体(允许为空, 兼容旧前端不传 refresh_token 的场景)
	var req logoutReq
	_ = c.ShouldBindJSON(&req)
	rdb := app.Get().RDB
	claims := middleware.GetClaims(c)
	if claims == nil {
		response.OKMsg(c, "已登出")
		return
	}
	// P0-A1: 复用中间件 extractToken, 保证黑名单 key 与中间件校验时完全一致
	tokenStr := middleware.ExtractToken(c)
	ctx := c.Request.Context()
	if rdb != nil && tokenStr != "" {
		ttl := time.Until(claims.ExpiresAt.Time)
		if ttl > 0 {
			if err := rdb.Set(ctx, "jwtblack:"+tokenStr, "1", ttl).Err(); err != nil {
				log.Printf("[Auth] blacklist set error: %v", err)
			}
		}
	}
	// P0-A3: 同时吊销 refresh token (TTL 用 refresh token 自己的剩余有效期, 这里取配置的 refreshTTL 上限)
	if rdb != nil && req.RefreshToken != "" {
		refreshTTL := app.Get().Cfg.JWTRefreshTTL
		if err := rdb.Set(ctx, "jwtblack:"+req.RefreshToken, "1", refreshTTL).Err(); err != nil {
			log.Printf("[Auth] refresh blacklist set error: %v", err)
		}
	}
	response.OKMsg(c, "已登出")
}

// LoginLogs [9] GET /api/v1/user/login-logs
// 当前用户的登录记录
func (h *AuthHandler) LoginLogs(c *gin.Context) {
	uid := middleware.GetUserID(c)
	page, size := parsePage(c)
	list, total, err := h.loginAuditRepo.ListByTarget("user", uid, page, size)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, gin.H{"list": list, "total": total})
}

// recordAudit 记录登录审计
// P1-AUTH-04: 失败登录时也记录尝试的用户名, 便于追踪撞库/爆破来源。
// username 为调用方传入的尝试用户名(成功登录时通常等于真实用户名; 失败时是客户端输入的字符串)。
//
// 注: TargetID 列在 DB 中为 UUID 类型(见 001_init.sql), 无法直接存非 UUID 的用户名。
// 因此当 targetID 为空时(用户不存在), 将 username 记入 Location 字段(原本用于地理位置, 当前未启用),
// 使审计记录可被 ListAll 的 keyword 搜索命中(IP ILIKE ? OR location ILIKE ?)。
func (h *AuthHandler) recordAudit(ctx context.Context, targetType, targetID, username, ip, ua string, success bool) {
	var tid *string
	location := ""
	if targetID != "" {
		tid = &targetID
	} else if username != "" {
		// 用户不存在时, 把尝试的用户名记入 Location (TargetID 列为 UUID, 不能存任意字符串)
		location = username
	}
	_ = h.loginAuditRepo.Create(&model.LoginAudit{
		TargetType: targetType,
		TargetID:   tid,
		IP:         ip,
		UserAgent:  ua,
		Location:   location,
		Success:    success,
	})
}

// changePasswordRequest 修改密码请求
type changePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8,max=64"` // P1-AUTH-03: 最低 8 位 (原 min=6 偏弱)
}

// ChangePassword [POST] /api/v1/auth/change-password
// 鉴权中间件: AnyAuth
// 逻辑:
//  1. 从 JWT claims 取出当前主体(role+id)
//  2. 角色为 admin: 走 AdminRepo; 角色为 user: 走 UserRepo
//  3. 校验旧密码(同时防止 NoSuchUser 错误返回错误码)
//  4. 用 bcrypt 哈希新密码, 保存
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	// P1-AUTH: 改密时校验密码强度(必须包含字母+数字), 与注册逻辑对齐
	hasLetter, hasDigit := false, false
	for _, ch := range req.NewPassword {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
			hasLetter = true
		} else if ch >= '0' && ch <= '9' {
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		response.FailMsg(c, response.CodeParamError, "密码必须包含字母和数字")
		return
	}
	claims := middleware.GetClaims(c)
	if claims == nil {
		response.Fail(c, response.CodeTokenInvalid)
		return
	}
	ctx := c.Request.Context()

	if claims.Role == security.RoleAdmin || claims.Role == middleware.RoleSuperAdmin {
		admin, err := h.adminRepo.GetByID(claims.UserID)
		if err != nil {
			response.Fail(c, response.CodeAccountPwdError)
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.OldPassword)); err != nil {
			response.Fail(c, response.CodeAccountPwdError)
			return
		}
		newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcryptCost)
		if err != nil {
			response.Fail(c, response.CodeServerError)
			return
		}
		admin.PasswordHash = string(newHash)
		if err := h.adminRepo.Update(admin); err != nil {
			response.Fail(c, response.CodeDBError)
			return
		}
		// 强制下线所有设备(写入 user_version 标记, 旧 token 失效)
		_ = bumpTokenVersion(ctx, claims.UserID, claims.Role)
		response.OKMsg(c, "密码已修改")
		return
	}

	// user
	user, err := h.userRepo.GetByID(claims.UserID)
	if err != nil {
		response.Fail(c, response.CodeAccountPwdError)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.OldPassword)); err != nil {
		response.Fail(c, response.CodeAccountPwdError)
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcryptCost)
	if err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	user.PasswordHash = string(newHash)
	if err := h.userRepo.Update(user); err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	_ = bumpTokenVersion(ctx, claims.UserID, claims.Role)
	response.OKMsg(c, "密码已修改")
}

// LogoutAll [POST] /api/v1/auth/logout-all
// 鉴权: AnyAuth
// 逻辑:
//   - 提升 token version(存 Redis: tokver:<role>:<id>), 旧 token 在下次请求中将被拒绝
//   - 提示: 前端需跳到登录页
func (h *AuthHandler) LogoutAll(c *gin.Context) {
	claims := middleware.GetClaims(c)
	if claims == nil {
		response.Fail(c, response.CodeTokenInvalid)
		return
	}
	ctx := c.Request.Context()
	if err := bumpTokenVersion(ctx, claims.UserID, claims.Role); err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	response.OKMsg(c, "已注销所有设备, 请重新登录")
}

// bumpTokenVersion 提升 token 版本号(强制旧 token 失效)
// key 格式: tokver:<role>:<id>, value 自增
func bumpTokenVersion(ctx context.Context, userID, role string) error {
	rdb := app.Get().RDB
	if rdb == nil {
		// 没有 Redis 时, 退化为将当前 access token 加入黑名单(由 Logout 行为处理)
		return nil
	}
	key := "tokver:" + role + ":" + userID
	// INCR 自动创建
	return rdb.Incr(ctx, key).Err()
}

// getCurrentTokenVersion 获取当前 token 版本号(供登录/刷新时使用, 使新 token 包含正确的 ver)
// 找不到则返回 0(无版本)
func getCurrentTokenVersion(ctx context.Context, userID, role string) int64 {
	rdb := app.Get().RDB
	if rdb == nil {
		return 0
	}
	v, err := rdb.Get(ctx, "tokver:"+role+":"+userID).Int64()
	if err != nil {
		return 0
	}
	return v
}

// parsePage 解析分页参数
func parsePage(c *gin.Context) (page, size int) {
	page = atoiDefault(c.Query("page"), 1)
	size = atoiDefault(c.Query("size"), 20)
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 20
	}
	return
}

// atoiDefault 字符串转整数(带默认值)，非法则返回默认值
func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return def
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

// ============================================================
// P0-2 修复: 邮箱验证码 + 修改邮箱 (ChangeEmail.vue 配套)
// ============================================================

// sendEmailCodeRequest 发送邮箱验证码请求
type sendEmailCodeRequest struct {
	Email string `json:"email" binding:"required,email"`
	Type  string `json:"type" binding:"required,oneof=register change_email"`
}

// SendEmailCode [POST] /api/v1/email/send-code
// 鉴权: AnyAuth (改邮箱场景需要登录; 注册场景由调用方决定是否带 token)
// 逻辑:
//  1. 校验邮箱格式
//  2. change_email 场景: 检查新邮箱是否已被占用
//  3. 生成 6 位数字验证码, 存入 Redis (key: emailcode:<type>:<email>, TTL 5min)
//  4. 调用 EmailService.SendMail 发送验证码邮件
func (h *AuthHandler) SendEmailCode(c *gin.Context) {
	var req sendEmailCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}

	// change_email 场景: 检查新邮箱是否已被占用
	if req.Type == "change_email" {
		if existing, err := h.userRepo.GetByEmail(req.Email); err == nil && existing != nil {
			response.FailMsg(c, response.CodeDuplicate, "邮箱已被占用")
			return
		}
	}

	// 生成 6 位数字验证码
	code, err := generateNumericCode(6)
	if err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}

	// 存入 Redis, 5 分钟过期
	rdb := app.Get().RDB
	if rdb == nil {
		response.FailMsg(c, response.CodeServerError, "缓存服务不可用")
		return
	}
	ctx := c.Request.Context()
	key := "emailcode:" + req.Type + ":" + req.Email
	if err := rdb.Set(ctx, key, code, 5*time.Minute).Err(); err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}

	// 发送邮件
	if h.emailSvc != nil {
		subject := "Nexus Panel 验证码"
		body := fmt.Sprintf("您的验证码是: %s\n\n验证码 5 分钟内有效, 请勿告知他人。", code)
		if err := h.emailSvc.SendMail([]string{req.Email}, subject, body); err != nil {
			response.FailMsg(c, response.CodeServerError, "邮件发送失败: "+err.Error())
			return
		}
	} else {
		log.Printf("[Auth] emailSvc 未注入, 验证码 %s (开发模式, 仅日志)", code)
	}

	response.OKMsg(c, "验证码已发送")
}

// changeEmailRequest 修改邮箱请求
type changeEmailRequest struct {
	NewEmail string `json:"new_email" binding:"required,email"`
	Code     string `json:"code" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// ChangeEmail [POST] /api/v1/auth/change-email
// 鉴权: AnyAuth
// 逻辑:
//  1. 从 JWT claims 取出当前主体(role+id)
//  2. 校验当前密码(防止会话被劫持后改邮箱)
//  3. 从 Redis 取验证码并校验, 校验通过后立即删除(一次性)
//  4. 检查新邮箱是否已被占用
//  5. 更新邮箱
//  6. bumpTokenVersion 强制其他设备重新登录
func (h *AuthHandler) ChangeEmail(c *gin.Context) {
	var req changeEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	claims := middleware.GetClaims(c)
	if claims == nil {
		response.Fail(c, response.CodeTokenInvalid)
		return
	}
	ctx := c.Request.Context()

	// 仅支持 user 角色改邮箱(admin 邮箱由系统管理)
	if claims.Role != security.RoleUser {
		response.FailMsg(c, response.CodeParamError, "仅用户可修改邮箱")
		return
	}

	// 1. 校验当前密码
	user, err := h.userRepo.GetByID(claims.UserID)
	if err != nil {
		response.Fail(c, response.CodeAccountPwdError)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		response.Fail(c, response.CodeAccountPwdError)
		return
	}

	// 2. 校验验证码(一次性, 校验后立即删除)
	// P1-AUTH-07: 用 Lua 脚本原子化 "读取+删除+返回", 避免 GET/DEL 之间窗口被并发重放
	rdb := app.Get().RDB
	if rdb == nil {
		response.FailMsg(c, response.CodeServerError, "缓存服务不可用")
		return
	}
	codeKey := "emailcode:change_email:" + req.NewEmail
	// Lua 脚本: 原子地 GET 后 DEL, 返回原值(nil 表示已过期/不存在)
	verifyScript := `local v = redis.call('GET', KEYS[1]); if v then redis.call('DEL', KEYS[1]) end; return v`
	res, err := rdb.Eval(ctx, verifyScript, []string{codeKey}).Result()
	if err != nil {
		log.Printf("[Auth] change_email verify code eval error: %v", err)
		response.Fail(c, response.CodeServerError)
		return
	}
	storedCode, _ := res.(string)
	if storedCode == "" {
		response.FailMsg(c, response.CodeParamError, "验证码已过期, 请重新获取")
		return
	}
	if storedCode != req.Code {
		response.FailMsg(c, response.CodeParamError, "验证码错误")
		return
	}

	// 3. 检查新邮箱是否已被占用
	if existing, err := h.userRepo.GetByEmail(req.NewEmail); err == nil && existing != nil {
		response.FailMsg(c, response.CodeDuplicate, "邮箱已被占用")
		return
	}

	// 4. 更新邮箱
	user.Email = req.NewEmail
	if err := h.userRepo.Update(user); err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}

	// 5. 强制其他设备重新登录(改邮箱应触发重新认证)
	_ = bumpTokenVersion(ctx, claims.UserID, claims.Role)

	response.OKMsg(c, "邮箱已修改")
}

// generateNumericCode 生成指定长度的数字验证码(密码学安全)
func generateNumericCode(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("length must be positive")
	}
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(length)), nil)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	// 前导零补齐
	return fmt.Sprintf("%0*d", length, n), nil
}
