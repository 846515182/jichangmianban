package grpc

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	nexuspb "nexus-panel/proto"
)

// isNodeAggregateUser 判定是否为节点级聚合流量上报
// node-agent 用 "node:"+nodeID 作为聚合标识，旧版用占位 UUID "00000000-0000-0000-0000-000000000000"
// 或 "node"。三种都视为聚合标记。
func isNodeAggregateUser(uid string) bool {
	return uid == "node" || uid == "00000000-0000-0000-0000-000000000000" || strings.HasPrefix(uid, "node:")
}

// s7WarnLimiter 限制每个节点的 S7 警告频率, 避免每 5 分钟刷屏
var s7WarnLimiter sync.Map // map[nodeID]time.Time

// shouldLogS7Warn 同一节点 1 小时内最多打一次 S7 警告(用于写库失败等真正的错误)
func shouldLogS7Warn(nodeID string) bool {
	now := time.Now()
	if v, ok := s7WarnLimiter.Load(nodeID); ok {
		if now.Sub(v.(time.Time)) < time.Hour {
			return false
		}
	}
	s7WarnLimiter.Store(nodeID, now)
	return true
}

// s7InfoLimiter 限制 S7 INFO 日志频率(无活跃用户等正常状态)
var s7InfoLimiter sync.Map // map[nodeID]time.Time

// shouldLogS7Info 同一节点 1 小时内最多打一次 S7 INFO 日志
func shouldLogS7Info(nodeID string) bool {
	now := time.Now()
	if v, ok := s7InfoLimiter.Load(nodeID); ok {
		if now.Sub(v.(time.Time)) < time.Hour {
			return false
		}
	}
	s7InfoLimiter.Store(nodeID, now)
	return true
}

// TrafficServiceServer 流量服务 gRPC 实现
// 负责: 接收节点实时流量上报，查询用户流量汇总
type TrafficServiceServer struct {
	nexuspb.UnimplementedTrafficServiceServer

	trafficRepo *repo.TrafficRepo
	nodeRepo    *repo.NodeRepo
	userRepo    *repo.UserRepo
	logger      *zap.Logger
}

// NewTrafficServiceServer 创建流量服务
func NewTrafficServiceServer(trafficRepo *repo.TrafficRepo, nodeRepo *repo.NodeRepo, userRepo *repo.UserRepo, logger *zap.Logger) *TrafficServiceServer {
	return &TrafficServiceServer{
		trafficRepo: trafficRepo,
		nodeRepo:    nodeRepo,
		userRepo:    userRepo,
		logger:      logger,
	}
}

// ReportRealtime 批量接收流量记录，写入 traffic_logs 表(INSERT)，
// 同时累加 users.traffic_used 和 nodes.traffic_used(用事务保证一致)
func (s *TrafficServiceServer) ReportRealtime(ctx context.Context, req *nexuspb.ReportRealtimeRequest) (*nexuspb.ReportRealtimeResponse, error) {
	if req.GetNodeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "缺少 node_id")
	}
	// 校验节点存在 + node_token
	if req.GetNodeToken() == "" {
		return nil, status.Error(codes.Unauthenticated, "缺少 node_token")
	}
	node, err := s.nodeRepo.GetByID(req.GetNodeId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "节点不存在")
		}
		return nil, status.Error(codes.Internal, "查询节点失败")
	}
	if node.NodeToken != req.GetNodeToken() {
		return nil, status.Error(codes.Unauthenticated, "node_token 不匹配")
	}
	records := req.GetRecords()
	if len(records) == 0 {
		return &nexuspb.ReportRealtimeResponse{
			Resp:     &nexuspb.Response{Code: 0, Message: "ok"},
			Accepted: 0,
		}, nil
	}

	db := app.Get().DB
	// 按 (user_id) 聚合本批次的字节增量(同一用户多条记录合并累加)
	type agg struct {
		upload   int64
		download int64
	}
	userAgg := make(map[string]*agg, len(records))
	nodeTotal := int64(0)
	accepted := int32(0)

	err = db.Transaction(func(tx *gorm.DB) error {
		for _, r := range records {
			if r.GetUserId() == "" {
				continue
			}
			logTime := time.Now()
			if r.GetLogTime() > 0 {
				logTime = time.Unix(r.GetLogTime(), 0)
			}

			// 累加到聚合 map（包括节点聚合流量）
			a, ok := userAgg[r.GetUserId()]
			if !ok {
				a = &agg{}
				userAgg[r.GetUserId()] = a
			}
			a.upload += r.GetUploadBytes()
			a.download += r.GetDownloadBytes()
			nodeTotal += r.GetUploadBytes() + r.GetDownloadBytes()

			// 节点聚合流量: 不写 traffic_log (留到 distributeNodeTraffic 写真实用户),
			// 不进 userAgg 的后续累加 (会在 distributeNodeTraffic 单独处理)
			if isNodeAggregateUser(r.GetUserId()) {
				continue
			}

			// 写入 traffic_logs
			log := &model.TrafficLog{
				UserID:        r.GetUserId(),
				NodeID:        req.GetNodeId(),
				UploadBytes:   r.GetUploadBytes(),
				DownloadBytes: r.GetDownloadBytes(),
				LogTime:       logTime,
			}
			if err := s.trafficRepo.CreateLogTx(tx, log); err != nil {
				s.logger.Warn("写入流量日志失败",
					zap.String("user_id", r.GetUserId()),
					zap.String("node_id", req.GetNodeId()),
					zap.Error(err))
				continue
			}
			accepted++
		}

		// 处理节点聚合流量：仅累加节点总流量, 不再等分到用户
		// 修复 P0-7: 旧实现把节点总流量 / 活跃用户数 等分到每个用户的 traffic_used,
		// 计费严重失真(用 90GB 的用户和用 10GB 的用户被同等记账 10GB),
		// 既少收又多收, 还会误标低流量用户为 traffic_exhausted 停服。
		// 正确做法: 节点 agent 应解析 Xray stats API 获取每用户流量,
		// 未上报用户级流量的节点不计入 traffic_used, 仅累加节点维度统计。
		// nodeTotal 已在循环中累加, 此处从 userAgg 删除聚合标记, 避免后续 AddTrafficTx 误加。
		for aggKey := range userAgg {
			if isNodeAggregateUser(aggKey) {
				delete(userAgg, aggKey)
			}
		}

		// 累加用户流量(忽略单条错误，整体事务保留)
		exhaustedUIDs := make([]string, 0)
		for uid, a := range userAgg {
			if err := s.userRepo.AddTrafficTx(tx, uid, a.upload, a.download); err != nil {
				s.logger.Warn("累加用户流量失败",
					zap.String("user_id", uid), zap.Error(err))
				continue
			}
			// 实时检测流量超额: 套餐有上限且已用 >= 上限 → 标记 traffic_exhausted
			// 标记后用户不再下发到节点(下次心跳触发配置变更剔除其凭证)
			if a.upload+a.download > 0 {
				marked, mErr := markUserExhaustedTx(tx, uid)
				if mErr != nil {
					s.logger.Warn("标记流量超额失败",
						zap.String("user_id", uid), zap.Error(mErr))
				} else if marked {
					exhaustedUIDs = append(exhaustedUIDs, uid)
				}
			}
		}
		// 累加节点流量
		if nodeTotal > 0 {
			if err := s.nodeRepo.AddTrafficTx(tx, req.GetNodeId(), nodeTotal); err != nil {
				s.logger.Warn("累加节点流量失败",
					zap.String("node_id", req.GetNodeId()), zap.Error(err))
			}
		}
		// 在事务内提交后日志记录(仅记录 ID，事务提交后状态已生效)
		if len(exhaustedUIDs) > 0 {
			s.logger.Info("检测到用户流量超额，已标记 traffic_exhausted",
				zap.Strings("user_ids", exhaustedUIDs),
				zap.String("node_id", req.GetNodeId()))
		}
		return nil
	})
	if err != nil {
		s.logger.Error("流量上报事务失败", zap.String("node_id", req.GetNodeId()), zap.Error(err))
		return nil, status.Error(codes.Internal, "流量上报失败")
	}

	// 流量上报成功 = agent 存活且 Xray 在跑，刷新 last_seen_at 防止
	// 心跳瞬时失败时被 MarkStaleNodesOffline 误判离线(面板显示离线但节点可用)。
	// 用 TouchOnline 而非 UpdateOnline，避免覆盖已记录的 agent version。
	if err := s.nodeRepo.TouchOnline(req.GetNodeId(), time.Now()); err != nil {
		s.logger.Warn("流量上报后刷新节点在线状态失败",
			zap.String("node_id", req.GetNodeId()), zap.Error(err))
	}

	// 修复 NODE-SPEED-01 (P1): 计算节点瞬时速度并写入 Redis heartbeat hash。
	// 旧版面板在 admin API 调用时用 (dbTrafficUsed 差值 / admin 调用时间差) 算速度,
	// 导致首次访问/快速刷新/agent 60s 未上报时 speed_bps 恒为 0, 且口径是 admin 视角
	// 平均速率而非节点真实速率。现改为: agent 每次上报流量时(60s 一次), 面板用
	// (本次上报字节增量 / 距上次上报时间差) 算出 60s 平均瞬时速度, 写入 heartbeat
	// hash 的 speed_bps 字段。admin API 直接读该字段, 不再自己算。
	//
	// 修复 NODE-SPEED-02 (P0): 无论本次是否有流量, 都要刷新 speed_bps。
	// 旧实现仅在 nodeTotal > 0 时写入, 节点空闲时(nodeTotal==0)不更新 Redis,
	// 导致上一次有流量时写入的速度值永久残留, 前端显示"CPU 0% + 连接 0 + 速度 611 B/s"
	// 的矛盾状态。节点空闲时算出 speedBps=0 并覆盖旧值, 保证速度字段与其他指标一致。
	s.recordNodeSpeed(req.GetNodeId(), nodeTotal)

	return &nexuspb.ReportRealtimeResponse{
		Resp:     &nexuspb.Response{Code: 0, Message: "ok"},
		Accepted: accepted,
	}, nil
}

// recordNodeSpeed 计算节点瞬时速度(bytes/s)并写入 Redis node:heartbeat:{id} 的 speed_bps 字段。
// 速度 = 本次上报字节增量 / 距上次上报时间差。首次上报无法算 dt, 跳过(保持 0)。
//
// 用独立 key node:traffic_ts:{id} 记录上次上报时间戳(不用 snap key, snap 归 admin
// 管理用于兜底算法, 两者职责分离避免互相覆盖)。
func (s *TrafficServiceServer) recordNodeSpeed(nodeID string, deltaBytes int64) {
	rdb := app.Get().RDB
	if rdb == nil {
		return
	}
	ctx := context.Background()
	tsKey := fmt.Sprintf("node:traffic_ts:%s", nodeID)
	hbKey := fmt.Sprintf("node:heartbeat:%s", nodeID)
	now := time.Now().Unix()

	// 读上次上报时间戳
	prevTsStr, err := rdb.Get(ctx, tsKey).Result()
	var speedBps int64
	if err == nil && prevTsStr != "" {
		prevTs, _ := strconv.ParseInt(prevTsStr, 10, 64)
		dt := now - prevTs
		// dt > 0 防止除零; dt 上限 600s 防止节点长时间未上报后算出异常小值
		if dt > 0 && dt <= 600 {
			speedBps = deltaBytes / dt
		}
	}

	// 写入 heartbeat hash 的 speed_bps 字段(HSet 单字段不覆盖其他字段)
	rdb.HSet(ctx, hbKey, "speed_bps", speedBps)
	// 更新上报时间戳(下次算 dt 用)
	rdb.Set(ctx, tsKey, now, 10*time.Minute)
}

// markUserExhaustedTx 在事务内检测用户是否超额，若是则将 status 改为 traffic_exhausted
// 返回 (是否被标记, error)。条件: traffic_limit>0 且 traffic_used>=traffic_limit 且 status='active'
//
// 修复 P0-8: 旧实现先 SELECT 读 traffic_used 再应用层判断, 但 AddTrafficTx 用 gorm.Expr
// 原子更新不会回写到事务内的 u 对象, 导致读到事务开始前的旧值, 漏判超额。
// 现改为单条 UPDATE WHERE traffic_used>=traffic_limit 让 DB 判断, 完全消除读后判断竞态。
// UpdateColumn 避免修改 updated_at(否则会触发节点配置版本号变更误判)。
func markUserExhaustedTx(tx *gorm.DB, userID string) (bool, error) {
	// 单条条件更新: 仅当 active 且已超额时改为 traffic_exhausted
	// DB 在当前事务内可见 AddTrafficTx 已提交的最新值, 无读后判断竞态
	res := tx.Model(&model.User{}).
		Where("id = ? AND is_deleted = false AND status = 'active' AND traffic_limit > 0 AND traffic_used >= traffic_limit", userID).
		UpdateColumn("status", "traffic_exhausted")
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// QueryUserTraffic 返回指定用户的流量汇总(支持按 user_ids 列表批量查询)
func (s *TrafficServiceServer) QueryUserTraffic(ctx context.Context, req *nexuspb.QueryUserTrafficRequest) (*nexuspb.QueryUserTrafficResponse, error) {
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
		return nil, status.Error(codes.Unauthenticated, "node_token 不匹配")
	}

	userIDs := req.GetUserIds()
	// 没指定用户时返回全部 active 用户
	var users []model.User
	if len(userIDs) > 0 {
		users, err = s.userRepo.ListByIDs(userIDs)
	} else {
		users, err = s.userRepo.ListActive()
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "查询用户失败")
	}

	// 修复 TRAFFIC-RESET-01 (P0): 旧版优先取 traffic_logs 汇总,
	// 但 ResetTraffic 后 traffic_logs 已清(本批修复), 优先级反转会导致节点拉到 0 流量。
	// 现在以 users.traffic_used 为唯一真相源(实时累加字段, 由 AddTrafficTx 维护),
	// traffic_logs 仅用于历史趋势展示, 不参与计费判定。
	// (配套清理: 旧版残留的 SumByUsers 调用 + sumMap 变量已废弃, 移除避免"declared and not used"编译错误)
	summaries := make([]*nexuspb.UserTrafficSummary, 0, len(users))
	for _, u := range users {
		total := u.TrafficUsed
		summaries = append(summaries, &nexuspb.UserTrafficSummary{
			UserId:       u.ID,
			TotalUsed:    total,
			TrafficLimit: u.TrafficLimit,
			Status:       userStatusToProto(u.Status),
		})
	}

	return &nexuspb.QueryUserTrafficResponse{
		Resp:  &nexuspb.Response{Code: 0, Message: "ok"},
		Users: summaries,
	}, nil
}

// distributeNodeTraffic [已废弃 P0-7] 旧的等分计费逻辑, 计费严重失真, 已停止调用。
// 保留方法体供历史参考, 不再被 ReportRealtime 调用。
// 长期方案: 节点 agent 解析 Xray stats API 上报每用户流量, 面板直接累加。
func (s *TrafficServiceServer) distributeNodeTraffic(tx *gorm.DB, nodeID string, node *model.Node, totalUpload, totalDownload int64) {
	_ = s.nodeRepo
	_ = nodeID
	_ = node
	_ = totalUpload
	_ = totalDownload
}
