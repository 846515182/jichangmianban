# ============================================================
# nexus node-agent 极简镜像
# 部署在代理节点服务器上，连接面板拉取配置并运行 Xray-core
#
# 优化: 面板服务器预编译二进制，节点只需 COPY(几秒构建，1GB 内存可跑)
# 面板编译命令(在 auto_deploy.go 中自动执行):
#   docker run --rm -v /root/nexus-panel/node_agent:/build -w /build golang:1.21-bullseye \
#     go mod download && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o agent .
# ============================================================

FROM alpine:3.18
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.tuna.tsinghua.edu.cn/g' /etc/apk/repositories && \
    apk add --no-cache ca-certificates wget tzdata
# 预编译好的二进制由面板通过 docker build 上下文提供
COPY agent /app/agent
RUN chmod +x /app/agent
WORKDIR /app
RUN mkdir -p /app/xray /app/config
# Xray 二进制在容器启动时由 agent 自动下载(按 XRAY_VERSION)
EXPOSE 443 50052
CMD ["/app/agent"]
