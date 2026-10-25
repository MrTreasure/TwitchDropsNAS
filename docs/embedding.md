# 嵌入已有管理后台

## 只运行后端

使用同一个 Docker 镜像，设置 `TDM_HTTP_PORT=0`，挂载持久化 `/data` 及宿主独占的 `/run/twitch-drops`。宿主的 API 网关使用 Unix socket 转发。例如将 `/api/twitch-drops/snapshot` 转为 socket 上的 `/snapshot`。宿主负责会话、管理员权限、Origin 检查，并传入经过验证的 `X-Drops-Actor`。此头仅受信 Unix socket 接受，HTTP 客户端不能借此跳过鉴权。

只转发已列入 API 文档的路径和方法，不开放 `/internal/scan`。保持 JSON 响应 `Cache-Control: no-store`，不将账号、授权码、进度或状态放入公共 CDN 缓存。

## 复用相同 UI

复制这两个文件到宿主的 UI 模块目录：

- `ui/src/TwitchDrops.tsx`
- `ui/src/twitch-drops.css`

宿主需提供兼容的 React 19、Ant Design 6、Lucide React。保持与 `ui/package.json` 版本一致。组件依赖的 `.wt-admin-panel` 等基础样式由宿主提供；独立模式示例为 `ui/src/shell.css`。

```tsx
import TwitchDrops from './TwitchDropsView';

<TwitchDrops
  apiBase="/api/twitch-drops"
  taskRegistryHref="/scheduled-tasks"
  onNavigateTasks={hostNavigationHandler}
/>
```

独立 UI 与嵌入 UI 的业务组件保持字节一致；外层登录与导航放在独立适配器中。仓库 `scripts/export-ui.py` 将组件复制为宿主的 `TwitchDropsView.tsx`，不复制任何宿主源码回本项目。

## 现有调度器

设置 `TDM_EXTERNAL_SCHEDULER=true`。保留宿主原有、唯一的原生随机检查计划，调用 socket `/internal/scan`，以真实 HTTP 返回记录执行状态；禁用时阻止后续同步。`deploy/sync.py` 支持该调用。

同步定时任务的停用不会停止观看心跳；页面的暂停才停止挂机。若宿主有统一任务登记，登记此原生计划并实际读回启停状态，避免重复计划。

## 私有凭据适配器

默认使用独立 `/data/credentials.json`。已有 YAML 密码本的宿主可配置 `TDM_PASSWORD_BOOK`，仅存 `twitch_drops.account`。原子替换需要挂载所在目录；这会扩大容器可见范围，部署方可优先使用专用凭据目录。不在镜像、UI、源码、API 响应和日志中复制令牌。
