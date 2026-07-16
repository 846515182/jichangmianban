package grpc

import (
	"context"
	"fmt"
	"net"

	"nexus-panel/internal/app"
	"nexus-panel/internal/repo"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	nexuspb "nexus-panel/proto"
)

type Server struct {
	listenAddr        string
	server            *grpc.Server
	logger            *zap.Logger
	plaintextDenied   bool // 未配置 TLS 且未显式允许明文时为 true, Start 时拒绝启动
}

func NewServer(listenAddr string) *Server {
	container := app.Get()
	logger := container.Logger
	db := container.DB
	cfg := container.Cfg

	nodeRepo := repo.NewNodeRepo(db)
	userRepo := repo.NewUserRepo(db)
	trafficRepo := repo.NewTrafficRepo(db)

	nodeSvc := NewNodeServiceServer(nodeRepo, userRepo, logger)
	userSyncSvc := NewUserSyncServiceServer(nodeRepo, userRepo, logger)
	trafficSvc := NewTrafficServiceServer(trafficRepo, nodeRepo, userRepo, logger)

	var opts []grpc.ServerOption
	plaintextDenied := false

	if cfg.GRPCTLSEnabled() {
		tlsCfg, err := cfg.LoadGRPCTLSConfig()
		if err != nil {
			logger.Error("加载 gRPC TLS 配置失败，回退到明文模式", zap.Error(err))
		} else if tlsCfg != nil {
			opts = append(opts, grpc.Creds(credentials.NewTLS(tlsCfg)))
			logger.Info("gRPC TLS 已启用",
				zap.String("cert", cfg.GRPCTLSCert),
				zap.String("key", cfg.GRPCTLSKey),
			)
		}
	} else if cfg.GRPCAllowPlaintext {
		logger.Warn("gRPC 运行在明文模式(未配置 TLS 证书), 已通过 GRPC_ALLOW_PLAINTEXT=1 显式允许; 仅限开发/内网, 生产环境请配置 GRPC_TLS_CERT/GRPC_TLS_KEY")
	} else {
		// 安全: 未配置 TLS 且未显式允许明文, 拒绝启动 (node_token 等机密不应在公网明文传输)
		logger.Error("gRPC 未配置 TLS 证书且未设置 GRPC_ALLOW_PLAINTEXT=1, 拒绝以明文模式启动。请配置 GRPC_TLS_CERT/GRPC_TLS_KEY, 或在开发/内网环境显式设置 GRPC_ALLOW_PLAINTEXT=1")
		plaintextDenied = true
	}

	s := grpc.NewServer(opts...)
	nexuspb.RegisterNodeServiceServer(s, nodeSvc)
	nexuspb.RegisterUserSyncServiceServer(s, userSyncSvc)
	nexuspb.RegisterTrafficServiceServer(s, trafficSvc)

	return &Server{
		listenAddr:      listenAddr,
		server:          s,
		logger:          logger,
		plaintextDenied: plaintextDenied,
	}
}

func (s *Server) GetGrpcServer() *grpc.Server {
	return s.server
}

func (s *Server) Start(ctx context.Context) error {
	if s.plaintextDenied {
		return fmt.Errorf("gRPC 拒绝以明文模式启动: 未配置 TLS 证书且未设置 GRPC_ALLOW_PLAINTEXT=1")
	}
	lis, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return fmt.Errorf("gRPC 监听失败 %s: %w", s.listenAddr, err)
	}
	if s.logger != nil {
		s.logger.Info("gRPC 服务启动", zap.String("addr", s.listenAddr))
	}
	go func() {
		<-ctx.Done()
		s.server.GracefulStop()
	}()
	return s.server.Serve(lis)
}
