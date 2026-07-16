package service

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"nexus-panel/internal/repo"
)

// EmailService 邮件服务
type EmailService struct {
	settingRepo *repo.SettingRepo
}

// NewEmailService 创建邮件服务
func NewEmailService(sr *repo.SettingRepo) *EmailService {
	return &EmailService{settingRepo: sr}
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

// GetConfig 获取邮件配置
func (s *EmailService) GetConfig() (*EmailConfig, error) {
	var cfg notifyConfig
	if err := s.settingRepo.Get("notification", &cfg); err != nil {
		return &EmailConfig{
			Enabled: false, Host: "", Port: 587,
			User: "", Password: "", From: "",
		}, nil
	}
	return &EmailConfig{
		Enabled: cfg.EmailEnabled, Host: cfg.EmailHost, Port: cfg.EmailPort,
		User: cfg.EmailUser, Password: cfg.EmailPassword, From: cfg.EmailFrom,
	}, nil
}

// SaveConfig 保存邮件配置
func (s *EmailService) SaveConfig(cfg *EmailConfig) error {
	var existing notifyConfig
	_ = s.settingRepo.Get("notification", &existing)
	existing.EmailEnabled = cfg.Enabled
	existing.EmailHost = cfg.Host
	existing.EmailPort = cfg.Port
	existing.EmailUser = cfg.User
	existing.EmailPassword = cfg.Password
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
	cfg, err := s.GetConfig()
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

	msg := []byte(fmt.Sprintf("From: Nexus-Panel <%s>\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		from, to[0], subject, body))

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	auth := smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)
	return sendMailWithTLS(addr, auth, from, to, msg)
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

	msg := []byte(fmt.Sprintf("From: Nexus-Panel <%s>\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		from, from, subject, body))

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	auth := smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)

	// 收件人 = from (发件人自己), 确保 cfg.From 是一个能收信的真实邮箱
	return sendMailWithTLS(addr, auth, from, []string{from}, msg)
}

// sendMailWithTLS 使用 TLS 发送邮件（兼容 Mailtrap 等需要 TLS 的 SMTP 服务）
func sendMailWithTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	host, _, _ := net.SplitHostPort(addr)

	// 建立初始连接
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("连接失败: %w", err)
	}
	defer conn.Close()

	// 创建 SMTP 客户端
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("创建 SMTP 客户端失败: %w", err)
	}
	defer client.Close()

	// 尝试 STARTTLS
	if ok, _ := client.Extension("STARTTLS"); ok {
		config := &tls.Config{
			ServerName:         host,
			InsecureSkipVerify: false,
		}
		if err := client.StartTLS(config); err != nil {
			return fmt.Errorf("STARTTLS 失败: %w", err)
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
