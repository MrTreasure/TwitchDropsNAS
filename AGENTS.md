# TwitchDropsNAS contributor instructions

- Python aiohttp backend; React + TypeScript + Vite UI. Use pnpm, exact dependency versions, and the committed lockfile.
- `ui/src/TwitchDrops.tsx` is the reusable standalone/embedded UI. Keep host authentication, navigation, branding and deployment code outside this component.
- Never commit tokens, proxy configuration, runtime SQLite data, user snapshots or deployment logs.
- Completion uses Twitch-confirmed reward data only. A successful watch POST or local elapsed time is not proof of Drops progress.
- Preserve unknown fields, source timestamps, account isolation and manual reward claiming.
- Do not implement media downloads, CAPTCHA/integrity bypasses or automatic reward claiming.
- All proxy choices must be explicit. Do not inspect or change the host's system proxy.
- External scheduling and the embedded scheduler are mutually exclusive. Embedded scheduling exposes enable/disable and execution status via `/api/automation`.
- Build/typecheck and bounded API probes are the routine checks. Browser acceptance is manual unless explicitly requested.
- Agent installation/integration instructions: `docs/agent-prompt.md`; API contract: `docs/api.md` and `docs/openapi.json`.
