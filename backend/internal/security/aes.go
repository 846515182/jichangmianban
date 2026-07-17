package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

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
// 返回 base64 密文; 若 masterKey 为空/无效或加密失败, 返回明文(降级, 不阻断业务)。
// 调用方应在存储前调用本函数, 读取时配对调用 DecryptSecret。
func EncryptSecret(masterKey, plaintext string) string {
	if plaintext == "" {
		return ""
	}
	m, err := NewAESManager(masterKey)
	if err != nil {
		return plaintext
	}
	enc, err := m.EncryptString(plaintext)
	if err != nil {
		return plaintext
	}
	return enc
}

// DecryptSecret 解密敏感字段, 兼容明文历史数据:
// 解密失败时认为旧数据是明文, 原样返回(平滑迁移, 不阻断业务)。
// 这样旧明文配置在升级后仍可读取, 下次 SaveConfig 时会被加密。
func DecryptSecret(masterKey, stored string) string {
	if stored == "" {
		return ""
	}
	m, err := NewAESManager(masterKey)
	if err != nil {
		return stored
	}
	plain, err := m.DecryptString(stored)
	if err != nil {
		return stored // 解密失败 = 旧明文数据, 原样返回
	}
	return plain
}
