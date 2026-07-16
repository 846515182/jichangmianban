package config

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	DBHost string
	DBPort string
	DBUser string
	DBPass string
	DBName string

	RedisAddr string
	RedisPass string

	JWTSecret     string
	AESMasterKey  string
	HMACSubSecret string

	JWTAccessTTL  time.Duration
	JWTRefreshTTL time.Duration

	HTTPListen string
	GRPCListen string

	PanelDomain string

	RateUser  int
	RateAdmin int
	RateSub   int

	LoginMaxFail    int
	LoginLockWindow time.Duration

	SubSigTTL time.Duration

	// [P0-3 2026-07-17] 订阅签名是否绑定客户端 IP
	// true:  签名包含客户端 IP, IP 变化则签名失效(更安全, 但用户换网络需重新获取)
	// false: 仅按时间校验(更友好, 但链接泄漏 60s 内可能被滥用)
	SubSigBindIP bool

	IPBanTTL time.Duration

	GRPCTLSCert string
	GRPCTLSKey  string
	GRPCTLSCA   string
	// GRPCAllowPlaintext 仅在未配置 TLS 证书时, 显式允许以明文模式启动 gRPC
	// (仅限开发/内网环境)。默认 false: 未配置 TLS 证书则拒绝启动, 避免
	// node_token 等机密在公网明文传输。由环境变量 GRPC_ALLOW_PLAINTEXT 控制。
	GRPCAllowPlaintext bool

	HTTPSListen string
	TLSCert     string
	TLSKey      string

	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPass     string
	SMTPFrom     string
	SMTPFromName string

	TelegramBotToken string
	TelegramChatID   string

	// [S9 fix 2026-07-14] 注册是否强制要求邀请码 (默认 false, 由 INVITE_CODE_REQUIRED 控制)
	InviteCodeRequired bool
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		DBHost:          getEnv("DB_HOST", "127.0.0.1"),
		DBPort:          getEnv("DB_PORT", "5432"),
		DBUser:          getEnv("DB_USER", "nexus"),
		DBPass:          getEnv("DB_PASS", "nexus"),
		DBName:          getEnv("DB_NAME", "nexus_panel"),
		RedisAddr:       getEnv("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPass:       getEnv("REDIS_PASS", ""),
		JWTSecret:       getEnv("JWT_SECRET", ""),
		AESMasterKey:    getEnv("AES_MASTER_KEY", ""),
		HMACSubSecret:   getEnv("HMAC_SUB_SECRET", ""),
		HTTPListen:      getEnv("HTTP_LISTEN", ":8080"),
		GRPCListen:      getEnv("GRPC_LISTEN", ":9090"),
		PanelDomain:     getEnv("PANEL_DOMAIN", ""),
		RateUser:        getEnvInt("RATE_USER", 60),
		RateAdmin:       getEnvInt("RATE_ADMIN", 30),
		RateSub:         getEnvInt("RATE_SUB", 10),
		LoginMaxFail:    getEnvInt("LOGIN_MAX_FAIL", 10),
		LoginLockWindow: getEnvDuration("LOGIN_LOCK_WINDOW", 15*time.Minute),
		SubSigTTL:       getEnvDuration("SUB_SIG_TTL", 60*time.Second), // [P0-3 2026-07-17] 默认 60s 缩短攻击窗口
		SubSigBindIP:    getEnvBool("SUB_SIG_BIND_IP", false),            // 默认不绑定, 兼容性优先; 高安全场景可开
		IPBanTTL:        getEnvDuration("IP_BAN_TTL", time.Hour),
		GRPCTLSCert:       getEnv("GRPC_TLS_CERT", ""),
		GRPCTLSKey:        getEnv("GRPC_TLS_KEY", ""),
		GRPCTLSCA:         getEnv("GRPC_TLS_CA", ""),
		GRPCAllowPlaintext: getEnvBool("GRPC_ALLOW_PLAINTEXT", false),
		SMTPHost:        getEnv("SMTP_HOST", ""),
		SMTPPort:        getEnvInt("SMTP_PORT", 587),
		SMTPUser:        getEnv("SMTP_USER", ""),
		SMTPPass:        getEnv("SMTP_PASS", ""),
		SMTPFrom:        getEnv("SMTP_FROM", ""),
		HTTPSListen:     getEnv("HTTPS_LISTEN", ":443"),
		TLSCert:         getEnv("TLS_CERT", ""),
		TLSKey:          getEnv("TLS_KEY", ""),
		SMTPFromName:    getEnv("SMTP_FROM_NAME", "Nexus-Panel"),
		TelegramBotToken: getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramChatID:   getEnv("TELEGRAM_CHAT_ID", ""),
		InviteCodeRequired: getEnvBool("INVITE_CODE_REQUIRED", false),
	}

	cfg.JWTAccessTTL = getEnvDuration("JWT_ACCESS_TTL", 24*time.Hour)
	cfg.JWTRefreshTTL = getEnvDuration("JWT_REFRESH_TTL", 7*24*time.Hour)

	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("环境变量 JWT_SECRET 未设置")
	}
	if cfg.AESMasterKey == "" {
		return nil, fmt.Errorf("环境变量 AES_MASTER_KEY 未设置")
	}
	if cfg.HMACSubSecret == "" {
		return nil, fmt.Errorf("环境变量 HMAC_SUB_SECRET 未设置")
	}
	// 修复 F-06: 密钥强度校验, 防止弱密钥导致签名可被暴力破解
	// JWTSecret / HMACSubSecret 均要求 >= 32 字节(256 bit), 与 HS256 安全强度对齐
	if len(cfg.JWTSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET 长度不足: 当前 %d 字节, 要求 >= 32 字节(使用 openssl rand -hex 32 生成)", len(cfg.JWTSecret))
	}
	if len(cfg.HMACSubSecret) < 32 {
		return nil, fmt.Errorf("HMAC_SUB_SECRET 长度不足: 当前 %d 字节, 要求 >= 32 字节(使用 openssl rand -hex 32 生成)", len(cfg.HMACSubSecret))
	}
	if decoded, err := base64.StdEncoding.DecodeString(cfg.AESMasterKey); err == nil && len(decoded) == 32 {
		cfg.AESMasterKey = string(decoded)
	} else if len(cfg.AESMasterKey) != 32 {
		return nil, fmt.Errorf("AES_MASTER_KEY 必须为 32 字节(使用 openssl rand -base64 32 生成)")
	}

	return cfg, nil
}

func (c *Config) DSN() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable TimeZone=UTC",
		c.DBHost, c.DBPort, c.DBUser, c.DBPass, c.DBName)
}

func (c *Config) GRPCTLSEnabled() bool {
	return c.GRPCTLSCert != "" && c.GRPCTLSKey != ""
}

func (c *Config) LoadGRPCTLSConfig() (*tls.Config, error) {
	if !c.GRPCTLSEnabled() {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(c.GRPCTLSCert, c.GRPCTLSKey)
	if err != nil {
		return nil, fmt.Errorf("加载 gRPC TLS 证书失败: %w", err)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	if c.GRPCTLSCA != "" {
		// mTLS mode - would need client CA verification
		// For now, just server-side TLS
	}
	return tlsCfg, nil
}

func (c *Config) SMTPEnabled() bool {
	return c.SMTPHost != "" && c.SMTPUser != ""
}

func (c *Config) TelegramEnabled() bool {
	return c.TelegramBotToken != "" && c.TelegramChatID != ""
}

func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
