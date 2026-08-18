# 单镜像打包三个服务：materials-server（:8080）、api-server（:8090）、
# Next.js web 前端（:28082，唯一对外暴露的端口）。三者在同一个容器内
# 通过 loopback 通信，浏览器只需要访问容器的 28082 端口。

# ---- Stage 1: 编译 Go 二进制（api-server / materials-server） ----
FROM golang:1.26-alpine AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/api-server ./cmd/api-server
COPY cmd/materials-server ./cmd/materials-server
COPY internal ./internal
# sqlite 驱动是 modernc.org/sqlite（纯 Go 实现），无需 CGO/gcc。
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/api-server ./cmd/api-server
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/materials-server ./cmd/materials-server

# ---- Stage 2: 构建 Next.js 前端（standalone 产物） ----
FROM node:20-alpine AS web-builder
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --legacy-peer-deps
COPY web ./
# 空字符串 = 浏览器请求同源相对路径 /api/*，由 next.config.js 的
# rewrites 转发到容器内 api-server，构建期写死进客户端 JS 包。
ENV NEXT_PUBLIC_API_BASE_URL=""
RUN npm run build

# ---- Stage 3: 运行时镜像 ----
FROM node:20-alpine
RUN apk add --no-cache bash
WORKDIR /app

COPY --from=go-builder /out/api-server /app/bin/api-server
COPY --from=go-builder /out/materials-server /app/bin/materials-server
COPY suites ./suites

COPY --from=web-builder /src/web/.next/standalone ./web
COPY --from=web-builder /src/web/.next/static ./web/.next/static
COPY --from=web-builder /src/web/public ./web/public

COPY docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh && mkdir -p /data/reports

ENV WEB_PORT=28082 \
    API_PORT=8090 \
    MATERIALS_PORT=8080 \
    DB_PATH=/data/testbed.db \
    REPORTS_ROOT=/data/reports \
    SUITES_ROOT=/app/suites \
    AUTH_TOKEN=""

EXPOSE 28082
VOLUME ["/data"]

ENTRYPOINT ["/app/docker-entrypoint.sh"]
