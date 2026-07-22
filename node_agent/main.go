package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"nexus-agent/proto"
)

// agentVersion 节点 agent 自身版本号(上报给面板)
const agentVersion = "nexus-agent/0.1.0"

// Config 节点 agent 运行配置(来自环境变量)
type Config struct {
	PanelGrpcAddr string // 面板 gRPC 地址，如 177.3.32.94:50051
	NodeToken     string // 面板生成的节点通信令牌
	XrayVersion   string // Xray-core 版本，如 v26.6.1
	ListenPort    int    // Xray 监听端口
	HealthPort    int    // 健康检查 HTTP 端口
}

// Agent 节点 agent 主对象
type Agent struct {
	cfg     *Config
	client  *PanelClient
	xray    *XrayManager
	traffic *TrafficCounter

	// 用户级流量统计（Xray StatsService）
	trafficStats *UserTraffic
	apiPort      int // Xray API 端口

	startTime time.Time

	// 注册后从面板拿到的节点信息
	mu          sync.RWMutex
	nodeID      string
	nodePort    int
	configVer   string
	xrayCfgJSON string

	// fatalShutdown=1 表示因致命错误(节点被删/token失效)已停止 Xray 代理服务
	// 进入停服状态后不再发心跳，进程不退出(避免 docker unless-stopped 重启死循环)
	// 修复 NODE-FATAL-RECOVERY: fatalShutdown 不再是永久状态, tryRecoverFromFatal 会周期性重新 bootstrap, 成功则清除本标记
	fatalShutdown int32

	// recoverInProgress=1 表示当前有 tryRecoverFromFatal 在执行(bootstrap 最多 2.5 分钟)
	// 用 CAS 防止 recoverTicker 与 handleFatalShutdown 立即恢复并发触发多次 bootstrap
	recoverInProgress int32

	// 实际 Xray 监听端口（面板分配或 LISTEN_PORT 覆盖）
	// 用于健康检查，保证始终与 Xray 实际监听端口一致
	// P0-AG1: effectivePort 纳入 a.mu 保护, 读写均需持锁
	effectivePort int

	// 修复 NODE-HEALTH-02: Xray 崩溃自动重启的限流记录
	// restartHistory 保存最近 10 分钟窗口内的重启时间戳, 超 3 次暂停自动重启
	restartMu      sync.Mutex
	restartHistory []time.Time

	// P0-AG2: applyConfig 串行化锁, 防止并发 applyConfig 导致 Xray 进程状态混乱
	applyMu sync.Mutex

	// P0-AG4/P0-AG5: 主 ctx 引用, 供 handleFatalShutdown 等异步流程派生 ctx
	mainCtx context.Context
}

// safeGo 安全启动 goroutine
func safeGo(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[node_agent] goroutine panic recovered: %v", r)
			}
		}()
		fn()
	}()
}
func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("nexus-agent starting (%s)", agentVersion)

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("配置错误: %v", err)
	}
	log.Printf("配置: panel=%s listen_port=%d health_port=%d xray=%s",
		cfg.PanelGrpcAddr, cfg.ListenPort, cfg.HealthPort, cfg.XrayVersion)

	// P0-AG4: 信号注册提前到 main 开头, 确保 waitForBootstrapRecovery 等长阻塞流程能响应 SIGTERM/SIGINT
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	safeGo(func() {
		sig := <-sigCh
		log.Printf("收到信号 %v，开始关闭...", sig)
		cancel()
	})

	agent := &Agent{
		cfg:       cfg,
		startTime: time.Now(),
		traffic:   NewTrafficCounter(),
		mainCtx:   ctx,
	}

	// 1. 启动健康检查 HTTP 服务
	safeGo(agent.runHealthServer)

	// 2. 建立 gRPC 连接(非阻塞，底层自动重连)
	// 修复 NODE-BOOTSTRAP-01 (P0): 旧版 NewPanelClient/NewXrayManager 失败用 log.Fatalf
	// 直接退出进程, 配合 docker restart=unless-stopped 进入死循环: 退出→重启→失败→退出→...
	// 现改为: 失败后进入等待重试模式(同 bootstrap), 进程不退出。适用于:
	//   - 面板 gRPC 暂时不可达(面板重启中)
	//   - CA 证书路径配错, 运维修正后 docker restart 即可恢复
	//   - 文件系统临时异常(如磁盘满、权限问题)
	client, err := NewPanelClient(cfg.PanelGrpcAddr, ctx)
	if err != nil {
		log.Printf("[FATAL] 建立 gRPC 连接失败, 进入等待重试模式: %v", err)
		agent.waitForBootstrapRecovery(ctx)
		// recovery 成功后重新初始化 client
		client, err = NewPanelClient(cfg.PanelGrpcAddr, ctx)
		if err != nil {
			log.Fatalf("gRPC 连接 recovery 后仍然失败: %v", err)
		}
	}
	agent.client = client
	defer client.Close()

	// 3. 下载 Xray-core 二进制(若不存在)
	xm, err := NewXrayManager(cfg.XrayVersion)
	if err != nil {
		log.Printf("[FATAL] 初始化 Xray 管理器失败, 进入等待重试模式: %v", err)
		agent.waitForBootstrapRecovery(ctx)
		xm, err = NewXrayManager(cfg.XrayVersion)
		if err != nil {
			log.Fatalf("Xray 管理器 recovery 后仍然失败: %v", err)
		}
	}
	agent.xray = xm
	// 初始化用户级流量统计器（Xray API 端口: 10085）
	agent.apiPort = defaultXrayAPIPort
	agent.trafficStats = NewUserTraffic(xm.BinaryPath(), agent.apiPort)
	if err := xm.EnsureBinary(); err != nil {
		log.Printf("警告: 准备 Xray 二进制失败(将继续尝试): %v", err)
	}

	// 4. 注册节点 + 拉取配置 + 启动 Xray
	// 修复 NODE-BOOTSTRAP-01 (P0): 旧版 bootstrap 失败后 log.Fatalf 退出进程,
	// 配合 docker restart=unless-stopped 会进入死循环: 退出→重启→注册失败→退出→...
	// 持续刷 docker 日志、占 CPU。现改为: bootstrap 失败进入"休眠+周期重试"模式,
	// 进程不退出, 等待运维在面板侧恢复节点(token 复用/节点重新创建)后自动注册成功。
	// 这样避免 docker 死循环; 同时也给运维时间在面板侧排查问题。
	if err := agent.bootstrap(ctx); err != nil {
		log.Printf("[FATAL] 首次启动失败, 进入等待重试模式(不退出进程, 避免 docker 死循环): %v", err)
		agent.waitForBootstrapRecovery(ctx)
	}

	// 5. 启动心跳定时器(每 30s)
	var wg sync.WaitGroup
	wg.Add(1)
	safeGo(func() {
		defer wg.Done()
		agent.heartbeatLoop(ctx)
	})

	// 6. 启动流量上报定时器(每 60s)
	wg.Add(1)
	safeGo(func() {
		defer wg.Done()
		agent.trafficLoop(ctx)
	})

	// 7. 等待信号触发 ctx 取消, 然后等所有后台 goroutine 退出
	<-ctx.Done()
	wg.Wait()

	if agent.xray != nil {
		if err := agent.xray.Stop(); err != nil {
			log.Printf("停止 Xray 失败: %v", err)
		}
	}
	log.Printf("nexus-agent 已退出")
}

// loadConfig 从环境变量加载配置
func loadConfig() (*Config, error) {
	cfg := &Config{
		PanelGrpcAddr: os.Getenv("PANEL_GRPC_ADDR"),
		NodeToken:     os.Getenv("NODE_TOKEN"),
		XrayVersion:   getenvDefault("XRAY_VERSION", "v26.6.1"),
		ListenPort:    getenvInt("LISTEN_PORT", 0),
		HealthPort:    getenvInt("HEALTH_PORT", 50052),
	}
	if cfg.PanelGrpcAddr == "" {
		return nil, fmt.Errorf("环境变量 PANEL_GRPC_ADDR 必填")
	}
	if cfg.NodeToken == "" {
		return nil, fmt.Errorf("环境变量 NODE_TOKEN 必填")
	}
	return cfg, nil
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// runHealthServer 启动健康检查 HTTP 服务
func (a *Agent) runHealthServer() {
	mux := http.NewServeMux()
	// /healthz: 完整健康检查(含 REALITY 自连测试)，Docker healthcheck 用
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		health := a.CheckProxyHealth()
		if health.ProxyReachable {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(health)
	})
	// /livez: 轻量级存活检查(不做 REALITY 自连，仅检查进程存活)
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// P1-AG12: 健康检查服务绑定 127.0.0.1, 避免暴露到公网被外部扫描
	addr := fmt.Sprintf("127.0.0.1:%d", a.cfg.HealthPort)
	log.Printf("健康检查服务监听 %s (/healthz=完整检查, /livez=存活检查)", addr)
	srv := &http.Server{Addr: addr, Handler: mux}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("健康检查服务退出: %v", err)
	}
}

// bootstrap 注册节点 + 拉取配置 + 启动 Xray，带重试直到成功
// P0-AG4: 接受 ctx 参数, 内部 RPC 调用通过 PanelClient.mainCtx 派生 ctx, ctx 取消时立即返回
func (a *Agent) bootstrap(ctx context.Context) error {
	const maxAttempts = 30
	// 注册
	var nodeInfo *proto.NodeInfo
	for i := 1; i <= maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		ni, err := a.client.Register(a.cfg.NodeToken, hostname(), agentVersion)
		if err != nil {
			log.Printf("注册失败(%d/%d): %v", i, maxAttempts, err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
			continue
		}
		if ni.GetResp().GetCode() != 0 {
			log.Printf("注册被拒(%d/%d): code=%d msg=%s", i, maxAttempts, ni.GetResp().GetCode(), ni.GetResp().GetMessage())
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
			continue
		}
		nodeInfo = ni.GetNode()
		break
	}
	if nodeInfo == nil {
		return fmt.Errorf("注册节点失败: 已达最大重试次数")
	}

	a.mu.Lock()
	a.nodeID = nodeInfo.GetId()
	a.nodePort = int(nodeInfo.GetPort())
	a.mu.Unlock()
	log.Printf("注册成功: node_id=%s name=%s protocol=%v port=%d", nodeInfo.GetId(), nodeInfo.GetName(), nodeInfo.GetProtocol(), a.nodePort)

	// 拉取配置
	for i := 1; i <= maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		cfgResp, err := a.client.GetConfig(a.getNodeID(), a.cfg.NodeToken, "")
		if err != nil {
			log.Printf("拉取配置失败(%d/%d): %v", i, maxAttempts, err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
			continue
		}
		if cfgResp.GetResp().GetCode() != 0 {
			log.Printf("拉取配置被拒(%d/%d): code=%d msg=%s", i, maxAttempts, cfgResp.GetResp().GetCode(), cfgResp.GetResp().GetMessage())
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
			continue
		}
		a.mu.Lock()
		a.configVer = cfgResp.GetConfigVersion()
		a.xrayCfgJSON = cfgResp.GetXrayConfig()
		a.mu.Unlock()
		log.Printf("拉取配置成功: config_version=%s xray_config_len=%d", cfgResp.GetConfigVersion(), len(cfgResp.GetXrayConfig()))
		break
	}
	if a.xrayCfgJSON == "" {
		return fmt.Errorf("拉取 Xray 配置失败: 已达最大重试次数")
	}

	// 写入配置并启动 Xray
	return a.applyConfig()
}

// waitForBootstrapRecovery bootstrap 首次失败后的等待重试模式。
//
// 修复 NODE-BOOTSTRAP-01 (P0): 旧版 bootstrap 失败 log.Fatalf 退出进程,
// 配合 docker restart=unless-stopped 进入死循环(退出→重启→注册失败→退出→...),
// 持续刷 docker 日志、占 CPU。
//
// 现改为: 每 5 分钟重试一次 bootstrap, 进程不退出。
// 适用场景:
//   - 面板重启中 gRPC 暂时不可达
//   - 节点 token 被误删, 运维在面板重新创建节点后能自动恢复
//   - 面板 IP 变化后运维修正 .env 后能自动恢复
//
// 一旦 bootstrap 成功则返回, 主流程继续启动心跳/流量上报。
//
// P0-AG4: 接受 ctx 参数, select 监听 ctx.Done(), 收到 SIGTERM/SIGINT 时立即返回不再重试
func (a *Agent) waitForBootstrapRecovery(ctx context.Context) {
	const retryInterval = 5 * time.Minute
	attempt := 0
	for {
		attempt++
		log.Printf("[recovery] 等待 %v 后第 %d 次重试 bootstrap...", retryInterval, attempt)
		select {
		case <-ctx.Done():
			log.Printf("[recovery] ctx 已取消, 停止重试 bootstrap")
			return
		case <-time.After(retryInterval):
		}
		if err := a.bootstrap(ctx); err != nil {
			log.Printf("[recovery] 第 %d 次重试失败: %v", attempt, err)
			continue
		}
		log.Printf("[recovery] 第 %d 次重试成功, 继续启动心跳/流量上报", attempt)
		return
	}
}

// applyConfig 写入 Xray 配置文件并(重启)启动 Xray 进程
// P0-AG2: 整体持 applyMu 锁串行化, 防止并发 applyConfig 导致 Xray 进程状态混乱
// (reloadConfig + autoRestartXray + heartbeatLoop 可能并发触发)
func (a *Agent) applyConfig() error {
	a.applyMu.Lock()
	defer a.applyMu.Unlock()

	a.mu.RLock()
	cfgJSON := a.xrayCfgJSON
	nodePort := a.nodePort
	a.mu.RUnlock()

	// 优先使用用户显式设置的 LISTEN_PORT（非0），否则使用面板返回的节点端口
	// 这样可以确保订阅中的端口与 Xray 实际监听的端口一致
	listenPort := a.cfg.ListenPort
	if listenPort == 0 {
		listenPort = nodePort
	}
	if listenPort == 0 {
		listenPort = 443 // 最终兜底
	}
	// P0-AG1: effectivePort 写入需持 a.mu 锁, 防止与健康检查/连接数统计的读并发
	a.mu.Lock()
	a.effectivePort = listenPort
	a.mu.Unlock()

	finalCfg, err := OverrideListenPort(cfgJSON, listenPort)
	if err != nil {
		return fmt.Errorf("覆盖监听端口失败: %w", err)
	}

	// 注入 Xray Stats/API 配置，启用用户级流量统计
	statsCfg, err := InjectStatsConfig(finalCfg, a.apiPort)
	if err != nil {
		log.Printf("[WARN] 注入 Stats 配置失败(不影响代理功能): %v", err)
	} else {
		finalCfg = statsCfg
	}

	if err := a.xray.WriteConfig(finalCfg); err != nil {
		return fmt.Errorf("写入 Xray 配置失败: %w", err)
	}

	if a.xray.IsRunning() {
		log.Printf("配置变更，重启 Xray...")
		if err := a.xray.Restart(); err != nil {
			return fmt.Errorf("重启 Xray 失败: %w", err)
		}
	} else {
		if err := a.xray.Start(); err != nil {
			return fmt.Errorf("启动 Xray 失败: %w", err)
		}
	}
	log.Printf("Xray 已启动，监听端口 %d", listenPort)
	return nil
}

// heartbeatLoop 每 30s 上报心跳；若面板提示配置/用户变更则重新拉取并重启 Xray
//
// 修复 NODE-OFFLINE-01 (P0): 旧版心跳失败后直接 return, 等下一个 30s tick 才重试。
// 面板重启(一键更新)期间, agent 心跳连续失败, 面板回来后还要等最多 30s 才能恢复
// online=true, 这段时间节点在面板上显示离线。现在失败后 10s 内立即补一次心跳,
// 面板一回来就能尽快把 online 刷新回 true。
//
// 修复 NODE-FATAL-RECOVERY (P0, 2026-07-19): 旧版 fatalShutdown 是永久状态, 一旦触发
// (如 panel 重启期间 gRPC 返回 Unauthenticated/NotFound), agent 永远不再发心跳,
// 节点永久离线, 必须 docker restart 才能恢复。这就是"修一下 panel 节点就掉线"的根因。
// 现改为: fatalShutdown 状态下, 每 1 分钟尝试一次 tryRecoverFromFatal(重新 bootstrap),
// 成功则清除 fatalShutdown 标记, 恢复正常心跳。panel 重启后 agent 自动恢复, 无需人工介入。
// 此外 handleFatalShutdown 触发后立即异步执行一次恢复(不等 ticker), 加快 panel 短暂重启场景的恢复。
func (a *Agent) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	// 恢复探测 ticker: fatalShutdown 状态下每 1 分钟尝试恢复
	// (旧版 5 分钟太慢, panel 重启后节点最多离线 5+ 分钟, 用户感知明显)
	recoverTicker := time.NewTicker(1 * time.Minute)
	defer recoverTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// fatalShutdown 状态下不发心跳, 等恢复探测 ticker 处理
			if atomic.LoadInt32(&a.fatalShutdown) == 1 {
				continue
			}
			ok := a.doHeartbeat(ctx)
			if !ok {
				// 心跳失败(非致命), 10s 后立即补一次, 不等下一个 30s 周期
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Second):
					// 补发前再次检查 fatalShutdown(可能上一次心跳触发了致命错误)
					if atomic.LoadInt32(&a.fatalShutdown) == 1 {
						continue
					}
					a.doHeartbeat(ctx)
				}
			}
		case <-recoverTicker.C:
			// 仅在 fatalShutdown 状态下尝试恢复
			if atomic.LoadInt32(&a.fatalShutdown) == 1 {
				a.tryRecoverFromFatal(ctx)
			}
		}
	}
}

// tryRecoverFromFatal 尝试从 fatalShutdown 状态恢复。
//
// 修复 NODE-FATAL-RECOVERY (P0, 2026-07-19):
// 旧版 handleFatalShutdown 是永久停服, agent 进程不退出但永远不再发心跳。
// 典型事故场景: panel 重启期间 gRPC 短暂返回 Unauthenticated/NotFound(因为 panel
// 还没加载完节点表), agent 误判为"节点被删/token 失效", 触发 fatalShutdown 永久停服。
// panel 起来后 agent 也不重连, 节点永久离线, 必须人工 docker restart。
//
// 现改为: 每 1 分钟尝试一次完整 bootstrap(重新注册 + 拉配置 + 启动 Xray),
// - 成功: 清除 fatalShutdown 标记, 恢复正常心跳
// - 失败: 继续等下一个 1 分钟周期重试, 进程不退出
//
// 注意: bootstrap 内部已有 30 次重试(每次 5s 间隔), 一次 tryRecoverFromFatal
// 调用最多耗时 2.5 分钟, 失败后等 1 分钟再试, 不会高频刷日志。
// recoverInProgress CAS 防止 handleFatalShutdown 立即恢复 与 recoverTicker 周期恢复
// 并发触发多次 bootstrap(bootstrap 修改共享状态, 不能并发)。
func (a *Agent) tryRecoverFromFatal(ctx context.Context) {
	if !atomic.CompareAndSwapInt32(&a.recoverInProgress, 0, 1) {
		log.Printf("[recovery] 已有恢复尝试在执行, 跳过本次")
		return
	}
	defer atomic.StoreInt32(&a.recoverInProgress, 0)

	log.Printf("[recovery] fatalShutdown 状态下尝试恢复: 重新 bootstrap...")
	// P0-AG5: bootstrap 接受主 ctx, ctx 取消时立即终止(响应 SIGTERM)
	if err := a.bootstrap(ctx); err != nil {
		log.Printf("[recovery] 恢复失败, 将在 1 分钟后重试: %v", err)
		return
	}
	// bootstrap 成功, 清除 fatalShutdown 标记, 恢复心跳
	atomic.StoreInt32(&a.fatalShutdown, 0)
	log.Printf("[recovery] bootstrap 成功, 已清除 fatalShutdown 标记, 恢复正常心跳")
}

// doHeartbeat 发送一次心跳。返回 true 表示心跳成功(面板已刷新 online=true),
// 返回 false 表示心跳失败(网络错误/被拒等非致命情况), 调用方可据此决定是否补发。
// 致命错误(节点被删/token 失效)会触发 handleFatalShutdown, 此时也返回 false 但
// fatalShutdown 已置位, 后续不会再补发。
// P0-AG5/P0-AG6: 接受 ctx 参数, 传递给 autoRestartXray/reloadConfig(进而传给 execCommand)
func (a *Agent) doHeartbeat(ctx context.Context) bool {
	// 已因致命错误停服，跳过心跳(进程保持运行，避免 docker 重启死循环)
	if atomic.LoadInt32(&a.fatalShutdown) == 1 {
		return false
	}
	uptime := time.Since(a.startTime).Seconds()
	memTotal, memUsed := readMemInfo()
	memUsage := 0.0
	if memTotal > 0 {
		memUsage = float64(memUsed) / float64(memTotal) * 100
	}

	// 修复 NODE-HEALTH-02: Xray 进程崩溃后自动重启
	// 旧版 Xray 退出后 agent 仍发心跳, 面板 online=true 但代理已不可用
	// 这里检测 Xray 进程是否存活, 不存活则尝试重启(最多 3 次/10 分钟窗口)
	if a.xray != nil && !a.xray.IsRunning() {
		log.Printf("[health][WARN] Xray 进程未运行, 尝试自动重启...")
		if err := a.autoRestartXray(ctx); err != nil {
			log.Printf("[health][ERROR] Xray 自动重启失败: %v", err)
		}
	}

	// REALITY 自连健康检查(每次心跳时执行，约 30s 一次)
	health := a.CheckProxyHealth()
	if health.ProxyReachable {
		log.Printf("[health] 代理自连正常 latency=%dms", health.ProxyLatencyMs)
	} else {
		log.Printf("[health][ERROR] 代理自连失败: %s (latency=%dms)", health.ProxyError, health.ProxyLatencyMs)
	}

	// 修复 NODE-HEALTH-01: 上报 proxy 健康结果, 让面板区分 agent 进程可达 vs 代理服务可用
	// 修复 NODE-DATA-01: cpuUsage/onlineConns 原硬编码 0, 现在用真实值
	// 修复 NODE-DATA-02: trafficDown/Up 原塞进 trafficLimit/Used, 现在传 0 让 DB 字段为准
	cpuUsage := readCPUUsage()
	onlineConns := a.readOnlineConnections(ctx)
	resp, err := a.client.Heartbeat(
		a.getNodeID(), a.cfg.NodeToken, agentVersion,
		cpuUsage, memUsage, memTotal, onlineConns, uptime,
		0, 0, // trafficLimit/Used 由 DB 维护, 心跳不再上报流量
		health.ProxyReachable, health.ProxyLatencyMs, health.ProxyError,
	)
	if err != nil {
		// 致命错误: 节点已被面板删除/token 失效 → 停止 Xray 代理服务
		if isFatalHeartbeatError(err) {
			a.handleFatalShutdown(fmt.Sprintf("心跳失败: %v (节点可能已被面板删除或 token 失效)", err))
			return false
		}
		log.Printf("心跳失败: %v", err)
		return false
	}
	if resp.GetResp().GetCode() != 0 {
		msg := resp.GetResp().GetMessage()
		if isFatalHeartbeatMsg(msg) {
			a.handleFatalShutdown(fmt.Sprintf("心跳被拒: %s", msg))
			return false
		}
		log.Printf("心跳被拒: code=%d msg=%s", resp.GetResp().GetCode(), msg)
		return false
	}

	// 配置或用户变更 -> 重新拉取配置并应用
	if resp.GetConfigChanged() || resp.GetUsersChanged() {
		log.Printf("面板提示配置/用户已变更，重新拉取配置...")
		if err := a.reloadConfig(ctx); err != nil {
			log.Printf("重新拉取配置失败: %v", err)
		}
	}
	return true
}

// handleFatalShutdown 致命错误停服: 停止 Xray 并标记停服状态
// 不退出进程(docker restart=unless-stopped 会重启导致死循环)，保持容器运行但 Xray 已停
//
// 修复 NODE-FATAL-RECOVERY: 触发 fatalShutdown 后, 立即异步执行一次 tryRecoverFromFatal
// (不等 recoverTicker 的下一个 1 分钟周期)。这样 panel 短暂重启(几秒~几十秒)的场景下,
// agent 能在 panel 恢复后立即(5s 延迟后)尝试恢复, 不用等下一个 ticker 周期。
// recoverInProgress CAS 保证不会与 recoverTicker 并发执行多次 bootstrap。
func (a *Agent) handleFatalShutdown(reason string) {
	if !atomic.CompareAndSwapInt32(&a.fatalShutdown, 0, 1) {
		return // 已停服
	}
	log.Printf("[FATAL] %s，停止 Xray 代理服务", reason)
	if a.xray != nil {
		if err := a.xray.Stop(); err != nil {
			log.Printf("[FATAL] 停止 Xray 失败: %v", err)
		}
	}
	log.Printf("[FATAL] 节点已停服，5s 后异步触发恢复尝试(后续每 1 分钟重试)...")
	// 立即异步触发一次恢复, 不等 ticker (加快 panel 短暂重启场景的恢复)
	// 等 5s 是给 panel 一点喘息时间(刚返回 fatal 错误, 立即重试大概率还是失败)
	// P0-AG5: 使用主 ctx(从 a.mainCtx 取), ctx 取消时立即返回不再触发恢复
	ctx := a.mainCtx
	if ctx == nil {
		ctx = context.Background()
	}
	safeGo(func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
		a.tryRecoverFromFatal(ctx)
	})
}

// isFatalHeartbeatError 判断 gRPC 错误是否为致命错误(节点被删/token失效/被禁用)
// 这类错误不可恢复，继续运行 Xray 会导致"已删节点仍代理流量"的安全漏洞
// 修复: 使用 gRPC 标准状态码而非子串匹配，避免依赖语言环境
func isFatalHeartbeatError(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		// 非 gRPC 错误（如网络错误），不致命
		return false
	}
	switch st.Code() {
	case codes.NotFound, codes.Unauthenticated, codes.PermissionDenied:
		return true
	}
	return false
}

// isFatalHeartbeatMsg 判断心跳被拒消息是否为致命错误
func isFatalHeartbeatMsg(msg string) bool {
	return msg != "" && (strings.Contains(msg, "token") ||
		strings.Contains(msg, "节点不存在") ||
		strings.Contains(msg, "节点已禁用") ||
		strings.Contains(msg, "节点 token 无效"))
}

// reloadConfig 重新拉取 Xray 配置并重启进程
// P0-AG6: 接受 ctx 参数, 返回 error 供调用方决策(如 autoRestartXray 拉取失败时 fallback 到本地缓存)
func (a *Agent) reloadConfig(ctx context.Context) error {
	cfgResp, err := a.client.GetConfig(a.getNodeID(), a.cfg.NodeToken, a.getConfigVer())
	if err != nil {
		return fmt.Errorf("重新拉取配置失败: %w", err)
	}
	if cfgResp.GetResp().GetCode() != 0 {
		return fmt.Errorf("重新拉取配置被拒: code=%d msg=%s", cfgResp.GetResp().GetCode(), cfgResp.GetResp().GetMessage())
	}
	a.mu.Lock()
	a.configVer = cfgResp.GetConfigVersion()
	a.xrayCfgJSON = cfgResp.GetXrayConfig()
	a.mu.Unlock()
	if err := a.applyConfig(); err != nil {
		return fmt.Errorf("应用新配置失败: %w", err)
	}
	return nil
}

// trafficLoop 每 60s 读取系统流量增量并上报
func (a *Agent) trafficLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.doTrafficReport()
		}
	}
}

func (a *Agent) doTrafficReport() {
	// P1-AG14: fatalShutdown 状态下不上报流量(节点已停服, 不应再向面板发请求避免触发新的 fatal)
	if atomic.LoadInt32(&a.fatalShutdown) == 1 {
		return
	}

	nodeID := a.getNodeID()
	nodeToken := a.cfg.NodeToken

	// 方案1: 通过 Xray StatsService 获取用户级流量增量（精确）
	if a.trafficStats != nil {
		deltas, err := a.trafficStats.QueryDelta()
		if err != nil {
			log.Printf("[traffic] Xray Stats 查询失败: %v (跳过本轮上报)", err)
		} else if len(deltas) > 0 {
			// 转换为 proto TrafficRecord 并上报
			records := make([]*proto.TrafficRecord, 0, len(deltas))
			for _, d := range deltas {
				records = append(records, &proto.TrafficRecord{
					UserId:        d.UserID,
					UploadBytes:   d.Upload,
					DownloadBytes: d.Download,
				})
			}
			resp, err := a.client.ReportRealtime(nodeID, nodeToken, records)
			if err != nil {
				log.Printf("[traffic] 上报流量失败(%d 条记录): %v", len(records), err)
			} else {
				r := resp.GetResp()
				if r != nil && r.GetCode() != 0 {
					log.Printf("[traffic] 上报流量被拒(%d 条记录): code=%d msg=%s",
						len(records), r.GetCode(), r.GetMessage())
				} else {
					log.Printf("[traffic] 上报成功: %d 个用户, 共 %d 条流量记录",
						len(deltas), len(records))
				}
			}
			return // Xray Stats 成功上报后不再走节点级流量兜底
		}
	}

	// 方案2(兜底): 节点级 /proc/net/dev 流量汇总（仅在 Xray Stats 不可用时）
	upload, download := a.traffic.Peek()
	if upload == 0 && download == 0 {
		return
	}
	// 节点级流量作为兜底，以 node_id 为 user_id 上报（面板后端已不处理此格式，仅消费增量避免内存堆积）
	a.traffic.Commit(upload, download)
	log.Printf("[traffic] 节点级流量(兜底,未上报): upload=%d download=%d", upload, download)
}

// getNodeID / getConfigVer 并发安全读取
func (a *Agent) getNodeID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.nodeID
}

func (a *Agent) getConfigVer() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.configVer
}

// hostname 返回主机名(注册时上报)
func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}

// autoRestartXray Xray 进程崩溃后自动重启, 限流: 10 分钟窗口内最多 3 次
// 修复 NODE-HEALTH-02: 旧版 Xray 退出后 agent 继续发心跳但代理不可用, 无任何恢复机制
// 限流防止 Xray 配置错误导致无限重启死循环
// P0-AG6: 先尝试从面板拉新配置(可能配置已修复), 拉取失败则 fallback 到本地缓存配置重启
// 避免用坏掉的本地缓存配置反复重启导致死循环
func (a *Agent) autoRestartXray(ctx context.Context) error {
	now := time.Now()
	a.restartMu.Lock()
	defer a.restartMu.Unlock()

	// 清理 10 分钟窗口外的重启记录
	cutoff := now.Add(-10 * time.Minute)
	valid := a.restartHistory[:0]
	for _, t := range a.restartHistory {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	a.restartHistory = valid

	if len(a.restartHistory) >= 3 {
		return fmt.Errorf("Xray 10 分钟内已重启 %d 次, 暂停自动重启防止死循环(需人工检查配置)", len(a.restartHistory))
	}

	a.restartHistory = append(a.restartHistory, now)
	log.Printf("[health] 开始重启 Xray (窗口内第 %d 次)", len(a.restartHistory))

	// P0-AG6: 先尝试从面板拉新配置(可能配置已修复), 失败则 fallback 到本地缓存配置
	if err := a.reloadConfig(ctx); err != nil {
		log.Printf("[health] 拉取新配置失败, 用本地缓存重启: %v", err)
		if err := a.applyConfig(); err != nil {
			return fmt.Errorf("重启 Xray 失败: %w", err)
		}
	}
	log.Printf("[health] Xray 重启成功")
	return nil
}

// readCPUUsage 读取 CPU 使用率(简化版: 读 /proc/stat 两次取差)
// 修复 NODE-DATA-01: 原 doHeartbeat 硬编码 cpuUsage=0, 面板 CPU 永远显示 0
func readCPUUsage() float64 {
	// 读两次 /proc/stat 取差值, 间隔 100ms
	readStat := func() (idle, total uint64) {
		data, err := os.ReadFile("/proc/stat")
		if err != nil {
			return 0, 0
		}
		// 只解析第一行(aggregate)
		line := strings.SplitN(string(data), "\n", 2)[0]
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			return 0, 0
		}
		var sums [10]uint64
		for i := 1; i < len(fields) && i <= 10; i++ {
			v, _ := strconv.ParseUint(fields[i], 10, 64)
			sums[i-1] = v
		}
		idle = sums[3] + sums[4] // idle + iowait
		for _, v := range sums {
			total += v
		}
		return idle, total
	}
	idle1, total1 := readStat()
	time.Sleep(100 * time.Millisecond)
	idle2, total2 := readStat()
	if total2 <= total1 {
		return 0
	}
	totalDiff := total2 - total1
	idleDiff := idle2 - idle1
	if totalDiff == 0 {
		return 0
	}
	usage := float64(totalDiff-idleDiff) / float64(totalDiff) * 100
	if usage < 0 {
		return 0
	}
	if usage > 100 {
		return 100
	}
	return usage
}

// readOnlineConnections 读取 Xray 当前活跃连接数
//
// 主路径: ss 命令统计 ESTABLISHED 连接(需容器装 iproute2)
// 兜底:   ss 失败时(如容器未装 iproute2/命令不存在/语法问题)读 /proc/net/tcp[6],
//
//	不依赖任何外部命令, 纯 Go 读文件。这样即使节点未重新部署(旧镜像没装 iproute2),
//	连接数也能正常统计, 不至于恒为 0。
//
// 修复 NODE-DATA-01 (P0): 原 doHeartbeat 硬编码 onlineConns=0, 面板连接数永远显示 0。
// 之前尝试用 ss 修复但 ss 不存在导致恒 0; 现加 /proc/net/tcp 兜底确保至少有值。
func (a *Agent) readOnlineConnections(ctx context.Context) int32 {
	// P0-AG1: effectivePort 读取需持 a.mu.RLock, 防止与 applyConfig 的写并发
	a.mu.RLock()
	port := a.effectivePort
	a.mu.RUnlock()
	if port == 0 {
		return 0
	}
	// 主路径: ss 命令
	// -H 去表头; state established 只看已建立连接;
	// filter 用 sport/dport 匹配本节点 listen 端口(进/出方向都算)。
	// P1-AG15: execCommand 接受 ctx, 5s 超时, ctx 取消时立即返回
	out, err := execCommand(ctx, "ss", "-H", "-ant", "state", "established",
		fmt.Sprintf("( sport = :%d or dport = :%d )", port, port))
	if err == nil {
		var count int32
		for _, line := range strings.Split(out, "\n") {
			if strings.TrimSpace(line) != "" {
				count++
			}
		}
		return count
	}
	// 兜底: ss 失败, 读 /proc/net/tcp[6] 统计
	if shouldLogConnErr() {
		log.Printf("[conn][WARN] ss 命令失败, 回退到 /proc/net/tcp 统计: %v", err)
	}
	return readOnlineConnectionsFromProc(port)
}

// readOnlineConnectionsFromProc 直接读 /proc/net/tcp 和 /proc/net/tcp6,
// 统计 local_address 端口 == port 且状态为 ESTABLISHED(01) 的连接数。
//
// 作为 ss 命令失败时的兜底, 不依赖 iproute2, 纯 Go 读文件。
// /proc/net/tcp 格式:
//
//	sl  local_address rem_address   st tx_queue ...
//	 0: 0100007F:1F90 0100007F:1F90 01 ...
//
// local_address/rem_address 格式: IP:PORT(端口为 4 位十六进制大写)
// st 字段: 01=ESTABLISHED 0A=LISTEN 06=TIME_WAIT 等
//
// 对于代理服务器, 用户连进来的连接 local_address 是节点IP:监听端口,
// rem_address 是用户IP:用户端口, 统计 local_address 端口 == listen port 且 st==01 即可。
func readOnlineConnectionsFromProc(port int) int32 {
	if port <= 0 || port > 65535 {
		return 0
	}
	portHex := fmt.Sprintf("%04X", port) // /proc/net/tcp 端口固定 4 位十六进制大写
	var count int32
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			fields := strings.Fields(line)
			if len(fields) < 4 {
				continue
			}
			// 跳过表头(第一字段是 "sl")
			if fields[0] == "sl" {
				continue
			}
			localAddr := fields[1] // IP:PORT
			st := fields[3]        // state
			if st != "01" {        // 01 = ESTABLISHED
				continue
			}
			// local_address 格式 IP:PORT, 取最后一个冒号后的端口部分
			colonIdx := strings.LastIndex(localAddr, ":")
			if colonIdx < 0 {
				continue
			}
			if localAddr[colonIdx+1:] == portHex {
				count++
			}
		}
		f.Close()
	}
	return count
}

// connErrLimiter 限制 ss 失败日志频率, 避免每 30s 心跳刷一次
var connErrLimiter struct {
	sync.Mutex
	last time.Time
}

// shouldLogConnErr 同一错误 10 分钟内最多打一次日志
func shouldLogConnErr() bool {
	connErrLimiter.Lock()
	defer connErrLimiter.Unlock()
	now := time.Now()
	if now.Sub(connErrLimiter.last) < 10*time.Minute {
		return false
	}
	connErrLimiter.last = now
	return true
}

// execCommand 在 agent 进程内执行命令并返回输出
// P1-AG15: 接受 ctx 参数, 派生 5s 超时 ctx, 防止子进程卡死阻塞心跳/流量上报
// ctx 取消(如收到 SIGTERM)时, CommandContext 会发送 SIGKILL 给子进程
func execCommand(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	return string(out), err
}
