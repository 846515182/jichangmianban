package service

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// epaySignRef 参考实现(按文档手写), 用于和线上 epaySign 交叉验证
func epaySignRef(params map[string]string, key string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" {
			continue
		}
		if v == "" || v == "0" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(params[k])
	}
	b.WriteString(key)
	sum := md5.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func TestEpaySign_FiltersEmptyAndZeroValues(t *testing.T) {
	key := "testkey123"
	params := map[string]string{
		"pid":          "1001",
		"type":         "alipay",
		"out_trade_no": "ORDER001",
		"notify_url":   "http://example.com/notify",
		"return_url":   "http://example.com/return",
		"name":         "VIP会员",
		"money":        "1.00",
		"param":        "", // 空值, 应被过滤
		"device":       "0", // "0"值, 应被过滤
		"sign":         "shouldbeignored",
		"sign_type":    "MD5",
	}

	got := epaySign(params, key)
	want := epaySignRef(params, key)

	if got != want {
		t.Fatalf("epaySign mismatch:\n got=%s\nwant=%s", got, want)
	}
	t.Logf("签名: %s", got)

	// 验证空值和"0"值确实被过滤: 手动构造不含空值/"0"值的签名
	cleanParams := map[string]string{
		"pid":          "1001",
		"type":         "alipay",
		"out_trade_no": "ORDER001",
		"notify_url":   "http://example.com/notify",
		"return_url":   "http://example.com/return",
		"name":         "VIP会员",
		"money":        "1.00",
	}
	cleanSign := epaySign(cleanParams, key)
	if got != cleanSign {
		t.Fatalf("签名包含空值/\"0\"值参数:\n got(含空值)=%s\n clean(无空值)=%s", got, cleanSign)
	}
	t.Log("空值和\"0\"值参数被正确过滤")
}

func TestEpaySign_ParameterOrder(t *testing.T) {
	key := "secret"
	// 参数顺序不影响签名 (因为内部排序)
	params1 := map[string]string{
		"z": "1",
		"a": "2",
		"m": "3",
	}
	params2 := map[string]string{
		"a": "2",
		"m": "3",
		"z": "1",
	}
	s1 := epaySign(params1, key)
	s2 := epaySign(params2, key)
	if s1 != s2 {
		t.Fatalf("参数顺序影响签名: s1=%s s2=%s", s1, s2)
	}
	t.Log("参数顺序不影响签名 (正确)")
}

func TestEpaySign_CallbackSimulation(t *testing.T) {
	// 模拟 EPay 回调参数 (含可能为空的 param 字段)
	key := "merchantKey123"
	callbackParams := map[string]string{
		"pid":          "1001",
		"trade_no":     "20230806151343349021",
		"out_trade_no": "ORDER001",
		"type":         "alipay",
		"name":         "VIP会员",
		"money":        "1.00",
		"trade_status": "TRADE_SUCCESS",
		"param":        "", // 回调中可能为空
		"sign":         "xxx",
		"sign_type":    "MD5",
	}

	// 用线上 epaySign 生成签名
	sign := epaySign(callbackParams, key)

	// 模拟验签: 传入完整参数(含 sign), 验证是否匹配
	callbackParams["sign"] = sign
	expectedSign := epaySign(callbackParams, key)
	if sign != expectedSign {
		t.Fatalf("回调验签失败: sign=%s expected=%s", sign, expectedSign)
	}
	t.Log("回调验签成功 (含空 param 字段)")
}

func TestPaymentMethodToEPayType_USDT(t *testing.T) {
	if got := paymentMethodToEPayType("epay_usdt"); got != "usdt" {
		t.Fatalf("expected 'usdt', got '%s'", got)
	}
	if got := paymentMethodToEPayType("epay_alipay"); got != "alipay" {
		t.Fatalf("expected 'alipay', got '%s'", got)
	}
	if got := paymentMethodToEPayType("epay_wechat"); got != "wxpay" {
		t.Fatalf("expected 'wxpay', got '%s'", got)
	}
	if got := paymentMethodToEPayType("unknown"); got != "" {
		t.Fatalf("expected empty for unknown method, got '%s'", got)
	}
	t.Log("所有支付方式映射正确 (alipay/wxpay/usdt)")
}

func TestEpaySign_ZeroMoneyExcluded(t *testing.T) {
	// 边界场景: money="0" 时应被过滤 (虽然实际不会发 0 元订单到 EPay)
	key := "testkey"
	params := map[string]string{
		"pid":   "1001",
		"money": "0", // 应被过滤
		"name":  "test",
	}
	sign := epaySign(params, key)

	// 不含 money 的签名
	withoutMoney := epaySign(map[string]string{
		"pid":  "1001",
		"name": "test",
	}, key)

	if sign != withoutMoney {
		t.Fatalf("money=\"0\" 未被过滤: sign=%s withoutMoney=%s", sign, withoutMoney)
	}
	t.Log("money=\"0\" 被正确过滤")
}

func TestEpaySign_DocExample(t *testing.T) {
	// 按文档示例验证签名格式
	// 文档: sign = md5(a=b&c=d&e=f + KEY)
	key := "89unJUB8HZ54Hj7x4nUj56HN4nUzUJ8i"
	params := map[string]string{
		"pid":    "1001",
		"name":   "VIP会员",
		"money":  "1.00",
		"type":   "alipay",
	}

	got := epaySign(params, key)

	// 手动计算期望值
	// 排序: money, name, pid, type
	// 拼接: money=1.00&name=VIP会员&pid=1001&type=alipay
	manual := "money=1.00&name=VIP会员&pid=1001&type=alipay" + key
	sum := md5.Sum([]byte(manual))
	want := hex.EncodeToString(sum[:])

	if got != want {
		t.Fatalf("签名与手动计算不一致:\n got=%s\nwant=%s\n str=%s", got, want, manual)
	}
	t.Logf("签名验证通过: %s", got)
	// 输出拼接串便于调试
	fmt.Printf("签名串: %s\n", manual)
}
