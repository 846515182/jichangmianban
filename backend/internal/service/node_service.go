package service

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/security"
)

// NodeService 节点服务
type NodeService struct {
	repo *repo.NodeRepo
}

// NewNodeService 创建节点服务
func NewNodeService(r *repo.NodeRepo) *NodeService {
	return &NodeService{repo: r}
}

// CreateNodeInput 创建节点入参
type CreateNodeInput struct {
	Name          string                 `json:"name"`
	CountryCode   string                 `json:"country_code"`
	Protocol      string                 `json:"protocol"`
	ServerAddress string                 `json:"server_address"`
	Port          int                    `json:"port"`
	GrpcPort      int                    `json:"grpc_port"`
	TrafficLimit  int64                  `json:"traffic_limit"`
	PlanIDs       []string               `json:"plan_ids"`   // 绑定的套餐ID列表(必填，至少一个)
	ExtraConfig   map[string]interface{} `json:"extra_config"` // 额外配置(如 reality dest, sni 等)
}

// CreateNode 创建节点：
// 1. 自动生成 REALITY 密钥对(X25519)
// 2. 私钥 AES-256-GCM 加密后入库
// 3. 生成 node_token
// 4. 强制绑定至少一个套餐(node_plan_bindings)
func (s *NodeService) CreateNode(in *CreateNodeInput) (*model.Node, error) {
	if in.Name == "" || in.Protocol == "" || in.ServerAddress == "" || in.Port == 0 {
		return nil, errors.New("缺少必填字段")
	}
	// 校验套餐绑定(必填，至少一个)
	if len(in.PlanIDs) == 0 {
		return nil, errors.New("请至少选择一个套餐绑定")
	}

	// NODE-DUP-01 兜底校验: 防止创建重复节点 (name + server_address + port)
	// 数据库已加 partial unique index (uq_nodes_name_addr_port_active) 防并发,
	// 但提前查询可以给出更友好的中文错误, 而不是 raw DB 错误
	var exists int64
	if err := app.Get().DB.Model(&model.Node{}).
		Where("name = ? AND server_address = ? AND port = ? AND is_deleted = false",
			in.Name, in.ServerAddress, in.Port).
		Count(&exists).Error; err != nil {
		return nil, fmt.Errorf("校验节点唯一性失败: %w", err)
	}
	if exists > 0 {
		return nil, fmt.Errorf("节点已存在: 名称=%s 地址=%s:%d, 请勿重复创建(可编辑现有节点或先删除旧节点)", in.Name, in.ServerAddress, in.Port)
	}

	// [P1-同机多节点] 校验 name 唯一(uk_nodes_name 唯一索引), 提前给友好错误而非 raw DB 错误
	var nameExists int64
	if err := app.Get().DB.Model(&model.Node{}).
		Where("name = ? AND is_deleted = false", in.Name).
		Count(&nameExists).Error; err != nil {
		return nil, fmt.Errorf("校验节点名称唯一性失败: %w", err)
	}
	if nameExists > 0 {
		return nil, fmt.Errorf("节点名称「%s」已存在, 同机多节点请用不同名称(如 美国01/美国02)", in.Name)
	}

	// [P1-同机多节点] 校验 (server_address, port) 不与已有节点冲突,
	// 同地址+同端口会导致部署时端口冲突或运行时容器 bind 失败
	var addrPortExists int64
	if err := app.Get().DB.Model(&model.Node{}).
		Where("server_address = ? AND port = ? AND is_deleted = false",
			in.ServerAddress, in.Port).
		Count(&addrPortExists).Error; err != nil {
		return nil, fmt.Errorf("校验节点地址端口失败: %w", err)
	}
	if addrPortExists > 0 {
		return nil, fmt.Errorf("地址 %s:%d 已被其他节点占用, 同机多节点需用不同端口(如 443/8443)", in.ServerAddress, in.Port)
	}

	// 验证协议是否支持
	supportedProtocols := map[string]bool{
		"vless":       true,
		"vmess":       true,
		"trojan":      true,
		"shadowsocks": true,
		"ss":          true,
	}
	if !supportedProtocols[strings.ToLower(in.Protocol)] {
		return nil, errors.New("不支持的协议类型")
	}

	aesMgr, err := security.NewAESManager(app.Get().Cfg.AESMasterKey)
	if err != nil {
		return nil, err
	}

	// 1. 生成 REALITY 密钥对
	privB64, pubB64, err := generateRealityKeypair()
	if err != nil {
		return nil, err
	}

	// 2. 私钥 AES 加密
	encPriv, err := aesMgr.EncryptString(privB64)
	if err != nil {
		return nil, err
	}

	// 3. 生成 node_token
	nodeToken, err := generateNodeToken()
	if err != nil {
		return nil, err
	}

	// 4. 组装 server_config(JSONB)
	// REALITY 默认 dest/sni = gateway.icloud.com(www.microsoft.com 的 AkamaiGHost
	// TLS 实现与 REALITY 不兼容，会导致 handshakeStatus: false)
	shortID, err := generateShortID()
	if err != nil {
		return nil, err
	}
	realityCfg := map[string]interface{}{
		"public_key":      pubB64,
		"private_key_enc": encPriv,
		"short_id":        shortID,
		"sni":             "gateway.icloud.com",
		"dest":            "gateway.icloud.com:443",
	}
	cfgMap := map[string]interface{}{
		"protocol":       in.Protocol,
		"server_address": in.ServerAddress,
		"port":           in.Port,
		"reality":        realityCfg,
	}

	// 5. 根据协议生成认证凭证(uuid/password)
	switch strings.ToLower(in.Protocol) {
	case "vless", "vmess":
		cfgMap["uuid"] = uuid.NewString()
	case "trojan", "shadowsocks", "ss":
		password, err := generateRandomPassword()
		if err != nil {
			return nil, err
		}
		cfgMap["password"] = password
	}

	for k, v := range in.ExtraConfig {
		// reality 字段做深度合并(保留自动生成的密钥对)，其他字段直接覆盖
		if k == "reality" {
			if extraReality, ok := v.(map[string]interface{}); ok {
				if curReality, ok := cfgMap["reality"].(map[string]interface{}); ok {
					for rk, rv := range extraReality {
						curReality[rk] = rv
					}
					continue
				}
			}
		}
		cfgMap[k] = v
	}

	// VLESS+REALITY: 校验 dest 兼容性(防止 AkamaiGHost 等不兼容服务器导致握手失败)
	if strings.ToLower(in.Protocol) == "vless" {
		if dest := extractRealityDest(cfgMap); dest != "" {
			if err := CheckRealityDest(dest); err != nil {
				return nil, fmt.Errorf("REALITY dest 校验失败: %w", err)
			}
		}
	}

	cfgJSON, err := json.Marshal(cfgMap)
	if err != nil {
		return nil, err
	}

	node := &model.Node{
		Name:          in.Name,
		CountryCode:   in.CountryCode,
		Protocol:      in.Protocol,
		ServerAddress: in.ServerAddress,
		Port:          in.Port,
		GrpcPort:      in.GrpcPort,
		TrafficLimit:  in.TrafficLimit,
		IsEnabled:     true,
		NodeToken:     nodeToken,
		ServerConfig:  datatypes.JSON(cfgJSON),
	}

	// 事务: 创建节点 + 写入套餐绑定
	err = app.Get().DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(node).Error; err != nil {
			return err
		}
		return s.repo.ReplacePlanBindings(tx, node.ID, in.PlanIDs)
	})
	if err != nil {
		return nil, err
	}
	return node, nil
}

// UpdateNodeInput 更新节点入参
type UpdateNodeInput struct {
	Name          *string                `json:"name"`
	CountryCode   *string                `json:"country_code"`
	ServerAddress *string                `json:"server_address"`
	Port          *int                   `json:"port"`
	GrpcPort      *int                   `json:"grpc_port"`
	TrafficLimit  *int64                 `json:"traffic_limit"`
	PlanIDs       *[]string              `json:"plan_ids"`   //不为 nil 时整体替换绑定
	IsEnabled     *bool                  `json:"is_enabled"`
	ExtraConfig   map[string]interface{} `json:"extra_config"`
}

// UpdateNode 更新节点
func (s *NodeService) UpdateNode(id string, in *UpdateNodeInput) (*model.Node, error) {
	node, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		node.Name = *in.Name
	}
	if in.CountryCode != nil {
		node.CountryCode = *in.CountryCode
	}
	if in.ServerAddress != nil {
		node.ServerAddress = *in.ServerAddress
	}
	if in.Port != nil {
		node.Port = *in.Port
	}
	if in.GrpcPort != nil {
		node.GrpcPort = *in.GrpcPort
	}
	if in.TrafficLimit != nil {
		node.TrafficLimit = *in.TrafficLimit
	}
	if in.IsEnabled != nil {
		node.IsEnabled = *in.IsEnabled
	}
	if in.ExtraConfig != nil {
		var existing map[string]interface{}
		if err := json.Unmarshal(node.ServerConfig, &existing); err != nil {
			if logger := app.Get().Logger; logger != nil {
				logger.Warn("解析节点 ServerConfig 失败，使用空配置继续", zap.String("node_id", id), zap.Error(err))
			}
		}
		if existing == nil {
			existing = map[string]interface{}{}
		}
		for k, v := range in.ExtraConfig {
			if k == "reality" {
				if extraReality, ok := v.(map[string]interface{}); ok {
					if curReality, ok := existing["reality"].(map[string]interface{}); ok {
						protectedKeys := map[string]bool{
							"public_key":      true,
							"private_key_enc": true,
							"short_id":        true,
						}
						for rk, rv := range extraReality {
							if !protectedKeys[rk] {
								curReality[rk] = rv
							}
						}
						continue
					}
				}
			}
			existing[k] = v
		}
		// VLESS+REALITY: 更新 dest 时校验兼容性
		if strings.ToLower(node.Protocol) == "vless" {
			if dest := extractRealityDest(existing); dest != "" {
				if err := CheckRealityDest(dest); err != nil {
					return nil, fmt.Errorf("REALITY dest 校验失败: %w", err)
				}
			}
		}
		b, err := json.Marshal(existing)
		if err != nil {
			if logger := app.Get().Logger; logger != nil {
				logger.Error("序列化节点 ServerConfig 失败", zap.String("node_id", id), zap.Error(err))
			}
			return nil, fmt.Errorf("序列化节点配置失败")
		}
		node.ServerConfig = datatypes.JSON(b)
	}

	// 事务: 更新节点 + 替换套餐绑定(仅当 PlanIDs 非 nil 时)
	err = app.Get().DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(node).Error; err != nil {
			return err
		}
		if in.PlanIDs != nil {
			if len(*in.PlanIDs) == 0 {
				return errors.New("至少绑定一个套餐")
			}
			return s.repo.ReplacePlanBindings(tx, node.ID, *in.PlanIDs)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return node, nil
}

// DeleteNode 软删除节点，并执行联动清理:
// 1. 软删除节点(is_deleted=true)
// 2. 清零 online 状态(避免后台显示 stale 在线)
// 3. 软删除 user_nodes 关联(避免孤儿记录)
// 4. 清理 node_plan_bindings(物理删除)
// 5. 清理 Redis 残留 key(心跳/状态/配置版本/用户指纹)
// 注意: node-agent 侧在心跳收到 NotFound 后会自行停止 Xray(见 node_agent/main.go handleFatalShutdown)
//
// P0-N5: 所有 DB 操作改为单事务包裹, 任一步失败整体回滚, 避免半删状态。
// Redis 缓存清理放在事务外(事务成功后才清), 防止 DB 回滚但 Redis 已清的不一致。
func (s *NodeService) DeleteNode(id string) error {
	// 先查出节点信息(软删除前)，用于清理 SSH host key 指纹
	node, err := s.repo.GetByID(id)
	if err != nil {
		return err
	}

	// 单事务包裹所有 DB 操作, 任一步失败整体回滚
	err = app.Get().DB.Transaction(func(tx *gorm.DB) error {
		// 1. 软删除节点(is_deleted=true)
		if err := tx.Model(&model.Node{}).Where("id = ? AND is_deleted = false", id).
			Update("is_deleted", true).Error; err != nil {
			return err
		}
		// 2. 清零 online(不过滤 is_deleted，因为节点刚被软删除)
		if err := tx.Model(&model.Node{}).Where("id = ?", id).
			UpdateColumn("online", false).Error; err != nil {
			return err
		}
		// 3. 软删除 user_nodes 关联(避免孤儿记录)
		if err := tx.Model(&model.UserNode{}).Where("node_id = ? AND is_deleted = false", id).
			Update("is_deleted", true).Error; err != nil {
			return err
		}
		// 4. 物理删除 node_plan_bindings(绑定关系不需要软删除保留)
		if err := tx.Where("node_id = ?", id).Delete(&model.NodePlanBinding{}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Redis 缓存清理(事务外, 仅在 DB 操作成功后执行)
	if rdb := app.Get().RDB; rdb != nil {
		ctx := context.Background()
		keys := []string{
			fmt.Sprintf("node:heartbeat:%s", id),
			fmt.Sprintf("node:status:%s", id),
			fmt.Sprintf("node:configver:%s", id),
			fmt.Sprintf("node:usershash:%s", id),
		}
		// 清理 SSH host key 指纹(部署 TOFU + 终端)，避免节点重装后无法重新部署/连接
		// 部署 key: deploy:{host}:{host}:{sshPort}，终端 key: sshterm:{host}:{sshPort}
		// 由于 SSH 端口是部署时临时传入(非节点固定字段)，用 SCAN 匹配包含 host 的 key 清理
		if node.ServerAddress != "" {
			host := node.ServerAddress
			scanPatterns := []string{
				fmt.Sprintf("deploy:*%s*", host),
				fmt.Sprintf("sshterm:*%s*", host),
			}
			for _, pattern := range scanPatterns {
				iter := rdb.Scan(ctx, 0, pattern, 100).Iterator()
				for iter.Next(ctx) {
					keys = append(keys, iter.Val())
				}
				if err := iter.Err(); err != nil {
					if logger := app.Get().Logger; logger != nil {
						logger.Warn("Redis SCAN 清理 SSH 指纹失败", zap.String("pattern", pattern), zap.Error(err))
					}
				}
			}
		}
		for _, k := range keys {
			if err := rdb.Del(ctx, k).Err(); err != nil {
				if logger := app.Get().Logger; logger != nil {
					logger.Warn("Redis DEL 失败", zap.String("key", k), zap.Error(err))
				}
			}
		}
	}
	return nil
}

// RotateToken 轮换节点通信 token
// 修复 P1: 轮换后清 Redis 缓存, 让 agent 下次心跳拉新配置
// 注意: agent 配置文件里的旧 token 仍会导致注册失败, 必须重新部署 agent
// (用旧 token 注册失败 30 次 -> log.Fatalf -> docker restart 死循环刷日志)
//
// P0-N3: 双 token 宽限期。轮换后旧 token 仍写入 Redis (key=node:old_token:{nodeID},
// TTL=24h), 供 traffic_service 校验时做宽限期。同时打警告日志提醒运维重新部署 agent,
// 否则 agent 用旧 token 注册将失败, 流量上报中断。
//
// TODO(model): Node model 应新增 PreviousNodeToken + TokenRotatedAt 字段做持久化宽限期,
// 当前 model 不在修改清单内, 暂用 Redis 兜底 (Redis 重启后宽限期失效, 但 agent 通常
// 已在 24h 内完成重部署)。
func (s *NodeService) RotateToken(id string) (string, error) {
	node, err := s.repo.GetByID(id)
	if err != nil {
		return "", err
	}
	oldToken := node.NodeToken
	tok, err := generateNodeToken()
	if err != nil {
		return "", err
	}
	if err := s.repo.UpdateToken(id, tok); err != nil {
		return "", err
	}
	// 清 Redis 配置缓存, 让 agent 下次心跳时重新拉取配置(含新 token)
	// 若 agent 不支持热更新 token, 此操作无害, 仍需重新部署 agent 更新配置文件
	if rdb := app.Get().RDB; rdb != nil {
		ctx := context.Background()
		rdb.Del(ctx, "node:configver:"+id)
		rdb.Del(ctx, "node:usershash:"+id)
		// 双 token 宽限期: 旧 token 写 Redis, TTL=24h,
		// 供 traffic_service (不在修改清单) 校验流量上报 token 时做宽限。
		// 24h 内 agent 旧 token 上报的流量仍被接受, 避免轮换瞬间流量丢失。
		if oldToken != "" {
			rdb.Set(ctx, fmt.Sprintf("node:old_token:%s", id), oldToken, 24*time.Hour)
		}
	}
	// 警告: 轮换后必须重新部署 agent, 否则用旧 token 上报将失败
	if logger := app.Get().Logger; logger != nil {
		logger.Warn("节点 token 已轮换, 需重新部署 agent 否则流量上报将失败",
			zap.String("node_id", id),
			zap.String("note", "旧 token 已写入 Redis 24h 宽限期, 但 agent 配置文件未更新会持续注册失败"))
	}
	return tok, nil
}

// GetDecryptedRealityPrivateKey 解密并返回 REALITY 私钥(仅超级管理员可调用)
func (s *NodeService) GetDecryptedRealityPrivateKey(id string) (string, error) {
	node, err := s.repo.GetByID(id)
	if err != nil {
		return "", err
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(node.ServerConfig, &cfg); err != nil {
		return "", err
	}
	reality, ok := cfg["reality"].(map[string]interface{})
	if !ok {
		return "", errors.New("节点未配置 REALITY")
	}
	enc, ok := reality["private_key_enc"].(string)
	if !ok || enc == "" {
		return "", errors.New("缺少加密私钥")
	}
	aesMgr, err := security.NewAESManager(app.Get().Cfg.AESMasterKey)
	if err != nil {
		return "", err
	}
	priv, err := aesMgr.DecryptString(enc)
	if err != nil {
		return "", err
	}
	return priv, nil
}

// generateRealityKeypair 生成 REALITY(X25519) 密钥对，返回 base64url 编码的私钥与公钥
func generateRealityKeypair() (privB64, pubB64 string, err error) {
	curve := ecdh.X25519()
	privKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	privB64 = base64.RawURLEncoding.EncodeToString(privKey.Bytes())
	pubB64 = base64.RawURLEncoding.EncodeToString(privKey.PublicKey().Bytes())
	return privB64, pubB64, nil
}

// generateNodeToken 生成 32 字节随机节点通信 token(hex 编码)
func generateNodeToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// generateShortID 生成 8 字节 short_id(hex 编码)
func generateShortID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// generateRandomPassword 生成 16 字节随机密码(base64url 编码)，用于 trojan/ss 协议
func generateRandomPassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
