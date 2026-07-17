package service

import (
        "context"
        "time"

        "github.com/redis/go-redis/v9"
        "go.uber.org/zap"

        "nexus-panel/internal/app"
        "nexus-panel/internal/model"
        "nexus-panel/internal/repo"
)

type CronService struct {
        userRepo  *repo.UserRepo
        orderSvc  *OrderService
        nodeRepo  *repo.NodeRepo
        logger    *zap.Logger
}

func NewCronService(u *repo.UserRepo, o *OrderService, n *repo.NodeRepo, logger *zap.Logger) *CronService {
        return &CronService{userRepo: u, orderSvc: o, nodeRepo: n, logger: logger}
}

func (s *CronService) ExpireOverdueUsers() {
        now := time.Now()
        count, err := s.userRepo.ExpireOverdueUsers(now)
        if err != nil {
                s.logger.Error("clean expired users failed", zap.Error(err))
                return
        }
        if count > 0 {
                s.logger.Info("cleaned expired users", zap.Int64("count", count))
        }
}

func (s *CronService) ExpireOrders() {
        count, err := s.orderSvc.ExpireOrders()
        if err != nil {
                s.logger.Error("clean expired orders failed", zap.Error(err))
                return
        }
        if count > 0 {
                s.logger.Info("cleaned expired orders", zap.Int("count", count))
        }
}

// tryLock 尝试获取分布式锁，成功返回解锁函数，失败返回 nil
func tryLock(rdb *redis.Client, key string, ttl time.Duration) (unlock func()) {
	if rdb == nil {
		return nil
	}
	token := time.Now().Format(time.RFC3339Nano)
	ok, err := rdb.SetNX(context.Background(), key, token, ttl).Result()
	if err != nil || !ok {
		return nil
	}
	return func() {
		// 仅删除自己的锁（通过 Lua 脚本保证原子性）
		script := `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`
		rdb.Eval(context.Background(), script, []string{key}, token)
	}
}

// RunAll 执行所有定时任务（带分布式锁，防止多副本重复执行）
func (s *CronService) RunAll() {
	unlock := tryLock(app.Get().RDB, "cron:lock:runall", 4*time.Minute)
	if unlock == nil {
		s.logger.Debug("定时任务 RunAll 被其它实例占用，跳过本次")
		return
	}
	defer unlock()
	s.ExpireOverdueUsers()
	s.ExpireOrders()
	s.MarkTrafficExhausted()
	s.CleanAggregateTrafficLogs()
}

// MarkTrafficExhausted 兜底检测所有超额用户并标记 traffic_exhausted
// 修复 BIZ-FATAL-02: 此前仅在 grpc ReportRealtime 实时上报时检测超额,
// 若节点 agent 上报异常/缺失,超额用户永远不会被停服("流量不停机")。
// 每 5 分钟扫一次 users 表,凡 status='active' 且 traffic_used>=traffic_limit (>0) 全部标记。
func (s *CronService) MarkTrafficExhausted() {
        if s.userRepo == nil {
                return
        }
        count, err := s.userRepo.MarkAllTrafficExhausted()
        if err != nil {
                s.logger.Error("mark traffic exhausted failed", zap.Error(err))
                return
        }
        if count > 0 {
                s.logger.Warn("已自动标记超额用户为 traffic_exhausted", zap.Int64("count", count))
        }
}

// CleanAggregateTrafficLogs 清理历史遗留的"幽灵用户"流量日志
// node-agent 0.1.0 之前把节点聚合流量写到 user_id="00000000-0000-0000-0000-000000000000",
// 造成后台 TopUsers/TrafficStats 出现"deleted"幽灵用户,后台显示混乱。
// 每天清理一次超过 7 天的聚合标记流量日志(给客户端 grace 期间再清)。
// 现在使用 "node:"+nodeID 前缀标识节点级流量，一并清理。
func (s *CronService) CleanAggregateTrafficLogs() {
	db := app.Get().DB
	if db == nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -7)
	// 清理旧格式的占位 UUID 与新格式的 "node:" 前缀聚合流量
	result := db.Exec(`
		DELETE FROM traffic_logs
		WHERE log_time < ?
		AND (user_id = ? OR user_id LIKE ?)
	`, cutoff, "00000000-0000-0000-0000-000000000000", "node:%")
	if result.Error != nil {
		s.logger.Error("clean aggregate traffic logs failed", zap.Error(result.Error))
	} else if result.RowsAffected > 0 {
		s.logger.Info("已清理幽灵用户流量日志", zap.Int64("count", result.RowsAffected))
	}
}

func (s *CronService) CleanOrphanData() {
	unlock := tryLock(app.Get().RDB, "cron:lock:orphan", 3*time.Hour)
	if unlock == nil {
		return
	}
	defer unlock()

	db := app.Get().DB
	if db == nil {
		return
	}

        result := db.Exec(`
                UPDATE subscriptions SET is_deleted = true, updated_at = NOW()
                WHERE is_deleted = false
                AND user_id NOT IN (SELECT id FROM users WHERE is_deleted = false)
        `)
        if result.Error != nil {
                s.logger.Error("clean orphan subscriptions failed", zap.Error(result.Error))
        } else if result.RowsAffected > 0 {
                s.logger.Info("cleaned orphan subscriptions", zap.Int64("count", result.RowsAffected))
        }

        result = db.Exec(`
                UPDATE user_nodes SET is_deleted = true, updated_at = NOW()
                WHERE is_deleted = false
                AND (
                        user_id NOT IN (SELECT id FROM users WHERE is_deleted = false)
                        OR node_id NOT IN (SELECT id FROM nodes WHERE is_deleted = false)
                )
        `)
        if result.Error != nil {
                s.logger.Error("clean orphan user_nodes failed", zap.Error(result.Error))
        } else if result.RowsAffected > 0 {
                s.logger.Info("cleaned orphan user_nodes", zap.Int64("count", result.RowsAffected))
        }

        result = db.Exec(`
                DELETE FROM node_plan_bindings
                WHERE node_id NOT IN (SELECT id FROM nodes WHERE is_deleted = false)
                OR plan_id NOT IN (SELECT id FROM plans WHERE is_deleted = false)
        `)
        if result.Error != nil {
                s.logger.Error("clean orphan node_plan_bindings failed", zap.Error(result.Error))
        } else if result.RowsAffected > 0 {
                s.logger.Info("cleaned orphan node_plan_bindings", zap.Int64("count", result.RowsAffected))
        }

        cutOff := time.Now().AddDate(0, 0, -30)
        result = db.Where("log_time < ?", cutOff).Delete(&model.TrafficLog{})
        if result.Error != nil {
                s.logger.Error("clean old traffic logs failed", zap.Error(result.Error))
        } else if result.RowsAffected > 0 {
                s.logger.Info("cleaned old traffic logs", zap.Int64("count", result.RowsAffected))
        }
}



// MarkStaleNodesOffline 检测心跳超时的节点并标记为离线
// 修复 BIZ-HEARTBEAT-01: 节点 gRPC 失联 3 分钟后自动标记 offline=true，
// 避免面板误显示节点在线但实际 Xray 仍在跑旧配置。
// 同步清除 Redis 缓存，强制节点下次心跳时拉取最新配置。
func (s *CronService) MarkStaleNodesOffline() {
	if s.nodeRepo == nil {
		return
	}
	unlock := tryLock(app.Get().RDB, "cron:lock:markstale", 50*time.Second)
	if unlock == nil {
		return
	}
	defer unlock()
	threshold := time.Now().Add(-3 * time.Minute)
	count, err := s.nodeRepo.MarkStaleNodesOffline(threshold)
	if err != nil {
		s.logger.Error("mark stale nodes offline failed", zap.Error(err))
		return
	}
	if count > 0 {
		s.logger.Warn("节点心跳超时,已自动标记为离线", zap.Int64("count", count), zap.Time("threshold", threshold))
		// 清除 Redis 中这些节点的 configver/usershash 缓存,确保节点恢复后能拉取新配置
		if rdb := app.Get().RDB; rdb != nil {
			nodes, _, _ := s.nodeRepo.List(0, 0, "")
			for _, n := range nodes {
				if !n.IsEnabled || n.IsDeleted {
					continue
				}
				rdb.Del(context.Background(), "node:configver:"+n.ID, "node:usershash:"+n.ID)
			}
		}
	}
}
