package handler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/response"
	"nexus-panel/internal/service"
)

// AdminNodeHandler 管理端节点处理器
type AdminNodeHandler struct {
	nodeService *service.NodeService
	nodeRepo    *repo.NodeRepo
}

// NewAdminNodeHandler 创建管理端节点处理器
func NewAdminNodeHandler(s *service.NodeService, r *repo.NodeRepo) *AdminNodeHandler {
	return &AdminNodeHandler{nodeService: s, nodeRepo: r}
}

// NodeList [10] GET /api/v1/admin/nodes
// 节点列表(分页 + 关键字)
// 增强: 读取 Redis 心跳返回实时 CPU/内存/连接数/速度；附带按 server_address 聚合的流量汇总
func (h *AdminNodeHandler) NodeList(c *gin.Context) {
	page, size := parsePage(c)
	keyword := c.Query("keyword")
	list, total, err := h.nodeRepo.List(page, size, keyword)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}

	rdb := app.Get().RDB
	ctx := context.Background()

	// 修复 PERF-NPLUS1-01: 旧实现对每个节点循环调 GetPlanIDsByNode + readNodeRuntime
	// (readNodeRuntime 内部又 4 次 Redis 往返), N 个节点 = N 次 DB + 4N 次 Redis, 全部串行。
	// 现改为:
	//   1) 1 次 SQL WHERE node_id IN (...) 拿全部 plan_ids
	//   2) 1 个 Redis Pipeline 拿全部节点的 heartbeat + speed_snap
	//   3) 1 个 Redis Pipeline 回写全部节点的 speed_snap
	nodeIDs := make([]string, 0, len(list))
	for i := range list {
		nodeIDs = append(nodeIDs, list[i].ID)
	}
	planIDsMap, _ := h.nodeRepo.GetPlanIDsByNodeIDs(nodeIDs)

	// 批量预取 Redis 心跳 + 速度快照
	hbMap := make(map[string]map[string]string, len(list))
	snapMap := make(map[string]map[string]string, len(list))
	if rdb != nil && len(nodeIDs) > 0 {
		pipe := rdb.Pipeline()
		// 修复 BUILD-REDIS-01 (P0): go-redis v9.5.1 中 HGetAll 返回 *MapStringStringCmd,
		// 而非旧版的 *StringStringMapCmd; 旧代码使用了不存在的类型名导致编译失败。
		hbCmds := make([]*redis.MapStringStringCmd, len(list))
		snapCmds := make([]*redis.MapStringStringCmd, len(list))
		for i, id := range nodeIDs {
			hbCmds[i] = pipe.HGetAll(ctx, fmt.Sprintf("node:heartbeat:%s", id))
			snapCmds[i] = pipe.HGetAll(ctx, fmt.Sprintf("node:speed_snap:%s", id))
		}
		// 修复 BUILD-REDIS-02 (P0): pipe.Exec 返回 ([]Cmder, error), 需用 2 个变量接收,
		// 旧代码 `_ = pipe.Exec(ctx)` 只有 1 个变量导致 "assignment mismatch" 编译错误。
		_, _ = pipe.Exec(ctx)
		for i, id := range nodeIDs {
			hb, _ := hbCmds[i].Result()
			hbMap[id] = hb
			snap, _ := snapCmds[i].Result()
			snapMap[id] = snap
		}
	}

	// 为每个节点附加实时状态(CPU/内存/连接/速度)和套餐绑定
	items := make([]gin.H, 0, len(list))
	// 收集需要回写快照的节点(speed_bps 兜底算法用: 记录 admin 视角的 traffic_used + ts)
	type snapWrite struct {
		key         string
		trafficUsed int64
	}
	snapWrites := make([]snapWrite, 0, len(list))
	for i := range list {
		n := &list[i]
		item := gin.H{
			"id":             n.ID,
			"name":           n.Name,
			"country_code":   n.CountryCode,
			"protocol":       n.Protocol,
			"server_address": n.ServerAddress,
			"port":           n.Port,
			"grpc_port":      n.GrpcPort,
			"traffic_limit":  n.TrafficLimit,
			"traffic_used":   n.TrafficUsed,
			"is_enabled":     n.IsEnabled,
			"online":         n.Online,
			// [S2 fix 2026-07-14] 隐藏 node_token 防止泄露
			//"node_token":     n.NodeToken,
			"server_config":  n.ServerConfig,
			"version":        n.Version,
			"last_seen_at":   n.LastSeenAt,
		}

		// 套餐绑定 ID 列表(前端编辑时回显) - 已批量预取
		item["plan_ids"] = planIDsMap[n.ID]

		// 实时状态: 从预取结果计算
		if rdb != nil {
			item["runtime"] = h.buildNodeRuntimeFromCache(hbMap[n.ID], snapMap[n.ID], n.TrafficUsed)
			snapWrites = append(snapWrites, snapWrite{
				key:         fmt.Sprintf("node:speed_snap:%s", n.ID),
				trafficUsed: n.TrafficUsed,
			})
		} else {
			item["runtime"] = gin.H{
				"cpu_usage":          0,
				"memory_usage":       0,
				"online_connections": 0,
				"speed_bps":          0,
				"uptime_seconds":     0,
				"updated_at":         0,
				"proxy_reachable":    int64(1),
				"proxy_error":        "",
			}
		}
		items = append(items, item)
	}

	// 批量回写速度快照(供 speed_bps 字段缺失时的兜底算法用)
	// snap = {traffic_used, ts}: admin 两次调用间 DB 流量增量 / 时间差 = 估算速率
	if rdb != nil && len(snapWrites) > 0 {
		now := time.Now().Unix()
		pipe := rdb.Pipeline()
		for _, w := range snapWrites {
			pipe.HSet(ctx, w.key, "traffic_used", w.trafficUsed, "ts", now)
			pipe.Expire(ctx, w.key, 10*time.Minute)
		}
		_, _ = pipe.Exec(ctx)
	}

	// 按 server_address 聚合流量(管理员统一流量展示)
	trafficGroups, _ := h.nodeRepo.TrafficSummaryByServer()

	response.OK(c, gin.H{
		"list":           items,
		"total":          total,
		"traffic_groups": trafficGroups,
	})
}

// buildNodeRuntimeFromCache 从已预取的心跳数据计算运行时状态(纯计算, 无 IO)。
// 修复 PERF-NPLUS1-01: 抽离自 readNodeRuntime, 供 NodeList 批量预取后复用。
//
// 修复 NODE-SPEED-01 (P1): speed_bps 主路径读 heartbeat.speed_bps(traffic_service
// 在 agent 上报流量时算的真实瞬时速率); 兜底: 字段缺失/为 0(新面板刚部署未收到首次
// 上报、agent 无流量不上报、heartbeat TTL 过期重建)时, 回退到 snap 算法(用 admin
// 两次调用的 DB traffic_used 差值 / 时间差估算), 避免速度恒为 0。
func (h *AdminNodeHandler) buildNodeRuntimeFromCache(hb map[string]string, snap map[string]string, dbTrafficUsed int64) gin.H {
	rt := gin.H{
		"cpu_usage":          0,
		"memory_usage":       0,
		"online_connections": 0,
		"speed_bps":          0,
		"uptime_seconds":     0,
		"updated_at":         0,
		// NODE-PROXY-01: 透传 agent 上报的代理可达性状态
		// 默认可达(1), 避免 agent 老版本无该字段时误报异常
		"proxy_reachable": int64(1),
		"proxy_error":    "",
	}
	if len(hb) == 0 {
		return rt
	}

	// 解析心跳字段
	cpuUsage, _ := strconv.ParseFloat(hb["cpu_usage"], 64)
	memUsage, _ := strconv.ParseFloat(hb["memory_usage"], 64)
	memTotal, _ := strconv.ParseInt(hb["memory_total"], 10, 64)
	onlineConn, _ := strconv.ParseInt(hb["online_connections"], 10, 64)
	uptime, _ := strconv.ParseInt(hb["uptime_seconds"], 10, 64)
	hbUpdatedAt, _ := strconv.ParseInt(hb["updated_at"], 10, 64)
	// 主路径: speed_bps 由 traffic_service 写入 heartbeat hash, 直接读
	speedBps, _ := strconv.ParseInt(hb["speed_bps"], 10, 64)
	// NODE-PROXY-01: 读取代理可达性 (agent 上报, 0=不可达 1=可达)
	proxyReachable := int64(1)
	if v, ok := hb["proxy_reachable"]; ok && v != "" {
		proxyReachable, _ = strconv.ParseInt(v, 10, 64)
	}
	proxyError := hb["proxy_error"]

	// 兜底: speed_bps 缺失或为 0 时, 回退到 snap 算法估算
	// 场景: 新面板刚部署 traffic_service 还没收到首次流量上报 / agent 低负载无流量不上报
	if speedBps == 0 && len(snap) > 0 {
		prevUsed, _ := strconv.ParseInt(snap["traffic_used"], 10, 64)
		prevTs, _ := strconv.ParseInt(snap["ts"], 10, 64)
		curTs := time.Now().Unix()
		dt := curTs - prevTs
		if dt > 0 && dbTrafficUsed >= prevUsed {
			speedBps = (dbTrafficUsed - prevUsed) / dt
		}
	}

	rt["cpu_usage"] = cpuUsage
	rt["memory_usage"] = memUsage
	rt["memory_total"] = memTotal
	rt["online_connections"] = onlineConn
	rt["uptime_seconds"] = uptime
	rt["updated_at"] = hbUpdatedAt
	rt["speed_bps"] = speedBps
	rt["proxy_reachable"] = proxyReachable
	rt["proxy_error"] = proxyError
	return rt
}

// nodeCreateInput 创建节点请求体
// 扩展 service.CreateNodeInput, 增加节点容量/限速/用途字段
// (LoadStatus 由心跳评分自动计算, 不接收前端入参)
type nodeCreateInput struct {
	service.CreateNodeInput
	MaxClients       int    `json:"max_clients"`        // 最大用户数, 0=不限
	MaxBandwidthMbps int    `json:"max_bandwidth_mbps"` // 节点带宽上限Mbps, 0=不限
	CpuThreshold     int    `json:"cpu_threshold"`      // CPU超载阈值%, 默认80
	SpeedLimitMbps   int    `json:"speed_limit_mbps"`   // 单用户限速Mbps, 0=不限
	UsageType        string `json:"usage_type"`         // 动态限速开关 limited(开启)/general(关闭)
}

// nodeUpdateInput 更新节点请求体
// 扩展 service.UpdateNodeInput, 增加节点容量/限速/用途字段
// (指针类型, 支持 partial update; LoadStatus 不接收, 由心跳自动计算)
type nodeUpdateInput struct {
	service.UpdateNodeInput
	MaxClients       *int    `json:"max_clients"`        // 最大用户数, 0=不限
	MaxBandwidthMbps *int    `json:"max_bandwidth_mbps"` // 节点带宽上限Mbps, 0=不限
	CpuThreshold     *int    `json:"cpu_threshold"`      // CPU超载阈值%, 默认80
	SpeedLimitMbps   *int    `json:"speed_limit_mbps"`   // 单用户限速Mbps, 0=不限
	UsageType        *string `json:"usage_type"`         // 动态限速开关 limited(开启)/general(关闭)
}

// allowedUsageTypes 节点动态限速开关白名单
// limited=开启动态限速(自动按负载限速), general=关闭(不限速)
var allowedUsageTypes = map[string]bool{
	"general": true,
	"limited": true,
}

// validateNodeCapacityFields 校验并归一化创建请求中的容量/限速/用途字段。
// 返回归一化后的值与校验错误信息(空串表示通过)。
// 约定:
//   - MaxClients/MaxBandwidthMbps/SpeedLimitMbps: 0=不限, 仅校验非负
//   - CpuThreshold: 0 视为默认 80, 有效范围 1-100
//   - UsageType: 空串视为默认 general, 仅允许 limited/general
//   - LoadStatus 由心跳评分自动计算, 不在此处理
func validateNodeCapacityFields(maxClients, maxBandwidthMbps, cpuThreshold, speedLimitMbps int, usageType string) (int, int, int, int, string, string) {
	if maxClients < 0 {
		return 0, 0, 0, 0, "", "max_clients 不能为负数"
	}
	if maxBandwidthMbps < 0 {
		return 0, 0, 0, 0, "", "max_bandwidth_mbps 不能为负数"
	}
	if speedLimitMbps < 0 {
		return 0, 0, 0, 0, "", "speed_limit_mbps 不能为负数"
	}
	if cpuThreshold == 0 {
		cpuThreshold = 80 // 默认 CPU 超载阈值
	}
	if cpuThreshold < 1 || cpuThreshold > 100 {
		return 0, 0, 0, 0, "", "cpu_threshold 取值范围为 1-100"
	}
	if usageType == "" {
		usageType = "general"
	}
	if !allowedUsageTypes[usageType] {
		return 0, 0, 0, 0, "", "usage_type 只允许 limited/general"
	}
	return maxClients, maxBandwidthMbps, cpuThreshold, speedLimitMbps, usageType, ""
}

// validateNodeCapacityFieldsPtr 校验更新请求中的容量/限速/用途字段(仅校验显式传入的字段)。
// 返回校验错误信息(空串表示通过); nil 字段跳过校验。
func validateNodeCapacityFieldsPtr(maxClients *int, maxBandwidthMbps *int, cpuThreshold *int, speedLimitMbps *int, usageType *string) string {
	if maxClients != nil && *maxClients < 0 {
		return "max_clients 不能为负数"
	}
	if maxBandwidthMbps != nil && *maxBandwidthMbps < 0 {
		return "max_bandwidth_mbps 不能为负数"
	}
	if speedLimitMbps != nil && *speedLimitMbps < 0 {
		return "speed_limit_mbps 不能为负数"
	}
	if cpuThreshold != nil {
		v := *cpuThreshold
		if v == 0 {
			v = 80 // 0 视为默认阈值
		}
		if v < 1 || v > 100 {
			return "cpu_threshold 取值范围为 1-100"
		}
	}
	if usageType != nil {
		v := *usageType
		if v == "" {
			v = "general"
		}
		if !allowedUsageTypes[v] {
			return "usage_type 只允许 limited/general"
		}
	}
	return ""
}

// NodeCreate [11] POST /api/v1/admin/nodes
// 创建节点(自动生成 REALITY 密钥对 + node_token)
func (h *AdminNodeHandler) NodeCreate(c *gin.Context) {
	var in nodeCreateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	in.ServerAddress = strings.TrimSpace(in.ServerAddress)
	// 校验节点容量/限速/用途字段
	maxClients, maxBandwidthMbps, cpuThreshold, speedLimitMbps, usageType, msg := validateNodeCapacityFields(
		in.MaxClients, in.MaxBandwidthMbps, in.CpuThreshold, in.SpeedLimitMbps, in.UsageType)
	if msg != "" {
		response.FailMsg(c, response.CodeParamError, msg)
		return
	}
	node, err := h.nodeService.CreateNode(&in.CreateNodeInput)
	if err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	// service.CreateNode 入参(service.CreateNodeInput)不含容量字段, 需在此单独持久化。
	// 重新拉取节点拿全 DB 默认值(GrpcPort/CpuThreshold/UsageType/LoadStatus 等),
	// 避免直接 Save 用零值覆盖 DB 默认值(如 GrpcPort=50051 被写成 0、LoadStatus 被清空)。
	// LoadStatus 由心跳评分自动计算, 此处不设置。
	node, err = h.nodeRepo.GetByID(node.ID)
	if err != nil {
		response.FailMsg(c, response.CodeServerError, "读取新建节点失败: "+err.Error())
		return
	}
	node.MaxClients = maxClients
	node.MaxBandwidthMbps = maxBandwidthMbps
	node.CpuThreshold = cpuThreshold
	node.SpeedLimitMbps = speedLimitMbps
	node.UsageType = usageType
	if err := h.nodeRepo.Update(node); err != nil {
		response.FailMsg(c, response.CodeServerError, "保存节点容量配置失败: "+err.Error())
		return
	}
	response.OK(c, node)
}

// NodeDetail GET /api/v1/admin/nodes/:id
// 获取单个节点详情(部署前校验/编辑回显等场景需要)
//
// 安全说明: NodeList(列表) 已隐藏 node_token 防止批量泄露, 但 NodeDetail 是
// 管理员单查场景(查看部署信息/编辑回显), 需要返回完整 node_token 和 server_config:
//   - 部署信息弹窗生成 .env.node 需要 NODE_TOKEN, 否则 agent 注册失败死循环
//   - parseRealityInfo 从 server_config 提取 public_key/short_id 供部署命令展示
//
// 路由层已有 AdminAuth 中间件保护, 非管理员无法访问。
// 旧版 P0-N8 清空了 node_token/server_config, 导致前端部署信息弹窗
// NODE_TOKEN 永远为空, 管理员复制的部署命令无法注册节点。
func (h *AdminNodeHandler) NodeDetail(c *gin.Context) {
	id := c.Param("id")
	node, err := h.nodeRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.Fail(c, response.CodeDBError)
		return
	}
	// 保留完整字段: 管理员单查场景需要 node_token 和 server_config 生成部署信息
	response.OK(c, node)
}

// NodeMonitor GET /api/v1/admin/nodes/monitor
// 节点负载监控大盘: 返回所有节点的实时负载评分 + 各维度占比 + 状态汇总,
// 供前端 Monitor.vue 监控大盘展示节点负载情况。
//
// 数据来源:
//   - DB: 所有未删除节点(含容量配置 MaxClients/MaxBandwidthMbps/CpuThreshold 等)
//   - Redis node:heartbeat:{id}: 心跳快照(CPU/内存/连接数/速度/更新时间)
//   - Redis node:loadscore:{id}: 评分缓存(score/status, 由心跳处理流程写入)
//
// 评分计算复用 service.LoadScorer.CalculateScore, 保证与订阅调度/踢人逻辑一致;
// 用 2 个 Redis Pipeline 批量预取心跳+评分缓存, 避免 N+1 Redis 往返。
func (h *AdminNodeHandler) NodeMonitor(c *gin.Context) {
	logger := app.Get().Logger

	// 1) 查询所有未删除节点
	var list []model.Node
	if err := app.Get().DB.Where("is_deleted = false").Order("created_at DESC").Find(&list).Error; err != nil {
		if logger != nil {
			logger.Warn("监控大盘查询节点失败", zap.Error(err))
		}
		response.Fail(c, response.CodeDBError)
		return
	}

	rdb := app.Get().RDB
	ctx := context.Background()

	// 2) 批量预取所有节点的心跳 + 评分缓存(单个 Pipeline, 避免 N+1 Redis 往返)
	hbMap := make(map[string]map[string]string, len(list))
	lsMap := make(map[string]map[string]string, len(list))
	if rdb != nil && len(list) > 0 {
		hbCmds := make([]*redis.MapStringStringCmd, len(list))
		lsCmds := make([]*redis.MapStringStringCmd, len(list))
		pipe := rdb.Pipeline()
		for i, n := range list {
			hbCmds[i] = pipe.HGetAll(ctx, fmt.Sprintf("node:heartbeat:%s", n.ID))
			lsCmds[i] = pipe.HGetAll(ctx, fmt.Sprintf("node:loadscore:%s", n.ID))
		}
		_, _ = pipe.Exec(ctx)
		for i, n := range list {
			hb, _ := hbCmds[i].Result()
			hbMap[n.ID] = hb
			ls, _ := lsCmds[i].Result()
			lsMap[n.ID] = ls
		}
	}

	// 3) 逐节点计算评分 + 组装监控数据; 评分复用 LoadScorer.CalculateScore
	scorer := service.NewLoadScorer()
	nodes := make([]gin.H, 0, len(list))
	total, onlineCnt, offlineCnt := 0, 0, 0
	idleCnt, normalCnt, busyCnt, fullCnt := 0, 0, 0, 0
	for i := range list {
		n := &list[i]

		// 心跳快照 → 实时评分(含各维度 ratio), 与订阅调度/踢人逻辑同源
		snap := h.buildHeartbeatSnapshot(hbMap[n.ID])
		score := scorer.CalculateScore(n, snap)

		loadScore := score.Score
		loadStatus := score.Status
		// [动态限速] 当前生效的单用户限速(Mbps)。在线节点实时算, 离线节点读缓存。
		// 管理员不手动设值, 系统按 usage_type 定基础速度 + 负载动态调整, 0 表示不限速。
		dynamicLimit := 0
		// 无心跳(离线)时 CalculateScore 返回 0/idle, 回退到评分缓存展示上次负载,
		// 避免离线节点一律显示 idle 误导运维。
		if snap == nil {
			if v, e := strconv.ParseFloat(lsMap[n.ID]["score"], 64); e == nil && v > 0 {
				loadScore = v
			}
			if s := lsMap[n.ID]["status"]; s != "" {
				loadStatus = s
			}
			if v, e := strconv.Atoi(lsMap[n.ID]["dynamic_limit_mbps"]); e == nil {
				dynamicLimit = v
			}
		} else {
			dynamicLimit = service.CalcDynamicSpeedLimit(n, score, snap)
		}

		nodes = append(nodes, gin.H{
			"id":                  n.ID,
			"name":                n.Name,
			"server_address":      n.ServerAddress,
			"port":                n.Port,
			"online":              n.Online,
			"load_status":         loadStatus,
			"load_score":          loadScore,
			"max_clients":         n.MaxClients,
			"max_bandwidth_mbps":  n.MaxBandwidthMbps,
			"cpu_threshold":       n.CpuThreshold,
			"usage_type":          n.UsageType,
			"dynamic_limit_mbps":  dynamicLimit,
			"runtime":             h.buildMonitorRuntime(hbMap[n.ID]),
			"ratios": gin.H{
				"client_ratio":    score.ClientRatio,
				"bandwidth_ratio": score.BandwidthRto,
				"cpu_ratio":       score.CpuRatio,
				"mem_ratio":       score.MemRatio,
			},
		})

		// 4) 汇总统计: 在线/离线 + 各负载状态数量
		total++
		if n.Online {
			onlineCnt++
		} else {
			offlineCnt++
		}
		switch loadStatus {
		case service.StatusIdle:
			idleCnt++
		case service.StatusNormal:
			normalCnt++
		case service.StatusBusy:
			busyCnt++
		case service.StatusFull:
			fullCnt++
		}
	}

	response.OK(c, gin.H{
		"nodes": nodes,
		"summary": gin.H{
			"total":   total,
			"online":  onlineCnt,
			"offline": offlineCnt,
			"idle":    idleCnt,
			"normal":  normalCnt,
			"busy":    busyCnt,
			"full":    fullCnt,
		},
	})
}

// buildHeartbeatSnapshot 从已预取的心跳 hash 构造 LoadScorer 用的心跳快照。
// 心跳为空(节点离线/未上报)时返回 nil, CalculateScore 会据此返回 idle。
func (h *AdminNodeHandler) buildHeartbeatSnapshot(hb map[string]string) *service.HeartbeatSnapshot {
	if len(hb) == 0 {
		return nil
	}
	snap := &service.HeartbeatSnapshot{}
	if v, err := strconv.ParseFloat(hb["cpu_usage"], 64); err == nil {
		snap.CpuUsage = v
	}
	if v, err := strconv.ParseFloat(hb["memory_usage"], 64); err == nil {
		snap.MemoryUsage = v
	}
	if v, err := strconv.ParseInt(hb["online_connections"], 10, 64); err == nil {
		snap.OnlineConnections = int32(v)
	}
	if v, err := strconv.ParseInt(hb["speed_bps"], 10, 64); err == nil {
		snap.SpeedBps = v
	}
	if v, err := strconv.ParseInt(hb["updated_at"], 10, 64); err == nil {
		snap.UpdatedAt = v
	}
	return snap
}

// buildMonitorRuntime 从已预取的心跳 hash 提取监控大盘所需的实时运行时指标。
// 仅返回监控面板关心的 5 个字段(cpu/内存/连接数/速度/更新时间), 不含代理可达性等部署信息。
func (h *AdminNodeHandler) buildMonitorRuntime(hb map[string]string) gin.H {
	rt := gin.H{
		"cpu_usage":          float64(0),
		"memory_usage":       float64(0),
		"online_connections": int64(0),
		"speed_bps":          int64(0),
		"updated_at":         int64(0),
	}
	if len(hb) == 0 {
		return rt
	}
	if v, err := strconv.ParseFloat(hb["cpu_usage"], 64); err == nil {
		rt["cpu_usage"] = v
	}
	if v, err := strconv.ParseFloat(hb["memory_usage"], 64); err == nil {
		rt["memory_usage"] = v
	}
	if v, err := strconv.ParseInt(hb["online_connections"], 10, 64); err == nil {
		rt["online_connections"] = v
	}
	if v, err := strconv.ParseInt(hb["speed_bps"], 10, 64); err == nil {
		rt["speed_bps"] = v
	}
	if v, err := strconv.ParseInt(hb["updated_at"], 10, 64); err == nil {
		rt["updated_at"] = v
	}
	return rt
}

// NodeUpdate [12] PUT /api/v1/admin/nodes/:id
func (h *AdminNodeHandler) NodeUpdate(c *gin.Context) {
	id := c.Param("id")
	var in nodeUpdateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	if in.ServerAddress != nil {
		trimmed := strings.TrimSpace(*in.ServerAddress)
		*in.ServerAddress = trimmed
	}
	// 校验节点容量/限速/用途字段(仅校验显式传入的字段)
	if msg := validateNodeCapacityFieldsPtr(in.MaxClients, in.MaxBandwidthMbps, in.CpuThreshold, in.SpeedLimitMbps, in.UsageType); msg != "" {
		response.FailMsg(c, response.CodeParamError, msg)
		return
	}
	node, err := h.nodeService.UpdateNode(id, &in.UpdateNodeInput)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	// service.UpdateNode 入参不含容量字段, 需在此单独持久化(仅当传入了任意容量字段时)。
	// node 已由 service 从 DB 加载完整字段, 直接设置容量字段后 Save 不会覆盖 LoadStatus 等自动字段。
	// LoadStatus 由心跳评分自动计算, 此处不更新。
	hasCapacityUpdate := in.MaxClients != nil || in.MaxBandwidthMbps != nil ||
		in.CpuThreshold != nil || in.SpeedLimitMbps != nil || in.UsageType != nil
	if hasCapacityUpdate {
		if in.MaxClients != nil {
			node.MaxClients = *in.MaxClients
		}
		if in.MaxBandwidthMbps != nil {
			node.MaxBandwidthMbps = *in.MaxBandwidthMbps
		}
		if in.CpuThreshold != nil {
			ct := *in.CpuThreshold
			if ct == 0 {
				ct = 80 // 0 视为默认阈值
			}
			node.CpuThreshold = ct
		}
		if in.SpeedLimitMbps != nil {
			node.SpeedLimitMbps = *in.SpeedLimitMbps
		}
		if in.UsageType != nil {
			ut := *in.UsageType
			if ut == "" {
				ut = "general"
			}
			node.UsageType = ut
		}
		if err := h.nodeRepo.Update(node); err != nil {
			response.FailMsg(c, response.CodeServerError, "保存节点容量配置失败: "+err.Error())
			return
		}
	}
	response.OK(c, node)
}

// NodeDelete [13] DELETE /api/v1/admin/nodes/:id
func (h *AdminNodeHandler) NodeDelete(c *gin.Context) {
	id := c.Param("id")
	if err := h.nodeService.DeleteNode(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.Fail(c, response.CodeServerError)
		return
	}
	response.OKMsg(c, "已删除")
}

// RotateToken [23] POST /api/v1/admin/nodes/:id/rotate-token
// 轮换节点通信 token(仅超级管理员，路由层加 RBAC)
func (h *AdminNodeHandler) RotateToken(c *gin.Context) {
	id := c.Param("id")
	tok, err := h.nodeService.RotateToken(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.Fail(c, response.CodeServerError)
		return
	}
	response.OK(c, gin.H{"node_token": tok})
}

// PingNode POST /api/v1/admin/nodes/:id/ping
// 主动 TCP 探测节点 gRPC 端口，验证节点是否真实在线。
// 解决节点服务器重装后 node_agent 不再运行，但面板依赖 8 分钟心跳超时
// 才标记离线的问题——管理员可手动点击"检测连接"立即确认。
func (h *AdminNodeHandler) PingNode(c *gin.Context) {
	id := c.Param("id")
	node, err := h.nodeRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.Fail(c, response.CodeDBError)
		return
	}

	// [P0-PingNode] 旧版拨号 node.GrpcPort(50051), 但 agent 不监听 50051,
	// gRPC 是 agent→面板单向出站, 节点上无任何服务监听 50051 → 必然失败 → 误标离线。
	// 改为拨号 node.Port(Xray 代理端口, 映射到宿主机), TCP 可达即说明 Xray 在跑。
	addr := net.JoinHostPort(node.ServerAddress, strconv.Itoa(node.Port))
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		// TCP 连接失败 → 节点代理端口不可达，立即标记离线
		now := time.Now()
		_ = h.nodeRepo.MarkOffline(node.ID)
		response.OK(c, gin.H{
			"reachable": false,
			"error":     err.Error(),
			"action":    "marked_offline",
			"checked_at": now,
		})
		return
	}
	conn.Close()

	// TCP 连接成功 → Xray 代理端口可达，刷新 last_seen_at + online=true
	now := time.Now()
	_ = h.nodeRepo.UpdateOnline(node.ID, true, node.Version, now)

	// 同步清除 Redis configver/usershash 缓存，让节点下次心跳时重新拉取配置
	if rdb := app.Get().RDB; rdb != nil {
		ctx := context.Background()
		rdb.Del(ctx, fmt.Sprintf("node:configver:%s", node.ID))
		rdb.Del(ctx, fmt.Sprintf("node:usershash:%s", node.ID))
	}

	response.OK(c, gin.H{
		"reachable":  true,
		"action":     "refreshed_online",
		"checked_at": now,
	})
}
