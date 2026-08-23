# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.23-bookworm AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY yyb_go/go.mod yyb_go/go.sum ./yyb_go/
WORKDIR /src/yyb_go
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY yyb_go/ ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/yyb-go ./cmd/yyb-go

FROM alpine:3.21

RUN apk add --no-cache ca-certificates \
    && addgroup -S yyb \
    && adduser -S -G yyb yyb \
    && mkdir -p /app/resource /app/data \
    && chown -R yyb:yyb /app

WORKDIR /app
COPY --from=builder /out/yyb-go /app/yyb-go
COPY yyb_go/resource/ /app/resource/

RUN chown -R yyb:yyb /app

ENV GIN_MODE=release

USER yyb
EXPOSE 8000

# 只把运行时数据目录 /app/data 声明为卷。/app/resource 存放 static / templates，
# 必须留在镜像层，否则挂载会盖住镜像里的前端资源，导致更新镜像后页面仍是旧版本。
VOLUME ["/app/data"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8000/health || exit 1

ENTRYPOINT ["/app/yyb-go"]
CMD ["-host", "0.0.0.0", "-port", "8000", "-resource-root", "/app/resource", "-data-root", "/app/data"]
