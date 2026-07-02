# Office-Craft API

Golang + Fiber REST API for the Office-Craft Resource Management System, backed by Supabase (Postgres + Auth + Storage). Built to match an existing TanStack Start React frontend exactly — every JSON response uses camelCase field names.

## Stack

- **Go 1.22 + Fiber v2** — HTTP framework
- **Supabase Postgres** — accessed directly via `pgx/v5` (not PostgREST), so the API layer owns all authorization logic
- **Supabase Auth (GoTrue)** — login/register call Supabase's REST API; the API validates the resulting JWT itself on every subsequent request
- **Supabase Storage** — the frontend uploads photos/proofs directly to Storage and passes the resulting `path` to this API for metadata storage
- **golang-migrate** — SQL migrations in `migrations/`, applied automatically on boot

> A note on the `replace (...)` block at the bottom of `go.mod`: it exists only because this project was scaffolded in a network-sandboxed container that can't reach `golang.org`/`gopkg.in`. It's safe to delete — run `go mod tidy` on a normal machine and Go will re-resolve everything from the real module proxy.

## 1. Supabase setup

1. Create a Supabase project.
2. Grab from **Project Settings → API**: `Project URL`, `anon public` key, `service_role` key (used only for auto-seeding the first admin — keep it out of the frontend).
3. Grab from **Project Settings → API → JWT Settings**: the JWT Secret (`SUPABASE_JWT_SECRET`). This backend validates tokens with HS256 using that shared secret. If your project uses the newer asymmetric (ECC/JWKS) signing keys instead, swap the `jwt.ParseWithClaims` keyfunc in `internal/middleware/auth.go` for a JWKS-based verifier.
4. Grab from **Project Settings → Database**: the Postgres connection string (`DATABASE_URL`). Use the direct connection or session pooler — this API connects as a normal Postgres role and does **not** rely on Supabase Row Level Security, since authorization is enforced entirely in Go.
5. In **Storage**, create two buckets: `resource-photos` and `booking-proofs` (names configurable via env vars). The frontend should upload directly to Storage with the Supabase JS client and send this API the resulting object `path`.

## 2. Configure

```bash
cp .env.example .env
# fill in DATABASE_URL, SUPABASE_URL, SUPABASE_ANON_KEY,
# SUPABASE_SERVICE_ROLE_KEY, SUPABASE_JWT_SECRET
```

## 3. Run locally

```bash
go mod tidy   # on a machine with normal internet access
go run ./cmd/server
```

On boot the service:
1. Runs all pending migrations in `migrations/`.
2. If `app_users` is empty, creates an initial admin (`SEED_ADMIN_EMAIL` / `SEED_ADMIN_PASSWORD`) via the Supabase Admin API — requires `SUPABASE_SERVICE_ROLE_KEY`.
3. If `resources` is empty, inserts a few sample rooms/cars/bikes.

Health check: `GET /api/health`.

## 4. API surface

All routes are under `/api`. See the original spec for the full contract; summary:

| Area | Routes |
|---|---|
| Auth | `POST /auth/login`, `POST /auth/register`, `GET /auth/me` |
| Resources | `GET/POST /resources`, `GET/PUT/DELETE /resources/:id` (write = admin only) |
| Bookings | `GET/POST /bookings`, `GET /bookings/:id`, `PUT /bookings/:id/{approve,reject,close,cancel}` |
| Proofs | `GET/POST /bookings/:bookingId/proofs` |
| Public | `GET /public/bookings/all`, `GET /public/bookings/resource/:resourceId` |
| Stats | `GET /stats/overview` (admin only) |

Business rules enforced in `internal/services/booking_service.go`:
- Start/end must land on 30-minute boundaries.
- Max duration 4 hours.
- Start must be in the future.
- No overlapping `pending`/`approved` booking on the same resource.
- `userId` on a booking is always derived from the JWT, never trusted from the request body.

## 5. Deploying to Windows 10 via NSSM

```powershell
# one-time, per machine
go build -o office-craft-api.exe ./cmd/server
mkdir C:\apps\office-craft
copy office-craft-api.exe C:\apps\office-craft\
xcopy /E /I migrations C:\apps\office-craft\migrations
copy .env C:\apps\office-craft\.env   # not committed to git

.\scripts\install-service.ps1 -InstallDir "C:\apps\office-craft" -NssmPath "C:\tools\nssm\win64"
```

`scripts/uninstall-service.ps1` stops and removes the service. `scripts/deploy-update.ps1` is what CI calls to hot-swap the binary (stop → copy → start).

## 6. CI/CD

`.github/workflows/deploy.yml` runs on a **self-hosted Windows runner** installed on (or reachable by) the target machine:

1. Checkout, `go build` a Windows binary.
2. Copy `migrations/` alongside the build output.
3. Run `scripts/deploy-update.ps1` to stop the NSSM service, replace the `.exe`, and restart it.
4. Hit `/api/health` to confirm the new build came up.

The runner needs local access to NSSM and the install directory; the `.env` file on the target machine is left untouched by deploys (only the binary + migrations are replaced).
