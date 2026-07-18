package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"sync"
	"time"

	"go.uber.org/zap"

	"nexus-panel/internal/config"
)

type NotificationService struct {
	cfg         *config.Config
	logger      *zap.Logger
	emailSvc    *EmailService
}

func NewNotificationService(cfg *config.Config, logger *zap.Logger) *NotificationService {
	return &NotificationService{cfg: cfg, logger: logger}
}

// SetEmailService 注入 EmailService, 使通知服务复用 DB 配置(管理后台配的 SMTP)
// 修复 NOTIFY-CONFIG-01 (P1): 旧版 NotificationService 只读环境变量 SMTP_*,
// 管理员在 UI 配的邮件对 cron 磁盘告警无效。注入后 SendEmail 优先用 DB 配置,
// DB 未启用/不完整时回退环境变量(保持向后兼容)。
func (s *NotificationService) SetEmailService(e *EmailService) {
	s.emailSvc = e
}

type EmailPayload struct {
	To      string
	Subject string
	Body    string
}

// SendEmail 发送通知邮件。
// 修复 NOTIFY-CONFIG-01 (P1): 优先用 EmailService(DB 配置优先 + 环境变量回退),
// 统一邮件发送链路。EmailService 注入前回退到旧逻辑(仅环境变量, 兼容启动顺序)。
func (s *NotificationService) SendEmail(payload EmailPayload) error {
	// 优先走 EmailService(读 DB 配置 + 环境变量回退 + STARTTLS/465 支持)
	if s.emailSvc != nil {
		if err := s.emailSvc.SendMail([]string{payload.To}, payload.Subject, payload.Body); err != nil {
			s.logger.Error("通知邮件发送失败(DB配置)", zap.String("to", payload.To), zap.Error(err))
			return err
		}
		s.logger.Info("通知邮件发送成功(DB配置)", zap.String("to", payload.To))
		return nil
	}
	// 回退: EmailService 未注入(启动早期/cron 首次触发), 用环境变量
	// 使用 TLS-aware 发送 (兼容 Mailtrap 等需要 STARTTLS 的 SMTP 服务)
	if !s.cfg.SMTPEnabled() {
		return nil
	}
	addr := fmt.Sprintf("%s:%d", s.cfg.SMTPHost, s.cfg.SMTPPort)
	from := s.cfg.SMTPFrom
	fromName := s.cfg.SMTPFromName
	if fromName == "" {
		fromName = "Nexus-Panel"
	}
	fromHeader := fmt.Sprintf("%s <%s>", fromName, s.cfg.SMTPFrom)
	msg := fmt.Sprintf("From: %s\r\n"+
		"To: %s\r\n"+
		"Subject: =?UTF-8?B?%s?=\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: text/html; charset=UTF-8\r\n"+
		"\r\n"+
		"%s", fromHeader, payload.To, base64Encode(payload.Subject), payload.Body)

	auth := smtp.PlainAuth("", s.cfg.SMTPUser, s.cfg.SMTPPass, s.cfg.SMTPHost)
	// 使用 sendMailWithTLS 替代 smtp.SendMail，后者不支持 STARTTLS
	if err := sendMailWithTLS(addr, auth, s.cfg.SMTPFrom, []string{payload.To}, []byte(msg)); err != nil {
		s.logger.Error("邮件发送失败", zap.String("to", payload.To), zap.Error(err))
		return err
	}
	s.logger.Info("邮件发送成功", zap.String("to", payload.To))
	return nil
}

type TelegramPayload struct {
	Text string
}

func (s *NotificationService) SendTelegram(payload TelegramPayload) error {
	if !s.cfg.TelegramEnabled() {
		return nil
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", s.cfg.TelegramBotToken)
	body := map[string]interface{}{
		"chat_id":    s.cfg.TelegramChatID,
		"text":       payload.Text,
		"parse_mode": "HTML",
	}
	jsonBody, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		s.logger.Error("Telegram 发送失败", zap.Error(err))
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		s.logger.Error("Telegram API 错误", zap.Int("status", resp.StatusCode), zap.String("body", string(respBody)))
		return fmt.Errorf("Telegram API 返回 %d", resp.StatusCode)
	}
	s.logger.Info("Telegram 发送成功")
	return nil
}

func (s *NotificationService) NotifyAll(subject, message string) {
	var wg sync.WaitGroup

	// 修复 NOTIFY-CONFIG-01 (P1): 邮件收件人优先用 DB 配置的 email_from,
	// 回退到环境变量 SMTP_FROM; 都没有则跳过(避免发到空地址)
	if s.emailEnabled() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					s.logger.Error("SendEmail goroutine panic", zap.Any("panic", r))
				}
			}()
			_ = s.SendEmail(EmailPayload{
				To:      s.notifyRecipient(),
				Subject: subject,
				Body:    message,
			})
		}()
	}

	if s.cfg.TelegramEnabled() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					s.logger.Error("SendTelegram goroutine panic", zap.Any("panic", r))
				}
			}()
			_ = s.SendTelegram(TelegramPayload{
				Text: fmt.Sprintf("<b>%s</b>\n\n%s", subject, message),
			})
		}()
	}

	wg.Wait()
}

// emailEnabled 邮件是否启用(DB 配置或环境变量任一启用即 true)
func (s *NotificationService) emailEnabled() bool {
	if s.emailSvc != nil {
		if cfg, err := s.emailSvc.effectiveConfig(); err == nil && cfg != nil && cfg.Enabled {
			return true
		}
	}
	return s.cfg.SMTPEnabled()
}

// notifyRecipient 通知收件人(DB email_from 优先, 回退环境变量 SMTP_FROM)
func (s *NotificationService) notifyRecipient() string {
	if s.emailSvc != nil {
		if cfg, err := s.emailSvc.effectiveConfig(); err == nil && cfg != nil && cfg.Enabled {
			if cfg.From != "" {
				return cfg.From
			}
			if cfg.User != "" {
				return cfg.User
			}
		}
	}
	return s.cfg.SMTPFrom
}

func (s *NotificationService) IsEnabled() bool {
	return s.emailEnabled() || s.cfg.TelegramEnabled()
}

func base64Encode(s string) string {
	return b64enc(s)
}

func b64enc(input string) string {
	const b64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var result []byte
	src := []byte(input)
	for i := 0; i < len(src); i += 3 {
		b0 := src[i]
		result = append(result, b64[b0>>2])
		if i+1 >= len(src) {
			result = append(result, b64[(b0&0x03)<<4], '=', '=')
			break
		}
		b1 := src[i+1]
		result = append(result, b64[(b0&0x03)<<4|b1>>4])
		if i+2 >= len(src) {
			result = append(result, b64[(b1&0x0f)<<2], '=')
			break
		}
		b2 := src[i+2]
		result = append(result, b64[(b1&0x0f)<<2|b2>>6], b64[b2&0x3f])
	}
	return string(result)
}
