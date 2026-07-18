package service

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"strings"
	"time"

	"nexus-panel/internal/config"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/security"
)

// EmailService 邮件服务
type EmailService struct {
	settingRepo *repo.SettingRepo
	cfg         *config.Config
}

// NewEmailService 创建邮件服务。
// cfg 用于在数据库 notification 配置缺失/未启用时回退到环境变量 SMTP_* 配置，
// 保证管理员在 UI 配好邮件前，注册/重置等邮件仍能通过环境变量发出。
func NewEmailService(sr *repo.SettingRepo, cfg *config.Config) *EmailService {
	return &EmailService{settingRepo: sr, cfg: cfg}
}

// EmailConfig 邮件配置
type EmailConfig struct {
	Enabled  bool   `json:"email_enabled"`
	Host     string `json:"email_host"`
	Port     int    `json:"email_port"`
	User     string `json:"email_user"`
	Password string `json:"email_password"`
	From     string `json:"email_from"`
}

// notifyConfig 数据库存储的通知配置（包含邮件和 Telegram）
type notifyConfig struct {
	EmailEnabled    bool   `json:"email_enabled"`
	EmailHost       string `json:"email_host"`
	EmailPort       int    `json:"email_port"`
	EmailUser       string `json:"email_user"`
	EmailPassword   string `json:"email_password"`
	EmailFrom       string `json:"email_from"`
	TelegramEnabled bool   `json:"telegram_enabled"`
	TelegramBot     string `json:"telegram_bot"`
	TelegramChat    string `json:"telegram_chat"`
}

// GetConfig 获取邮件配置（仅读数据库 notification 设置，供管理后台展示/脱敏使用）
// 修复 SEC-ENCRYPT-01 (P1): email_password 入库前已 AES 加密, 读取时解密;
// 兼容旧明文数据(DecryptSecret 解密失败时原样返回)。
func (s *EmailService) GetConfig() (*EmailConfig, error) {
	var cfg notifyConfig
	if err := s.settingRepo.Get("notification", &cfg); err != nil {
		return &EmailConfig{
			Enabled: false, Host: "", Port: 587,
			User: "", Password: "", From: "",
		}, nil
	}
	masterKey := ""
	if s.cfg != nil {
		masterKey = s.cfg.AESMasterKey
	}
	return &EmailConfig{
		Enabled: cfg.EmailEnabled, Host: cfg.EmailHost, Port: cfg.EmailPort,
		User: cfg.EmailUser, Password: security.DecryptSecret(masterKey, cfg.EmailPassword), From: cfg.EmailFrom,
	}, nil
}

// effectiveConfig 返回实际生效的邮件配置: 优先数据库 notification 设置,
// 若数据库未启用或配置不完整, 回退到环境变量 SMTP_* (cfg)。
// 这样管理后台配置与历史环境变量配置都能驱动真实邮件发送。
func (s *EmailService) effectiveConfig() (*EmailConfig, error) {
	dbCfg, _ := s.GetConfig()
	return mergeConfig(dbCfg, s.cfg), nil
}

// mergeConfig 纯函数: 数据库配置优先, 缺失/未启用时回退环境变量配置 (便于单测)。
func mergeConfig(dbCfg *EmailConfig, cfg *config.Config) *EmailConfig {
	if dbCfg != nil && dbCfg.Enabled && dbCfg.Host != "" && dbCfg.User != "" && dbCfg.Password != "" {
		// 防御性兜底: 若 DB 中 EmailPort 为 0 (老数据或 UI 未填), 默认 587
		// 否则 SendMail 拼出 "host:0" 会导致连接失败
		if dbCfg.Port == 0 {
			dbCfg.Port = 587
		}
		return dbCfg
	}
	if cfg != nil && cfg.SMTPEnabled() {
		port := cfg.SMTPPort
		if port == 0 {
			port = 587
		}
		return &EmailConfig{
			Enabled:  true,
			Host:     cfg.SMTPHost,
			Port:     port,
			User:     cfg.SMTPUser,
			Password: cfg.SMTPPass,
			From:     cfg.SMTPFrom,
		}
	}
	return dbCfg
}

// SaveConfig 保存邮件配置
// 修复 SEC-ENCRYPT-01 (P1): email_password 入库前 AES 加密, 防止数据库拖库后 SMTP 凭据泄露。
func (s *EmailService) SaveConfig(cfg *EmailConfig) error {
	var existing notifyConfig
	_ = s.settingRepo.Get("notification", &existing)
	existing.EmailEnabled = cfg.Enabled
	existing.EmailHost = cfg.Host
	existing.EmailPort = cfg.Port
	existing.EmailUser = cfg.User
	masterKey := ""
	if s.cfg != nil {
		masterKey = s.cfg.AESMasterKey
	}
	existing.EmailPassword = security.EncryptSecret(masterKey, cfg.Password)
	existing.EmailFrom = cfg.From
	return s.settingRepo.Set("notification", existing)
}

// resolveFrom 解析发件人地址: 优先用 cfg.From(必须是合法邮箱), 否则回退到 cfg.User
// 对于 Mailtrap 等服务, cfg.User 是 "api" / "APIsmtp@mailtrap.io", 不是邮箱
// 所以必须正确设置 cfg.From 为已验证域名的邮箱地址
func resolveFrom(cfg *EmailConfig) (string, error) {
	from := cfg.From
	if from == "" || !strings.Contains(from, "@") {
		// cfg.User 可能是邮箱(Mailtrap 旧版) 或 用户名(新版)
		if strings.Contains(cfg.User, "@") {
			from = cfg.User
		}
	}
	if from == "" || !strings.Contains(from, "@") {
		return "", fmt.Errorf("发件人邮箱(email_from)未设置或不合法, 请在邮件配置中填写已验证域名的邮箱地址")
	}
	return from, nil
}

// SendMail 发送邮件
func (s *EmailService) SendMail(to []string, subject, body string) error {
	if len(to) == 0 {
		return fmt.Errorf("收件人列表为空")
	}
	cfg, err := s.effectiveConfig()
	if err != nil {
		return fmt.Errorf("获取邮件配置失败: %w", err)
	}
	if !cfg.Enabled {
		return fmt.Errorf("邮件功能未启用")
	}
	if cfg.Host == "" || cfg.User == "" || cfg.Password == "" {
		return fmt.Errorf("邮件配置不完整")
	}

	from, err := resolveFrom(cfg)
	if err != nil {
		return err
	}

	// RFC 2047: 中文主题必须 base64 编码, 否则部分邮箱服务器拒收/进垃圾箱
	msg := buildMessage(from, to, subject, body)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	auth := smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)
	if err := sendMailWithTLS(addr, auth, from, to, msg); err != nil {
		return err
	}
	log.Printf("[email] 发送成功 to=%v subject=%s", to, subject)
	return nil
}

// buildMessage 构建符合 RFC 2047/MIME 标准的邮件内容
// 注: b64enc 复用了同包 notification_service.go 中的 base64 编码函数
func buildMessage(from string, to []string, subject, body string) []byte {
	encodedSubject := b64enc(subject)
	return []byte(fmt.Sprintf(
		"From: Nexus-Panel <%s>\r\n"+
			"To: %s\r\n"+
			"Subject: =?UTF-8?B?%s?=\r\n"+
			"MIME-Version: 1.0\r\n"+
			"Content-Type: text/plain; charset=UTF-8\r\n"+
			"Content-Transfer-Encoding: quoted-printable\r\n"+
			"\r\n"+
			"%s",
		from, strings.Join(to, ","), encodedSubject, body,
	))
}

// TestConfig 测试邮件配置
// 修复: 原代码用 cfg.User 作为收件人和发件人, 但 Mailtrap 的 user 是 "api" 不是邮箱
// 改为用 from(已验证域名邮箱) 作为收件人, 测试邮件发送到发件人自己
func (s *EmailService) TestConfig(cfg *EmailConfig) error {
	if cfg.Host == "" || cfg.User == "" || cfg.Password == "" {
		return fmt.Errorf("请填写完整的 SMTP 配置")
	}

	from, err := resolveFrom(cfg)
	if err != nil {
		return err
	}

	subject := "Nexus-Panel 邮件配置测试"
	body := fmt.Sprintf("这是一封测试邮件，如果您收到此邮件，说明邮件配置正确。\n\n配置信息：\n- SMTP 服务器: %s\n- 端口: %d\n- 发件人: %s",
		cfg.Host, cfg.Port, from)

	// 修复: 使用 buildMessage 构建符合 RFC 2047 标准的邮件内容
	msg := buildMessage(from, []string{from}, subject, body)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	auth := smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)

	// 收件人 = from (发件人自己), 确保 cfg.From 是一个能收信的真实邮箱
	if err := sendMailWithTLS(addr, auth, from, []string{from}, msg); err != nil {
		return err
	}
	log.Printf("[email] 测试邮件发送成功 to=%s", from)
	return nil
}

// sendMailWithTLS 发送邮件:
//   - 端口 465: 直接隐式 TLS (SMTPS) 连接
//   - 其他端口: 先建明文连接, 服务器支持 STARTTLS 时升级 (兼容 Mailtrap 等现代 SMTP 服务)
func sendMailWithTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	host, portStr, _ := net.SplitHostPort(addr)
	implicitTLS := portStr == "465"

	// 建立初始连接
	var conn net.Conn
	var err error
	if implicitTLS {
		tlsDialer := &tls.Dialer{
			NetDialer: &net.Dialer{Timeout: 10 * time.Second},
			Config:    &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
		}
		conn, err = tlsDialer.Dial("tcp", addr)
		if err != nil {
			return fmt.Errorf("TLS 连接失败: %w", err)
		}
	} else {
		conn, err = net.DialTimeout("tcp", addr, 10*time.Second)
		if err != nil {
			return fmt.Errorf("连接失败: %w", err)
		}
	}
	defer conn.Close()

	// 创建 SMTP 客户端
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("创建 SMTP 客户端失败: %w", err)
	}
	defer client.Close()

	// 非隐式 TLS 时尝试 STARTTLS 升级
	if !implicitTLS {
		ok, _ := client.Extension("STARTTLS")
		if ok {
			config := &tls.Config{
				ServerName:         host,
				InsecureSkipVerify: false,
				MinVersion:         tls.VersionTLS12,
			}
			if err := client.StartTLS(config); err != nil {
				return fmt.Errorf("STARTTLS 失败: %w", err)
			}
		}
	}

	// 认证
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("认证失败: %w", err)
	}

	// 设置发件人
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("设置发件人失败: %w", err)
	}

	// 设置收件人
	for _, addr := range to {
		if err := client.Rcpt(addr); err != nil {
			return fmt.Errorf("设置收件人 %s 失败: %w", addr, err)
		}
	}

	// 发送邮件内容
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("准备发送邮件内容失败: %w", err)
	}

	if _, err := w.Write(msg); err != nil {
		w.Close()
		return fmt.Errorf("写入邮件内容失败: %w", err)
	}

	// 必须先 Close 发送 DATA 结束标记(.), 服务器才会返回 250
	// 再调用 Quit 正常关闭连接
	if err := w.Close(); err != nil {
		return fmt.Errorf("关闭 DATA 写入失败: %w", err)
	}

	return client.Quit()
}
