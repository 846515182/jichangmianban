package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nexus-panel/internal/app"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/response"
	"nexus-panel/internal/service"
)

// AdminSystemHandler 管理端系统处理器
type AdminSystemHandler struct {
	trafficService *service.TrafficService
	settingRepo    *repo.SettingRepo
	loginAuditRepo *repo.LoginAuditRepo
	nodeRepo       *repo.NodeRepo
	userRepo       *repo.UserRepo
	subRepo        *repo.SubscriptionRepo
	paymentSvc     *service.PaymentService
	emailSvc       *service.EmailService
}

// NewAdminSystemHandler 创建管理端系统处理器
func NewSystemHandler(ts *service.TrafficService, sr *repo.SettingRepo, la *repo.LoginAuditRepo, nr *repo.NodeRepo, ur *repo.UserRepo, subR *repo.SubscriptionRepo) *AdminSystemHandler {
	return &AdminSystemHandler{trafficService: ts, settingRepo: sr, loginAuditRepo: la, nodeRepo: nr, userRepo: ur, subRepo: subR}
}

// SetPaymentService 注入支付服务(供 pay-config 接口使用)
func (h *AdminSystemHandler) SetPaymentService(p *service.PaymentService) {
	h.paymentSvc = p
}

// SetEmailService 注入邮件服务(供 notify-config 接口使用)
func (h *AdminSystemHandler) SetEmailService(e *service.EmailService) {
	h.emailSvc = e
}

// TrafficTop [21] GET /api/v1/admin/traffic/top
// 流量 TOP 统计(用户/节点)
// 前端传 range 参数(字符串: today/week/month/year)，后端转换为天数
func (h *AdminSystemHandler) TrafficTop(c *gin.Context) {
	limit := atoiDefault(c.Query("limit"), 10)
	days := 7

	// 解析 range 参数（字符串）
	rangeStr := c.Query("range")
	switch rangeStr {
	case "today":
		days = 1
	case "week":
		days = 7
	case "month":
		days = 30
	case "year":
		days = 365
	default:
		// 兼容旧的 days 参数（整数）
		if d := atoiDefault(c.Query("days"), 0); d > 0 {
			days = d
		}
	}

	// 安全校验: 限制 limit 和 days 的取值范围
	if limit < 1 || limit > 100 {
		limit = 10
	}
	if days < 1 || days > 365 {
		days = 7
	}

	res, err := h.trafficService.Top(limit, days)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, res)
}

// TrafficTrend [GET] /api/v1/admin/traffic/trend
// 流量趋势(按天聚合, 用于 ECharts 折线图)
// query: days (整数) 或 range (字符串: today/week/month/year)
func (h *AdminSystemHandler) TrafficTrend(c *gin.Context) {
	days := 7

	// 解析 range 参数（字符串）
	rangeStr := c.Query("range")
	switch rangeStr {
	case "today":
		days = 1
	case "week":
		days = 7
	case "month":
		days = 30
	case "year":
		days = 365
	default:
		// 兼容旧的 days 参数（整数）
		if d := atoiDefault(c.Query("days"), 0); d > 0 {
			days = d
		}
	}

	// 安全校验
	if days < 1 || days > 365 {
		days = 7
	}

	res, err := h.trafficService.Trend(days)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, res)
}

// Dashboard [22] GET /api/v1/admin/dashboard
// 仪表盘数据
func (h *AdminSystemHandler) Dashboard(c *gin.Context) {
	stats, err := h.trafficService.Dashboard()
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, stats)
}

// RotateHMAC [24] POST /api/v1/admin/system/rotate-hmac
// 轮换订阅签名密钥(仅超级管理员，路由层 RBAC 校验)
// 生成新的 HMAC 密钥，持久化到 settings 并更新内存配置
func (h *AdminSystemHandler) RotateHMAC(c *gin.Context) {
	newSecret, err := generateRandomSecret(32)
	if err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	// 持久化到 settings
	if err := h.settingRepo.Set("hmac_sub_secret", newSecret); err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	// 更新运行时配置
	app.Get().Cfg.HMACSubSecret = newSecret
	response.OK(c, gin.H{"rotated_at": time.Now()})
}

// LoginAudit [25] GET /api/v1/admin/system/login-audit
// 登录审计列表(支持按 target_type 过滤)
func (h *AdminSystemHandler) LoginAudit(c *gin.Context) {
	page, size := parsePage(c)
	targetType := c.Query("target_type")
	list, total, err := h.loginAuditRepo.ListAll(targetType, page, size)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, gin.H{"list": list, "total": total})
}

// Backup [26] POST /api/v1/admin/system/backup
// 系统备份(导出关键表的 JSON 快照)，仅超级管理员可调用
func (h *AdminSystemHandler) Backup(c *gin.Context) {
	users, _, err := h.userRepo.List(1, 10000, "")
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	nodes, _, err := h.nodeRepo.List(1, 10000, "")
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	settings, err := h.settingRepo.GetAll()
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}

	// 脱敏敏感字段
	type safeUser struct {
		ID           string     `json:"id"`
		Username     string     `json:"username"`
		Email        string     `json:"email"`
		Status       string     `json:"status"`
		PlanID       *string    `json:"plan_id"`
		ExpiredAt    *time.Time `json:"expired_at"`
		CreatedAt    time.Time  `json:"created_at"`
		TrafficLimit int64      `json:"traffic_limit"`
		TrafficUsed  int64      `json:"traffic_used"`
	}
	type safeNode struct {
		ID            string    `json:"id"`
		Name          string    `json:"name"`
		ServerAddress string    `json:"server_address"`
		Port          int       `json:"port"`
		Protocol      string    `json:"protocol"`
		IsEnabled     bool      `json:"is_enabled"`
		GrpcPort      int       `json:"grpc_port"`
		CreatedAt     time.Time `json:"created_at"`
	}

	safeUsers := make([]safeUser, len(users))
	for i, u := range users {
		safeUsers[i] = safeUser{
			ID: u.ID, Username: u.Username, Email: u.Email,
			Status: u.Status, PlanID: u.PlanID, ExpiredAt: u.ExpiredAt,
			CreatedAt: u.CreatedAt, TrafficLimit: u.TrafficLimit,
			TrafficUsed: u.TrafficUsed,
		}
	}
	safeNodes := make([]safeNode, len(nodes))
	for i, n := range nodes {
		safeNodes[i] = safeNode{
			ID: n.ID, Name: n.Name, ServerAddress: n.ServerAddress,
			Port: n.Port, Protocol: n.Protocol, IsEnabled: n.IsEnabled,
			GrpcPort: n.GrpcPort, CreatedAt: n.CreatedAt,
		}
	}

	snapshot := gin.H{
		"version":   "1.0",
		"backup_at": time.Now(),
		"users":     safeUsers,
		"nodes":     safeNodes,
		"settings":  settings,
	}
	b, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	filename := "nexus-backup-" + time.Now().Format("20060102-150405") + ".json"
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=\""+filename+"\"")
	c.Data(http.StatusOK, "application/json", b)
}

// SubConfig [27] PUT /api/v1/admin/system/sub-config
// 更新订阅配置(如默认订阅类型、订阅基础URL等)
// 安全修复: 白名单校验允许的 key, 防止注入任意配置项
func (h *AdminSystemHandler) SubConfig(c *gin.Context) {
	var cfg map[string]interface{}
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	// 白名单: 仅允许已知的订阅配置 key
	allowedKeys := map[string]bool{
		"default_sub_type": true,
		"sub_base_url":     true,
		"sub_prefix":       true,
		"sub_suffix":       true,
		"sub_info":         true,
		"show_node_info":   true,
	}
	for k := range cfg {
		if !allowedKeys[k] {
			response.FailMsg(c, response.CodeParamError, "不允许的配置项: "+k)
			return
		}
	}
	if err := h.settingRepo.Set("sub_config", cfg); err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OKMsg(c, "订阅配置已更新")
}

// GetSubConfig GET /api/v1/admin/system/sub-config
func (h *AdminSystemHandler) GetSubConfig(c *gin.Context) {
	var cfg map[string]interface{}
	if err := h.settingRepo.Get("sub_config", &cfg); err != nil {
		// 不存在则返回空对象
		response.OK(c, gin.H{})
		return
	}
	response.OK(c, cfg)
}

// generateRandomSecret 生成 n 字节随机密钥(hex 编码)
func generateRandomSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// GetPayConfig GET /api/v1/admin/system/pay-config
// 获取 EPay 支付配置
func (h *AdminSystemHandler) GetPayConfig(c *gin.Context) {
	if h.paymentSvc == nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	cfg, err := h.paymentSvc.GetConfig()
	if err != nil {
		// 配置不存在则返回默认空配置
		response.OK(c, &service.EPayConfig{})
		return
	}
	// 脱敏: 不返回 key 完整内容
	masked := &service.EPayConfig{
		PID:       cfg.PID,
		APIURL:    cfg.APIURL,
		Enabled:   cfg.Enabled,
		NotifyURL: cfg.NotifyURL,
		ReturnURL: cfg.ReturnURL,
	}
	if cfg.Key != "" {
		masked.Key = maskSecret(cfg.Key)
	}
	response.OK(c, masked)
}

// UpdatePayConfig PUT /api/v1/admin/system/pay-config
// 更新 EPay 支付配置
func (h *AdminSystemHandler) UpdatePayConfig(c *gin.Context) {
	if h.paymentSvc == nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	var in service.EPayConfig
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	// 若前端传入的是脱敏值(包含 *), 则保留原 key
	if in.Key != "" && containsAsterisk(in.Key) {
		existing, err := h.paymentSvc.GetConfig()
		if err == nil && existing != nil {
			in.Key = existing.Key
		}
	}
	if err := h.paymentSvc.SaveConfig(&in); err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OKMsg(c, "支付配置已更新")
}

// TestPayConfig POST /api/v1/admin/system/pay-config/test
// 测试 EPay 支付配置是否正确(调用易支付"查询商户信息"API)
func (h *AdminSystemHandler) TestPayConfig(c *gin.Context) {
	if h.paymentSvc == nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	var in service.EPayConfig
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	// 若前端传入的是脱敏值(包含 *), 则用已保存的 key 测试
	if in.Key != "" && containsAsterisk(in.Key) {
		existing, err := h.paymentSvc.GetConfig()
		if err == nil && existing != nil {
			in.Key = existing.Key
		}
	}
	result, err := h.paymentSvc.TestConnection(in.PID, in.Key, in.APIURL)
	if err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	// 提取关键信息返回
	msg := "连接成功"
	if active, ok := result["active"].(float64); ok {
		if active == 1 {
			msg = "连接成功，商户状态正常"
		} else {
			msg = "连接成功，但商户状态异常(可能已封禁)"
		}
	}
	if money, ok := result["money"].(string); ok {
		msg += fmt.Sprintf("，余额: %s元", money)
	}
	response.OKMsg(c, msg)
}

// maskSecret 脱敏处理: 仅保留前 4 位与后 4 位, 中间以 **** 代替
func maskSecret(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}

// containsAsterisk 判断字符串是否包含 *
func containsAsterisk(s string) bool {
	for _, ch := range s {
		if ch == '*' {
			return true
		}
	}
	return false
}

// GetNotifyConfig GET /api/v1/admin/system/notify-config
func (h *AdminSystemHandler) GetNotifyConfig(c *gin.Context) {
	if h.emailSvc == nil {
		response.OK(c, gin.H{
			"email_enabled":    false,
			"telegram_enabled": false,
			"email_host":       "",
			"email_port":       587,
			"email_user":       "",
			"email_from":       "",
			"email_password":   "",
			"telegram_bot":     "",
			"telegram_chat":    "",
		})
		return
	}
	cfg, err := h.emailSvc.GetConfig()
	if err != nil {
		response.OK(c, gin.H{
			"email_enabled":    false,
			"telegram_enabled": false,
			"email_host":       "",
			"email_port":       587,
			"email_user":       "",
			"email_from":       "",
			"email_password":   "",
			"telegram_bot":     "",
			"telegram_chat":    "",
		})
		return
	}
	// 脱敏：不返回完整密码
	maskedPassword := ""
	if cfg.Password != "" {
		if len(cfg.Password) > 8 {
			maskedPassword = cfg.Password[:4] + "****" + cfg.Password[len(cfg.Password)-4:]
		} else {
			maskedPassword = "****"
		}
	}
	response.OK(c, gin.H{
		"email_enabled":    cfg.Enabled,
		"telegram_enabled": false,
		"email_host":       cfg.Host,
		"email_port":       cfg.Port,
		"email_user":       cfg.User,
		"email_from":       cfg.From,
		"email_password":   maskedPassword,
		"telegram_bot":     "",
		"telegram_chat":    "",
	})
}

// UpdateNotifyConfig PUT /api/v1/admin/system/notify-config
func (h *AdminSystemHandler) UpdateNotifyConfig(c *gin.Context) {
	var cfg map[string]interface{}
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	if err := h.settingRepo.Set("notification", cfg); err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OKMsg(c, "通知配置已更新")
}

// AdminAuditLog [GET] /api/v1/admin/audit
// 管理员操作审计日志（仅超级管理员可查看）
func (h *AdminSystemHandler) AdminAuditLog(c *gin.Context) {
        page, size := parsePage(c)
        action := c.Query("action")
        adminID := c.Query("admin_id")
        auditRepo := repo.NewAdminActionRepo(app.Get().DB)
        list, total, err := auditRepo.List(page, size, action, adminID)
        if err != nil {
                response.Fail(c, response.CodeDBError)
                return
        }
        response.OK(c, gin.H{"list": list, "total": total})
}


// TestNotifyConfig POST /api/v1/admin/system/notify-config/test
// 测试邮件配置是否正确（支持 TLS，兼容 Mailtrap 等现代 SMTP 服务）
func (h *AdminSystemHandler) TestNotifyConfig(c *gin.Context) {
	if h.emailSvc == nil {
		response.FailMsg(c, response.CodeServerError, "邮件服务未初始化")
		return
	}

	var cfg service.EmailConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}

	// 如果密码是脱敏值（包含****），则从数据库读取原密码
	if cfg.Password != "" && containsAsterisk(cfg.Password) {
		existingCfg, err := h.emailSvc.GetConfig()
		if err == nil && existingCfg != nil {
			cfg.Password = existingCfg.Password
		}
	}

	if err := h.emailSvc.TestConfig(&cfg); err != nil {
		response.FailMsg(c, response.CodeServerError, "邮件发送失败: "+err.Error())
		return
	}

	response.OKMsg(c, "测试邮件已发送，请查收")
}

