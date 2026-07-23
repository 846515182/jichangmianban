package grpc

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	nexuspb "nexus-panel/proto"
)

// UserSyncServiceServer 用户同步服务 gRPC 实现
// 负责: 向节点下发用户凭证列表
type UserSyncServiceServer struct {
	nexuspb.UnimplementedUserSyncServiceServer

	nodeRepo *repo.NodeRepo
	userRepo *repo.UserRepo
	logger   *zap.Logger
}

// NewUserSyncServiceServer 创建用户同步服务
func NewUserSyncServiceServer(nodeRepo *repo.NodeRepo, userRepo *repo.UserRepo, logger *zap.Logger) *UserSyncServiceServer {
	return &UserSyncServiceServer{
		nodeRepo: nodeRepo,
		userRepo: userRepo,
		logger:   logger,
	}
}

// SyncUsers 用 node_id+node_token 校验后，下发该节点可见的活跃用户凭证列表
// since_version 暂未做增量(0=全量)，后续可基于 updated_at 实现
//
// P1-SyncUsers口径: 改用 listActiveUsersForNode(node) 而非 userRepo.ListActive()。
// 旧版把全部 active 用户下发到每个节点, 导致:
//  1. 安全: 节点 A 的用户凭证会泄露给节点 B, 被攻破的节点 B 可冒充节点 A 的用户
//  2. 性能: 节点数 × 用户数 全量下发, DB 与带宽浪费
//  现按 node_plan_bindings 过滤, 只下发绑定到该节点套餐的用户。
func (s *UserSyncServiceServer) SyncUsers(ctx context.Context, req *nexuspb.SyncUsersRequest) (*nexuspb.SyncUsersResponse, error) {
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

	users, err := s.listActiveUsersForNode(node)
	if err != nil {
		s.logger.Error("SyncUsers 查询用户失败", zap.Error(err))
		return nil, status.Error(codes.Internal, "查询用户失败")
	}

	creds := make([]*nexuspb.UserCredential, 0, len(users))
	for _, u := range users {
		// 修复 P0-14: 节点 agent 只需要 UUID(Xray clients[].id)做代理认证,
		// 不需要 username/traffic_limit/traffic_used(计费在面板侧, 下发这些敏感信息
		// 会让被攻破的节点获取业务数据)。proto 字段保留但置空, 兼容旧 agent。
		creds = append(creds, &nexuspb.UserCredential{
			Uuid:    u.ID, // users.id 即为 uuid, 仅用于 Xray clients[].id
			Status:  userStatusToProto(u.Status),
			Version: u.UpdatedAt.Unix(),
		})
	}

	return &nexuspb.SyncUsersResponse{
		Resp:          &nexuspb.Response{Code: 0, Message: "ok"},
		Users:         creds,
		LatestVersion: time.Now().Unix(),
	}, nil
}

// listActiveUsersForNode 查询节点可见的活跃用户
// P1-SyncUsers口径: 与 grpc/node_service.go 中同名方法逻辑一致,
// 按节点套餐绑定过滤用户。复制实现以避免跨 server 类型耦合(两 server 都持有相同 repo)。
// [P2-NODE-06] 节点未绑定套餐时不再回退到所有用户, 避免权限模型被静默放宽。
func (s *UserSyncServiceServer) listActiveUsersForNode(node *model.Node) ([]model.User, error) {
	planIDs, err := s.nodeRepo.GetPlanIDsByNode(node.ID)
	if err != nil {
		return nil, err
	}
	if len(planIDs) == 0 {
		s.logger.Warn("SyncUsers: 节点未绑定任何套餐, 不下发用户凭证",
			zap.String("node_id", node.ID),
			zap.String("node_name", node.Name))
		return []model.User{}, nil
	}
	return s.userRepo.ListActiveForPlans(planIDs)
}

// userStatusToProto 数据库 status 字符串转 proto 枚举
func userStatusToProto(s string) nexuspb.UserStatus {
	switch s {
	case "active":
		return nexuspb.UserStatus_USER_ACTIVE
	case "disabled":
		return nexuspb.UserStatus_USER_DISABLED
	case "expired":
		return nexuspb.UserStatus_USER_EXPIRED
	case "locked":
		return nexuspb.UserStatus_USER_LOCKED
	default:
		return nexuspb.UserStatus_USER_STATUS_UNSPECIFIED
	}
}
