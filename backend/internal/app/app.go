package app

import (
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
}

// Get 获取全局容器(便捷方法)
func Get() *Container {
	return App
}
