package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"nexus-panel/internal/response"
)

func init() {
	// 单元测试不初始化数据库, 跳过 RBAC 对 super_admin 的 DB 二次校验
	rbacSkipDBCheck = true
}

// performRequestAsRole 以指定角色发起请求
func performRequestAsRole(role string, method, path string) *httptest.ResponseRecorder {
	r := setupRouterWithRole(role)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	r.ServeHTTP(w, req)
	return w
}

// setupRouterWithRole 构造带角色注入的路由(模拟 JWT 中间件设置 role/user_id 后进入 RBAC)
func setupRouterWithRole(role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 模拟 JWT 中间件: 将 role/user_id 注入 context
	r.Use(func(c *gin.Context) {
		if role != "" {
			c.Set(string(CtxRole), role)
			// 为 super_admin 的 DB 二次校验提供 adminID(测试跳过该校验, 但仍需非空)
			c.Set(string(CtxUserID), "test-admin-id")
		}
		c.Next()
	})
	// 与 routes.go 完全一致的路由注册
	r.POST("/admin/users/:id/status", RBAC(PermFundManage), func(c *gin.Context) {
		c.JSON(http.StatusOK, response.Result{Code: response.CodeSuccess, Msg: "toggle_ok"})
	})
	r.DELETE("/admin/users/:id/hard", RBAC(PermFundManage), func(c *gin.Context) {
		c.JSON(http.StatusOK, response.Result{Code: response.CodeSuccess, Msg: "hard_delete_ok"})
	})
	r.POST("/admin/users/:id/activate-plan", RBAC(PermFundManage), func(c *gin.Context) {
		c.JSON(http.StatusOK, response.Result{Code: response.CodeSuccess, Msg: "activate_ok"})
	})
	r.POST("/admin/users/:id/reset-traffic", RBAC(PermFundManage), func(c *gin.Context) {
		c.JSON(http.StatusOK, response.Result{Code: response.CodeSuccess, Msg: "reset_ok"})
	})
	return r
}

// parseBody 解析响应体为 response.Result
func parseBody(t *testing.T, w *httptest.ResponseRecorder) response.Result {
	t.Helper()
	var res response.Result
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("解析响应体失败: %v, body=%s", err, w.Body.String())
	}
	return res
}

// ============================================================
// 测试用例: 验证 P0-3 修复 - /admin/users 写操作 RBAC 权限
// ============================================================

// TestSuperAdminCanAccessAllUserWrites super_admin 应能访问所有 /admin/users 写操作
func TestSuperAdminCanAccessAllUserWrites(t *testing.T) {
	cases := []struct {
		method, path, desc string
	}{
		{"POST", "/admin/users/1/status", "UserToggleStatus"},
		{"DELETE", "/admin/users/1/hard", "UserHardDelete"},
		{"POST", "/admin/users/1/activate-plan", "UserActivatePlan"},
		{"POST", "/admin/users/1/reset-traffic", "UserResetTraffic"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			w := performRequestAsRole(RoleSuperAdmin, tc.method, tc.path)
			if w.Code != http.StatusOK {
				t.Errorf("[%s] super_admin 期望 200, 实际 %d, body=%s", tc.desc, w.Code, w.Body.String())
				return
			}
			res := parseBody(t, w)
			if res.Code != response.CodeSuccess {
				t.Errorf("[%s] super_admin 期望业务码 %d, 实际 %d (%s)",
					tc.desc, response.CodeSuccess, res.Code, res.Msg)
			}
			t.Logf("[%s] super_admin 访问成功: %s", tc.desc, res.Msg)
		})
	}
}

// TestNormalAdminBlockedFromUserWrites 普通 admin 应被拒绝访问所有 /admin/users 写操作
// 这是 P0-3 修复的核心: 普通管理员不能绕过财务权限
func TestNormalAdminBlockedFromUserWrites(t *testing.T) {
	cases := []struct {
		method, path, desc string
	}{
		{"POST", "/admin/users/1/status", "UserToggleStatus"},
		{"DELETE", "/admin/users/1/hard", "UserHardDelete"},
		{"POST", "/admin/users/1/activate-plan", "UserActivatePlan"},
		{"POST", "/admin/users/1/reset-traffic", "UserResetTraffic"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			w := performRequestAsRole(RoleAdmin, tc.method, tc.path)
			// HTTP 状态码应为 403
			if w.Code != http.StatusForbidden {
				t.Errorf("[%s] admin 期望 HTTP 403, 实际 %d, body=%s",
					tc.desc, w.Code, w.Body.String())
				return
			}
			// 业务码应为 CodeNoPermission (40301)
			res := parseBody(t, w)
			if res.Code != response.CodeNoPermission {
				t.Errorf("[%s] admin 期望业务码 %d (CodeNoPermission), 实际 %d (%s)",
					tc.desc, response.CodeNoPermission, res.Code, res.Msg)
				return
			}
			t.Logf("[%s] 普通 admin 被正确拒绝: HTTP %d, code=%d, msg=%q",
				tc.desc, w.Code, res.Code, res.Msg)
		})
	}
}

// TestUserRoleBlockedFromUserWrites user 角色应被拒绝访问所有 /admin/users 写操作
func TestUserRoleBlockedFromUserWrites(t *testing.T) {
	w := performRequestAsRole("user", "POST", "/admin/users/1/status")
	if w.Code != http.StatusForbidden {
		t.Errorf("user 期望 HTTP 403, 实际 %d", w.Code)
		return
	}
	res := parseBody(t, w)
	if res.Code != response.CodeNoPermission {
		t.Errorf("user 期望业务码 %d, 实际 %d", response.CodeNoPermission, res.Code)
		return
	}
	t.Logf("user 角色被正确拒绝: %s", w.Body.String())
}

// TestNoRoleBlockedFromUserWrites 无角色(未登录/异常)应被拒绝
func TestNoRoleBlockedFromUserWrites(t *testing.T) {
	w := performRequestAsRole("", "POST", "/admin/users/1/status")
	if w.Code != http.StatusForbidden {
		t.Errorf("无角色期望 HTTP 403, 实际 %d", w.Code)
		return
	}
	res := parseBody(t, w)
	if res.Code != response.CodeNoPermission {
		t.Errorf("无角色期望业务码 %d, 实际 %d", response.CodeNoPermission, res.Code)
		return
	}
	t.Logf("无角色被正确拒绝: %s", w.Body.String())
}

// TestRBACDirectly 直接测试 RBAC 中间件本身(最纯粹的单元测试)
func TestRBACDirectly(t *testing.T) {
	cases := []struct {
		name     string
		role     string
		perm     string
		expectOK bool
	}{
		{"super_admin+fund_manage", RoleSuperAdmin, PermFundManage, true},
		{"super_admin+backup", RoleSuperAdmin, PermBackup, true},
		{"admin+fund_manage", RoleAdmin, PermFundManage, false},
		{"admin+backup", RoleAdmin, PermBackup, false},
		{"admin+key_manage", RoleAdmin, PermKeyManage, false},
		{"admin+node_manage", RoleAdmin, PermNodeManage, false},
		{"user+fund_manage", "user", PermFundManage, false},
		{"empty+fund_manage", "", PermFundManage, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/", nil)
			if tc.role != "" {
				c.Set(string(CtxRole), tc.role)
				c.Set(string(CtxUserID), "test-admin-id")
			}
			blocked := false
			RBAC(tc.perm)(c)
			if !c.IsAborted() {
				// 未被拦截, 调用 next handler
				blocked = false
			} else {
				blocked = true
			}
			if tc.expectOK && blocked {
				t.Errorf("%s: 期望放行, 实际被拦截", tc.name)
			}
			if !tc.expectOK && !blocked {
				t.Errorf("%s: 期望被拦截, 实际放行", tc.name)
			}
			t.Logf("%s: role=%s perm=%s → %s",
				tc.name, tc.role, tc.perm,
				map[bool]string{true: "放行", false: "拦截"}[!blocked])
		})
	}
}
