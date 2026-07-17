package grpc

import (
	"context"
	"errors"
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

// shouldLogS7Warn 同一节点 1 小时内最多打一次 S7 警告
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

		// 处理节点聚合流量：分发到节点的真实用户
		// 触发条件: userAgg 中存在任意一种聚合标记("node" 或占位 UUID)
		for aggKey := range userAgg {
			if isNodeAggregateUser(aggKey) {
				nodeAgg := userAgg[aggKey]
				s.distributeNodeTraffic(tx, req.GetNodeId(), node, nodeAgg.upload, nodeAgg.download)
				delete(userAgg, aggKey)
				break
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

	return &nexuspb.ReportRealtimeResponse{
		Resp:     &nexuspb.Response{Code: 0, Message: "ok"},
		Accepted: accepted,
	}, nil
}

// markUserExhaustedTx 在事务内检测用户是否超额，若是则将 status 改为 traffic_exhausted
// 返回 (是否被标记, error)。条件: traffic_limit>0 且 traffic_used>=traffic_limit 且 status='active'
// 注意: 使用 UpdateColumns 避免修改 updated_at(否则会触发节点配置版本号变更误判)
func markUserExhaustedTx(tx *gorm.DB, userID string) (bool, error) {
	var u model.User
	if err := tx.Select("traffic_limit, traffic_used, status").
		Where("id = ? AND is_deleted = false", userID).First(&u).Error; err != nil {
		return false, err
	}
	if u.TrafficLimit <= 0 || u.TrafficUsed < u.TrafficLimit {
		return false, nil
	}
	if u.Status == "traffic_exhausted" {
		return false, nil
	}
	if u.Status != "active" {
		return false, nil // disabled/expired 用户不改状态
	}
	res := tx.Model(&model.User{}).
		Where("id = ? AND status = 'active'", userID).
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

	// 取流量日志汇总(traffic_logs)
	ids := make([]string, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	sumMap, err := s.trafficRepo.SumByUsers(ids)
	if err != nil {
		s.logger.Warn("查询流量汇总失败", zap.Error(err))
		sumMap = map[string]int64{} // 容错：失败时返回 users 表里的 traffic_used
	}

	summaries := make([]*nexuspb.UserTrafficSummary, 0, len(users))
	for _, u := range users {
		// 优先取 traffic_logs 汇总；若汇总为 0 则回退到 users.traffic_used
		total := sumMap[u.ID]
		if total == 0 {
			total = u.TrafficUsed
		}
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

// distributeNodeTraffic 将 node-agent 上报的聚合流量分发到节点的真实用户
// node-agent 当前仅能按 /proc/net/dev 汇总节点总流量,无法区分具体用户。
// 此处按节点绑定的套餐/等级匹配活跃用户,**等分**节点流量到每个活跃用户,
// 并写 traffic_log / AddTrafficTx 让 traffic_used 真实增长,
// 这样 markUserExhaustedTx 后续才能把超额用户标 traffic_exhausted(防止"流量不停机")。
// [S7 fix 2026-07-14] 旧的等分写库逻辑被禁用,导致 traffic_used 永不增长;
// 2026-07-16 改进: 真正执行等分,但加单节点每小时 1 次的警告限速,避免日志刷屏;
// 等分虽然不精确,但优于"完全丢弃"。长期方案: 节点 agent 解析 Xray access log。
func (s *TrafficServiceServer) distributeNodeTraffic(tx *gorm.DB, nodeID string, node *model.Node, totalUpload, totalDownload int64) {
	total := totalUpload + totalDownload
	if total <= 0 {
		return
	}

	// 优先用套餐绑定查用户；无绑定时回退到所有活跃用户
	planIDs, _ := s.nodeRepo.GetPlanIDsByNode(nodeID)
	var users []model.User
	var err error
	if len(planIDs) > 0 {
		users, err = s.userRepo.ListActiveForPlans(planIDs)
	} else {
		users, err = s.userRepo.ListActive()
	}
	if err != nil || len(users) == 0 {
		// 限速警告: 同节点 1h 内最多 1 次
		if shouldLogS7Warn(nodeID) {
			s.logger.Warn("S7: 节点聚合流量分发失败, 无活跃用户",
				zap.String("node_id", nodeID),
				zap.Int64("total_upload", totalUpload),
				zap.Int64("total_download", totalDownload),
				zap.Error(err))
		}
		return
	}

	// 等分到每个活跃用户
	n := int64(len(users))
	perUpload := totalUpload / n
	perDownload := totalDownload / n
	remUpload := totalUpload - perUpload*n
	remDownload := totalDownload - perDownload*n

	distributed := 0
	for i, u := range users {
		up := perUpload
		dn := perDownload
		if i == 0 {
			up += remUpload
			dn += remDownload
		}
		if up == 0 && dn == 0 {
			continue
		}
		// 写 traffic_log(用真实 user_id,避免幽灵用户污染 TopUsers)
		log := &model.TrafficLog{
			UserID:        u.ID,
			NodeID:        nodeID,
			UploadBytes:   up,
			DownloadBytes: dn,
			LogTime:       time.Now(),
		}
		if err := s.trafficRepo.CreateLogTx(tx, log); err != nil {
			if shouldLogS7Warn(nodeID) {
				s.logger.Warn("S7: 写分布式流量日志失败",
					zap.String("user_id", u.ID), zap.Error(err))
			}
			continue
		}
		// 累加 traffic_used(让 markUserExhaustedTx 能正确触发)
		if err := s.userRepo.AddTrafficTx(tx, u.ID, up, dn); err != nil {
			if shouldLogS7Warn(nodeID) {
				s.logger.Warn("S7: 累加分布式流量失败",
					zap.String("user_id", u.ID), zap.Error(err))
			}
			continue
		}
		// 检查是否超额 → 标 traffic_exhausted
		if up+dn > 0 {
			if _, mErr := markUserExhaustedTx(tx, u.ID); mErr != nil && shouldLogS7Warn(nodeID) {
				s.logger.Warn("S7: 标记分布式用户超额失败",
					zap.String("user_id", u.ID), zap.Error(mErr))
			}
		}
		distributed++
	}

	// 限速 info 日志(同节点 1h 内最多 1 次)
	if shouldLogS7Warn(nodeID) {
		s.logger.Info("S7: 节点聚合流量已等分到活跃用户",
			zap.String("node_id", nodeID),
			zap.Int64("total_upload", totalUpload),
			zap.Int64("total_download", totalDownload),
			zap.Int("user_count", len(users)),
			zap.Int("distributed", distributed),
		)
	}
}
