package handler

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/response"
	"nexus-panel/internal/security"
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
	response.OK(c, gin.H{"rotated_at": time.Now(), "hmac_key": newSecret})
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

// BackupToFile [26] POST /api/v1/admin/system/backup
// 系统备份: 保存到磁盘 + 返回下载，仅超级管理员可调用
func (h *AdminSystemHandler) BackupToFile(c *gin.Context) {
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
		"settings":  maskedSettings(settings),
	}
	b, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	filename := "nexus-backup-" + time.Now().Format("20060102-150405") + ".json"

	// 保存到磁盘
	if err := ensureBackupDir(); err == nil {
		filePath := filepath.Join(backupDir, filename)
		if err := os.WriteFile(filePath, b, 0600); err != nil {
			app.Get().Logger.Warn("备份写入磁盘失败", zap.String("file", filePath), zap.Error(err))
		}
	}

	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=\""+filename+"\"")
	c.Data(http.StatusOK, "application/json", b)
}

// maskedSettings 返回 settings 的副本, 其中敏感配置项(密钥/密码)被脱敏,
// 避免备份文件(可被超级管理员下载)泄露 epay_key / hmac_sub_secret / 邮箱密码等机密。
func maskedSettings(settings []model.Setting) []model.Setting {
	// 整体脱敏的 key: 值直接替换为掩码
	fullMask := map[string]bool{
		"hmac_sub_secret": true,
		"epay_key":        true,
	}
	out := make([]model.Setting, len(settings))
	for i, s := range settings {
		out[i] = s
		if fullMask[s.Key] {
			out[i].Value = []byte(`"****"`)
			continue
		}
		// notification 是 JSON 对象, 仅脱敏 email_password 字段
		if s.Key == "notification" {
			out[i].Value = maskNotificationValue(s.Value)
		}
	}
	return out
}

// maskNotificationValue 脱敏 notification 配置中的 email_password 字段
func maskNotificationValue(raw []byte) []byte {
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return []byte(`{}`)
	}
	if _, ok := m["email_password"]; ok {
		m["email_password"] = "****"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return []byte(`{}`)
	}
	return b
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

// validatePayAPIURL 校验支付 APIURL 防止 SSRF 探测内网/环回/元数据端点(P1)
func validatePayAPIURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("APIURL 不能为空")
	}
	u, err := neturl.Parse(raw)
	if err != nil {
		return fmt.Errorf("APIURL 格式无效")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("APIURL 仅支持 http/https")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("APIURL 主机为空")
	}
	// 拒绝内网/环回/链路本地/未指定 IP
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("APIURL 不允许指向内网/环回地址")
		}
	}
	// 拒绝常见云元数据主机名与本地后缀
	lower := strings.ToLower(host)
	switch {
	case lower == "metadata.google.internal",
		lower == "169.254.169.254",
		strings.HasSuffix(lower, ".internal"),
		strings.HasSuffix(lower, ".localhost"),
		strings.HasSuffix(lower, ".local"):
		return fmt.Errorf("APIURL 不允许指向内网/元数据地址")
	}
	return nil
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
	// SSRF 防护: 校验 APIURL 不指向内网/元数据端点
	if err := validatePayAPIURL(in.APIURL); err != nil {
		response.FailMsg(c, response.CodeParamError, err.Error())
		return
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
// 修复 SEC-ENCRYPT-01 (P1): 此前直接把前端原始 map 写入 settings, email_password 明文落库。
// 现在处理 email_password: 空值/脱敏值(含*)保留原存储值, 新明文值 AES 加密后存储。
// 修复: 先读取 existing 做全量合并, 避免前端只传 email 字段时丢失 telegram 等其他配置。
func (h *AdminSystemHandler) UpdateNotifyConfig(c *gin.Context) {
	var input map[string]interface{}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}

	// 先读取 existing 全量配置, 用前端传入的字段覆盖（合并而非替换）
	var existing map[string]interface{}
	_ = h.settingRepo.Get("notification", &existing)
	if existing == nil {
		existing = make(map[string]interface{})
	}
	for k, v := range input {
		existing[k] = v
	}

	masterKey := ""
	if a := app.Get(); a != nil && a.Cfg != nil {
		masterKey = a.Cfg.AESMasterKey
	}

	// 处理 email_password: 空值/脱敏值保留原密文, 新明文 AES 加密
	if pwd, ok := existing["email_password"].(string); ok {
		if pwd == "" || containsAsterisk(pwd) {
			// 空值或脱敏值: 从 DB 读取原密文保留（input 可能已经覆盖了 existing）
			// 注意: 此时 existing 已被 input 覆盖, 需要重新从 DB 读原始值
			var orig map[string]interface{}
			if err := h.settingRepo.Get("notification", &orig); err == nil {
				if ep, ok := orig["email_password"].(string); ok && ep != "" {
					existing["email_password"] = ep
				}
			}
		} else {
			// 新明文密码: AES 加密后存储, 同时清理可能的旧密码缓存
			existing["email_password"] = security.EncryptSecret(masterKey, pwd)
		}
	}

	if err := h.settingRepo.Set("notification", existing); err != nil {
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

// ============================================================
// 备份文件管理
// ============================================================

// backupDir 备份目录。
// 修复 STORAGE-BACKUP-01 (P0): 旧值 "/var/backups/nexus" 与 docker-compose.yml
// 挂载点 "./backups:/app/data/backup" 不一致, 导致备份写入容器内非挂载路径,
// 容器重建即丢失全部备份。现对齐为挂载目录 /app/data/backup, 备份持久化到宿主。
var backupDir = "/app/data/backup"

// ensureBackupDir 确保备份目录存在
func ensureBackupDir() error {
	return os.MkdirAll(backupDir, 0700)
}

// backupFileInfo 备份文件信息
type backupFileInfo struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	SizeHuman string    `json:"size_human"`
	CreatedAt time.Time `json:"created_at"`
}

// formatSize 格式化文件大小
func formatSize(size int64) string {
	switch {
	case size >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(size)/(1<<30))
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// ListBackups GET /api/v1/admin/system/backups
// 列出所有备份文件
func (h *AdminSystemHandler) ListBackups(c *gin.Context) {
	if err := ensureBackupDir(); err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}

	var backups []backupFileInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		backups = append(backups, backupFileInfo{
			Name:      info.Name(),
			Size:      info.Size(),
			SizeHuman: formatSize(info.Size()),
			CreatedAt: info.ModTime(),
		})
	}

	// 按创建时间倒序
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].CreatedAt.After(backups[j].CreatedAt)
	})

	response.OK(c, gin.H{"list": backups, "total": len(backups)})
}

// DeleteBackup DELETE /api/v1/admin/system/backups/:name
// 删除指定备份文件
func (h *AdminSystemHandler) DeleteBackup(c *gin.Context) {
	name := c.Param("name")
	// 安全检查: 防止路径穿越
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		response.FailMsg(c, response.CodeParamError, "无效的文件名")
		return
	}
	if !strings.HasSuffix(name, ".json") {
		response.FailMsg(c, response.CodeParamError, "仅允许删除 .json 备份文件")
		return
	}

	filePath := filepath.Join(backupDir, name)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		response.FailMsg(c, response.CodeNotFound, "备份文件不存在")
		return
	}
	if err := os.Remove(filePath); err != nil {
		response.FailMsg(c, response.CodeServerError, "删除失败: "+err.Error())
		return
	}
	response.OKMsg(c, "备份已删除")
}

// DownloadBackup GET /api/v1/admin/system/backups/:name/download
// 下载指定备份文件
func (h *AdminSystemHandler) DownloadBackup(c *gin.Context) {
	name := c.Param("name")
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		response.FailMsg(c, response.CodeParamError, "无效的文件名")
		return
	}

	filePath := filepath.Join(backupDir, name)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		response.FailMsg(c, response.CodeNotFound, "备份文件不存在")
		return
	}
	c.FileAttachment(filePath, name)
}

// ============================================================
// 系统更新 & GitHub 同步
// ============================================================

// systemActionResult 系统操作结果
type systemActionResult struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
}

// execCommandDir 在指定目录执行 shell 命令(超时 600 秒)
func execCommandDir(dir, name string, args ...string) systemActionResult {
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	result := systemActionResult{
		Output: strings.TrimSpace(string(output)),
	}
	if err != nil {
		result.Error = err.Error()
	}
	result.Success = err == nil
	return result
}

// getGitRoot 自动检测 git 仓库根目录
func getGitRoot() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		// 回退到环境变量或默认路径
		if root := os.Getenv("PROJECT_ROOT"); root != "" {
			return root
		}
		return "/root/nexus-panel"
	}
	return strings.TrimSpace(string(output))
}

// getCurrentBranch 获取当前分支名
func getCurrentBranch() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "main"
	}
	return strings.TrimSpace(string(output))
}

// execCommand 在 git 仓库根目录执行命令
func execCommand(name string, args ...string) systemActionResult {
	return execCommandDir(getGitRoot(), name, args...)
}

// ============================================================
// 异步在线更新（后台运行 + 实时日志轮询）
// ============================================================

var (
	gitPullMu    sync.Mutex
	gitPullLog   strings.Builder
	gitPullDone  bool
	gitPullOK    bool
)

// gitPullLogFile 持久化更新日志的文件路径。
// 修复 UI-LOG-01 (P1): 旧版日志只存内存(strings.Builder), syscall.Exec 原地
// 重启后新进程的 gitPullLog/gitPullDone 被重置为空/false, 前端轮询拿到空日志
// + done=false, 误以为新一轮更新开始却没日志, 显示"更新中"卡住。
// 改为同时写文件, 新进程启动时 init 恢复上次状态, 前端能正确看到"已完成"。
//
// [fix 2026-07-18] 路径从 /tmp 改到 /root/nexus-panel/.update-state/
// 原因: docker compose up -d 重启容器时, /tmp 不持久化, 状态文件丢失,
// 又触发同样的"更新中卡住"问题。改用挂载到宿主机的 /root/nexus-panel 目录,
// 容器重启后状态文件仍存在, init 能正确恢复 done/success。
const gitPullLogDir  = "/root/nexus-panel/.update-state"
const gitPullLogFile = "/root/nexus-panel/.update-state/git-pull.log"

// gitPullStateFile 持久化完成状态(done/success), 供新进程启动时恢复
const gitPullStateFile = "/root/nexus-panel/.update-state/git-pull.state"

func init() {
	// 启动时恢复上次更新状态(syscall.Exec 重启后生效)
	if data, err := os.ReadFile(gitPullStateFile); err == nil {
		var st struct {
			Done    bool `json:"done"`
			Success bool `json:"success"`
		}
		if json.Unmarshal(data, &st) == nil {
			gitPullDone = st.Done
			gitPullOK = st.Success
		}
	}
}

// logWrite 写入日志: 同时写内存(供本次运行实时轮询)和文件(供重启后恢复)
func logWrite(format string, args ...interface{}) {
	line := fmt.Sprintf(format, args...)
	line += "\n"
	gitPullLog.WriteString(line)
	// 追加写文件, 重启后可读回完整日志
	// [fix 2026-07-18] 首次写入前确保目录存在, 否则 OpenFile 会失败
	_ = os.MkdirAll(gitPullLogDir, 0755)
	f, err := os.OpenFile(gitPullLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		f.WriteString(line)
		f.Close()
	}
}

// gitPullWriteState 持久化完成状态(done/success), 供 syscall.Exec 重启后恢复
func gitPullWriteState(done, success bool) {
	st := struct {
		Done    bool `json:"done"`
		Success bool `json:"success"`
	}{Done: done, Success: success}
	if data, err := json.Marshal(st); err == nil {
		os.WriteFile(gitPullStateFile, data, 0644)
	}
}

// gitPullReadLogFromFile 从文件读取完整日志(重启后内存为空时使用)
func gitPullReadLogFromFile() string {
	data, err := os.ReadFile(gitPullLogFile)
	if err != nil {
		return ""
	}
	return string(data)
}

// setPullDone 统一设置更新完成状态(内存 + 持久化文件)
// 修复 UI-LOG-01 (P1): 所有完成点都通过此函数同步状态到文件,
// syscall.Exec 重启后 init 能恢复 done/success, 前端不会误判为新更新。
func setPullDone(success bool) {
	gitPullOK = success
	gitPullDone = true
	gitPullWriteState(true, success)
}

// execCommandLog 执行命令并将输出同时写入日志（默认 600s 超时）
func execCommandLog(dir, name string, args ...string) bool {
	return execCommandLogTimeout(dir, name, 600, args...)
}

// execCommandLogTimeout 执行命令并将输出同时写入日志（指定超时秒数）
func execCommandLogTimeout(dir, name string, timeoutSec int, args ...string) bool {
	logWrite("$ %s %s", name, strings.Join(args, " "))
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		logWrite("启动失败: %v", err)
		return false
	}
	// 实时读取 stdout
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			logWrite("  %s", scanner.Text())
		}
	}()
	// 实时读取 stderr
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			logWrite("  %s", scanner.Text())
		}
	}()
	if err := cmd.Wait(); err != nil {
		logWrite("失败: %v", err)
		return false
	}
	return true
}

// GitPull POST /api/v1/admin/system/git-pull
// 一键在线更新（异步）: 立即返回，后台执行构建，进度通过 GitPullLog 轮询
func (h *AdminSystemHandler) GitPull(c *gin.Context) {
	if !gitPullMu.TryLock() {
		response.OK(c, gin.H{"success": false, "msg": "已有更新任务正在执行，请查看日志"})
		return
	}

	gitPullLog.Reset()
	gitPullDone = false
	gitPullOK = false
	// 修复 UI-LOG-01 (P1): 同时清空持久化日志文件和状态文件, 避免新更新读到旧日志
	// [fix 2026-07-18] 顺便清理 /tmp 旧路径残留(从老版本升级时旧文件可能还在)
	_ = os.MkdirAll(gitPullLogDir, 0755)
	os.Remove(gitPullLogFile)
	os.Remove(gitPullStateFile)
	os.Remove("/tmp/nexus-git-pull.log")
	os.Remove("/tmp/nexus-git-pull.state")
	gitPullWriteState(false, false)

	go func() {
		defer gitPullMu.Unlock()
		gitRoot := getGitRoot()
		branch := getCurrentBranch()

		logWrite(">>> 1/7 清理残余垃圾 (历史备份/旧二进制/旧日志)")
		// [fix 2026-07-19] 一键更新流程 historically 会留下大量残留:
		//   - /app/nexus-panel.new / nexus-panel-fix / nexus-panel.backup.* (旧二进制)
		//   - gitRoot 下 *.bak* / backend.bak*/ / frontend.bak*/ (代码备份)
		//   - .update-state/git-pull.log.* (历史轮转日志)
		//   - /tmp/nexus-git-pull.{log,state} (老版本路径残留)
		// 每次更新前主动清理, 避免长期累积占用磁盘。
		cleanupPatterns := []string{
			"/app/nexus-panel.new",
			"/app/nexus-panel-fix",
			"/app/nexus-panel-new",
			"/tmp/nexus-git-pull.log",
			"/tmp/nexus-git-pull.state",
		}
		cleanedCount := 0
		for _, p := range cleanupPatterns {
			if err := os.RemoveAll(p); err == nil {
				cleanedCount++
			}
		}
		// 通配清理 /app/nexus-panel.backup.* (按时间轮转的旧二进制)
		if entries, err := filepath.Glob("/app/nexus-panel.backup.*"); err == nil {
			for _, e := range entries {
				if err := os.Remove(e); err == nil {
					cleanedCount++
				}
			}
		}
		// 通配清理 gitRoot 下的 .bak* 文件和 backend.bak*/ frontend.bak*/ 目录
		bakGlobPatterns := []string{
			filepath.Join(gitRoot, "*.bak*"),
			filepath.Join(gitRoot, "backend.bak*"),
			filepath.Join(gitRoot, "frontend.bak*"),
		}
		for _, pattern := range bakGlobPatterns {
			entries, err := filepath.Glob(pattern)
			if err != nil {
				continue
			}
			for _, e := range entries {
				// 跳过 .gitignore 等关键文件(虽然 *.bak* 不会匹配到, 但防御性写法)
				if strings.HasPrefix(filepath.Base(e), ".git") {
					continue
				}
				if err := os.RemoveAll(e); err == nil {
					cleanedCount++
				}
			}
		}
		// 清理 .update-state/ 下的轮转历史日志(保留当前 git-pull.log/state, 它们刚被重置过)
		if stateEntries, err := os.ReadDir(gitPullLogDir); err == nil {
			for _, e := range stateEntries {
				name := e.Name()
				// 只清轮转/备份文件, 当前正在用的 git-pull.log / git-pull.state 不动
				if name == "git-pull.log" || name == "git-pull.state" {
					continue
				}
				if err := os.RemoveAll(filepath.Join(gitPullLogDir, name)); err == nil {
					cleanedCount++
				}
			}
		}
		logWrite("已清理 %d 个残留文件/目录", cleanedCount)

		logWrite(">>> 2/7 预检工作树")
		// [fix 2026-07-19] 用 git status --porcelain=2 拿到结构化输出, 过滤掉未跟踪文件(?? 前缀),
		// 只对已跟踪文件的修改做 stash, 避免对 .update-state/ 等运行时目录做无意义的 stash
		// 导致后续 git stash pop 失败的问题。
		statusResult := execCommand("git", "status", "--porcelain")
		stashed := false
		hasTrackedChanges := false
		if statusResult.Output != "" {
			// 只关心已跟踪文件的修改(M / M_ / D / R / C 等), 跳过未跟踪文件(??)
			for _, line := range strings.Split(statusResult.Output, "\n") {
				if len(line) < 2 {
					continue
				}
				// porcelain 格式: XY filename, X=staged状态 Y=工作区状态
				// "XY" 前两字符中只要不是 "??" 就是已跟踪文件的修改
				if !strings.HasPrefix(line, "??") {
					hasTrackedChanges = true
					break
				}
			}
		}
		if hasTrackedChanges {
			logWrite("工作树有已跟踪文件的修改，自动 stash 保留:\n%s", statusResult.Output)
			stashResult := execCommand("git", "stash", "push", "-m", "nexus-panel-auto-stash-before-pull")
			if !stashResult.Success {
				logWrite("git stash 失败: %s", stashResult.Error)
				gitPullOK = false
				gitPullDone = true
				return
			}
			stashed = true
			logWrite("本地修改已 stash 保存，更新完成后将自动恢复")
		} else {
			logWrite("工作树干净(仅有未跟踪文件, 不影响 git reset)")
		}

		logWrite(">>> 3/7 拉取代码 git fetch origin (branch=%s)", branch)
		if !execCommandLog(gitRoot, "git", "fetch", "origin") {
			logWrite("git fetch 失败")
			setPullDone(false)
			return
		}

		logWrite(">>> 4/7 同步代码 git reset --hard origin/%s", branch)
		if !execCommandLog(gitRoot, "git", "reset", "--hard", "origin/"+branch) {
			logWrite("git reset 失败")
			setPullDone(false)
			return
		}

		// 更新成功后恢复之前 stash 的本地修改
		if stashed {
			logWrite(">>> 恢复 stash 的本地修改 git stash pop")
			popResult := execCommand("git", "stash", "pop")
			if !popResult.Success {
				logWrite("警告: git stash pop 失败(可能有冲突)，本地修改保留在 stash 中: %s", popResult.Error)
				logWrite("可手动执行 git stash list / git stash pop 处理")
			} else {
				logWrite("本地修改已恢复")
			}
		}

		logWrite(">>> 5/7 构建镜像 docker compose build panel frontend")
		logWrite("（首次构建约3-5分钟，后续有缓存会快很多）")
		if !execCommandLogTimeout(gitRoot, "docker", 1800, "compose", "build", "panel", "frontend") {
			logWrite("镜像构建失败，请查看上方日志")
			setPullDone(false)
			return
		}

		logWrite(">>> 6/7 复制新二进制到当前容器")
		newImage := "nexus-panel:latest"
		// 从新镜像中提取二进制
		extractCmd := exec.Command("docker", "run", "--rm", "--entrypoint", "sh", newImage, "-c", "cat /app/nexus-panel")
		newBinary, err := extractCmd.Output()
		if err != nil {
			logWrite("提取二进制失败: %v", err)
			setPullDone(false)
			return
		}
		// 写入临时路径，然后 mv 替换（防止写一半被读取）
		tmpPath := "/app/nexus-panel.new"
		// 修复 STORAGE-RESIDUAL-01 (P2): 写入临时二进制后若 Rename 失败/进程崩溃,
		// /app/nexus-panel.new 会永久残留占用磁盘。defer 兜底删除(Rename 成功后文件
		// 已不存在, Remove 是 no-op; Rename 失败时清理残留)。
		defer os.Remove(tmpPath)
		if err := os.WriteFile(tmpPath, newBinary, 0755); err != nil {
			logWrite("写入新二进制失败: %v", err)
			setPullDone(false)
			return
		}
		if err := os.Rename(tmpPath, "/app/nexus-panel"); err != nil {
			logWrite("替换二进制失败: %v", err)
			setPullDone(false)
			return
		}
		logWrite("二进制已更新")

		// 写入构建标记文件, 让 GitStatus 能检测下次是否有未部署的更新
		newHead := strings.TrimSpace(execCommand("git", "rev-parse", "--short", "HEAD").Output)
		if newHead != "" {
			_ = os.WriteFile(filepath.Join(gitRoot, ".last_build_version"), []byte(newHead), 0644)
		}

		logWrite(">>> 7/7 重建前端容器 + 清理旧镜像 + 重启面板 (用新换旧, 全自动)")
		if !execCommandLog(gitRoot, "docker", "compose", "up", "-d", "--no-deps", "frontend") {
			logWrite("重启前端失败")
			setPullDone(false)
			return
		}

		// 修复 STORAGE-UPDATE-01 (P0): 每次在线更新都会 docker compose build,
		// 累积大量悬空镜像层和构建缓存(单次更新可残留数百 MB-数 GB)。
		// 更新完成后清理悬空镜像 + build cache, 防止多次更新后磁盘爆满。
		// 旧版问题: 构建新 nexus-panel:latest 后, 旧镜像变 <none> 悬空镜像,
		// 虽然 docker image prune -f 能清, 但要等下一次手动清理才触发。
		// 现在主动 prune, 立即用新换旧:
		//   1. 清悬空镜像 (旧版本 <none> 立即释放)
		//   2. 清 build cache (构建缓存累积可达数 GB)
		//   3. 自动重启 panel 容器, 让新二进制立即生效 (无需用户手动点"重启面板")
		_ = execCommandLog(gitRoot, "docker", "image", "prune", "-f")
		_ = execCommandLog(gitRoot, "docker", "builder", "prune", "-f")

		// 自动重启 panel 容器, 让新二进制立即生效
		// 旧版要求用户手动点"重启面板"按钮, 容易遗忘导致更新不生效。
		// 现改为: 更新成功后 2 秒自动 systemctl restart nexus-panel,
		// 配合前端 /healthz boot_time 变化判断重启完成。
		logWrite(">>> 自动重启 panel 容器使新版本生效")
		go func() {
			time.Sleep(2 * time.Second)
			// systemctl restart 是原子操作, panel 会以新二进制重新启动
			execCommand("systemctl", "restart", "nexus-panel")
		}()

		logWrite("在线更新完成！面板已自动重启, 新版本立即生效 (用新换旧, 无需手动操作)")
		setPullDone(true)
	}()


	response.OK(c, gin.H{"success": true, "msg": "更新已开始，请在日志面板查看实时进度"})
}

// GitPullLog GET /api/v1/admin/system/git-pull-log
// 轮询获取在线更新的实时日志
// 修复 UI-LOG-01 (P1): syscall.Exec 重启后内存 gitPullLog 为空,
// 但 gitPullDone 已从状态文件恢复, 此时从日志文件读取完整内容返回前端。
func (h *AdminSystemHandler) GitPullLog(c *gin.Context) {
	logStr := gitPullLog.String()
	// 内存为空但 done=true(从文件恢复的状态), 说明是重启后, 从文件读日志
	if logStr == "" && gitPullDone {
		logStr = gitPullReadLogFromFile()
	}
	response.OK(c, gin.H{
		"log":     logStr,
		"done":    gitPullDone,
		"success": gitPullOK,
	})
}

// GitPullClearLog DELETE /api/v1/admin/system/git-pull-log
// 清理在线更新日志: 清空内存 + 删除持久化日志文件 + 重置状态
// 用于管理员手动清理更新日志, 避免长期累积占用磁盘。
// 注意: 用 TryLock 抢锁, 更新进行中时拒绝清理, 防止清掉正在写入的日志。
func (h *AdminSystemHandler) GitPullClearLog(c *gin.Context) {
	if !gitPullMu.TryLock() {
		response.FailMsg(c, response.CodeServerError, "更新进行中, 无法清理日志")
		return
	}
	defer gitPullMu.Unlock()
	// 清空内存日志
	gitPullLog.Reset()
	// 删除持久化日志文件
	_ = os.Remove(gitPullLogFile)
	// 重置状态文件
	_ = os.Remove(gitPullStateFile)
	gitPullDone = false
	gitPullOK = false
	response.OKMsg(c, "日志已清理")
}

// SystemRestart POST /api/v1/admin/system/restart
// 手动重启后端服务
func (h *AdminSystemHandler) SystemRestart(c *gin.Context) {
	go func() {
		time.Sleep(1 * time.Second)
		execCommand("systemctl", "restart", "nexus-panel")
	}()
	response.OKMsg(c, "重启指令已下发，面板即将恢复")
}

// GitPush POST /api/v1/admin/system/git-push
// 推送本地代码到 GitHub(协同开发提交)
func (h *AdminSystemHandler) GitPush(c *gin.Context) {
	var req struct {
		Message string `json:"message" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailMsg(c, response.CodeParamError, "请填写提交信息")
		return
	}

	branch := getCurrentBranch()

	// 1. git add . (使用 . 而非 -A，尊重 .gitignore)
	addResult := execCommand("git", "add", ".")
	if !addResult.Success {
		response.FailMsg(c, response.CodeServerError, "git add 失败: "+addResult.Error)
		return
	}

	// 2. git commit (可能因无变更而失败，这是正常的)
	commitResult := execCommand("git", "commit", "-m", req.Message)
	noChanges := strings.Contains(commitResult.Error, "nothing to commit") ||
		strings.Contains(commitResult.Output, "nothing to commit")

	// 3. git push
	pushResult := execCommand("git", "push", "origin", branch)
	if !pushResult.Success {
		response.FailMsg(c, response.CodeServerError, "git push 失败: "+pushResult.Error)
		return
	}

	response.OK(c, gin.H{
		"action":        "git-push",
		"branch":        branch,
		"no_changes":    noChanges,
		"commit_output": commitResult.Output,
		"push_output":   pushResult.Output,
	})
}

// GitStatus GET /api/v1/admin/system/git-status
// 查看当前 git 状态
// 返回:
//   - branch: 当前分支
//   - recent_5: 最近 5 条提交(用于查看历史)
//   - local_head / remote_head: 本地与远程最新提交哈希(短)
//   - behind: 本地落后远程多少个提交(>0 表示有可更新)
//   - ahead:  本地领先远程多少个提交(本地有未推送的提交)
//   - up_to_date: 本地是否与远程一致(behind==0 && ahead==0)
//
// 修复 UI-GITSTATUS-01: 旧版只返回 recent_5 历史提交, 前端无法判断
// "是否有新版本可更新"。用户更新到最新后, 历史提交列表照常显示, 看起来
// 像"更新了还在显示", 体验混乱。新增 behind/up_to_date 让前端能明确展示
// "已是最新版本"或"有 N 个更新可用"。
//
// [fix 2026-07-19] 移除 status 字段: 一键更新流程会 stash 本地修改, .update-state/
// 已加 .gitignore, 工作树始终干净, "本地变更"显示区已无意义, 前端也已删除该 UI。
func (h *AdminSystemHandler) GitStatus(c *gin.Context) {
	gitRoot := getGitRoot()
	logResult := execCommand("git", "log", "--oneline", "-5")
	branch := getCurrentBranch()

	// 先 fetch 远程引用(不修改工作树), 拿到 origin/<branch> 最新位置
	// 静默执行, 失败(如离线)不影响返回历史提交
	execCommandDir(gitRoot, "git", "fetch", "origin")

	localHead := execCommand("git", "rev-parse", "--short", "HEAD").Output
	remoteRef := "origin/" + branch
	if branch == "" {
		remoteRef = "origin/main"
	}
	remoteHead := execCommand("git", "rev-parse", "--short", remoteRef).Output
	// 去除可能的换行
	localHead = strings.TrimSpace(localHead)
	remoteHead = strings.TrimSpace(remoteHead)

	// 运行版本: 从标记文件读取上次构建部署时的 git HEAD
	runningVersion := ""
	if data, err := os.ReadFile(filepath.Join(gitRoot, ".last_build_version")); err == nil {
		runningVersion = strings.TrimSpace(string(data))
	}

	behind := 0
	ahead := 0
	changelog := ""
	changedFiles := ""
	if localHead != "" && remoteHead != "" && localHead != remoteHead {
		// 落后数: 远程有而本地没有的提交
		if c := execCommand("git", "rev-list", "--count", "HEAD.."+remoteRef).Output; c != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(c)); err == nil {
				behind = n
			}
		}
		// 领先数: 本地有而远程没有的提交(未推送)
		if c := execCommand("git", "rev-list", "--count", remoteRef+"..HEAD").Output; c != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(c)); err == nil {
				ahead = n
			}
		}
		// 有更新时, 获取远程相比本地的详细更新说明和变更文件列表
		if behind > 0 {
			changelog = execCommand("git", "log", "--format=%h %s%n%b", "HEAD.."+remoteRef).Output
			changedFiles = execCommand("git", "diff", "--stat", "HEAD..."+remoteRef).Output
		}
	}

	// 检测容器是否需要重建: 上次构建版本 != 当前代码版本
	// 场景: git pull 拉了新代码但还没 docker compose build, 或手动改了代码
	needsRebuild := false
	rebuildChangelog := ""
	if runningVersion != "" && localHead != "" && runningVersion != localHead {
		needsRebuild = true
		// 获取从上次构建版本到当前 HEAD 的提交记录(即未部署的更新)
		rebuildChangelog = execCommand("git", "log", "--oneline", runningVersion+".."+localHead).Output
	}

	response.OK(c, gin.H{
		"recent_5":          logResult.Output,
		"branch":            branch,
		"local_head":        localHead,
		"remote_head":       remoteHead,
		"behind":            behind,
		"ahead":             ahead,
		"up_to_date":        behind == 0 && ahead == 0 && !needsRebuild,
		"changelog":         strings.TrimSpace(changelog),
		"changed_files":     strings.TrimSpace(changedFiles),
		"running_version":   runningVersion,
		"needs_rebuild":     needsRebuild,
		"rebuild_changelog": strings.TrimSpace(rebuildChangelog),
	})
}

// ============================================================
// 磁盘清理
// ============================================================

// DiskUsage GET /api/v1/admin/system/disk-usage
// 查看磁盘使用情况
func (h *AdminSystemHandler) DiskUsage(c *gin.Context) {
	result := execCommand("df", "-h")
	response.OK(c, gin.H{"output": result.Output})
}

// DiskCleanup POST /api/v1/admin/system/disk-cleanup
// 清理无用文件: Docker 悬空镜像/容器、系统日志、临时文件、旧备份
// 修复 STORAGE-CLEANUP-01/02/03 (P0):
//   1. 旧版只清 .json 不清 .sql.gz, 导致数据库备份无限累积(真正的存储杀手)
//   2. 旧版 docker system prune --volumes 会误删 pg-data/redis-data 等业务卷
//   3. 旧版 keep_backup_count 默认 5 违反"自动备份仅保留最新一份"需求, 改为 1
//   4. 新增 PostgreSQL VACUUM ANALYZE 回收死元组(traffic_logs 高频删除后膨胀)
//   5. 新增 Docker build cache 清理(builder 缓存长期累积可达数 GB)
func (h *AdminSystemHandler) DiskCleanup(c *gin.Context) {
	var req struct {
		CleanDocker     bool `json:"clean_docker"`
		CleanLogs       bool `json:"clean_logs"`
		CleanTmp        bool `json:"clean_tmp"`
		CleanOldBackups bool `json:"clean_old_backups"`
		KeepBackupCount int  `json:"keep_backup_count"` // 保留最近 N 个备份, 0=全部保留
		VacuumDB        bool `json:"vacuum_db"`         // 清理 PostgreSQL 死元组
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		req.CleanDocker = true
		req.CleanLogs = true
		req.CleanTmp = true
		req.CleanOldBackups = true
		req.KeepBackupCount = 1 // 修复 STORAGE-CLEANUP-03: 默认只保留 1 份(用户需求)
		req.VacuumDB = true
	}

	var results []string

	// Docker 清理(修复 STORAGE-CLEANUP-02: 不用 --volumes 避免误删业务卷)
	if req.CleanDocker {
		results = append(results, "=== Docker 清理(悬空镜像+停止容器+构建缓存) ===")
		// 1. 清理已停止的容器 + 悬空镜像(dangling), 不动正在使用的镜像和卷
		out := execCommand("docker", "container", "prune", "-f")
		results = append(results, "停止的容器: "+out.Output)
		out = execCommand("docker", "image", "prune", "-f") // 仅 dangling 镜像
		results = append(results, "悬空镜像: "+out.Output)
		// 2. 清理 build cache(每次 docker build 都会累积, 是存储杀手)
		out = execCommand("docker", "builder", "prune", "-f")
		results = append(results, "构建缓存: "+out.Output)
		if out.Error != "" {
			results = append(results, "Docker 清理提示(容器内无 docker.sock 时正常): "+out.Error)
		}
	}

	// 日志清理
	if req.CleanLogs {
		results = append(results, "=== 日志清理 ===")
		// 清理超过 7 天的 journald 日志
		out := execCommand("journalctl", "--vacuum-time=7d")
		results = append(results, out.Output)
		// 清理 /var/log 下的旧日志
		out2 := execCommand("find", "/var/log", "-type", "f", "-name", "*.log", "-mtime", "+7", "-delete")
		results = append(results, "清理旧日志文件: "+out2.Output)
		// 清空系统日志
		out3 := execCommand("truncate", "-s", "0", "/var/log/syslog", "/var/log/messages", "/var/log/kern.log")
		results = append(results, "清空系统日志: "+out3.Output)
	}

	// 临时文件清理
	if req.CleanTmp {
		results = append(results, "=== 临时文件清理 ===")
		out := execCommand("find", "/tmp", "-type", "f", "-mtime", "+1", "-delete")
		results = append(results, "清理 /tmp 旧文件: "+out.Output)
	}

	// 旧备份清理(修复 STORAGE-CLEANUP-01: 同时清理 .json 和 .sql.gz)
	if req.CleanOldBackups && req.KeepBackupCount > 0 {
		results = append(results, "=== 旧备份清理 ===")
		deleted := cleanupOldBackups(req.KeepBackupCount)
		results = append(results, fmt.Sprintf("已删除 %d 个旧备份, 保留最近 %d 个", deleted, req.KeepBackupCount))
	}

	// PostgreSQL VACUUM(修复 STORAGE-CLEANUP-04: traffic_logs 高频 DELETE 后死元组膨胀)
	if req.VacuumDB {
		results = append(results, "=== PostgreSQL VACUUM ANALYZE ===")
		out := execCommand("docker", "exec", "nexus-postgres", "psql", "-U",
			os.Getenv("DB_USER"), "-d", os.Getenv("DB_NAME"),
			"-c", "VACUUM ANALYZE;")
		results = append(results, out.Output)
		if out.Error != "" {
			results = append(results, "VACUUM 提示: "+out.Error)
		}
	}

	// 最终磁盘状态
	diskResult := execCommand("df", "-h")
	results = append(results, "=== 清理后磁盘状态 ===")
	results = append(results, diskResult.Output)

	response.OK(c, gin.H{
		"summary": results,
		"output":  strings.Join(results, "\n"),
	})
}

// cleanupOldBackups 清理备份目录, 对每种类型(.json / .sql.gz)各保留最近 N 份
// 修复 STORAGE-CLEANUP-01 (P0): 旧版只清 .json 不清 .sql.gz, 数据库备份无限累积
func cleanupOldBackups(keep int) int {
	if err := ensureBackupDir(); err != nil {
		return 0
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return 0
	}
	// 按后缀分组
	type entryInfo struct {
		name string
		mod  time.Time
	}
	groups := map[string][]entryInfo{
		".json":   {},
		".sql.gz": {},
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		info, err := e.Info()
		if err != nil {
			continue
		}
		switch {
		case strings.HasSuffix(name, ".sql.gz"):
			groups[".sql.gz"] = append(groups[".sql.gz"], entryInfo{name, info.ModTime()})
		case strings.HasSuffix(name, ".json"):
			groups[".json"] = append(groups[".json"], entryInfo{name, info.ModTime()})
		}
	}
	deleted := 0
	for _, g := range groups {
		if len(g) <= keep {
			continue
		}
		// 按修改时间倒序(最新在前)
		sort.Slice(g, func(i, j int) bool { return g[i].mod.After(g[j].mod) })
		// 删除超出保留数量的旧文件
		for _, e := range g[keep:] {
			if err := os.Remove(filepath.Join(backupDir, e.name)); err == nil {
				deleted++
			}
		}
	}
	return deleted
}

