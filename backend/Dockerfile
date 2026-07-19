# ============================================================
# Nexus-Panel 后端 Dockerfile（多阶段构建）
# 第一阶段: golang:1.21-alpine 编译
# 第二阶段: alpine 运行
# 暴露端口: 8080(HTTP API) + 50051(gRPC)
# ============================================================

# ---------- 编译阶段 ----------
FROM golang:1.21-bullseye AS builder

# 安装构建所需的工具（git 用于拉取依赖，ca-certificates 用于 HTTPS）
RUN sed -i 's/deb.debian.org/mirrors.tuna.tsinghua.edu.cn/g' /etc/apt/sources.list && \
    apt-get update && apt-get install -y --no-install-recommends git ca-certificates && \
    update-ca-certificates

# 设置工作目录
WORKDIR /build

# 先复制依赖清单，利用 Docker 层缓存加速构建
COPY go.mod go.sum ./
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download

# 复制源码
COPY . .

# 通过 build arg 注入版本号(默认 dev)
# 修复 CRITICAL 2026-07-19: 旧版用 `git rev-parse --short HEAD` 在 build 阶段取版本,
# 但 build context 是 ./backend, 不含 .git 目录, 命令必然失败 fallback 到 dev,
# 导致 app.Version 永远是 "dev", CheckVersionConsistency 兜底 cron 直接跳过失效。
# 现改为由调用方(gitPull 流程 / 部署脚本)通过 --build-arg VERSION=<git HEAD short> 显式传入。
ARG VERSION=dev
ENV GOPROXY=https://goproxy.cn,direct
# 双重注入: main.Version 和 app.Version 都注入,
# 防止 main.go 的 app.Version = Version 赋值因任何原因失效
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -X 'main.Version=${VERSION}' -X 'nexus-panel/internal/app.Version=${VERSION}'" \
    -o nexus-panel \
    ./cmd/server

# ---------- 运行阶段 ----------
FROM alpine:3.18

# 安装运行时所需的最小依赖：ca-certificates(HTTPS)、tzdata(时区)、wget(健康检查)
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.tuna.tsinghua.edu.cn/g' /etc/apk/repositories && \
    apk add --no-cache ca-certificates tzdata wget git docker-cli docker-cli-compose && \
    update-ca-certificates

# 设置时区为 UTC
ENV TZ=UTC \
    LANG=C.UTF-8

# 创建非 root 用户运行服务，提升安全性
RUN addgroup -S nexus && adduser -S -G nexus nexus

# 创建数据与配置目录
RUN mkdir -p /app/data /app/config /app/tls && \
    chown -R nexus:nexus /app

WORKDIR /app

# 从编译阶段拷贝二进制
COPY --from=builder /build/nexus-panel /app/nexus-panel

# 拷贝迁移脚本（容器启动时由 entrypoint 加载执行）
COPY --from=builder /build/migrations /app/migrations

# 切换非 root 用户
USER nexus

# 暴露 HTTP API(8080) 与 gRPC(50051) 端口
EXPOSE 8080 50051

# 健康检查：调用 HTTP 健康端点
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

# 启动服务
ENTRYPOINT ["/app/nexus-panel"]
