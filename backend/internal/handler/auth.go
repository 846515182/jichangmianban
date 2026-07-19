package handler

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"nexus-panel/internal/app"
	"nexus-panel/internal/middleware"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/response"
	"nexus-panel/internal/security"
)

// AuthHandler 认证处理器
type AuthHandler struct {
	adminRepo     *repo.AdminRepo
	userRepo      *repo.UserRepo
	loginAuditRepo *repo.LoginAuditRepo
	jwtMgr        *security.JWTManager
}

// NewAuthHandler 创建认证处理器
func NewAuthHandler(a *repo.AdminRepo, u *repo.UserRepo, la *repo.LoginAuditRepo, jwtMgr *security.JWTManager) *AuthHandler {
	return &AuthHandler{adminRepo: a, userRepo: u, loginAuditRepo: la, jwtMgr: jwtMgr}
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

// adminLogin 管理员登录
func (h *AuthHandler) adminLogin(c *gin.Context, ctx context.Context, req *loginRequest, ip, ua string) {
	admin, err := h.adminRepo.GetByUsername(req.Username)
	if err != nil {
		// 记录失败
		middleware.RecordLoginFail(ctx, ip, req.Username)
		h.recordAudit(ctx, "admin", "", ip, ua, false)
		response.Fail(c, response.CodeAccountPwdError)
		return
	}
	// 检查账号锁定
	if locked, _ := middleware.CheckAccountLocked(ctx, req.Username); locked {
		response.Fail(c, response.CodeAccountLocked)
		return
	}
	if admin.Status == "disabled" {
		response.Fail(c, response.CodeAccountDisabled)
		return
	}
	// 校验密码
	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)); err != nil {
		_, locked := middleware.RecordLoginFail(ctx, ip, req.Username)
		h.recordAudit(ctx, "admin", admin.ID, ip, ua, false)
		if locked {
			response.Fail(c, response.CodeAccountLocked)
			return
		}
		response.Fail(c, response.CodeAccountPwdError)
		return
	}
	// 登录成功
	middleware.RecordLoginSuccess(ctx, ip, req.Username)
	now := time.Now()
	_ = h.adminRepo.UpdateLastLogin(admin.ID, ip, now)
	h.recordAudit(ctx, "admin", admin.ID, ip, ua, true)

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
	user, err := h.userRepo.GetByUsername(req.Username)
	if err != nil {
		middleware.RecordLoginFail(ctx, ip, req.Username)
		h.recordAudit(ctx, "user", "", ip, ua, false)
		response.Fail(c, response.CodeAccountPwdError)
		return
	}
	if locked, _ := middleware.CheckAccountLocked(ctx, req.Username); locked {
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
		h.recordAudit(ctx, "user", user.ID, ip, ua, false)
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
	h.recordAudit(ctx, "user", user.ID, ip, ua, true)

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
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	// 检查 refresh_token 是否已在黑名单(已被轮换过)
	rdb := app.Get().RDB
	if rdb != nil {
		if exists, err := rdb.Exists(c.Request.Context(), "jwtblack:"+req.RefreshToken).Result(); err != nil {
			log.Printf("[Auth] refresh blacklist check error: %v", err)
		} else if exists > 0 {
			response.Fail(c, response.CodeTokenInvalid)
			return
		}
	}
	claims, err := h.jwtMgr.ValidateRefresh(req.RefreshToken)
	if err != nil {
		response.Fail(c, response.CodeTokenInvalid)
		return
	}
	// 签发新的 token 对(轮换) - 携带当前 token 版本号, 防止注销所有设备后用户用旧 refresh 续命
	ver := getCurrentTokenVersion(c.Request.Context(), claims.UserID, claims.Role)
	access, refresh, err := h.jwtMgr.GenerateTokenPairWithVer(claims.UserID, claims.Username, claims.Role, ver)
	if err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	// 将旧 refresh_token 加入黑名单，防止重放攻击
	if rdb != nil {
		ttl := time.Until(claims.ExpiresAt.Time)
		if ttl > 0 {
			if err := rdb.Set(c.Request.Context(), "jwtblack:"+req.RefreshToken, "1", ttl).Err(); err != nil {
				log.Printf("[Auth] refresh blacklist set error: %v", err)
			}
		}
	}
	response.OK(c, tokenResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int64(app.Get().Cfg.JWTAccessTTL.Seconds()),
		Role:         claims.Role,
	})
}

// Logout [3] POST /api/v1/auth/logout
// 将当前 access token 加入 Redis 黑名单(剩余有效期内)
func (h *AuthHandler) Logout(c *gin.Context) {
	rdb := app.Get().RDB
	claims := middleware.GetClaims(c)
	if claims == nil {
		response.OKMsg(c, "已登出")
		return
	}
	// 从请求头提取纯 token 写入黑名单（与 extractToken 逻辑一致）
	authHeader := c.GetHeader("Authorization")
	tokenStr := authHeader
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		tokenStr = authHeader[7:]
	}
	if rdb != nil && tokenStr != "" {
		ttl := time.Until(claims.ExpiresAt.Time)
		if ttl > 0 {
			if err := rdb.Set(c.Request.Context(), "jwtblack:"+tokenStr, "1", ttl).Err(); err != nil {
				log.Printf("[Auth] blacklist set error: %v", err)
			}
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
func (h *AuthHandler) recordAudit(ctx context.Context, targetType, targetID, ip, ua string, success bool) {
	var tid *string
	if targetID != "" {
		tid = &targetID
	}
	_ = h.loginAuditRepo.Create(&model.LoginAudit{
		TargetType: targetType,
		TargetID:   tid,
		IP:         ip,
		UserAgent:  ua,
		Location:   "",
		Success:    success,
	})
}

// changePasswordRequest 修改密码请求
type changePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6,max=64"`
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
		newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
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
	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
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
//  - 提升 token version(存 Redis: tokver:<role>:<id>), 旧 token 在下次请求中将被拒绝
//  - 提示: 前端需跳到登录页
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
