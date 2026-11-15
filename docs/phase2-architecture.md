# LOVELACE Booking System — Phase 2 Direct Booking Engine Architecture

**Milestone Status:** Step 2 Architecture Specification  
**Administrative Application:** Existing Go SSR Console + `/api/v1/*` (`LOVELACE Booking System`)  
**Public Application:** Standalone Next.js + React + TypeScript Frontend (`apps/public-web/`, branded per property e.g. `Tir Bach Campsite`)  
**Shared Authority:** Go Backend Services + PostgreSQL 17 (`inventory_claims` GiST Exclusions)

---

## 1. Two-Application Separation & Origin Boundaries

| Layer | Public Booking Application (`apps/public-web`) | Administrative Application (`internal/webui`) |
|---|---|---|
| **Brand Identity** | Property-specific (Tir Bach Campsite + subtle *"Booking system powered by LOVELACE"* footer) | Always `LOVELACE Booking System` |
| **Stack** | Next.js (App Router), React, TypeScript | Existing Go `html/template` SSR |
| **Deployment / Origin** | Standalone single container (`https://book.tirbach.wales`, local `http://127.0.0.1:3000`) | Standalone single Go server (`admin.<platform>`, local `http://127.0.0.1:8080`) |
| **API Surface Consumed** | Public-only `/api/public/v1/*` via Next.js server-side BFF (`X-Lovelace-Public-Key` / server-side fetch; browser never holds secrets) | Admin-only `/api/v1/*` protected by Argon2id session cookie + CSRF + RBAC |
| **Direct DB Access** | **Prohibited** (`apps/public-web` has no `DATABASE_URL` or pg driver) | Go backend via `lovelace_app` DML-only role |
| **Browser Cookies / Auth** | No admin cookies; confirmation lookup uses high-entropy signed/opaque retrieval token (`token` query/header) + booking reference | `tirbach_session` (`HttpOnly`, `SameSite=Lax`, `Secure` in prod) + CSRF token |

---

## 2. Public API Contract (`/api/public/v1/*`) & Webhook Endpoint

Public routes are mounted cleanly under `/api/public/v1/...` (separate middleware chain from admin auth, enforcing property lookup by slug/domain, strict input validation, public CORS allowlist, per-IP rate limiting, and zero leakage of internal notes/admin users/channel credentials):

- `GET /api/public/v1/properties/{slug}` — Public property profile, branding, booking policy (`hold_duration_minutes`, `deposit_mode`), terms/privacy URLs, cancellation policy.
- `GET /api/public/v1/accommodation?property={slug}` — Active, publicly bookable `unit_types` with photos, amenities, occupancy limits, stay rules, indicative from-rate, and available active extras.
- `GET /api/public/v1/accommodation/{code}?property={slug}` — Public unit-type detail + gallery + extras (never exposes internal unit UUIDs or maintenance/owner block explanations).
- `POST /api/public/v1/availability` — Authoritative Go search: validates dates (`[arrival, departure)` in property timezone), party occupancy, active unit status, `inventory_claims` (`RESERVATION`, `PENDING_RESERVATION`, `HOLD`, closures, `CHANNEL_ALLOCATION` / `CHANNEL_RESTRICTION` for `DIRECT`), and `availability_release_rules` for `DIRECT`. Returns bookable accommodation types & anonymous availability counts.
- `POST /api/public/v1/pricing` — Authoritative Go quote engine: resolves `rate_plans`, `rate_rules` (weekend/seasonal priority), extra adult/child/infant charges, extras, taxes/VAT bps, deposit due (`FULL`/`PERCENTAGE`/`FIXED`), balance due, and persists a tamper-proof `booking_quotes` snapshot.
- `POST /api/public/v1/booking/holds` — Atomically selects an eligible unit inside a DB transaction, validates release/occupancy rules, creates a `booking_holds` record + `HOLD` row in `inventory_claims` (enforced by PostgreSQL GiST `excl_inventory_claims_no_overlap`) and attaches a fresh immutable `booking_quotes` snapshot.
- `POST /api/public/v1/checkout` — Validates active non-expired hold + terms acceptance, creates an idempotent `payment_attempts` row and a Stripe Checkout Session (server-side secret key), returning `checkout_url`, `payment_attempt_id`, and `hold_expires_at`.
- `POST /api/public/v1/webhooks/stripe` — Raw-body Stripe `Stripe-Signature` HMAC-SHA256 verification, deduplicated via `payment_webhook_events(event_id)`. Drives state transitions idempotently.
- `POST /api/public/v1/booking/holds/{id}/cancel` — Explicitly releases an abandoned/cancelled hold (`booking_holds.status='CANCELLED'`, `inventory_claims.status='RELEASED'`).
- `GET /api/public/v1/booking-confirmation?ref=TB-...&token=...` — Verifies constant-time hashed `confirmation_access_tokens` (or retrieves hold status while webhook settles) and returns booking reference, stay details, itemized price snapshot, and payment history.
- `POST /api/public/v1/booking-confirmation/cancel-request?ref=TB-...&token=...` — Allows verified customer to submit a cancellation request (auto-cancels eligible unpaid/policy-compliant direct bookings or flags cancellation request in admin queue).

---

## 3. Data Flows, Booking & Hold Lifecycle

```text
Customer Browser (apps/public-web :3000)
   │ 1. Search dates + party
   ▼
Next.js BFF (/api/* or Server Actions/RSC) ──(No DB, no admin cookie)──► Go Backend (/api/public/v1/*)
   │                                                                         │
   │ 2. Price Quote -> Hold -> Checkout                                      ├─ Availability & Rate Engine
   ▼                                                                         ├─ PG TX: INSERT booking_holds + inventory_claims(HOLD) [GiST!]
Stripe Checkout Hosted Page                                                  ├─ Create Stripe Checkout Session (deposit/full amount)
   │ 3. Card payment + asynchronous webhook                                  │
   ▼                                                                         ▼
POST /api/public/v1/webhooks/stripe ──► Verify HMAC Sig & Dedupe event_id ──► PG TX:
   ├─ Hold ACTIVE & unexpired ─► Convert HOLD claim ─► RESERVATION (`CONFIRMED`, source=`DIRECT`),
   │                              insert `payments`, `confirmation_access_tokens`, `email_outbox`, `audit_logs`
   └─ Hold EXPIRED / overlap  ─► Insert `payment_exceptions` (`EXPIRED_HOLD_PAID`) + trigger/flag auto-refund (never overbooks!)
```

### Stripe Integration Choice: Hosted Stripe Checkout Sessions
- **Choice:** Stripe Checkout Sessions (`mode=payment`) initiated strictly from the Go backend, paired with signed webhook processing (`/api/public/v1/webhooks/stripe`).
- **Why:** Zero raw card handling in either Next.js or Go, full SCA / 3DS / Apple Pay / Google Pay / UK GBP support out-of-the-box, clean return (`success_url` -> `/booking/confirmation?ref=...&token=...&session_id=...`) where confirmation displays "verifying payment" until webhook verification completes, and full test-mode/mock verification support.

---

## 4. Temporary Hold Expiry & Exception Reconciliation

1. **Background Sweep (`cmd/server` ticker every 30s + pre-hold expiry cleanup):**
   Transitions `booking_holds WHERE status = 'ACTIVE' AND expires_at <= NOW()` to `EXPIRED` and marks linked `inventory_claims` as `RELEASED` transactionally.
2. **Late / Post-Expiry Webhook:**
   If `checkout.session.completed` arrives after hold expiry:
   - The Go handler locks the hold row `FOR UPDATE`, attempts to validate unit availability / claim conversion, and if already expired or re-booked:
   - Never creates an overlapping `CONFIRMED` reservation.
   - Records `payment_attempts.status = 'RECONCILIATION_REQUIRED'`, writes a high-visibility `payment_exceptions` row (`EXPIRED_HOLD_PAID`), attempts idempotent automatic refund (if configured/online) or queues manual refund in LOVELACE Admin, and writes an `audit_logs` entry.
3. **Out-of-Order & Duplicate Webhooks:**
   `payment_webhook_events(provider, event_id)` unique index guarantees idempotency; terminal `CONFIRMED` + `PAID` states ignore late duplicate/stale failure notifications safely.

---

## 5. Email Confirmation & Retries

- `email_outbox` stores Tir Bach–branded text + HTML receipts (with subtle *"Booking system powered by LOVELACE"* footer) in the same DB transaction as reservation confirmation.
- A background Go outbox dispatcher delivers pending emails (`smtp` or configurable local/log sink in dev/test) with exponential retry backoff (`attempts`, `next_attempt_at`, `last_error`). Email delivery failures never roll back or invalidate confirmed bookings.

---

## 6. Deployment & Docker Compose Topology

- `postgres` (`postgres:17-alpine`) + `db-bootstrap` (`ensure-roles.sh`) + `migrate` (`0001`..`0007` under `lovelace_migrator`)
- `app` (single Go backend + LOVELACE Admin UI on `:8080`, using `lovelace_app` DML-only role)
- `public-web` (single Next.js standalone container on `:3000`, talking ONLY to Go backend over internal `http://app:8080/api/public/v1/...`, zero `DATABASE_URL`; BFF helper protected by `import "server-only"`)
- **Single-Instance Rate Limiting Note:** Both Go and Next.js rate limiters use in-memory, process-local counters tailored for this initial single-instance topology. Before scaling horizontally across multiple `app` or `public-web` replicas, migrate rate-limit counters to a shared backing store (such as Redis).
