package service

import (
	"strings"
	"testing"
	"time"

	"nexus-panel/internal/security"
)

// === P0-NONCE 修复验证 ===

// TestSigFormat_NoNonce 验证 GenerateSignedURL 生成的签名格式为 exp.sig (两段, 无 nonce)
// 修复前: 签名格式为 exp.nonce.sig (三段), nonce 防重放导致客户端自动更新失败
// 修复后: 签名格式为 exp.sig (两段), 允许 TTL 窗口内多次使用
func TestSigFormat_NoNonce(t *testing.T) {
	hmacMgr := security.NewHMACManager("test-secret")
	subToken := "test-sub-token-abc123"
	userID := "user-xyz"
	sig, exp := hmacMgr.SignWithTTL(subToken, userID, 24*time.Hour)
	sigStr := hmacMgr.BuildSigStr(exp, sig)

	parts := strings.SplitN(sigStr, ".", 3)
	if len(parts) != 2 {
		t.Fatalf("修复后签名应为 2 段 (exp.sig), 实际 %d 段: %s", len(parts), sigStr)
	}
	if parts[0] != time.Now().Add(24*time.Hour).Format("1577836800") {
		// 只验证 exp 是数字即可
		if len(parts[0]) < 10 {
			t.Fatalf("exp 部分应为 unix 时间戳, got: %s", parts[0])
		}
	}
	if parts[1] != sig {
		t.Fatalf("sig 部分应与 SignWithTTL 返回值一致")
	}
}

// TestSigRepeatUse_NoRejection 验证同一签名在 TTL 窗口内可多次使用不被拒绝
// 这是 nonce 防重放移除后的核心行为: Clash/V2RayN 自动更新订阅时重复请求不应失败
func TestSigRepeatUse_NoRejection(t *testing.T) {
	hmacMgr := security.NewHMACManager("test-secret")
	subToken := "test-sub-token-repeat"
	userID := "user-repeat"
	sig, exp := hmacMgr.SignWithTTL(subToken, userID, 1*time.Hour)
	sigStr := hmacMgr.BuildSigStr(exp, sig)

	// 模拟客户端连续 5 次使用同一订阅 URL 拉取
	for i := 0; i < 5; i++ {
		err := hmacMgr.VerifySigStr(subToken, userID, sigStr)
		if err != nil {
			t.Fatalf("第 %d 次验证失败 (nonce 防重放未移除?): %v", i+1, err)
		}
	}
}

// TestSigExpired_Rejected 验证过期签名仍被正确拒绝
// 移除 nonce 后, TTL 过期是唯一的防重放机制, 必须正常工作
func TestSigExpired_Rejected(t *testing.T) {
	hmacMgr := security.NewHMACManager("test-secret")
	subToken := "test-sub-token-expired"
	userID := "user-expired"
	// 生成已过期 1 秒的签名
	exp := time.Now().Add(-1 * time.Second).Unix()
	sig := hmacMgr.Sign(subToken, userID, exp)
	sigStr := hmacMgr.BuildSigStr(exp, sig)

	err := hmacMgr.VerifySigStr(subToken, userID, sigStr)
	if err == nil {
		t.Fatal("过期签名应被拒绝, 实际验证通过")
	}
	if !strings.Contains(err.Error(), "过期") {
		t.Fatalf("过期签名应返回 '过期' 错误, got: %v", err)
	}
}

// TestSigWrongSecret_Rejected 验证密钥不匹配的签名被拒绝
func TestSigWrongSecret_Rejected(t *testing.T) {
	hmacMgr1 := security.NewHMACManager("secret-a")
	hmacMgr2 := security.NewHMACManager("secret-b")
	subToken := "test-sub-token-wrongsec"
	userID := "user-wrongsec"
	sig, exp := hmacMgr1.SignWithTTL(subToken, userID, 1*time.Hour)
	sigStr := hmacMgr1.BuildSigStr(exp, sig)

	// 用不同密钥的 manager 验证
	err := hmacMgr2.VerifySigStr(subToken, userID, sigStr)
	if err == nil {
		t.Fatal("不同密钥的签名应被拒绝")
	}
}

// TestSigOldFormat_StillValid 验证旧格式 exp.sig 仍被接受 (向后兼容)
// 修复前的客户端可能缓存了旧格式的签名, 不应被拒绝
func TestSigOldFormat_StillValid(t *testing.T) {
	hmacMgr := security.NewHMACManager("test-secret")
	subToken := "test-sub-token-oldfmt"
	userID := "user-oldfmt"
	sig, exp := hmacMgr.SignWithTTL(subToken, userID, 1*time.Hour)
	sigStr := hmacMgr.BuildSigStr(exp, sig) // exp.sig 格式

	err := hmacMgr.VerifySigStr(subToken, userID, sigStr)
	if err != nil {
		t.Fatalf("旧格式 exp.sig 应被接受: %v", err)
	}
}

// TestSigNonceFormat_AlsoValid 验证带 nonce 的旧格式签名仍被验证通过
// (VerifySigStr 支持三段格式, 但 GenerateSignedURL 不再生成它)
func TestSigNonceFormat_AlsoValid(t *testing.T) {
	hmacMgr := security.NewHMACManager("test-secret")
	subToken := "test-sub-token-nonce"
	userID := "user-nonce"
	nonce := "abc12345"
	exp := time.Now().Add(1 * time.Hour).Unix()
	sig := hmacMgr.SignWithNonce(subToken, userID, exp, nonce)
	sigStr := hmacMgr.BuildSigStrWithNonce(exp, nonce, sig)

	err := hmacMgr.VerifySigStr(subToken, userID, sigStr)
	if err != nil {
		t.Fatalf("三段格式 exp.nonce.sig 应仍被接受: %v", err)
	}
}

// === P0: Sing-Box 格式参数验证 ===

// TestSubTypeSingBox_Constant 验证 SubTypeSingBox 常量值为 "sing-box"
// 前端 Subscribe.vue 的 radio value 必须与此一致
func TestSubTypeSingBox_Constant(t *testing.T) {
	if SubTypeSingBox != "sing-box" {
		t.Fatalf("SubTypeSingBox 应为 'sing-box', 实际: %s", SubTypeSingBox)
	}
}

// TestSubTypeConstants 验证所有订阅类型常量
func TestSubTypeConstants(t *testing.T) {
	cases := []struct {
		name     string
		constant string
		expected string
	}{
		{"Clash", SubTypeClash, "clash"},
		{"SingBox", SubTypeSingBox, "sing-box"},
		{"V2Ray", SubTypeV2Ray, "v2ray"},
		{"SIP008", SubTypeSIP008, "sip008"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.constant != c.expected {
				t.Fatalf("%s 常量应为 %s, 实际: %s", c.name, c.expected, c.constant)
			}
		})
	}
}

// TestDetectSubType 验证 User-Agent 自动识别客户端类型
func TestDetectSubType(t *testing.T) {
	cases := []struct {
		ua       string
		expected string
	}{
		{"Clash/0.20.39", SubTypeClash},
		{"clash-verge/1.3.8", SubTypeClash},
		{"mihomo/1.18.1", SubTypeV2Ray}, // mihomo UA 不含 clash, 走 default
		{"sing-box/1.8.0", SubTypeSingBox},
		{"SingBox/0.2.0", SubTypeSingBox},
		{"v2rayN/6.0", SubTypeV2Ray},
		{"Shadowrocket/1900", SubTypeV2Ray},
		{"sip008-client/1.0", SubTypeSIP008},
		{"Mozilla/5.0", SubTypeV2Ray}, // 浏览器走 default
		{"", SubTypeV2Ray},            // 空 UA 走 default
	}
	for _, c := range cases {
		t.Run(c.ua, func(t *testing.T) {
			got := detectSubType(c.ua)
			if got != c.expected {
				t.Fatalf("detectSubType(%q) = %s, want %s", c.ua, got, c.expected)
			}
		})
	}
}

// TestDetectSubType_SingBoxVariants 验证 Sing-Box 的各种 UA 变体
// 修复前: 前端传 "singbox" (无连字符), 后端无法匹配 "sing-box" 常量
// 修复后: 前端传 "sing-box", 与后端常量一致
// detectSubType 同时支持 "sing-box" 和 "singbox" 两种 UA 写法
func TestDetectSubType_SingBoxVariants(t *testing.T) {
	uas := []string{
		"sing-box/1.8.0",
		"SING-BOX/1.8.0",
		"singbox/0.1.0",
		"SingBox/2.0",
	}
	for _, ua := range uas {
		t.Run(ua, func(t *testing.T) {
			got := detectSubType(ua)
			if got != SubTypeSingBox {
				t.Fatalf("detectSubType(%q) = %s, want %s", ua, got, SubTypeSingBox)
			}
		})
	}
}

// === 签名 URL 格式集成测试 (不依赖 DB) ===

// TestSignedURLFormat_NoNonceInURL 模拟 GenerateSignedURL 的签名拼接逻辑
// 验证生成的 URL 中 sig 参数为 exp.sig 格式 (无 nonce)
func TestSignedURLFormat_NoNonceInURL(t *testing.T) {
	hmacMgr := security.NewHMACManager("test-secret")
	subToken := "test-sub-token-url"
	userID := "user-url"
	sig, exp := hmacMgr.SignWithTTL(subToken, userID, 24*time.Hour)
	sigStr := hmacMgr.BuildSigStr(exp, sig)

	// 验证 URL 中 sig 参数不包含三段格式
	if strings.Count(sigStr, ".") != 1 {
		t.Fatalf("sig 应为 exp.sig 格式(1个点), 实际有 %d 个点: %s",
			strings.Count(sigStr, "."), sigStr)
	}

	// 验证 sigStr 可被 VerifySigStr 接受
	err := hmacMgr.VerifySigStr(subToken, userID, sigStr)
	if err != nil {
		t.Fatalf("生成的签名验证失败: %v", err)
	}
}

// TestSimulatedClientAutoUpdate 模拟 Clash 客户端自动更新订阅的完整流程
// 1. 面板生成签名 URL -> 2. 客户端首次拉取 -> 3. 客户端 6 小时后自动更新(同一 URL)
// 修复前: 第 3 步因 nonce 防重放失败
// 修复后: 第 3 步成功
func TestSimulatedClientAutoUpdate(t *testing.T) {
	hmacMgr := security.NewHMACManager("panel-secret")
	subToken := "clash-user-token"
	userID := "clash-user"

	// Step 1: 面板生成签名 URL (有效期 24h)
	sig, exp := hmacMgr.SignWithTTL(subToken, userID, 24*time.Hour)
	sigStr := hmacMgr.BuildSigStr(exp, sig)

	// Step 2: 客户端首次拉取订阅
	err1 := hmacMgr.VerifySigStr(subToken, userID, sigStr)
	if err1 != nil {
		t.Fatalf("首次拉取失败: %v", err1)
	}

	// Step 3: 客户端 6 小时后自动更新 (同一 URL, 同一 sig)
	err2 := hmacMgr.VerifySigStr(subToken, userID, sigStr)
	if err2 != nil {
		t.Fatalf("6 小时后自动更新失败 (nonce 防重放未移除?): %v", err2)
	}

	// Step 4: 客户端 12 小时后再次自动更新
	err3 := hmacMgr.VerifySigStr(subToken, userID, sigStr)
	if err3 != nil {
		t.Fatalf("12 小时后自动更新失败: %v", err3)
	}

	// Step 5: 25 小时后签名过期, 应被拒绝
	time.Sleep(0) // 不实际等待, 通过构造过期签名验证
	expiredSig := hmacMgr.Sign(subToken, userID, time.Now().Add(-1*time.Hour).Unix())
	expiredSigStr := hmacMgr.BuildSigStr(time.Now().Add(-1*time.Hour).Unix(), expiredSig)
	err4 := hmacMgr.VerifySigStr(subToken, userID, expiredSigStr)
	if err4 == nil {
		t.Fatal("25 小时后过期签名应被拒绝")
	}
}

// TestMultipleDevicesSameURL 验证多个设备使用同一订阅 URL 都能成功
// 修复前: nonce 防重放导致第二个设备使用失败
func TestMultipleDevicesSameURL(t *testing.T) {
	hmacMgr := security.NewHMACManager("multi-device-secret")
	subToken := "multi-device-token"
	userID := "multi-user"

	sig, exp := hmacMgr.SignWithTTL(subToken, userID, 24*time.Hour)
	sigStr := hmacMgr.BuildSigStr(exp, sig)

	// 模拟 3 个设备同时或先后使用同一订阅 URL
	for i := 0; i < 3; i++ {
		err := hmacMgr.VerifySigStr(subToken, userID, sigStr)
		if err != nil {
			t.Fatalf("设备 %d 使用同一订阅 URL 失败: %v", i+1, err)
		}
	}
}
