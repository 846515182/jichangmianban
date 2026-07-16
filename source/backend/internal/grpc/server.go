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
	listenAddr string
	server     *grpc.Server
	logger     *zap.Logger
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
	} else {
		logger.Info("gRPC 运行在明文模式(未配置 TLS 证书)")
	}

	s := grpc.NewServer(opts...)
	nexuspb.RegisterNodeServiceServer(s, nodeSvc)
	nexuspb.RegisterUserSyncServiceServer(s, userSyncSvc)
	nexuspb.RegisterTrafficServiceServer(s, trafficSvc)

	return &Server{
		listenAddr: listenAddr,
		server:     s,
		logger:     logger,
	}
}

func (s *Server) GetGrpcServer() *grpc.Server {
	return s.server
}

func (s *Server) Start(ctx context.Context) error {
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
