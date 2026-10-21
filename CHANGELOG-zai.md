# Changelog (zai-beta)

`zai-beta` 是 klinsmaya/multica 的集成分支，用于把尚未合入 `main` 的运营后台与 open-connector 相关 PR 合在一起，方便本地或测试环境整体验证。分支基线为上游 `main`，按 PR #7 → #8 → #9 → #6 的顺序合入。

每次推送 `zai-beta` 都会由 `.github/workflows/zai-beta-image.yml` 构建镜像并推送到 GHCR：
`ghcr.io/klinsmaya/multica-backend:zai-beta` 与 `ghcr.io/klinsmaya/multica-web:zai-beta`（同时打 `zai-beta-<short sha>` 标签）。

## 2026-10-21

基线：`main` @ `5b21b63`（fix(codex): attribute default-model usage, #9360）。

### 平台运营后台 `/admin`（PR #7）

- 新增自建运营后台，路径 `/admin`，与应用同源并复用登录会话。页面包括概览计数、用户（禁用/启用、吊销个人访问令牌）、工作区及成员、审计日志、平台管理员管理。
- 角色：`super_admin`（管理管理员）、`operator`（禁用/启用/吊销）、`viewer`（只读）。`PLATFORM_ADMIN_EMAILS` 引导首个 super_admin，其余在后台授予。
- 所有写操作必须填写原因，并记录到 `admin_audit_log`（操作者、IP、User-Agent），写操作与审计行在同一事务内。
- 用户禁用改为数据库状态 `user.disabled_at` / `disabled_reason`，每个后端实例 30 秒内生效；原硬编码拒绝名单通过迁移导入。
- 迁移 `600`–`606`：`platform_admin`、`admin_audit_log`、`user.disabled_at`，并发索引各自独立文件。
- 后端：`handler/admin.go`（`/api/admin/*`，`RequirePlatformAdmin(minRole)`，拒绝机器凭证）、`auth/disabled_users*.go`。前端：`packages/core/admin`、`packages/views/admin`、`apps/web/app/admin/*`。
- 配置与文档：`.env.example`、`apps/docs` 环境变量页新增 `PLATFORM_ADMIN_EMAILS`。

### 自托管 open-connector 接入（PR #8）

- `TaskService` 改为持有多个 `ConnectorProvider`，open-connector 成为 Composio 之外的第二个连接器后端；仅配置 Composio 的部署行为不变。
- 每次任务入队时为代理拥有者签发 open-connector 运行时令牌（`allowedConnections` 限定为拥有者在代理服务白名单内的活跃连接），把 `<open-connector>/mcp` 挂载进运行，任务结束后删除令牌。
- 设置 → 已连接应用 新增 open-connector 分区（OAuth 或 API key 连接、重命名、设为默认、断开）；代理 MCP 标签页新增按服务的白名单（仅拥有者可改）。
- 新增 Go SDK `server/pkg/openconnector`（测试回放真实实例录制的响应）、业务服务 `server/internal/integrations/openconnector`、路由 `/api/integrations/open-connector`、数据库保存的 OAuth state（回调可跨重启/多副本）、过期令牌回收的调度任务。
- 迁移 `590`–`596`：`agent.connector_service_allowlist`、任务连接器句柄、`user_connector_connection`、`connector_oauth_state` 及索引。
- 前端：`packages/core` 新增 connector 类型、zod schema 与畸形响应测试、查询/变更 hooks；`packages/views` 新增 `open-connector-tab.tsx`；多语言文案（en/zh-Hans/fr/ja/ko）。
- 文档：`open-connector-setup`（en/zh）；`.env.example` 新增 `OPEN_CONNECTOR_*`；可选 e2e（`E2E_OPEN_CONNECTOR=1`）。

### open-connector 运营页接入 `/admin`（PR #9）

- `/admin/connectors` 新增 open-connector 运营页：实例健康检查（10 秒超时，可重新检测）、按服务的发布开关、按服务的工具与用户白名单、按服务及按用户的连接数统计。写操作经 `runAdminAction`，需填写原因并审计（`connector_service.enable|disable|policy`）。
- 行为变更：用户可连接的服务改为由平台管理员在后台「发布」决定。发布目录 = 实例可连接 ∩ `policy.enabled` ∩ `allowed_users` 允许该用户。同一策略同时收窄任务运行时令牌：`allowedActions` 按白名单工具生成 `service.<tool>`（为空时 `service.*`），已下架或用户受限的服务不再挂载进任务。
- `open_connector_apps` 标志仍下发给客户端，但语义改为「集成已接线」（`OPEN_CONNECTOR_BASE_URL` + `ADMIN_TOKEN` 均已设置）；移除 `OPEN_CONNECTOR_API_KEY_SERVICES`。
- 已下架服务的既有连接仍保留在镜像表中，拥有者可查看与断开。
- 迁移 `597`–`599`：`connector_service_policy`。
- 后端：`handler/admin_openconnector.go`（`GET /api/admin/open-connector/{health,services,users}`，`POST …/services/{service}/{enable,disable}`，`PUT …/services/{service}/policy`）；`ErrUserNotAllowed` → 403 `open_connector_user_not_allowed`。
- 前端：`packages/views/admin/connectors-page.tsx`、`apps/web/app/admin/connectors`、`admin` 命名空间多语言文案。
- 文档：`open-connector-setup`（en/zh）改为从 `/admin/connectors` 发布服务。

### 项目信息流视图与任务详情性能（PR #6）

- 项目页布局菜单新增「信息流」视图：顶层任务按时间显示为消息流，每条下方是讨论串条（回复数、参与者、最后回复、未解决串数、状态、PR 芯片、交付物芯片、进度清单），点击打开现有的任务侧窥。底部输入框直接建任务（首行标题、其余描述，`@智能体` / `@小队` 指派，否则给项目负责人），快捷操作行提供继续 / 做计划 / 新任务。支持按创建或按活跃排序并随保存视图持久化。
- 卡片带未读点（每用户阅读游标，`PUT /api/issues/{id}/read`）；智能体最新回复 @ 到当前用户的未解决串显示「等你回复」卡片，可勾选多个串一次性回复；带批注的引用回复链接回原评论。
- 交付物全屏聚焦视图：左侧文件、右侧该任务的讨论，可翻页；Markdown 文件可选中文字加批注；`<!-- pending: … -->` 逐段写作占位与新增段落高亮；`canvas.json` 原型画布（缩放、拖拽、保存布局、Play 预览）。
- 右侧栏仅在信息流视图下显示概览 / 自动化 / 文件库 / PR 四个页签；其他视图保持原属性栏。新增 `GET /api/projects/{id}/attachments`、`POST /api/projects/{id}/library-issue`、`GET /api/projects/{id}/pull-requests`。
- 性能：任务详情不再订阅整个工作区任务列表；详情接口返回 `child_count` / `child_done_count`，子任务展开时按 50 条分页加载；`child-progress` 支持 `parent_ids` 过滤；时间线按根评论分页（`timeline?roots=N`），向上滚动加载更早一页。未带新参数的请求响应不变，旧客户端不受影响。
- 智能体运行提示词新增三段可选约定：进度清单（`multica issue comment add --type progress_update`）、逐段上传文档、原型画布。
- 迁移 `585`–`587`：`issue_read_cursor`（并发唯一索引独立文件）。编号低于本分支已有的 `590`–`606`，迁移运行器按版本逐条判断是否已应用，已部署的 zai-beta 库会补跑这三条。
- 合并冲突仅两处，均为追加合并：`server/cmd/migrate/main.go` 的 `concurrentIndexCleanups` 与 `packages/core/api/schemas.ts`。

### 集成分支自身

- `docker-compose.selfhost.yml` 的 backend 服务透传 `PLATFORM_ADMIN_EMAILS` 与 `OPEN_CONNECTOR_*`。
- 新增 `.github/workflows/zai-beta-image.yml`：推送 `zai-beta` 时构建 amd64/arm64 镜像并推送到 GHCR。
