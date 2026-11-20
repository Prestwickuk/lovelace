# LOVELACE Booking System — Phase 2.1.1 Final Stripe Mode & Production Readiness Corrections

## 1. Files Changed

- **Go Backend & Migrations**
  - `migrations/0007_phase2_1_1.sql` — Adds `payment_webhook_events.livemode BOOLEAN NULL` and expands status constraint to allow `'REJECTED'`.
  - `internal/config/config.go` — Adds `StripeKeyIsLive`, environment-aware `IsValidStripeSecretKey(k, isProduction)`, `ExpectedStripeLiveMode()`, and strict production rejection of `sk_test_` / `rk_test_` keys while permitting `sk_live_` / `rk_live_` and preserving dev/test support.
  - `internal/store/postgres/public_booking_repo.go` — Records `livemode` in `BeginWebhookEvent`.
  - `internal/service/service.go` — Enforces live Stripe key and mandatory SMTP readiness whenever any public property has `stripe_enabled=true` (even if `LOVELACE_REQUIRE_EMAIL=false`), while still allowing admin-only / payments-disabled operation without SMTP when `LOVELACE_REQUIRE_EMAIL=false`.
  - `internal/service/public_booking_service.go` — Enforces email and explicit public origin readiness at checkout initiation, prevents silent DB `custom_domain` substitution in production, captures top-level Stripe webhook `livemode`, verifies event mode against configured API key mode, records mismatched-mode events as `REJECTED` in `payment_webhook_events` and `payment.webhook_livemode_mismatch` in `audit_logs` idempotently (`WebhookResult`), and includes `"livemode": false` in dev sandbox events.
  - `internal/httpapi/public_handlers.go` — Surfaces `rejected` / `reason` cleanly from `ProcessStripeWebhook`.
  - `internal/app/phase2_test.go`, `internal/app/phase2_1_1_test.go` — Updates mock webhook events with `"livemode": false` and adds comprehensive Phase 2.1.1 integration tests.

- **Public Next.js Frontend (`apps/public-web`)**
  - `apps/public-web/package.json`, `apps/public-web/package-lock.json`, `apps/public-web/eslint.config.mjs` — Replaces deprecated Next 16 `next lint` script with `eslint .` (`eslint` + `eslint-config-next@16.4.6` flat config), adds `server-only`, and overrides `minimatch@^11.0.8` to resolve all npm advisories.
  - `apps/public-web/src/lib/lovelace-api.ts`, `apps/public-web/src/lib/bff-response.ts` — Adds explicit `import "server-only";` boundary so `LOVELACE_PUBLIC_API_SECRET` cannot be imported into client components.
  - `apps/public-web/src/components/ConfirmationViewClient.tsx` — Refactors `useEffect` hydration via `useCallback` + async invocation to satisfy React 19 ESLint `react-hooks/set-state-in-effect`.
  - `apps/public-web/src/lib/server-boundary.test.ts` — Verifies `server-only` guards and client-bundle isolation via Vitest.
  - `apps/public-web/e2e/booking.spec.ts` — Fixes `/api/public/availability` `items` key check so consecutive runs pick fresh open dates cleanly.

- **Infrastructure & Documentation**
  - `.env.example`, `docker-compose.yml` — Standardizes on canonical `https://book.example.com`, removes silent fallback domain substitution in production compose (`${LOVELACE_PUBLIC_ORIGIN:?...}`), sets `LOVELACE_REQUIRE_EMAIL=true` by default, and documents live Stripe credential rules.
  - `docs/phase2-architecture.md`, `docs/operations.md`, `docs/launch-readiness-checklist.md`, `docs/security.md` — Aligns canonical `https://book.example.com`, Stripe live/webhook `livemode` enforcement, email policy, `server-only` boundary, and process-local single-instance rate-limiting / Redis horizontal scaling documentation.

---

## 2. Security & Readiness Controls Implemented

- **Live Stripe Credentials in Production:** `config.StripeKeyIsLive` & `config.IsValidStripeSecretKey(k, true)` accept `sk_live_` and `rk_live_`, reject `sk_test_`, `rk_test_`, placeholders, and malformed keys in production, and fail `ValidateProductionStartup` + `/readyz` when any property has `stripe_enabled=true` without live credentials. Dev/test retains `sk_test_` / `rk_test_` support.
- **Webhook `livemode` Verification:** `ProcessStripeWebhook` requires `evt.LiveMode != nil` on signed payloads and verifies `*evt.LiveMode == s.cfg.ExpectedStripeLiveMode()`. Mode mismatches (`livemode=false` in production or `livemode=true` in test-mode dev) do not confirm reservations or record payments; they write an immutable `audit_logs` anomaly (`payment.webhook_livemode_mismatch`) and mark `payment_webhook_events.status = 'REJECTED'` (`livemode` recorded), preventing reprocessing while preserving webhook idempotency (`duplicate: true` on replays).
- **Mandatory Email Readiness for Live Public Bookings:** Live public booking properties (`stripe_enabled=true`) always require a genuine SMTP provider at startup (`ValidateProductionStartup`), `/readyz` (`email.status = "misconfigured"`), and checkout initiation (`ErrStripeDisabled`), even if `LOVELACE_REQUIRE_EMAIL=false`. Administration-only / payments-disabled operation (`stripe_enabled=false` across properties) may start cleanly without SMTP when `LOVELACE_REQUIRE_EMAIL=false`.
- **Canonical Domain (`https://book.example.com`):** Removed all occurrences of `bookings.example.com`, required explicit `LOVELACE_PUBLIC_ORIGIN` in production compose and `InitiatePublicCheckout`, and ensured checkout `success_url`/`cancel_url` derive exclusively from the approved origin.
- **Next.js 16 ESLint & Server-Only Secret Protection:** Configured ESLint 9 flat config (`eslint .`), added `import "server-only";` to `lovelace-api.ts` and `bff-response.ts`, and verified `.next/static/` contains zero references to `LOVELACE_PUBLIC_API_SECRET`.
- **Rate Limiting Documentation:** Documented that Go and Next.js rate limiters use in-memory process-local counters tailored for a single Go + single Next.js instance without Redis, and documented shared-store (e.g., Redis) migration as a prerequisite before horizontal scaling.

---

## 3. Verification Evidence

1. **Go Formatting, Build & Vet**:
   - `test -z "$(gofmt -l $(find cmd internal -name '*.go' -type f))" && go build ./... && go vet ./...` -> exited `0`.
2. **Go Race & PostgreSQL Integration Suite**:
   - `TEST_DATABASE_URL="postgres://lovelace_test_runner:...@127.0.0.1:5432/lovelace_test?sslmode=disable" go test -race -count=1 ./...` -> exited `0` (`internal/app` `42.521s`, `internal/auth` `3.605s`, `internal/domain` `1.544s`), including `TestPhase211StripeKeyPrefixValidationAndProductionStartup`, `TestPhase211StripeWebhookLiveModeAndOutOfOrder`, `TestPhase211MandatoryEmailReadinessForPublicBookings`, and `TestPhase211CanonicalDomainNoSilentFallbackInProduction`.
3. **Frontend Audit, Typecheck, ESLint, Vitest, Production Build & Client Bundle Scan**:
   - `cd apps/public-web && npm audit` -> exited `0` (`found 0 vulnerabilities`).
   - `cd apps/public-web && npx tsc --noEmit && npm run lint && npm test` -> exited `0` (`eslint .`: 0 errors; Vitest: `3 test files, 12 tests passed`).
   - `cd apps/public-web && npm run build` -> exited `0` (`Next.js 16.4.6 (Turbopack)`).
   - Client chunk secret scan (`grep -rn "LOVELACE_PUBLIC_API_SECRET\|lovelace-api\|X-Lovelace-Public-Key" apps/public-web/.next/static/`) -> `CLEAN_CLIENT_CHUNKS`.
4. **Docker Compose, Migrations, Live Probes & Playwright E2E**:
   - Production compose config check: missing `LOVELACE_PUBLIC_ORIGIN` rejected (`FAIL_CLOSED_MISSING_ORIGIN_OK`); explicit `LOVELACE_PUBLIC_ORIGIN=https://book.example.com` valid (`PROD_COMPOSE_VALID_OK`).
   - `docker compose -f docker-compose.dev.yml up -d --build` -> exited `0`, `0007_phase2_1_1.sql` applied, `lovelace-postgres`, `lovelace-app` (`:8080`), and `lovelace-public-web` (`:3000`) healthy; `/readyz` -> `200`, unauthenticated `/api/public/v1/...` -> `401`, `public-web` `/login` -> `404`, admin `/login` -> `200`.
   - `cd apps/public-web && npx playwright test` -> exited `0` (`4 passed (3.7s)`).

---

## 4. Remaining Operational Requirements (Launch Operator)

- Provision live Stripe API key (`sk_live_...` or appropriately scoped `rk_live_...` with Checkout Sessions write, PaymentIntents read, Refunds write permissions) and matching live `LOVELACE_STRIPE_WEBHOOK_SECRET` (`whsec_...`) in production secrets; perform one low-value live card transaction + webhook + refund check per `docs/launch-readiness-checklist.md`.
- Configure production SMTP credentials (`LOVELACE_SMTP_ADDR`, `LOVELACE_SMTP_USERNAME`, `LOVELACE_SMTP_PASSWORD`, `LOVELACE_EMAIL_FROM`) and verify SPF/DKIM/DMARC before enabling live bookings (`stripe_enabled=true`).
- Complete property owner content/policy review in Admin Console (`/property`) and confirm explicit `LOVELACE_PUBLIC_ORIGIN=https://book.example.com` and distinct DB role credentials.
