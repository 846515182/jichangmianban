package grpc

import (
	"encoding/json"
	"fmt"
	"strings"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/security"
)

// XrayConfigBuilder 按协议构造 Xray 服务端配置。
// 将 buildXrayConfig 中硬编码的 VLESS 逻辑拆分为可扩展的协议注册表，
// 新增 VMess/Shadowsocks 时只需实现一个 Builder 并注册，无需改动核心流程。
type XrayConfigBuilder interface {
	// Build 返回可直接交给 Xray-core 运行的 JSON 配置字节。
	// limitMbps 为生效的单用户限速值(Mbps): 动态限速与静态限速的并集, 0 表示不限速。
	Build(node *model.Node, users []model.User, limitMbps int) ([]byte, error)
}

// xrayBuilderRegistry 协议 → Builder 映射。
// 新增 Trojan 支持：自动生成/使用自签 TLS 证书，客户端需信任证书或开启 allowInsecure。
var xrayBuilderRegistry = map[string]XrayConfigBuilder{
	"vless":       &vlessRealityBuilder{},
	"vmess":       &vmessTCPBuilder{},
	"shadowsocks": &shadowsocksTCPBuilder{},
	"ss":          &shadowsocksTCPBuilder{},
	"trojan":      &trojanTLSBuilder{},
}

// getXrayBuilder 获取协议对应的 Builder，未注册返回错误。
func getXrayBuilder(protocol string) (XrayConfigBuilder, error) {
	p := strings.ToLower(protocol)
	b, ok := xrayBuilderRegistry[p]
	if !ok {
		return nil, fmt.Errorf("不支持的节点协议: %s", protocol)
	}
	return b, nil
}

// buildBaseXray 返回所有协议共用的基础结构(log/outbounds)。
func buildBaseXray() map[string]interface{} {
	return map[string]interface{}{
		"log": map[string]interface{}{
			"loglevel": "warning",
			"access":   "/app/xray-access.log",
		},
		"outbounds": []map[string]interface{}{
			{"protocol": "freedom", "tag": "direct"},
		},
	}
}

// buildPolicy 根据动态限速值构造 policy 段。
// 始终开启用户级流量统计，限速时额外生成 level=1 的限速策略。
func buildPolicy(dynLimitMbps int) map[string]interface{} {
	levels := map[string]interface{}{
		"0": map[string]interface{}{
			"bufferSize":        0,
			"statsUserUplink":   true,
			"statsUserDownlink": true,
		},
	}
	if dynLimitMbps > 0 {
		ratePerSec := int64(dynLimitMbps) * 1000 * 1000 / 8
		levels["1"] = map[string]interface{}{
			"bufferSize":        ratePerSec,
			"headerLimit":       ratePerSec,
			"uplinkOnly":        0,
			"downlinkOnly":      0,
			"refreshSizeSec":    ratePerSec,
			"statsUserUplink":   true,
			"statsUserDownlink": true,
		}
	}
	return map[string]interface{}{
		"levels": levels,
		"system": map[string]interface{}{
			"statsInboundUplink":   true,
			"statsInboundDownlink": true,
			"statsUserUplink":      true,
			"statsUserDownlink":    true,
		},
	}
}

// sniffingSettings 返回统一的域名嗅探配置。
func sniffingSettings() map[string]interface{} {
	return map[string]interface{}{
		"enabled":      true,
		"destOverride": []string{"http", "tls", "quic"},
	}
}

// vlessRealityBuilder VLESS + REALITY + XTLS-Vision。
type vlessRealityBuilder struct{}

func (b *vlessRealityBuilder) Build(node *model.Node, users []model.User, limitMbps int) ([]byte, error) {
	cfgMap := map[string]interface{}{}
	_ = json.Unmarshal(node.ServerConfig, &cfgMap)
	if cfgMap == nil {
		cfgMap = map[string]interface{}{}
	}

	dest := "gateway.icloud.com:443"
	serverNames := []string{"gateway.icloud.com"}
	var privateKey string
	var shortIDs []string

	if reality, ok := cfgMap["reality"].(map[string]interface{}); ok {
		if v, ok := reality["dest"].(string); ok && v != "" {
			dest = v
		}
		if v, ok := reality["sni"].(string); ok && v != "" {
			serverNames = []string{v}
		}
		if v, ok := reality["server_names"].([]interface{}); ok && len(v) > 0 {
			names := make([]string, 0, len(v))
			for _, n := range v {
				if s, ok := n.(string); ok {
					names = append(names, s)
				}
			}
			if len(names) > 0 {
				serverNames = names
			}
		}
		if v, ok := reality["private_key"].(string); ok && v != "" {
			privateKey = v
		} else if enc, ok := reality["private_key_enc"].(string); ok && enc != "" {
			aesMgr, err := security.NewAESManager(app.Get().Cfg.AESMasterKey)
			if err != nil {
				return nil, fmt.Errorf("初始化 AES 管理器失败: %w", err)
			}
			priv, err := aesMgr.DecryptString(enc)
			if err != nil {
				return nil, fmt.Errorf("解密 REALITY 私钥失败: %w", err)
			}
			privateKey = priv
		}
		if v, ok := reality["short_id"].(string); ok && v != "" {
			shortIDs = []string{v}
		}
		if v, ok := reality["short_ids"].([]interface{}); ok && len(v) > 0 {
			ids := make([]string, 0, len(v))
			for _, id := range v {
				if s, ok := id.(string); ok {
					ids = append(ids, s)
				}
			}
			if len(ids) > 0 {
				shortIDs = ids
			}
		}
	}

	clientLevel := 0
	if limitMbps > 0 {
		clientLevel = 1
	}
	clients := make([]map[string]interface{}, 0, len(users))
	for _, u := range users {
		if u.ID == "" {
			continue
		}
		clients = append(clients, map[string]interface{}{
			"id":    u.ID,
			"flow":  "xtls-rprx-vision",
			"level": clientLevel,
		})
	}

	xray := buildBaseXray()
	xray["inbounds"] = []map[string]interface{}{
		{
			"listen":   "0.0.0.0",
			"port":     node.Port,
			"protocol": "vless",
			"settings": map[string]interface{}{
				"clients":    clients,
				"decryption": "none",
			},
			"streamSettings": map[string]interface{}{
				"network":  "tcp",
				"security": "reality",
				"realitySettings": map[string]interface{}{
					"show":        false,
					"dest":        dest,
					"xver":        0,
					"serverNames": serverNames,
					"privateKey":  privateKey,
					"shortIds":    shortIDs,
				},
			},
			"sniffing": sniffingSettings(),
		},
	}
	xray["policy"] = buildPolicy(limitMbps)
	return json.Marshal(xray)
}

// vmessTCPBuilder VMess over TCP（无额外 TLS，适合内网/测试或配合 CDN 回源）。
type vmessTCPBuilder struct{}

func (b *vmessTCPBuilder) Build(node *model.Node, users []model.User, limitMbps int) ([]byte, error) {
	clientLevel := 0
	if limitMbps > 0 {
		clientLevel = 1
	}
	clients := make([]map[string]interface{}, 0, len(users))
	for _, u := range users {
		if u.ID == "" {
			continue
		}
		clients = append(clients, map[string]interface{}{
			"id":      u.ID,
			"alterId": 0,
			"level":   clientLevel,
		})
	}

	xray := buildBaseXray()
	xray["inbounds"] = []map[string]interface{}{
		{
			"listen":   "0.0.0.0",
			"port":     node.Port,
			"protocol": "vmess",
			"settings": map[string]interface{}{
				"clients": clients,
			},
			"streamSettings": map[string]interface{}{
				"network":  "tcp",
				"security": "none",
			},
			"sniffing": sniffingSettings(),
		},
	}
	xray["policy"] = buildPolicy(limitMbps)
	return json.Marshal(xray)
}

// shadowsocksTCPBuilder Shadowsocks over TCP+UDP。
type shadowsocksTCPBuilder struct{}

func (b *shadowsocksTCPBuilder) Build(node *model.Node, users []model.User, limitMbps int) ([]byte, error) {
	cfgMap := map[string]interface{}{}
	_ = json.Unmarshal(node.ServerConfig, &cfgMap)
	if cfgMap == nil {
		cfgMap = map[string]interface{}{}
	}
	method := "chacha20-ietf-poly1305"
	if v, ok := cfgMap["method"].(string); ok && v != "" {
		method = v
	}
	password := ""
	if v, ok := cfgMap["password"].(string); ok && v != "" {
		password = v
	}
	if password == "" {
		return nil, fmt.Errorf("shadowsocks 节点缺少 password")
	}

	xray := buildBaseXray()
	xray["inbounds"] = []map[string]interface{}{
		{
			"listen":   "0.0.0.0",
			"port":     node.Port,
			"protocol": "shadowsocks",
			"settings": map[string]interface{}{
				"method":   method,
				"password": password,
				"network":  "tcp,udp",
			},
			"streamSettings": map[string]interface{}{
				"network": "tcp",
			},
			"sniffing": sniffingSettings(),
		},
	}
	// Shadowsocks 是共享密码协议，单用户限速无法像 VLESS/VMess 那样按 clients 区分。
	// 为保持行为稳定，这里不强制切到 level=1; policy 保留用于流量统计。
	// 如需限制整体 Shadowsocks 带宽，可通过节点带宽上限 + 动态限速/静态限速组合控制。
	xray["policy"] = buildPolicy(0)
	return json.Marshal(xray)
}

// trojanTLSBuilder Trojan over TLS。
// Trojan 必须依赖 TLS 证书；本实现支持两种来源：
//   1. 节点创建时自动生成自签名证书(server_config.tls.cert_pem + tls.key_enc)
//   2. 管理员在 extra_config.tls 中传入自己的证书(cert_pem/key_pem)
// 如证书缺失或私钥解密失败，Build 返回明确错误而非生成无效配置。
type trojanTLSBuilder struct{}

func (b *trojanTLSBuilder) Build(node *model.Node, users []model.User, limitMbps int) ([]byte, error) {
	cfgMap := map[string]interface{}{}
	_ = json.Unmarshal(node.ServerConfig, &cfgMap)
	if cfgMap == nil {
		cfgMap = map[string]interface{}{}
	}

	tlsCfg, ok := cfgMap["tls"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("Trojan 节点缺少 TLS 证书配置，请先创建/更新节点以生成证书")
	}
	certPEM, _ := tlsCfg["cert_pem"].(string)
	if certPEM == "" {
		return nil, fmt.Errorf("Trojan 节点缺少 TLS 证书(cert_pem)")
	}
	keyPEM := ""
	if v, ok := tlsCfg["key_pem"].(string); ok && v != "" {
		keyPEM = v
	} else if enc, ok := tlsCfg["key_enc"].(string); ok && enc != "" {
		aesMgr, err := security.NewAESManager(app.Get().Cfg.AESMasterKey)
		if err != nil {
			return nil, fmt.Errorf("初始化 AES 管理器失败: %w", err)
		}
		keyPEM, err = aesMgr.DecryptString(enc)
		if err != nil {
			return nil, fmt.Errorf("解密 Trojan TLS 私钥失败: %w", err)
		}
	}
	if keyPEM == "" {
		return nil, fmt.Errorf("Trojan 节点缺少 TLS 私钥(key_pem/key_enc)")
	}

	fallbackDest := "gateway.icloud.com:443"
	if v, ok := cfgMap["fallback_dest"].(string); ok && v != "" {
		fallbackDest = v
	}

	clientLevel := 0
	if limitMbps > 0 {
		clientLevel = 1
	}
	clients := make([]map[string]interface{}, 0, len(users))
	for _, u := range users {
		if u.ID == "" {
			continue
		}
		// 使用用户 UUID 作为 Trojan 密码：唯一、随机、无需额外存储。
		clients = append(clients, map[string]interface{}{
			"password": u.ID,
			"level":    clientLevel,
			"email":    u.ID,
		})
	}

	xray := buildBaseXray()
	xray["inbounds"] = []map[string]interface{}{
		{
			"listen":   "0.0.0.0",
			"port":     node.Port,
			"protocol": "trojan",
			"settings": map[string]interface{}{
				"clients":     clients,
				"fallbacks":   []map[string]interface{}{{"dest": fallbackDest}},
			},
			"streamSettings": map[string]interface{}{
				"network":  "tcp",
				"security": "tls",
				"tlsSettings": map[string]interface{}{
					"certificates": []map[string]interface{}{
						{
							// 内联证书避免 agent 额外写文件, 即拉即用
							"certificate": certPEM,
							"key":         keyPEM,
						},
					},
				},
			},
			"sniffing": sniffingSettings(),
		},
	}
	xray["policy"] = buildPolicy(limitMbps)
	return json.Marshal(xray)
}
