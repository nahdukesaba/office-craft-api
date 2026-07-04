# WhatsApp Notifications via OpenWA — Setup Guide

This sets up [OpenWA](https://github.com/rmyndharis/OpenWA), a self-hosted WhatsApp API gateway, on the same Windows 10 PC that runs the Office-Craft API. Office-Craft's backend will call OpenWA's HTTP API to send booking notifications; OpenWA handles the actual WhatsApp connection.

## Why `baileys`, not `whatsapp-web.js`

OpenWA supports two engines:

| Engine | How it works | RAM footprint |
|---|---|---|
| `whatsapp-web.js` | Runs a full headless Chromium browser | 300–500MB+, sometimes more |
| `baileys` | Talks to WhatsApp directly over a WebSocket, no browser | ~50–100MB |

Your machine has 2GB total, already running Postgres connections and the Go backend. **This guide uses `baileys`** — running Chromium alongside everything else risks the whole box swapping/thrashing. The tradeoff: `baileys` is a reverse-engineered protocol client rather than an automated real browser, so it's slightly more exposed to WhatsApp changing things unexpectedly — acceptable for internal booking reminders, not something I'd bet a customer-facing product on without a fallback plan.

## 1. Prerequisites

- **Node.js 20 or 22 LTS** — download from [nodejs.org](https://nodejs.org/) (the Windows Installer `.msi`, pick LTS). Verify after install:
  ```powershell
  node -v
  npm -v
  ```
- **Git for Windows** (or just download the repo as a ZIP from GitHub if you'd rather not install git) — [git-scm.com](https://git-scm.com/download/win)
- A WhatsApp number to dedicate to this bot (a spare SIM or a number you don't mind linking as a "linked device" — WhatsApp allows this without it being your primary phone's only session, but that phone does need to stay powered on and connected to the internet periodically, similar to WhatsApp Web).

## 2. Get the code

```powershell
cd C:\apps
git clone https://github.com/rmyndharis/OpenWA.git
cd OpenWA
npm install
```

`npm install` also pulls in the dashboard's dependencies (there's a `postinstall` step for that) — this can take a few minutes and a few hundred MB of disk, that's normal.

## 3. Configure for a low-resource box

Copy the minimal env template and edit it:

```powershell
copy .env.minimal .env
notepad .env
```

Set (or confirm) these values in `.env`:

```ini
NODE_ENV=production
PORT=2785

# Lightest possible engine - no Chromium
ENGINE_TYPE=baileys

# Zero-config database and storage - no separate Postgres/MySQL needed
DATABASE_TYPE=sqlite
DATABASE_NAME=./data/openwa.sqlite
DATABASE_SYNCHRONIZE=true

STORAGE_TYPE=local
STORAGE_LOCAL_PATH=./data/media

# Skip Redis/queue entirely - not needed for a single-session, low-volume bot
REDIS_ENABLED=false
QUEUE_ENABLED=false

SESSION_DATA_PATH=./data/sessions

# Pin your own API key rather than relying on the random generated one, so
# the Go backend's config doesn't have to change if OpenWA restarts.
API_MASTER_KEY=CHANGE-THIS-TO-A-LONG-RANDOM-STRING

# Optional but recommended: turn off the Swagger UI in production
ENABLE_SWAGGER=false
```

Generate a random value for `API_MASTER_KEY` (any long random string works — e.g. run `node -e "console.log(require('crypto').randomBytes(32).toString('hex'))"` in the OpenWA folder and paste the output in).

Create the data folders it expects:

```powershell
mkdir data\sessions
mkdir data\media
```

## 4. Build for production

Dev mode (`npm run dev`) also starts a separate Vite dashboard dev server — unnecessary overhead for a background service. Build once and run the compiled output instead:

```powershell
npm run build
npm run dashboard:build
```

This produces `dist/` (the API) with the dashboard bundled in and served on the same port.

## 5. First run — link your WhatsApp number

Before installing it as a background service, run it once in the foreground so you can scan the QR code:

```powershell
npm run start:prod
```

You should see it listening on `http://localhost:2785`. Now, from another PowerShell window (or Postman, or your browser for the GET), walk through session setup:

```powershell
$headers = @{ "X-API-Key" = "CHANGE-THIS-TO-A-LONG-RANDOM-STRING"; "Content-Type" = "application/json" }

# Create a session
Invoke-RestMethod -Uri "http://localhost:2785/api/sessions" -Method Post -Headers $headers -Body '{"name":"office-craft"}'
# note the "id" field in the response - that's your sessionId

# Start it
Invoke-RestMethod -Uri "http://localhost:2785/api/sessions/<sessionId>/start" -Method Post -Headers $headers

# Get the QR code (base64 PNG)
Invoke-RestMethod -Uri "http://localhost:2785/api/sessions/<sessionId>/qr" -Headers $headers
```

The QR endpoint returns base64 image data. Easiest way to actually see it: open `http://localhost:2785` in a browser — the bundled dashboard shows the QR code visually and updates live once you scan it. Log in there with your `API_MASTER_KEY`, find your session, and scan the QR with **WhatsApp → Settings → Linked Devices → Link a Device** on the dedicated number's phone.

Once scanned, the session status flips to "connected" and stays that way (it reconnects automatically on restart, using the saved session in `data/sessions`) — you won't need to re-scan unless you explicitly log the device out from the phone.

Send a test message to confirm it works:

```powershell
Invoke-RestMethod -Uri "http://localhost:2785/api/sessions/<sessionId>/messages/send-text" -Method Post -Headers $headers -Body '{"chatId": "62812xxxxxxxx@c.us", "text": "Test from OpenWA"}'
```

`chatId` is the recipient's number in international format (no `+`, no leading `0`) with `@c.us` appended, e.g. an Indonesian `0812-3456-7890` becomes `6281234567890@c.us`.

Once that test message arrives, stop the foreground process (`Ctrl+C`) and move to installing it as a real service.

## 6. Install as a Windows service (NSSM)

Same tool, same pattern as the Office-Craft backend. If you haven't already got NSSM from setting that up, grab it from [nssm.cc](https://nssm.cc/).

Run `scripts/install-openwa-service.ps1` (below) as Administrator:

```powershell
.\install-openwa-service.ps1 -InstallDir "C:\apps\OpenWA" -NssmPath "C:\tools\nssm\win64"
```

This registers OpenWA as `OpenWABackend`, set to auto-start, logging to `C:\apps\OpenWA\logs\`.

## 7. Point the Office-Craft backend at it

You'll need two values in the Go backend's `.env` (wired up in the next step of this integration):

```ini
OPENWA_BASE_URL=http://localhost:2785
OPENWA_API_KEY=CHANGE-THIS-TO-A-LONG-RANDOM-STRING
OPENWA_SESSION_ID=<the sessionId from step 5>
```

## Resource-usage notes for your specific box

- Expect OpenWA (baileys engine, idle) to sit around 60–120MB RAM. Check with Task Manager after it's been running a while — if it's climbing well past that, something's wrong (likely a session stuck reconnecting in a loop; check `logs\stderr.log`).
- `DATABASE_SYNCHRONIZE=true` is fine for a single-instance, low-traffic setup like this — it just means the SQLite schema self-manages, no migration step needed.
- If disk space ever gets tight (500GB should be nowhere close, but just in case), the biggest growth item over time is `data/media` if OpenWA ever receives media messages — you're only sending, so this should stay small.
- Restart the `OpenWABackend` service (not just the Go backend) if WhatsApp messages stop delivering after a long uptime — WebSocket-based connections like Baileys can occasionally need a nudge after ISP-level network blips.

## Next steps

This guide only covers getting OpenWA running and able to send a manual test message. Wiring actual booking-status notifications (approved / in_use / finished, with the "asset care" reminders you asked for) into the Go backend's `/notify` endpoint is a separate change to `internal/services/notify_service.go` — coming in the next message so this one doesn't get overloaded.