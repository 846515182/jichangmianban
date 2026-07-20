package grpc

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

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

// SyncUsers 用 node_id+node_token 校验后，下发所有 active 用户的凭证列表
// since_version 暂未做增量(0=全量)，后续可基于 updated_at 实现
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

	users, err := s.userRepo.ListActive()
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
