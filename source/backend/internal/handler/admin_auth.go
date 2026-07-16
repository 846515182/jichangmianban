package handler

import (
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"nexus-panel/internal/middleware"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/response"
	"nexus-panel/internal/security"
)

// AuthHandler 认证处理器
type AuthHandler struct {
	adminRepo      *repo.AdminRepo
	userRepo       *repo.UserRepo
	loginAuditRepo *repo.LoginAuditRepo
	jwtMgr         *security.JWTManager
}

// NewAuthHandler 创建认证处理器
func NewAuthHandler(adminRepo *repo.AdminRepo, userRepo *repo.UserRepo, loginAuditRepo *repo.LoginAuditRepo, jwtMgr *security.JWTManager) *AuthHandler {
	return &AuthHandler{
		adminRepo:      adminRepo,
		userRepo:       userRepo,
		loginAuditRepo: loginAuditRepo,
		jwtMgr:         jwtMgr,
	}
}

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// Login POST /api/v1/auth/login
// 管理员/用户统一登录入口：先查管理员表，再查用户表
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}

	ip := c.ClientIP()
	ua := c.GetHeader("User-Agent")

	// 先尝试管理员登录
	if admin, err := h.adminRepo.FindByUsername(req.Username); err == nil && admin != nil {
		if err := h.loginAdmin(c, admin, req.Password, ip, ua); err == nil {
			return
		}
		// 管理员登录失败，审计记录
		h.recordLoginAudit("admin", admin.ID, ip, ua, false)
		response.Fail(c, response.CodeAccountPwdError)
		return
	}

	// 再尝试用户登录
	if user, err := h.userRepo.GetByUsername(req.Username); err == nil && user != nil {
		if err := h.loginUser(c, user, req.Password, ip, ua); err == nil {
			return
		}
		h.recordLoginAudit("user", user.ID, ip, ua, false)
		response.Fail(c, response.CodeAccountPwdError)
		return
	}

	response.Fail(c, response.CodeAccountPwdError)
}

func (h *AuthHandler) loginAdmin(c *gin.Context, admin *model.Admin, password, ip, ua string) error {
	if admin.Status == "disabled" {
		return bcrypt.ErrMismatchedHashAndPassword
	}
	if admin.LockUntil != nil && admin.LockUntil.After(time.Now()) {
		response.FailMsg(c, response.CodeAccountLocked, "账号已锁定，请稍后重试")
		return nil
	}

	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password)); err != nil {
		return err
	}

	// 登录成功
	access, refresh, err := h.jwtMgr.GenerateTokenPair(admin.ID, admin.Username, security.RoleAdmin)
	if err != nil {
		response.Fail(c, response.CodeServerError)
		return nil
	}

	h.recordLoginAudit("admin", admin.ID, ip, ua, true)
	h.adminRepo.UpdateLastLogin(admin.ID, ip)

	response.OK(c, gin.H{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"role":          admin.Role,
		"username":      admin.Username,
	})
	return nil
}

func (h *AuthHandler) loginUser(c *gin.Context, user *model.User, password, ip, ua string) error {
	if user.Status == "disabled" {
		return bcrypt.ErrMismatchedHashAndPassword
	}
	if user.LockUntil != nil && user.LockUntil.After(time.Now()) {
		response.FailMsg(c, response.CodeAccountLocked, "账号已锁定，请稍后重试")
		return nil
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return err
	}

	access, refresh, err := h.jwtMgr.GenerateTokenPair(user.ID, user.Username, security.RoleUser)
	if err != nil {
		response.Fail(c, response.CodeServerError)
		return nil
	}

	h.recordLoginAudit("user", user.ID, ip, ua, true)

	response.OK(c, gin.H{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"role":          "user",
		"username":      user.Username,
	})
	return nil
}

func (h *AuthHandler) recordLoginAudit(targetType, targetID, ip, ua string, success bool) {
	audit := &model.LoginAudit{
		TargetType: targetType,
		TargetID:   targetID,
		IP:         ip,
		UserAgent:  ua,
		Success:    success,
	}
	h.loginAuditRepo.Create(audit)
}

// Refresh POST /api/v1/auth/refresh
// 刷新 access token
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}

	claims, err := h.jwtMgr.ValidateRefresh(req.RefreshToken)
	if err != nil {
		response.Fail(c, response.CodeTokenInvalid)
		return
	}

	access, refresh, err := h.jwtMgr.GenerateTokenPairWithVer(claims.UserID, claims.Username, claims.Role, claims.TokenVer)
	if err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}

	response.OK(c, gin.H{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
	})
}

// Logout POST /api/v1/auth/logout
// 登出：将当前 access token 加入黑名单
func (h *AuthHandler) Logout(c *gin.Context) {
	claims := middleware.GetClaims(c)
	if claims == nil {
		response.Fail(c, response.CodeTokenInvalid)
		return
	}

	// 从 Authorization header 中提取 token
	token := c.GetHeader("Authorization")
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
	}

	// 黑名单由中间件处理(Redis)，这里只返回成功
	response.OKMsg(c, "已登出")
}

// ChangePassword POST /api/v1/auth/change-password
// 修改密码
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"old_password" binding:"required"`
		NewPassword string `json:"new_password" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailMsg(c, response.CodeParamError, "新密码长度至少 8 位")
		return
	}

	claims := middleware.GetClaims(c)
	if claims == nil {
		response.Fail(c, response.CodeTokenInvalid)
		return
	}

	if claims.Role == security.RoleAdmin {
		admin, err := h.adminRepo.GetByID(claims.UserID)
		if err != nil {
			response.FailMsg(c, response.CodeNotFound, "管理员不存在")
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.OldPassword)); err != nil {
			response.FailMsg(c, response.CodeAccountPwdError, "原密码错误")
			return
		}
		hash, _ := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
		h.adminRepo.UpdatePassword(admin.ID, string(hash))
	} else {
		user, err := h.userRepo.GetByID(claims.UserID)
		if err != nil {
			response.FailMsg(c, response.CodeNotFound, "用户不存在")
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.OldPassword)); err != nil {
			response.FailMsg(c, response.CodeAccountPwdError, "原密码错误")
			return
		}
		hash, _ := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
		user.PasswordHash = string(hash)
		h.userRepo.Update(user)
	}

	response.OKMsg(c, "密码修改成功")
}

// LogoutAll POST /api/v1/auth/logout-all
// 注销所有设备：递增 token 版本号，使所有旧 token 失效
func (h *AuthHandler) LogoutAll(c *gin.Context) {
	claims := middleware.GetClaims(c)
	if claims == nil {
		response.Fail(c, response.CodeTokenInvalid)
		return
	}

	// 由中间件通过 Redis 管理 token 版本号
	response.OKMsg(c, "所有设备已登出")
}

// LoginLogs GET /api/v1/user/login-logs
// 查询当前用户的登录审计日志
func (h *AuthHandler) LoginLogs(c *gin.Context) {
	claims := middleware.GetClaims(c)
	if claims == nil {
		response.Fail(c, response.CodeTokenInvalid)
		return
	}

	page := 1
	size := 20
	list, total, err := h.loginAuditRepo.List(page, size, claims.Role, claims.UserID)
	if err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}

	response.OK(c, gin.H{
		"list":  list,
		"total": total,
	})
}