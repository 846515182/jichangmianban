package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// LoadScorer 节点负载评分服务
// 基于心跳上报的 CPU/内存/连接数/带宽 综合计算负载评分,
// 用于订阅智能调度(过滤满载节点)和自动踢人保护(超载移除用户凭证)。
//
// 评分算法: score = max(连接数占比, 带宽占比, CPU占比, 内存占比)
//   - score < 0.5  → idle   (空闲, 优先调度)
//   - 0.5 ~ 0.8    → normal (正常)
//   - 0.8 ~ 1.0    → busy   (繁忙, 限制新用户)
//   - >= 1.0       → full   (满载, 拒绝新用户 + 触发踢人)
type LoadScorer struct {
	rdb    *redis.Client
	logger *zap.Logger
}

func NewLoadScorer() *LoadScorer {
	return &LoadScorer{
		rdb:    app.Get().RDB,
		logger: app.Get().Logger,
	}
}

// HeartbeatSnapshot 心跳快照(从 Redis hash 读取)
type HeartbeatSnapshot struct {
	NodeID            string
	CpuUsage          float64
	MemoryUsage       float64
	OnlineConnections int32
	SpeedBps          int64 // 从 traffic_service 写入
	UpdatedAt         int64
}

// LoadScore 负载评分结果
type LoadScore struct {
	Score        float64 // 0~1+, >=1 表示满载
	Status       string  // idle/normal/busy/full
	ClientRatio  float64 // 连接数占比
	BandwidthRto float64 // 带宽占比
	CpuRatio     float64 // CPU占比
	MemRatio     float64 // 内存占比
}

const (
	StatusIdle   = "idle"
	StatusNormal = "normal"
	StatusBusy   = "busy"
	StatusFull   = "full"
)

// GetHeartbeatSnapshot 从 Redis 读取节点心跳快照
func (s *LoadScorer) GetHeartbeatSnapshot(ctx context.Context, nodeID string) (*HeartbeatSnapshot, error) {
	key := fmt.Sprintf("node:heartbeat:%s", nodeID)
	m, err := s.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(m) == 0 {
		return nil, nil // 无心跳数据
	}
	snap := &HeartbeatSnapshot{NodeID: nodeID}
	if v, err := strconvParseFloat(m["cpu_usage"]); err == nil {
		snap.CpuUsage = v
	}
	if v, err := strconvParseFloat(m["memory_usage"]); err == nil {
		snap.MemoryUsage = v
	}
	if v, err := strconvParseInt(m["online_connections"]); err == nil {
		snap.OnlineConnections = int32(v)
	}
	if v, err := strconvParseInt(m["speed_bps"]); err == nil {
		snap.SpeedBps = v
	}
	if v, err := strconvParseInt(m["updated_at"]); err == nil {
		snap.UpdatedAt = v
	}
	return snap, nil
}

// CalculateScore 计算节点负载评分
// node: 节点配置(含 MaxClients/MaxBandwidthMbps/CpuThreshold)
// snap: 心跳快照(含 CPU/内存/连接数/带宽)
// 若节点未配置容量上限(MaxClients=0 && MaxBandwidthMbps=0), 返回 idle 不参与调度
func (s *LoadScorer) CalculateScore(node *model.Node, snap *HeartbeatSnapshot) LoadScore {
	// 无心跳数据 → idle(不阻断, 节点可能刚启动)
	if snap == nil || snap.UpdatedAt == 0 {
		return LoadScore{Score: 0, Status: StatusIdle}
	}

	// 未配置任何容量上限 → 不参与调度, 返回 idle
	if node.MaxClients <= 0 && node.MaxBandwidthMbps <= 0 {
		return LoadScore{Score: 0, Status: StatusIdle}
	}

	var clientRatio, bandwidthRatio, cpuRatio, memRatio float64

	// 连接数占比
	if node.MaxClients > 0 {
		clientRatio = float64(snap.OnlineConnections) / float64(node.MaxClients)
	}

	// 带宽占比 (speed_bps 是双向总速率, MaxBandwidthMbps 是 Mbps)
	if node.MaxBandwidthMbps > 0 && snap.SpeedBps > 0 {
		maxBps := float64(node.MaxBandwidthMbps) * 1e6
		bandwidthRatio = float64(snap.SpeedBps) / maxBps
	}

	// CPU 占比 (默认阈值 80%)
	cpuThreshold := float64(node.CpuThreshold)
	if cpuThreshold <= 0 {
		cpuThreshold = 80
	}
	cpuRatio = snap.CpuUsage / cpuThreshold

	// 内存占比 (固定阈值 90%, 内存泄漏才需关注)
	memRatio = snap.MemoryUsage / 90.0

	// 综合评分 = 各维度最大值(短板效应)
	score := math.Max(math.Max(clientRatio, bandwidthRatio), math.Max(cpuRatio, memRatio))

	// 状态分级
	status := StatusIdle
	switch {
	case score >= 1.0:
		status = StatusFull
	case score >= 0.8:
		status = StatusBusy
	case score >= 0.5:
		status = StatusNormal
	}

	return LoadScore{
		Score:        score,
		Status:       status,
		ClientRatio:  clientRatio,
		BandwidthRto: bandwidthRatio,
		CpuRatio:     cpuRatio,
		MemRatio:     memRatio,
	}
}

// UpdateNodeLoadStatus 计算评分并更新节点 load_status 到 DB + Redis
// 返回计算出的评分(供心跳处理逻辑判断是否触发踢人)
func (s *LoadScorer) UpdateNodeLoadStatus(ctx context.Context, node *model.Node, snap *HeartbeatSnapshot) (LoadScore, error) {
	score := s.CalculateScore(node, snap)

	// 只在状态变化时更新 DB(减少写压力)
	if node.LoadStatus != score.Status {
		if err := app.Get().DB.Model(&model.Node{}).
			Where("id = ? AND is_deleted = false", node.ID).
			Update("load_status", score.Status).Error; err != nil {
			s.logger.Warn("更新节点负载状态失败",
				zap.String("node_id", node.ID),
				zap.String("status", score.Status),
				zap.Error(err))
			return score, err
		}
		node.LoadStatus = score.Status
	}

	// 评分写入 Redis hash(供订阅调度读取, 避免每次查 DB)
	scoreKey := fmt.Sprintf("node:loadscore:%s", node.ID)
	s.rdb.HSet(ctx, scoreKey, "score", fmt.Sprintf("%.4f", score.Score),
		"status", score.Status,
		"updated_at", time.Now().Unix())
	s.rdb.Expire(ctx, scoreKey, 10*time.Minute)

	return score, nil
}

// ShouldEvict 判断节点是否需要踢人(满载且有容量上限)
func (s *LoadScorer) ShouldEvict(node *model.Node, score LoadScore) bool {
	return node.MaxClients > 0 && score.Status == StatusFull
}

// EvictCount 计算需要踢出多少用户(超载量)
func (s *LoadScorer) EvictCount(node *model.Node, snap *HeartbeatSnapshot) int {
	if node.MaxClients <= 0 || snap == nil {
		return 0
	}
	over := int(snap.OnlineConnections) - node.MaxClients
	if over <= 0 {
		return 0
	}
	// 踢出超载量 + 1 个余量, 避免踢完又立刻满载
	return over + 1
}

// GetScoreFromCache 从 Redis 读取缓存的评分(订阅调度用)
func (s *LoadScorer) GetScoreFromCache(ctx context.Context, nodeID string) (score float64, status string, err error) {
	key := fmt.Sprintf("node:loadscore:%s", nodeID)
	m, err := s.rdb.HGetAll(ctx, key).Result()
	if err != nil || len(m) == 0 {
		return 0, StatusIdle, err
	}
	if v, e := strconvParseFloat(m["score"]); e == nil {
		score = v
	}
	status = m["status"]
	if status == "" {
		status = StatusIdle
	}
	return score, status, nil
}

// 辅助函数(避免 strconv import 污染)
func strconvParseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

func strconvParseInt(s string) (int64, error) {
	var i int64
	_, err := fmt.Sscanf(s, "%d", &i)
	return i, err
}
