# 可复制给 Agent 的部署与接入 Prompt

将下面整段提示词交给你的 Agent。所有部署环境、NAS、代理、账号与数据均属于使用这份提示词的用户；项目作者的运行实例不是公共后端。已有服务只填写用户自己的访问方式，无需重新安装。设备授权成功不代表能读取掉宝进度，先检查实际能力。

---

请部署或接入开源项目 https://github.com/MrTreasure/TwitchDropsNAS ，提供 Twitch Drops 目标管理。

## 开始前

1. 阅读仓库 README、AGENTS.md、docs/api.md 和 docs/openapi.json。
2. 在我选定的部署环境内检查是否已有运行实例、持久卷和定时任务，优先复用，禁止重复部署或重复调度。
3. 本任务只授权处理掉宝项目，不读取或上传无关应用的源码、数据库或凭据。
4. 先确定我的部署目标：WorkBuddy 当前执行环境、我自己的 NAS／服务器，或我自己的已有控制台。选择 WorkBuddy 时直接检查当前环境，不默认要求 NAS、SSH 或外部服务器。只有我选择自己的远程设备时，才询问该设备缺失的地址及访问方式。运行目录、代理、游戏／奖励等缺失信息按需询问。
5. 不连接、探测或复用项目作者、演示站点或其他用户的 NAS、管理 API、代理、账号和数据。公开仓库是源码分发入口，不提供共享运行服务；不从截图、文章或源码历史推导部署地址。代理只能使用我指定的地址／节点，不改系统代理、不自动选供应商。

## 部署或接入方式

### WorkBuddy 当前执行环境

- 目标是在我当前使用的 WorkBuddy 执行环境中运行独立后端和 UI。先阅读 [docs/workbuddy.md](workbuddy.md)，检查实际操作系统、Go 构建工具／Node／pnpm、Docker 引擎与 Compose 是否可用、持久目录、端口访问方式及后台进程生命周期。
- 环境内有可用的 Linux Docker 引擎时，在该环境执行下方独立 Docker 部署；不连接 NAS。
- 没有 Docker 时，优先下载校验对应平台的 Release 程序，或构建并在当前环境运行 Go 独立程序。UI 已嵌入，无需 Python；设置私有持久数据目录，HTTP 默认绑定 `127.0.0.1`，原生模式无需 Unix socket。源码构建需要 Go 1.26.5、Node.js 26 和 pnpm 12.3.4，运行阶段不需要这些工具。Windows 使用对应 `.exe` 且不启用 Unix socket；跨平台编译通过不等于 WorkBuddy 已实测。
- WorkBuddy 支持执行命令，不等于它提供 Docker daemon、持久沙箱或常驻服务托管。检查环境是否会随任务、会话、沙箱回收、退出或设备休眠而终止服务，是否允许持续外网请求；无法确认时明确报告未验证。
- 当前环境不满足条件时报告具体缺项，保留当前部署目标，等待我选择下一步；不得自动改为连接任何 NAS、购买云主机或部署到外部网站托管。

### 独立 Docker 部署（当前环境或我自己的 NAS／服务器）

- 克隆项目，使用 Docker Compose 构建并启动，持久化 `/data`，默认仅绑定主机回环地址。
- 本地环境直接访问回环 UI；我自己的远程设备才使用 SSH 转发，或在保留鉴权和 Origin 的 HTTPS 反向代理后提供 UI；不要直接暴露 Unix socket。
- 启动时确认是否需要独立 Web UI，使用 `--web-ui=true/false` 或 `TDM_WEB_UI`；已有管理台选择 false。关闭页面不等于关闭 API，也不等于停止任务。
- 管理令牌从我的凭据存储或容器持久化文件注入，不写入仓库、命令行参数、聊天、日志或公开产物。
- 先检查 `/health`，用 Bearer 管理令牌读取 `/api/snapshot`，确认未授权请求被拒绝。
- 独立模式只保留一份内部资料同步计划，`/api/automation` 可查看、启停和读取真实结果。

### 已有控制台

- 只运行同一个 Docker 后端，设置 `TDM_WEB_UI=false`、`TDM_HTTP_PORT=0`；由现有已鉴权网关代理 owner-only Unix socket，校验现有管理员、Origin 和请求方法。
- 复用 `ui/src/TwitchDrops.tsx` 和 CSS，使用 `node scripts/export-ui.mjs <目标目录>` 导出，传入控制台 API 前缀和可选任务导航。不要重写另一套奖励判定逻辑。
- 保留组件内独立的“默认／战雷”页面风格开关；导出同时包含 `war-thunder-style.ts`。选择只写浏览器 `twitch-drops-ui:style:v1`，不修改宿主全局主题、后端配置或任务。若宿主有 CSP，图片来源需允许 `https://warthunder.com` 与 `https://static.encyclopedia.warthunder.com`；不转存官方素材。
- 如果已有原生同步计划，设 `TDM_EXTERNAL_SCHEDULER=true`；由唯一 cron/systemd 执行器调用 socket `/internal/scan`。登记到宿主统一任务控制台并实际核对启停；没有登记桥接器则报告该限制，不虚称已接入。

## Twitch 授权和目标

1. 调用 `/auth/start`，只把官方激活链接和短期授权码提供给我，由我在 Twitch 官方页面登录批准。不要索取邮箱／Twitch 密码，也不要读取我的浏览器 Cookie。
2. 区分未连接／`authorization.reconnectRequired` 与已连接但进度接口不可用；后者不反复引导授权，改为提示官方库存和游戏关联入口，参见 docs/authorization.md。确认账号连接后调用一次 `/sync`，轮询当前部署服务的 `/snapshot` 到 `syncing=false`；独立 HTTP API 路径使用 `/api` 前缀。
3. 为我的游戏创建任务，填写频道策略和优先级；从官方快照选择奖励 ID。允许“本轮全部奖励”或“指定奖励”。全部模式锁定本轮，不自动追逐未来活动。
4. 配置 `querySchedule`：默认 auto 使用已公布直播场次，开播前 5 分钟查询；缺少资料的游戏仍低频巡检。需要人工设置时通过 UI 的北京时间输入，或 API 的带时区 `startsAt`。不可用活动起止时间代替直播时间。原生计划仅保留一份，窗口外跳过对外请求；不要再建一个广播轮询任务。
5. 没有官方活动资料时明确报告未知；只有我已确认活动和频道资格时才使用 `watchOnly=true`。
6. 开始后由当前部署的后端自动发现符合条件直播。以官方进度达标为准；观看上报成功、本地计时均不构成奖励证明。
7. 前置需要领取时提醒我手动领取，其他目标可继续。目标达标后停止观看；所有启用任务达标后自动结束。不得添加自动领取、视频下载或验证绕过。

## 验收与交付

- 核对容器或原生服务健康、未授权拒绝、带鉴权读取、任务与目标保存、唯一调度源、启停控制，以及已有控制台功能保留。记录 WorkBuddy 环境的保活和持久化验证结果，不把临时进程启动成功当作持续托管成功。
- 不默认运行浏览器自动化。UI 由我人工验收。
- 若没有符合条件的直播，清楚交付“服务正常、等待开播，实际掉宝增长未验证”，不要伪造分钟数。
- 提供访问方式、目录、配置项名、镜像版本、备份与回滚步骤；不展示任何密钥值。
- 后续配置变更先读取当前状态，保留无关游戏和目标。管理 API 没有自动领取接口。

---

## Reward images

Read `rewardImageScopes` from the authenticated snapshot. Normal Twitch synchronization parses benefit image URLs automatically. If the capability is unavailable, ask the operator for public reward-region JSON/HTML exported from their normal Twitch page; never request cookies, passwords, or integrity tokens. Call `POST /reward-images/preview` with the exact scope and input, present matches/unmatched/conflicts, then import only after the operator confirms the mapping. Do not infer reward IDs, claim state or watched time from pictures. No code edit or redeployment is required; original allowed CDN URLs persist in SQLite. Details: `docs/reward-images.md`.

### 添加活动任务的操作动线

先读 `/snapshot` 的 `campaignDirectory`，让使用者按游戏/活动选择；需要奖励详情时调用 `/tasks/preview`，不要为列表每一项请求 Twitch。从返回的 `campaigns` 取真实 reward ID，提交普通 `/tasks` 或更新同一游戏已有任务。未明确替换时合并原目标并保留频道/查询计划。选择单个活动全部奖励使用 `goal.mode=selected` 与该活动实际引用，不要改为整个游戏的 all 模式。保存与开启队列是两步；使用者选择“保存并开始”后才调用 `/control`。目录不可读时说明列表不完整，可由使用者选择手动添加游戏；不从公开公告编造奖励 ID。
