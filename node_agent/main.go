package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
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

	startTime time.Time

	// 注册后从面板拿到的节点信息
	mu          sync.RWMutex
	nodeID      string
	nodePort    int
	configVer   string
	xrayCfgJSON string

	// fatalShutdown=1 表示因致命错误(节点被删/token失效)已停止 Xray 代理服务
	// 进入停服状态后不再发心跳，进程不退出(避免 docker unless-stopped 重启死循环)
	fatalShutdown int32
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

	agent := &Agent{
		cfg:       cfg,
		startTime: time.Now(),
		traffic:   NewTrafficCounter(),
	}

	// 1. 启动健康检查 HTTP 服务
	safeGo(agent.runHealthServer)

	// 2. 建立 gRPC 连接(非阻塞，底层自动重连)
	client, err := NewPanelClient(cfg.PanelGrpcAddr)
	if err != nil {
		log.Fatalf("建立 gRPC 连接失败: %v", err)
	}
	agent.client = client
	defer client.Close()

	// 3. 下载 Xray-core 二进制(若不存在)
	xm, err := NewXrayManager(cfg.XrayVersion)
	if err != nil {
		log.Fatalf("初始化 Xray 管理器失败: %v", err)
	}
	agent.xray = xm
	if err := xm.EnsureBinary(); err != nil {
		log.Printf("警告: 准备 Xray 二进制失败(将继续尝试): %v", err)
	}

	// 4. 注册节点 + 拉取配置 + 启动 Xray(带重试，直到成功)
	if err := agent.bootstrap(); err != nil {
		log.Fatalf("启动失败: %v", err)
	}

	// 5. 启动心跳定时器(每 30s)
	ctx, cancel := context.WithCancel(context.Background())
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

	// 7. 监听信号优雅退出
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("收到信号 %v，开始关闭...", sig)

	cancel()
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
		ListenPort:    getenvInt("LISTEN_PORT", 443),
		HealthPort:    getenvInt("HEALTH_PORT", 50052),
	}
	if cfg.PanelGrpcAddr == "" {
		return nil, fmt.Errorf("环境变量 PANEL_GRPC_ADDR 必填")
	}
	// [P1-3 2026-07-17] 校验 host:port 格式, 避免 grpc.Dial 时报"passthrough: received empty
	// address"等不友好错误。若用户填了 DNS 域名也会被接受(net.SplitHostPort 支持域名)
	if _, _, err := net.SplitHostPort(cfg.PanelGrpcAddr); err != nil {
		return nil, fmt.Errorf("PANEL_GRPC_ADDR 格式错误, 应为 host:port 如 panel.example.com:50051: %w", err)
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
	addr := fmt.Sprintf(":%d", a.cfg.HealthPort)
	log.Printf("健康检查服务监听 %s (/healthz=完整检查, /livez=存活检查)", addr)
	srv := &http.Server{Addr: addr, Handler: mux}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("健康检查服务退出: %v", err)
	}
}

// bootstrap 注册节点 + 拉取配置 + 启动 Xray，带重试直到成功
func (a *Agent) bootstrap() error {
	const maxAttempts = 30
	// 注册
	var nodeInfo *proto.NodeInfo
	for i := 1; i <= maxAttempts; i++ {
		ni, err := a.client.Register(a.cfg.NodeToken, hostname(), agentVersion)
		if err != nil {
			log.Printf("注册失败(%d/%d): %v", i, maxAttempts, err)
			time.Sleep(5 * time.Second)
			continue
		}
		if ni.GetResp().GetCode() != 0 {
			log.Printf("注册被拒(%d/%d): code=%d msg=%s", i, maxAttempts, ni.GetResp().GetCode(), ni.GetResp().GetMessage())
			time.Sleep(5 * time.Second)
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
		cfgResp, err := a.client.GetConfig(a.getNodeID(), a.cfg.NodeToken, "")
		if err != nil {
			log.Printf("拉取配置失败(%d/%d): %v", i, maxAttempts, err)
			time.Sleep(5 * time.Second)
			continue
		}
		if cfgResp.GetResp().GetCode() != 0 {
			log.Printf("拉取配置被拒(%d/%d): code=%d msg=%s", i, maxAttempts, cfgResp.GetResp().GetCode(), cfgResp.GetResp().GetMessage())
			time.Sleep(5 * time.Second)
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

// applyConfig 写入 Xray 配置文件并(重启)启动 Xray 进程
func (a *Agent) applyConfig() error {
	a.mu.RLock()
	cfgJSON := a.xrayCfgJSON
	nodePort := a.nodePort
	a.mu.RUnlock()

	// 优先使用用户显式设置的 LISTEN_PORT，否则使用面板返回的节点端口
	// 这样可以确保订阅中的端口与 Xray 实际监听的端口一致
	listenPort := a.cfg.ListenPort
	if listenPort == 0 && nodePort > 0 {
		listenPort = nodePort
	}

	finalCfg, err := OverrideListenPort(cfgJSON, listenPort)
	if err != nil {
		return fmt.Errorf("覆盖监听端口失败: %w", err)
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
func (a *Agent) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.doHeartbeat()
		}
	}
}

func (a *Agent) doHeartbeat() {
	// 已因致命错误停服，跳过心跳(进程保持运行，避免 docker 重启死循环)
	if atomic.LoadInt32(&a.fatalShutdown) == 1 {
		return
	}
	uptime := time.Since(a.startTime).Seconds()
	memTotal, memUsed := readMemInfo()
	memUsage := 0.0
	if memTotal > 0 {
		memUsage = float64(memUsed) / float64(memTotal) * 100
	}

	// REALITY 自连健康检查(每次心跳时执行，约 30s 一次)
	health := a.CheckProxyHealth()
	if health.ProxyReachable {
		log.Printf("[health] 代理自连正常 latency=%dms", health.ProxyLatencyMs)
	} else {
		log.Printf("[health][ERROR] 代理自连失败: %s (latency=%dms)", health.ProxyError, health.ProxyLatencyMs)
	}

	resp, err := a.client.Heartbeat(a.getNodeID(), a.cfg.NodeToken, agentVersion, 0, memUsage, memTotal, 0, uptime, 0, 0)
	if err != nil {
		// 致命错误: 节点已被面板删除/token 失效 → 停止 Xray 代理服务
		if isFatalHeartbeatError(err) {
			a.handleFatalShutdown(fmt.Sprintf("心跳失败: %v (节点可能已被面板删除或 token 失效)", err))
			return
		}
		log.Printf("心跳失败: %v", err)
		return
	}
	if resp.GetResp().GetCode() != 0 {
		msg := resp.GetResp().GetMessage()
		if isFatalHeartbeatMsg(msg) {
			a.handleFatalShutdown(fmt.Sprintf("心跳被拒: %s", msg))
			return
		}
		log.Printf("心跳被拒: code=%d msg=%s", resp.GetResp().GetCode(), msg)
		return
	}

	// 配置或用户变更 -> 重新拉取配置并应用
	if resp.GetConfigChanged() || resp.GetUsersChanged() {
		log.Printf("面板提示配置/用户已变更，重新拉取配置...")
		a.reloadConfig()
	}
}

// handleFatalShutdown 致命错误停服: 停止 Xray 并标记停服状态
// 不退出进程(docker restart=unless-stopped 会重启导致死循环)，保持容器运行但 Xray 已停
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
	log.Printf("[FATAL] 节点已停服，等待运维处理 (docker stop nexus-node-agent 或在面板重新部署该节点)")
}

// isFatalHeartbeatError 判断 gRPC 错误是否为致命错误(节点被删/token失效/被禁用)
// 这类错误不可恢复，继续运行 Xray 会导致"已删节点仍代理流量"的安全漏洞
// [P1-1 2026-07-17] 改用 gRPC status.Code() 判定, 不再依赖错误消息字符串
// 后端 grpc 服务已统一返回 codes.NotFound / codes.Unauthenticated / codes.PermissionDenied
func isFatalHeartbeatError(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		// 非 gRPC 状态错误(网络层/超时等), 视为可重试, 不停服
		return false
	}
	switch st.Code() {
	case codes.NotFound, codes.Unauthenticated, codes.PermissionDenied:
		return true
	default:
		return false
	}
}

// isFatalHeartbeatMsg 判断心跳被拒消息是否为致命错误
func isFatalHeartbeatMsg(msg string) bool {
	return strings.Contains(msg, "节点不存在") ||
		strings.Contains(msg, "节点已禁用") ||
		strings.Contains(msg, "token")
}

// reloadConfig 重新拉取 Xray 配置并重启进程
func (a *Agent) reloadConfig() {
	cfgResp, err := a.client.GetConfig(a.getNodeID(), a.cfg.NodeToken, a.getConfigVer())
	if err != nil {
		log.Printf("重新拉取配置失败: %v", err)
		return
	}
	if cfgResp.GetResp().GetCode() != 0 {
		log.Printf("重新拉取配置被拒: code=%d msg=%s", cfgResp.GetResp().GetCode(), cfgResp.GetResp().GetMessage())
		return
	}
	a.mu.Lock()
	a.configVer = cfgResp.GetConfigVersion()
	a.xrayCfgJSON = cfgResp.GetXrayConfig()
	a.mu.Unlock()
	if err := a.applyConfig(); err != nil {
		log.Printf("应用新配置失败: %v", err)
	}
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
	upload, download := a.traffic.Delta()
	if upload == 0 && download == 0 {
		return // 无流量变化不上报
	}
	nodeID := a.getNodeID()
	now := time.Now().Unix()
	records := []*proto.TrafficRecord{
		{
			NodeId:        nodeID,
			UserId:        "00000000-0000-0000-0000-000000000000",
			UploadBytes:   upload,
			DownloadBytes: download,
			LogTime:       now,
		},
	}
	resp, err := a.client.ReportRealtime(nodeID, a.cfg.NodeToken, records)
	if err != nil {
		log.Printf("流量上报失败: %v", err)
		return
	}
	if resp.GetResp().GetCode() != 0 {
		log.Printf("流量上报被拒: code=%d msg=%s", resp.GetResp().GetCode(), resp.GetResp().GetMessage())
	}
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
