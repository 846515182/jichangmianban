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
	cfg    *config.Config
	logger *zap.Logger
}

func NewNotificationService(cfg *config.Config, logger *zap.Logger) *NotificationService {
	return &NotificationService{cfg: cfg, logger: logger}
}

type EmailPayload struct {
	To      string
	Subject string
	Body    string
}

func (s *NotificationService) SendEmail(payload EmailPayload) error {
	if !s.cfg.SMTPEnabled() {
		return nil
	}
	addr := fmt.Sprintf("%s:%d", s.cfg.SMTPHost, s.cfg.SMTPPort)
	from := s.cfg.SMTPFrom
	if s.cfg.SMTPFromName != "" {
		from = fmt.Sprintf("%s <%s>", s.cfg.SMTPFromName, s.cfg.SMTPFrom)
	}
	msg := fmt.Sprintf("From: %s\r\n"+
		"To: %s\r\n"+
		"Subject: =?UTF-8?B?%s?=\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: text/html; charset=UTF-8\r\n"+
		"\r\n"+
		"%s", from, payload.To, base64Encode(payload.Subject), payload.Body)

	auth := smtp.PlainAuth("", s.cfg.SMTPUser, s.cfg.SMTPPass, s.cfg.SMTPHost)
	if err := smtp.SendMail(addr, auth, s.cfg.SMTPFrom, []string{payload.To}, []byte(msg)); err != nil {
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

	if s.cfg.SMTPEnabled() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.SendEmail(EmailPayload{
				To:      s.cfg.SMTPFrom,
				Subject: subject,
				Body:    message,
			})
		}()
	}

	if s.cfg.TelegramEnabled() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.SendTelegram(TelegramPayload{
				Text: fmt.Sprintf("<b>%s</b>\n\n%s", subject, message),
			})
		}()
	}

	wg.Wait()
}

func (s *NotificationService) IsEnabled() bool {
	return s.cfg.SMTPEnabled() || s.cfg.TelegramEnabled()
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
