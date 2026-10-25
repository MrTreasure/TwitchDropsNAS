# Twitch 授权与进度读取

## 三个独立状态

1. **未连接或登录失效**：通过服务的「连接并授权 Twitch／重新授权 Twitch」取得一次性授权码，在 Twitch 官方激活页批准。账号密码只输入 Twitch 页面。
2. **游戏账号尚未关联**：打开 [Twitch 活动页](https://www.twitch.tv/drops/campaigns)，展开对应活动，使用活动提供的游戏账号连接入口。已关联的账号不要求重复操作。
3. **Twitch 账号已连接，但进度不可读**：这是接口能力未知／不可用，不应显示成没有登录授权。先在 [官方库存](https://www.twitch.tv/drops/inventory) 检查个人进度，返回服务后同步。无法读取时保留未知，不保证自动达标停止。

截至 2026-10-25，核对 Twitch 官方 OAuth scope 清单后，未找到可供普通观众额外开启个人 Drops 观看进度读取的授权项。官方 Drops Technical Guide 面向游戏开发者的奖励发放集成，不能当作普通观众的“进度读取授权”教程。重新设备授权或换自有 Client ID 都不能保证解决接口不可读，不索取 Cookie、密码或完整性令牌。

设备授权入口来自 Twitch 设备授权响应，短期授权码仅展示给发起授权的管理员。只有登录验证明确失败时，Go API 的 `authorization.reconnectRequired` 才设为 true；库存或活动目录不可读不会设置这个标志。成功校验或重新授权后清除标志。

## 官方资料

- [Device code grant flow](https://dev.twitch.tv/docs/authentication/getting-tokens-oauth/#device-code-grant-flow)
- [Twitch Access Token Scopes](https://dev.twitch.tv/docs/authentication/scopes/)
- [Drops Technical Guide](https://dev.twitch.tv/docs/drops/technical-guide/)

## English

Distinguish account authorization, game account linking, and unavailable personal-progress APIs. Use official device authorization for a missing/expired login and the campaign page for game linking. A connected account whose progress API is unavailable must not be described as unauthorized. No additional ordinary-viewer personal Drops-progress OAuth scope was found in the official scope list checked on 2026-10-25. Reauthorizing or changing Client ID does not guarantee progress access; the developer Drops integration guide is not an ordinary viewer permission flow. Direct users to their official inventory and keep unknown progress unknown.
