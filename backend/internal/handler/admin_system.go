package handler

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
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

// ============================================================
// 备份文件管理
// ============================================================

const backupDir = "/var/backups/nexus"

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

// logWrite 写入日志并在末尾追加换行
func logWrite(format string, args ...interface{}) {
	gitPullLog.WriteString(fmt.Sprintf(format, args...))
	gitPullLog.WriteString("\n")
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

	go func() {
		defer gitPullMu.Unlock()
		gitRoot := getGitRoot()
		branch := getCurrentBranch()

		logWrite(">>> 1/6 预检工作树")
		statusResult := execCommand("git", "status", "--short")
		if statusResult.Output != "" {
			logWrite("工作树有未提交的修改:\n%s", statusResult.Output)
			gitPullOK = false
			gitPullDone = true
			return
		}
		logWrite("工作树干净")

		logWrite(">>> 2/6 拉取代码 git fetch origin (branch=%s)", branch)
		if !execCommandLog(gitRoot, "git", "fetch", "origin") {
			logWrite("git fetch 失败")
			gitPullOK = false
			gitPullDone = true
			return
		}

		logWrite(">>> 3/6 同步代码 git reset --hard origin/%s", branch)
		if !execCommandLog(gitRoot, "git", "reset", "--hard", "origin/"+branch) {
			logWrite("git reset 失败")
			gitPullOK = false
			gitPullDone = true
			return
		}

		logWrite(">>> 4/6 构建镜像 docker compose build panel frontend")
		logWrite("（首次构建约3-5分钟，后续有缓存会快很多）")
		if !execCommandLogTimeout(gitRoot, "docker", 1800, "compose", "build", "panel", "frontend") {
			logWrite("镜像构建失败，请查看上方日志")
			gitPullOK = false
			gitPullDone = true
			return
		}

		logWrite(">>> 5/6 复制新二进制到当前容器")
		newImage := "nexus-panel:latest"
		// 从新镜像中提取二进制
		extractCmd := exec.Command("docker", "run", "--rm", "--entrypoint", "sh", newImage, "-c", "cat /app/nexus-panel")
		newBinary, err := extractCmd.Output()
		if err != nil {
			logWrite("提取二进制失败: %v", err)
			gitPullOK = false
			gitPullDone = true
			return
		}
		// 写入临时路径，然后 mv 替换（防止写一半被读取）
		tmpPath := "/app/nexus-panel.new"
		if err := os.WriteFile(tmpPath, newBinary, 0755); err != nil {
			logWrite("写入新二进制失败: %v", err)
			gitPullOK = false
			gitPullDone = true
			return
		}
		if err := os.Rename(tmpPath, "/app/nexus-panel"); err != nil {
			logWrite("替换二进制失败: %v", err)
			gitPullOK = false
			gitPullDone = true
			return
		}
		logWrite("二进制已更新")

		logWrite(">>> 6/6 重建前端容器 docker compose up -d frontend")
		if !execCommandLog(gitRoot, "docker", "compose", "up", "-d", "frontend") {
			logWrite("重启前端失败")
			gitPullOK = false
			gitPullDone = true
			return
		}

		logWrite("在线更新完成！面板将在3秒后自动重启生效（页面会短暂不可用）")
		gitPullOK = true
		gitPullDone = true

		// 延迟重启面板自身（sleep 后 exec 替换当前进程）
		time.Sleep(3 * time.Second)
		logWrite("正在重启面板...")
		syscall.Exec("/app/nexus-panel", os.Args, os.Environ())
	}()

	response.OK(c, gin.H{"success": true, "msg": "更新已开始，请在日志面板查看实时进度"})
}

// GitPullLog GET /api/v1/admin/system/git-pull-log
// 轮询获取在线更新的实时日志
func (h *AdminSystemHandler) GitPullLog(c *gin.Context) {
	response.OK(c, gin.H{
		"log":     gitPullLog.String(),
		"done":    gitPullDone,
		"success": gitPullOK,
	})
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
func (h *AdminSystemHandler) GitStatus(c *gin.Context) {
	statusResult := execCommand("git", "status", "--short")
	logResult := execCommand("git", "log", "--oneline", "-5")
	branch := getCurrentBranch()

	response.OK(c, gin.H{
		"status":   statusResult.Output,
		"recent_5": logResult.Output,
		"branch":   branch,
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
// 清理无用文件: Docker 镜像/容器/卷、系统日志、临时文件、旧备份
func (h *AdminSystemHandler) DiskCleanup(c *gin.Context) {
	var req struct {
		CleanDocker      bool `json:"clean_docker"`
		CleanLogs        bool `json:"clean_logs"`
		CleanTmp         bool `json:"clean_tmp"`
		CleanOldBackups  bool `json:"clean_old_backups"`
		KeepBackupCount  int  `json:"keep_backup_count"` // 保留最近 N 个备份, 0=全部保留
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		req.CleanDocker = true
		req.CleanLogs = true
		req.CleanTmp = true
		req.CleanOldBackups = true
		req.KeepBackupCount = 5
	}

	var results []string

	// Docker 清理
	if req.CleanDocker {
		results = append(results, "=== Docker 清理 ===")
		out := execCommand("docker", "system", "prune", "-af", "--volumes")
		results = append(results, out.Output)
		if out.Error != "" {
			results = append(results, "Error: "+out.Error)
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

	// 旧备份清理
	if req.CleanOldBackups && req.KeepBackupCount > 0 {
		results = append(results, "=== 旧备份清理 ===")
		if err := ensureBackupDir(); err == nil {
			entries, _ := os.ReadDir(backupDir)
			// 按修改时间排序
			sort.Slice(entries, func(i, j int) bool {
				infoI, _ := entries[i].Info()
				infoJ, _ := entries[j].Info()
				if infoI == nil || infoJ == nil {
					return false
				}
				return infoI.ModTime().After(infoJ.ModTime())
			})
			// 删除超过保留数量的旧备份
			deleted := 0
			for i := req.KeepBackupCount; i < len(entries); i++ {
				if !entries[i].IsDir() && strings.HasSuffix(entries[i].Name(), ".json") {
					os.Remove(filepath.Join(backupDir, entries[i].Name()))
					deleted++
				}
			}
			results = append(results, fmt.Sprintf("已删除 %d 个旧备份, 保留最近 %d 个", deleted, req.KeepBackupCount))
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

