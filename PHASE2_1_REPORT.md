# LOVELACE Booking System — Phase 2.1 Production Hardening & Launch Readiness Report

## Overall Verification Status

| Area | Requirement | Status | Evidence |
| --- | --- | --- | --- |
| 1 | Stripe Checkout Return URL Security | **PASS** | `internal/payments/urls.go`, `internal/payments/sandbox.go`, `internal/httpapi/public_handlers.go`, `apps/public-web/src/lib/format.ts`, `TestPhase21CheckoutReturnURLSecurityUnitAndAPI`, Playwright `e2e/booking.spec.ts` |
| 2 | Enforce `stripe_enabled` Property Settings | **PASS** | `internal/service/public_booking_service.go` (`ErrStripeDisabled` -> `409 stripe_disabled`), `apps/public-web/src/components/BookingWizard.tsx`, `TestPhase21StripeEnabledEnforcementAndFailClosedProductionReadiness` |
| 3 | Strict Fail-Closed Production Stripe Config & Readiness | **PASS** | `internal/config/config.go`, `internal/service/service.go` (`ValidateProductionStartup`, `Readiness`), `cmd/server/main.go`, `internal/httpapi/api.go` (`/readyz`), `TestPhase21StripeEnabledEnforcementAndFailClosedProductionReadiness` |
| 4 | Single Active Payment Attempt per Hold & Concurrency | **PASS** | `migrations/0006_phase2_1.sql` (`uq_payment_attempts_one_active_per_hold`), `internal/store/postgres/public_booking_repo.go` (hold `FOR UPDATE`, bounded retries, `EXPIRED` sweep), `TestPhase21SingleActiveAttemptConcurrencyAndRetry` |
| 5 | Email Outbox Fail-Closed Delivery & Claim Locking | **PASS** | `internal/mailer/errors.go`, `internal/service/service.go` (`nil` when unconfigured), `internal/store/postgres/public_booking_repo.go` (`ClaimDueOutboxEmails` with `FOR UPDATE SKIP LOCKED`, permanent failure handling), `/readyz`, `TestPhase21EmailOutboxFailClosedAndPermanentFailure` |
| 6 | Rate Limiting, Trusted Proxies & BFF Isolation | **PASS** | `internal/httpx/middleware.go` (`ExtractClientIP`, `ResolveForwardedCustomerIP`), `internal/httpapi/public_handlers.go`, `apps/public-web/src/lib/rate-limit.ts`, `docker-compose.yml`, `TestPhase21TrustedProxyClientIPAndPublicSecretUnit`, Vitest `rate-limit.test.ts`, live `curl` `401` probe |
| 7 | Next.js / React / Vitest Security Dependency Upgrade | **PASS** | Upgraded `apps/public-web/package.json` (`next@16.4.6`, `react@19.3.1`, `react-dom@19.3.1`, `vitest@^5.1.0`), `npm audit` (`found 0 vulnerabilities`) |
| 8 | Confirmation Bearer Token Hygiene & Lookup Protection | **PASS** | `migrations/0006_phase2_1.sql`, `internal/store/postgres/public_booking_repo.go` (256-bit tokens, `RotateConfirmationToken`), `internal/httpapi/public_handlers.go` (`400 query_token_forbidden`, `no-store`/`no-referrer`), `apps/public-web/src/app/lookup/page.tsx`, `ConfirmationViewClient.tsx`, `next.config.ts`, `TestPhase21ConfirmationBearerTokensAndRotation`, Playwright `e2e/booking.spec.ts` |
| 9 | Property Content Audit, Configurable Policies & Sign-Off Checklist | **PASS** | `migrations/0006_phase2_1.sql`, `internal/webui/templates/property.html`, `internal/webui/handlers.go`, `apps/public-web/src/app/page.tsx`, `terms/page.tsx`, `privacy/page.tsx`, `SiteChrome.tsx`, `docs/launch-readiness-checklist.md` |
| 10 | Live External Stripe Account Test Checkout | **BLOCKED (No Credentials)** | Sandbox signed HMAC webhook replay & fake provider tests PASS end-to-end; live remote `api.stripe.com` test-mode card transaction requires real `sk_test_`/`whsec_` credentials at operator launch (`docs/launch-readiness-checklist.md`). |
| 11 | Documentation & Compose Production Defaults | **PASS** | Updated `docker-compose.yml`, `docker-compose.dev.yml`, `.env.example`, `apps/public-web/.env.example`, `docs/operations.md`, `docs/security.md`, `docs/api.md`, `docs/openapi-public-v1.yaml` |

---

## Summary of Hardening Implemented

### 1. Stripe Return URL & Sandbox Redirect Security
- Removed `success_url` and `cancel_url` from `service.CreateCheckoutRequest`, `handlePublicInitiateCheckout`, `apps/public-web/src/lib/lovelace-api.ts`, and `/api/public/booking/checkout`. Both Go and Next.js BFF explicitly reject client-supplied redirect parameters (`422 validation_failed`).
- Added `internal/payments/urls.go` (`ValidateCheckoutReturnURL`) enforcing strict origin/domain allowlists, HTTPS in production, and blocking protocol-relative URLs, userinfo injection, backslashes, and subdomain suffix spoofs.
- Hardened `internal/payments/sandbox.go`, `src/lib/format.ts` (`buildSafeSandboxRedirect`), `src/components/SandboxCheckoutClient.tsx`, and `src/app/checkout/sandbox/page.tsx` so sandbox checkouts never accept arbitrary query redirect URLs and are disabled (`404`) in production when `LOVELACE_STRIPE_SANDBOX` is off.

### 2. Fail-Closed Stripe Policy & Production Startup Readiness
- `InitiatePublicCheckout` checks `property_booking_policies.stripe_enabled` and refuses initiation with `409 stripe_disabled` (`domain.ErrStripeDisabled`) when disabled; `BookingWizard.tsx` renders an unavailable-payment notice when `!property.policy.stripe_enabled`.
- `ValidateProductionStartup` (called on boot in `cmd/server/main.go`) and `/readyz` (`internal/httpapi/api.go`) fail closed in production if `TIRBACH_STRIPE_SANDBOX=true`, if `TIRBACH_PUBLIC_API_SECRET` is empty, if `TIRBACH_PUBLIC_ORIGIN` lacks valid HTTPS origins, or if any active public property has `stripe_enabled=true` without valid paired Stripe keys/webhook secret. When all properties disable Stripe (`stripe_enabled=false`), startup and `/readyz` succeed cleanly (`payments.status = "disabled"`).

### 3. Single Active Payment Attempt & Hold Lifecycle Atomicity
- Added PostgreSQL partial unique index `uq_payment_attempts_one_active_per_hold` in `migrations/0006_phase2_1.sql`.
- `CreateOrGetPaymentAttempt` (`internal/store/postgres/public_booking_repo.go`) locks the hold row (`FOR UPDATE`), reuses exact idempotency-key replays, returns `domain.ErrActiveCheckoutExists` (`409 active_checkout_exists`) when an active attempt already exists under another key, allows up to 3 retries after `FAILED`/`EXPIRED` attempts, and transitions open attempts to `EXPIRED` when holds expire.

### 4. Email Outbox Integrity (No Silent `LogSender` False Successes)
- `service.New` no longer wires `mailer.LogSender{}` when `TIRBACH_SMTP_ADDR` is omitted (`emailSender = nil`).
- Unconfigured `DispatchPendingEmails` leaves queued emails in `PENDING` (`ErrProviderUnconfigured`), surfaced as `"email": {"status": "unconfigured"}` in `/readyz`.
- Added atomic `FOR UPDATE SKIP LOCKED` claiming (`ClaimDueOutboxEmails`), stale lock recovery (>5 min), duplicate queuing protection (`uq_email_outbox_reservation_template`), and immediate `FAILED` transition for permanent SMTP 5xx / `mailer.Permanent` errors (`internal/mailer/errors.go`).

### 5. Rate Limiting, Trusted Proxies & Public API Isolation
- Updated `httpx.ExtractClientIP` / `ResolveForwardedCustomerIP` and `apps/public-web/src/lib/rate-limit.ts` to validate peer IPs against explicit CIDR allowlists (`TIRBACH_TRUSTED_PROXIES`, `LOVELACE_TRUSTED_PROXIES`) and walk `X-Forwarded-For` right-to-left.
- Isolated rate-limiters per `httpapi.Handler` instance with both per-client and global ceilings across Go `/api/public/v1/*` and Next.js `/api/public/*`.
- Enforced `TIRBACH_PUBLIC_API_SECRET` (`X-Lovelace-Public-Key`) in production `docker-compose.yml` and startup checks.

### 6. Next.js / React Security Upgrade & Confirmation Token Hygiene
- Upgraded `apps/public-web` dependencies to `next@16.4.6`, `react@19.3.1`, `react-dom@19.3.1`, `vitest@^5.1.0`, eliminating all 6 advisories (`npm audit`: `found 0 vulnerabilities`).
- Upgraded confirmation tokens to 256-bit CSPRNG, added `revoked_at` / `last_used_at` and token rotation (`POST /api/public/v1/booking-confirmation/rotate-token`), rejected `?token=` query strings on `GET /api/public/v1/booking-confirmation` (`400 query_token_forbidden`), moved frontend lookups to `POST /api/public/booking/confirmation` + `Authorization: Bearer`, scrubbed legacy `?token=` via `history.replaceState`, and enforced `Cache-Control: no-store`, `Referrer-Policy: no-referrer`, and `X-Robots-Tag: noindex, nofollow`.

### 7. Property Content Audit & Owner Launch Sign-Off
- Added configurable policy fields (`quiet_hours`, `fire_policy`, `pet_policy`, `privacy_notice`) across schema, Admin Console (`/property`), and Public API.
- Removed hardcoded unverified phone numbers (`+44 1437 555 019`) and unverified site/facility claims from React components (`page.tsx`, `SiteChrome.tsx`, `terms/page.tsx`, `privacy/page.tsx`) and added owner sign-off verification in `docs/launch-readiness-checklist.md`.

---

## Executed Test & Command Verification (Post-Final Edit)

1. **`gofmt -l internal cmd && go build ./... && go vet ./...`**: exited `0` (zero formatting issues, clean compile & vet).
2. **`TEST_DATABASE_URL="postgres://tir_bach_test_runner:...@127.0.0.1:5432/tir_bach_test?sslmode=disable" go test -race -count=1 ./...`**: exited `0`; `internal/app` (`39.334s`, covering Phases 1, 1.1, 1.5, 1.6, 2, and 2.1), `internal/auth` (`4.216s`), and `internal/domain` (`1.360s`) all passed against PostgreSQL with race detector enabled.
3. **`cd apps/public-web && npm audit`**: exited `0` (`found 0 vulnerabilities`).
4. **`cd apps/public-web && npx tsc --noEmit && npm test`**: exited `0`; Vitest ran 2 test files / 9 tests passed (`format.test.ts`, `rate-limit.test.ts`), and `next build` (Next.js 16.4.6 Turbopack) succeeded cleanly (including inside the `docker compose -f docker-compose.dev.yml up -d --build` image build).
5. **`docker compose -f docker-compose.dev.yml up -d --build`**: exited `0`; `tir-bach-postgres` healthy, `tir-bach-db-bootstrap` and `tir-bach-migrate` (applying `0006_phase2_1.sql`) exited `0`, `tir-bach-app` (`:8080`) and `tir-bach-public-web` (`:3000`) healthy.
6. **Live Stack Isolation & Readiness Checks**:
   - `GET http://127.0.0.1:8080/readyz` -> `200 OK` (`database: ok`, `payments: ok (sandbox)`, `email: unconfigured (none)`).
   - `GET http://127.0.0.1:8080/api/public/v1/properties/tir-bach` without `X-Lovelace-Public-Key` -> `401`.
   - `GET http://127.0.0.1:3000/api/healthz` -> `200`, `GET http://127.0.0.1:3000/login` -> `404`, `GET http://127.0.0.1:8080/login` -> `200`.
7. **`cd apps/public-web && npx playwright test`**: exited `0`; `4 passed (2.7s)` against live Docker Compose stack (homepage identity/admin isolation, sandbox open-redirect rejection & 422 checkout guard, mobile responsive layout, full booking hold -> release -> re-hold -> sandbox checkout -> token-free URL confirmation -> cancellation request -> inventory depletion).
