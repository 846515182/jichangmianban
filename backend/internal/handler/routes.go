package handler

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"nexus-panel/internal/app"
	"nexus-panel/internal/middleware"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/response"
	"nexus-panel/internal/security"
	"nexus-panel/internal/service"
)

// bootTime 进程启动时间(Unix 秒)。用于 /healthz 让前端判断面板是否已重启完成。
// syscall.Exec 原地重启后此值会变(新进程重新初始化包级变量)。
var bootTime = time.Now().Unix()

type Deps struct {
	JWTMgr *security.JWTManager

	AdminRepo      *repo.AdminRepo
	UserRepo       *repo.UserRepo
	NodeRepo       *repo.NodeRepo
	SubRepo        *repo.SubscriptionRepo
	TrafficRepo    *repo.TrafficRepo
	SettingRepo    *repo.SettingRepo
	LoginAuditRepo *repo.LoginAuditRepo
	AnnounceRepo   *repo.AnnouncementRepo
	PlanRepo       *repo.PlanRepo
	OrderRepo      *repo.OrderRepo
	CouponRepo     *repo.CouponRepo
	TicketRepo     *repo.TicketRepo

	NodeSvc     *service.NodeService
	UserSvc     *service.UserService
	SubSvc      *service.SubscribeService
	TrafficSvc  *service.TrafficService
	PlanSvc     *service.PlanService
	OrderSvc    *service.OrderService
	PaymentSvc  *service.PaymentService
	EmailSvc    *service.EmailService
	RegisterSvc *service.UserRegisterService
	SysStatsSvc *service.SystemStatsService
	TicketSvc   *service.TicketService
}

func NewDeps() *Deps {
	db := app.Get().DB
	cfg := app.Get().Cfg

	adminRepo := repo.NewAdminRepo(db)
	userRepo := repo.NewUserRepo(db)
	nodeRepo := repo.NewNodeRepo(db)
	subRepo := repo.NewSubscriptionRepo(db)
	trafficRepo := repo.NewTrafficRepo(db)
	settingRepo := repo.NewSettingRepo(db)
	loginAuditRepo := repo.NewLoginAuditRepo(db)
	announceRepo := repo.NewAnnouncementRepo(db)
	planRepo := repo.NewPlanRepo(db)
	orderRepo := repo.NewOrderRepo(db)
	couponRepo := repo.NewCouponRepo(db)
	ticketRepo := repo.NewTicketRepo(db)

	jwtMgr := security.NewJWTManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)

	orderSvc := service.NewOrderService(orderRepo, planRepo, couponRepo, userRepo)
	paymentSvc := service.NewPaymentService(settingRepo, orderSvc)
	emailSvc := service.NewEmailService(settingRepo, cfg)

	return &Deps{
		JWTMgr:         jwtMgr,
		AdminRepo:      adminRepo,
		UserRepo:       userRepo,
		NodeRepo:       nodeRepo,
		SubRepo:        subRepo,
		TrafficRepo:    trafficRepo,
		SettingRepo:    settingRepo,
		LoginAuditRepo: loginAuditRepo,
		AnnounceRepo:   announceRepo,
		PlanRepo:       planRepo,
		OrderRepo:      orderRepo,
		CouponRepo:     couponRepo,
		TicketRepo:     ticketRepo,
		NodeSvc:        service.NewNodeService(nodeRepo),
		UserSvc:        service.NewUserService(userRepo),
		SubSvc:         service.NewSubscribeService(subRepo, nodeRepo, userRepo),
		TrafficSvc:     service.NewTrafficService(trafficRepo, nodeRepo, userRepo),
		PlanSvc:        service.NewPlanService(planRepo),
		OrderSvc:       orderSvc,
		PaymentSvc:     paymentSvc,
		EmailSvc:       emailSvc,
		RegisterSvc:    service.NewUserRegisterService(userRepo, planRepo),
		SysStatsSvc:    service.NewSystemStatsService(nodeRepo, userRepo),
		TicketSvc:      service.NewTicketService(ticketRepo, userRepo, adminRepo),
	}
}

func RegisterRoutes(r *gin.Engine, deps *Deps) {
	// 修复 UI-RELOAD-01 (P1): /healthz 返回进程启动时间(boot_time),
	// 前端一键更新/重启面板后用它判断面板是否已重启完成:
	// 后端用 syscall.Exec 原地重启, HTTP 断开窗口可能 <2s, 前端 2s 轮询
	// 容易错过断开瞬间, 导致永远卡在"等待断开"阶段。改为对比 boot_time,
	// 启动时间变化 = 新进程已起来 = 重启完成。
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "boot_time": bootTime})
	})

	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	authH := NewAuthHandler(deps.AdminRepo, deps.UserRepo, deps.LoginAuditRepo, deps.JWTMgr)
	authRegisterH := NewAuthRegisterHandler(deps.RegisterSvc)
	userH := NewUserHandler(deps.UserRepo, deps.NodeRepo, deps.AnnounceRepo, deps.SubSvc, deps.TrafficSvc)
	adminNodeH := NewAdminNodeHandler(deps.NodeSvc, deps.NodeRepo)
	adminUserH := NewAdminUserHandler(deps.UserSvc, deps.UserRepo, deps.SubSvc, deps.SubRepo, deps.OrderSvc, deps.NodeRepo)
	systemH := NewSystemHandler(deps.TrafficSvc, deps.SettingRepo, deps.LoginAuditRepo, deps.NodeRepo, deps.UserRepo, deps.SubRepo)
	systemH.SetPaymentService(deps.PaymentSvc)
	systemH.SetEmailService(deps.EmailSvc)
	sysStatsH := NewSystemStatsHandler(deps.SysStatsSvc)
	planH := NewPlanHandler(deps.PlanSvc)
	orderH := NewOrderHandler(deps.OrderSvc, deps.PaymentSvc)
	paymentH := NewPaymentHandler(deps.PaymentSvc)
	couponH := NewCouponHandler(deps.CouponRepo, deps.OrderRepo)
	ticketH := NewTicketHandler(deps.TicketSvc)

	api := r.Group("/api/v1")

	auth := api.Group("/auth")
	{
		auth.POST("/login", middleware.LoginLockGuard(), middleware.RateLimit(middleware.RateScopeAdmin), authH.Login)
		auth.POST("/register", middleware.RateLimit(middleware.RateScopeUser), authRegisterH.Register)
		auth.POST("/refresh", middleware.RateLimit(middleware.RateScopeAdmin), authH.Refresh)
		auth.POST("/logout", middleware.AnyAuth(), middleware.RateLimit(middleware.RateScopeUser), authH.Logout)
		auth.POST("/change-password", middleware.AnyAuth(), middleware.RateLimit(middleware.RateScopeUser), authH.ChangePassword)
		auth.POST("/logout-all", middleware.AnyAuth(), middleware.RateLimit(middleware.RateScopeUser), authH.LogoutAll)
	}

	api.GET("/captcha", GetCaptcha)

	pay := api.Group("/payment")
	{
		pay.GET("/notify", paymentH.Notify)
		pay.POST("/notify", paymentH.Notify)
		pay.GET("/return", paymentH.Return)
	}

	// 公开订阅拉取(无需 JWT, 通过 token+sig 认证)
	api.GET("/subscribe", middleware.RateLimit(middleware.RateScopeSub), userH.PublicSubscribe)

	user := api.Group("/user")
	user.Use(middleware.RateLimit(middleware.RateScopeUser), middleware.UserAuth())
	{
		user.GET("/info", userH.UserInfo)
		user.GET("/login-logs", authH.LoginLogs)
		user.GET("/subscribe", middleware.RateLimit(middleware.RateScopeSub), userH.Subscribe)
		user.POST("/orders", orderH.CreateOrder)
		user.GET("/orders", orderH.ListUserOrders)
		user.GET("/orders/:id", orderH.GetOrder)
		user.POST("/orders/:id/cancel", orderH.CancelOrder)
		user.POST("/orders/:id/pay", orderH.PayOrder)
		user.POST("/coupon/validate", couponH.UserCouponValidate)
		// 系统实时状态（CPU/内存/网络速度）— 用户端精简版
		user.GET("/system/stats", sysStatsH.UserSystemStats)
		// 工单
		user.GET("/tickets", ticketH.UserListTickets)
		user.GET("/tickets/:id", ticketH.UserGetTicket)
		user.POST("/tickets", ticketH.UserCreateTicket)
		user.POST("/tickets/:id/reply", ticketH.UserReplyTicket)
		user.POST("/tickets/:id/close", ticketH.UserCloseTicket)
	}

	// 兼容老路径 /api/v1/tickets/* (与前端约定一致)
	tickets := api.Group("/tickets")
	tickets.Use(middleware.RateLimit(middleware.RateScopeUser), middleware.AnyAuth())
	{
		// 用户的 mine/list: 走 user handler
		tickets.GET("/mine", ticketH.UserListTickets)
		// 用户/管理员共用的 reply/close: 根据 role 区分(handler 内部)
		tickets.POST("/:id/reply", ticketH.ReplyAlias)
		tickets.POST("/:id/close", ticketH.CloseAlias)
	}

	userPub := api.Group("")
	userPub.Use(middleware.RateLimit(middleware.RateScopeUser), middleware.UserAuth())
	{
		userPub.GET("/nodes/list", userH.NodeList)
		userPub.GET("/nodes/latency", userH.NodeLatency)
		userPub.GET("/announcements", userH.Announcements)
		userPub.GET("/plans", planH.UserPlanList)
	}

	// SSH WebSocket 终端（token 通过 query 参数传递，handler 内部自验证）
	sshTermH := NewSSHTerminalHandler(deps.NodeRepo, deps.JWTMgr)
	api.GET("/admin/nodes/:id/terminal",
		middleware.RateLimit("ssh_term"),
		sshTermH.Terminal,
	)

	admin := api.Group("/admin")
	admin.Use(middleware.RateLimit(middleware.RateScopeAdmin), middleware.AdminAuth())
	{
		admin.GET("/nodes", adminNodeH.NodeList)
		admin.GET("/nodes/:id", adminNodeH.NodeDetail)
		admin.POST("/nodes", middleware.AuditAction("node.create"), adminNodeH.NodeCreate)
		admin.PUT("/nodes/:id", middleware.AuditAction("node.update"), adminNodeH.NodeUpdate)
		admin.DELETE("/nodes/:id", middleware.AuditAction("node.delete"), adminNodeH.NodeDelete)
		admin.POST("/nodes/:id/rotate-token", middleware.RBAC(middleware.PermKeyManage), middleware.AuditAction("node.rotate_token"), adminNodeH.RotateToken)
		// 主动 TCP 探测节点 gRPC 端口，立即确认在线状态(解决服务器重装后等 8 分钟才变离线的问题)
		admin.POST("/nodes/:id/ping", adminNodeH.PingNode)
		// 一键部署涉及远程 root 权限, 仅 super_admin 可用
		admin.POST("/nodes/:id/auto-deploy", middleware.RBAC(middleware.PermNodeManage), middleware.AuditAction("node.auto_deploy"), NewAutoDeployHandler(deps.NodeRepo, deps.JWTMgr).Deploy)
		// 节点清理并删除: SSE 流式, SSH 到节点服务器清理容器/目录/镜像 + DB 删除
		// 比 DELETE /nodes/:id 更彻底, 解决旧版只删 DB 不清节点服务器残留的问题
		admin.DELETE("/nodes/:id/cleanup", middleware.RBAC(middleware.PermNodeManage), middleware.AuditAction("node.delete"), NewNodeCleanupHandler(deps.NodeSvc, deps.NodeRepo, app.Get().Logger).CleanupWithProgress)

		admin.GET("/users", adminUserH.UserList)
		admin.POST("/users", middleware.AuditAction("user.create"), adminUserH.UserCreate)
		admin.PUT("/users/:id", middleware.AuditAction("user.update"), adminUserH.UserUpdate)
		admin.DELETE("/users/:id", middleware.AuditAction("user.delete"), adminUserH.UserDelete)
		// 物理删除(彻底清理测试数据, 释放 username/email 唯一索引, 重新注册不冲突)
		admin.DELETE("/users/:id/hard", middleware.AuditAction("user.hard_delete"), adminUserH.UserHardDelete)
		admin.POST("/users/import", middleware.AuditAction("user.import"), adminUserH.UserImport)
		admin.POST("/users/:id/reset-traffic", middleware.AuditAction("user.reset_traffic"), adminUserH.UserResetTraffic)
		admin.GET("/subscriptions", NewAdminSubscriptionHandler(deps.SubRepo, deps.SubSvc).List)
		admin.GET("/users/:id/subscription", NewAdminSubscriptionHandler(deps.SubRepo, deps.SubSvc).GetByUserID)
		admin.POST("/users/:id/status", middleware.AuditAction("user.toggle_status"), adminUserH.UserToggleStatus)
		admin.POST("/users/:id/activate-plan", middleware.AuditAction("user.activate_plan"), adminUserH.UserActivatePlan)

		admin.GET("/traffic/top", systemH.TrafficTop)
		admin.GET("/dashboard", systemH.Dashboard)
		// 系统实时状态（CPU/内存/磁盘/网络速度/在线节点用户数）— 管理端完整版
		admin.GET("/system/stats", sysStatsH.AdminSystemStats)

		admin.POST("/system/rotate-hmac", middleware.RBAC(middleware.PermKeyManage), middleware.AuditAction("system.rotate_hmac"), systemH.RotateHMAC)
		admin.GET("/system/login-audit", middleware.RBAC(middleware.PermGlobalSec), systemH.LoginAudit)
		admin.POST("/system/backup", middleware.RBAC(middleware.PermBackup), middleware.AuditAction("system.backup"), systemH.BackupToFile)
		admin.GET("/system/backups", middleware.RBAC(middleware.PermBackup), systemH.ListBackups)
		admin.DELETE("/system/backups/:name", middleware.RBAC(middleware.PermBackup), middleware.AuditAction("system.backup_delete"), systemH.DeleteBackup)
		admin.GET("/system/backups/:name/download", middleware.RBAC(middleware.PermBackup), systemH.DownloadBackup)
		admin.GET("/system/sub-config", systemH.GetSubConfig)
		admin.PUT("/system/sub-config", middleware.RBAC(middleware.PermGlobalSec), middleware.AuditAction("system.sub_config"), systemH.SubConfig)
		admin.GET("/system/pay-config", systemH.GetPayConfig)
		admin.PUT("/system/pay-config", middleware.RBAC(middleware.PermGlobalSec), middleware.AuditAction("system.pay_config"), systemH.UpdatePayConfig)
		admin.POST("/system/pay-config/test", middleware.RBAC(middleware.PermGlobalSec), middleware.AuditAction("system.pay_config_test"), systemH.TestPayConfig)

		// 系统更新 & GitHub 同步
		admin.GET("/system/git-status", middleware.RBAC(middleware.PermBackup), systemH.GitStatus)
		admin.POST("/system/git-pull", middleware.RBAC(middleware.PermBackup), middleware.AuditAction("system.git_pull"), systemH.GitPull)
		admin.GET("/system/git-pull-log", middleware.RBAC(middleware.PermBackup), systemH.GitPullLog)
		admin.DELETE("/system/git-pull-log", middleware.RBAC(middleware.PermBackup), middleware.AuditAction("system.git_pull_log_clear"), systemH.GitPullClearLog)
		admin.POST("/system/git-push", middleware.RBAC(middleware.PermBackup), middleware.AuditAction("system.git_push"), systemH.GitPush)
		admin.POST("/system/restart", middleware.RBAC(middleware.PermBackup), middleware.AuditAction("system.restart"), systemH.SystemRestart)

		// 磁盘管理
		admin.GET("/system/disk-usage", middleware.RBAC(middleware.PermBackup), systemH.DiskUsage)
		admin.POST("/system/disk-cleanup", middleware.RBAC(middleware.PermBackup), middleware.AuditAction("system.disk_cleanup"), systemH.DiskCleanup)

		// 服务日志监控(容器列表 + 历史日志 + SSE 实时流)
		// 用于在仪表盘内嵌日志监控卡片, 半小时轮询拉取最近日志, 报错高亮
		admin.GET("/system/containers", middleware.RBAC(middleware.PermBackup), systemH.ContainerList)
		admin.GET("/system/containers/:name/logs", middleware.RBAC(middleware.PermBackup), systemH.ContainerLogs)
		admin.GET("/system/containers/:name/logs/stream", middleware.RBAC(middleware.PermBackup), systemH.ContainerLogStream)

		// 通知配置
		admin.GET("/system/notify-config", systemH.GetNotifyConfig)
		admin.PUT("/system/notify-config", middleware.AuditAction("system.notify_config"), systemH.UpdateNotifyConfig)
		admin.POST("/system/notify-config/test", systemH.TestNotifyConfig)

		admin.GET("/plans", planH.AdminPlanList)
		admin.POST("/plans", middleware.AuditAction("plan.create"), planH.AdminPlanCreate)
		admin.PUT("/plans/:id", middleware.AuditAction("plan.update"), planH.AdminPlanUpdate)
		admin.DELETE("/plans/:id", middleware.AuditAction("plan.delete"), planH.AdminPlanDelete)

		// 注意: /orders/stats 必须在 /orders/:id 之前注册, 避免 stats 被当作 :id 匹配
		admin.GET("/orders/stats", orderH.AdminOrderStats)
		admin.GET("/orders", orderH.AdminOrderList)
		admin.POST("/orders/:id/mark-paid", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("order.mark_paid"), orderH.AdminMarkPaid)
		admin.POST("/orders/:id/refund", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("order.refund"), orderH.AdminRefund)
		admin.POST("/orders/:id/cancel", middleware.AuditAction("order.cancel"), orderH.AdminCancelOrder)

		admin.GET("/coupons", couponH.AdminCouponList)
		admin.POST("/coupons", middleware.AuditAction("coupon.create"), couponH.AdminCouponCreate)
		admin.PUT("/coupons/:id", middleware.AuditAction("coupon.update"), couponH.AdminCouponUpdate)
		admin.DELETE("/coupons/:id", middleware.AuditAction("coupon.delete"), couponH.AdminCouponDelete)
		admin.PATCH("/coupons/:id/status", middleware.AuditAction("coupon.toggle_status"), couponH.AdminCouponToggleStatus)

		// 公告管理
		announceAdminH := NewAdminAnnouncementHandler(deps.AnnounceRepo)
		admin.GET("/announcements", announceAdminH.AdminListAnnouncements)
		admin.POST("/announcements", middleware.AuditAction("announcement.create"), announceAdminH.AdminCreateAnnouncement)
		admin.PUT("/announcements/:id", middleware.AuditAction("announcement.update"), announceAdminH.AdminUpdateAnnouncement)
		admin.DELETE("/announcements/:id", middleware.AuditAction("announcement.delete"), announceAdminH.AdminDeleteAnnouncement)
		admin.PATCH("/announcements/:id/pin", middleware.AuditAction("announcement.pin"), announceAdminH.AdminPinAnnouncement)

		// 流量趋势
		admin.GET("/traffic/trend", systemH.TrafficTrend)

		// 工单管理
		admin.GET("/tickets", ticketH.AdminListTickets)
		admin.GET("/tickets/:id", ticketH.AdminGetTicket)
		admin.POST("/tickets/:id/reply", middleware.AuditAction("ticket.reply"), ticketH.AdminReplyTicket)
		admin.POST("/tickets/:id/close", middleware.AuditAction("ticket.close"), ticketH.AdminCloseTicket)
			// 管理员操作审计日志 (2026-07-14 新增)
			admin.GET("/audit", middleware.RBAC(middleware.PermGlobalSec), systemH.AdminAuditLog)
	}

	r.NoRoute(func(c *gin.Context) {
		response.Fail(c, response.CodeNotFound)
	})
	r.NoMethod(func(c *gin.Context) {
		response.Fail(c, response.CodeParamError)
	})
}
