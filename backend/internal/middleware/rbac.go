package middleware

import (
	"github.com/gin-gonic/gin"

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

// RBAC 权限校验中间件
// super_admin 拥有全部权限；普通 admin 无密钥/备份/全局安全权限
//
// P1-RBAC: 对 super_admin 敏感操作(密钥管理/备份/全局安全/节点部署/资金管理),
// 当前仅依据 JWT claims 中的 role 字段放行。若 JWT 被盗且攻击者将 role 改为 super_admin,
// 仅靠签名校验仍可拦截; 但若密钥泄露, 攻击者可伪造 super_admin token 直接放行。
//
// 加固建议(待实现):
//   - 对 super_admin 敏感操作, 在中间件层加 DB 二次校验(带 30s 内存/Redis 缓存),
//     确认该 admin 在 DB 中仍持有 super_admin 角色(防止 token 颁发后被降权)。
//   - 缓存 key: rbac:role:<admin_id>, TTL 30s; miss 时查 admin_repo.GetByID。
//   - TODO: 实现带缓存的 DB 二次校验 (需注入 adminRepo, 当前中间件无 DB 依赖, 待后续重构)
func RBAC(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := GetRole(c)
		// super_admin 直接放行
		// TODO(P1-RBAC): 对敏感权限(key_manage/backup/global_sec/node_manage/fund_manage)
		// 增加 DB 二次校验(带 30s 缓存), 确认 super_admin 角色未被降级
		if role == RoleSuperAdmin {
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
