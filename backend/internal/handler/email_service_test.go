package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"nexus-panel/internal/model"
)

// ============================================================
// GenerateVerifyCode 单测
// ============================================================

func TestGenerateVerifyCode(t *testing.T) {
	code := GenerateVerifyCode()
	if len(code) != 6 {
		t.Fatalf("验证码长度应为 6, got %d (%q)", len(code), code)
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			t.Fatalf("验证码应全为数字, got %q", code)
		}
	}
	// 随机性: 生成 200 个, 不应全部相同 (理论概率极低)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		seen[GenerateVerifyCode()] = true
	}
	if len(seen) < 50 {
		t.Fatalf("验证码随机性不足, 200 次仅产生 %d 个不同值", len(seen))
	}
}

// ============================================================
// normalizeEmail 单测
// ============================================================

func TestNormalizeEmail(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Foo@BAR.com", "foo@bar.com"},
		{"  User@Example.com\n", "user@example.com"},
		{"\tMixed@Case.COM\t", "mixed@case.com"},
		{"already@lower.com", "already@lower.com"},
	}
	for _, c := range cases {
		got := normalizeEmail(c.in)
		if got != c.want {
			t.Errorf("normalizeEmail(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

// ============================================================
// hashCode 单测
// ============================================================

func TestHashCode(t *testing.T) {
	a := hashCode("123456")
	b := hashCode("123456")
	c := hashCode("654321")
	if a != b {
		t.Fatalf("相同输入应产生相同哈希: %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("不同输入不应产生相同哈希")
	}
	if len(a) != 64 { // sha256 hex = 64 字符
		t.Fatalf("哈希长度应为 64, got %d", len(a))
	}
}

// ============================================================
// maskedSettings (备份脱敏) 单测
// ============================================================

func TestMaskedSettings(t *testing.T) {
	in := []model.Setting{
		{Key: "epay_key", Value: []byte(`"super_secret_epay_key"`)},
		{Key: "hmac_sub_secret", Value: []byte(`"super_secret_hmac"`)},
		{Key: "notification", Value: []byte(`{"email_enabled":true,"email_password":"realpassword","email_host":"smtp.x.com"}`)},
		{Key: "sub_config", Value: []byte(`{"sub_prefix":"x"}`)},
	}
	out := maskedSettings(in)

	want := map[string]string{
		"epay_key":        `"****"`,
		"hmac_sub_secret": `"****"`,
		"sub_config":      `{"sub_prefix":"x"}`,
	}
	for i, s := range out {
		if exp, ok := want[s.Key]; ok {
			if string(s.Value) != exp {
				t.Errorf("key=%s 期望 %s, got %s", s.Key, exp, string(s.Value))
			}
		}
		// 原始不应被修改
		if string(in[i].Value) == string(s.Value) && (s.Key == "epay_key" || s.Key == "hmac_sub_secret") {
			t.Errorf("key=%s 应被脱敏但未改变", s.Key)
		}
	}

	// notification: email_password 被掩码, email_host 保留
	var notif map[string]interface{}
	for _, s := range out {
		if s.Key == "notification" {
			if err := json.Unmarshal(s.Value, &notif); err != nil {
				t.Fatalf("notification 反序列化失败: %v", err)
			}
			if notif["email_password"] != "****" {
				t.Errorf("email_password 应被掩码, got %v", notif["email_password"])
			}
			if notif["email_host"] != "smtp.x.com" {
				t.Errorf("email_host 应保留, got %v", notif["email_host"])
			}
			if notif["email_enabled"] != true {
				t.Errorf("email_enabled 应保留, got %v", notif["email_enabled"])
			}
		}
	}

	// 确保真实密钥不出现在输出中
	for _, s := range out {
		if strings.Contains(string(s.Value), "super_secret") || strings.Contains(string(s.Value), "realpassword") {
			t.Errorf("输出泄露敏感明文: key=%s", s.Key)
		}
	}
}
