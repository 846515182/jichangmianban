package grpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	nexuspb "nexus-panel/proto"
)

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
		// P0-N3: 双 token 宽限期。RotateToken 时旧 token 会写入 Redis (key=node:old_token:{nodeID},
		// TTL=24h)。此处校验当前 token 失败时, 兜底查 Redis 旧 token, 匹配则放行。
		// 用于 RotateToken 后 agent 仍用旧 token 上报流量/查询用户的过渡期, 避免流量丢失。
		// 注: 宽限期内同时接受新旧 token, 24h 后 Redis key 过期, 旧 token 失效。
		if !acceptOldNodeToken(ctx, req.GetNodeId(), req.GetNodeToken()) {
			return nil, status.Error(codes.Unauthenticated, "node_token 不匹配")
		}
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
	accepted := int32(0)

	err = db.Transaction(func(tx *gorm.DB) error {
		for _, r := range records {
			// 校验 upload/download 非负
			if r.GetUploadBytes() < 0 || r.GetDownloadBytes() < 0 {
				s.logger.Warn("流量记录含负数, 跳过",
					zap.String("user_id", r.GetUserId()),
					zap.Int64("upload", r.GetUploadBytes()),
					zap.Int64("download", r.GetDownloadBytes()))
				continue
			}
			// 校验 user_id 是有效 UUID 格式(避免 PG uuid 类型报错致整批回滚)
			if r.GetUserId() == "" {
				continue
			}
			if _, parseErr := uuid.Parse(r.GetUserId()); parseErr != nil {
				// 降为 Debug 避免日志噪音(agent 旧版可能上报 "node:xxx" 格式)
				s.logger.Debug("user_id 非有效 UUID, 跳过",
					zap.String("user_id", r.GetUserId()))
				continue
			}
			logTime := time.Now()
			if r.GetLogTime() > 0 {
				logTime = time.Unix(r.GetLogTime(), 0)
			}

			// 累加到聚合 map
			a, ok := userAgg[r.GetUserId()]
			if !ok {
				a = &agg{}
				userAgg[r.GetUserId()] = a
			}
			a.upload += r.GetUploadBytes()
			a.download += r.GetDownloadBytes()

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

		// 累加用户流量(忽略单条错误，整体事务保留)
		exhaustedUIDs := make([]string, 0)
		// [P0-同机多节点] 同时累加节点流量(nodes.traffic_used), 旧版只累加用户流量,
		// nodes.traffic_used 永远 0, 管理后台"服务器流量汇总"按 server_address 聚合恒 0
		var nodeTotalBytes int64
		for uid, a := range userAgg {
			if err := s.userRepo.AddTrafficTx(tx, uid, a.upload, a.download); err != nil {
				s.logger.Warn("累加用户流量失败",
					zap.String("user_id", uid), zap.Error(err))
				continue
			}
			nodeTotalBytes += a.upload + a.download
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
		// [P0-同机多节点] 累加节点流量, 让管理后台能看到每节点/每服务器的流量消耗
		if nodeTotalBytes > 0 {
			if err := s.nodeRepo.AddTrafficTx(tx, req.GetNodeId(), nodeTotalBytes); err != nil {
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

	// 修复 NODE-SPEED-01: 计算本批次瞬时速率写入 heartbeat hash。
	// admin_node.go 的 speed_bps 主路径读 heartbeat.speed_bps, 旧版无人写导致恒 0,
	// 只能靠 snap 兜底估算(延迟高、不准)。这里用本批次总字节 / 实际上报间隔算真实速率。
	// 用 Redis 记录上次上报时间戳(node:speed_ts:{nodeID})计算真实时间差, 避免假设固定 60s。
	if rdb := app.Get().RDB; rdb != nil && accepted > 0 {
		var totalBytes int64
		for _, a := range userAgg {
			totalBytes += a.upload + a.download
		}
		speedTSKey := fmt.Sprintf("node:speed_ts:%s", req.GetNodeId())
		now := time.Now()
		if prevTS, err := rdb.Get(ctx, speedTSKey).Int64(); err == nil && prevTS > 0 {
			elapsed := now.Unix() - prevTS
			if elapsed > 0 {
				speedBps := totalBytes * 8 / elapsed
				hbKey := fmt.Sprintf("node:heartbeat:%s", req.GetNodeId())
				if err := rdb.HSet(ctx, hbKey, "speed_bps", speedBps).Err(); err != nil {
					s.logger.Warn("写入 speed_bps 失败",
						zap.String("node_id", req.GetNodeId()), zap.Error(err))
				}
				// 刷新 TTL 避免心跳 hash 过期后 speed_bps 丢失
				_ = rdb.Expire(ctx, hbKey, 10*time.Minute).Err()
			}
		}
		_ = rdb.Set(ctx, speedTSKey, now.Unix(), 10*time.Minute).Err()
	}

	return &nexuspb.ReportRealtimeResponse{
		Resp:     &nexuspb.Response{Code: 0, Message: "ok"},
		Accepted: accepted,
	}, nil
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
		// P0-N3: 双 token 宽限期。RotateToken 时旧 token 会写入 Redis (key=node:old_token:{nodeID},
		// TTL=24h)。此处校验当前 token 失败时, 兜底查 Redis 旧 token, 匹配则放行。
		// 用于 RotateToken 后 agent 仍用旧 token 上报流量/查询用户的过渡期, 避免流量丢失。
		// 注: 宽限期内同时接受新旧 token, 24h 后 Redis key 过期, 旧 token 失效。
		if !acceptOldNodeToken(ctx, req.GetNodeId(), req.GetNodeToken()) {
			return nil, status.Error(codes.Unauthenticated, "node_token 不匹配")
		}
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
