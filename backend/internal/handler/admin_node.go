package handler

import (
	"context"
	"errors"
	"fmt"
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

	// 为每个节点附加实时状态(CPU/内存/连接/速度)和套餐绑定
	items := make([]gin.H, 0, len(list))
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

		// 套餐绑定 ID 列表(前端编辑时回显)
		planIDs, _ := h.nodeRepo.GetPlanIDsByNode(n.ID)
		item["plan_ids"] = planIDs

		// 实时状态: 从 Redis 心跳读取
		if rdb != nil {
			item["runtime"] = h.readNodeRuntime(ctx, rdb, n.ID, n.TrafficUsed)
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

	// 解析心跳字段
	cpuUsage, _ := strconv.ParseFloat(hb["cpu_usage"], 64)
	memUsage, _ := strconv.ParseFloat(hb["memory_usage"], 64)
	memTotal, _ := strconv.ParseInt(hb["memory_total"], 10, 64)
	onlineConn, _ := strconv.ParseInt(hb["online_connections"], 10, 64)
	uptime, _ := strconv.ParseInt(hb["uptime_seconds"], 10, 64)
	hbUpdatedAt, _ := strconv.ParseInt(hb["updated_at"], 10, 64)

	rt["cpu_usage"] = cpuUsage
	rt["memory_usage"] = memUsage
	rt["memory_total"] = memTotal
	rt["online_connections"] = onlineConn
	rt["uptime_seconds"] = uptime
	rt["updated_at"] = hbUpdatedAt

	// 计算实时速度: 与上次管理端查询的快照对比
	// 快照 key: node:speed_snap:{id} = {traffic_used, ts}
	snapKey := fmt.Sprintf("node:speed_snap:%s", nodeID)
	snap, _ := rdb.HGetAll(ctx, snapKey).Result()
	if len(snap) > 0 {
		prevUsed, _ := strconv.ParseInt(snap["traffic_used"], 10, 64)
		prevTs, _ := strconv.ParseInt(snap["ts"], 10, 64)
		// 使用 DB 的 traffic_used(最准确，ReportRealtime 已累加)
		curTs := time.Now().Unix()
		dt := curTs - prevTs
		if dt > 0 && dbTrafficUsed >= prevUsed {
			speedBps := (dbTrafficUsed - prevUsed) / dt
			rt["speed_bps"] = speedBps
		}
	}
	// 更新快照(用 DB traffic_used，TTL 10 分钟)
	now := time.Now().Unix()
	rdb.HSet(ctx, snapKey, "traffic_used", dbTrafficUsed, "ts", now)
	rdb.Expire(ctx, snapKey, 10*time.Minute)

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
