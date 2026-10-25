# TwitchDropsNAS

简体中文 · [English](README.en.md)

NAS 上运行的 Twitch Drops 管理服务：低流量观看、多游戏队列、奖励目标、独立 Web UI，以及 Agent / 控制台接入 API。MIT 开源。

**当前状态（实验性）：** 已验证 Docker、UI 与管理 API，但现有设备授权未返回活动目录和个人掉宝进度；网页查询还可能要求完整性验证。授权成功不等于掉宝能力可用。活动说明与账号进度独立展示，不会把 `null` 当成无活动或零进度；个人进度不可读时，不能可靠执行自动达标停止。实际掉宝增长尚未经符合条件的直播验证。

## 界面截图

截图来自隔离演示实例，未连接真实 Twitch 账号，不包含个人昵称、NAS 地址、授权码或密钥。公开奖励说明不代表已获得或已计入进度。

![独立 UI：公开活动奖励说明与进度状态](docs/images/dashboard.png)

![随机检查间隔与连接设置](docs/images/check-settings.png)

## 功能

- Twitch 官方设备授权：在 Twitch 激活页登录，NAS 不接收邮箱密码。
- 一个账号、最多 12 个游戏任务；优先级调度、指定频道、自动发现开播。
- 选择本轮全部奖励，或指定同一游戏不同活动中的奖励。
- 全部模式锁定本轮目标，未来活动不会自动加入；未知资料不当作 0 或完成。
- Twitch 确认已领取、可领取，或观看时长达到官方要求后，停止该目标的观看。
- 前置奖励等手动领取时，继续其他可进行目标；全部启用游戏目标达标后自动结束本轮。
- 不自动领取，不下载视频、音频或播放列表；没有小时／每日流量配额。
- 独立 UI 和嵌入控制台使用同一组件、同一 API；后端可仅启用 Unix socket。

## Docker 启动

```sh
git clone https://github.com/MrTreasure/TwitchDropsNAS.git
cd TwitchDropsNAS
docker compose up -d --build
# 仅首次安装需要：在自己的终端读取自动生成的管理令牌
docker compose exec drops cat /data/admin-token
```

打开 <http://127.0.0.1:8080>，用管理令牌登录，再连接 Twitch、完成官方授权、同步资料、设置游戏和奖励目标并开始挂机。远程 NAS 可先使用 SSH 端口转发：

```sh
ssh -L 8080:127.0.0.1:8080 user@your-nas
```

Compose 默认只绑定主机回环地址。使用 HTTPS 反向代理时，设置 `TDM_PUBLIC_ORIGIN=https://drops.example.com`，保留 Host；浏览器使用 HttpOnly 会话 Cookie，Agent 使用 Bearer 管理令牌。Twitch 凭据与管理令牌不同。

数据位于 Docker 命名卷 `drops-data`。升级前备份此卷；不要使用 `docker compose down -v` 进行普通升级。容器默认 UID/GID 为 1000:1000，宿主机目录挂载需提前赋予该 UID 写入权限。

## 网络与省流

默认先匿名检查直连，失败后使用显式配置的代理。未配置代理时报告连接失败，不自动读取系统代理。需要代理时复制 `.env.example` 为 `.env`，设置容器内可访问的 HTTP 代理地址 `TDM_PROXY_URL`，然后重建容器。`127.0.0.1` 在容器里指容器自身；宿主机代理需要使用容器可达地址。

也可只读挂载原有 mihomo 配置与二进制，设置 `TDM_PROXY_SOURCE`、`TDM_PROXY_NODE`、`TDM_MIHOMO`，只使用明确指定的节点。临时配置通过 Linux memfd 传入，不复制节点密钥到磁盘。项目不包含、推荐或自动选择任何代理供应商。

复用连接；观看上报每分钟一次；离线频道批量检查；空闲频道默认随机间隔 10～20 分钟检查；独立模式的资料同步也采用随机区间；库存通常缓存 30 分钟，等待前置领取时随下一次资料检查更新；活动目录缓存 1 小时，元数据缓存 6 小时。手动同步可立即更新。计量值不是服务商账单，外部 HTTP 代理的开销按估算记录。

## 目标与停止规则

1. **全部奖励：** 保存时锁定当前可见的活动奖励；资料尚未到达时，等待首次完整活动资料后锁定。之后的新活动不自动加入。
2. **指定奖励：** 按活动多选奖励；只接受已读取且属于此游戏的奖励 ID。
3. **达标：** 以官方数据为准。领取和观看达标分开显示，达到目标即可结束观看，不要求最后的奖励已手动领取。
4. **前置：** 只检查所选目标的依赖链。等领取时跳过对应分支，其他可进行目标继续。
5. **完成本轮：** 所有启用任务的非空目标集合均达标，持久化结束状态。重启不会重新挂机。开始新一轮需重新设置目标。
6. **未知／过期：** 不推算完成、不用本地计时替代官方进度。活动过期且未达标时等待管理员调整目标。

尚未取得活动资料时可显式启用仅观看模式，但无法保证掉宝资格或自动识别达标。只有收到官方奖励资料后才能锁定目标和验证完成。只有参与活动的直播可累计，VOD 回放不累计。

## 嵌入已有控制台

- 后端：仅启用 Unix socket，或使用带 Bearer 鉴权的 HTTP API。
- UI：复用 `ui/src/TwitchDrops.tsx` 与 `twitch-drops.css`，传入 `apiBase`；可选传入定时任务页面地址和宿主导航回调。
- Unix socket 必须只允许受信宿主读取。宿主负责自己的登录、权限、Origin 和 actor 校验；不要直接对外代理该 socket。
- 私有宿主集成说明：[docs/embedding.md](docs/embedding.md)。Agent 可用提示词：[docs/agent-prompt.md](docs/agent-prompt.md)。

## 配置

| 环境变量 | 默认值／用途 |
|---|---|
| `TDM_DATA_DIR` | `/data`，状态与独立部署凭据目录 |
| `TDM_SOCKET` | `/run/twitch-drops/service.sock`，权限 0600 |
| `TDM_HTTP_PORT` | `8080`；设为 `0` 禁用 TCP / 独立 UI |
| `TDM_HTTP_HOST` | `0.0.0.0`，配合 Compose 的主机回环绑定 |
| `TDM_ADMIN_TOKEN` | 可选，至少 24 字符；未配置时生成 `/data/admin-token` |
| `TDM_PUBLIC_ORIGIN` | 可选，反向代理的完整公网来源 |
| `TDM_PROXY_URL` | 可选，外部 HTTP/HTTPS 代理；不与内置 mihomo 同用 |
| `TDM_PROXY_SOURCE` / `TDM_PROXY_NODE` | 可选，只读 mihomo 原配置及指定节点 |
| `TDM_MIHOMO` | `/usr/local/bin/mihomo`，需自行只读挂载兼容二进制 |
| `TDM_PROXY_LABEL` | 可选，界面上的非敏感代理显示名 |
| `TDM_EXTERNAL_SCHEDULER` | `false`；设为 `true` 时关闭服务内随机资料检查，改由唯一外部执行器调用 |
| `TDM_EXTERNAL_CHECK_MIN_SECONDS` / `TDM_EXTERNAL_CHECK_MAX_SECONDS` | 外部计划在 UI 中的说明范围，默认 600 / 1200 秒；须与真实原生计划一致，并不修改原生计划 |
| `TDM_ANNOUNCEMENTS_FILE` | 可选，管理员维护的公开活动说明 JSON；默认使用内置的已核对公告，仅展示说明，不作为个人进度或奖励 ID |
| `TDM_PASSWORD_BOOK` | 可选 YAML 凭据宿主适配器，只修改 `twitch_drops.account`；未配置则用 `/data/credentials.json` |

## 调度与 Agent

独立部署默认在 10～20 分钟范围内重新随机选择资料检查间隔，可通过 `/api/automation` 查询启停和真实执行状态。开启／暂停挂机与启停资料同步是两个独立开关。

若宿主已有 cron、systemd 或任务控制台，使用 `TDM_EXTERNAL_SCHEDULER=true`，只登记原生执行器；不要再建另一份业务计划。附带的 systemd 示例使用 上次检查结束后等待 10 分钟，再加 0～10 分钟随机延迟；启动时另有一次初始检查，不声明这能避免平台识别。`deploy/` 提供 systemd 模板。HTTP 保活和观看心跳不是资料同步调度。

[API 文档](docs/api.md) · [OpenAPI](docs/openapi.json) · [Agent 接入提示词](docs/agent-prompt.md)

## 本地开发

```sh
python3 -m venv .venv
.venv/bin/pip install -r requirements.txt
pnpm --dir ui install --frozen-lockfile
pnpm --dir ui run build
TDM_DATA_DIR=./data TDM_UI_DIR=./ui/dist TDM_SOCKET=./data/service.sock .venv/bin/python service.py
```

常规验证：`python3 -m py_compile *.py`、`pnpm --dir ui run typecheck`、`pnpm --dir ui run build`、容器健康和有界 API 检查。UI 交互由人工验收，不默认运行浏览器自动化。

完整 Dockerfile 在容器内构建 UI；已有本地 UI 构建时可用 `docker build -f Dockerfile.runtime -t twitch-drops-nas:1.0.1 .`。Python / UI 依赖固定，Node 构建阶段采用固定版本。

## 说明与许可证

此项目与 Twitch、Gaijin 及任何游戏发行商无隶属关系。活动资格、奖励发放、授权范围及进度由 Twitch / 游戏发行商决定；遇验证或限流停止／退避，不绕过验证。参考项目和原版权声明见 [NOTICE.md](NOTICE.md)，本项目使用 [MIT](LICENSE)。

## 活动说明与账号进度

`catalogue/announcements.json` 是人工核对、带来源与核对时间的公开活动说明。目前包含战雷 WTCS 2026 第一周的 5 项奖励，来源是发行商官方公告，活动窗口参考 Twitch 活动页。它不是从私人库存推断的结果，不含账号关联、观看分钟数、领取状态或 Twitch 奖励 ID，不进入自动目标完成判定。其他游戏可由管理员按同样结构补充公开说明。

活动目录、活动详情和个人库存分别读取，接口字段必须是期望的列表类型；返回空值时显示能力未知／不可用，并保留真正确认过的数据。接口能力失败会退避缓存，手动同步可主动核对；遇完整性验证不会更换客户端头、生成校验令牌或绕过验证。

## 隐私与开源边界

仅发布独立掉宝服务、可复用 UI 与公开说明；不包含任何部署方的真实账号、代理节点、NAS 目录、数据库、日志或其他后台源码。Twitch 公共 Client ID 和 GraphQL 查询哈希是公开互操作标识，不是用户 AK。真实管理令牌和 Twitch 凭据只保存在运行时私有存储中，不进入 API 快照、镜像构建上下文或 Git。
