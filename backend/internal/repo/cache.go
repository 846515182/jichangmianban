package repo

import (
	"context"
	"fmt"

	"nexus-panel/internal/app"
)

// clearNodeUsersHashCache 删除指定节点的 usershash 与 configver 缓存。
// 用户列表/套餐绑定变更后, 下次心跳会重新计算用户指纹并触发 agent 重拉配置。
func clearNodeUsersHashCache(ctx context.Context, nodeIDs ...string) {
	rdb := app.Get().RDB
	if rdb == nil {
		return
	}
	keys := make([]string, 0, len(nodeIDs)*2)
	for _, id := range nodeIDs {
		if id == "" {
			continue
		}
		keys = append(keys, fmt.Sprintf("node:usershash:%s", id), fmt.Sprintf("node:configver:%s", id))
	}
	if len(keys) > 0 {
		_ = rdb.Del(ctx, keys...).Err()
	}
}

// clearAllNodeUsersHashCache 扫描并删除所有节点的 usershash 与 configver 缓存。
// 兜底: 当无法精确定位受影响节点(如批量用户状态变更/套餐删除)时, 全量清理使所有 agent 下次心跳重拉。
func clearAllNodeUsersHashCache(ctx context.Context) {
	rdb := app.Get().RDB
	if rdb == nil {
		return
	}
	iter := rdb.Scan(ctx, 0, "node:usershash:*", 100).Iterator()
	keys := []string{}
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return
	}
	if len(keys) == 0 {
		return
	}
	// 同时清理对应的 configver, 让 agent 强制重拉
	for _, k := range keys {
		id := k[len("node:usershash:"):]
		if id != "" {
			keys = append(keys, fmt.Sprintf("node:configver:%s", id))
		}
	}
	_ = rdb.Del(ctx, keys...).Err()
}

// clearNodeCacheKeys 删除节点相关的一组 Redis 缓存 key。
// 供节点删除/重建等场景兜底清理, 避免旧数据残留。
func clearNodeCacheKeys(ctx context.Context, keys ...string) {
	rdb := app.Get().RDB
	if rdb == nil || len(keys) == 0 {
		return
	}
	_ = rdb.Del(ctx, keys...).Err()
}

// ClearNodeUsersHashCache 导出版: 删除指定节点的 usershash 与 configver 缓存。
// 供 service/handler 等跨包调用, 确保数据变更后缓存及时失效。
func ClearNodeUsersHashCache(ctx context.Context, nodeIDs ...string) {
	clearNodeUsersHashCache(ctx, nodeIDs...)
}

// ClearAllNodeUsersHashCache 导出版: 扫描并删除所有节点的 usershash 与 configver 缓存。
// 兜底: 当无法精确定位受影响节点时, 全量清理使所有 agent 下次心跳重拉。
func ClearAllNodeUsersHashCache(ctx context.Context) {
	clearAllNodeUsersHashCache(ctx)
}

// ClearNodeCacheKeys 导出版: 删除节点相关的一组 Redis 缓存 key。
func ClearNodeCacheKeys(ctx context.Context, keys ...string) {
	clearNodeCacheKeys(ctx, keys...)
}
