package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"strings"
)

// P1-AES: 加密数据魔数前缀, 用于区分"已加密"与"历史明文"数据
// - 有前缀: 强制按密文处理, 解密失败返回原文(向后兼容, 调用方拿到的不可用值会自然失败)
// - 无前缀: 视为历史明文(平滑迁移), 返回原值并记录告警, 下次写入时会被加密带上前缀
const encPrefix = "enc:v1:"

// AESManager AES-256-GCM 加密/解密器，密钥固定 32 字节
type AESManager struct {
	key []byte
}

// NewAESManager 创建 AES 管理器，要求 32 字节密钥
func NewAESManager(key string) (*AESManager, error) {
	if len(key) != 32 {
		return nil, errors.New("AES 密钥必须为 32 字节(用于 AES-256-GCM)")
	}
	return &AESManager{key: []byte(key)}, nil
}

// Encrypt 加密明文，返回 base64 编码的密文(含 nonce)
func (m *AESManager) Encrypt(plaintext []byte) (string, error) {
	block, err := aes.NewCipher(m.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	// 密文 = nonce + ciphertext
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt 解密 base64 编码的密文
func (m *AESManager) Decrypt(encoded string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(m.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("密文长度不足")
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("AES-GCM 解密失败")
	}
	return plaintext, nil
}

// EncryptString 加密字符串
func (m *AESManager) EncryptString(s string) (string, error) {
	return m.Encrypt([]byte(s))
}

// DecryptString 解密为字符串
func (m *AESManager) DecryptString(encoded string) (string, error) {
	b, err := m.Decrypt(encoded)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// EncryptSecret 使用应用主密钥加密敏感字段(供 settings 表存储用)。
// 返回带 "enc:v1:" 前缀的 base64 密文, 与 DecryptSecret 配对使用。
//
// P1-AES (fail-closed): masterKey 为空/无效或加密失败时不再静默降级为明文存储,
// 改为 log.Fatal 终止进程 — AES_MASTER_KEY 在 config.Load 阶段已强制校验,
// 此处仅为防御性兜底, 避免运行期被清空后写入明文敏感数据。
// 注: 保持原签名 (返回 string) 不变, 以免改动调用方 (email_service/payment_service/admin_system)。
func EncryptSecret(masterKey, plaintext string) string {
	if plaintext == "" {
		return ""
	}
	if masterKey == "" {
		log.Fatalf("[security] EncryptSecret: master key not configured; refusing to store plaintext secret")
	}
	m, err := NewAESManager(masterKey)
	if err != nil {
		log.Fatalf("[security] EncryptSecret: AES manager init failed: %v", err)
	}
	enc, err := m.EncryptString(plaintext)
	if err != nil {
		log.Fatalf("[security] EncryptSecret: AES encrypt failed: %v", err)
	}
	return encPrefix + enc
}

// DecryptSecret 解密敏感字段, 兼容历史数据:
//   - 有 "enc:v1:" 前缀: 强制按密文解密, 失败返回原文(向后兼容; 调用方拿到不可用值会自然失败, 等效 fail-closed)
//   - 无前缀: 视为历史明文(平滑迁移), 返回原值并记录告警, 下次 SaveConfig 时会被重新加密带上前缀
//   - 同时对无前缀数据尝试按旧格式 base64 解密, 兼容旧版 EncryptSecret 写入的密文(未带前缀的历史密文)
//
// 注: 保持原签名 (返回 string) 不变, 调用方无法区分"明文"与"密文" — 因此 fail-closed 行为靠
// "前缀+解密失败返回密文本身"实现 (拿到的值形如 "enc:v1:xxx" 不可用, 业务自然失败, 等同 closed)。
func DecryptSecret(masterKey, stored string) string {
	if stored == "" {
		return ""
	}
	// 有前缀: 严格按密文处理
	if strings.HasPrefix(stored, encPrefix) {
		payload := strings.TrimPrefix(stored, encPrefix)
		m, err := NewAESManager(masterKey)
		if err != nil {
			log.Printf("[security] DecryptSecret: AES manager init failed for prefixed data: %v", err)
			return stored // 返回原文(向后兼容)
		}
		plain, err := m.DecryptString(payload)
		if err != nil {
			log.Printf("[security] DecryptSecret: AES decrypt failed for prefixed data (returning raw, credential unusable): %v", err)
			return stored // 解密失败返回原文(向后兼容; 调用方拿到的密文不可用, 业务自然失败)
		}
		return plain
	}
	// 无前缀: 兼容旧版 EncryptSecret 写入的 base64 密文(历史密文, 未带前缀)
	// 先尝试按旧格式解密; 成功则返回明文, 失败再视为旧明文
	if masterKey != "" {
		if m, err := NewAESManager(masterKey); err == nil {
			if plain, err := m.DecryptString(stored); err == nil {
				return plain
			}
		}
	}
	// 旧明文数据, 返回原值并告警(一次性迁移, 下次写入会被加密带上前缀)
	log.Printf("[security] DecryptSecret: legacy plaintext data without encPrefix, returning as-is (one-time migration)")
	return stored
}
