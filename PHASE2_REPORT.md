# LOVELACE Booking System — Phase 2 Completion Report

**Status:** Complete & Verified  
**Initial Public Property:** Tir Bach Campsite (Pembrokeshire, Wales)  
**Administrative Platform:** LOVELACE Booking System (`:8080`, Go `html/template`)  
**Public Booking Frontend:** Standalone Next.js / React / TypeScript (`apps/public-web/`, `:3000`)

---

## 1. Two-Application Architectural Separation

1. **Separate Frontends & Origins:**
   - **LOVELACE Administration Console** remains in [internal/webui/](file:///Users/petervine/personal-git/tir-bach/internal/webui/webui.go) (`127.0.0.1:8080`), keeping its existing authentication, session cookies (`tirbach_session`), CSRF protection, and RBAC intact.
   - **Public Direct Booking Website** lives independently in [apps/public-web/](file:///Users/petervine/personal-git/tir-bach/apps/public-web/package.json) (`127.0.0.1:3000`), branded for **Tir Bach Campsite** with subtle attribution *"Booking system powered by LOVELACE"* in the footer.
   - Zero shared frontend UI templates, CSS, session cookies, or direct PostgreSQL connectivity exist in `apps/public-web/` (verified via `docker inspect tir-bach-public-web`: no `DATABASE_URL`, no encryption key, no admin credentials).
2. **Isolated Public API Surface:**
   - Public endpoints are scoped cleanly under `/api/public/v1/*` in [internal/httpapi/public_handlers.go](file:///Users/petervine/personal-git/tir-bach/internal/httpapi/public_handlers.go), backed by [docs/openapi-public-v1.yaml](file:///Users/petervine/personal-git/tir-bach/docs/openapi-public-v1.yaml) and [docs/phase2-architecture.md](file:///Users/petervine/personal-git/tir-bach/docs/phase2-architecture.md).
   - Server-to-server BFF proxy routes under `apps/public-web/src/app/api/` attach `X-Lovelace-Public-Key` server-side when configured, enforce strict CORS origin checks, apply per-IP sliding rate limits ([internal/httpx/ratelimit.go](file:///Users/petervine/personal-git/tir-bach/internal/httpx/ratelimit.go)), and never read admin cookies.

---

## 2. Backend, PostgreSQL Schema & Payment Lifecycle

- **Additive Migration:** [migrations/0005_phase2.sql](file:///Users/petervine/personal-git/tir-bach/migrations/0005_phase2.sql) extends the PostgreSQL 17 schema (upgrading existing volumes cleanly under `lovelace_migrator` while preserving DML-only runtime isolation for `lovelace_app`):
  - Property public metadata & `property_booking_policies` (hold TTL, `FULL`/`PERCENTAGE`/`FIXED` deposit modes, balance due window, extra adult/child/infant charges, terms version, Stripe enablement).
  - Temporary exclusive holds (`booking_holds`) backed by `inventory_claims` (`claim_type = 'HOLD'`) enforced by PostgreSQL GiST exclusion (`excl_inventory_claims_no_overlap`).
  - Tamper-resistant quote snapshots (`booking_quotes`), payment tracking (`payment_attempts`, `payments`, `refunds`, `payment_webhook_events`, `payment_exceptions`), hashed guest lookup tokens (`confirmation_access_tokens`), and transactional confirmation emails (`email_outbox`).
- **Authoritative Domain & Service Logic:**
  - [internal/domain/public_booking.go](file:///Users/petervine/personal-git/tir-bach/internal/domain/public_booking.go), [internal/store/postgres/public_booking_repo.go](file:///Users/petervine/personal-git/tir-bach/internal/store/postgres/public_booking_repo.go), and [internal/service/public_booking_service.go](file:///Users/petervine/personal-git/tir-bach/internal/service/public_booking_service.go) handle live `DIRECT` availability checks across reservations, active holds, maintenance/owner blockouts, channel restrictions/allocations, scheduled release rules, stay min/max nights, and occupancy limits.
  - [internal/payments/stripe.go](file:///Users/petervine/personal-git/tir-bach/internal/payments/stripe.go) integrates Stripe Checkout Sessions + raw-body HMAC-SHA256 `Stripe-Signature` verification + idempotent webhook confirmation + automatic idempotent refunds when late payments land after hold expiry (`EXPIRED_HOLD_PAID`).
  - [internal/payments/sandbox.go](file:///Users/petervine/personal-git/tir-bach/internal/payments/sandbox.go) provides an explicit **non-production only** (`TIRBACH_STRIPE_SANDBOX=true`, rejected on boot if `TIRBACH_ENV=production`) checkout simulator that dispatches genuine signed webhooks for local QA and browser E2E testing.
  - [cmd/server/main.go](file:///Users/petervine/personal-git/tir-bach/cmd/server/main.go) runs 30-second background workers sweeping expired holds (`SweepExpiredHolds`) and retrying queued outbox emails (`DispatchPendingEmails` via [internal/mailer/mailer.go](file:///Users/petervine/personal-git/tir-bach/internal/mailer/mailer.go)).
- **Administrative Visibility:**
  - Admin endpoints (`GET/PUT /api/v1/properties/{id}/booking-policy`, `GET /api/v1/properties/{id}/payment-exceptions`, `GET /api/v1/properties/{id}/reservations/{id}/financials`) and LOVELACE Admin Console views ([property.html](file:///Users/petervine/personal-git/tir-bach/internal/webui/templates/property.html), [reservation_detail.html](file:///Users/petervine/personal-git/tir-bach/internal/webui/templates/reservation_detail.html)) expose booking policy controls, payment ledgers, itemized snapshots, and customer cancellation requests.

---

## 3. Public Frontend (`apps/public-web/`)

- **Pages Implemented:**
  - `/` ([src/app/page.tsx](file:///Users/petervine/personal-git/tir-bach/apps/public-web/src/app/page.tsx)): Tir Bach homepage with coastal hero, availability search widget, accommodation highlights, facilities, and arrivals/location details.
  - `/stays` ([src/app/stays/page.tsx](file:///Users/petervine/personal-git/tir-bach/apps/public-web/src/app/stays/page.tsx)) & `/stays/[code]` ([src/app/stays/[code]/page.tsx](file:///Users/petervine/personal-git/tir-bach/apps/public-web/src/app/stays/[code]/page.tsx)): Live availability discovery, category filtering, accommodation galleries, amenities, and booking CTA.
  - `/book` ([src/app/book/page.tsx](file:///Users/petervine/personal-git/tir-bach/apps/public-web/src/app/book/page.tsx)) & [BookingWizard.tsx](file:///Users/petervine/personal-git/tir-bach/apps/public-web/src/components/BookingWizard.tsx): Authoritative extras recalculation, guest details validation, temporary hold creation with countdown timer & hold release, and Checkout handoff.
  - `/checkout/sandbox` ([src/app/checkout/sandbox/page.tsx](file:///Users/petervine/personal-git/tir-bach/apps/public-web/src/app/checkout/sandbox/page.tsx)): Non-production Stripe sandbox checkout simulator.
  - `/booking/confirmation` ([src/app/booking/confirmation/page.tsx](file:///Users/petervine/personal-git/tir-bach/apps/public-web/src/app/booking/confirmation/page.tsx)), `/lookup`, `/terms`, `/privacy`: Confirmation & payment status receipt, token-protected booking lookup, and guest cancellation requests.

---

## 4. Verification Results Observed

1. **Go Build, Vet & Full Race-Enabled PostgreSQL 17 Suite** (`go build ./... && go vet ./... && TEST_DATABASE_URL=... go test -v -race -count=1 ./...`):
   - `internal/app`: **All 14 test suites passed** (0 failures), covering Phases 1–1.6.1 plus Phase 2 origin/secret isolation, availability/channel/release filtering, deposit & occupancy calculations, immutable quote snapshots, concurrent 2-goroutine last-unit hold race, Stripe webhook signature/replay/refunds, `EXPIRED_HOLD_PAID` reconciliation & auto-refund, outbox email retry after SMTP failure, and Admin cancellation visibility.
   - `internal/auth` and `internal/domain`: All unit tests passed.
2. **Public Frontend Unit Tests & Production Build** (`cd apps/public-web && npm test && npm run build`):
   - Vitest: 3/3 unit tests passed (`src/lib/format.test.ts`).
   - Next.js `15.3.6` standalone production build & strict TypeScript/lint check passed.
3. **Live Multi-Container Docker Compose Stack** (`docker compose -f docker-compose.dev.yml up -d --build`):
   - Verified `tir-bach-postgres`, `tir-bach-db-bootstrap`, `tir-bach-migrate` (`0005_phase2.sql`), `tir-bach-app` (`:8080`, `200 OK` on `/readyz` and `/login`), and `tir-bach-public-web` (`:3000`, `200 OK` on `/api/healthz` and `/`, `404` on `/api/v1/users`).
4. **Playwright Chromium Browser E2E Suite** (`npx playwright test --reporter=list`):
   - **3/3 passed (`2.4s`)** against live Docker containers: Tir Bach branding & admin UI absence check, mobile `390x844` zero-horizontal-overflow check, and complete end-to-end flow (availability search -> `/book` -> authoritative extras update -> temporary hold + countdown -> release hold -> re-hold -> sandbox Stripe webhook confirmation -> `PARTIALLY_PAID` deposit receipt -> guest cancellation request -> subsequent availability search excludes newly booked unique unit).
