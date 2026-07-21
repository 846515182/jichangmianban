package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/keepalive"

	"nexus-agent/proto"
)

type PanelClient struct {
	conn       *grpc.ClientConn
	nodeSvc    proto.NodeServiceClient
	trafficSvc proto.TrafficServiceClient
	userSvc    proto.UserSyncServiceClient
	// P1-AG13: 主 ctx 引用, 用于派生 RPC ctx; mainCtx 被取消时所有 RPC 也会被取消
	mainCtx context.Context
}

// NewPanelClient 创建 gRPC 客户端。mainCtx 用于派生 RPC 超时 ctx, 传入主 ctx 后,
// 主 ctx 取消(SIGTERM)会级联取消所有进行中的 RPC, 加快优雅退出。
func NewPanelClient(addr string, mainCtx context.Context) (*PanelClient, error) {
	var creds credentials.TransportCredentials

	tlsCert := os.Getenv("GRPC_TLS_CA")
	if tlsCert != "" {
		caCert, err := os.ReadFile(tlsCert)
		if err != nil {
			// 修复 NODE-TLS-01: CA 读取失败应直接 fail-fast, 而非静默降级 InsecureSkipVerify
			// 静默降级会让"证书路径配错"被掩盖成"连接看起来正常但实际未校验", 是安全隐患
			return nil, fmt.Errorf("读取 CA 证书失败(路径 %s): %w — 请检查 GRPC_TLS_CA 配置", tlsCert, err)
		}
		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("CA 证书解析失败(路径 %s): 格式无效, 请检查文件内容", tlsCert)
		}
		// 修复 NODE-TLS-02: 校验 CA 证书有效期, 过期前 7 天告警
		// (面板侧证书过期会全节点静默掉线, 提前告警可让运维有窗口处理)
		if err := verifyCACertValidity(caPool); err != nil {
			log.Printf("[WARN] CA 证书校验告警: %v", err)
		}
		tlsCfg := &tls.Config{
			RootCAs:            caPool,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: false,
		}
		creds = credentials.NewTLS(tlsCfg)
		log.Printf("gRPC TLS 模式已启用(CA: %s)", tlsCert)
	} else {
		// 修复 NODE-TLS-03 (P0): 未配置 GRPC_TLS_CA 时的兜底。
		// 旧版直接用 insecure 明文连, 但面板若启用 TLS(如 Let's Encrypt 公网证书),
		// agent 发明文会被面板 TLS 端直接 EOF, 表现为
		// "error reading server preface: EOF", 所有 gRPC 调用永久失败, 节点显示离线
		// (但 Xray 仍在跑, 用户能用)。这正是本次节点离线的根因。
		// 兜底: 先 TCP 探测面板端口是否是 TLS 服务端, 若是则自动用系统 CA 池
		// (Alpine 镜像已装 ca-certificates, /etc/ssl/certs/ca-certificates.crt 含
		// ISRG Root X1, 可信 Let's Encrypt 等公信 CA); 否则保持明文。
		creds = autoDetectCredentials(addr)
	}

	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(creds),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  1 * time.Second,
				Multiplier: 1.6,
				Jitter:     0.2,
				MaxDelay:   30 * time.Second,
			},
			MinConnectTimeout: 5 * time.Second,
		}),
		// 修复 NODE-CONN-01: 添加 keepalive, 及时感知半开连接
		// 网络分区(TCP 连接未断但无流量)时, 无 keepalive 心跳会阻塞到 15s 超时
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
		// 修复 NODE-CONN-02: 添加 RPC 级 retry policy, 单次瞬时抖动自动重试
		// 修复 NODE-OFFLINE-01: 把 INTERNAL/UNKNOWN 也加入重试列表。
		// 面板重启(一键更新)期间, DB 短暂不可用会让 Heartbeat 返回 codes.Internal
		// ("查询节点失败"), 旧版只重试 UNAVAILABLE/DEADLINE_EXCEEDED, 导致 Internal
		// 直接失败, agent 等下一个 30s tick, 节点被面板误判离线。加入 INTERNAL 后,
		// gRPC 客户端会自动重试, 面板 DB 恢复后下一次 RPC 即可成功。
		grpc.WithDefaultServiceConfig(`{
			"methodConfig": [{
				"name": [{"service": "nexus.NodeService"}, {"service": "nexus.TrafficService"}],
				"retryPolicy": {
					"maxAttempts": 4,
					"initialBackoff": "1s",
					"maxBackoff": "10s",
					"backoffMultiplier": 2,
					"retryableStatusCodes": ["UNAVAILABLE", "DEADLINE_EXCEEDED", "INTERNAL", "UNKNOWN"]
				}
			}]
		}`),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 gRPC 连接失败: %w", err)
	}
	log.Printf("gRPC 客户端已创建(目标: %s)", addr)
	return &PanelClient{
		conn:       conn,
		nodeSvc:    proto.NewNodeServiceClient(conn),
		trafficSvc: proto.NewTrafficServiceClient(conn),
		userSvc:    proto.NewUserSyncServiceClient(conn),
		mainCtx:    mainCtx,
	}, nil
}

// verifyCACertValidity 校验 CA 证书池中所有证书的有效期, 过期/即将过期时返回告警
func verifyCACertValidity(pool *x509.CertPool) error {
	// x509.CertPool 不直接暴露证书列表, 通过 TryAddCert + 解析原始 PEM 的方式校验
	// 这里简化为: 重新解析 PEM 并检查 NotAfter
	// 注: 调用方已 AppendCertsFromPEM 成功, 这里再做一次解析只为读取有效期
	caCert := os.Getenv("GRPC_TLS_CA")
	if caCert == "" {
		return nil
	}
	pemData, err := os.ReadFile(caCert)
	if err != nil {
		return nil
	}
	var found bool
	for len(pemData) > 0 {
		var block *pem.Block
		block, pemData = pem.Decode(pemData)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		found = true
		timeUntilExpiry := time.Until(cert.NotAfter)
		if timeUntilExpiry <= 0 {
			return fmt.Errorf("CA 证书已过期(NotAfter: %s), 必须立即更新, 否则 TLS 握手会全部失败", cert.NotAfter.Format("2006-01-02 15:04:05"))
		}
		if timeUntilExpiry < 7*24*time.Hour {
			return fmt.Errorf("CA 证书将于 %s 过期(剩余 %s), 请尽快更新", cert.NotAfter.Format("2006-01-02"), timeUntilExpiry.Round(time.Hour))
		}
	}
	if !found {
		return fmt.Errorf("PEM 文件中未找到有效证书")
	}
	return nil
}

// autoDetectCredentials 未配置 GRPC_TLS_CA 时的兜底: 探测面板 gRPC 端口是否是 TLS 服务端。
// 修复 NODE-TLS-03 (P0): 旧版未配置 GRPC_TLS_CA 时直接用 insecure 明文, 但面板若启用
// TLS(如 Let's Encrypt 公网证书), agent 发明文会被面板 TLS 端直接 EOF, 表现为
// "error reading server preface: EOF", 所有 gRPC 调用永久失败, 节点显示离线
// (但 Xray 仍在跑, 用户能用)。这正是本次节点离线的根因。
//
// 兜底策略: 向面板端口发一个 TLS ClientHello, 若对面回复 TLS ServerHello(握手数据)
// 则判定为 TLS 服务端, 自动用系统 CA 池(/etc/ssl/certs/ca-certificates.crt, Alpine
// 镜像已装 ca-certificates, 含 ISRG Root X1 等公信根 CA, 可信 Let's Encrypt);
// 若对面无 TLS 响应(明文 gRPC 直接回 HTTP/2 preface)则保持明文模式。
// 这样无论面板是否启用 TLS, agent 都能正确连接, 不再因配置不匹配永久掉线。
func autoDetectCredentials(addr string) credentials.TransportCredentials {
	// addr 形如 "host:port", 拆出 host 和 port
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// 解析失败, 退回明文(保持旧行为, 不阻断启动)
		log.Printf("[WARN] gRPC 明文模式(未配置 GRPC_TLS_CA 且地址解析失败 %q): %v — 若面板启用 TLS 将连接失败", addr, err)
		return insecure.NewCredentials()
	}
	tlsDetected := probeTLSServer(host, port)
	if !tlsDetected {
		log.Printf("[WARN] gRPC 明文模式(未配置 GRPC_TLS_CA, 探测面板端口非 TLS) — 生产环境建议启用 TLS")
		return insecure.NewCredentials()
	}
	// 面板是 TLS 服务端, 自动用系统 CA 池(公信 CA 如 Let's Encrypt 可直接信任)
	// 若面板用自签证书, 系统池不信任会握手失败, 此时仍需显式配置 GRPC_TLS_CA
	systemPool, err := x509.SystemCertPool()
	if err != nil || systemPool == nil {
		// 系统 CA 池不可用(极端情况), 退回明文并告警, 不阻断启动
		log.Printf("[WARN] gRPC 面板为 TLS 但系统 CA 池不可用(%v), 退回明文 — 自签证书请显式配置 GRPC_TLS_CA", err)
		return insecure.NewCredentials()
	}
	// P1-AG17: 先用系统池 + ServerName=host 做严格 TLS 校验。
	// 若失败且错误含 "x509:"(典型: 证书 SAN 不匹配, 如面板用 IP 直连但证书 SAN 只有域名),
	// 回退到 InsecureSkipVerify 并强烈告警, 避免节点因证书 SAN 不匹配永久掉线。
	// 其他错误(网络瞬断等)仍用系统池让 gRPC 重连机制处理。
	strictCreds := credentials.NewTLS(&tls.Config{
		RootCAs:            systemPool,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: false,
		ServerName:         host,
	})
	if verifyErr := probeTLSHandshake(host, port, systemPool, host); verifyErr != nil {
		if strings.Contains(verifyErr.Error(), "x509:") {
			log.Printf("[WARN][SECURITY] TLS 证书 SAN 不匹配(host=%s), 回退到 InsecureSkipVerify(不安全! 建议配置 GRPC_TLS_CA 或修正证书 SAN): %v", host, verifyErr)
			return credentials.NewTLS(&tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: true,
			})
		}
		// 非 x509 错误(如网络瞬断), 仍用系统池让 gRPC 重连机制处理
		log.Printf("[INFO] gRPC 自动 TLS 模式(系统 CA 池), 测试握手错误(非 x509, 将由 gRPC 重连处理): %v", verifyErr)
	}
	log.Printf("[INFO] gRPC 自动 TLS 模式(未配置 GRPC_TLS_CA, 探测面板为 TLS, 使用系统 CA 池)")
	return strictCreds
}

// probeTLSHandshake 用指定 CA 池和 ServerName 做一次完整 TLS 握手测试。
// 用于 autoDetectCredentials 在严格校验模式下检测证书 SAN 是否匹配。
// 返回 nil 表示握手成功(证书校验通过), 返回 error 表示握手/校验失败。
func probeTLSHandshake(host, port string, pool *x509.CertPool, serverName string) error {
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.Dial("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	tlsConn := tls.Client(conn, &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
		ServerName: serverName,
	})
	return tlsConn.Handshake()
}

// probeTLSServer 向 host:port 发起 TLS ClientHello, 判断对面是否是 TLS 服务端。
// 返回 true 表示对面回复了 TLS 握手数据(是 TLS 服务端), false 表示明文或不可达。
func probeTLSServer(host, port string) bool {
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.Dial("tcp", net.JoinHostPort(host, port))
	if err != nil {
		// 端口不可达, 无法探测, 保守返回 false(用明文, 让 gRPC 重连机制处理)
		log.Printf("[WARN] TLS 探测: 连接 %s:%s 失败(%v), 假定明文", host, port, err)
		return false
	}
	defer conn.Close()
	// 设 3s 读写超时, 避免阻塞
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	// 发 TLS 1.2 ClientHello(最小化, 不做完整握手)
	tlsConn := tls.Client(conn, &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // 探测阶段不校验, 只看是否是 TLS 协议
		ServerName:         host,
	})
	// 尝试握手: 若对面是 TLS 服务端会完成握手(或返回 TLS alert); 若是明文 gRPC,
	// 对面会回 HTTP/2 preface 或直接关闭, tls.Handshake 会报 "first record not TLS handshake"
	// 修复 NODE-TLS-PANIC (P0): if err := Handshake() 中用 := 声明了新局部 err,
	// 遮蔽了外层 dial 的 nil err。出 if 块后 err 回到外层 nil → err.Error() panic。
	// 改用 = 复用外层 err, 同时判 nil 兜底(conn 被提前关闭等极端场景)
	err = tlsConn.Handshake()
	if err == nil {
		return true // 握手成功, 确定是 TLS
	}
	// 握手失败, 但错误类型能区分: TLS alert(对面是 TLS 但证书有问题) vs 非 TLS
	if err == nil {
		log.Printf("[WARN] TLS 探测: %s:%s Handshake 返回 nil error 但握手失败(极端情况), 假定明文", host, port)
		return false
	}
	errStr := err.Error()
	if strings.Contains(errStr, "tls:") || strings.Contains(errStr, "handshake") ||
		strings.Contains(errStr, "certificate") || strings.Contains(errStr, "alert") {
		// TLS 协议层错误(如证书校验失败/alert), 说明对面确实是 TLS 服务端
		log.Printf("[INFO] TLS 探测: %s:%s 是 TLS 服务端(握手报错: %v), 将用系统 CA 池", host, port, err)
		return true
	}
	// "first record not TLS handshake" / EOF / connection reset 等 → 明文服务端
	log.Printf("[INFO] TLS 探测: %s:%s 非 TLS(%v), 用明文", host, port, err)
	return false
}

func (c *PanelClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// withTimeout 派生 15s 超时 ctx 用于 RPC 调用
// P1-AG13: 改为 PanelClient 方法, 基于 c.mainCtx 派生; mainCtx 被取消时 RPC 也会被取消
// 若 c.mainCtx 为 nil(构造后未设置), 回退到 context.Background() 保持向后兼容
func (c *PanelClient) withTimeout() (context.Context, context.CancelFunc) {
	parent := c.mainCtx
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, 15*time.Second)
}

func (c *PanelClient) Register(nodeToken, hostname, version string) (*proto.RegisterResponse, error) {
	ctx, cancel := c.withTimeout()
	defer cancel()
	req := &proto.RegisterRequest{
		NodeToken: nodeToken,
		Hostname:  hostname,
		Version:   version,
	}
	resp, err := c.nodeSvc.Register(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("Register RPC 失败: %w", err)
	}
	return resp, nil
}

func (c *PanelClient) GetConfig(nodeID, nodeToken, configVersion string) (*proto.NodeConfigResponse, error) {
	ctx, cancel := c.withTimeout()
	defer cancel()
	req := &proto.GetConfigRequest{
		NodeId:        nodeID,
		NodeToken:     nodeToken,
		ConfigVersion: configVersion,
	}
	resp, err := c.nodeSvc.GetConfig(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("GetConfig RPC 失败: %w", err)
	}
	return resp, nil
}

func (c *PanelClient) Heartbeat(nodeID, nodeToken, version string, cpuUsage, memUsage float64, memTotal int64, onlineConns int32, uptime float64, trafficLimit, trafficUsed int64, proxyReachable bool, proxyLatencyMs int64, proxyError string) (*proto.HeartbeatResponse, error) {
	ctx, cancel := c.withTimeout()
	defer cancel()
	req := &proto.HeartbeatRequest{
		NodeId:            nodeID,
		NodeToken:         nodeToken,
		Version:           version,
		CpuUsage:          cpuUsage,
		MemoryUsage:       memUsage,
		MemoryTotal:       memTotal,
		OnlineConnections: onlineConns,
		UptimeSeconds:     uptime,
		TrafficLimit:      trafficLimit,
		TrafficUsed:       trafficUsed,
		// 修复 NODE-HEALTH-01: 上报代理自检结果, 让面板区分 agent 进程可达 vs 代理服务可用
		ProxyReachable: proxyReachable,
		ProxyLatencyMs: proxyLatencyMs,
		ProxyError:     proxyError,
	}
	resp, err := c.nodeSvc.Heartbeat(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("Heartbeat RPC 失败: %w", err)
	}
	return resp, nil
}

func (c *PanelClient) ReportRealtime(nodeID, nodeToken string, records []*proto.TrafficRecord) (*proto.ReportRealtimeResponse, error) {
	ctx, cancel := c.withTimeout()
	defer cancel()
	req := &proto.ReportRealtimeRequest{
		NodeId:       nodeID,
		NodeToken:    nodeToken,
		Records:      records,
	}
	resp, err := c.trafficSvc.ReportRealtime(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ReportRealtime RPC 失败: %w", err)
	}
	return resp, nil
}

func (c *PanelClient) SyncUsers(nodeID, nodeToken string, sinceVersion int64) (*proto.SyncUsersResponse, error) {
	ctx, cancel := c.withTimeout()
	defer cancel()
	req := &proto.SyncUsersRequest{
		NodeId:       nodeID,
		NodeToken:    nodeToken,
		SinceVersion: sinceVersion,
	}
	resp, err := c.userSvc.SyncUsers(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("SyncUsers RPC 失败: %w", err)
	}
	return resp, nil
}
