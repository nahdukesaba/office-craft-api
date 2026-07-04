# Office-Craft API — Backend Integration Reference

Use this document to connect the existing frontend to the Office-Craft Golang + Fiber backend. Do not invent endpoints or field names that aren't listed here — mirror them exactly.

## Base URL

```
https://ephsilalahi.tailf32e23.ts.net/api
```

All routes below are relative to this base. (This is a Tailscale-tunneled local dev server — swap for a production URL once one exists.)

## Auth model

- Auth is backed by Supabase Auth. The backend issues a **JWT** on login.
- Store the token and send it on every authenticated request:
  ```
  Authorization: Bearer <token>
  ```
- `GET /public/*` routes need no auth. Every other route requires the header above.
- Roles: `"user"` and `"admin"`. Admin-only routes are marked below.
- **Every new registration requires admin approval before it can do anything.** See below — this is not optional and the frontend needs a UI state for it.

### `POST /auth/login`
Body: `{ "email": string, "password": string }`
Response `200`: `{ "token": string, "user": AppUser }`
Response `403` if the account is `pending` (`ACCOUNT_PENDING_APPROVAL`) or `rejected` (`ACCOUNT_REJECTED`) — see AppUser.status below. **Show a distinct, friendly screen for these** ("Your account is awaiting admin approval" / "Your access request was declined") rather than a generic login-failed toast.

### `POST /auth/register`
Body: `{ "email": string, "password": string, "fullName": string }`
Response `201`: `{ "message": string, "user": AppUser }` — **no token is ever returned here**, regardless of Supabase's own email-confirmation setting. `user.status` will be `"pending"`. Show the user a "your account is awaiting admin approval" screen immediately after registering; do not attempt to auto-login.

### `GET /auth/me` (auth required)
Response `200`: `AppUser`

```ts
interface AppUser {
  id: string;
  email: string;
  fullName: string;
  role: "user" | "admin";
  status: "pending" | "approved" | "rejected";
  createdAt: string; // ISO 8601
  updatedAt: string;
}
```

---

## Users (admin: approve/reject registrations)

### `GET /users` (admin only)
Query params: `status` (optional — `pending` | `approved` | `rejected`; omit for all users).
Response `200`: `AppUser[]`

Build an admin "Pending Approvals" screen around `GET /users?status=pending` with Approve/Reject buttons per row.

### `PUT /users/:id/approve` (admin only)
Response `200`: `AppUser` (now `status: "approved"`)

### `PUT /users/:id/reject` (admin only)
Response `200`: `AppUser` (now `status: "rejected"`)

---

## Error contract (applies to every endpoint)

Every 4xx/5xx response has this exact shape:

```ts
interface ApiError {
  error: string;      // stable machine-readable code, e.g. "BOOKING_CONFLICT"
  message: string;    // human-readable, safe to show in a toast
  details?: unknown;  // present on some errors, see BOOKING_CONFLICT below
}
```

Common codes you'll see: `VALIDATION_ERROR`, `INVALID_BODY`, `INVALID_RANGE`, `INVALID_INTERVAL`, `TOO_LONG`, `PAST_BOOKING`, `RESOURCE_NOT_FOUND`, `RESOURCE_UNAVAILABLE`, `BOOKING_CONFLICT`, `BOOKING_NOT_FOUND`, `NOT_PENDING`, `INVALID_STATUS`, `NOT_START_DAY`, `PROOF_NOT_ALLOWED`, `PHOTO_REQUIRED`, `NOTIFY_NOT_ALLOWED`, `ACCOUNT_PENDING_APPROVAL`, `ACCOUNT_REJECTED`, `USER_NOT_FOUND`, `FORBIDDEN`, `UNAUTHORIZED`, `NOT_FOUND`, `INTERNAL_ERROR`.

Parse `error.response.data.error` to branch behavior (e.g. show a special "slot taken" dialog for `BOOKING_CONFLICT`, or the dedicated pending/rejected screens for `ACCOUNT_PENDING_APPROVAL`/`ACCOUNT_REJECTED`), and always fall back to showing `message` in a toast for anything else.

---

## Resources

`Resource` is a tagged union stored as one table; irrelevant fields are simply omitted.

```ts
interface Resource {
  id: string;
  type: "room" | "car" | "bike";
  name: string;
  description: string;
  location: string;
  photoUrl?: string | null;
  isAvailable: boolean;
  // room only:
  capacity?: number;
  amenities?: string[];
  // car/bike only:
  licensePlate?: string;
  seats?: number;
  fuelType?: string;
  createdAt: string;
  updatedAt: string;
}
```

### `GET /resources` (auth required)
Query params (all optional): `search` (string, matches name/description/location), `type` (`room`|`car`|`bike`|`all`), `availability` (`true`|`false`).
Response `200`: `Resource[]`

### `GET /resources/:id` (auth required)
Response `200`: `Resource`

### `POST /resources` (admin only)
Body: `Omit<Resource, "id"|"createdAt"|"updatedAt">` (send only the fields relevant to `type`).
Response `201`: `Resource`

### `PUT /resources/:id` (admin only)
Same body shape as create. Response `200`: `Resource`

### `DELETE /resources/:id` (admin only)
Response `204`, empty body. (Internally this is a soft delete — nothing for the frontend to change, the resource just stops appearing in list/get responses from this point on.)

---

## Bookings

### Lifecycle

```
pending → approved → in_use → finished
   ↓          ↓
rejected   cancelled (also reachable from in_use via revoke)
```

- Multiple `pending` bookings can exist on the same resource/time window simultaneously — the frontend should **not** treat a pending overlap as an error at creation time.
- A booking becomes exclusive only once it's `approved`. Approving one booking **auto-rejects every other pending booking that overlaps its window** — always read `autoRejectedIds` off the approve response and surface it (e.g. "3 other pending requests were auto-rejected").
- Starting a booking (`approved → in_use`) **requires a "before" proof to already be uploaded** — enforced server-side, not just in the UI. Don't let a "Start" button call the endpoint before the before-photo upload has succeeded.

```ts
interface Booking {
  id: string;
  resourceId: string;
  userId: string;
  startTime: string;   // ISO 8601
  endTime: string;     // ISO 8601
  date: string;        // "YYYY-MM-DD", Asia/Jakarta calendar date of startTime
  endDate: string;      // "YYYY-MM-DD", Asia/Jakarta calendar date of endTime
  status: "pending" | "approved" | "in_use" | "finished" | "rejected" | "cancelled";
  purpose: string;
  adminNotes: string;   // set on revoke / auto-reject, empty otherwise
  createdAt: string;
  updatedAt: string;
}

interface BookingWithDetails extends Booking {
  resource?: Resource;
  user?: AppUser;
}
```

### `GET /bookings` (auth required)
Query params: `status`, `resourceId`, `from` (ISO date/datetime), `to`, `page` (default 1), `pageSize` (default 20). Non-admins always get only their own bookings regardless of any `userId` param; admins can pass `userId` to filter by user.

Response `200`:
```ts
interface PaginatedBookings {
  data: BookingWithDetails[];
  page: number;
  pageSize: number;
  total: number;
  totalPages: number;
}
```

### `GET /bookings/:id` (auth required — owner or admin)
Response `200`: `BookingWithDetails`

### `POST /bookings` (auth required)
Body:
```ts
{ resourceId: string; startTime: string; endTime: string; purpose: string }
```
`userId` is never sent — the backend derives it from the JWT.

Validation (in order): end > start; both times on a 30-minute boundary; duration ≤ 4 hours; start time in the future; resource exists and `isAvailable`; no overlapping `approved`/`in_use`/`finished` booking on that resource.

Response `201`: `BookingWithDetails`

On conflict, response `409`:
```json
{
  "error": "BOOKING_CONFLICT",
  "message": "Slot already approved for another user",
  "details": {
    "id": "...",
    "userFullName": "...",
    "startTime": "...",
    "endTime": "...",
    "date": "...",
    "endDate": "..."
  }
}
```

### `PUT /bookings/:id/approve` (admin only)
Transitions `pending → approved`. Atomically re-checks for conflicts and auto-rejects overlapping pending bookings.

Response `200`:
```ts
interface ApproveBookingResponse {
  booking: BookingWithDetails;
  autoRejectedIds: string[];
}
```
Response `409` with `BOOKING_CONFLICT` (same shape as above) if another booking got approved first. Response `409` with `NOT_PENDING` if it's no longer pending.

### `PUT /bookings/:id/reject` (admin only)
Pending → rejected. Response `200`: `BookingWithDetails`

### `PUT /bookings/:id/revoke` (admin only)
Approved or in_use → cancelled. Body (optional): `{ "adminNotes"?: string, "reason"?: string }`. Response `200`: `BookingWithDetails` (its `adminNotes` field will start with `"Revoked by admin: "`).

### `PUT /bookings/:id/start` (owner or admin)
Approved → in_use. Requires **both**:
- today (Asia/Jakarta) falls within `[date, endDate]`, else `403 NOT_START_DAY`
- a `"before"` proof already uploaded for this booking, else `400 PHOTO_REQUIRED`

Response `200`: `BookingWithDetails`. **Disable/hide the Start button until the before-photo upload has completed** — the backend will reject the call outright otherwise, so don't rely on the UI ordering alone.

### `PUT /bookings/:id/finish` (owner or admin)
In_use → finished. Requires at least one `"after"` proof already uploaded for the booking, else `400 PHOTO_REQUIRED`. Response `200`: `BookingWithDetails`

### `PUT /bookings/:id/cancel` (owner or admin)
Pending or approved → cancelled. Response `200`: `BookingWithDetails`

### `POST /bookings/:id/notify` (owner or admin)
Only allowed when status is `approved`, `in_use`, or `finished`, else `400 NOTIFY_NOT_ALLOWED`. Currently logs server-side (no email/SMS wired up yet — safe to call, just won't deliver anything externally today). Response `200`: `{ "success": true, "booking": Booking }`

### `GET /bookings/:id/history` (owner or admin)
Returns the full audit trail for a booking — every status change plus every proof upload — merged into one chronological array. **Use this to render a booking detail timeline.**

```ts
type TimelineEntry =
  | {
      type: "status_change";
      timestamp: string;
      actorId?: string;
      actor?: AppUser;
      eventType: "created" | "approved" | "auto_rejected" | "rejected" | "started" | "finished" | "cancelled" | "revoked";
      fromStatus?: string;
      toStatus: string;
      notes?: string;
    }
  | {
      type: "proof_uploaded";
      timestamp: string;
      actorId?: string;
      actor?: AppUser;
      proofId: string;
      proofKind: "before" | "after";
      proofPath: string;
    };
```
Response `200`: `TimelineEntry[]`, sorted oldest → newest. Every entry has `type` as a discriminant — switch on it to pick the right renderer (a status pill for `status_change`, a thumbnail/link for `proof_uploaded`).

---

## Proofs (before/after photos)

Upload the file to Supabase Storage from the frontend directly, then POST the resulting path here — the backend only stores metadata.

```ts
interface BookingProof {
  id: string;
  bookingId: string;
  kind: "before" | "after";
  path: string;        // Supabase Storage object path
  uploadedBy: string;  // user id
  createdAt: string;
}
```

### `GET /bookings/:bookingId/proofs` (owner or admin)
Response `200`: `BookingProof[]`

### `POST /bookings/:bookingId/proofs` (owner or admin)
Body: `{ "kind": "before" | "after", "path": string }`

Gating rules (violating these returns `403 PROOF_NOT_ALLOWED`):
- `"before"`: booking status must be `approved` or `in_use`, **and** today (Asia/Jakarta) must be within `[date, endDate]` inclusive.
- `"after"`: booking status must be `in_use`, **and** today must be on or before `endDate`.

Response `201`: `BookingProof`

---

## Public (no auth) — calendar/availability views

### `GET /public/bookings/all`
### `GET /public/bookings/resource/:resourceId`

Both return only bookings with status in `pending`, `approved`, `in_use`, `finished` (never rejected/cancelled), with minimal fields — no user PII:

```ts
interface PublicBooking {
  id: string;
  resourceId: string;
  startTime: string;
  endTime: string;
  status: "pending" | "approved" | "in_use" | "finished";
}
```

---

## Stats

### `GET /stats/overview` (admin only)
Response `200`:
```ts
interface StatsOverview {
  totalResources: number;
  totalUsers: number;
  totalBookings: number;
  bookingsByStatus: Record<string, number>;
  pendingBookings: number;
  approvedBookings: number;
  completedBookings: number; // count of "finished" bookings
}
```

---

## Implementation notes for the frontend

1. **camelCase everywhere.** Every JSON field, in both directions, is camelCase — no `snake_case` anywhere in the API surface.
2. **Registration is a two-step flow now.** After `POST /auth/register`, there is no token and no auto-login — show an "awaiting admin approval" screen. Build the admin-side "Pending Approvals" screen around `GET /users?status=pending` + the approve/reject endpoints above. A `403 ACCOUNT_PENDING_APPROVAL` or `403 ACCOUNT_REJECTED` on login should route to dedicated screens, not a generic error toast.
3. **Dates:** `startTime`/`endTime` are full ISO 8601 timestamps; `date`/`endDate` are plain `YYYY-MM-DD` calendar dates already computed in Asia/Jakarta time — use them directly for "N days" labels and date-range gating instead of re-deriving from `startTime`.
4. **Don't block on pending overlaps client-side** when submitting a new booking — the backend now allows this. Only treat `approved`/`in_use`/`finished` as "taken" when rendering availability.
5. **After approving a booking, always check `autoRejectedIds`** and show a toast like "3 other request(s) were automatically rejected" when non-empty.
6. **Gate action buttons using `booking.status`**, not local assumptions:
    - "Notify user" → visible only for `approved`/`in_use`/`finished`.
    - "Mulai Pemakaian" (start) + before-photo uploader → visible only when status is `approved` and today is within `[date, endDate]`. The before-photo upload must complete (wait for its `201`) before enabling the Start button — the backend now rejects Start outright without it.
    - After-photo uploader + "Finish" button → visible only when status is `in_use`.
    - "Revoke" (admin) → visible only for `approved`/`in_use`.
7. **Booking detail screen should include a timeline** built from `GET /bookings/:id/history` — it's pre-merged and pre-sorted, no client-side merging of statuses and proofs needed.
8. **Health check:** `GET /health` (relative to the base URL above) → `{ "status": "ok" }`, unauthenticated. Good for a connectivity smoke test before wiring up real screens.