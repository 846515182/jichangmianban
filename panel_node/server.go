package grpc

import (
	"context"
	"fmt"
	"net"
	"time"

	"nexus-panel/internal/app"
	"nexus-panel/internal/repo"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
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

	// P1-gRPC限制: 限制单条消息 1MB + keepalive + panic recover interceptor
	// 避免恶意/异常 agent 上报超大消息打爆内存, keepalive 防止僵尸连接占资源,
	// interceptor 保证单次 RPC panic 不会拖垮整个 gRPC server。
	opts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(1 << 20), // 1MB
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    60 * time.Second,
			Timeout: 20 * time.Second,
		}),
		grpc.UnaryInterceptor(grpcUnaryInterceptor),
	}

	if cfg.GRPCTLSEnabled() {
		tlsCfg, err := cfg.LoadGRPCTLSConfig()
		if err != nil {
			// P0-N4: TLS 配置加载失败必须 fail-closed (Fatal 退出),
			// 不能静默降级到明文模式。否则配置错误(如证书路径写错)时 gRPC 会以明文启动,
			// 节点 token / 用户凭证全部明文传输, 中间人可窃听全部节点通信。
			logger.Fatal("gRPC TLS 配置加载失败, 拒绝以明文模式启动",
				zap.Error(err))
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

// grpcUnaryInterceptor gRPC 一元拦截器: 捕获 handler panic, 防止单次 RPC panic
// 拖垮整个 gRPC server。P1-gRPC限制 配套。
func grpcUnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
	defer func() {
		if r := recover(); r != nil {
			if l := app.Get().Logger; l != nil {
				l.Error("gRPC panic",
					zap.String("method", info.FullMethod),
					zap.Any("panic", r))
			}
			err = status.Error(codes.Internal, "internal error")
		}
	}()
	return handler(ctx, req)
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
