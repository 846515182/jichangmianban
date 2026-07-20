package security

import (
	"testing"
	"time"
)

// === P0-17: HMAC nonce 防重放测试 ===

// TestSignWithNonce_Verify 验证带 nonce 的签名能正确校验
func TestSignWithNonce_Verify(t *testing.T) {
	mgr := NewHMACManager("test-secret")
	subToken := "tok123"
	userID := "user-1"
	exp := time.Now().Add(5 * time.Minute).Unix()
	nonce := "abc123"

	sig := mgr.SignWithNonce(subToken, userID, exp, nonce)
	sigStr := mgr.BuildSigStrWithNonce(exp, nonce, sig)

	// 正确校验应通过
	if err := mgr.VerifySigStr(subToken, userID, sigStr); err != nil {
		t.Fatalf("valid nonce sig should verify: %v", err)
	}
}

// TestSignWithNonce_TamperNonce 验证篡改 nonce 后签名失败
func TestSignWithNonce_TamperNonce(t *testing.T) {
	mgr := NewHMACManager("test-secret")
	subToken := "tok123"
	userID := "user-1"
	exp := time.Now().Add(5 * time.Minute).Unix()
	nonce := "abc123"

	sig := mgr.SignWithNonce(subToken, userID, exp, nonce)
	// 篡改 nonce
	tamperedSigStr := mgr.BuildSigStrWithNonce(exp, "xyz789", sig)

	if err := mgr.VerifySigStr(subToken, userID, tamperedSigStr); err == nil {
		t.Fatal("tampered nonce should fail verification")
	}
}

// TestSignWithNonce_ExtractNonce 验证从 sigStr 提取 nonce
func TestSignWithNonce_ExtractNonce(t *testing.T) {
	mgr := NewHMACManager("test-secret")

	// 新格式 exp.nonce.sig
	sigStr := "1234567890.abc123.deadbeef"
	nonce := mgr.ExtractNonce(sigStr)
	if nonce != "abc123" {
		t.Fatalf("expected nonce 'abc123', got '%s'", nonce)
	}

	// 旧格式 exp.sig (无 nonce)
	oldSigStr := "1234567890.deadbeef"
	nonce = mgr.ExtractNonce(oldSigStr)
	if nonce != "" {
		t.Fatalf("old format should return empty nonce, got '%s'", nonce)
	}
}

// TestVerifySigStr_OldFormat 验证旧格式 exp.sig 仍能校验(向后兼容)
func TestVerifySigStr_OldFormat(t *testing.T) {
	mgr := NewHMACManager("test-secret")
	subToken := "tok123"
	userID := "user-1"
	exp := time.Now().Add(5 * time.Minute).Unix()

	sig := mgr.Sign(subToken, userID, exp)
	sigStr := mgr.BuildSigStr(exp, sig)

	if err := mgr.VerifySigStr(subToken, userID, sigStr); err != nil {
		t.Fatalf("old format sig should verify: %v", err)
	}
}

// TestVerifySigStr_Expired 验证过期签名被拒绝
func TestVerifySigStr_Expired(t *testing.T) {
	mgr := NewHMACManager("test-secret")
	subToken := "tok123"
	userID := "user-1"
	// 已过期的 exp
	exp := time.Now().Add(-1 * time.Minute).Unix()
	nonce := "abc123"

	sig := mgr.SignWithNonce(subToken, userID, exp, nonce)
	sigStr := mgr.BuildSigStrWithNonce(exp, nonce, sig)

	if err := mgr.VerifySigStr(subToken, userID, sigStr); err == nil {
		t.Fatal("expired sig should fail")
	}
}

// TestVerifySigStr_WrongSecret 验证不同 secret 签名失败
func TestVerifySigStr_WrongSecret(t *testing.T) {
	signer := NewHMACManager("secret-a")
	verifier := NewHMACManager("secret-b")
	subToken := "tok123"
	userID := "user-1"
	exp := time.Now().Add(5 * time.Minute).Unix()
	nonce := "abc123"

	sig := signer.SignWithNonce(subToken, userID, exp, nonce)
	sigStr := signer.BuildSigStrWithNonce(exp, nonce, sig)

	if err := verifier.VerifySigStr(subToken, userID, sigStr); err == nil {
		t.Fatal("sig with wrong secret should fail")
	}
}

// TestSignWithNonce_DifferentNonces 验证同一 payload 不同 nonce 产生不同签名
func TestSignWithNonce_DifferentNonces(t *testing.T) {
	mgr := NewHMACManager("test-secret")
	subToken := "tok123"
	userID := "user-1"
	exp := time.Now().Add(5 * time.Minute).Unix()

	sig1 := mgr.SignWithNonce(subToken, userID, exp, "nonce-a")
	sig2 := mgr.SignWithNonce(subToken, userID, exp, "nonce-b")

	if sig1 == sig2 {
		t.Fatal("different nonces should produce different sigs")
	}
}

// TestVerifySigStr_InvalidFormat 验证非法格式被拒绝
func TestVerifySigStr_InvalidFormat(t *testing.T) {
	mgr := NewHMACManager("test-secret")

	// 单段
	if err := mgr.VerifySigStr("tok", "u", "onlyonepart"); err == nil {
		t.Fatal("single part should fail")
	}
	// 四段
	if err := mgr.VerifySigStr("tok", "u", "a.b.c.d"); err == nil {
		t.Fatal("four parts should fail")
	}
	// 空字符串
	if err := mgr.VerifySigStr("tok", "u", ""); err == nil {
		t.Fatal("empty string should fail")
	}
}
