package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/security"
)

// 订阅类型常量
const (
	SubTypeClash   = "clash"
	SubTypeSingBox = "sing-box"
	SubTypeV2Ray   = "v2ray"
	SubTypeSIP008  = "sip008"
)

// SubscribeService 订阅服务
type SubscribeService struct {
	subRepo  *repo.SubscriptionRepo
	nodeRepo *repo.NodeRepo
	userRepo *repo.UserRepo
}

// NewSubscribeService 创建订阅服务
func NewSubscribeService(s *repo.SubscriptionRepo, n *repo.NodeRepo, u *repo.UserRepo) *SubscribeService {
	return &SubscribeService{subRepo: s, nodeRepo: n, userRepo: u}
}

// GenerateSignedURL 为用户生成带签名的订阅链接(有效期由配置决定)
// 修复 P0-NONCE: 移除 nonce 防重放 — Clash/V2RayN 等客户端会缓存订阅 URL 并定期自动更新,
// nonce 防重放导致第二次更新被拒绝(ErrSubSigExpired), 用户看到节点不更新/新节点不出现。
// 订阅链接的 TTL 过期机制已足够防止长期重放(默认 24h), 无需 nonce。
func (s *SubscribeService) GenerateSignedURL(userID, baseURL, clientIP string) (string, error) {
	sub, err := s.subRepo.GetByUserID(userID)
	if err != nil {
		return "", err
	}
	hmacMgr := security.NewHMACManager(app.Get().Cfg.HMACSubSecret)
	sig, exp := hmacMgr.SignWithTTL(sub.SubToken, userID, app.Get().Cfg.SubSigTTL)
	sigStr := hmacMgr.BuildSigStr(exp, sig)

	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/api/v1/subscribe")
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("token", sub.SubToken)
	q.Set("sig", sigStr)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// FetchResult 订阅获取结果
type FetchResult struct {
	Content     string
	ContentType string
	Filename    string
	Blocked     bool   // 账号是否被阻断(到期/超流量/禁用)
	BlockReason string // 阻断原因
}

// Fetch 获取订阅内容
// subType 为空时按 User-Agent 识别客户端
// sig 格式: exp.signature
// 修复 P0-NONCE: 移除 nonce 防重放验证 — 允许同一签名在 TTL 窗口内多次使用,
// 解决 Clash/V2RayN 等客户端自动更新订阅时第二次请求被拒绝的问题。
// TTL 过期机制(默认 24h)已足够防止长期重放。
func (s *SubscribeService) Fetch(userID, subType, sig, userAgent, clientIP string) (*FetchResult, error) {
	// 1. 校验签名（仅校验时间有效期，不绑定 IP）
	sub, err := s.subRepo.GetByUserID(userID)
	if err != nil {
		return nil, err
	}
	hmacMgr := security.NewHMACManager(app.Get().Cfg.HMACSubSecret)
	if err := hmacMgr.VerifySigStr(sub.SubToken, userID, sig); err != nil {
		return nil, ErrSubSigExpired
	}

	// 2. 校验用户状态: 账号禁用 / 已到期 / 流量耗尽时返回空订阅(避免客户端报错)
	u, err := s.userRepo.GetByID(userID)
	if err != nil {
		return nil, err
	}
	blocked := false
	reason := ""
	if u.Status == "disabled" {
		blocked, reason = true, "account disabled"
	} else if u.ExpiredAt != nil && u.ExpiredAt.Before(time.Now()) {
		blocked, reason = true, "subscription expired"
	} else if u.TrafficLimit > 0 && u.TrafficUsed >= u.TrafficLimit {
		blocked, reason = true, "traffic exhausted"
	}

	// P0-DeviceLimit: 订阅拉取维度设备数限制(仅对活跃用户生效)
	// 超过套餐 DeviceLimit 个不同 IP 在 1h 内拉取 → 拒绝(阻止新增设备)
	if !blocked {
		if err := s.enforceDeviceLimit(userID, clientIP); err != nil {
			return nil, err
		}
	}

	// 3. 识别订阅类型
	if subType == "" {
		subType = detectSubType(userAgent)
	}

	// 4. 获取用户可访问的节点(被阻断时返回空列表)
	var nodes []model.Node
	if !blocked {
		nodes, err = s.nodeRepo.ListByUser(userID)
		if err != nil {
			return nil, err
		}
		// [节点容量管理] 智能负载调度:
		// ① 过滤掉 full 状态节点(满载, 拒绝新用户)
		// ② busy 状态节点降权排序(允许连接但排最后)
		// ③ 按 load_status 优先级排序: idle > normal > busy, 让用户优先连空闲节点
		nodes = filterAndSortByLoad(nodes)
	}

	// 5. 按类型生成
	res := &FetchResult{}
	switch subType {
	case SubTypeClash:
		res.Content = s.generateClashYAML(nodes, userID)
		res.ContentType = "application/x-yaml; charset=utf-8"
		res.Filename = "clash.yaml"
	case SubTypeSingBox:
		res.Content = s.generateSingBoxJSON(nodes, userID)
		res.ContentType = "application/json; charset=utf-8"
		res.Filename = "sing-box.json"
	case SubTypeSIP008:
		res.Content = s.generateSIP008(nodes)
		res.ContentType = "application/json; charset=utf-8"
		res.Filename = "sip008.json"
	case SubTypeV2Ray:
		fallthrough
	default:
		res.ContentType = "text/plain; charset=utf-8"
		res.Filename = "v2ray.txt"
		res.Content = base64.StdEncoding.EncodeToString([]byte(s.generateV2RayURIs(nodes, userID)))
	}
	// 阻断时在响应头标记原因(订阅生成器透传)
	res.Blocked = blocked
	res.BlockReason = reason
	return res, nil
}

// detectSubType 根据客户端 User-Agent 识别订阅类型
func detectSubType(ua string) string {
	ua = strings.ToLower(ua)
	switch {
	case strings.Contains(ua, "clash"):
		return SubTypeClash
	case strings.Contains(ua, "sing-box") || strings.Contains(ua, "singbox"):
		return SubTypeSingBox
	case strings.Contains(ua, "v2ray") || strings.Contains(ua, "shadowrocket"):
		return SubTypeV2Ray
	case strings.Contains(ua, "sip008"):
		return SubTypeSIP008
	default:
		return SubTypeV2Ray
	}
}

// nodeConfig 解析节点 server_config
type nodeConfig struct {
	Protocol          string                 `json:"protocol"`
	ServerAddress     string                 `json:"server_address"`
	Port              int                    `json:"port"`
	Reality           map[string]interface{} `json:"reality"`
	Flow              string                 `json:"flow"`
	SNI               string                 `json:"sni"`
	RealityServerName string                 `json:"reality_server_name"`
	Fingerprint       string                 `json:"fingerprint"`
	Transport         map[string]interface{} `json:"transport"`
	Password          string                 `json:"password"`
	Method            string                 `json:"method"` // shadowsocks 加密方式
	UUID              string                 `json:"uuid"`
	AlterID           int                    `json:"alter_id"`
	Extra             map[string]interface{} `json:"extra"`
}

func parseNodeConfig(n *model.Node) *nodeConfig {
	var c nodeConfig
	_ = json.Unmarshal(n.ServerConfig, &c)
	if c.Protocol == "" {
		c.Protocol = n.Protocol
	}
	if c.ServerAddress == "" {
		c.ServerAddress = n.ServerAddress
	}
	if c.Port == 0 {
		c.Port = n.Port
	}
	// SNI 字段回退: server_config 中可能使用 reality_server_name 而非 sni
	if c.SNI == "" && c.RealityServerName != "" {
		c.SNI = c.RealityServerName
	}
	// 进一步回退: 从 reality.sni 提取(本系统 CreateNode 默认存放在此)
	if c.SNI == "" && c.Reality != nil {
		if v, ok := c.Reality["sni"].(string); ok && v != "" {
			c.SNI = v
		} else if v, ok := c.Reality["dest"].(string); ok && v != "" {
			// dest 形如 "gateway.icloud.com:443"，去掉端口
			if idx := strings.LastIndex(v, ":"); idx > 0 {
				c.SNI = v[:idx]
			} else {
				c.SNI = v
			}
		}
	}
	// 最终回退: VLESS+REALITY 必须有 SNI，否则客户端 TLS 握手会失败
	// 与服务端 buildXrayConfig 的默认值保持一致(gateway.icloud.com)
	if c.SNI == "" && strings.ToLower(c.Protocol) == "vless" && c.Reality != nil {
		c.SNI = "gateway.icloud.com"
	}
	// VLESS+REALITY 默认使用 XTLS-Vision(flow=xtls-rprx-vision)，与服务端 buildXrayConfig 保持一致
	if c.Flow == "" && strings.ToLower(c.Protocol) == "vless" && c.Reality != nil {
		c.Flow = "xtls-rprx-vision"
	}
	return &c
}

// generateClashYAML 生成 Clash Meta 兼容 YAML
func (s *SubscribeService) generateClashYAML(nodes []model.Node, userID string) string {
	var proxies []map[string]interface{}
	for _, n := range nodes {
		c := parseNodeConfig(&n)
		// VLESS/VMess 用 user.ID 作为客户端 UUID（与服务端 xray clients.id 对齐）
		if strings.ToLower(c.Protocol) == "vless" || strings.ToLower(c.Protocol) == "vmess" {
			c.UUID = userID
		}
		p := buildClashProxy(n, c)
		proxies = append(proxies, p)
	}

	var names []string
	for _, n := range nodes {
		names = append(names, n.Name)
	}

	doc := map[string]interface{}{
		"proxies": proxies,
		"proxy-groups": []map[string]interface{}{
			{
				"name":    "Nexus",
				"type":    "select",
				"proxies": append([]string{"DIRECT"}, names...),
			},
		},
		"rules": []string{
			"GEOIP,CN,DIRECT",
			"MATCH,Nexus",
		},
	}
	b, err := yaml.Marshal(doc)
	if err != nil {
		return ""
	}
	return string(b)
}

// buildClashProxy 构建单个 Clash proxy 配置
func buildClashProxy(n model.Node, c *nodeConfig) map[string]interface{} {
	p := map[string]interface{}{
		"name":   n.Name,
		"server": c.ServerAddress,
		"port":   c.Port,
	}
	switch strings.ToLower(c.Protocol) {
	case "vless":
		p["type"] = "vless"
		p["uuid"] = c.UUID
		if c.Flow != "" {
			p["flow"] = c.Flow
		}
		if c.Reality != nil {
			p["tls"] = true
			p["servername"] = c.SNI
			p["client-fingerprint"] = orDefault(c.Fingerprint, "chrome")
			if pk, ok := c.Reality["public_key"].(string); ok {
				shortID := ""
				if sid, ok := c.Reality["short_id"].(string); ok {
					shortID = sid
				}
				p["reality-opts"] = map[string]interface{}{
					"public-key": pk,
					"short-id":   shortID,
				}
			}
		}
	case "vmess":
		p["type"] = "vmess"
		p["uuid"] = c.UUID
		p["alterId"] = c.AlterID
		p["cipher"] = orDefault(c.Method, "auto")
	case "trojan":
		p["type"] = "trojan"
		p["password"] = c.Password
		p["sni"] = c.SNI
	case "shadowsocks", "ss":
		p["type"] = "ss"
		p["cipher"] = c.Method
		p["password"] = c.Password
	default:
		p["type"] = c.Protocol
	}
	return p
}

// generateSingBoxJSON 生成 sing-box 兼容 JSON
func (s *SubscribeService) generateSingBoxJSON(nodes []model.Node, userID string) string {
	var outbounds []map[string]interface{}
	for _, n := range nodes {
		c := parseNodeConfig(&n)
		// VLESS/VMess 用 user.ID 作为客户端 UUID（与服务端 xray clients.id 对齐）
		if strings.ToLower(c.Protocol) == "vless" || strings.ToLower(c.Protocol) == "vmess" {
			c.UUID = userID
		}
		outbounds = append(outbounds, buildSingBoxOutbound(n, c))
	}
	// 构建选择器可选出站标签(direct + 所有节点)
	tags := []string{"direct"}
	for _, n := range nodes {
		tags = append(tags, n.Name)
	}
	outbounds = append(outbounds, map[string]interface{}{
		"type":      "selector",
		"tag":       "Nexus",
		"outbounds": tags,
		"default":   "direct",
	})

	doc := map[string]interface{}{
		"log":       map[string]interface{}{"level": "info"},
		"outbounds": outbounds,
		"route": map[string]interface{}{
			"rules": []map[string]interface{}{
				{"geoip": "cn", "outbound": "direct"},
			},
			"final": "Nexus",
		},
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

// buildSingBoxOutbound 构建 sing-box 单个出站
func buildSingBoxOutbound(n model.Node, c *nodeConfig) map[string]interface{} {
	o := map[string]interface{}{
		"type":        c.Protocol,
		"tag":         n.Name,
		"server":      c.ServerAddress,
		"server_port": c.Port,
	}
	switch strings.ToLower(c.Protocol) {
	case "vless":
		o["uuid"] = c.UUID
		if c.Flow != "" {
			o["flow"] = c.Flow
		}
		if c.Reality != nil {
			tls := map[string]interface{}{"enabled": true}
			if c.SNI != "" {
				tls["server_name"] = c.SNI
			}
			tls["utls"] = map[string]interface{}{
				"enabled":     true,
				"fingerprint": orDefault(c.Fingerprint, "chrome"),
			}
			if pk, ok := c.Reality["public_key"].(string); ok {
				shortID := ""
				if sid, ok := c.Reality["short_id"].(string); ok {
					shortID = sid
				}
				tls["reality"] = map[string]interface{}{
					"enabled":    true,
					"public_key": pk,
					"short_id":   shortID,
				}
			}
			o["tls"] = tls
		}
	case "vmess":
		o["uuid"] = c.UUID
		o["alter_id"] = c.AlterID
		o["security"] = orDefault(c.Method, "auto")
	case "trojan":
		o["password"] = c.Password
	case "shadowsocks", "ss":
		o["method"] = c.Method
		o["password"] = c.Password
	}
	return o
}

// generateV2RayURIs 生成 V2Ray 分享链接(每行一条)
func (s *SubscribeService) generateV2RayURIs(nodes []model.Node, userID string) string {
	var lines []string
	for _, n := range nodes {
		c := parseNodeConfig(&n)
		// VLESS/VMess 用 user.ID 作为客户端 UUID（与服务端 xray clients.id 对齐）
		if strings.ToLower(c.Protocol) == "vless" || strings.ToLower(c.Protocol) == "vmess" {
			c.UUID = userID
		}
		uri := buildV2RayURI(n, c)
		if uri != "" {
			lines = append(lines, uri)
		}
	}
	return strings.Join(lines, "\n")
}

// buildV2RayURI 构建单条分享链接
func buildV2RayURI(n model.Node, c *nodeConfig) string {
	switch strings.ToLower(c.Protocol) {
	case "vless":
		params := url.Values{}
		if c.Reality != nil {
			params.Set("encryption", "none")
			if c.SNI != "" {
				params.Set("sni", c.SNI)
			}
			params.Set("security", "reality")
			if pk, ok := c.Reality["public_key"].(string); ok {
				params.Set("pbk", pk)
			}
			if sid, ok := c.Reality["short_id"].(string); ok {
				params.Set("sid", sid)
			}
			params.Set("fp", orDefault(c.Fingerprint, "chrome"))
			// 不设置 alpn: 部分客户端(如 v2rayN)对 alpn 中的逗号编码解析异常，
			// 会导致后续 REALITY 参数全部丢失，REALITY 握手失败
		}
		if c.Flow != "" {
			params.Set("flow", c.Flow)
		}
		params.Set("type", "tcp")
		return fmt.Sprintf("vless://%s@%s:%d?%s#%s",
			c.UUID, c.ServerAddress, c.Port, params.Encode(), url.PathEscape(n.Name))
	case "vmess":
		// vmess://base64(json)
		obj := map[string]interface{}{
			"v":    "2",
			"ps":   n.Name,
			"add":  c.ServerAddress,
			"port": c.Port,
			"id":   c.UUID,
			"aid":  c.AlterID,
			"scy":  orDefault(c.Method, "auto"),
			"net":  "tcp",
		}
		b, _ := json.Marshal(obj)
		return "vmess://" + base64.StdEncoding.EncodeToString(b)
	case "trojan":
		return fmt.Sprintf("trojan://%s@%s:%d?security=tls&sni=%s#%s",
			c.Password, c.ServerAddress, c.Port, url.QueryEscape(c.SNI), url.PathEscape(n.Name))
	case "shadowsocks", "ss":
		// ss://base64(method:password)@host:port#name
		userinfo := base64.RawURLEncoding.EncodeToString([]byte(c.Method + ":" + c.Password))
		return fmt.Sprintf("ss://%s@%s:%d#%s", userinfo, c.ServerAddress, c.Port, url.PathEscape(n.Name))
	}
	return ""
}

// generateSIP008 生成 SIP008 JSON(仅包含 Shadowsocks 节点，SIP008 规范不支持其他协议)
func (s *SubscribeService) generateSIP008(nodes []model.Node) string {
	var servers []map[string]interface{}
	for _, n := range nodes {
		c := parseNodeConfig(&n)
		// SIP008 仅支持 Shadowsocks，跳过其他协议
		if strings.ToLower(c.Protocol) != "shadowsocks" && strings.ToLower(c.Protocol) != "ss" {
			continue
		}
		entry := map[string]interface{}{
			"id":          n.ID,
			"remarks":     n.Name,
			"server":      c.ServerAddress,
			"server_port": c.Port,
			"method":      orDefault(c.Method, "chacha20-ietf-poly1305"),
			"password":    c.Password,
		}
		servers = append(servers, entry)
	}
	doc := map[string]interface{}{
		"version":    "2",
		"servers":    servers,
		"bytes_used": 0,
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return string(b)
}

// orDefault 字符串为空则返回默认值
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// EnsureSubscription 为用户确保存在订阅记录(不存在则创建)
func (s *SubscribeService) EnsureSubscription(userID string) (*model.Subscription, error) {
	sub, err := s.subRepo.GetByUserID(userID)
	if err == nil && sub != nil {
		return sub, nil
	}
	// 创建新订阅
	tok, err := generateNodeToken() // 复用随机 token 生成
	if err != nil {
		return nil, err
	}
	sub = &model.Subscription{
		UserID:   userID,
		SubToken: tok,
		SubType:  SubTypeClash,
	}
	if err := s.subRepo.Create(sub); err != nil {
		return nil, err
	}
	return sub, nil
}

// ErrSubSigExpired 订阅签名过期/无效
var ErrSubSigExpired = errors.New("sub sig expired")

// ErrDeviceLimitExceeded 设备数超限
// P0-DeviceLimit: 用户在 1 小时窗口内从超过套餐 DeviceLimit 个不同 IP 拉取订阅
var ErrDeviceLimitExceeded = errors.New("device limit exceeded")

// PublicFetch 公开订阅(通过 sub_token + sig 认证, 无需 JWT)
// clientIP 用于校验 IP 绑定签名
func (s *SubscribeService) PublicFetch(token, subType, sig, userAgent, clientIP string) (*FetchResult, error) {
	sub, err := s.subRepo.GetByToken(token)
	if err != nil {
		return nil, ErrSubSigExpired
	}
	return s.Fetch(sub.UserID, subType, sig, userAgent, clientIP)
}

// enforceDeviceLimit P0-DeviceLimit: 订阅拉取维度设备数限制
//
// 原理: 每台新设备使用订阅前必须先拉取订阅(获取节点列表)。
// 用 Redis sorted set 记录用户最近 1 小时内拉取订阅的不同 IP:
//   - key:   sub:ips:{userID}
//   - score: 时间戳(unix 秒)
//   - member: clientIP
//
// ZADD 自然去重(同一 IP 只保留最新 score), ZREMRANGEBYSCORE 清过期, ZCARD 计数。
// 超过套餐 DeviceLimit 时拒绝拉取, 阻止新增设备。
//
// 注意: 这是订阅层限制, 已导入订阅的设备仍可直连节点。
// 完整的连接级限制需要 node agent 解析 Xray access log(已预留 device_limit 到 Meta)。
func (s *SubscribeService) enforceDeviceLimit(userID, clientIP string) error {
	// 查用户套餐的 DeviceLimit
	u, err := s.userRepo.GetByID(userID)
	if err != nil || u == nil {
		return nil // 查不到用户不阻断(后面 Fetch 会再查)
	}
	deviceLimit := 0
	if u.PlanID != nil && *u.PlanID != "" {
		var plan model.Plan
		if err := app.Get().DB.Where("id = ? AND is_deleted = false", *u.PlanID).First(&plan).Error; err == nil {
			deviceLimit = plan.DeviceLimit
		}
	}
	// DeviceLimit=0 表示不限
	if deviceLimit <= 0 {
		return nil
	}
	rdb := app.Get().RDB
	if rdb == nil || clientIP == "" {
		return nil // Redis 不可用或无 IP 时不阻断(避免误杀)
	}

	ctx := context.Background()
	key := fmt.Sprintf("sub:ips:%s", userID)
	now := time.Now().Unix()

	// 1. 清理 1 小时前的记录
	cutoff := now - 3600
	_ = rdb.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", cutoff)).Err()
	// 2. 记录当前 IP (ZADD 会更新已有 member 的 score)
	_ = rdb.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: clientIP}).Err()
	// 3. 设置 TTL 避免冷用户 key 永留
	_ = rdb.Expire(ctx, key, 2*time.Hour).Err()
	// 4. 统计不同 IP 数
	count, err := rdb.ZCard(ctx, key).Result()
	if err != nil {
		return nil // Redis 出错不阻断
	}
	if int(count) > deviceLimit {
		app.Get().Logger.Warn("设备数超限, 拒绝订阅拉取",
			zap.String("user_id", userID),
			zap.Int("device_limit", deviceLimit),
			zap.Int64("current_ip_count", count),
			zap.String("client_ip", clientIP))
		return ErrDeviceLimitExceeded
	}
	return nil
}

// GetByToken 通过 sub_token 反查订阅记录(用于 PublicSubscribe 填充响应头)
func (s *SubscribeService) GetByToken(token string) (*model.Subscription, error) {
	return s.subRepo.GetByToken(token)
}

// filterAndSortByLoad [节点容量管理] 订阅侧负载调度
// 过滤 full 状态节点(满载拒绝新用户) + 离线节点, 按 load_status 优先级排序(idle > normal > busy)
// 保留原 created_at 顺序作为同优先级内的稳定排序
func filterAndSortByLoad(nodes []model.Node) []model.Node {
	if len(nodes) == 0 {
		return nodes
	}

	// 负载状态优先级: idle(0) < normal(1) < busy(2) < full(3, 过滤掉)
	// P0-Offline: 离线/未知状态也给 3(过滤掉), 防止 LoadStatus 不在 map 中时返回 0 被当 idle 优先下发
	statusPriority := map[string]int{
		"idle":   0,
		"normal": 1,
		"busy":   2,
		"full":   3,
	}

	// 过滤掉 full 状态节点(满载, 拒绝新用户) + 离线节点(不下发死节点给用户)
	filtered := make([]model.Node, 0, len(nodes))
	for _, n := range nodes {
		// P0-Offline: 离线节点直接跳过, 不下发给用户(否则客户端连死节点超时)
		if !n.Online {
			continue
		}
		prio := statusPriority[n.LoadStatus]
		if prio >= 3 {
			// full 状态, 跳过(不下发给新用户)
			continue
		}
		filtered = append(filtered, n)
	}

	// 按负载优先级稳定排序(保留原 created_at 顺序)
	// 使用稳定排序确保同优先级节点保持原顺序
	sort.SliceStable(filtered, func(i, j int) bool {
		pi := statusPriority[filtered[i].LoadStatus]
		pj := statusPriority[filtered[j].LoadStatus]
		return pi < pj
	})

	return filtered
}
