# TwitchDropsNAS

[简体中文](README.md) · English

A self-hosted Twitch Drops manager with Docker, a reusable Web UI, per-game reward goals, and an API for agents and existing admin consoles. MIT licensed.

Choose your own deployment target: your NAS/server or a compatible WorkBuddy execution environment. Each instance owns its user's accounts and data; it does not depend on the author's NAS, admin service, or proxy. WorkBuddy requires environment checks and has not been tested with this project. See [WorkBuddy deployment requirements](docs/workbuddy.md#english-summary) and the [agent prompt](docs/agent-prompt.md).

> **Experimental status:** On 2026-10-25, a Linux amd64 deployment on a real War Thunder stream returned a valid reward ID, official watched minutes and reward goals. A user-provided Twitch inventory screenshot dated 2026-10-26 also confirms two manually claimed booster rewards; see the final verification section. Viewing uses HLS playlist metadata and segment HEAD requests without media body downloads. Other games, authorization profiles and environments still require separate verification; full-round automatic stopping remains unverified. Campaign discovery can still return null. Unknown data is never treated as zero or completion, and integrity challenges are not bypassed.

## Screenshots

These desktop screenshots show the same UI in two themes, using anonymized demo data in an isolated local page. They illustrate the interface; real Drops and claim evidence appears in the final section.

### War Thunder theme

Dark hangar panels, official War Thunder icons and reward cards.

![War Thunder theme: task queue, reward goals and progress panels using isolated demo data](docs/images/dashboard-war-thunder.png)

### Default style

The same tasks, progress and account controls, with the theme switch at the top right.

![Default style: the same functionality using isolated demo data](docs/images/dashboard-default.png)

<details>
<summary>Connection settings and reward image import</summary>

![Connection settings and randomized idle-check range, isolated demo](docs/images/check-settings.png)

![Activity-scoped image import preview, isolated demo](docs/images/reward-image-import.png)

</details>

## Features

- A page-specific Default / War Thunder style switch with the official logo, research and talisman icons, muted red reward cards, and game-inspired panels. The choice stays in this browser and does not change the host console theme. Official assets load directly from their original HTTPS URLs.
- Official Twitch device authorization. Approve the code on Twitch; the backend does not request your email or Twitch password.
- One account and up to 12 prioritized game tasks, with configured channels and optional channel discovery.
- Select all rewards in the current round or specific rewards from officially returned campaign data.
- All-reward mode freezes one round. Future campaigns are not silently added.
- Stop a goal after Twitch confirms it is claimed, claimable, or has reached the required watch time. All enabled tasks must have nonempty, achieved goals before the round ends.
- Wait for manual prerequisite claims while other runnable goals continue. Claiming is always manual.
- No video/audio segment downloads, browser playback, automatic claims, or hourly/daily traffic quotas. The watcher reads small HLS playlist metadata and sends HEAD requests to segments, following the current upstream playback path.
- Public publisher announcements and private account progress are displayed separately.
- Reusable UI for standalone operation or embedding in an existing console.

## Run with Docker

```sh
git clone https://github.com/MrTreasure/TwitchDropsNAS.git
cd TwitchDropsNAS
docker compose up -d --build
# First installation only: read the generated management token in your own terminal.
docker compose exec drops cat /data/admin-token
```

Open <http://127.0.0.1:8080>, sign in with the management token, connect Twitch, approve the official device authorization, synchronize, configure a game and its goals, and start the queue. If Twitch does not return a capability, the UI reports it as unavailable.

For a remote NAS, an SSH tunnel keeps the default loopback binding:

```sh
ssh -L 8080:127.0.0.1:8080 user@your-nas
```

For an HTTPS reverse proxy, set `TDM_PUBLIC_ORIGIN=https://drops.example.com` and preserve the Host header. Browsers use an HttpOnly session cookie; agents use a management Bearer token. The management token and Twitch access token are different credentials.

Data lives in the `drops-data` Docker volume. Back it up before upgrades. Do not use `docker compose down -v` for an ordinary upgrade. The container runs as UID/GID 1000:1000; prepare writable host bind mounts for that UID if replacing the named volume.

## Add a watching task

1. **Connect Twitch:** obtain the device code in the account panel and approve it on Twitch. Verify game account linking on the [official campaign page](https://www.twitch.tv/drops/campaigns).
2. **Choose a campaign:** click **选择掉宝活动** (Choose Drops campaign) in the game task panel. Search by game or campaign; filter active, upcoming or all entries, then click **选择活动与奖励**. Use **同步活动资料** to request a refresh if needed, and wait for it to finish.
3. **Review rewards:** the game is filled automatically and all rewards in this campaign are selected. Remove unwanted rewards. If that game already has a task, the new campaign is added to the draft alongside existing targets, preserving channel, priority and schedule settings. Changes apply only after saving.
4. **Save and start:** click **保存并开始挂机** (Save and start). **保存任务** saves without starting a paused queue; **开始挂机** starts it later. A running queue continues after saving. Disabled tasks must be enabled explicitly.
5. **Claim manually:** use the [official inventory](https://www.twitch.tv/drops/inventory). Claim prerequisites and synchronize so dependent goals can continue.

One account supports up to **12 game tasks**, with multiple campaigns per game. **Only one channel is watched at a time**, with higher-priority runnable tasks first. The round ends when Twitch confirms every enabled task's nonempty goal set is achieved.

**Catalogue sources:** Device authorization may expose inventory while returning no Twitch campaign directory. The service supplements discovery from the [Sunkwi public catalogue](https://twitch-drops-api.sunkwi.com/v2/drops), labels its source/time, and reuses a one-hour cache (about 0.8 MB per response, variable). Only anonymous GETs to this fixed URL are allowed; Twitch tokens, cookies, account identifiers and management credentials are never forwarded. Public linkage/progress/claim fields are discarded, and public watch requirements cannot establish completion. Twitch live-channel metadata must confirm eligibility and official requirements before that data is authoritative; inventory/session data remains authoritative for personal progress. Unverified rewards remain unknown. Sync results distinguish inventory from catalogue failures. Cache and manual game setup remain available if both directories fail; no integrity checks are bypassed. Campaign validity dates are not stream start times. Follow this section for the current button flow.

## Network and low bandwidth

The default policy probes direct connectivity anonymously and falls back only to an explicitly configured HTTP/HTTPS proxy. Without a proxy, a failed direct connection is reported; the service does not discover or change the system proxy.

Copy `.env.example` to `.env` and set `TDM_PROXY_URL` to a proxy reachable from inside the container. Container `127.0.0.1` refers to the container itself.

Alternatively, mount your original mihomo configuration and compatible binary read-only. Configure `TDM_PROXY_SOURCE`, `TDM_PROXY_NODE`, and `TDM_MIHOMO`. Only the selected node is used; an in-memory Linux memfd holds the temporary configuration. No provider credentials are shipped or copied into generated disk files.

Idle channel checks default to a newly randomized 10–20 minute interval. The standalone metadata scheduler uses that range too. The external systemd example uses 10 minutes after the previous check finishes plus a random delay of up to 10 minutes, with a separate initial startup check. Random timing reduces synchronized requests; it is **not** a promise to avoid platform detection.

Active viewing heartbeats retain their protocol timing. TLS connections are reused, channel lookups are batched, inventories are normally cached for 30 minutes, catalogues for one hour, and campaign metadata for six hours. Manual synchronization can refresh immediately. Failed capabilities use retry caching; verification challenges are not bypassed. Traffic measurements are not carrier billing figures, and external proxy overhead is estimated.

## Goals and unknown progress

1. **All rewards:** freeze the current visible campaign rewards. If metadata has not arrived, wait for the first complete set. Future campaigns are not added automatically.
2. **Selected rewards:** choose known reward IDs belonging to the selected game. IDs are never invented from names or screenshots.
3. **Completion:** trust Twitch-confirmed account data. Local elapsed time and successful watch POSTs do not establish watched minutes.
4. **Prerequisites:** only the chosen goal's dependency chain blocks it. Other runnable branches remain eligible.
5. **End of round:** persist completion after all enabled tasks have achieved their nonempty goals. Restarting does not restart a completed round. Change goals before beginning another round.
6. **Unknown/expired:** an unavailable field, null list, or expired unfinished campaign is never treated as complete.

Explicit watch-only mode is available after the user checks game linkage and channel eligibility. It does not guarantee Drops or automatic completion. VOD playback does not count; eligible live broadcasts are required.

## Public announcements versus account progress

`catalogue/announcements.json` contains manually checked public notices with source URLs and verification timestamps. The included War Thunder WTCS 2026 first-week notice lists five rewards from the publisher's official announcement; the activity window was checked against the Twitch listing.

These records contain **no** personal watch time, claim state, account linkage, or Twitch reward IDs. They never enter automatic goal-completion logic. Administrators can add other games in the same format or configure `TDM_ANNOUNCEMENTS_FILE`.

Campaign discovery, campaign details, and personal inventory have separate capability handling. A null response is not an empty catalogue. Data confirmed earlier retains its original source time. The service does not switch client identities, generate integrity tokens, or work around verification requirements.

## Embed into an existing console

Run the same Docker backend with `TDM_HTTP_PORT=0` and mount the owner-only Unix socket into a trusted gateway. The gateway retains its existing login, administrator checks, Origin checks, and method/path allowlist. Do not expose that socket directly.

Export `ui/src/TwitchDrops.tsx` and its CSS with `scripts/export-ui.py`; pass `apiBase` and optional scheduler navigation props. Keep host login and navigation in a small wrapper. The standalone and embedded business components remain identical.

See [embedding instructions](docs/embedding.md), [API documentation](docs/api.md), and [OpenAPI](docs/openapi.json).

## Scheduling and agent integration

Standalone scheduling exposes real enable/disable state and execution timestamps through `/api/automation`. Metadata synchronization and the watch queue are separate controls.

If a host already uses cron, systemd, or a task registry, set `TDM_EXTERNAL_SCHEDULER=true`, retain exactly one native executor, and register/control that executor in the host's task console. The `deploy/` directory contains systemd examples. Disabling metadata synchronization does not stop viewing; use the queue's Pause action for that.

The copyable [agent integration prompt](docs/agent-prompt.md) covers deployment, existing-console integration, official human approval, capability verification, goal selection, scheduling, and secret handling. Agents must not infer reward progress or bypass platform checks.

## Configuration

| Variable | Default / purpose |
|---|---|
| `TDM_DATA_DIR` | `/data`; private state and standalone credential storage |
| `TDM_SOCKET` | `/run/twitch-drops/service.sock`; mode 0600 |
| `TDM_WEB_UI` | `true`; disable standalone pages while retaining APIs and background tasks |
| `TDM_HTTP_PORT` | `8080`; use `0` to disable standalone TCP/UI |
| `TDM_HTTP_HOST` | `0.0.0.0` inside the container; Compose binds the host port to loopback |
| `TDM_ADMIN_TOKEN` | Optional, at least 24 characters; otherwise generated in `/data/admin-token` |
| `TDM_PUBLIC_ORIGIN` | Optional full origin for an HTTPS reverse proxy |
| `TDM_PROXY_URL` | Optional explicit HTTP/HTTPS proxy; mutually exclusive with an embedded mihomo node |
| `TDM_PROXY_SOURCE` / `TDM_PROXY_NODE` | Optional original mihomo config and explicit node name |
| `TDM_MIHOMO` | `/usr/local/bin/mihomo`; mount your compatible binary read-only |
| `TDM_PROXY_LABEL` | Optional non-sensitive label displayed by the UI |
| `TDM_EXTERNAL_SCHEDULER` | `false`; use `true` for a single native scheduler |
| `TDM_EXTERNAL_CHECK_MIN_SECONDS` / `TDM_EXTERNAL_CHECK_MAX_SECONDS` | Displayed external schedule range, default 600 / 1200; must reflect the real native schedule |
| `TDM_ANNOUNCEMENTS_FILE` | Optional administrator-owned public announcement JSON |
| `TDM_PASSWORD_BOOK` | Optional YAML host adapter modifying only `twitch_drops.account`; otherwise `/data/credentials.json` |

The UI can configure the idle-check range. In standalone mode it also controls metadata synchronization; in external mode the native schedule remains authoritative.

## Account authorization and unavailable progress

The UI guides missing or expired accounts through official device authorization. A connected account with unreadable progress instead gets official inventory/game-linking links and clear guidance; no extra ordinary-viewer progress permission has been confirmed. Reauthorizing is not a guaranteed fix. See [authorization states](docs/authorization.md).

## Release downloads and Web UI choice

Release archives cover Linux amd64/arm64, Windows amd64/arm64, and macOS Intel/Apple Silicon, with SHA256SUMS. Verify the checksum before extracting. Running an archive needs no Python, Go, Node.js or Docker; consult release notes for actual platform validation status.

Interactive terminal launches ask whether to start the standalone Web UI. For unattended launches, choose explicitly:

```sh
./twitch-drops --web-ui=true
./twitch-drops --web-ui=false
```

Use `twitch-drops.exe` on Windows or set `TDM_WEB_UI=true/false`. Disabling the UI makes `/` and `/assets/*` return 404; authenticated APIs and background tasks continue. For an existing admin console, disable this standalone UI and reuse the embedded component. `TDM_HTTP_PORT=0` additionally disables TCP and requires a trusted Unix socket.

## Go executable and development

The Go backend embeds its UI, public notices and OpenAPI document. Running it needs no Python, Node.js or separate SQLite service. Building from source needs Go 1.26.5, Node.js 26 and pnpm 12.3.4:

```sh
sh scripts/build.sh
./dist/twitch-drops
# In another private terminal after first startup; do not paste the token into chat
cat data/admin-token
```

Native defaults: `127.0.0.1:8080`, private state in `./data`, Unix socket disabled. Set `TDM_DATA_DIR` for persistent storage; set `TDM_SOCKET` only for a trusted local gateway or native scheduler. A Windows-targeted build uses an `.exe` with no Unix socket. Cross compilation is verified; actual Windows/macOS runtime acceptance remains unverified. The optional embedded mihomo adapter needs Linux; other systems use direct access or an explicit HTTP/HTTPS proxy.

Routine verification: `go vet ./...`, `go build ./...`, UI typecheck/build, and bounded health/API probes. Browser interaction acceptance is manual unless requested. Documentation screenshots use an isolated demo instance.

`Dockerfile` builds UI and Go in separate build stages. After `sh scripts/build.sh`, use `docker build -f Dockerfile.runtime -t twitch-drops-nas:2.0.0 .` for the compact runtime image. Runtime images contain no Python, Node.js or Go compiler. Dependencies are pinned in `go.mod`, `go.sum` and the UI lockfile.

Reward cards reference the original official CDN HTTPS URLs directly. Browsers lazy-load the images; the NAS does not download, proxy, or rehost the originals. Public notices use `rewards[].imageUrl`; tracked rewards use Twitch-provided `benefit.imageAssetURL`. Missing or failed images keep a labeled placeholder and the reward text. Additional image domains require matching backend, UI, and host CSP allowlists.

## Images for future campaigns

Normal synchronization automatically parses Twitch `benefit.imageAssetURL` for every game. If images are missing, open **奖励图片** in the desktop UI, select the activity, paste exported Twitch page JSON or a reward-region HTML fragment, preview the matches, then confirm. Matching is scoped to one activity and uses names, registered aliases, or nearby labels. Unrelated entries, ambiguous names, invalid URLs, and conflicting URLs are listed explicitly. Correct the name in the JSON and preview again if needed.

Only original CDN URLs and provenance timestamps are persisted in SQLite. No source edit or redeployment is needed. Fresh official images take precedence over imported fallbacks. Imports never change watch time, goals, claims, or authorization. See [repeatable image ingestion and agent API](docs/reward-images.md).

## Privacy and license

The repository contains this independent service, its reusable UI, and public documentation. It excludes deployment credentials, private account identifiers, proxy nodes, NAS paths, databases, logs, and other backend source. The user-authorized inventory screenshot below only shows the campaign, rewards, progress and claim states. Twitch public Client IDs and GraphQL query hashes are interoperability identifiers, not a user's AK or secret.

This project is not affiliated with Twitch, Gaijin, or any game publisher. Reward eligibility, authorization capabilities, progress and fulfillment remain controlled by those platforms. See [NOTICE.md](NOTICE.md) for upstream attribution and [LICENSE](LICENSE) for the MIT license.

## Stream-aware query scheduling

The default auto mode uses verified advertised stream sessions and begins queries five minutes before the stream, including a check at its advertised start. Campaign eligibility dates are not used as stream times. Outside known windows, automatic channel discovery and metadata checks skip Twitch requests. Native wakeups remain local policy checks; manual sync is still available.

Each task can use Auto / Custom / Always. Custom entries accept explicit timezones through the API and Beijing time through the UI. Games without known sessions retain randomized low-frequency discovery. A one-hour late-start grace applies only while waiting to find a stream; a selected real stream continues until it actually ends or goals complete. Add a session or use Always for unannounced/very late broadcasts. The built-in 18 WTCS sessions cite Gaijin's announcement; operators may supply `TDM_STREAM_SCHEDULES_FILE`. See the API guide for states and provenance.

## Real-world verification

The following screenshot was supplied by a user on **2026-10-26** from Twitch's official Drops inventory, showing real progress and manual claims during use of this project:

- **Claimed:** 50% SL Booster and 50% RP Booster.
- **Still progressing:** 1 Day of Premium at 62%, Vampir camouflage at 25%, and the Bf 109 E-3 vehicle at 12%.

![Real Twitch inventory: two claimed boosters and ongoing progress for the remaining rewards](docs/images/twitch-drops-verification.png)

This is a real Twitch page, not an AI illustration or demo-data screen. Reward artwork, progress percentages and claim states are unchanged. It was checked for account names, email addresses, access tokens and private deployment addresses; none appear in the image.

This record verifies official watch-time accumulation and two manual claims for this War Thunder campaign. Other games, accounts and deployment environments require separate verification. Automatic stopping after an entire round remains untested in a live completion run.
