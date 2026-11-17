# LOVELACE Booking System — REST API Reference (`/api/v1`)

All endpoints return JSON and authenticate via `tirbach_session` (`HttpOnly` cookie).  
All state-changing requests (`POST`, `PUT`, `PATCH`, `DELETE`) other than `/api/v1/auth/login` require header `X-CSRF-Token` matching the session's `csrf_token`.

Error responses share a standard envelope:
```json
{
  "error": {
    "code": "date_overlap",
    "message": "human-readable error description",
    "fields": { "optional_field": "validation detail" }
  }
}
```

## Health & Auth
- `GET /healthz` — Liveness probe (`200 OK`).
- `GET /readyz` — Readiness probe (`200 OK` / `503 Service Unavailable`) reporting `database`, `payments`, `email` (e.g. `configured` vs `unconfigured`), and `public_api` component states.
- Additional Phase 2.1 error codes: `409 stripe_disabled`, `409 active_checkout_exists`, `400 query_token_forbidden`, `429 rate_limited`.
- `POST /api/v1/auth/login` — Body `{"email","password"}`. Sets `tirbach_session` cookie; returns `user` + `csrf_token`.
- `POST /api/v1/auth/logout` — Revokes current session & clears cookie.
- `GET /api/v1/auth/me` — Returns current actor summary (`must_rotate_password`, `role`, `csrf_token`).
- `POST /api/v1/auth/change-password` — Body `{"current_password","new_password"}`. Clears `must_rotate_password`, revokes all prior sessions, and rotates session cookie + `csrf_token`.

> **Password Rotation Gate:** When `must_rotate_password=true`, all routes except `/api/v1/auth/me`, `/api/v1/auth/logout`, and `/api/v1/auth/change-password` return `403 password_rotation_required`.

## Property, Inventory & Rates (Phase 1 Core)
- `GET /api/v1/properties` / `GET /api/v1/properties/{propertyID}` / `PATCH /api/v1/properties/{propertyID}`
- `GET|POST /api/v1/properties/{propertyID}/unit-types`, `PATCH|DELETE /api/v1/properties/{propertyID}/unit-types/{id}`
- `GET|POST /api/v1/properties/{propertyID}/units`, `PATCH|DELETE /api/v1/properties/{propertyID}/units/{id}`
- `GET|POST /api/v1/properties/{propertyID}/extras`, `PATCH|DELETE /api/v1/properties/{propertyID}/extras/{id}`
- `GET|POST /api/v1/properties/{propertyID}/rate-plans`, `PATCH /api/v1/properties/{propertyID}/rate-plans/{id}`
- `GET|POST /api/v1/properties/{propertyID}/rate-rules`, `PATCH|DELETE /api/v1/properties/{propertyID}/rate-rules/{id}`
- `GET /api/v1/properties/{propertyID}/rate-preview?unit_type_id=...&rate_plan_id=...&start_date=YYYY-MM-DD&end_date=YYYY-MM-DD`
- Legacy single-unit availability block compatibility endpoints (backed by `blockout_groups` + `inventory_claims`):
  - `GET|POST /api/v1/properties/{propertyID}/availability-blocks`
  - `POST /api/v1/properties/{propertyID}/availability-blocks/{id}/release`

## Reservations & Booking Channels (Phase 1.1)
- `GET /api/v1/properties/{propertyID}/channels` — List booking channels (`?active_only=true`).
- `POST /api/v1/properties/{propertyID}/channels` — Add custom booking channel (`code`, `name`, `category`).
- `GET /api/v1/properties/{propertyID}/reservations` — Query reservations (`?status=...&channel_code=...&start_date=YYYY-MM-DD&end_date=YYYY-MM-DD`).
- `POST /api/v1/properties/{propertyID}/reservations` — Create manual multi-unit reservation (`guest_name`, `guest_email`, `guest_phone`, `channel_code`, `arrival_date`, `departure_date`, `adults`, `children`, `infants`, `status`, `internal_notes`, `unit_ids[]` or explicit per-unit `units: [{unit_id, adults, children, infants}]`). Enforces release rules, active unit status, per-unit/combined occupancy limits (`max_adults`, `max_children`, `max_infants`, `max_guests`), channel-aware restrictions/allocations, and unified `inventory_claims` GiST exclusion (`409 date_overlap` / `409 release_restricted` / `422 validation_failed`).
- `GET /api/v1/properties/{propertyID}/reservations/{id}` — Get reservation detail + assigned units.
- `PATCH /api/v1/properties/{propertyID}/reservations/{id}` — Amend guest details, dates, channel, party size, or units (optimistic concurrency `version`).
- `POST /api/v1/properties/{propertyID}/reservations/{id}/transition` — Body `{"status","reason","version"}`. Enforces allowed transitions (`INQUIRY`, `PENDING`, `CONFIRMED`, `CHECKED_IN`, `CHECKED_OUT`, `CANCELLED`, `NO_SHOW`). Cancelling or marking no-show releases claims atomically.

## Bulk Blockouts & Scheduled Availability Releases (Phase 1.1)
- `GET /api/v1/properties/{propertyID}/blockouts` — List blockout groups (`?include_released=true`).
- `POST /api/v1/properties/{propertyID}/blockouts/preview` — Dry-run preview returning `affected_units`, `conflicts`, and `can_commit`.
- `POST /api/v1/properties/{propertyID}/blockouts` — Atomic bulk commit for `UNIT`, `UNITS`, `UNIT_TYPE`, or `PROPERTY` scopes (`apply_to_future_units` optional). Rolls back entirely on any unit conflict (`409 date_overlap`).
- `GET /api/v1/properties/{propertyID}/blockouts/{id}` — Inspect blockout group + per-unit claims.
- `POST /api/v1/properties/{propertyID}/blockouts/{id}/release` — Body `{"unit_ids": []}` (empty releases all active claims in group; non-empty releases only selected units).
- `POST /api/v1/properties/{propertyID}/blockouts/{id}/reinstate` — Body `{"claim_ids": [], "unit_ids": []}` (explicitly reinstates manually released claims that do not overlap existing bookings/claims).
- `PATCH /api/v1/properties/{propertyID}/blockouts/{id}/dates` — Body `{"start_date","end_date","version"}`. Atomically amends dates while preserving `CONSUMED` reservation allocations and previously `RELEASED` segments, only activating net-new uncovered dates.
- `GET /api/v1/properties/{propertyID}/releases` — List release rules (`?include_cancelled=true`).
- `POST /api/v1/properties/{propertyID}/releases` — Create release rule (`scope_type`, `unit_id`, `unit_type_id`, `start_date`, `end_date`, `channel_code`, `release_at`, `notes`).
- `PATCH /api/v1/properties/{propertyID}/releases/{id}` — Update rule before release/cancellation (`version`).
- `POST /api/v1/properties/{propertyID}/releases/{id}/release-now` — Idempotent immediate manual release.
- `POST /api/v1/properties/{propertyID}/releases/{id}/cancel` — Cancel rule.

## Calendar & Channel Manager Configuration (Phase 1.1)
- `GET /api/v1/properties/{propertyID}/calendar` — Real-time booking calendar data (`?view=month|week|day&start_date=YYYY-MM-DD&unit_type_id=...&channel_code=...&status=...`).
- `GET /api/v1/properties/{propertyID}/availability-check?unit_id=...&start_date=YYYY-MM-DD&end_date=YYYY-MM-DD&channel_code=...` — Point availability + release hold evaluation.
- `GET /api/v1/properties/{propertyID}/integrations/channel-manager` — Registered provider definitions + sanitized configurations (`credential_hints` only).
- `PUT /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}` — Validate provider inputs, encrypt secret fields via AES-256-GCM, persist config (rejects `status=CONNECTED` with `422` because live adapters are Phase 4).
- `POST /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/status` — Toggle `DISABLED` / `CREDENTIALS_SAVED` / `AWAITING_AUTHORIZATION` (prohibits `CONNECTED`).
- `DELETE /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}` — Remove stored channel configuration and encrypted secrets.

## Users & Audit Logs
- `GET|POST /api/v1/users`, `PATCH /api/v1/users/{id}`
- `GET /api/v1/audit-logs?property_id=...&resource_type=...&limit=...`

## Phase 2 Direct Booking Policy & Financials (Admin)
- `GET|PUT /api/v1/properties/{propertyID}/booking-policy` — Inspect or update hold duration, deposit mode (`FULL`/`PERCENTAGE`/`FIXED`), balance due window, extra guest nightly charges, terms version, and Stripe enablement.
- `GET /api/v1/properties/{propertyID}/payment-exceptions` — Inspect payment reconciliation exceptions (`EXPIRED_HOLD_PAID`, `OVERLAP_ON_CONFIRM`, `AMOUNT_MISMATCH`) and automatic refund states.
- `GET /api/v1/properties/{propertyID}/reservations/{id}/financials` — Retrieve reservation totals, immutable quote snapshot (`booking_quotes`), and `payments` ledger rows.

> **Public Booking API (`/api/public/v1/*`):** Consumed exclusively by the separate Next.js public booking site (`apps/public-web/`). Completely isolated from `/api/v1/*` and admin cookies (maintenance records, work logs, attachments, contractors, and staff are never exposed publicly). See [docs/openapi-public-v1.yaml](openapi-public-v1.yaml).

## Phase 3 Milestone 1 — Operations Centre & Housekeeping (Admin)
Full JSON schemas are documented in [docs/openapi-admin-v1.yaml](openapi-admin-v1.yaml).

- `GET /api/v1/properties/{propertyID}/operations?date=YYYY-MM-DD&focus=all|front_desk|housekeeping|attention` — Operations Centre snapshot (arrivals/departures, pending & inquiry follow-ups, daily changeover board, attention items, and maintenance operational summary).
- `POST /api/v1/properties/{propertyID}/housekeeping/tasks` — Manually queue a unit changeover task (`CLEANING_REQUIRED`).
- `POST /api/v1/properties/{propertyID}/housekeeping/tasks/{taskID}/stage` — Advance or regress task stage (`GUEST_DEPARTING`, `CLEANING_REQUIRED`, `CLEANING_IN_PROGRESS`, `INSPECTION_REQUIRED`, `READY`).
- `POST /api/v1/properties/{propertyID}/housekeeping/tasks/{taskID}/assign` — Assign housekeeping task to an active admin user.

## Phase 3 Milestone 2 — Maintenance Management & Facilities (Admin)
- `GET /api/v1/properties/{propertyID}/operations/maintenance-summary` — Open counts, urgent/safety/overdue counts, inspection backlog, and affected confirmed bookings.
- `GET|POST /api/v1/properties/{propertyID}/maintenance` — Filterable maintenance request list and request creation (`MNT-YYYY-NNNN`).
- `GET|PATCH /api/v1/properties/{propertyID}/maintenance/{requestID}` — Full request detail (work entries, inspections, status history, attachments, inventory restrictions, overlapping confirmed bookings) and metadata updates.
- `POST /api/v1/properties/{propertyID}/maintenance/{requestID}/assign` — Assign internal staff member and/or external contractor (`ContractorID`).
- `POST /api/v1/properties/{propertyID}/maintenance/{requestID}/transition` — Validated workflow state transition (`REPORTED`, `TRIAGED`, `SCHEDULED`, `IN_PROGRESS`, `AWAITING_PARTS`, `ON_HOLD`, `COMPLETED`, `CANCELLED`, `CLOSED`).
- `POST /api/v1/properties/{propertyID}/maintenance/{requestID}/work-notes` — Append-only repair note / labour log entry.
- `POST /api/v1/properties/{propertyID}/maintenance/{requestID}/inspection` — Record repair inspection (`PASSED` or `FAILED`); completion/approval never auto-releases inventory restrictions.
- `POST /api/v1/properties/{propertyID}/maintenance/{requestID}/attachments` — Multipart upload (magic-byte validated PNG/JPEG/WEBP/GIF/PDF/TXT).
- `GET /api/v1/properties/{propertyID}/maintenance/attachments/{attachmentID}` — Authenticated download (`X-Content-Type-Options: nosniff`, `Cache-Control: no-store`, forced `attachment` for non-inline types).
- `POST /api/v1/properties/{propertyID}/maintenance/{requestID}/inventory-restriction` — Link/create explicit `MAINTENANCE` `blockout_groups` + `inventory_claims` (409 `date_overlap` if confirmed bookings intersect).
- `PATCH /api/v1/properties/{propertyID}/maintenance/{requestID}/inventory-restriction/{linkID}` — Resize active restriction date range without displacing confirmed reservations.
- `POST /api/v1/properties/{propertyID}/maintenance/{requestID}/release-restriction` — Explicit manual release of active unit/unit-type maintenance restriction (`restriction_id`).
- `GET|POST /api/v1/properties/{propertyID}/maintenance/categories` & `PATCH .../categories/{id}`
- `GET|POST /api/v1/properties/{propertyID}/maintenance/locations` & `PATCH .../locations/{id}`
- `GET|POST /api/v1/properties/{propertyID}/maintenance/assets` & `GET|PATCH .../assets/{id}`
- `GET|POST /api/v1/properties/{propertyID}/maintenance/contractors` & `PATCH .../contractors/{id}`
- `GET|POST /api/v1/properties/{propertyID}/maintenance/schedules`, `PATCH .../schedules/{id}`, `POST .../schedules/run` — Preventative maintenance schedules & idempotent due generation.

## Phase 5 — Reporting & Business Intelligence (Admin, Property-Scoped)
- `GET /api/v1/properties/{propertyID}/reports/overview?start_date=YYYY-MM-DD&end_date=YYYY-MM-DD&currency=GBP&horizon_days=30` — Combined KPI snapshot (`reports:read`; `owner_admin`, `manager`, `viewer`).
- `GET /api/v1/properties/{propertyID}/reports/occupancy` — Date-aware daily, monthly, unit-type, physical vs. closure/owner-block vs. sellable & accepted occupancy from `unit_status_history` (`reports:read`).
- `GET /api/v1/properties/{propertyID}/reports/revenue` — Strict per-currency stay-prorated booked revenue (accommodation, surcharge, extras, discount, tax, ADR, RevPAR using date-aware supply) and timezone-localized cash collections/persisted refunds (`refunds.occurred_at`, excluding undated legacy refunds with explicit completeness warnings) (`reports:read`).
- `GET /api/v1/properties/{propertyID}/reports/channels` — Channel bookings, unit-nights, booked/cash revenue, ADR, avg lead time, and Phase 4 unmapped/conflict completeness flags (`reports:read`).
- `GET /api/v1/properties/{propertyID}/reports/seasons?season=peak|shoulder|off_peak|custom&compare_start=...&compare_end=...` — Side-by-side seasonal comparison with baseline deltas (`reports:read`).
- `GET /api/v1/properties/{propertyID}/reports/forecast?as_of_date=YYYY-MM-DD&horizon_days=30` — Strictly read-only OTB vs. trailing-90d weekday model with uncertainty bounds and non-duplicating proportional multi-currency night allocation (`allocated_additional_nights`) (`reports:read`).
- `GET /api/v1/properties/{propertyID}/reports/retention` — Strictly aggregate repeat-guest counts across canonical `merged_into_guest_id` lineage without guest PII (`reports:read`).
- `GET /api/v1/properties/{propertyID}/reports/accounting-export?type=booking_line_items|payment_ledger|refund_ledger&format=csv|json` — Formula-safe repeatable accounting feed (`lovelace.accounting.v1`, audited as `report.exported`, `reports:export`; `owner_admin`, `manager` only).
