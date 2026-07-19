package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"nexus-panel/internal/app"
	"nexus-panel/internal/config"
	grpcapi "nexus-panel/internal/grpc"
	"nexus-panel/internal/handler"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/service"
)

// Version 编译时通过 ldflags 注入(git HEAD short hash), 用于检测容器是否需要重建
var Version = "dev"

func main() {
	// 0. 注入编译版本号到全局
	app.Version = Version

	// 修复 2026-07-19: 启动时打印实际版本号, 便于排查 ldflags 是否生效
	fmt.Printf("[VERSION] main.Version=%q app.Version=%q\n", Version, app.Version)

	// 1. 加载配置
	cfg, err := config.Load()
	if err != nil {
		panic("加载配置失败: " + err.Error())
	}

	// 2. 初始化日志
	logger, err := initLogger(cfg)
	if err != nil {
		panic("初始化日志失败: " + err.Error())
	}
	defer logger.Sync()
	zap.ReplaceGlobals(logger)

	logger.Info("Nexus-Panel 启动中...")

	// 3. 初始化数据库
	db, err := initDB(cfg, logger)
	if err != nil {
		logger.Fatal("初始化数据库失败", zap.Error(err))
	}

	// 4. 自动迁移(开发期使用) + SQL 迁移
	if err := autoMigrate(db); err != nil {
		logger.Warn("自动迁移失败", zap.Error(err))
	}
	if err := runSQLMigrations(db, logger); err != nil {
		logger.Warn("SQL 迁移执行失败", zap.Error(err))
	}

	// 5. 初始化 Redis
	rdb := initRedis(cfg, logger)

	// 6. 初始化全局容器
	app.Init(cfg, db, rdb, logger)

	// 7. 确保存在默认超级管理员(首次启动)
	ensureSuperAdmin(db, cfg, logger)

	// 8. 确保存在试用套餐(注册自动发放, 5GB/30天)
	ensureTrialPlan(db, logger)

	// 8. 注册路由
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	// 修复 F-05: 禁用 trusted proxies, 防止客户端通过 X-Forwarded-For 伪造 IP
	// 直接监听公网或单层代理时使用 nil, 使 c.ClientIP() 始终返回真实远程地址
	// 若后续部署于反代后端, 应改为配置实际代理 IP 列表: r.SetTrustedProxies([]string{"10.0.0.2"})
	_ = r.SetTrustedProxies([]string{"127.0.0.1", "172.16.0.0/12"})
	r.Use(gin.Recovery())
	// 自定义日志中间件
	r.Use(ginZapLogger(logger))

	deps := handler.NewDeps()
	handler.RegisterRoutes(r, deps)



	// 9. 启动 gRPC 服务(节点通信)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	grpcSrv := grpcapi.NewServer(cfg.GRPCListen)
	go func() {
		if err := grpcSrv.Start(ctx); err != nil {
			logger.Error("gRPC 服务退出", zap.Error(err))
		}
	}()

	// 9.5 启动定时任务(修复 BIZ-FATAL-01: 清理过期用户 + 过期订单)
	userRepo := repo.NewUserRepo(db)
	orderRepo := repo.NewOrderRepo(db)
	planRepo := repo.NewPlanRepo(db)
	couponRepo := repo.NewCouponRepo(db)
	nodeRepo := repo.NewNodeRepo(db)
	// 修复 TRAFFIC-RESET-02 (P0): settingRepo 供 CronService 读取 settings.traffic.reset_day
	settingRepo := repo.NewSettingRepo(db)
	orderSvc := service.NewOrderService(orderRepo, planRepo, couponRepo, userRepo)
	cronSvc := service.NewCronService(userRepo, orderSvc, nodeRepo, settingRepo, logger)
	go func() {
		tickerExpire := time.NewTicker(5 * time.Minute)
		defer tickerExpire.Stop()
		for {
			select {
			case <-tickerExpire.C:
				cronSvc.RunAll()
			case <-ctx.Done():
				return
			}
		}
	}()
	logger.Info("定时任务已启动(每5分钟清理过期用户和订单)")
	// 修复 H3: 原代码仅调度 RunAll(ExpireOverdueUsers+ExpireOrders),
	// MarkStaleNodesOffline 与 CleanOrphanData 定义后从未被调用, 导致
	// 节点 gRPC 失联后 online 恒真、仪表盘在线节点数失真、孤儿数据堆积。
	// 新增独立 ticker: 每 1 分钟标记心跳超时节点为离线; 每 6 小时清理孤儿数据。
	//
	// 修复 NODE-OFFLINE-01 (P0): 启动后立即巡检会在面板刚重启(如一键更新)
	// 后立刻把所有节点误判离线——agent 还没来得及重连发心跳, last_seen_at 仍是
	// 重启前的旧值, 必然 < 5min 阈值。结果用户节点 Xray 还在跑(缓存配置), 用户
	// 仍能用, 但面板显示"离线"。
	// 现在启动后延迟 3 分钟再开始巡检, 给 agent gRPC 重连+发首条心跳留足窗口。
	go func() {
		// 启动后等待 3 分钟再开始巡检(等 agent 重连), 之后每 1 分钟一次
		startupGrace := time.NewTimer(3 * time.Minute)
		defer startupGrace.Stop()
		select {
		case <-startupGrace.C:
		case <-ctx.Done():
			return
		}
		tickerStale := time.NewTicker(1 * time.Minute)
		defer tickerStale.Stop()
		for {
			select {
			case <-tickerStale.C:
				cronSvc.MarkStaleNodesOffline()
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		tickerOrphan := time.NewTicker(6 * time.Hour)
		defer tickerOrphan.Stop()
		// 启动后先跑一次, 清理历史孤儿数据
		cronSvc.CleanOrphanData()
		for {
			select {
			case <-tickerOrphan.C:
				cronSvc.CleanOrphanData()
			case <-ctx.Done():
				return
			}
		}
	}()
	logger.Info("节点心跳巡检(1m)与孤儿数据清理(6h)定时任务已启动")

	// 修复 TRAFFIC-RESET-02 (P0): 周期性流量重置 cron。
	// 每小时检查一次, 当"今日 == settings.traffic.reset_day"时触发批量重置。
	// 方法内部用 Redis SetNX 当日幂等键, 保证同一天只执行一次(多副本/重启安全)。
	go func() {
		tickerReset := time.NewTicker(1 * time.Hour)
		defer tickerReset.Stop()
		// 启动后立即检查一次(若今天还没重置且今天是 reset_day, 立即执行)
		cronSvc.ResetTrafficMonthly()
		for {
			select {
			case <-tickerReset.C:
				cronSvc.ResetTrafficMonthly()
			case <-ctx.Done():
				return
			}
		}
	}()
	logger.Info("周期性流量重置定时任务已启动(每小时检查, 每月 reset_day 日执行)")

	// 修复 STORAGE-DISK-01 (P0): 磁盘阈值告警 cron。每 5 分钟检查根分区使用率,
	// >=85% WARN 告警, >=95% ERROR 紧急告警, 通过邮件/Telegram 通知(若已配置)。
	// Redis 1h 冷却键防刷屏。
	go func() {
		tickerDisk := time.NewTicker(5 * time.Minute)
		defer tickerDisk.Stop()
		for {
			select {
			case <-tickerDisk.C:
				cronSvc.CheckDiskThreshold()
			case <-ctx.Done():
				return
			}
		}
	}()

	// 修复 STORAGE-BACKUP-02/03 (P0): 自动数据库备份 + 轮转 cron。
	// 每日 1 次执行 pg_dump 全量备份(无 pg_dump 则降级告警), 并只保留最新 1 份备份。
	go func() {
		tickerBackup := time.NewTicker(24 * time.Hour)
		defer tickerBackup.Stop()
		// 启动后先清理历史残留旧备份(立即释放存储, 不等 pg_dump)
		cronSvc.RotateBackupsKeepOne()
		// 再跑一次完整备份流程(含轮转)
		cronSvc.AutoBackupDatabase()
		for {
			select {
			case <-tickerBackup.C:
				cronSvc.AutoBackupDatabase()
			case <-ctx.Done():
				return
			}
		}
	}()
	logger.Info("磁盘阈值告警(5m)与数据库自动备份+轮转(24h)定时任务已启动")

	// 修复 PAY-RECON-01 (P0): 掉单对账 cron。
	// 每 5 分钟扫描近 30 分钟内仍为 pending 的订单, 主动查 EPay 真实支付状态,
	// 已支付的兜底调 PaySuccess 开通套餐, 防止回调丢失导致"用户已付款但订单永远 pending"。
	go func() {
		tickerReconcile := time.NewTicker(5 * time.Minute)
		defer tickerReconcile.Stop()
		for {
			select {
			case <-tickerReconcile.C:
				cronSvc.ReconcilePendingOrders()
			case <-ctx.Done():
				return
			}
		}
	}()
	logger.Info("掉单对账(5m)定时任务已启动")

	// 修复 CRITICAL 2026-07-19: 版本一致性兜底巡检 cron。
	// 每 5 分钟对比代码版本(git HEAD)与运行版本(app.Version),
	// 不一致自动 docker compose up -d --no-deps panel 重建容器。
	// 兜底"在线更新显示成功但跑旧二进制"类问题, 不再依赖人工 SSH 修复。
	go func() {
		// 启动后延迟 3 分钟再开始巡检, 给一键更新流程留时间
		startupGrace := time.NewTimer(3 * time.Minute)
		defer startupGrace.Stop()
		select {
		case <-startupGrace.C:
		case <-ctx.Done():
			return
		}
		tickerVer := time.NewTicker(5 * time.Minute)
		defer tickerVer.Stop()
		// 启动后先跑一次, 立即修复历史不一致
		cronSvc.CheckVersionConsistency()
		for {
			select {
			case <-tickerVer.C:
				cronSvc.CheckVersionConsistency()
			case <-ctx.Done():
				return
			}
		}
	}()
	logger.Info("版本一致性兜底巡检(5m)定时任务已启动")

	// 10. 启动 HTTP/HTTPS 服务
	httpSrv := &http.Server{
		Addr:         cfg.HTTPListen,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 600 * time.Second,
	}
	go func() {
		logger.Info("HTTP 服务启动", zap.String("listen", cfg.HTTPListen))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("HTTP 服务异常", zap.Error(err))
		}
	}()
	// 若配置了 TLS 证书则同时启动 HTTPS
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		httpsSrv := &http.Server{
			Addr:         cfg.HTTPSListen,
			Handler:      r,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 600 * time.Second,
		}
		go func() {
			logger.Info("HTTPS 服务启动", zap.String("listen", cfg.HTTPSListen))
			if err := httpsSrv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Fatal("HTTPS 服务异常", zap.Error(err))
			}
		}()
		go httpToHTTPSRedirect()
	}

	// 11. 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("收到退出信号，开始优雅关闭...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	cancel()
	logger.Info("Nexus-Panel 已退出")
}

// initLogger 初始化 zap 日志
func initLogger(cfg *config.Config) (*zap.Logger, error) {
	if cfg.HTTPListen == "" {
		return zap.NewDevelopment()
	}
	return zap.NewProduction()
}

// initDB 初始化 PostgreSQL via GORM
func initDB(cfg *config.Config, logger *zap.Logger) (*gorm.DB, error) {
	gormCfg := &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	}
	db, err := gorm.Open(postgres.Open(cfg.DSN()), gormCfg)
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	// 修复 PERF-DBPOOL-01: 旧值 MaxIdle=10/MaxOpen=100 在仪表盘并发(4 路请求) +
	// 节点 gRPC(心跳/流量/GetConfig) + 管理员操作同时进行时容易打满, 导致请求排队等待连接,
	// 表现为面板整体卡顿。调大上限并缩短连接复用周期, 让连接池更稳定地承载峰值。
	sqlDB.SetMaxIdleConns(25)
	sqlDB.SetMaxOpenConns(200)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(10 * time.Minute)
	logger.Info("数据库已连接", zap.String("host", cfg.DBHost), zap.String("db", cfg.DBName))
	return db, nil
}

// initRedis 初始化 Redis 客户端
func initRedis(cfg *config.Config, logger *zap.Logger) *redis.Client {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPass,
		DB:       0,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		logger.Warn("Redis 连接失败(部分功能不可用)", zap.String("addr", cfg.RedisAddr), zap.Error(err))
	} else {
		logger.Info("Redis 已连接", zap.String("addr", cfg.RedisAddr))
	}
	return rdb
}

// autoMigrate 自动迁移表结构
func autoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.Admin{},
		&model.User{},
		&model.Node{},
		&model.UserNode{},
		&model.Subscription{},
		&model.TrafficLog{},
		&model.Announcement{},
		&model.Setting{},
		&model.LoginAudit{},
		&model.Plan{},
		&model.Order{},
		&model.Coupon{},
		&model.NodePlanBinding{},
		&model.Ticket{},
		&model.TicketReply{},
		&model.AdminAction{},
		&model.SchemaMigration{},
	)
}

// ensureSuperAdmin 首次启动时确保存在默认超级管理员
// 修复 SEC-P0-05: 移除硬编码弱口令 admin123，未设置 ADMIN_INIT_PASSWORD 时生成随机密码
func ensureSuperAdmin(db *gorm.DB, cfg *config.Config, logger *zap.Logger) {
	var count int64
	db.Model(&model.Admin{}).Where("is_deleted = false").Count(&count)
	if count > 0 {
		return
	}
	password := os.Getenv("ADMIN_INIT_PASSWORD")
	randomGenerated := false
	if password == "" {
		password = generateRandomPassword(20)
		randomGenerated = true
		logger.Warn("未设置 ADMIN_INIT_PASSWORD，已生成随机管理员密码(请立即保存并修改)",
			zap.String("username", "admin"),
			zap.String("password", password))
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		logger.Error("生成默认管理员密码失败", zap.Error(err))
		return
	}
	admin := &model.Admin{
		Username:     "admin",
		PasswordHash: string(hash),
		Email:        "admin@nexus.local",
		Role:         "super_admin",
		Status:       "active",
	}
	if err := db.Create(admin).Error; err != nil {
		logger.Error("创建默认管理员失败", zap.Error(err))
		return
	}
	if randomGenerated {
		logger.Info("已创建默认超级管理员(随机密码)，请立即登录修改")
	} else {
		logger.Info("已创建默认超级管理员", zap.String("username", "admin"))
	}
}

// ensureTrialPlan 确保存在试用套餐(注册自动发放)
// 5GB 流量 / 30 天 / 0 元 / 设备数 2
//
// 修复 CRITICAL 2026-07-19: 旧版漏设 IsTrial: true, 导致试用套餐的 is_trial=false,
// ListEnabled 的 WHERE is_trial=false 过滤失效, 试用套餐出现在用户购买列表中,
// 用户点击购买会触发"试用套餐无法购买"或订单创建失败。
//
// 防御策略(三层兜底, 确保任何情况下试用套餐不会出现在购买列表):
//  1. 新建试用套餐时显式 IsTrial: true
//  2. 每次启动无条件 UPDATE: 把所有 name 含"试用"且 is_trial=false 的套餐修正为 true
//     (不依赖查询-判断-更新的脆弱链路, 直接批量 UPDATE, 一次搞定)
//  3. 同时 UPDATE: 把所有 PriceCents=0 且 DurationDays=30 且 DeviceLimit=2 的套餐也标为试用
//     (兜底: 即便 name 不含"试用"但符合试用套餐特征的也标记, 防止用户重命名后漏标)
func ensureTrialPlan(db *gorm.DB, logger *zap.Logger) {
	// 第 1 步: 兜底修正存量数据 - 无条件批量 UPDATE, 不依赖查询判断
	// 修正策略 A: 按名称匹配
	resultA := db.Model(&model.Plan{}).
		Where("is_deleted = false AND is_trial = false AND name LIKE ?", "%试用%").
		Update("is_trial", true)
	if resultA.Error != nil {
		logger.Warn("兜底修正试用套餐 is_trial 失败(按名称)", zap.Error(resultA.Error))
	} else if resultA.RowsAffected > 0 {
		logger.Info("已兜底修正试用套餐 is_trial 字段(按名称)", zap.Int64("affected", resultA.RowsAffected))
	}

	// 修正策略 B: 按试用套餐特征匹配(0元/30天/2设备/5GB) - 防止用户改过名字后漏标
	fiveGB := int64(5 * 1024 * 1024 * 1024)
	resultB := db.Model(&model.Plan{}).
		Where("is_deleted = false AND is_trial = false AND price_cents = 0 AND duration_days = 30 AND device_limit = 2 AND traffic_limit = ?", fiveGB).
		Update("is_trial", true)
	if resultB.Error != nil {
		logger.Warn("兜底修正试用套餐 is_trial 失败(按特征)", zap.Error(resultB.Error))
	} else if resultB.RowsAffected > 0 {
		logger.Info("已兜底修正试用套餐 is_trial 字段(按特征)", zap.Int64("affected", resultB.RowsAffected))
	}

	// 第 2 步: 确保存在试用套餐记录(若全表都没匹配到, 才创建新的)
	var existing model.Plan
	err := db.Where("is_deleted = false AND is_trial = true").First(&existing).Error
	if err == nil {
		return // 已存在试用套餐
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.Warn("查询试用套餐失败", zap.Error(err))
		return
	}
	trial := &model.Plan{
		Name:                "试用套餐",
		Description:         "注册即享 5GB 试用流量，30 天有效",
		TrafficLimit:        5 * 1024 * 1024 * 1024, // 5GB
		DurationDays:        30,
		PriceCents:          0,
		OriginalPriceCents:  0,
		DeviceLimit:         2,
		IsEnabled:           true,
		IsTrial:             true, // 试用套餐标记, ListEnabled 会过滤掉
		SortOrder:           0,    // 排在最前
	}
	if err := db.Create(trial).Error; err != nil {
		logger.Warn("创建试用套餐失败", zap.Error(err))
		return
	}
	logger.Info("已自动创建试用套餐(5GB/30天)", zap.String("name", trial.Name))
}

// generateRandomPassword 使用 crypto/rand 生成随机密码
func generateRandomPassword(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("无法生成随机密码: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)[:n]
}

// httpToHTTPSRedirect 将 HTTP 请求 301 重定向到 HTTPS
func httpToHTTPSRedirect() {
	redirectSrv := &http.Server{
		Addr:         ":80",
		Handler:      http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			target := "https://" + r.Host + r.URL.String()
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		}),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	if err := redirectSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		app.Get().Logger.Warn("HTTP 重定向服务退出", zap.Error(err))
	}
}

// runSQLMigrations 执行尚未运行过的 SQL 迁移文件
func runSQLMigrations(db *gorm.DB, logger *zap.Logger) error {
	// 确保迁移记录表存在
	if err := db.AutoMigrate(&model.SchemaMigration{}); err != nil {
		return fmt.Errorf("创建迁移记录表失败: %w", err)
	}

	migrations := []struct {
		Version string
		File    string
	}{
		{"001_init", "migrations/001_init.sql"},
		{"002_plans_orders", "migrations/002_plans_orders.sql"},
		{"003_node_plan_bindings", "migrations/003_node_plan_bindings.sql"},
		{"004_tickets", "migrations/004_tickets.sql"},
		{"005_admin_status_fix", "migrations/005_admin_status_fix.sql"},
		{"006_traffic_partition_automate", "migrations/006_traffic_partition_automate.sql"},
		{"2026_07_14_account_flow", "migrations/2026_07_14_account_flow.sql"},
		{"2026_07_16_fix_missing_updated_at", "migrations/2026_07_16_fix_missing_updated_at.sql"},
		{"2026_07_16_drop_node_level_add_coupon", "migrations/2026_07_16_drop_node_level_add_coupon.sql"},
		{"2026_07_17_perf_indexes", "migrations/2026_07_17_perf_indexes.sql"},
		{"2026_07_19_drop_invite_codes", "migrations/2026_07_19_drop_invite_codes.sql"},
		// 修复 CRITICAL 2026-07-19: 注册试用套餐标记迁移
		// 旧版未注册, 导致存量试用套餐 is_trial 始终为 false, 出现在购买列表中
		// 此迁移会 UPDATE plans SET is_trial=true WHERE name LIKE '%试用%'
		{"2026_07_19_add_plan_is_trial", "migrations/2026_07_19_add_plan_is_trial.sql"},
	}

	for _, m := range migrations {
		var count int64
		db.Model(&model.SchemaMigration{}).Where("version = ?", m.Version).Count(&count)
		if count > 0 {
			logger.Info("迁移已执行, 跳过", zap.String("version", m.Version))
			continue
		}
		data, err := os.ReadFile(m.File)
		if err != nil {
			logger.Warn("读取迁移文件失败, 跳过", zap.String("file", m.File), zap.Error(err))
			continue
		}
		tx := db.Begin()
		if err := tx.Exec(string(data)).Error; err != nil {
			tx.Rollback()
			logger.Error("执行迁移失败", zap.String("version", m.Version), zap.Error(err))
			continue
		}
		if err := tx.Create(&model.SchemaMigration{Version: m.Version}).Error; err != nil {
			tx.Rollback()
			return fmt.Errorf("记录迁移版本失败: %w", err)
		}
		tx.Commit()
		logger.Info("迁移执行成功", zap.String("version", m.Version))
	}
	return nil
}

// ginZapLogger 简易 gin 日志中间件(使用 zap)
func ginZapLogger(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Info("HTTP",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.String("ip", c.ClientIP()),
		)
	}
}
