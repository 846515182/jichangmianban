package config

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	DBHost    string
	DBPort    string
	DBUser    string
	DBPass    string
	DBName    string
	DBSSlMode string

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

	IPBanTTL time.Duration

	GRPCTLSCert string
	GRPCTLSKey  string
	GRPCTLSCA   string

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

	// P1-known_hosts: SSH known_hosts 持久化路径, 默认指向挂载卷,
	// 避免容器重启后 TOFU 指纹丢失导致节点重新部署时被拒绝。
	SSHKnownHostsPath string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		DBHost:           getEnv("DB_HOST", "127.0.0.1"),
		DBPort:           getEnv("DB_PORT", "5432"),
		DBUser:           getEnv("DB_USER", "nexus"),
		DBPass:           getEnv("DB_PASSWORD", ""),
		DBName:           getEnv("DB_NAME", "nexus_panel"),
		DBSSlMode:        getEnv("DB_SSLMODE", "disable"),
		RedisAddr:        getEnv("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPass:        getEnv("REDIS_PASSWORD", ""),
		JWTSecret:        getEnv("JWT_SECRET", ""),
		AESMasterKey:     getEnv("AES_MASTER_KEY", ""),
		HMACSubSecret:    getEnv("HMAC_SUB_SECRET", ""),
		HTTPListen:       getEnv("HTTP_LISTEN", ":8080"),
		GRPCListen:       getEnv("GRPC_LISTEN", ":9090"),
		PanelDomain:      getEnv("PANEL_DOMAIN", ""),
		RateUser:         getEnvInt("RATE_USER", 60),
		RateAdmin:        getEnvInt("RATE_ADMIN", 30),
		RateSub:          getEnvInt("RATE_SUB", 10),
		LoginMaxFail:     getEnvInt("LOGIN_MAX_FAIL", 10),
		LoginLockWindow:  getEnvDuration("LOGIN_LOCK_WINDOW", 15*time.Minute),
		SubSigTTL:        getEnvDuration("SUB_SIG_TTL", 24*time.Hour),
		IPBanTTL:         getEnvDuration("IP_BAN_TTL", time.Hour),
		GRPCTLSCert:      getEnv("GRPC_TLS_CERT", ""),
		GRPCTLSKey:       getEnv("GRPC_TLS_KEY", ""),
		GRPCTLSCA:        getEnv("GRPC_TLS_CA", ""),
		SMTPHost:         getEnv("SMTP_HOST", ""),
		SMTPPort:         getEnvInt("SMTP_PORT", 587),
		SMTPUser:         getEnv("SMTP_USER", ""),
		SMTPPass:         getEnv("SMTP_PASS", ""),
		SMTPFrom:         getEnv("SMTP_FROM", ""),
		HTTPSListen:      getEnv("HTTPS_LISTEN", ":443"),
		TLSCert:          getEnv("TLS_CERT", ""),
		TLSKey:           getEnv("TLS_KEY", ""),
		SMTPFromName:     getEnv("SMTP_FROM_NAME", "Nexus-Panel"),
		TelegramBotToken: getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramChatID:   getEnv("TELEGRAM_CHAT_ID", ""),
		SSHKnownHostsPath: getEnv("SSH_KNOWN_HOSTS_PATH", "/app/data/ssh_known_hosts"),
	}

	cfg.JWTAccessTTL = getEnvDuration("JWT_ACCESS_TTL", 24*time.Hour)
	cfg.JWTRefreshTTL = getEnvDuration("JWT_REFRESH_TTL", 7*24*time.Hour)

	if cfg.DBPass == "" {
		return nil, fmt.Errorf("环境变量 DB_PASSWORD 未设置, 请在 .env 中配置 PostgreSQL 密码")
	}
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

	// 兜底: 如果 CA 证书文件不存在但同目录下 ca.key 存在,
	// 则自动从 ca.key 生成自签名 CA 证书.
	// 场景: 一键部署时, gRPC TLS 需要将 CA 证书推送到节点 agent,
	// 但 deployments/tls/ 只有 ca.key 没有 ca.crt 会导致部署崩溃.
	ensureCACert(cfg)

	return cfg, nil
}

func (c *Config) DSN() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s TimeZone=UTC",
		c.DBHost, c.DBPort, c.DBUser, c.DBPass, c.DBName, c.DBSSlMode)
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
		// mTLS: 加载客户�?CA 并强制校验客户端证书(仅当配置�?CA 时启�?
		// 节点须持有由�?CA 签发的有效客户端证书方可连接, 防止未授权节点接�?
		caPEM, err := os.ReadFile(c.GRPCTLSCA)
		if err != nil {
			return nil, fmt.Errorf("加载 gRPC 客户�?CA 失败: %w", err)
		}
		clientCAs := x509.NewCertPool()
		if !clientCAs.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("gRPC 客户�?CA 证书解析失败")
		}
		tlsCfg.ClientCAs = clientCAs
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return tlsCfg, nil
}

func (c *Config) SMTPEnabled() bool {
	return c.SMTPHost != "" && c.SMTPUser != "" && c.SMTPPass != ""
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

// ensureCACert �?GRPC_TLS_CA 指向的文件不存在但同目录�?ca.key 存在,
// 则用 Go 标准库从 ca.key 自动生成自签�?CA 证书(不依�?openssl)�?
// 解决面板部署节点�?os.ReadFile(ca.crt) 失败的崩溃问题�?
func ensureCACert(cfg *Config) {
	if cfg.GRPCTLSCA == "" {
		return
	}
	if _, err := os.Stat(cfg.GRPCTLSCA); err == nil {
		return // ca.crt 已存�?
	}

	// 推导 CA 私钥路径: ca.crt �?ca.key (同目�?
	ext := filepath.Ext(cfg.GRPCTLSCA)
	caKey := cfg.GRPCTLSCA[:len(cfg.GRPCTLSCA)-len(ext)] + ".key"
	keyData, err := os.ReadFile(caKey)
	if err != nil {
		return // �?CA 私钥可生�? 不阻断启�?可能使用公信 CA)
	}

	privKey, err := parsePrivateKey(keyData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[config] 解析 CA 私钥失败(%s): %v\n", caKey, err)
		return
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[config] 生成 CA 序列号失�? %v\n", err)
		return
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "Nexus-Panel-Root-CA",
		},
		NotBefore:             time.Now(),
		// P1-config: CA 证书有效期从 10 年缩短到 2 年。10 年期过长, 私钥泄露后无法
		// 在合理周期内轮换; 2 年兼顾运维成本与安全, 配合监控告警提前续期。
		NotAfter:              time.Now().Add(2 * 365 * 24 * time.Hour), // 2 年
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	if _, ok := privKey.(*ecdsa.PrivateKey); ok {
		// ECDSA CA: 签名用证书私钥即�? 无需额外限制
		_ = ok
	}

	certDER, err := x509.CreateCertificate(
		rand.Reader, template, template,
		publicKeyFor(privKey), privKey,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[config] 生成 CA 证书失败: %v\n", err)
		return
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(cfg.GRPCTLSCA, certPEM, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "[config] 写入 CA 证书失败(%s): %v\n", cfg.GRPCTLSCA, err)
		return
	}
	fmt.Fprintf(os.Stderr, "[config] 已自动生�?CA 证书: %s (RSA CA from %s)\n", cfg.GRPCTLSCA, caKey)
}

// parsePrivateKey 解析 PEM 编码的私�? 支持 PKCS#1/PKCS#8/EC 格式
func parsePrivateKey(der []byte) (crypto.PrivateKey, error) {
	block, _ := pem.Decode(der)
	if block == nil {
		return nil, fmt.Errorf("不是有效�?PEM 数据")
	}
	// 尝试各种格式
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("不支持的私钥格式")
}

// publicKeyFor 从私钥提取对应的公钥
func publicKeyFor(priv crypto.PrivateKey) crypto.PublicKey {
	switch k := priv.(type) {
	case *rsa.PrivateKey:
		return &k.PublicKey
	case *ecdsa.PrivateKey:
		return &k.PublicKey
	case ed25519.PrivateKey:
		return k.Public()
	case *ed25519.PrivateKey:
		return k.Public()
	default:
		return nil
	}
}
