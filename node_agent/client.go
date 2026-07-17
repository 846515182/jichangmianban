package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"os"
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
}

func NewPanelClient(addr string) (*PanelClient, error) {
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
		creds = insecure.NewCredentials()
		log.Printf("[WARN] gRPC 明文模式(未配置 GRPC_TLS_CA) — 生产环境建议启用 TLS")
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

func (c *PanelClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func withTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

func (c *PanelClient) Register(nodeToken, hostname, version string) (*proto.RegisterResponse, error) {
	ctx, cancel := withTimeout()
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
	ctx, cancel := withTimeout()
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
	ctx, cancel := withTimeout()
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
	ctx, cancel := withTimeout()
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
	ctx, cancel := withTimeout()
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
