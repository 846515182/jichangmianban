package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/security"
	"nexus-panel/internal/service"
	nexuspb "nexus-panel/proto"
)

// NodeServiceServer 节点服务 gRPC 实现
// 负责: 节点注册/心跳/配置下发/状态上报
type NodeServiceServer struct {
	nexuspb.UnimplementedNodeServiceServer

	nodeRepo  *repo.NodeRepo
	userRepo  *repo.UserRepo
	logger    *zap.Logger
	loadScorer *service.LoadScorer
}

// NewNodeServiceServer 创建节点服务
func NewNodeServiceServer(nodeRepo *repo.NodeRepo, userRepo *repo.UserRepo, logger *zap.Logger) *NodeServiceServer {
	return &NodeServiceServer{
		nodeRepo:   nodeRepo,
		userRepo:   userRepo,
		logger:     logger,
		loadScorer: service.NewLoadScorer(),
	}
}

// Register 节点注册: 用 node_token 找到节点，更新 online/last_seen_at/version
// 返回 NodeInfo(含解密后的 REALITY 明文私钥，节点 agent 启动 Xray 时需要)
func (s *NodeServiceServer) Register(ctx context.Context, req *nexuspb.RegisterRequest) (*nexuspb.RegisterResponse, error) {
	if req.GetNodeToken() == "" {
		return nil, status.Error(codes.Unauthenticated, "缺少 node_token")
	}

	node, err := s.nodeRepo.GetByToken(req.GetNodeToken())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.Unauthenticated, "节点 token 无效")
		}
		s.logger.Error("注册查询节点失败", zap.Error(err))
		return nil, status.Error(codes.Internal, "查询节点失败")
	}
	if !node.IsEnabled {
		return nil, status.Error(codes.PermissionDenied, "节点已禁用")
	}

	now := time.Now()
	if err := s.nodeRepo.UpdateOnline(node.ID, true, req.GetVersion(), now); err != nil {
		s.logger.Error("更新节点在线状态失败", zap.String("node_id", node.ID), zap.Error(err))
	}
	node.Online = true
	node.Version = req.GetVersion()
	node.LastSeenAt = &now

	// P1-Register校验: 移除 agent 上报 server_address/grpc_port 的覆盖逻辑。
	// 节点地址由管理员在面板配置, agent 不应修改。旧版允许 agent 覆盖会导致:
	//   1. 被攻破的节点 agent 可伪造地址把流量统计指向错误节点
	//   2. NAT/CDN 环境下 agent 上报的内网/回环地址覆盖公网地址, 面板 PingNode 失败
	//   3. 管理员修改地址后 agent 下次注册又改回去, 配置漂移

	info, err := buildNodeInfoWithDecryptedKey(node)
	if err != nil {
		s.logger.Error("构造 NodeInfo 失败", zap.String("node_id", node.ID), zap.Error(err))
		return nil, status.Error(codes.Internal, "构造节点信息失败")
	}

	return &nexuspb.RegisterResponse{
		Resp: &nexuspb.Response{Code: 0, Message: "ok"},
		Node: info,
	}, nil
}

// Heartbeat 节点心跳: 更新 last_seen_at/online/运行时信息(存 Redis)
// 每次心跳检测节点配置版本变更，触发 agent 重新拉取 Xray 配置
func (s *NodeServiceServer) Heartbeat(ctx context.Context, req *nexuspb.HeartbeatRequest) (*nexuspb.HeartbeatResponse, error) {
	if req.GetNodeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "缺少 node_id")
	}
	node, err := s.nodeRepo.GetByID(req.GetNodeId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "节点不存在")
		}
		return nil, status.Error(codes.Internal, "查询节点失败")
	}
	if !node.IsEnabled {
		return nil, status.Error(codes.PermissionDenied, "节点已禁用")
	}
	if req.GetNodeToken() == "" {
		return nil, status.Error(codes.Unauthenticated, "缺少 node_token")
	}
	if req.GetNodeToken() != node.NodeToken {
		return nil, status.Error(codes.Unauthenticated, "node_token 不匹配")
	}

	now := time.Now()
	if err := s.nodeRepo.UpdateOnline(node.ID, true, req.GetVersion(), now); err != nil {
		s.logger.Warn("心跳更新节点状态失败", zap.String("node_id", node.ID), zap.Error(err))
	}

	// 运行时信息存 Redis(节点 agent 仪表盘用)
	// 修复 NODE-HEALTH-01: 新增 proxy_reachable / proxy_latency_ms / proxy_error 字段
	// 让面板区分"agent 进程可达"和"代理服务可用"
	if rdb := app.Get().RDB; rdb != nil {
		hb := map[string]interface{}{
			"node_id":            node.ID,
			"version":            req.GetVersion(),
			"cpu_usage":          req.GetCpuUsage(),
			"memory_usage":       req.GetMemoryUsage(),
			"memory_total":       req.GetMemoryTotal(),
			"online_connections": req.GetOnlineConnections(),
			"uptime_seconds":     req.GetUptimeSeconds(),
			"updated_at":         now.Unix(),
			// 修复 NODE-HEALTH-01: 代理自检结果, 面板用它显示真实在线状态
			"proxy_reachable": req.GetProxyReachable(),
			"proxy_latency":   req.GetProxyLatencyMs(),
			"proxy_error":     req.GetProxyError(),
		}
		key := fmt.Sprintf("node:heartbeat:%s", node.ID)
		if err := rdb.HSet(ctx, key, hb).Err(); err != nil {
			s.logger.Warn("写入心跳 Redis 失败", zap.String("key", key), zap.Error(err))
		}
		// 修复 NODE-REDIS-TTL: 原 2min TTL 与 MarkStale 5min 阈值不一致, 心跳延迟 >2min 时
		// Redis key 过期导致 runtime 全 0, 而 DB online 仍 true, 状态割裂 2-5min。
		// 调到 10min(2 倍 MarkStale 阈值), 心跳恢复后自动刷新。
		_ = rdb.Expire(ctx, key, 10*time.Minute).Err()

		// [节点容量管理] 心跳上报后计算负载评分, 更新 load_status 到 DB + Redis
		// 评分基于 CPU/内存/连接数/带宽, 用于订阅智能调度(过滤满载) + 自动踢人(超载移除用户)
		if s.loadScorer != nil {
			snap := &service.HeartbeatSnapshot{
				NodeID:            node.ID,
				CpuUsage:          req.GetCpuUsage(),
				MemoryUsage:       req.GetMemoryUsage(),
				OnlineConnections: req.GetOnlineConnections(),
				UpdatedAt:         now.Unix(),
			}
			// 从 Redis hash 读 speed_bps(由 traffic_service 写入)
			if speedStr, err := rdb.HGet(ctx, key, "speed_bps").Result(); err == nil && speedStr != "" {
				if v, e := service.ParseInt64(speedStr); e == nil {
					snap.SpeedBps = v
				}
			}
			score, _ := s.loadScorer.UpdateNodeLoadStatus(ctx, node, snap)

			// [自动踢人保护] 节点满载(score>=1.0)且有容量上限 → 触发配置刷新
			// 下次 GetConfig 调用时 listActiveUsersForNode 会按 MaxClients 截断用户列表,
			// 多余用户的凭证从 Xray 配置移除, agent 重载 Xray 后被踢用户断开连接
			if s.loadScorer.ShouldEvict(node, score) {
				evictCount := s.loadScorer.EvictCount(node, snap)
				s.logger.Info("节点超载触发踢人保护",
					zap.String("node_id", node.ID),
					zap.String("node_name", node.Name),
					zap.Float64("score", score.Score),
					zap.Int32("online", snap.OnlineConnections),
					zap.Int("max", node.MaxClients),
					zap.Int("evict_count", evictCount))
				// 清 Redis configver + usershash 强制下次心跳触发重拉
				rdb.Del(ctx, fmt.Sprintf("node:configver:%s", node.ID))
				rdb.Del(ctx, fmt.Sprintf("node:usershash:%s", node.ID))
				// 更新 node.UpdatedAt 让 configVer 变化
				s.nodeRepo.TouchEnabled(node.ID)
			}
		}
	}

	// 修复 NODE-CONFIGVER-01: 原 configVer = UpdatedAt.Unix() 秒级精度,
	// 同秒内多次更新会漏判(如先改 name 再改 port), agent 拿到旧配置。
	// 改用 UpdatedAt.UnixNano() 纳秒精度, 配合 GORM 微秒精度足够区分。
	configVer := strconv.FormatInt(node.UpdatedAt.UnixNano(), 10)
	configChanged := false
	// 修复 NODE-REDIS-DEGRADE: 原 Redis 不可用时 configChanged 恒 false,
	// 用户超额后 agent 永远不重拉配置, 超额用户继续可用。
	// 降级策略: Redis 不可用时强制 configChanged=true, 让 agent 每次(30s)重拉一次,
	// 虽然增加开销, 但保证状态最终一致。
	rdb := app.Get().RDB
	if rdb != nil {
		key := fmt.Sprintf("node:configver:%s", node.ID)
		oldVer, _ := rdb.Get(ctx, key).Result()
		if oldVer != configVer {
			configChanged = true
			if err := rdb.Set(ctx, key, configVer, 0).Err(); err != nil {
				// Set 失败(如 Redis 只读), 下次心跳仍会触发, 可接受
				s.logger.Warn("写入 configVer 失败", zap.String("key", key), zap.Error(err))
			}
		}
	} else {
		// P1-Redis-configChanged: Redis 不可用时不再强制 configChanged=true,
		// 改用节点 ID hash 做随机退避(约 10% 概率触发拉配置, 平均每 10 次心跳拉一次),
		// 避免所有节点在 Redis 故障期间每次心跳都全量拉配置导致 DB 压力激增。
		// 用 fnv hash 让不同节点错开, 避免同一时刻全节点涌入 GetConfig。
		if hashString(node.ID)%10 == 0 {
			configChanged = true
		}
		s.logger.Warn("Redis 不可用, 用节点 ID hash 退避拉配置",
			zap.String("node_id", node.ID),
			zap.Bool("config_changed", configChanged))
	}

	// 用户变更检测：对当前活跃用户列表做指纹哈希，与 Redis 缓存比较
	// 修复 BIZ-FATAL-02: 原有实现只比较 node.UpdatedAt，用户增删改时不会触发 ConfigChanged
	usersChanged := false
	if rdb != nil {
		users, err := s.listActiveUsersForNode(node)
		if err == nil {
			// 计算用户列表指纹：user_id 排序拼接后取 hash
			ids := make([]string, 0, len(users))
			for _, u := range users {
				ids = append(ids, u.ID)
			}
			sort.Strings(ids)
			fingerprint := strings.Join(ids, ",")
			if fingerprint == "" {
				fingerprint = "empty"
			}
			hash := strconv.FormatInt(int64(hashString(fingerprint)), 10)

			key := fmt.Sprintf("node:usershash:%s", node.ID)
			oldHash, _ := rdb.Get(ctx, key).Result()
			if oldHash != hash {
				usersChanged = true
				if err := rdb.Set(ctx, key, hash, 0).Err(); err != nil {
					s.logger.Warn("写入 usershash 失败", zap.String("key", key), zap.Error(err))
				}
				s.logger.Info("检测到用户列表变更，触发配置更新",
				zap.String("node_id", node.ID),
				zap.Int("user_count", len(users)))
			}
		}
	} else {
		// P1-Redis-configChanged: 同 configChanged, Redis 不可用时用节点 ID hash 退避,
		// 避免每次心跳都触发全量用户列表查询(在高用户数场景下 DB 压力大)。
		if hashString(node.ID)%10 == 0 {
			usersChanged = true
		}
	}

	return &nexuspb.HeartbeatResponse{
		Resp:          &nexuspb.Response{Code: 0, Message: "ok"},
		ConfigChanged: configChanged || usersChanged,
		UsersChanged:  configChanged || usersChanged,
		ServerTime:    now.Unix(),
	}, nil
}

// GetConfig 节点拉取完整 Xray 服务端配置
// 同时校验 node_id + node_token，并将全量用户凭证嵌入 meta 字段
func (s *NodeServiceServer) GetConfig(ctx context.Context, req *nexuspb.GetConfigRequest) (*nexuspb.NodeConfigResponse, error) {
	if req.GetNodeId() == "" || req.GetNodeToken() == "" {
		return nil, status.Error(codes.Unauthenticated, "缺少 node_id 或 node_token")
	}
	node, err := s.nodeRepo.GetByID(req.GetNodeId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.Unauthenticated, "节点不存在")
		}
		return nil, status.Error(codes.Internal, "查询节点失败")
	}
	if node.NodeToken != req.GetNodeToken() {
		return nil, status.Error(codes.Unauthenticated, "node_token 与 node_id 不匹配")
	}
	if !node.IsEnabled {
		return nil, status.Error(codes.PermissionDenied, "节点已禁用")
	}

	// 修复 SEC-P0-03 + BIZ-FATAL-01: 按节点套餐绑定过滤用户，且只返回未过期/未超额用户
	users, err := s.listActiveUsersForNode(node)
	if err != nil {
		s.logger.Error("拉取配置时查询用户失败", zap.Error(err))
		return nil, status.Error(codes.Internal, "查询用户失败")
	}
	s.logger.Info("GetConfig 下发用户凭证",
		zap.String("node_id", node.ID),
		zap.Int("user_count", len(users)))

	xrayCfg, err := s.buildXrayConfig(node, users)
	if err != nil {
		s.logger.Error("构造 Xray 配置失败", zap.String("node_id", node.ID), zap.Error(err))
		return nil, status.Error(codes.Internal, "构造配置失败")
	}

	// meta: 嵌入用户凭证列表
	// 修复 P0-14: 旧实现下发 user_id/username/traffic_limit/traffic_used 等敏感信息,
	// 被攻破的节点可拿这些信息冒充用户或分析业务数据。
	// 节点 agent 只需要 UUID(Xray clients[].id)和 flow, 其余字段对节点无业务价值。
	// 计费/到期判定均在面板侧完成, 节点侧无需感知。
	creds := make([]map[string]interface{}, 0, len(users))
	for _, u := range users {
		creds = append(creds, map[string]interface{}{
			"uuid": u.ID, // users.id 即为 uuid, 仅用于 Xray clients[].id
		})
	}
	metaMap := map[string]interface{}{
		"node_id":   node.ID,
		"node_name": node.Name,
		"users":     creds,
	}
	metaBytes, _ := json.Marshal(metaMap)

	// P1-configVersion: 用节点 updated_at 的 UnixNano 作为版本号,
	// 与 Heartbeat 中的 configVer 保持一致(都用纳秒), 避免秒级精度下同秒多次更新漏判。
	configVersion := strconv.FormatInt(node.UpdatedAt.UnixNano(), 10)

	return &nexuspb.NodeConfigResponse{
		Resp:          &nexuspb.Response{Code: 0, Message: "ok"},
		ConfigVersion: configVersion,
		XrayConfig:    string(xrayCfg),
		Meta:          string(metaBytes),
	}, nil
}

// ReportStatus 节点上报运行状态(写入 Redis: node:status:{node_id})
func (s *NodeServiceServer) ReportStatus(ctx context.Context, req *nexuspb.ReportStatusRequest) (*nexuspb.Response, error) {
	if req.GetNodeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "缺少 node_id")
	}
	node, err := s.nodeRepo.GetByID(req.GetNodeId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "节点不存在")
		}
		return nil, status.Error(codes.Internal, "查询节点失败")
	}
	if !node.IsEnabled {
		return nil, status.Error(codes.PermissionDenied, "节点已禁用")
	}
	if req.GetNodeToken() == "" {
		return nil, status.Error(codes.Unauthenticated, "缺少 node_token")
	}
	if req.GetNodeToken() != node.NodeToken {
		return nil, status.Error(codes.Unauthenticated, "node_token 不匹配")
	}

	rdb := app.Get().RDB
	if rdb == nil {
		// Redis 不可用时降级: 仅刷新 last_seen_at(不覆盖 version)
		_ = s.nodeRepo.TouchOnline(req.GetNodeId(), time.Now())
		return &nexuspb.Response{Code: 0, Message: "ok(redis unavailable)"}, nil
	}

	now := time.Now()
	st := map[string]interface{}{
		"node_id":            req.GetNodeId(),
		"cpu_usage":          req.GetCpuUsage(),
		"memory_usage":       req.GetMemoryUsage(),
		"online_connections": req.GetOnlineConnections(),
		"uptime_seconds":     req.GetUptimeSeconds(),
		"updated_at":         now.Unix(),
	}
	key := fmt.Sprintf("node:status:%s", req.GetNodeId())
	if err := rdb.HSet(ctx, key, st).Err(); err != nil {
		s.logger.Warn("写入节点状态 Redis 失败", zap.String("key", key), zap.Error(err))
		return nil, status.Error(codes.Internal, "写入状态失败")
	}
	_ = rdb.Expire(ctx, key, 5*time.Minute).Err()

	// 顺便刷新 last_seen_at(不覆盖 version)
	_ = s.nodeRepo.TouchOnline(req.GetNodeId(), now)

	return &nexuspb.Response{Code: 0, Message: "ok"}, nil
}

// buildNodeInfoWithDecryptedKey 构造 NodeInfo，并将 server_config 中的
// reality.private_key_enc 解密成明文放到 reality.private_key 字段(node-agent 需要明文私钥)
func buildNodeInfoWithDecryptedKey(node *model.Node) (*nexuspb.NodeInfo, error) {
	info := &nexuspb.NodeInfo{
		Id:            node.ID,
		Name:          node.Name,
		CountryCode:   node.CountryCode,
		Protocol:      protocolToProto(node.Protocol),
		ServerAddress: node.ServerAddress,
		Port:          int32(node.Port),
		ServerConfig:  string(node.ServerConfig),
		TrafficLimit:  node.TrafficLimit,
		TrafficUsed:   node.TrafficUsed,
		IsEnabled:     node.IsEnabled,
		NodeToken:     node.NodeToken,
		GrpcPort:      int32(node.GrpcPort),
		Online:        node.Online,
		Version:       node.Version,
	}
	if node.LastSeenAt != nil {
		info.LastSeenAt = node.LastSeenAt.Unix()
	}

	// 解密 REALITY 私钥并写入 server_config 的 reality.private_key 字段
	cfgMap := map[string]interface{}{}
	if err := json.Unmarshal(node.ServerConfig, &cfgMap); err != nil {
		return info, nil // server_config 非法 JSON 时直接返回原样
	}
	if reality, ok := cfgMap["reality"].(map[string]interface{}); ok {
		if enc, ok := reality["private_key_enc"].(string); ok && enc != "" {
			aesMgr, err := security.NewAESManager(app.Get().Cfg.AESMasterKey)
			if err != nil {
				return nil, fmt.Errorf("初始化 AES 管理器失败: %w", err)
			}
			priv, err := aesMgr.DecryptString(enc)
			if err != nil {
				return nil, fmt.Errorf("解密 REALITY 私钥失败: %w", err)
			}
			reality["private_key"] = priv
			delete(reality, "private_key_enc") // 移除加密字段，避免明文泄露源
			cfgMap["reality"] = reality
			if b, err := json.Marshal(cfgMap); err == nil {
				info.ServerConfig = string(b)
			}
		}
	}
	return info, nil
}

// buildXrayConfig 构造 VLESS+REALITY+XTLS-Vision 的 Xray 服务端配置 JSON
func (s *NodeServiceServer) buildXrayConfig(node *model.Node, users []model.User) ([]byte, error) {
	// 解析节点 server_config 取 REALITY 配置
	var cfgMap map[string]interface{}
	_ = json.Unmarshal(node.ServerConfig, &cfgMap)
	if cfgMap == nil {
		cfgMap = map[string]interface{}{}
	}

	// REALITY 字段: dest / serverNames / privateKey / shortIds
	// 注意: www.microsoft.com (AkamaiGHost) 的 TLS 响应与 REALITY 不兼容，
	// 会导致 handshakeStatus: false。使用 gateway.icloud.com 作为默认 dest。
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
		// 优先使用已解密的明文私钥(正常不会出现)；若仍是加密字段则解密
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

	// clients: 每个用户一条，UUID=user.id，flow=xtls-rprx-vision
	// [动态限速] 限速值由 LoadScorer 按用途+负载自动计算, 不用手动设
	// 所有用 level=1, policy.levels.1 配动态限速值; 用途为 download 时不限速
	clientLevel := 0
	// 优先用动态限速(从 Redis 缓存读), 若管理员手动设了 SpeedLimitMbps 则用手动值(兼容)
	dynLimit := 0
	if s.loadScorer != nil {
		dynLimit = s.loadScorer.GetDynamicLimitFromCache(context.Background(), node.ID)
	}
	// 限速生效条件: 动态限速>0 或 手动限速>0, 且用途不是 download(下载不限速)
	applyLimit := (dynLimit > 0 || node.SpeedLimitMbps > 0) && node.UsageType != "download"
	if applyLimit {
		clientLevel = 1
	}
	clients := make([]map[string]interface{}, 0, len(users))
	for _, u := range users {
		if u.ID == "" {
			continue
		}
		client := map[string]interface{}{
			"id":    u.ID,
			"flow":  "xtls-rprx-vision",
			"level": clientLevel,
		}
		clients = append(clients, client)
	}

	xray := map[string]interface{}{
		"log": map[string]interface{}{
			"loglevel": "warning",
		},
		"inbounds": []map[string]interface{}{
			{
				"listen": "0.0.0.0",
				"port":   node.Port,
				"protocol": "vless",
				"settings": map[string]interface{}{
					"clients":     clients,
					"decryption":  "none",
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
				"sniffing": map[string]interface{}{
					"enabled":      true,
					"destOverride": []string{"http", "tls", "quic"},
				},
			},
		},
		"outbounds": []map[string]interface{}{
			{"protocol": "freedom", "tag": "direct"},
			// [节点策略控制] block outbound: 用于路由规则拦截视频/下载流量
			{"protocol": "blackhole", "tag": "block"},
		},
	}

	// [动态限速] 限速值优先级: 动态计算 > 手动设置 > 不限速
	// 动态限速由 LoadScorer 心跳时按用途+负载自动算, 写入 Redis 缓存
	// 这里从缓存读, 生成 Xray policy 配置
	if applyLimit {
		limitMbps := dynLimit
		if limitMbps <= 0 {
			limitMbps = node.SpeedLimitMbps // 回退到手动值
		}
		if limitMbps > 0 {
			// Mbps → bytes/s (Xray policy bufferSize 单位)
			ratePerSec := int64(limitMbps) * 1000 * 1000 / 8
			xray["policy"] = map[string]interface{}{
				"levels": map[string]interface{}{
					"0": map[string]interface{}{"bufferSize": 0},
					"1": map[string]interface{}{
						"bufferSize":     ratePerSec,
						"headerLimit":    ratePerSec,
						"uplinkOnly":     0,
						"downlinkOnly":   0,
						"refreshSizeSec": ratePerSec,
					},
				},
			}
		}
	}

	// [节点策略控制] 用途路由: 根据节点 usage_type 添加路由规则
	// browsing(仅浏览): 拦截视频流媒体 + 大文件下载
	// video(视频): 允许视频流媒体, 拦截大文件下载
	// download(允许下载): 无限制
	// general(通用): 无限制
	routingRules := buildUsageRoutingRules(node.UsageType)
	if len(routingRules) > 0 {
		xray["routing"] = map[string]interface{}{
			"domainStrategy": "AsIs",
			"rules":          routingRules,
		}
	}

	return json.Marshal(xray)
}

// buildUsageRoutingRules 根据节点用途类型生成 Xray 路由规则
// 用于限制节点用途(仅浏览/视频/下载), 实现线路分级运营
func buildUsageRoutingRules(usageType string) []map[string]interface{} {
	switch usageType {
	case "browsing":
		// 仅浏览: 拦截视频流媒体 + 大文件下载
		// 视频域名: youtube/netflix/bilibili/iqiyi/tencent video 等
		// 下载域名: 大型 CDN/网盘
		return []map[string]interface{}{
			{
				"type":        "field",
				"outboundTag": "block",
				"domain": []string{
					// 视频流媒体
					"youtube.com", "googlevideo.com", "ytimg.com",
					"netflix.com", "nflxvideo.net", "nflximg.net",
					"bilibili.com", "bilivideo.com", "bilivideo.cn",
					"iqiyi.com", "iq.com",
					"v.qq.com", "wetv.vip",
					"youku.com", "mgtv.com",
					"disneyplus.com", "hbomax.com", "hulu.com",
					"primevideo.com", "twitch.tv",
					// 下载/网盘
					"pan.baidu.com", "aliyundrive.net", "alipan.com",
					"onedrive.live.com",
					// 大文件 CDN
					"github.com", "objects.githubusercontent.com",
					"release-assets.com",
				},
			},
		}
	case "video":
		// 视频: 允许视频流媒体, 拦截大文件下载
		return []map[string]interface{}{
			{
				"type":        "field",
				"outboundTag": "block",
				"domain": []string{
					// 下载/网盘(允许视频但禁止下载大文件)
					"pan.baidu.com", "aliyundrive.net", "alipan.com",
					"onedrive.live.com",
					"github.com", "objects.githubusercontent.com",
					"release-assets.com",
				},
			},
		}
	case "download", "general", "":
		// 允许下载/通用: 无限制
		return nil
	default:
		return nil
	}
}

// protocolToProto 字符串协议名转 proto 枚举
func protocolToProto(p string) nexuspb.Protocol {
	switch p {
	case "vmess":
		return nexuspb.Protocol_PROTOCOL_VMESS
	case "vless":
		return nexuspb.Protocol_PROTOCOL_VLESS
	case "trojan":
		return nexuspb.Protocol_PROTOCOL_TROJAN
	case "shadowsocks", "ss":
		return nexuspb.Protocol_PROTOCOL_SHADOWSOCKS
	default:
		return nexuspb.Protocol_PROTOCOL_UNSPECIFIED
	}
}

// listActiveUsersForNode 查询节点可见的活跃用户
// 优先使用 node_plan_bindings 命中(用户 plan_id 在节点绑定列表中)；
// 若节点未配置任何绑定，则回退到所有活跃用户(避免历史节点失联)
func (s *NodeServiceServer) listActiveUsersForNode(node *model.Node) ([]model.User, error) {
	planIDs, err := s.nodeRepo.GetPlanIDsByNode(node.ID)
	if err != nil {
		return nil, err
	}
	var users []model.User
	if len(planIDs) > 0 {
		users, err = s.userRepo.ListActiveForPlans(planIDs)
	} else {
		// 回退: 节点未配置绑定 → 返回所有活跃用户
		users, err = s.userRepo.ListActive()
	}
	if err != nil {
		return nil, err
	}

	// [自动踢人保护] 若节点配置了 MaxClients 且当前用户数超限, 截断用户列表。
	// 多出的用户凭证不会下发到 Xray, agent 重载后这些用户的连接被断开。
	// 截断策略: 按 created_at 升序保留最早注册的用户(老用户优先), 踢出最后加入的新用户。
	if node.MaxClients > 0 && len(users) > node.MaxClients {
		// 按 CreatedAt 升序排序, 保留前 MaxClients 个
		sort.Slice(users, func(i, j int) bool {
			return users[i].CreatedAt.Before(users[j].CreatedAt)
		})
		evicted := len(users) - node.MaxClients
		s.logger.Info("节点容量限制截断用户",
			zap.String("node_id", node.ID),
			zap.String("node_name", node.Name),
			zap.Int("total_users", len(users)),
			zap.Int("max_clients", node.MaxClients),
			zap.Int("evicted", evicted))
		users = users[:node.MaxClients]
	}

	return users, nil
}

// hashString 计算字符串的 FNV-1a 哈希
func hashString(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}
