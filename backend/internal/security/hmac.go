package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// HMACManager HMAC-SHA256 签名器，用于订阅链接签名
// 签名原文 = sub_token + user_id + exp
type HMACManager struct {
	secret []byte
}

// NewHMACManager 创建 HMAC 管理器
func NewHMACManager(secret string) *HMACManager {
	return &HMACManager{secret: []byte(secret)}
}

// Sign 生成签名，签名原文 = sub_token + user_id + exp(unix 秒)
// 返回 hex 编码的签名
func (m *HMACManager) Sign(subToken, userID string, exp int64) string {
	raw := subToken + "|" + userID + "|" + strconv.FormatInt(exp, 10)
	h := hmac.New(sha256.New, m.secret)
	h.Write([]byte(raw))
	return hex.EncodeToString(h.Sum(nil))
}

// SignWithTTL 生成签名并附带有效期，返回签名与过期时间
func (m *HMACManager) SignWithTTL(subToken, userID string, ttl time.Duration) (sig string, exp int64) {
	exp = time.Now().Add(ttl).Unix()
	sig = m.Sign(subToken, userID, exp)
	return
}

// Verify 校验签名，并检查是否在有效期内
// 失败原因：签名不匹配 / 已过期
func (m *HMACManager) Verify(subToken, userID, sig string, exp int64) error {
	// 先校验过期时间
	if time.Now().Unix() > exp {
		return errors.New("签名已过期")
	}
	expected := m.Sign(subToken, userID, exp)
	if !hmac.Equal([]byte(expected), []byte(strings.ToLower(sig))) {
		return errors.New("签名不匹配")
	}
	return nil
}

// VerifySigStr 校验字符串形式的 exp + sig
// sigStr 格式: exp.sig
func (m *HMACManager) VerifySigStr(subToken, userID, sigStr string) error {
	parts := strings.SplitN(sigStr, ".", 2)
	if len(parts) != 2 {
		return errors.New("签名格式错误")
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return errors.New("过期时间解析失败")
	}
	return m.Verify(subToken, userID, parts[1], exp)
}

// BuildSigStr 拼接签名串 exp.sig
func (m *HMACManager) BuildSigStr(exp int64, sig string) string {
	return strconv.FormatInt(exp, 10) + "." + sig
}

// SignWithTTLAndIP 生成带 IP 绑定的签名 - 修复 SEC-P0-02
// 签名原文 = sub_token + user_id + exp + client_ip
func (m *HMACManager) SignWithTTLAndIP(subToken, userID, clientIP string, ttl time.Duration) (sig string, exp int64) {
    exp = time.Now().Add(ttl).Unix()
    raw := subToken + "|" + userID + "|" + strconv.FormatInt(exp, 10) + "|" + clientIP
    h := hmac.New(sha256.New, m.secret)
    h.Write([]byte(raw))
    sig = hex.EncodeToString(h.Sum(nil))
    return
}

// VerifySigStrWithIP 校验带 IP 绑定的签名 - 修复 SEC-P0-02
// sigStr 格式: exp.sig
func (m *HMACManager) VerifySigStrWithIP(subToken, userID, sigStr, clientIP string) error {
    parts := strings.SplitN(sigStr, ".", 2)
    if len(parts) != 2 {
        return errors.New("签名格式错误")
    }
    exp, err := strconv.ParseInt(parts[0], 10, 64)
    if err != nil {
        return errors.New("过期时间解析失败")
    }
    if time.Now().Unix() > exp {
        return errors.New("签名已过期")
    }
    raw := subToken + "|" + userID + "|" + parts[0] + "|" + clientIP
    h := hmac.New(sha256.New, m.secret)
    h.Write([]byte(raw))
    expected := hex.EncodeToString(h.Sum(nil))
    if !hmac.Equal([]byte(expected), []byte(strings.ToLower(parts[1]))) {
        return errors.New("签名不匹配")
    }
    return nil
}
