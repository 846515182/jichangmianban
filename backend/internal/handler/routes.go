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
	ReferralRepo   *repo.ReferralRepo

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
	ReferralSvc *service.ReferralService
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
	referralRepo := repo.NewReferralRepo(db)

	jwtMgr := security.NewJWTManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)

	referralSvc := service.NewReferralService(referralRepo, userRepo, settingRepo)
	orderSvc := service.NewOrderService(orderRepo, planRepo, couponRepo, userRepo, referralSvc)
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
		ReferralRepo:   referralRepo,
		NodeSvc:        service.NewNodeService(nodeRepo),
		UserSvc:        service.NewUserService(userRepo),
		SubSvc:         service.NewSubscribeService(subRepo, nodeRepo, userRepo),
		TrafficSvc:     service.NewTrafficService(trafficRepo, nodeRepo, userRepo),
		PlanSvc:        service.NewPlanService(planRepo),
		OrderSvc:       orderSvc,
		PaymentSvc:     paymentSvc,
		EmailSvc:       emailSvc,
		RegisterSvc:    service.NewUserRegisterService(userRepo, planRepo, subRepo),
		SysStatsSvc:    service.NewSystemStatsService(nodeRepo, userRepo),
		TicketSvc:      service.NewTicketService(ticketRepo, userRepo, adminRepo),
		ReferralSvc:    referralSvc,
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

	authH := NewAuthHandler(deps.AdminRepo, deps.UserRepo, deps.LoginAuditRepo, deps.JWTMgr, deps.EmailSvc)
	authRegisterH := NewAuthRegisterHandler(deps.RegisterSvc)
	userH := NewUserHandler(deps.UserRepo, deps.NodeRepo, deps.AnnounceRepo, deps.SubSvc, deps.TrafficSvc)
	adminNodeH := NewAdminNodeHandler(deps.NodeSvc, deps.NodeRepo)
	adminUserH := NewAdminUserHandler(deps.UserSvc, deps.UserRepo, deps.SubSvc, deps.SubRepo, deps.OrderSvc, deps.NodeRepo)
	systemH := NewSystemHandler(deps.TrafficSvc, deps.SettingRepo, deps.LoginAuditRepo, deps.NodeRepo, deps.UserRepo, deps.SubRepo, deps.AdminRepo)
	systemH.SetPaymentService(deps.PaymentSvc)
	systemH.SetEmailService(deps.EmailSvc)
	sysStatsH := NewSystemStatsHandler(deps.SysStatsSvc)
	planH := NewPlanHandler(deps.PlanSvc)
	orderH := NewOrderHandler(deps.OrderSvc, deps.PaymentSvc)
	paymentH := NewPaymentHandler(deps.PaymentSvc)
	couponH := NewCouponHandler(deps.CouponRepo, deps.OrderRepo)
	ticketH := NewTicketHandler(deps.TicketSvc)
	referralH := NewReferralHandler(deps.ReferralSvc)
	adminReferralH := NewAdminReferralHandler(deps.ReferralSvc)

	api := r.Group("/api/v1")

	auth := api.Group("/auth")
	{
		// P1-RateLimit: 登录接口加 IP 限流(10 次/分钟), 防止暴力撞库
		auth.POST("/login",
			middleware.RateLimitByIP("login:ip:", 10, time.Minute),
			authH.Login)
		// P1-RegAbuse: 注册限流改由 handler 内成功后再计数(3 次/小时/IP),
		// 避免校验/验证码失败误触限流导致 NAT/校园网用户大面积无法注册。
		auth.POST("/register", authRegisterH.Register)
		auth.POST("/refresh", authH.Refresh)
		auth.POST("/logout", middleware.AnyAuth(), authH.Logout)
		auth.POST("/change-password", middleware.AnyAuth(), authH.ChangePassword)
		auth.POST("/logout-all", middleware.AnyAuth(), authH.LogoutAll)
		// 修复 P0-2: ChangeEmail 路由注册(原前端调用但后端路由缺失, 整页不可用)
		auth.POST("/change-email", middleware.AnyAuth(), authH.ChangeEmail)
	}

	// 修复 P0-2: 邮箱验证码发送路由(register/change_email 两种场景)
	email := api.Group("/email")
	{
		// P1-RateLimit: 验证码发送限流(5 次/小时/IP), 防止邮件服务被刷
		email.POST("/send-code",
			middleware.RateLimitByIP("emailcode:ip:", 5, time.Hour),
			middleware.AnyAuth(), authH.SendEmailCode)
	}

	api.GET("/captcha", GetCaptcha)

	pay := api.Group("/payment")
	{
		pay.GET("/notify", paymentH.Notify)
		pay.POST("/notify", paymentH.Notify)
		pay.GET("/return", paymentH.Return)
	}

	// 公开订阅拉取(无需 JWT, 通过 token+sig 认证)
	// P0-PublicSubscribe: 双维度限流防 DB 被打爆
	//   - IP 维度: 10 次/分钟(正常用户 1-2 次, 允许重试)
	//   - token 维度: 6 次/分钟(Clash/V2RayN 自动刷新通常 1 次足够)
	// 两者独立计数, 任一超限即拒绝(429)
	// Redis 故障时 fail-open(放行), 避免全站订阅不可用
	api.GET("/subscribe",
		middleware.RateLimitByIP("sub:ip:", 10, time.Minute),
		middleware.RateLimitByParam("token", "sub:tok:", 6, time.Minute),
		userH.PublicSubscribe)

	user := api.Group("/user")
	user.Use(middleware.UserAuth())
	{
		user.GET("/info", userH.UserInfo)
		user.GET("/login-logs", authH.LoginLogs)
		user.GET("/subscribe", userH.Subscribe)
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
		// 邀请返利
		user.GET("/referral/invite-code", referralH.GetMyInviteCode)
		user.GET("/referral/stats", referralH.GetReferralStats)
		user.GET("/referral/invitations", referralH.ListInvitations)
		user.GET("/referral/rewards", referralH.ListRewards)
		user.POST("/referral/bind", referralH.BindInviteCode)
	}

	// 兼容老路径 /api/v1/tickets/* (与前端约定一致)
	tickets := api.Group("/tickets")
	tickets.Use(middleware.AnyAuth())
	{
		// 用户的 mine/list: 走 user handler
		tickets.GET("/mine", ticketH.UserListTickets)
		// 用户/管理员共用的 reply/close: 根据 role 区分(handler 内部)
		tickets.POST("/:id/reply", ticketH.ReplyAlias)
		tickets.POST("/:id/close", ticketH.CloseAlias)
	}

	// 公开试用套餐信息(注册页展示, 无需登录)
	// 修复 P1-TRIAL-03: 注册页动态展示真实试用套餐。
	api.GET("/plans/trial", planH.PublicTrialPlan)

	userPub := api.Group("")
	userPub.Use(middleware.UserAuth())
	{
		userPub.GET("/nodes/list", userH.NodeList)
		userPub.GET("/nodes/latency", userH.NodeLatency)
		userPub.GET("/announcements", userH.Announcements)
		userPub.GET("/plans", planH.UserPlanList)
	}

	// SSH WebSocket 终端（token 通过 query 参数传递，handler 内部自验证）
	sshTermH := NewSSHTerminalHandler(deps.NodeRepo, deps.JWTMgr)
	api.GET("/admin/nodes/:id/terminal",
		middleware.AdminAuth(),
		middleware.RBAC(middleware.PermNodeManage),
		sshTermH.Terminal,
	)

	admin := api.Group("/admin")
	admin.Use(middleware.AdminAuth())
	{
		admin.GET("/nodes", adminNodeH.NodeList)
		// 同机多节点端口推荐与冲突检测
		admin.GET("/nodes/suggest-port", adminNodeH.SuggestPort)
		// P1-RBAC: NodeDetail 返回 node_token + REALITY 私钥, 需 PermKeyManage 权限
		admin.GET("/nodes/:id", middleware.RBAC(middleware.PermKeyManage), adminNodeH.NodeDetail)
		// 节点负载监控大盘: 返回所有节点实时负载评分 + 状态汇总, 供前端 Monitor.vue 使用
		admin.GET("/nodes/monitor", adminNodeH.NodeMonitor)
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
		// 修复 P0-3: /admin/users 写操作全部加 RBAC(PermFundManage), 与 plans/coupons/orders 对齐
		// 原代码仅有 AuditAction, 普通 admin 可绕过财务权限执行 activate-plan/reset-traffic/DELETE
		admin.POST("/users", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("user.create"), adminUserH.UserCreate)
		admin.PUT("/users/:id", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("user.update"), adminUserH.UserUpdate)
		admin.DELETE("/users/:id", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("user.delete"), adminUserH.UserDelete)
		// 物理删除(彻底清理测试数据, 释放 username/email 唯一索引, 重新注册不冲突)
		admin.DELETE("/users/:id/hard", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("user.hard_delete"), adminUserH.UserHardDelete)
		admin.POST("/users/import", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("user.import"), adminUserH.UserImport)
		admin.POST("/users/:id/reset-traffic", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("user.reset_traffic"), adminUserH.UserResetTraffic)
		// P1-RBAC: 订阅查询返回 sub_token, 需 PermFundManage 权限
		admin.GET("/subscriptions", middleware.RBAC(middleware.PermFundManage), NewAdminSubscriptionHandler(deps.SubRepo, deps.SubSvc).List)
		admin.GET("/users/:id/subscription", middleware.RBAC(middleware.PermFundManage), NewAdminSubscriptionHandler(deps.SubRepo, deps.SubSvc).GetByUserID)
		admin.POST("/users/:id/status", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("user.toggle_status"), adminUserH.UserToggleStatus)
		admin.POST("/users/:id/activate-plan", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("user.activate_plan"), adminUserH.UserActivatePlan)

		// P0: 管理员自我管理 API(仅 super_admin 可操作)
		admin.GET("/admins", middleware.RBAC(middleware.PermGlobalSec), systemH.AdminList)
		admin.POST("/admins", middleware.RBAC(middleware.PermGlobalSec), middleware.AuditAction("admin.create"), systemH.AdminCreate)
		admin.PUT("/admins/:id", middleware.RBAC(middleware.PermGlobalSec), middleware.AuditAction("admin.update"), systemH.AdminUpdate)
		admin.DELETE("/admins/:id", middleware.RBAC(middleware.PermGlobalSec), middleware.AuditAction("admin.delete"), systemH.AdminDelete)

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

		// 智能运维: 全局错误聚合 + 一键自动清理(自动清缓存/修脏配置/清 stale 缓存)
		admin.GET("/system/errors", middleware.RBAC(middleware.PermBackup), systemH.ErrorsAggregate)
		admin.POST("/system/auto-cleanup", middleware.RBAC(middleware.PermBackup), middleware.AuditAction("system.auto_cleanup"), systemH.AutoOpsCleanup)

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
		// 修复 P2: plans 增删改加 RBAC(PermFundManage), 防止普通 admin 随意改价
		admin.POST("/plans", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("plan.create"), planH.AdminPlanCreate)
		admin.PUT("/plans/:id", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("plan.update"), planH.AdminPlanUpdate)
		admin.DELETE("/plans/:id", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("plan.delete"), planH.AdminPlanDelete)

		// 注意: /orders/stats 必须在 /orders/:id 之前注册, 避免 stats 被当作 :id 匹配
		admin.GET("/orders/stats", orderH.AdminOrderStats)
		admin.GET("/orders", orderH.AdminOrderList)
		admin.POST("/orders/:id/mark-paid", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("order.mark_paid"), orderH.AdminMarkPaid)
		admin.POST("/orders/:id/refund", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("order.refund"), orderH.AdminRefund)
		// 修复 P1: 旧版只有 AuditAction 无 RBAC, 普通 admin 可绕过资金管理权限强制取消订单(释放优惠券、改订单状态)。
		admin.POST("/orders/:id/cancel", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("order.cancel"), orderH.AdminCancelOrder)

		admin.GET("/coupons", couponH.AdminCouponList)
		// 修复 P2: 旧版 plans/coupons 增删改只有 AuditAction 无 RBAC, 普通 admin 可随意改价、改优惠券面额(如把 fixed 100 改成 100000)、停用优惠券, 直接影响资金。
		admin.POST("/coupons", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("coupon.create"), couponH.AdminCouponCreate)
		admin.PUT("/coupons/:id", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("coupon.update"), couponH.AdminCouponUpdate)
		admin.DELETE("/coupons/:id", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("coupon.delete"), couponH.AdminCouponDelete)
		admin.PATCH("/coupons/:id/status", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("coupon.toggle_status"), couponH.AdminCouponToggleStatus)

		// 邀请返利管理
		admin.GET("/referral/config", adminReferralH.GetReferralConfig)
		admin.PUT("/referral/config", middleware.RBAC(middleware.PermFundManage), middleware.AuditAction("referral.config"), adminReferralH.UpdateReferralConfig)
		admin.GET("/referrals", adminReferralH.AdminReferralList)
		admin.GET("/referral/rewards", adminReferralH.AdminReferralRewards)

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
