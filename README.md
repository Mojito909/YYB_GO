# YYB Go

基于 Go 的第三方平台接入服务端，用于扫码账号接入、账号管理和目标应用能力调用。

## 项目来源

本项目基于原作者项目进行二次开发：

- 原作者项目：[SuperNaiBA/YYB_GO](https://github.com/SuperNaiBA/YYB_GO)
- 当前项目：在原项目基础上进行功能扩展、架构整理和管理端优化

感谢原作者对相关协议和应用能力调用的探索与实现。

## 二次开发重点

- 增加管理员登录、Cookie 会话和密码管理
- 增加扫码登录、账号保存与账号状态维护
- 增加账号管理和目标应用能力 HTTP API
- 增加 SQLite 数据持久化与会话缓存
- 增加 OpenAPI / Swagger 接口文档
- 优化管理后台、登录页和扫码页的 UI 体验
- 增加亮色 / 暗色模式、响应式布局和基础交互反馈
- 补充青龙等任务平台的 HTTP 调用支持说明

## 快速启动

项目 Go 模块位于 `yyb_go/` 目录：

```bash
cd yyb_go
export YYB_ADMIN_USER=admin
export YYB_ADMIN_PASSWORD='change-me-please'
go run ./cmd/yyb-go -host 127.0.0.1 -port 8000
```

启动后访问：

- 控制台：http://127.0.0.1:8000/
- 登录页：http://127.0.0.1:8000/login
- 扫码页：http://127.0.0.1:8000/scan
- 接口文档：http://127.0.0.1:8000/docs/index.html

如果未设置 `YYB_ADMIN_PASSWORD`，首次启动会生成随机密码并输出到日志。

## Docker

仓库已配置 GitHub Actions：

- Pull Request：只构建镜像，不推送
- `main` 分支：构建并推送 `latest` 和提交标签
- `v*` 标签：构建并推送版本标签
- 构建平台：`linux/amd64`、`linux/arm64`

工作流使用仓库 Secrets 中的 `DOCKER_USERNAME` 和 `DOCKER_PASSWORD`，镜像名称为：

```text
docker.io/DOCKER_USERNAME/yyb-go
```

本地运行：

```bash
docker build -t yyb-go:local .
docker volume create yyb-go-data
docker run -d \
  --name yyb-go \
  -p 8000:8000 \
  -e YYB_ADMIN_USER=admin \
  -e YYB_ADMIN_PASSWORD='change-me-please' \
  -v yyb-go-data:/app/resource \
  yyb-go:local
```

`/app/resource` 用于持久化数据库、账号凭证、会话和页面运行数据。

## 项目结构

```text
yyb_go/
├── cmd/yyb-go/        # 服务入口
├── internal/httpapi/  # HTTP 路由、鉴权和业务接口
├── internal/protocol/ # 协议、会话和网络传输
├── internal/qr/       # 扫码登录流程
├── internal/store/    # SQLite 数据存储
└── resource/          # 页面资源和运行时数据
```

## 开发检查

```bash
cd yyb_go
gofmt -w ./cmd ./internal
go test ./...
go build ./cmd/yyb-go
```

## 更新记录

详细变更请查看 [CHANGELOG.md](CHANGELOG.md)。后续功能统一先记录在 `[Unreleased]` 区域，再按版本整理。

## 使用说明

本项目为二次开发项目。使用、分发或继续开发前，请阅读原作者项目及本项目中的相关声明，并妥善保护运行目录中的账号凭证、会话数据和数据库文件。
