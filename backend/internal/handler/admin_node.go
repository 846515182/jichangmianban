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
	"gorm.io/gorm"

	"nexus-panel/internal/app"
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

// readNodeRuntime 从 Redis 读取节点实时运行状态并计算实时速度
// 速度计算: 对比本次请求与上次请求(redis 快照)的 traffic_used 差值 / 时间差
func (h *AdminNodeHandler) readNodeRuntime(ctx context.Context, rdb *redis.Client, nodeID string, dbTrafficUsed int64) gin.H {
	hbKey := fmt.Sprintf("node:heartbeat:%s", nodeID)
	hb, err := rdb.HGetAll(ctx, hbKey).Result()
	rt := gin.H{
		"cpu_usage":          0,
		"memory_usage":       0,
		"online_connections": 0,
		"speed_bps":          0,
		"uptime_seconds":     0,
		"updated_at":         0,
	}
	if err != nil || len(hb) == 0 {
		return rt
	}

	// 计算实时速度: 与上次管理端查询的快照对比
	snapKey := fmt.Sprintf("node:speed_snap:%s", nodeID)
	snap, _ := rdb.HGetAll(ctx, snapKey).Result()
	rt = h.buildNodeRuntimeFromCache(hb, snap, dbTrafficUsed)

	// 更新快照(用 DB traffic_used，TTL 10 分钟)
	now := time.Now().Unix()
	rdb.HSet(ctx, snapKey, "traffic_used", dbTrafficUsed, "ts", now)
	rdb.Expire(ctx, snapKey, 10*time.Minute)

	return rt
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
	return rt
}

// NodeCreate [11] POST /api/v1/admin/nodes
// 创建节点(自动生成 REALITY 密钥对 + node_token)
func (h *AdminNodeHandler) NodeCreate(c *gin.Context) {
	var in service.CreateNodeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	in.ServerAddress = strings.TrimSpace(in.ServerAddress)
	node, err := h.nodeService.CreateNode(&in)
	if err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OK(c, node)
}

// NodeUpdate [12] PUT /api/v1/admin/nodes/:id
func (h *AdminNodeHandler) NodeUpdate(c *gin.Context) {
	id := c.Param("id")
	var in service.UpdateNodeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	if in.ServerAddress != nil {
		trimmed := strings.TrimSpace(*in.ServerAddress)
		*in.ServerAddress = trimmed
	}
	node, err := h.nodeService.UpdateNode(id, &in)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
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

	addr := fmt.Sprintf("%s:%d", node.ServerAddress, node.GrpcPort)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		// TCP 连接失败 → 节点确实不在线，立即标记离线
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

	// TCP 连接成功 → 节点 gRPC 端口可达，刷新 last_seen_at + online=true
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
