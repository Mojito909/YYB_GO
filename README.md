# YYB Go

基于 Go 的第三方平台接入服务端，用于扫码账号接入、账号管理和目标应用能力调用。

## 项目来源

本项目基于原作者项目进行二次开发：

- 原作者项目：[SuperNaiBA/YYB\_GO](https://github.com/SuperNaiBA/YYB_GO)
- 当前项目：在原项目基础上进行功能扩展、架构整理和管理端优化

感谢原作者对相关协议和应用能力调用的探索与实现。

## 二次开发重点

- 增加管理员登录、Cookie 会话和密码管理
- 增加扫码登录、账号保存与账号状态维护
- 增加账号管理和目标应用能力 HTTP API
- 增加 SQLite 数据持久化与会话缓存
- 增加 OpenAPI / Swagger 接口文档
- 增加 API Bearer Token，方便青龙等脚本调用
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

- 控制台：<http://127.0.0.1:8000/>
- 登录页：<http://127.0.0.1:8000/login>
- 扫码页：<http://127.0.0.1:8000/scan>
- 接口文档：<http://127.0.0.1:8000/docs/index.html>

如果未设置 `YYB_ADMIN_PASSWORD`，首次启动会生成随机密码并输出到日志。

登录后台后，进入「账号管理」，在账号卡片点「令牌」或在详情抽屉点「API 令牌」为该账号生成令牌。令牌与账号一对一绑定，只能操作绑定的那个账号。

也可以直接调用接口生成（需要先用管理员会话登录，`ref` 支持账号 id / uin / openid；如果纯数字同时命中 id 和 uin，接口会返回 400，此时请改用 `id:1`、`uin:123` 或 `openid:xxx` 前缀）：

```bash
curl -b cookie.txt -H "Content-Type: application/json" \
  -d '{"ref":"YOUR_ACCOUNT_OPENID"}' \
  http://127.0.0.1:8000/auth/token
```

青龙调用时使用：

```bash
curl -H "Authorization: Bearer YOUR_API_TOKEN" \
  http://127.0.0.1:8000/accounts
```

令牌场景下 `GET /accounts` 只返回绑定的那一条账号，因此青龙脚本无需再配置 `YYB_ACCOUNTS`。令牌只能访问 `GET /accounts`、`GET /accounts/avatar`、`POST /accounts/refresh`、`POST /accounts/resync`、`POST /wxapp/getCode`、`POST /wxapp/getPhoneNumber`、`POST /wxapp/operateWxData`，访问其他接口或其他账号的 `ref` 会返回 403。

同一账号重新生成令牌会使该账号的旧令牌立即失效（不影响其他账号），修改管理员密码会撤销全部令牌。令牌只在生成时显示一次，请妥善保存。

## Bark 通知

账号登录失效时可通过 [Bark](https://bark.day.app/#/?id=bark) 推送到 iPhone。通知按账号独立配置：

1. 在账号卡片点击「Bark」。
2. 粘贴 Bark App 中的 device key，或直接粘贴推送 URL（形如 `https://api.day.app/<device_key>/`）；可先点「发送测试」确认，再点「保存」，留空保存表示关闭通知。
3. 同一轮失效只推送一次；账号从失效恢复为可用（重新扫码或自动刷新成功）时会推送一次恢复通知，下一次失领会重新推送。

如果测试返回「Bark 服务中不存在该 device key」，请确认填写的是 Bark App 首页的 device key（通常为 22 位字母数字，不是 API 令牌或 64 位 APNs token），并且弹窗中显示的「推送服务」与 Bark App 里配置的服务器地址一致。

自建 Bark 服务通过 `-bark-server` 或 `YYB_BARK_SERVER` 指定，例如 `YYB_BARK_SERVER=https://bark.example.com`；未设置时使用 `https://api.day.app`。

## Docker

仓库已配置 GitHub Actions：

- Pull Request：只构建镜像，不推送
- `main` 分支：构建并推送 `latest` 和提交标签
- `v*` 标签：构建并推送版本标签
- 构建平台：`linux/amd64`、`linux/arm64`

当前已发布 Docker Hub 镜像：

拉取镜像：

```bash
docker pull joey772/yyb-go:latest
```

运行容器：

```bash
docker volume create yyb-go-data
docker run -d \
  --name yyb-go \
  -p 8000:8000 \
  -e YYB_ADMIN_USER=admin \
  -e YYB_ADMIN_PASSWORD='YourStrongPasswordHere' \
  -e TZ=Asia/Shanghai \
  -v yyb-go-data:/app/data \
  --log-driver json-file \
  --log-opt max-size=10m \
  --log-opt max-file=3 \
  --restart unless-stopped \
  joey772/yyb-go:latest
```

如果服务位于 Nginx、Caddy 等反向代理之后，追加 `-e YYB_TRUST_PROXY=true` 让服务信任 `X-Forwarded-For` / `X-Forwarded-Proto`；需要强制会话 Cookie 仅通过 HTTPS 传输时追加 `-e YYB_SECURE_COOKIE=true`。直接暴露端口时不要开启 `YYB_TRUST_PROXY`，否则来源 IP 和审计日志可被伪造。


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
go vet ./...
go test ./...
go build ./cmd/yyb-go
```

## 更新记录

详细变更请查看 [CHANGELOG.md](CHANGELOG.md)。后续功能统一先记录在 `[Unreleased]` 区域，再按版本整理。

## 使用说明

本项目为二次开发项目。使用、分发或继续开发前，请阅读原作者项目及本项目中的相关声明，并妥善保护运行目录中的账号凭证、会话数据和数据库文件。
