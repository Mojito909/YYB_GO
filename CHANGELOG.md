# 更新记录

本文件记录 YYB Go 二次开发版本的重要变更，采用接近 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 的结构。

变更类型说明：

- `Added`：新增功能
- `Changed`：已有功能调整或体验改进
- `Fixed`：问题修复
- `Security`：鉴权、凭证和数据安全相关调整
- `Deprecated`：计划废弃但暂时保留的功能
- `Removed`：移除功能

<br />

## \[0.2.4] - 2026-10-21

### Added

#### Bark 通知

- 账号卡片增加「Bark」入口，可按账号保存 device key 或推送 URL、发送测试通知，留空保存表示关闭通知。
- 账号登录失效时自动推送 Bark 通知；同一轮失效只推送一次，账号恢复可用后重置，下一次失效可再次通知。
- 账号从失效恢复为可用时推送一次恢复通知，标题为 `YYB Go 账号恢复`，正文提示已重新可用。
- 支持 `-bark-server` / `YYB_BARK_SERVER` 指定自建 Bark 服务，默认使用 `https://api.day.app`。
- 新增 `GET/POST /accounts/bark` 与 `POST /accounts/bark/test` 接口，并补充 OpenAPI 说明。
- 测试通知失败时区分“device key 未注册”，返回可操作提示，并在弹窗中显示当前推送服务地址。

### Security

#### 登录鉴权

- 登录接口增加按来源 IP 的失败限流：5 分钟内失败 5 次后封禁 5 分钟，返回 429 并携带 `Retry-After`。
- 用户不存在时同样执行一次 bcrypt 校验，降低通过响应耗时枚举用户名的可能。
- 修改密码的最小长度由 6 位提升到 8 位；修改密码仅接受管理后台 Cookie 会话，账号级 API 令牌调用返回 401。

#### 反向代理与会话

- 新增 `-trust-proxy` / `YYB_TRUST_PROXY` 和 `-secure-cookie` / `YYB_SECURE_COOKIE` 启动配置。
- 默认不再信任 `X-Forwarded-For` / `X-Real-IP`，来源 IP 以 TCP 对端为准；开启 `trust-proxy` 后按 `X-Forwarded-Proto` 决定是否下发 Secure Cookie。

#### 请求与网络

- 所有请求体限制为 1 MiB，声明长度超限返回 413，chunked 超限由 `MaxBytesReader` 兜底。
- 头像下载在拨号阶段拒绝回环、私网、链路本地和 CGNAT 地址，并忽略环境代理，降低 SSRF 风险。

#### 错误输出

- 500 响应不再回显数据库或内部错误细节，统一返回 `internal server error`，详细错误仅写入服务日志。

### Changed

#### 接口日志

- 接口调用日志改为异步批量写入，每 64 条或每分钟落库一次，停机时先清空队列。
- 日志保留期清理由“每次调用执行一次 DELETE”改为每分钟执行一次。
- `api_tokens.last_used_at` 最多每分钟更新一次，降低 SQLite 写压力。

#### 数据库与会话

- `wechat_accounts` 增加 `bark_key` 和 `expired_notified` 字段，启动时自动为旧库补充缺失列。
- SQLite 连接级 PRAGMA 移入 DSN，确保每个新建连接都启用 WAL、外键、busy\_timeout 和 synchronous。
- 总览指标改为 SQL 聚合（总计、分桶、P95 三条查询），不再把统计窗口内的全部日志加载进内存。
- 删除账号时同时清理本地头像文件。
- 启动时清理一次已过期的协议会话。

#### 账号标识

- 数字 `ref` 同时匹配到账号 id 和 uin 时返回 400，并支持 `id:` / `uin:` / `openid:` 显式前缀。
- README 与 OpenAPI 文档同步补充前缀说明。

#### 服务与部署

- `/health` 增加数据库探活，数据库不可用时返回 503。
- HTTP 服务增加 `ReadTimeout`、`WriteTimeout`、`IdleTimeout`，覆盖二维码创建的最长链路。
- CI 测试任务增加 `gofmt` 与 `go vet` 门禁。

### Fixed

#### 账号状态与会话

- 网络或上游临时故障不再把账号误标为 `expired`，仅在上游明确拒绝凭证时标记失效。
- 本地会话缓存损坏时自动清除并重新登录，不再持续返回 502。
- 修复 `api_tokens.last_used_at` 写入失败会导致鉴权失败的问题。

#### 扫码登录

- 修复并发创建二维码时可能误删其他请求刚写入的图片。
- 修复扫码页轮询的 `AbortSignal` 未真正传给 `fetch` 的问题，离开页面时现在能取消在飞请求。

### Removed

#### 遗留功能与代码

- 移除 `/features` 残留：`features` 表定义、默认数据、查询方法及相关扫描代码。
- 移除未使用的 `requestLogger`、`sortedKeys`、`ScanTimeout` 与 `LoginBufferCredentials.Expired`。
- `openAPISpec` 移回 OpenAPI 实现文件，避免文档变量散落在 fallback 页面文件中。

## \[0.2.3] - 2026-08-26

### Added

#### 总览仪表盘

- 增加 `GET /dashboard/metrics` 统计接口，支持日、周、月三种统计周期。
- 增加调用次数、成功数、失败数、成功率、独立 IP、平均耗时和 P95 耗时统计。
- 增加调用趋势数据和 Go 运行时指标，包括运行时长、堆内存、协程数和 GC 次数。
- 控制台总览页增加日 / 周 / 月趋势切换、调用趋势图、失败调用展示和系统性能面板。
- 总览指标每 15 秒自动刷新；指标接口不可用时显示降级状态。

#### 账号管理

- 增加账号状态分段筛选：全部、可用、需重扫、待确认。
- 增加账号状态数量统计，以及按最近更新、名称和状态排序。
- 增加搜索结果数量、当前筛选提示和账号健康度展示。
- 账号卡片补充别名、OpenID、UIN 和最近检测时间信息。
- 增加「添加账号」入口。

#### 能力调用

- 调用配置调整为「发起调用」流程式界面。
- 能力选择增加参数提示。
- 请求与响应区域增加等待、请求中、成功和失败状态展示。
- 增加请求耗时和失败原因展示，清空结果后恢复为准备就绪状态。

### Changed

#### 访问日志

- 增加 `YYB_ACCESS_LOG` 环境变量，支持 `all` 输出全部 Gin 访问日志、`errors` 仅输出 5xx 或耗时超过 2 秒的请求、`off` 关闭访问日志。
- Docker 镜像默认使用 `YYB_ACCESS_LOG=errors`，减少普通成功请求的日志输出。
- Docker 部署示例增加 `json-file` 日志轮转配置，单文件最大 10 MB，最多保留 3 个文件。

#### 控制台

- 调整仪表盘、账号卡片、维护操作、调用结果和筛选控件的视觉与交互布局。

#### 接口文档

- OpenAPI 文档增加 `dashboard` 标签和 `/dashboard/metrics` 接口说明。

### Security

#### 访问日志

- 访问日志仅记录请求路径，不记录完整 URL 查询参数，降低敏感参数进入日志的风险。
- Docker 日志轮转限制日志文件增长，降低日志持续增长导致磁盘占满的风险。

## \[0.2.2] - 2026-08-25

### Added

#### Docker 与 CI

- 增加多阶段 Docker 构建文件。
- 增加 `linux/amd64` 和 `linux/arm64` 多架构镜像构建。
- 增加 GitHub Actions 工作流：Pull Request 只构建，`main` 和 `v*` 标签自动推送 Docker Hub。
- 发布 Docker Hub 镜像 `joey772/yyb-go:latest`。
- 增加 Docker Buildx 缓存和健康检查配置。
- 增加 `.dockerignore`，排除本地运行数据和敏感文件。

#### API 鉴权

- 增加管理员生成、查询和撤销 API Bearer Token 的接口。
- 支持青龙等脚本通过 `Authorization: Bearer TOKEN` 调用受保护 API。
- API Token 仅保存 SHA-256 哈希，生成新令牌时旧令牌立即失效。
- 修改管理员密码时同步撤销 API Token。

#### 青龙接入

- 补充青龙脚本取 code 的接入方式：改用 `POST /wxapp/getCode` 并携带 `Authorization: Bearer TOKEN`。
- 约定脚本侧环境变量：`YYB_BASE`（服务地址）、`YYB_TOKEN`（API 令牌）、`YYB_ACCOUNTS`（账号标识，逗号分隔，取 id / uin / openid 任一）。
- 说明容器网络注意事项：青龙运行在 Docker 内时 `YYB_BASE` 不能填 `127.0.0.1`。

#### 接口日志

- 增加 `/wxapp/` 路径请求的审计中间件，自动记录调用账号、客户端 IP、程序标识（`X-Program` 头或 `User-Agent`）、状态码、成功/失败和耗时。
- 增加 `api_call_logs` 表和 `api_log_settings` 表，启动时自动建表并清理过期日志。
- 增加 `GET /api-logs` 查询接口，支持关键字搜索、IP 过滤、状态筛选和时间范围分页查询。
- 增加 `DELETE /api-logs` 清空全部日志和 `DELETE /api-logs/:id` 删除单条日志。
- 增加 `GET /api-logs/settings` 和 `PATCH /api-logs/settings` 查询与更新日志保留周期（3 / 7 / 15 / 30 天可选）。
- 控制台新增「接口日志」视图：统计卡片（记录总数、成功率、调用 IP 数、保留周期）、日志表格（时间、接口、账号、调用 IP、程序、状态、耗时）、搜索 / IP / 状态筛选、分页导航、每 10 秒自动刷新、单条删除与清空。

### Changed

#### API 令牌

- API 令牌改为账号级：令牌与微信账号一对一绑定，`api_tokens` 表新增 `account_id`。
- `/auth/token` 的 POST / GET / DELETE 均需要 `ref`（账号 id / uin / openid），POST 与 DELETE 也可放在请求体。
- 令牌只能访问白名单接口：`GET /accounts`、`GET /accounts/avatar`、`POST /accounts/refresh`、`POST /accounts/resync`、`POST /wxapp/getCode`、`POST /wxapp/getPhoneNumber`、`POST /wxapp/operateWxData`，其余接口返回 403。
- 令牌访问其他账号的 `ref` 返回 403；`GET /accounts` 使用令牌时只返回绑定账号一条记录。
- 启动迁移会丢弃不含 `account_id` 的旧 `api_tokens` 表，历史令牌全部失效，需在控制台重新生成。
- 删除账号时同步清理该账号的令牌。

#### 控制台

- 令牌入口从右上角用户菜单迁到账号管理：账号卡片「令牌」按钮与详情抽屉「API 令牌」按钮。
- 令牌弹窗显示绑定账号、生成时间与最近使用时间，支持重新生成与吊销。

#### 青龙接入

- 使用账号级令牌后脚本无需再配置 `YYB_ACCOUNTS`，令牌自身即决定可操作的账号。

#### 仓库管理

- 将 `resource/db`、`resource/avatars`、`resource/qr` 等运行时数据移出版本控制，新增 `.gitignore`。

### Fixed

#### Docker 架构

- 修复跨架构构建时未显式传入 `TARGETOS/TARGETARCH`，导致 ARM 镜像可能携带错误架构可执行文件的问题。

#### Docker 数据卷（部署侧变更）

- 新增 `-data-root` 启动参数，把运行时数据（`db`/`avatars`/`qr`）与页面资源（`static`/`templates`）分离；未指定时沿用 `-resource-root`，本地开发布局不变。
- 镜像的数据目录改为 `/app/data`，`VOLUME` 由 `/app/resource` 收窄为 `/app/data`。原先挂载 `/app/resource` 会让命名卷覆盖镜像内的前端，导致更新镜像后控制台仍是旧页面（例如账号卡片缺少「令牌」按钮）。
- 已有部署只需把 `-v yyb-go-data:/app/resource` 改为 `-v yyb-go-data:/app/data`，卷内 `db`/`avatars`/`qr` 布局不变，数据无需迁移。

#### 控制台缓存

- `/static` 下的资源返回 `Cache-Control: no-cache`，避免浏览器启发式缓存复用旧的 `console.js`/`console.css`。

#### 文档

- 修复 README 中 Markdown 链接未用尖括号包裹导致渲染异常的问题。
- 精简 README 中 Docker 数据卷的重复说明。

## \[0.2.1] - 2026-08-23

### Changed

#### 控制台 UI

- 重做控制台为侧边栏 + 内容区布局，划分总览、账号管理、能力调用、系统四个视图。
- 增加 Hash 路由视图切换，刷新和分享链接可直达对应视图。
- 主题切换升级为亮色 / 暗色 / 跟随系统三态，偏好写入 localStorage。
- 概览页改为统计卡片 + 纯 CSS 状态分布条，不依赖图表库。
- 建立 CSS 设计令牌单一来源，亮暗主题共用同一套变量。
- 窄屏下侧边栏折叠为抽屉导航，保留键盘操作和跳转链接。

#### 扫码轮询

- 二维码状态轮询改为异步状态缓存模式：后台协程持续代理微信长轮询并更新会话状态，`/qr/{sid}/poll` 仅读取快照、毫秒级返回。
- 会话状态读取与登录凭证换取使用独立锁，网络请求期间不持有状态锁。
- 会话过期、主动删除或服务关闭时停止后台轮询并中断在飞的长轮询请求。
- 扫码页改为串行轮询，使用轮次令牌作废过期响应，并在页面隐藏、卸载和站内导航时收尾。

### Fixed

#### 浏览器控制台

- 修复扫码页停留或导航离开时，控制台出现 `net::ERR_ABORTED` 二维码轮询请求错误日志的问题。

## \[0.2.0] - 2026-08-22

本版本为 YYB Go 的二次开发阶段版本，基于原作者 [SuperNaiBA/YYB\_GO](https://github.com/SuperNaiBA/YYB_GO) 持续扩展。

### Added

#### 管理员认证

- 增加管理员登录、退出登录和当前会话查询。
- 增加首次启动管理员初始化。
- 增加环境变量 `YYB_ADMIN_USER` 和 `YYB_ADMIN_PASSWORD`。
- 增加管理员密码修改功能。
- 增加 HttpOnly Cookie 会话和滑动续期。

#### 二维码登录

- 增加二维码创建接口。
- 增加二维码图片获取接口。
- 增加扫码状态轮询接口。
- 增加扫码授权确认和账号入库接口。
- 增加二维码临时文件清理。

#### 账号管理

- 增加账号列表查询。
- 增加账号按 `id`、`uin` 或 `openid` 定位。
- 增加账号删除接口。
- 增加账号头像接口。
- 增加账号存活状态刷新。
- 增加账号资料同步。

#### 目标应用能力 API

- 增加 `POST /wxapp/getCode`。
- 增加 `POST /wxapp/getPhoneNumber`。
- 增加 `POST /wxapp/operateWxData`。
- 统一返回 `{code, msg, data}` JSON 响应结构。

#### 数据存储

- 增加 SQLite 数据库初始化。
- 增加账号、登录凭证和账号资料持久化。
- 增加协议会话缓存及过期时间管理。
- 增加历史 `wmpf_sessions` 表迁移到 `sessions` 表的兼容逻辑。

#### 接口文档

- 增加 OpenAPI 3.0.3 文档。
- 增加 Swagger UI 页面。
- 增加接口请求体、响应体和数据模型说明。

#### 管理后台

- 增加控制台账号管理工作区。
- 增加账号搜索和状态筛选。
- 增加账号详情抽屉。
- 增加批量刷新存活状态和同步资料操作。
- 增加调用配置、结果展示和结果复制。
- 增加登录页和扫码页。

### Changed

#### 前端 UI

- 统一控制台、登录页和扫码页的视觉设计。
- 调整为深海蓝灰基础色与青绿色操作强调色。
- 优化控制台概览、统计卡片、账号卡片和调用工作区的信息层级。
- 优化按钮、输入框、面板、抽屉、Toast 和确认弹窗的交互状态。
- 增加亮色 / 暗色主题适配。
- 增加移动端响应式布局。
- 增加悬停、选中、焦点和加载状态反馈。
- 增加 `prefers-reduced-motion` 动效降级处理。

#### 协议会话

- 增加短期会话缓存，减少重复登录和重复握手。
- 按 TCP 代理配置隔离协议会话。
- 代理连接失败时支持回退直连。

#### 接口结构

- 业务 API 统一纳入管理员鉴权中间件。
- 浏览器页面未登录时跳转 `/login`。
- 未登录 API 请求返回 401 JSON 响应。

### Fixed

#### 数据库兼容

- 增加旧会话表结构迁移处理。
- 增加 SQLite 数据目录和运行时资源目录自动创建。

#### 前端交互

- 增加异步操作期间的按钮禁用和进行中提示。
- 增加账号卡片键盘 Enter / 空格选择支持。
- 增加头像加载失败时的首字母回退显示。
- 增加二维码刷新时的稳定占位和错误提示。

### Security

#### 鉴权与凭证

- 管理员密码使用 bcrypt 哈希存储。
- 登录凭证通过 HttpOnly Cookie 传递。
- 管理员修改密码后销毁其他活动会话。
- 账号登录凭证不通过公开账号列表接口返回。

## \[0.1.0] - 原始项目基线

### Added

#### 原始实现

- 相关协议基础实现。
- 扫码登录流程基础实现。
- 目标应用能力调用基础实现。
- Go 服务端项目基础结构。

原始项目及版权信息请参考：[SuperNaiBA/YYB\_GO](https://github.com/SuperNaiBA/YYB_GO)。

## 后续记录规范

版本条目按以下层级组织：

```text
版本
└── 变更类型
    └── 功能模块
        └── 具体变更
```

### 新增功能

在 `[Unreleased]` → `Added` 下按模块增加条目：

```markdown
### Added

#### 模块名称

- 增加具体功能和用户可见行为。
```

### 问题修复

在 `[Unreleased]` → `Fixed` 下写清楚影响范围和修复结果：

```markdown
### Fixed

#### 模块名称

- 修复某个问题，说明修复后的行为。
```

### 发布版本

发布时按以下顺序处理：

1. 将 `[Unreleased]` 中已完成的内容整理到新版本。
2. 补充发布日期和版本号。
3. 保留 `Unreleased`，继续记录下一轮开发。
4. 同步更新 README 的功能和部署说明。

