# API v1

Standalone base URL: `http://127.0.0.1:8080/api`. Send `Authorization: Bearer <management-token>` from server-side clients. Never use the Twitch access token as the management token. Browser UI authenticates with `POST /api/session` and uses an HttpOnly Cookie; mutations require the matching Origin. API responses are private and `no-store`.

Embedded gateways use the same handlers on an owner-only Unix socket, without the `/api` prefix. `/internal/scan` exists only on that socket. All operations are single-account; do not present this as a multi-tenant service.

| Method | Path | Purpose |
|---|---|---|
| GET | `/snapshot` | Account summary, tasks, goals, official campaign progress, queue state, logs and traffic; never access/refresh tokens |
| POST | `/auth/start` | `{ "clientId": "optional-public-client-id" }`; return official device code/URL; human must approve |
| POST | `/auth/cancel` | Cancel current device authorization |
| POST | `/auth/disconnect` | Stop queue, clear local Twitch credentials, attempt remote revocation |
| POST | `/control` | `{ "running": true/false }`; completed rounds require goals changed first |
| PATCH | `/settings` | `{ "networkMode": "auto"/"direct"/"proxy", "checkMinSeconds": 600, "checkMaxSeconds": 1200 }`; proxy must already be configured at deployment |
| POST | `/tasks` | Create a game task |
| PUT | `/tasks/{id}` | Update task settings and optionally goals; omitted goals preserve existing selection |
| DELETE | `/tasks/{id}` | Remove task |
| POST | `/reward-images/preview` | `{ scope, input }`; parse JSON or HTML and preview activity-scoped image matching |
| POST | `/reward-images/import` | Revalidate and persist matched CDN URLs; no changes to progress, goals or account state |
| POST | `/sync` | Start forced official refresh, returns 202; read `/snapshot` until `syncing=false` |
| GET/PATCH | `/automation` | Query scheduler / `{ "enabled": true/false }`; external scheduler control returns 409 |
| GET | `/openapi.json` | Machine-readable contract |
| GET/POST/DELETE | `/session` | Browser login state / `{ "token": "management-token" }` / logout |

Unauthenticated `GET /health` reports process health only. `/api/session` POST requires the management token; other API calls require Bearer or a valid cookie. Socket auth is delegated to the owner-only host adapter.

## Task write

```json
{
  "gameName": "War Thunder",
  "priority": 100,
  "enabled": true,
  "watchOnly": false,
  "autoChannels": false,
  "channels": ["warthunder_esports"],
  "goal": {
    "mode": "selected",
    "rewards": [{"campaignId": "FROM_OFFICIAL_SNAPSHOT", "dropId": "FROM_OFFICIAL_SNAPSHOT"}]
  }
}
```

`goal.mode="all"` freezes the current round. `goal.reset=true` explicitly replaces an already locked all-mode selection; ordinary task updates preserve it. `mode="selected"` requires 1–500 valid reward references belonging to the chosen game. The client may not set `completed`, `minutes`, `claimed` or `lockedAt`. Up to 12 game tasks; priority integer 0–100; up to 20 channel logins.

## Snapshot goal state

`goals[]` contains `taskId`, `status`, `total`, `completed`, `claimPending`, `blocked`, `runnableCampaignIds` and per-reward derived state. Status is `waiting_targets`, `ready`, `waiting_claim`, `unknown`, `expired` or `completed`.

- `total=0` / no metadata never means completion.
- `claimPending` counts achieved selected rewards that still need manual claiming.
- `blocked` counts selected goals waiting for prerequisite claims. Other runnable goals remain eligible.
- `progress.status="available"` means the official inventory endpoint responded, **not** that every field is present or that Drops are accumulating.
- `watchReports` is a historical count of accepted watch operations, including retired Spade events; never report it as official watched minutes. `watchMethod="hls_head"`, `playbackChecks` and `lastPlaybackAt` identify the new HLS playlist/segment-HEAD path separately. Successful HEAD cycles are still not official reward progress.
- `currentProgress.minutes` and each reward's `minutes` are Twitch values; `null` means unknown. An empty official drop ID is not a valid reward session and is never exposed as zero-minute reward progress.
- `queueCompletedAt` persists round completion. `running=false` after all enabled goals are achieved. Disabled tasks are excluded.
- An expired, unfinished campaign is not completed. Editing goals does not automatically start a paused queue.

## Agent behavior

Read before writing. Discover game/campaign/reward IDs through snapshots, never invent them. Handle HTTP 409 for unsupported capabilities/state changes and 429 cooldown by respecting service state; do not rotate proxies or retry around verification. No claim endpoint exists. Device approval and reward claims require the human.

Poll only the deployed service API, with increasing intervals (e.g. 2, 5, 10 seconds while syncing, then 30–60 seconds); do not repeatedly force Twitch refreshes. Do not log account tokens, device codes, bearer headers or user snapshots in public logs.

## Public descriptions and randomized checks

`announcements[]` contains manually verified public publisher notices with source URLs and verification times. These entries do not have Twitch reward IDs, account linkage, watched minutes or claim state; they never count as tracked goals or completion evidence.

`schedule` exposes `metadataSource`, `metadataMinSeconds`, `metadataMaxSeconds`, `nextMetadataAt` (Unix seconds in internal mode) and `nextChannelAt` (Unix seconds). External schedules remain authoritative and report no fabricated next execution time. Settings allow a randomized idle-check range, integer seconds between 120 and 10800, minimum <= maximum. Older writes omitting the range retain it.

A null campaign/inventory list means unavailable, not an empty list. Inventory and campaign discovery have independent capability flags and retry caches. A normal web request may require integrity verification even after device authorization succeeds; the service does not bypass it.

### Reward images

Public announcement rewards have an optional `imageUrl` containing the original official CDN URL. Tracked campaign drops expose `images[]` with `{ name, url }` from Twitch benefit metadata; older cached drops may omit this array. Only approved public HTTPS CDN hosts are accepted. The backend never fetches image bodies or offers an image proxy. Browsers load the original URL lazily with no referrer; absent or failed images remain visibly labeled. Image availability is unrelated to account progress or reward completion.

See [reward image ingestion](reward-images.md) for repeatable browser extraction, scope selection, matching, conflicts, persistence and automatic Twitch image parsing.

## Authorization guidance

`authorization.reconnectRequired` is an optional boolean for a confirmed expired/rejected account login; campaign/inventory capability failures do not set it. Missing accounts should be guided through `/auth/start` and Twitch device approval. Connected accounts with unreadable progress should be directed to official inventory and game linking, without promising another OAuth permission. See [authorization states](authorization.md).

## Scheduled stream queries

Tasks accept optional `querySchedule`: `{ "mode": "auto" | "always" | "custom", "sessions": [{ "startsAt": "2026-10-25T21:50:00+08:00" }] }`. Omission preserves an existing task setting; new tasks default to auto. Custom mode requires 1–100 explicit timezone-bearing dates. Auto uses matching manually verified entries from the bundled `catalogue/stream-schedules.json`, or a file set by `TDM_STREAM_SCHEDULES_FILE`; games with no matching schedule retain randomized low-frequency discovery. Always ignores advertised time windows.

Known sessions start querying exactly five minutes before the advertised stream time, with a selection check also due at the advertised start. Until a channel is selected, known sessions use randomized 60–120 second channel discovery from five minutes before through fifteen minutes after the advertised start. This bounds stream-publication latency; selected streams and later idle checks retain the configured cadence. This is part of the existing watcher, not another scheduled job. A one-hour late-start grace period is a local polling policy, not an official stream end. Once a real stream is selected, watching continues until it actually ends or goals finish; no guessed duration cuts it off. Published live reruns still require a currently live, eligible Twitch channel and do not enable VOD playback. After the known sessions finish, add sessions or choose always for unannounced broadcasts.

`schedule.queryPlan` contains state, active, nextWakeAt, leadSeconds and per-task plans with state, known, startsAt, nextWakeAt, sourceUrl and verifiedAt. All displayed dates use Beijing time in the UI; stored instants are UTC. Campaign eligibility startsAt/endsAt are not stream start times.

The existing metadata executor is retained. Outside query windows (or while the queue is paused), automatic scans return `checked=false`, reason `waiting_for_query_window`, and record `skipped` without validating the account or contacting Twitch. Native executor wakeups remain native wakeups, not claims of fresh official data. No duplicate embedded metadata scheduler is started in external mode. The existing continuous watcher waits locally until the exact preflight time. Explicit manual `/sync` may refresh outside windows; it does not bypass cooldown/integrity protection.

## Playback and restart recovery

The watcher follows the maintained upstream HLS path at approximately 20-second intervals: obtain the authorized live playback token, cache the lowest-bandwidth variant URL, fetch bounded playlist metadata, and send HEAD requests to the listed Twitch segments. Segment GETs, foreign hosts, automatic claims and integrity endpoints are forbidden. Tokens and signed URLs stay in memory and never enter snapshots or logs. Successful playback checks do not prove reward progress.

A recently verified stream candidate is persisted per account and task. On restart, it is rechecked through Twitch before playback continues, including after the start-wait grace period. Stale, disabled, paused or cross-account candidates are rejected. This recovery is not an extra periodic scheduler.
