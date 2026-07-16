package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/backoff"

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
			log.Printf("警告: 读取 CA 证书失败: %v, 使用不验证模式", err)
			tlsCfg := &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: true,
			}
			creds = credentials.NewTLS(tlsCfg)
		} else {
			caPool := x509.NewCertPool()
			caPool.AppendCertsFromPEM(caCert)
			tlsCfg := &tls.Config{
				RootCAs:            caPool,
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: false,
			}
			creds = credentials.NewTLS(tlsCfg)
		}
		log.Printf("gRPC TLS 模式已启用")
	} else {
		creds = insecure.NewCredentials()
		log.Printf("gRPC 明文模式(未配置 GRPC_TLS_CA)")
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

func (c *PanelClient) Heartbeat(nodeID, nodeToken, version string, cpuUsage, memUsage float64, memTotal int64, onlineConns int32, uptime float64, trafficLimit, trafficUsed int64) (*proto.HeartbeatResponse, error) {
	ctx, cancel := withTimeout()
	defer cancel()
	req := &proto.HeartbeatRequest{
		NodeId:            nodeID,
		NodeToken:          nodeToken,
		Version:           version,
		CpuUsage:          cpuUsage,
		MemoryUsage:       memUsage,
		MemoryTotal:       memTotal,
		OnlineConnections: onlineConns,
		UptimeSeconds:     uptime,
		TrafficLimit:      trafficLimit,
		TrafficUsed:       trafficUsed,
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
