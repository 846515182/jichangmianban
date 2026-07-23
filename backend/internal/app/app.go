package app

import (
	"sync/atomic"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"nexus-panel/internal/config"
)

// Container 全局依赖容器，保存数据库、Redis、日志等单例
type Container struct {
	Cfg    *config.Config
	DB     *gorm.DB
	RDB    *redis.Client
	Logger *zap.Logger

	// P0-Redis: Redis 健康状态(atomic, 由后台健康检查 goroutine 维护)
	// initRedis 恒返回非 nil client(go-redis 内部自动重连), 导致全代码的
	// `rdb == nil` 守卫是死代码 — Redis 真挂时守卫不触发, 操作静默失败。
	// 用此标志替代 `rdb == nil` 检查, 由后台 Ping 每 10s 刷新。
	redisHealthy atomic.Bool
}

// 全局实例
var App *Container

// Version 编译时注入的版本号(git HEAD short hash), 用于检测容器是否需要重建
var Version string

// Init 初始化全局容器
func Init(cfg *config.Config, db *gorm.DB, rdb *redis.Client, logger *zap.Logger) {
	App = &Container{
		Cfg:    cfg,
		DB:     db,
		RDB:    rdb,
		Logger: logger,
	}
	// 初始假设 Redis 可用(避免启动瞬间所有请求被拒), 后台 Ping 会立即校正
	App.redisHealthy.Store(true)
}

// Get 获取全局容器(便捷方法)
func Get() *Container {
	return App
}

// IsRedisAvailable 返回 Redis 是否可用
// P0-Redis: 替代 `rdb == nil` 检查(rdb 恒非 nil, 但 Redis 可能不可达)
// 由 main.go 的后台健康检查 goroutine 每 10s 刷新
func (c *Container) IsRedisAvailable() bool {
	return c.redisHealthy.Load()
}

// SetRedisHealth 更新 Redis 健康状态(供后台健康检查调用)
func (c *Container) SetRedisHealth(healthy bool) {
	c.redisHealthy.Store(healthy)
}
