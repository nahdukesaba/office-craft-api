# Office-Craft API

Go and Fiber backend for SILAPET resource management. PostgreSQL stores business data, Central SSO owns identity, and this backend owns authorization, encrypted application sessions, and Supabase Storage access.

## Authentication architecture

- Direct login is submitted by the SILAPET frontend and verified with Central SSO server-to-server.
- Browser SSO uses application state, PKCE S256, a one-time authorization code, and Central SID introspection.
- Both flows terminate in the encrypted HttpOnly `silapet-session` cookie.
- Browser JavaScript never receives or stores Central access tokens, refresh tokens, or SID values.
- Protected routes decrypt the application session and revalidate the current SILAPET membership, role, and status.
- Central JWT verification uses asymmetric JWKS keys; the legacy shared HS256 secret is not used.

## Storage architecture

Resource photos and booking proofs are uploaded through this backend. `SUPABASE_SERVICE_ROLE_KEY` is server-only and is used for Storage uploads and signed URLs. Frontend code must not call Supabase Storage directly.

## Local setup

1. Copy `.env.example` to `.env`.
2. Configure `DATABASE_URL` and `SUPABASE_URL`.
3. Configure the Central SSO client, redirect URI, introspection secret, and Browser SSO flow key.
4. Generate a base64-encoded 32-byte `APP_SESSION_KEY`.
5. Configure the server-only Storage service-role key and bucket names.
6. Start the API:

```bash
go run ./cmd/server
```

The service runs pending migrations at startup and inserts sample business resources only when the resource table is empty. Identity and admin provisioning remain Central-owned.

Health check: `GET /api/health`.

## Authentication routes

| Route | Purpose |
|---|---|
| `POST /api/auth/login` | Direct login through Central SSO |
| `POST /api/auth/register` | Central-owned registration and SILAPET membership request |
| `GET /api/auth/me` | Reload the current HttpOnly application session |
| `POST /api/auth/refresh` | Refresh the server-held Central credentials and application session |
| `POST /api/auth/logout` | Clear the SILAPET session and report whether Central logout is required |
| `GET /api/auth/sso/start` | Start Browser SSO with state and PKCE |
| `POST /api/auth/sso/exchange` | Consume state and exchange the one-time Central code |
| `GET /api/auth/sso/logout` | Start coordinated Central logout |

`centralLogoutRequired` is true only for sessions created through Browser SSO. Direct-login sessions remain isolated from an unrelated Central browser session.

## Business API

All routes are under `/api`.

| Area | Routes |
|---|---|
| Resources | `GET/POST /resources`, `GET/PUT/DELETE /resources/:id` |
| Bookings | `GET/POST /bookings`, `GET /bookings/:id`, booking lifecycle actions |
| Proofs | `GET/POST /bookings/:bookingId/proofs` |
| Public | `GET /public/bookings/all`, `GET /public/bookings/resource/:resourceId` |
| Stats | `GET /stats/overview` |

Booking ownership is always derived from the trusted application session and is never accepted from request-body `userId` values.

## Verification

```bash
go test ./...
go vet ./...
go build ./...
```

## Deployment

Deployment scripts are in `scripts/`. See `scripts/how-to-deploy.md` for the maintained procedure. Production deployments must preserve the server-side `.env`, run migrations, replace the binary, restart the service, and verify `/api/health`.
