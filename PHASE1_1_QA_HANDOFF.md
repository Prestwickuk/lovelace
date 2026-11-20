# Meadowbrook Estate — Phase 1.1 Implementation Findings & Agnostic QA / Verification Guide

> **For any AI coding agent (Codex, Claude Code, Antigravity) or human reviewer:**  
> All paths in this document are relative to the repository root (`lovelace/`). Do not assume git history (the extracted bundle has no `.git` directory). All commands were verified against Go `1.27.1` (target `go.mod` `go 1.24`) and PostgreSQL `17`.

---

## 1. Current Project Scope & Boundaries

- **Implemented Scope:** Phase 1 + Phase 1.1 (Core inventory, manual administrative reservations, unified central inventory claims, bulk blockout workflow, scheduled availability releases, primary administrative booking calendar, channel manager configuration framework, security & RBAC hardening, append-only audit triggers).
- **Explicitly Deferred (NOT Implemented):**
  - **Phase 2:** Public customer accommodation search, guest checkout journey, dynamic store pricing, temporary `HOLD` claims, payment processing, deposits, booking confirmation emails, customer cancellations/amendments.
  - **Phase 3:** Guest portal, automated guest communications, housekeeping/turnaround task management.
  - **Phase 4:** Live Airbnb / Booking.com / OTA sync adapters (only provider registry + encrypted configuration vault exist in Phase 1.1).
  - **Phase 5:** Occupancy/revenue analytics, channel performance BI reporting.

---

## 2. What Was Built (Key Files & Responsibilities)

### Database Schema & Migrations
- `migrations/0001_init.sql` — Preserved unmodified Phase 1 baseline (`properties`, `unit_types`, `units`, `extras`, `rate_plans`, `rate_rules`, `admin_users`, `admin_sessions`, `login_attempts`, `audit_logs`, legacy `availability_blocks`).
- `migrations/0002_phase1_1.sql` — Additive Phase 1.1 schema:
  - `booking_channels` (extensible booking source classification; seeded with `DIRECT`, `AIRBNB`, `BOOKING_COM`, `PHONE`, `EMAIL`, `WALK_IN`, `OTHER` for existing properties and created automatically for new properties in `internal/store/postgres/property_repo.go`).
  - `guests` (minimal Phase 1.1 guest records expandable in Phase 3).
  - `reservations` and `reservation_units` (unique `LV-YYYY-XXXXXX` references, party counts, internal notes, multi-unit support, lifecycle status without payment fields).
  - `blockout_groups` and `inventory_claims` (single unified overlap-protected allocation table with GiST constraint `excl_inventory_claims_no_overlap` on `(unit_id WITH =, claim_range WITH &&) WHERE (status = 'ACTIVE')`).
  - Migrates all legacy `availability_blocks` rows into `blockout_groups` + `inventory_claims` and drops `availability_blocks`.
  - `availability_release_rules` (`UNIT`, `UNIT_TYPE`, `PROPERTY` + `channel_code` + UTC `release_at` timestamps).
  - `channel_connections` (non-secret `public_config`, AES-256-GCM `encrypted_credentials`, masked `credential_hints`).
  - `prevent_audit_log_mutation()` trigger (`trg_audit_logs_append_only`) rejecting `UPDATE` and `DELETE` on `audit_logs`.

### Domain, Security, Channels & Store Repositories
- `internal/domain/reservations.go` — `BookingChannel`, `Guest`, `Reservation`, `ReservationStatus` (`INQUIRY`, `PENDING`, `CONFIRMED`, `CHECKED_IN`, `CHECKED_OUT`, `CANCELLED`, `NO_SHOW`), `BlocksInventory()`, `CanTransitionTo()`.
- `internal/domain/claims_releases.go` — `ClaimType`, `BlockoutScope` (`UNIT`, `UNITS`, `UNIT_TYPE`, `PROPERTY`), `BlockoutGroup`, `InventoryClaim`, `BulkBlockoutInput`, `BlockoutPreview`, `AvailabilityReleaseRule`, `EvaluateReleaseForStay()`.
- `internal/secrets/box.go` — AES-256-GCM credential vault (`NewBox`, `SealMap`, `OpenMap`, `MaskSecret`).
- `internal/channels/registry.go` — Registered providers (`beds24`, `smoobu`, `lodgify`, `channex`), `Adapter` interface for Phase 4, `ValidateAndSplitInputs`. Every provider currently sets `HasLiveAdapter: false`.
- `internal/config/config.go` — Fail-closed `production` guards (`LOVELACE_COOKIE_SECURE=true`, `LOVELACE_AUTO_SEED=false`, valid non-dev 32-byte `LOVELACE_ENCRYPTION_KEY`, non-dev Postgres password).
- `internal/auth/rbac.go` — Permissions including `PermReservationsRead/Write`, `PermReleasesRead/Write`, `PermChannelsRead/Write`.
- `internal/store/postgres/` — `reservations_repo.go`, `blockouts_repo.go`, `releases_repo.go`, `channels_repo.go`, updated `blocks_repo.go`, `inventory_repo.go` (`InheritFutureUnitBlockouts`), and `property_repo.go`.

### Services, REST API (`/api/v1`) & Admin Web UI
- `internal/service/` — `service.go`, `auth_service.go` (`AuthenticateSession`, `ChangeOwnPassword`), `reservations_service.go`, `blockouts_service.go`, `releases_service.go`, `calendar_service.go`, `channels_service.go`.
- `internal/httpapi/` — `api.go`, `handlers_reservations.go`, `handlers_blockouts_releases.go`, `handlers_calendar_channels.go`.
- `internal/webui/` — `webui.go`, `handlers_ops.go`, `handlers_phase1_1.go`, `static/app.css`, `static/calendar.js`, and server-rendered templates in `templates/` (`calendar.html`, `reservations.html`, `reservation_new.html`, `reservation_detail.html`, `blocks.html`, `releases.html`, `channel_manager.html`, `layout.html`, `account.html`).
- Deployment & Docs — `cmd/server/main.go`, `internal/seed/seed.go`, `docker-compose.yml` (production fail-closed baseline), `docker-compose.dev.yml` (standalone local dev stack), `.env.example`, `README.md`, `docs/architecture.md`, `docs/api.md`, `docs/operations.md`.

---

## 3. Key Implementation Findings & Gotchas to Know Before Reviewing

1. **Docker Compose Interpolation Behaviour:**
   - `docker-compose.yml` intentionally uses `${POSTGRES_PASSWORD:?...}` and `${LOVELACE_ENCRYPTION_KEY:?...}` so production fails closed if secrets are omitted.
   - During verification we found that `docker compose -f docker-compose.yml -f docker-compose.dev.yml` evaluates missing required variables in the base file *before* applying the dev override file. Therefore `docker-compose.dev.yml` is intentionally standalone (`docker compose -f docker-compose.dev.yml up -d --build`).
2. **Dev Secret Key Blocklist (`internal/config/config.go`):**
   - Integration tests verified that `00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff` (used in `docker-compose.dev.yml`) and `lovelace-dev-only-32byte-key-001` are explicitly rejected when `LOVELACE_ENV=production`.
3. **Secret Masking Format (`internal/secrets/box.go`):**
   - `MaskSecret` masks secrets using four Unicode bullet characters (`••••` + last 4 chars, e.g., `••••7654`), not ASCII asterisks.
4. **Reservation Status vs Inventory Claims:**
   - `INQUIRY` reserves **zero** `inventory_claims`.
   - `PENDING` creates `PENDING_RESERVATION` active claim; `CONFIRMED` and `CHECKED_IN` hold `RESERVATION` active claims.
   - Transitioning to `CANCELLED` or `NO_SHOW` updates claims to `RELEASED` transactionally.
5. **Channel-Specific Release Rules (`EvaluateReleaseForStay`):**
   - Release rules are evaluated dynamically against real `time.Now().UTC()` during reservation creation/status activation and calendar queries—no background cron job is required to open inventory when `release_at` passes.
   - Channel-specific rules (e.g., `AIRBNB`) take precedence over generic `ALL` rules for that channel on overlapping nights.
6. **Channel Manager Status Guardrail:**
   - Any API or service call attempting to set status `CONNECTED` returns `422 validation_failed` whenever `HasLiveAdapter == false`. Secrets are never returned in API JSON or written to `audit_logs`.
7. **Test Database Safety Guardrail (`internal/app/integration_test.go`):**
   - `verifySafeTestDatabase` verifies both the URL path database name and PostgreSQL `SELECT current_database()` end with `_test`, `-test`, or contain `_test_`, match each other, and do not equal `lovelace`, `postgres`, or contain `prod`. Non-matching databases abort before `TRUNCATE`.

---

## 4. What to Check (Agnostic Verification & QA Protocol)

### Step 1: Static Compilation & Vet
Run from inside `lovelace/`:
```bash
go build ./...
go vet ./...
```
**Expected:** Zero compiler errors and zero vet warnings.

### Step 2: Unit Tests (No Database Required)
```bash
go test -v -race -count=1 ./...
```
**Expected:**
- `internal/app`: `TestVerifySafeTestDatabase`, `TestConfigAndSecretsUnit`, `TestDomainPhase1_1Rules` PASS (`TestPhase1EndToEnd` and `TestPhase1_1EndToEnd` skip cleanly when `TEST_DATABASE_URL` is unset).
- `internal/auth`: `TestPasswordHashingAndPolicy`, `TestRBACMatrix` PASS.
- `internal/domain`: `TestCalculateRatePreview`, `TestFormatPence`, `TestValidationRules` PASS.

### Step 3: Full PostgreSQL 17 End-to-End & Concurrency Suite
Start PostgreSQL 17 if not already running (for example via dev Compose) and ensure a dedicated `lovelace_test` database exists:
```bash
docker compose -f docker-compose.dev.yml up -d postgres
PGPASSWORD=lovelace_dev_password psql -h 127.0.0.1 -U lovelace -d postgres -tc "SELECT 1 FROM pg_database WHERE datname = 'lovelace_test'" | grep -q 1 \
  || PGPASSWORD=lovelace_dev_password createdb -h 127.0.0.1 -U lovelace lovelace_test
```
Run the complete suite with race detector enabled:
```bash
TEST_DATABASE_URL="postgres://lovelace:lovelace_dev_password@127.0.0.1:5432/lovelace_test?sslmode=disable" \
  go test -v -race -count=1 ./...
```
**Verify all 10 test functions pass with 0 failures and 0 skips:**
- `TestPhase1EndToEnd` (backward compatibility of `/availability-blocks`, RBAC, rate preview, login rate limiting, Web UI smoke test across Phase 1 & Phase 1.1 pages)
- `TestPhase1_1EndToEnd` (multi-unit reservation + overlap `409` + cancellation release; blockout vs reservation overlap; unit-type + whole-property bulk blockout + future-unit inheritance + partial release; atomic bulk rollback; `AIRBNB` vs `DIRECT` scheduled release pre/post timestamp + manual immediate release; 10-goroutine concurrent booking race yielding strictly `1` created / `9` conflicts; Beds24/Channex encryption & `CONNECTED` rejection; `must_rotate_password` gate & prior session revocation; PostgreSQL trigger blocking `DELETE`/`UPDATE` on `audit_logs`).

### Step 4: Live Container Verification
```bash
docker compose -f docker-compose.dev.yml up -d --build
curl -sS -i http://127.0.0.1:8080/readyz
```
**Expected:** `HTTP/1.1 200 OK` with `{"status":"ready"}` and strict CSP / `X-Frame-Options: DENY` / `Cache-Control: no-store` headers.  
You can inspect the admin console at `http://127.0.0.1:8080/login` (dev default from `docker-compose.dev.yml`: `owner@example.com` / `lovelace-dev-admin-2026`), checking `/calendar`, `/reservations`, `/blocks`, `/releases`, and `/settings/integrations/channel-manager`.

### Step 5: Code & QA Audit Checklist (Phase 1.5 Readiness)
Reviewers should independently inspect and verify:
- [ ] **No Phase 2–5 Leakage:** Confirm zero public guest search/checkout handlers, zero payment/deposit/Stripe tables or status enums on `reservations`, zero live external OTA HTTP clients inside `internal/channels/`.
- [ ] **Migrations Integrity:** Confirm `migrations/0001_init.sql` is untouched and `migrations/0002_phase1_1.sql` migrates `availability_blocks` data into `blockout_groups` + `inventory_claims` cleanly.
- [ ] **Conflict Enforcement:** Confirm no code relies solely on app-level checks without PostgreSQL `excl_inventory_claims_no_overlap` backing transactional commits.
- [ ] **Security Posture:** Confirm `docker-compose.yml` fails closed without `POSTGRES_PASSWORD`/`LOVELACE_ENCRYPTION_KEY`, `LOVELACE_AUTO_SEED` is disabled in production, `internal/secrets/box.go` never logs or serializes plaintext credentials, and `must_rotate_password` gates both `/api/v1/*` and Web UI routes.
- [ ] **UI Interaction Coverage:** Verify progressive-enhancement calendar selection and scope toggles in `internal/webui/static/calendar.js` respect strict CSP (`script-src 'self'`, zero inline `<script>` blocks).
