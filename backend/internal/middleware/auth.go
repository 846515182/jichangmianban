package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"nexus-panel/internal/app"
	"nexus-panel/internal/response"
	"nexus-panel/internal/security"
)

// 上下文键
type ctxKey string

const (
	// CtxClaims JWT claims 键
	CtxClaims ctxKey = "claims"
	// CtxUserID 当前主体 ID
	CtxUserID ctxKey = "user_id"
	// CtxRole 当前主体角色
	CtxRole ctxKey = "role"
	// CtxUsername 当前主体用户名
	CtxUsername ctxKey = "username"
	// CtxClientIP 客户端 IP(已被中间件规范化)
	CtxClientIP ctxKey = "client_ip"
)

// JWTAuth JWT 认证中间件，校验 access token
// allowedRoles: 允许的角色，为空表示允许所有已认证主体
func JWTAuth(allowedRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			response.Fail(c, response.CodeTokenInvalid)
			c.Abort()
			return
		}
		mgr := security.NewJWTManager(app.Get().Cfg.JWTSecret, app.Get().Cfg.JWTAccessTTL, app.Get().Cfg.JWTRefreshTTL)
		claims, err := mgr.ValidateAccess(token)
		if err != nil {
			response.Fail(c, response.CodeTokenInvalid)
			c.Abort()
			return
		}
		// P0-Redis: Redis 不可用时, 对敏感管理接口采取 fail-closed
		// 旧版用 `RDB == nil` 判断, 但 initRedis 恒返回非 nil client, 导致此守卫是死代码。
		// 现改用 IsRedisAvailable()(后台 Ping 健康检查), Redis 真挂时管理员接口返回 503,
		// 避免被吊销的 token / 黑名单 token 在 Redis 故障窗口内复活。
		if !app.Get().IsRedisAvailable() && strings.HasPrefix(c.Request.URL.Path, "/api/v1/admin/") {
			response.FailWithHTTP(c, http.StatusServiceUnavailable, response.CodeTokenInvalid)
			c.Abort()
			return
		}
		// 登出黑名单校验（使用纯 token，与 extractToken 返回一致）
		blacklistKey := "jwtblack:" + token
		if rdb := app.Get().RDB; app.Get().IsRedisAvailable() {
			exists, err := rdb.Exists(c.Request.Context(), blacklistKey).Result()
			if err != nil {
				// Redis 操作出错: 对 admin 接口 fail-closed, 对 user 接口 fail-open
				// (admin 安全优先, user 可用性优先)
				if strings.HasPrefix(c.Request.URL.Path, "/api/v1/admin/") {
					response.FailWithHTTP(c, http.StatusServiceUnavailable, response.CodeTokenInvalid)
					c.Abort()
					return
				}
				c.Set("jwt_blacklist_check_error", err.Error())
			} else if exists > 0 {
				response.Fail(c, response.CodeTokenInvalid)
				c.Abort()
				return
			}
		}
		// 角色校验
		if len(allowedRoles) > 0 && !contains(allowedRoles, claims.Role) {
			response.Fail(c, response.CodeNoPermission)
			c.Abort()
			return
		}
		// P1-MW-AUTH: token 版本校验(注销所有设备时旧 token 全部失效)
		// - claims.TokenVer > 0: 该用户曾执行过 LogoutAll/ChangePassword, 必须依赖 Redis 实时校验当前版本
		//   Redis 不可用时 fail-closed 返回 503, 避免被吊销的 token 在 Redis 故障窗口内复活
		// - claims.TokenVer == 0: 旧 token(从未 bump 过版本), Redis 不可用时放行(向后兼容, 不阻断登录)
		if claims.TokenVer > 0 {
			// P0-Redis: rdb 恒非 nil(initRedis 自动重连), 用 IsRedisAvailable() 判断实际可达性
			// Redis 不可用时 fail-closed 返回 503, 避免被吊销的 token 在故障窗口内复活
			rdb := app.Get().RDB
			if !app.Get().IsRedisAvailable() {
				response.FailWithHTTP(c, http.StatusServiceUnavailable, response.CodeServerError)
				c.Abort()
				return
			}
			key := "tokver:" + claims.Role + ":" + claims.UserID
			cur, err := rdb.Get(c.Request.Context(), key).Int64()
			if err == nil && cur > claims.TokenVer {
				response.Fail(c, response.CodeTokenInvalid)
				c.Abort()
				return
			}
		}
		// 写入上下文
		c.Set(string(CtxClaims), claims)
		c.Set(string(CtxUserID), claims.UserID)
		c.Set(string(CtxRole), claims.Role)
		c.Set(string(CtxUsername), claims.Username)
		c.Set(string(CtxClientIP), c.ClientIP())
		c.Next()
	}
}

// AdminAuth 管理员认证
func AdminAuth() gin.HandlerFunc {
	return JWTAuth(security.RoleAdmin, RoleSuperAdmin)
}

// UserAuth 用户认证
func UserAuth() gin.HandlerFunc {
	return JWTAuth(security.RoleUser)
}

// AnyAuth 任意已认证主体
func AnyAuth() gin.HandlerFunc {
	return JWTAuth()
}

// ExtractToken 从请求头提取 Bearer token (P0-A1: 导出供 handler 包复用, 保证登出黑名单 key 与中间件一致)
func ExtractToken(c *gin.Context) string {
	return extractToken(c)
}

// extractToken 从请求头提取 Bearer token
func extractToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if auth == "" {
		return ""
	}
	// 支持 "Bearer xxx"
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return strings.TrimSpace(auth)
}

// GetClaims 从上下文获取 claims
func GetClaims(c *gin.Context) *security.Claims {
	v, ok := c.Get(string(CtxClaims))
	if !ok {
		return nil
	}
	if claims, ok := v.(*security.Claims); ok {
		return claims
	}
	return nil
}

// GetUserID 从上下文获取用户 ID
func GetUserID(c *gin.Context) string {
	if v, ok := c.Get(string(CtxUserID)); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// GetRole 从上下文获取角色
func GetRole(c *gin.Context) string {
	if v, ok := c.Get(string(CtxRole)); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func contains(s []string, v string) bool {
	for _, item := range s {
		if item == v {
			return true
		}
	}
	return false
}
