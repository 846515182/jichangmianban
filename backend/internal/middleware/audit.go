package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
)

// P1-AUDIT: 审计日志中需脱敏的敏感字段(全小写匹配, JSON key 不区分大小写也覆盖)
// 命中后值会被替换为 "***" 再截断到 512 字节, 避免明文密码/密钥进入审计表
var sensitiveFields = []string{
	"password", "old_password", "new_password",
	"secret", "api_key", "apikey",
	"smtp_pass", "smtp_password",
	"private_key", "node_token",
}

// AuditAction 记录管理员操作审计日志中间件
// 用法: admin.PUT("/nodes/:id", AuditAction("node.update"), adminNodeH.NodeUpdate)
func AuditAction(action string) gin.HandlerFunc {
        return func(c *gin.Context) {
                // 在 c.Next() 之前读取请求体，避免 body 被 handler 消费后无法读取
                bodyBytes := readRequestBody(c)

                c.Next() // 执行业务逻辑

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
                detail := formatRequestBody(bodyBytes)

                audit := &model.AdminAction{
                        AdminID:    claims.UserID,
                        AdminName:  claims.Username,
                        Action:     action,
                        TargetType: extractTargetType(c.Request.URL.Path),
                        TargetID:   c.Param("id"),
                        Detail:     detail,
                        IP:         c.ClientIP(),
                }

			// 异步写入，不阻塞响应; 失败时短暂重试, 避免审计日志丢失
			go func(a *model.AdminAction) {
				defer func() { _ = recover() }()
				r := repo.NewAdminActionRepo(app.Get().DB)
				for i := 0; i < 3; i++ {
					if err := r.Create(a); err == nil {
						return
					}
					time.Sleep(time.Duration(i+1) * time.Second)
				}
			}(audit)
        }
}

// readRequestBody 在 c.Next() 之前读取请求体，并将 body 重置以便 handler 后续使用
func readRequestBody(c *gin.Context) []byte {
        if c.Request.Body == nil {
                return nil
        }
        body, err := io.ReadAll(c.Request.Body)
        if err != nil {
                return nil
        }
        // 重新设置 body 供后续 handler 读取
        c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
        return body
}

// formatRequestBody 格式化已读取的请求体字节, 对敏感字段脱敏后截取最多 512 字节
// P1-AUDIT: 防止明文密码/密钥/私钥等敏感数据进入审计表
func formatRequestBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	// 尝试解析为 JSON 做字段级脱敏; 失败则按原始文本截断(无法识别字段, 仅截断)
	var obj interface{}
	if json.Unmarshal(body, &obj) == nil {
		maskSensitive(obj)
		if formatted, err := json.Marshal(obj); err == nil {
			if len(formatted) > 512 {
				formatted = formatted[:512]
			}
			return string(formatted)
		}
	}
	if len(body) > 512 {
		body = body[:512]
	}
	return string(body)
}

// maskSensitive 递归遍历 JSON 解析结果, 将命中 sensitiveFields 的 key 的值替换为 "***"
// 支持 map[string]interface{} 与 []interface{}; key 比较忽略大小写
func maskSensitive(v interface{}) {
	switch t := v.(type) {
	case map[string]interface{}:
		for k, val := range t {
			if isSensitiveField(k) {
				t[k] = "***"
				continue
			}
			maskSensitive(val)
		}
	case []interface{}:
		for i := range t {
			maskSensitive(t[i])
		}
	}
}

// isSensitiveField 判断字段名是否敏感(忽略大小写)
func isSensitiveField(key string) bool {
	lk := strings.ToLower(key)
	for _, s := range sensitiveFields {
		if lk == s {
			return true
		}
	}
	return false
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
