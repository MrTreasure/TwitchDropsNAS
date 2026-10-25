# 在用户自己的 WorkBuddy 环境中部署

这里的 WorkBuddy 场景是：在使用者当前选择的 WorkBuddy 执行环境中运行 TwitchDropsNAS 后端及 UI。无需连接项目作者的 NAS；仓库不提供共享后端。文档其他位置的 NAS 均指使用者自己的设备，只有主动选择远程 NAS 部署时才需要其地址和访问方式。

## 当前支持边界

**本项目尚未完成 WorkBuddy 实测，不能声明一键安装或持续托管已验证。** 已有 Docker Compose 和 Unix 本地运行方式，能否使用取决于该 WorkBuddy 环境实际提供的能力。

| 用户选定的执行环境 | 当前方案 |
|---|---|
| 可访问 Linux Docker 引擎及 Compose | 在该环境构建并启动现有 Compose，数据保存在该环境自己的卷中 |
| 无 Docker，环境可执行对应平台 Go 程序 | 运行独立可执行文件及内嵌 UI，无 Python 依赖；需要额外核实保活和持久化 |
| Windows | 使用 Go 的 Windows `.exe` 和 HTTP 入口，不配置 Unix socket；已验证交叉编译，真实运行尚未验收 |
| 临时沙箱、按请求调用的云函数或仅静态网站托管 | 未证明能承载常驻观看任务；需要平台明确提供持久存储、后台进程和持续外网请求能力后再评估 |

源码构建需要 Go 1.26.5、Node.js 26 和 pnpm 12.3.4；运行已构建程序不需要这些工具。检查 Docker 时需确认 daemon 可用，只有 `docker` 命令文件不够。内置 mihomo 配置适配器仍依赖 Linux 匿名内存文件；其他系统使用直连或使用者明确配置的 HTTP/HTTPS 代理。

## Agent 执行顺序

1. 确认当前任务目标是 WorkBuddy 环境内部部署；只检查该环境，不要求 NAS 地址，不扫描其他主机。
2. 检查实际系统、命令执行权限、依赖安装、可用 Docker daemon、磁盘持久化、端口访问和 Twitch 外网连通性。只使用用户指定的代理，不复用作者的代理。
3. 满足 Docker 条件时使用 README 的 Compose；否则使用对应平台的 Go 独立程序。原生运行设置 `TDM_DATA_DIR`，HTTP 默认监听 `127.0.0.1`；UI 已嵌入，无需配置 socket 或 UI 目录。
4. 确认 UI／API 可以在平台允许的入口访问，保留管理鉴权和 Origin 校验。容器回环地址、沙箱回环地址与用户浏览器不是必然相同的网络空间，不虚构可访问链接。
5. 核对后端的实际存活期限及数据持久化：任务结束、会话结束、沙箱回收、退出 WorkBuddy、系统休眠各自如何影响服务。没有验证的生命周期标为未知；若服务随环境停止，明确它无法继续挂机。
6. 用户在 Twitch 官方页面批准自己的授权，随后核对官方目录和进度能力。环境可运行不代表 Twitch 掉宝增长已验证。
7. 任何缺项均具体报告；未经用户选择，不切换到 NAS、外部服务器或平台网站发布功能。

本地运行命令见 README 的“本地开发”，完整接入流程见 [Agent 提示词](agent-prompt.md)。周期业务任务只保留一个执行器；WorkBuddy 不应另建重复检查任务。

## 官方文档依据

核对日期：2026-10-25。

- [默认权限与安全沙箱](https://www.workbuddy.cn/docs/workbuddy/From-Beginner-to-Expert-Guide/Function-Description/Permission-Modes)说明命令执行、文件和网络权限。这不构成 Docker 引擎或常驻托管能力的保证。
- [网站应用开发与发布](https://www.workbuddy.cn/docs/workbuddy/From-Beginner-to-Expert-Guide/Function-Description/App-Publishing/Web-App)说明网站及配套云服务，没有承诺可原样托管本项目的常驻后端进程或容器。
- [系统设置](https://www.workbuddy.cn/docs/workbuddy/From-Beginner-to-Expert-Guide/Function-Description/Setting)说明本地任务执行期间可防止空闲休眠，任务结束后不再提供这项保护；不能据此承诺后台服务永久存活。

## English summary

WorkBuddy deployment means running this backend and UI **inside the installing user's selected WorkBuddy execution environment**. It does not mean connecting to the author's NAS, service, proxy, or account. NAS instructions elsewhere refer to the installing user's own device and apply only when they choose that target.

This project has not been tested in WorkBuddy. Check the actual runtime before claiming support: a working Linux Docker engine and Compose can use the existing container deployment; a compatible environment can run the platform-specific Go executable with its embedded UI and HTTP API. No Python is required. Windows builds do not enable Unix sockets by default. Cross compilation is verified, but actual Windows/macOS runtime acceptance is not. The optional embedded mihomo adapter additionally needs Linux `memfd_create` and `/proc`; other environments should use direct access or the user's explicit HTTP/HTTPS proxy.

Verify persistent private storage, dependency installation, UI/API access, Twitch connectivity, and the process lifetime across task completion, session closure, sandbox recycling, application exit and system sleep. WorkBuddy command execution and website publishing documentation do not establish permanent container hosting. Report missing capabilities and wait for the user's choice; do not silently switch to a NAS or another external host. Deployment success also does not establish actual Twitch Drops progress.
