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
		cfg.Key = key
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
		if err := s.settingRepo.Set(settingKeyEPayKey, cfg.Key); err != nil {
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
// 修复 F-10: 调换 SetNX 与 PaySuccess 顺序, 防止"标记成功但业务失败"导致回调永久丢失
// 原实现: SetNX 在 PaySuccess 之前, 若 SetNX 成功但 PaySuccess 因 DB 故障失败,
//          返回 fail 给支付平台, 但 trade_no 已被 SetNX 占位, 后续重试全部被 !set 拦截,
//          导致用户付款后套餐永远无法开通, 必须人工介入清理 Redis key。
// 现实现: 先 PaySuccess, 成功后再 SetNX 标记。PaySuccess 内部用 FOR UPDATE 做幂等,
//         重复回调会命中 status==paid 直接返回 nil, 不会重复开通套餐。
func (s *PaymentService) HandleNotify(params map[string]string) (string, error) {
	// [P0-4 2026-07-17] 把所有错误路径用结构化日志记录, 便于运维排障
	// 之前: 所有错误均吞成 "fail" 给支付平台, 数据库侧零日志, 排查支付问题需手动 grep
	ok, err := s.VerifyCallback(params)
	if err != nil {
		app.Get().Logger.Warn("payment_notify verify error", zap.Error(err), zap.String("out_trade_no", params["out_trade_no"]), zap.String("trade_no", params["trade_no"]))
		return "fail", err
	}
	if !ok {
		app.Get().Logger.Warn("payment_notify sign mismatch", zap.String("out_trade_no", params["out_trade_no"]), zap.String("received_sign", params["sign"]))
		return "fail", errors.New("签名校验失败")
	}
	tradeStatus := params["trade_status"]
	if tradeStatus != "TRADE_SUCCESS" {
		app.Get().Logger.Info("payment_notify non-success status", zap.String("trade_status", tradeStatus), zap.String("out_trade_no", params["out_trade_no"]))
		return "fail", errors.New("trade_status 非 TRADE_SUCCESS")
	}
	orderNo := params["out_trade_no"]
	tradeNo := params["trade_no"]
	moneyStr := params["money"]
	if orderNo == "" {
		app.Get().Logger.Warn("payment_notify missing out_trade_no", zap.String("trade_no", tradeNo))
		return "fail", errors.New("缺少 out_trade_no")
	}
	// 金额校验
	order, err := s.orderSvc.GetByOrderNo(orderNo)
	if err != nil {
		app.Get().Logger.Warn("payment_notify order not found", zap.String("out_trade_no", orderNo), zap.Error(err))
		return "fail", errors.New("订单不存在")
	}
	moneyCents, err := parseMoneyToCents(moneyStr)
	if err != nil {
		app.Get().Logger.Warn("payment_notify money parse error", zap.String("out_trade_no", orderNo), zap.String("money", moneyStr), zap.Error(err))
		return "fail", fmt.Errorf("金额格式错误: %w", err)
	}
	if moneyCents != order.AmountCents {
		app.Get().Logger.Warn("payment_notify amount mismatch",
			zap.String("out_trade_no", orderNo),
			zap.Int64("callback_cents", moneyCents),
			zap.Int64("order_cents", order.AmountCents))
		return "fail", fmt.Errorf("金额不匹配: 回调=%d分 订单=%d分", moneyCents, order.AmountCents)
	}
	// 先执行业务逻辑(PaySuccess 内部用 SELECT FOR UPDATE 保证幂等)
	if err := s.orderSvc.PaySuccess(orderNo, tradeNo); err != nil {
		app.Get().Logger.Error("payment_notify PaySuccess failed", zap.String("out_trade_no", orderNo), zap.String("trade_no", tradeNo), zap.Error(err))
		return "fail", err
	}
	// 业务成功后再标记 trade_no 已处理, 阻断后续重复回调
	// 即使此处 SetNX 失败也不影响正确性: 下次回调会再次进入 PaySuccess,
	// 命中 status==paid 提前返回 nil, 不会重复开通套餐
	rdb := app.Get().RDB
	if rdb != nil {
		key := "epay:tradedone:" + tradeNo
		if _, err := rdb.SetNX(context.Background(), key, "1", 24*time.Hour).Result(); err != nil {
			// Redis 失败不影响正确性 (下次回调会幂等), 但要记录便于排障
			app.Get().Logger.Warn("SetNX trade done failed",
				zap.String("trade_no", tradeNo), zap.Error(err))
		}
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
// 2. 过滤 sign 与 sign_type, 空值不参与
// 3. 拼接成 a=1&b=2 格式
// 4. 末尾加上 key
// 5. MD5 加密(小写 hex)
func epaySign(params map[string]string, key string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "sign" || k == "sign_type" {
			continue
		}
		v := params[k]
		if v == "" {
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
