package middleware

import (
        "bytes"
        "encoding/json"
        "io"
        "strings"

        "github.com/gin-gonic/gin"

        "nexus-panel/internal/app"
        "nexus-panel/internal/model"
        "nexus-panel/internal/repo"
)

// AuditAction 记录管理员操作审计日志中间件
// 用法: admin.PUT("/nodes/:id", AuditAction("node.update"), adminNodeH.NodeUpdate)
func AuditAction(action string) gin.HandlerFunc {
        return func(c *gin.Context) {
                c.Next() // 先执行业务逻辑

                // 仅记录成功的状态变更操作 (POST/PUT/DELETE/PATCH)
                method := c.Request.Method
                if method != "POST" && method != "PUT" && method != "DELETE" && method != "PATCH" {
                        return
                }
                status := c.Writer.Status()
                if status < 200 || status >= 300 {
                        return
                }

                claims := GetClaims(c)
                if claims == nil {
                        return
                }
                // 仅记录管理员操作
                if claims.Role != RoleSuperAdmin && claims.Role != "admin" {
                        return
                }

                // 提取请求体摘要(最多 512 字节)
                detail := extractRequestBody(c)

                audit := &model.AdminAction{
                        AdminID:    claims.UserID,
                        AdminName:  claims.Username,
                        Action:     action,
                        TargetType: extractTargetType(c.Request.URL.Path),
                        TargetID:   c.Param("id"),
                        Detail:     detail,
                        IP:         c.ClientIP(),
                }

                // 异步写入，不阻塞响应
                go func(a *model.AdminAction) {
                        repo := repo.NewAdminActionRepo(app.Get().DB)
                        _ = repo.Create(a)
                }(audit)
        }
}

func extractRequestBody(c *gin.Context) string {
        if c.Request.Body == nil {
                return ""
        }
        body, err := io.ReadAll(c.Request.Body)
        if err != nil {
                return ""
        }
        // 重新设置 body 供后续 handler 读取
        c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
        if len(body) > 512 {
                body = body[:512]
        }
        // 尝试格式化 JSON
        var obj interface{}
        if json.Unmarshal(body, &obj) == nil {
                if formatted, err := json.Marshal(obj); err == nil {
                        return string(formatted)
                }
        }
        return string(body)
}

func extractTargetType(path string) string {
        parts := strings.Split(strings.Trim(path, "/"), "/")
        // /api/v1/admin/nodes -> nodes
        // /api/v1/admin/users -> users
        for i, p := range parts {
                if p == "admin" && i+1 < len(parts) {
                        return parts[i+1]
                }
        }
        return "unknown"
}
