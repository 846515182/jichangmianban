package middleware

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/response"
)

// 角色常量
const (
	RoleSuperAdmin = "super_admin"
	RoleAdmin      = "admin"
)

// 敏感权限分类
const (
	PermKeyManage    = "key_manage"    // 密钥管理(轮换 AES/HMAC、查看 REALITY 私钥)
	PermBackup       = "backup"        // 备份
	PermGlobalSec    = "global_sec"    // 全局安全配置
	PermNodeManage   = "node_manage"   // 节点管理(创建/编辑/删除节点、一键部署)
	PermFundManage   = "fund_manage"   // 资金管理(退款/标记支付, 影响用户访问权与资金)
)

// 需要在所有 admin 中受限的敏感权限
// 包含: 密钥管理 / 备份 / 全局安全 / 节点一键部署
// 普通 admin 访问这些接口会返回 403; super_admin 放行
var sensitivePerms = map[string]bool{
	PermKeyManage:  true,
	PermBackup:     true,
	PermGlobalSec:  true,
	PermNodeManage: true, // 一键部署涉及远程 root 权限与 SSH 密码传输, 仅 super_admin 可执行
	PermFundManage: true, // 退款/标记支付影响资金与访问权, 仅 super_admin 可执行
}

// rbacRoleCacheTTL DB 角色缓存时间
const rbacRoleCacheTTL = 30 * time.Second

// rbacRoleCacheKey 返回管理员角色缓存的 Redis key
func rbacRoleCacheKey(adminID string) string {
	return fmt.Sprintf("rbac:role:%s", adminID)
}

// InvalidateRBACRoleCache 清除管理员角色缓存。
// 在管理员角色/状态/删除变更后调用, 确保旧 token 的 super_admin 权限在缓存 TTL 内失效。
func InvalidateRBACRoleCache(ctx context.Context, adminID string) {
	if adminID == "" {
		return
	}
	container := app.Get()
	if container == nil || container.RDB == nil {
		return
	}
	_ = container.RDB.Del(ctx, rbacRoleCacheKey(adminID)).Err()
}

// rbacSkipDBCheck 仅用于单元测试: 跳过 DB 二次校验, 直接信任 JWT claims 中的 super_admin。
// 测试不会初始化真实数据库, 若不跳过则所有 super_admin 敏感权限测试都会被拦截。
var rbacSkipDBCheck bool

// validateSuperAdminRole 对 super_admin 做 DB/Redis 二次校验,
// 防止 JWT 颁发后管理员被降权仍持有 super_admin 权限。
// 兜底: Redis 不可用时直接查 DB; DB 不可用时基于 JWT claims fail-closed(拒绝敏感操作)。
func validateSuperAdminRole(ctx context.Context, adminID string) bool {
	if adminID == "" {
		return false
	}
	if rbacSkipDBCheck {
		return true
	}
	container := app.Get()
	if container == nil || container.DB == nil {
		return false
	}
	// 1. 优先读 Redis 缓存
	if rdb := app.Get().RDB; rdb != nil && app.Get().IsRedisAvailable() {
		key := fmt.Sprintf("rbac:role:%s", adminID)
		cached, err := rdb.Get(ctx, key).Result()
		if err == nil && cached == RoleSuperAdmin {
			return true
		}
		if err == nil && cached != "" {
			// 缓存命中但非 super_admin
			return false
		}
	}
	// 2. 缓存未命中或 Redis 不可用, 查 DB
	var admin model.Admin
	if err := container.DB.Select("id, role, is_deleted").Where("id = ? AND is_deleted = false", adminID).First(&admin).Error; err != nil {
		return false
	}
	isSuper := admin.Role == RoleSuperAdmin
	// 3. 写回 Redis 缓存(即使为 false 也缓存, 避免降权后反复查 DB)
	if rdb := app.Get().RDB; rdb != nil && app.Get().IsRedisAvailable() {
		key := fmt.Sprintf("rbac:role:%s", adminID)
		value := admin.Role
		if value == "" {
			value = "_none_"
		}
		_ = rdb.Set(ctx, key, value, rbacRoleCacheTTL).Err()
	}
	return isSuper
}

// RBAC 权限校验中间件
// super_admin 拥有全部权限；普通 admin 无密钥/备份/全局安全权限
//
// P0-RBAC: 对 super_admin 敏感操作(密钥管理/备份/全局安全/节点部署/资金管理),
// 增加 DB 二次校验(带 30s Redis 缓存), 确认该 admin 在 DB 中仍持有 super_admin 角色,
// 防止 token 颁发后被降权仍可访问敏感接口。
func RBAC(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := GetRole(c)
		// super_admin 对敏感权限做 DB 二次校验
		if role == RoleSuperAdmin {
			if sensitivePerms[perm] {
				adminID := GetUserID(c)
				if !validateSuperAdminRole(c.Request.Context(), adminID) {
					response.Fail(c, response.CodeNoPermission)
					c.Abort()
					return
				}
			}
			c.Next()
			return
		}
		// 普通管理员：校验敏感权限
		if role == RoleAdmin {
			if sensitivePerms[perm] {
				response.Fail(c, response.CodeNoPermission)
				c.Abort()
				return
			}
			c.Next()
			return
		}
		// 其它角色无管理员权限
		response.Fail(c, response.CodeNoPermission)
		c.Abort()
	}
}

// RequireSuperAdmin 仅超级管理员可访问
func RequireSuperAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if GetRole(c) == RoleSuperAdmin {
			c.Next()
			return
		}
		response.Fail(c, response.CodeNoPermission)
		c.Abort()
	}
}

// IsSuperAdmin 判断当前主体是否为超级管理员
func IsSuperAdmin(c *gin.Context) bool {
	return GetRole(c) == RoleSuperAdmin
}
