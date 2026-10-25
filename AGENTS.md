# TwitchDropsNAS contributor instructions

- Go backend with embedded UI and SQLite compatibility; React + TypeScript + Vite UI. Use pnpm, exact dependency versions, and the committed lockfile.
- `ui/src/TwitchDrops.tsx` is the reusable standalone/embedded UI. Keep host authentication, navigation, branding and deployment code outside this component.
- The management UI targets desktop browsers. Do not add mobile layouts or mobile-specific acceptance work; retain the same desktop component when embedded.
- Never commit tokens, proxy configuration, runtime SQLite data, user snapshots or deployment logs.
- Completion uses Twitch-confirmed reward data only. A successful watch POST or local elapsed time is not proof of Drops progress.
- Preserve unknown fields, source timestamps, account isolation and manual reward claiming.
- Do not implement media downloads, CAPTCHA/integrity bypasses or automatic reward claiming.
- All proxy choices must be explicit. Do not inspect or change the host's system proxy.
- External scheduling and the embedded scheduler are mutually exclusive. Embedded scheduling exposes enable/disable and execution status via `/api/automation`.
- Build/typecheck and bounded API probes are the routine checks. Browser acceptance is manual unless explicitly requested.
- Agent installation/integration instructions: `docs/agent-prompt.md`; API contract: `docs/api.md` and `docs/openapi.json`.
- Deployment targets belong to the person installing this project. WorkBuddy deployment means running in that user's selected WorkBuddy execution environment; do not redirect it to the author's NAS or any other external host. Ask for a NAS address only when the user selects their own remote NAS. Check runtime, persistence and process lifetime before claiming WorkBuddy compatibility; see `docs/workbuddy.md`.
- Preserve Python 1.x SQLite and credential compatibility, API null semantics, and management cookie signatures. Run `go vet ./...`, build, UI typecheck/build and bounded service probes. Runtime code must not invoke a Python sidecar.
