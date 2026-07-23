package grpc

import (
	"context"
	"fmt"

	"nexus-panel/internal/app"

	"go.uber.org/zap"
)

// acceptOldNodeToken 检查给定的 token 是否是节点最近轮换前的旧 token (P0-N3 宽限期消费方)。
// RotateToken 会把旧 token 写入 Redis key=node:old_token:{nodeID}, TTL=24h。
// 返回 true 表示当前 token 不匹配 DB 中的新 token, 但与 Redis 中保存的旧 token 匹配 (在宽限期内, 应放行)。
// Redis 不可用或 key 不存在时返回 false (调用方会拒绝请求)。
//
// 统一收口: Heartbeat / GetConfig / ReportStatus / ReportRealtime 都通过本函数做双 token 宽限,
// 避免 RotateToken 后 agent 用旧 token 心跳/拉配置/上报状态/上报流量全部失败。
func acceptOldNodeToken(ctx context.Context, nodeID, presentedToken string) bool {
	if presentedToken == "" || nodeID == "" {
		return false
	}
	rdb := app.Get().RDB
	if rdb == nil {
		return false
	}
	stored, err := rdb.Get(ctx, fmt.Sprintf("node:old_token:%s", nodeID)).Result()
	if err != nil {
		return false
	}
	if stored == presentedToken {
		if logger := app.Get().Logger; logger != nil {
			logger.Warn("接受节点旧 token (RotateToken 宽限期内)",
				zap.String("node_id", nodeID))
		}
		return true
	}
	return false
}
