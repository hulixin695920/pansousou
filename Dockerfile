# 构建阶段
# 使用 --platform=$BUILDPLATFORM 确保构建器始终在运行 Actions 的机器的原生架构上运行 (通常是 linux/amd64)
# $BUILDPLATFORM 是 buildx 自动提供的变量
FROM --platform=$BUILDPLATFORM docker.1ms.run/library/golang:1.24-alpine AS builder

# 安装构建依赖
RUN apk add --no-cache git ca-certificates tzdata

# 设置工作目录
WORKDIR /app

# 复制依赖文件
COPY go.mod go.sum ./

# 设置 Go 代理（国内网络可加速依赖下载，海外环境可注释或改为 GOPROXY=https://proxy.golang.org,direct）
ENV GOPROXY=https://goproxy.cn,direct

# 下载依赖
RUN go mod download

# 复制源代码
COPY . .

# 构建参数
ARG VERSION=dev
ARG BUILD_DATE=unknown
ARG VCS_REF=unknown

# 这是 buildx 自动传入的目标平台架构参数，例如 amd64, arm64
ARG TARGETARCH

# 构建应用
# Go 语言原生支持交叉编译，这里会根据传入的 TARGETARCH 编译出对应平台的可执行文件
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -ldflags="-s -w -extldflags '-static'" -o pansou .

# 运行阶段
# 这一阶段会根据 buildx 的 --platform 参数选择正确的基础镜像 (例如 linux/arm64 会拉取 arm64/alpine)
FROM docker.1ms.run/library/alpine:3.19

# 添加运行时依赖
RUN apk add --no-cache ca-certificates tzdata

# 创建缓存目录
RUN mkdir -p /app/cache

# 从构建阶段复制可执行文件
# buildx 会智能地从对应平台的 builder 中复制正确的可执行文件
COPY --from=builder /app/pansou /app/pansou

# 设置工作目录
WORKDIR /app

# 暴露端口
EXPOSE 8888

# 仅保留容器运行必需的默认值，其余配置均从 .env 或运行时环境变量读取
# docker-compose 使用 env_file: .env 加载配置；docker run 请使用 --env-file .env
ENV CACHE_PATH=/app/cache \
    TZ=Asia/Shanghai

# 构建参数
ARG VERSION=dev
ARG BUILD_DATE=unknown
ARG VCS_REF=unknown

# 添加镜像标签
LABEL org.opencontainers.image.title="PanSou" \
      org.opencontainers.image.description="高性能网盘资源搜索API服务" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.url="https://github.com/fish2018/pansou" \
      org.opencontainers.image.source="https://github.com/fish2018/pansou" \
      maintainer="fish2018"

# 运行应用
CMD ["/app/pansou"]
