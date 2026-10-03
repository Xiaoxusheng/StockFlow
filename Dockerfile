# StockFlow 后端镜像（多阶段构建，静态编译，非 root 运行）
# 构建：docker compose build app（或 docker build -t stockflow-server .）
FROM golang:1.27-alpine AS build
WORKDIR /src
# CGO 关闭 = 纯静态二进制（pgx 纯 Go）；GOPROXY 国内代理，海外部署可删
ENV CGO_ENABLED=0 GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY db ./db
RUN go build -trimpath -ldflags="-s -w" -o /out/stockflow-server ./cmd/server

FROM alpine:3.20
# ca-certificates：出网 HTTPS（如需）；tzdata+TZ：日志与时间戳用本地时区；wget：容器健康检查
RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S app && adduser -S app -G app
ENV TZ=Asia/Shanghai
WORKDIR /app
COPY --from=build /out/stockflow-server /app/stockflow-server
# 运行期兜底迁移用（SF_DATABASE_AUTO_MIGRATE=true 时按 file://db/migrations 相对路径读取）
COPY --from=build /src/db/migrations /app/db/migrations
USER app
EXPOSE 8080
ENTRYPOINT ["/app/stockflow-server"]
