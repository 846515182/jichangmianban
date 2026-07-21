package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/security"

	"go.uber.org/zap"
)

// PaymentService EPay 支付服务
type PaymentService struct {
	settingRepo *repo.SettingRepo
	orderSvc    *OrderService
}

// NewPaymentService 创建支付服务
func NewPaymentService(sr *repo.SettingRepo, o *OrderService) *PaymentService {
	return &PaymentService{settingRepo: sr, orderSvc: o}
}

// EPayConfig EPay 配置(从 settings 表读取)
type EPayConfig struct {
	PID       int64  `json:"pid"`
	Key       string `json:"key"`
	APIURL    string `json:"api_url"`
	Enabled   bool   `json:"enabled"`
	NotifyURL string `json:"notify_url"`
	ReturnURL string `json:"return_url"`
}

// 默认 setting key
const (
	settingKeyEPayPID     = "epay_pid"
	settingKeyEPayKey     = "epay_key"
	settingKeyEPayAPIURL  = "epay_api_url"
	settingKeyEPayEnabled = "epay_enabled"
	settingKeyEPayNotify  = "epay_notify_url"
	settingKeyEPayReturn  = "epay_return_url"
)

// paymentMethodToEPayType 将内部 payment_method 映射为 EPay 的 type 参数
// 注意: 柠檬支付(lemzf)等易支付协议的微信支付 type 为 wxpay, 不是 wechat
func paymentMethodToEPayType(method string) string {
	switch method {
	case "epay_alipay":
		return "alipay"
	case "epay_wechat":
		return "wxpay"
	default:
		return ""
	}
}

// loadConfig 从 settings 表读取 EPay 配置
func (s *PaymentService) loadConfig() (*EPayConfig, error) {
	cfg := &EPayConfig{}
	pid, err := s.settingRepo.GetString(settingKeyEPayPID)
	if err == nil && pid != "" {
		// 尝试解析为 int64
		var p int64
		for _, ch := range pid {
			if ch < '0' || ch > '9' {
				p = 0
				break
			}
			p = p*10 + int64(ch-'0')
		}
		cfg.PID = p
	}
	key, err := s.settingRepo.GetString(settingKeyEPayKey)
	if err == nil {
		// 修复 SEC-ENCRYPT-02 (P1): epay_key 入库前已 AES 加密, 读取时解密;
		// 兼容旧明文数据(DecryptSecret 解密失败时原样返回)。
		masterKey := ""
		if c := app.Get(); c != nil && c.Cfg != nil {
			masterKey = c.Cfg.AESMasterKey
		}
		cfg.Key = security.DecryptSecret(masterKey, key)
	}
	apiURL, err := s.settingRepo.GetString(settingKeyEPayAPIURL)
	if err == nil {
		cfg.APIURL = apiURL
	}
	// 注意: SaveConfig 通过 Set(key, cfg.Enabled) 保存为 JSON 布尔值 true,
	// 不能用 GetString 读取(json.Unmarshal 无法把 bool 解码进 string),
	// 否则 err != nil 导致 Enabled 永远为 false, 出现"EPay 支付未启用"。
	var enabledBool bool
	if err := s.settingRepo.Get(settingKeyEPayEnabled, &enabledBool); err == nil {
		cfg.Enabled = enabledBool
	} else {
		// 兼容历史数据: 值可能被存为字符串 "true"/"1"
		if s2, e2 := s.settingRepo.GetString(settingKeyEPayEnabled); e2 == nil && (s2 == "true" || s2 == "1") {
			cfg.Enabled = true
		}
	}
	notify, err := s.settingRepo.GetString(settingKeyEPayNotify)
	if err == nil {
		cfg.NotifyURL = notify
	}
	ret, err := s.settingRepo.GetString(settingKeyEPayReturn)
	if err == nil {
		cfg.ReturnURL = ret
	}
	return cfg, nil
}

// SaveConfig 保存 EPay 配置到 settings 表
func (s *PaymentService) SaveConfig(cfg *EPayConfig) error {
	if cfg.PID > 0 {
		if err := s.settingRepo.Set(settingKeyEPayPID, fmt.Sprintf("%d", cfg.PID)); err != nil {
			return err
		}
	}
	if cfg.Key != "" {
		// 修复 SEC-ENCRYPT-02 (P1): epay_key 入库前 AES 加密, 防止数据库拖库后商户密钥泄露。
		masterKey := ""
		if c := app.Get(); c != nil && c.Cfg != nil {
			masterKey = c.Cfg.AESMasterKey
		}
		encKey := security.EncryptSecret(masterKey, cfg.Key)
		if err := s.settingRepo.Set(settingKeyEPayKey, encKey); err != nil {
			return err
		}
	}
	if cfg.APIURL != "" {
		if err := s.settingRepo.Set(settingKeyEPayAPIURL, cfg.APIURL); err != nil {
			return err
		}
	}
	if err := s.settingRepo.Set(settingKeyEPayEnabled, cfg.Enabled); err != nil {
		return err
	}
	if cfg.NotifyURL != "" {
		if err := s.settingRepo.Set(settingKeyEPayNotify, cfg.NotifyURL); err != nil {
			return err
		}
	}
	if cfg.ReturnURL != "" {
		if err := s.settingRepo.Set(settingKeyEPayReturn, cfg.ReturnURL); err != nil {
			return err
		}
	}
	return nil
}

// GetConfig 获取 EPay 配置(对外暴露)
func (s *PaymentService) GetConfig() (*EPayConfig, error) {
	return s.loadConfig()
}

// TestConnection 测试 EPay 配置是否正确
// 调用易支付"查询商户信息"API: /api.php?act=query&pid={pid}&sign={sign}
// 返回商户状态、余额等信息，code=1 表示连接成功
// 注意: 签名需包含所有请求参数(除 sign/sign_type 外)，包括 act
func (s *PaymentService) TestConnection(pid int64, key, apiURL string) (map[string]interface{}, error) {
	if pid == 0 || key == "" || apiURL == "" {
		return nil, errors.New("商户ID、密钥、API地址不能为空")
	}
	// 签名参数: act 和 pid 都参与签名(按 ASCII 排序: act < pid)
	params := map[string]string{
		"act": "query",
		"pid": fmt.Sprintf("%d", pid),
	}
	sign := epaySign(params, key)
	queryURL := strings.TrimRight(apiURL, "/") + "/api.php?act=query" +
		"&pid=" + fmt.Sprintf("%d", pid) +
		"&sign=" + sign +
		"&sign_type=MD5"

	resp, err := httpGetWithTimeout(queryURL, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("请求支付接口失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w, body=%s", err, string(body))
	}
	code, _ := result["code"].(float64)
	if code != 1 {
		msg, _ := result["msg"].(string)
		return result, fmt.Errorf("支付接口返回失败: %s", msg)
	}
	return result, nil
}

// CreatePaymentResult 创建支付返回
type CreatePaymentResult struct {
	PayURL  string `json:"pay_url"`
	OrderNo string `json:"order_no"`
}

// CreatePayment 生成 EPay 支付 URL
// 参数: pid, out_trade_no(订单号), type(alipay/wechat/qq), notify_url, return_url, name, money, sign
func (s *PaymentService) CreatePayment(o *model.Order, baseURL string) (*CreatePaymentResult, error) {
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, errors.New("EPay 支付未启用")
	}
	if cfg.PID == 0 || cfg.Key == "" || cfg.APIURL == "" {
		return nil, errors.New("EPay 配置不完整")
	}
	payType := paymentMethodToEPayType(o.PaymentMethod)
	if payType == "" {
		return nil, errors.New("不支持的支付方式")
	}
	// 金额: 元(两位小数)
	money := fmt.Sprintf("%.2f", float64(o.AmountCents)/100.0)
	notifyURL := cfg.NotifyURL
	if notifyURL == "" && baseURL != "" {
		notifyURL = strings.TrimRight(baseURL, "/") + "/api/v1/payment/notify"
	}
	returnURL := cfg.ReturnURL
	if returnURL == "" && baseURL != "" {
		returnURL = strings.TrimRight(baseURL, "/") + "/api/v1/payment/return"
	}
	params := map[string]string{
		"pid":          fmt.Sprintf("%d", cfg.PID),
		"type":         payType,
		"out_trade_no": o.OrderNo,
		"notify_url":   notifyURL,
		"return_url":   returnURL,
		"name":         o.PlanName,
		"money":        money,
	}
	sign := epaySign(params, cfg.Key)
	params["sign"] = sign
	params["sign_type"] = "MD5"
	// 拼接提交 URL(采用 GET 方式跳转)
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	payURL := strings.TrimRight(cfg.APIURL, "/") + "/submit.php?" + q.Encode()
	return &CreatePaymentResult{PayURL: payURL, OrderNo: o.OrderNo}, nil
}

// VerifyCallback 验证 EPay 回调签名
func (s *PaymentService) VerifyCallback(params map[string]string) (bool, error) {
	cfg, err := s.loadConfig()
	if err != nil {
		return false, err
	}
	if cfg.Key == "" {
		return false, errors.New("EPay key 未配置")
	}
	sign, ok := params["sign"]
	if !ok || sign == "" {
		return false, errors.New("缺少 sign 参数")
	}
	expected := epaySign(params, cfg.Key)
	return strings.EqualFold(expected, sign), nil
}

// HandleNotify 处理支付回调, 调用 order_service.PaySuccess
// 修复 SEC-P1-02: 增加金额校验 + 防重放
// 修复 P0-F5: 在验签+金额校验通过后、PaySuccess 之前, 先 SetNX trade_no 占坑,
// 防止 PaySuccess 后崩溃导致重复履约(EPay 重试回调时会再次进入 PaySuccess)。
// 若 SetNX 返回 false(已处理过), 直接返回 success, EPay 收到 200 不再重试。
// 若 Redis 不可用或异常, 降级走 PaySuccess 自身的原子状态机幂等(P0-F1) 兜底。
func (s *PaymentService) HandleNotify(params map[string]string) (string, error) {
	ok, err := s.VerifyCallback(params)
	if err != nil {
		return "fail", err
	}
	if !ok {
		return "fail", errors.New("签名校验失败")
	}
	tradeStatus := params["trade_status"]
	if tradeStatus != "TRADE_SUCCESS" {
		return "fail", errors.New("trade_status 非 TRADE_SUCCESS")
	}
	orderNo := params["out_trade_no"]
	tradeNo := params["trade_no"]
	moneyStr := params["money"]
	if orderNo == "" {
		return "fail", errors.New("缺少 out_trade_no")
	}
	// 金额校验
	order, err := s.orderSvc.GetByOrderNo(orderNo)
	if err != nil {
		return "fail", errors.New("订单不存在")
	}
	moneyCents, err := parseMoneyToCents(moneyStr)
	if err != nil {
		return "fail", fmt.Errorf("金额格式错误: %w", err)
	}
	if moneyCents != order.AmountCents {
		return "fail", fmt.Errorf("金额不匹配: 回调=%d分 订单=%d分", moneyCents, order.AmountCents)
	}
	// P0-F5: 先占坑, 防止 PaySuccess 后崩溃导致重复履约
	// 若 SetNX 返回 false 表示已处理过, 直接返回 success(EPay 收到 200 不再重试)
	// 若 Redis 不可用/异常, 降级走 PaySuccess 自身的原子状态机幂等(P0-F1) 兜底
	//
	// 关键: 若 SetNX 成功但 PaySuccess 失败(DB 故障等), 必须显式 DEL key,
	// 否则下次 EPay 重试回调会因 SetNX=false 直接返回 success 而 PaySuccess 没被调用,
	// 导致订单永久丢失履约(用户付款但套餐未开通)。
	rdb := app.Get().RDB
	notifyKey := ""
	notifyKeySet := false
	if rdb != nil {
		notifyKey = "epay_notify:" + tradeNo
		ok, err := rdb.SetNX(context.Background(), notifyKey, "1", 7*24*time.Hour).Result()
		if err == nil && !ok {
			// 已处理过, 直接返回成功(EPay 收到 200 不再重试)
			return "success", nil
		}
		// err != nil 时降级走 PaySuccess 兜底, 但记录告警
		if err != nil {
			if logger := app.Get().Logger; logger != nil {
				logger.Warn("HandleNotify SetNX 异常, 降级走 PaySuccess 兜底",
					zap.String("trade_no", tradeNo), zap.Error(err))
			}
		} else {
			// SetNX 成功占坑, 标记后续 PaySuccess 失败时需 DEL 回退
			notifyKeySet = true
		}
	}
	// 然后才调 PaySuccess(P0-F1 内部原子状态机保证幂等, 不会重复履约)
	if err := s.orderSvc.PaySuccess(orderNo, tradeNo); err != nil {
		// PaySuccess 失败: 回退 SetNX 占坑, 让 EPay 下次重试能重新进入 PaySuccess
		// (不回退会导致下次重试被 SetNX=false 拦截, 订单永久丢失履约)
		if notifyKeySet && rdb != nil {
			if delErr := rdb.Del(context.Background(), notifyKey).Err(); delErr != nil {
				if logger := app.Get().Logger; logger != nil {
					logger.Warn("HandleNotify 回退 SetNX key 失败, 下次重试可能被吞",
						zap.String("trade_no", tradeNo), zap.Error(delErr))
				}
			}
		}
		return "fail", err
	}
	return "success", nil
}

// parseMoneyToCents 将元字符串转为分
func parseMoneyToCents(money string) (int64, error) {
	f, err := strconv.ParseFloat(money, 64)
	if err != nil {
		return 0, err
	}
	return int64(math.Round(f * 100)), nil
}

// epaySign EPay 签名算法:
// 1. 参数按 key 升序排序
// 2. 过滤 sign 与 sign_type(空值不过滤, 与 EPay 官方算法对齐)
// 3. 拼接成 a=1&b=2 格式
// 4. 末尾加上 key
// 5. MD5 加密(小写 hex)
//
// 修复 P0-2: 旧实现过滤空值, 与 EPay 官方签名算法不一致(官方不过滤空值),
// 存在签名绕过风险。现改为与官方对齐: 仅过滤 sign/sign_type, 保留空值参数。
func epaySign(params map[string]string, key string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "sign" || k == "sign_type" {
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

// httpGetWithTimeout 带超时的 HTTP GET 请求
func httpGetWithTimeout(rawURL string, timeout time.Duration) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "NexusPanel/1.0")
	return http.DefaultClient.Do(req)
}

// EPayOrderStatus EPay act=order 查询返回的关键字段
type EPayOrderStatus struct {
	TradeStatus string // TRADE_SUCCESS 表示已支付
	TradeNo     string // 第三方流水号
	Money       string // 实付金额(元)
	OutTradeNo  string // 商户订单号
	Type        string // 支付方式 alipay/wxpay/qqpay
}

// QueryOrderStatus 修复 PAY-RECON-01 (P0): 主动查询 EPay 订单真实状态。
// 用于掉单对账 cron(回调丢失时主动拉取支付结果)。
// EPay act=order 接口: GET /api.php?act=order&pid=&out_trade_no=&sign=&sign_type=MD5
// 返回 code=1 表示查询成功, trade_status=TRADE_SUCCESS 表示已支付。
func (s *PaymentService) QueryOrderStatus(orderNo string) (*EPayOrderStatus, error) {
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, err
	}
	if cfg.PID == 0 || cfg.Key == "" || cfg.APIURL == "" {
		return nil, errors.New("EPay 配置不完整")
	}
	params := map[string]string{
		"act":           "order",
		"pid":           fmt.Sprintf("%d", cfg.PID),
		"out_trade_no":  orderNo,
	}
	sign := epaySign(params, cfg.Key)
	queryURL := strings.TrimRight(cfg.APIURL, "/") + "/api.php?act=order" +
		"&pid=" + fmt.Sprintf("%d", cfg.PID) +
		"&out_trade_no=" + url.QueryEscape(orderNo) +
		"&sign=" + sign +
		"&sign_type=MD5"

	resp, err := httpGetWithTimeout(queryURL, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("查询支付订单失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取查询响应失败: %w", err)
	}
	var result struct {
		Code        interface{} `json:"code"`
		Msg         string      `json:"msg"`
		TradeStatus string      `json:"trade_status"`
		TradeNo     string      `json:"trade_no"`
		OutTradeNo  string      `json:"out_trade_no"`
		Money       string      `json:"money"`
		Type        string      `json:"type"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析查询响应失败: %w, body=%s", err, string(body))
	}
	// code 兼容数字 1 与字符串 "1"
	codeOK := false
	switch v := result.Code.(type) {
	case float64:
		codeOK = v == 1
	case string:
		codeOK = v == "1"
	}
	if !codeOK {
		return nil, fmt.Errorf("EPay 查询失败: %s", result.Msg)
	}
	return &EPayOrderStatus{
		TradeStatus: result.TradeStatus,
		TradeNo:     result.TradeNo,
		Money:       result.Money,
		OutTradeNo:  result.OutTradeNo,
		Type:        result.Type,
	}, nil
}

// RequestRefund 修复 PAY-REFUND-01 (P1): 退款 API 集成。
// 部分易支付网关支持退款接口(act=refund), 部分不支持。
// 本方法采用 best-effort 策略:
//   - 若网关支持退款, 调用并返回 nil;
//   - 若网关返回"不支持退款"等业务错误, 返回 nil(本地已退款, 第三方侧人工对账);
//   - 仅网络/系统错误返回 err, 由调用方决定是否告警。
// 已在 OrderService.AdminRefund 之外被调用, 不阻塞本地退款流程。
func (s *PaymentService) RequestRefund(orderNo, tradeNo, money string) error {
	cfg, err := s.loadConfig()
	if err != nil {
		return err
	}
	if cfg.PID == 0 || cfg.Key == "" || cfg.APIURL == "" {
		return errors.New("EPay 配置不完整,跳过退款同步")
	}
	params := map[string]string{
		"act":          "refund",
		"pid":          fmt.Sprintf("%d", cfg.PID),
		"out_trade_no": orderNo,
		"trade_no":     tradeNo,
		"money":        money,
	}
	sign := epaySign(params, cfg.Key)
	queryURL := strings.TrimRight(cfg.APIURL, "/") + "/api.php?act=refund" +
		"&pid=" + fmt.Sprintf("%d", cfg.PID) +
		"&out_trade_no=" + url.QueryEscape(orderNo) +
		"&trade_no=" + url.QueryEscape(tradeNo) +
		"&money=" + url.QueryEscape(money) +
		"&sign=" + sign +
		"&sign_type=MD5"

	resp, err := httpGetWithTimeout(queryURL, 10*time.Second)
	if err != nil {
		return fmt.Errorf("退款请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取退款响应失败: %w", err)
	}
	var result struct {
		Code interface{} `json:"code"`
		Msg  string      `json:"msg"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		// 非 JSON 响应, 通常是不支持退款的网关, 视为 best-effort 成功
		return nil
	}
	codeVal := -1.0
	switch v := result.Code.(type) {
	case float64:
		codeVal = v
	case string:
		// 解析字符串 code
		var f float64
		if _, err := fmt.Sscanf(v, "%f", &f); err == nil {
			codeVal = f
		}
	}
	// code=1 退款成功; code=-1 通常为"不支持退款"等业务错误, 视为 best-effort 成功
	if codeVal == 1 || codeVal == -1 {
		return nil
	}
	return fmt.Errorf("EPay 退款失败: %s", result.Msg)
}
