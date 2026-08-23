# 更新记录

本文件记录 YYB Go 二次开发版本的重要变更，采用接近 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 的结构。



变更类型说明：

- `Added`：新增功能
- `Changed`：已有功能调整或体验改进
- `Fixed`：问题修复
- `Security`：鉴权、凭证和数据安全相关调整
- `Deprecated`：计划废弃但暂时保留的功能
- `Removed`：移除功能

## [Unreleased]

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

### Planned

#### 青龙集成

- 增加独立的青龙脚本示例目录。
- 增加二维码 Base64 获取和通知渠道推送示例。
- 增加青龙定时任务的账号存活检查与失败重试模板。

#### 部署

- 增加 Docker Compose 示例。
- 增加健康检查、优雅停止和持久化目录说明。
- 增加常见代理和容器网络配置示例。

## [0.2.1] - 2026-08-23

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

## [0.2.0] - 2026-08-22

本版本为 YYB Go 的二次开发阶段版本，基于原作者 [SuperNaiBA/YYB_GO](https://github.com/SuperNaiBA/YYB_GO) 持续扩展。

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

## [0.1.0] - 原始项目基线

### Added

#### 原始实现

- 相关协议基础实现。
- 扫码登录流程基础实现。
- 目标应用能力调用基础实现。
- Go 服务端项目基础结构。

原始项目及版权信息请参考：[SuperNaiBA/YYB_GO](https://github.com/SuperNaiBA/YYB_GO)。

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
