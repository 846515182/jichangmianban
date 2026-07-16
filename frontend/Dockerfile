# ===== 阶段1: 构建前端 =====
FROM node:20-alpine AS builder
WORKDIR /app

# 复制 package 文件并安装依赖（利用缓存层）
COPY package.json package-lock.json* ./
RUN npm config set registry https://registry.npmmirror.com/ && \
    npm ci --legacy-peer-deps

# 复制源码并构建
COPY . .
RUN npm run build

# ===== 阶段2: Nginx 运行 =====
FROM nginx:alpine AS runner

# 复制自定义 nginx 配置
COPY nginx.conf /etc/nginx/conf.d/default.conf

# 复制构建产物到 nginx 静态目录
COPY --from=builder /app/dist /usr/share/nginx/html

# 暴露端口
EXPOSE 80

# 健康检查
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://127.0.0.1/ || exit 1

CMD ["nginx", "-g", "daemon off;"]
