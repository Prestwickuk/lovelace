# LOVELACE Booking System — Phase 1.5 QA, Hardening & Rebranding Report

**Date:** 2026-11-13  
**Repository:** `/Users/petervine/personal-git/tir-bach`  
**Platform Identity:** **LOVELACE Booking System** (`LOVELACE`)  
**Initial Property Seed:** **Tir Bach Campsite**, Pembrokeshire, Wales  
**Stack:** Go 1.24 · PostgreSQL 17 (`pgx/v5`, `btree_gist`) · Embedded SQL migrations · Server-rendered HTML templates · Docker Compose

---

## 1. Executive Summary & Phase 2 Readiness Recommendation

**Recommendation:** **READY FOR PHASE 2**

Phase 1.5 independently audited the Phase 1 and Phase 1.1 codebase, resolved inventory/channel/occupancy/security defects without breaking existing migrations (`0001_init.sql` and `0002_phase1_1.sql` preserved untouched), verified forward schema migration on a live Phase 1.1 PostgreSQL volume (`0002` → `0003`), tested backup/restore (`pg_dump`/`pg_restore --exit-on-error`), completed real headless Chromium verification (desktop, mobile, light/dark modes, zero console/network errors), and established **LOVELACE Booking System** as the authoritative platform identity while keeping **Tir Bach Campsite** cleanly isolated as a managed property.

Phases 2–5 (public guest checkout, payments, guest portal, live OTA sync adapters, BI reporting) remain strictly deferred.

---

## 2. Independent QA Findings & Fixes Implemented

### 2.1 Inventory & Channel Claims (`0003_phase1_5.sql`, `reservations_repo.go`, `blockouts_repo.go`, `blockouts_service.go`, `calendar_service.go`)
- **Defect:** `inventory_claims` previously enforced a single undifferentiated exclusion constraint `WHERE (status = 'ACTIVE')`, which prevented multi-channel `CHANNEL_RESTRICTION` stop-sells from coexisting and treated `CHANNEL_ALLOCATION` like a hard physical closure without atomic consumption/splitting for matching-channel bookings.
- **Fix:**
  - `migrations/0003_phase1_5.sql` replaced the constraint with two partial GiST exclusion constraints:
    - Exclusive claims (`claim_type <> 'CHANNEL_RESTRICTION'`) on `(unit_id WITH =, claim_range WITH &&) WHERE (status = 'ACTIVE' AND claim_type <> 'CHANNEL_RESTRICTION')`.
    - Channel restrictions on `(unit_id WITH =, channel_scope WITH =, claim_range WITH &&) WHERE (status = 'ACTIVE' AND claim_type = 'CHANNEL_RESTRICTION')`.
  - Added `'CONSUMED'` status and `consumed_by_reservation_id` column on `inventory_claims`.
  - Updated `syncReservationUnitsAndClaims` to lock overlapping active allocations (`FOR UPDATE`), reject non-matching channels, transition matched `CHANNEL_ALLOCATION` rows to `CONSUMED`, preserve residual `ACTIVE` prefix/suffix date segments under the same `blockout_group_id`, and recalculate `blockout_groups.status`.
  - Verified under 5-goroutine concurrency race (`TestPhase1_5HardeningChannelAllocationsAndRebrand`): strictly 1 reservation succeeds (`201`), 4 fail with `409 Conflict`.
  - Updated `calendar_service.go` (`ALLOCATED` state + `AllocatedNights`) and `calendar.html` filter controls to display channel-specific restrictions and allocations accurately.

### 2.2 Occupancy & Unit Lifecycle Validation (`domain/reservations.go`, `reservations_service.go`, `blockouts_repo.go`, `blockouts_service.go`, `releases_service.go`, `0003_phase1_5.sql`)
- **Defect:** `Reservation.Validate()` did not validate guest counts against `UnitType` occupancy ceilings (`max_adults`, `max_children`, `max_infants`, `max_guests`), and `ResolveBlockoutTargetUnits` / `validateReleaseRuleTargets` allowed creating blockouts or unit-scoped releases on `inactive` units.
- **Fix:**
  - Added `adults`, `children`, `infants` columns (`DEFAULT 0`) on `reservation_units` (`0003_phase1_5.sql`) preserving existing rows cleanly.
  - Implemented `Reservation.ValidateOccupancy` supporting both combined multi-unit capacities and explicit per-unit allocations across creation, amendment, and `INQUIRY → CONFIRMED` promotion.
  - Enforced rejection (`422`) of `inactive`, `retired`, and archived units across reservations, availability blocks, bulk blockouts (`ResolveBlockoutTargetUnits` + `validateBlockoutResolvedUnits`), and unit-scoped availability releases (`validateReleaseRuleTargets`), and filtered inactive/retired units out of `reservation_new.html`.

### 2.3 Channel Manager Auth Verification (`internal/channels/registry.go`)
- Verified provider docs and corrected contracts while maintaining `has_live_adapter: false` and rejecting `CONNECTED` transitions (`422`):
  - **Beds24 (`beds24`):** `token` (`refresh_token` + `property_id`, docs: `https://beds24.com/api/v2/`).
  - **Smoobu (`smoobu`):** `hmac_sha256` (`api_key`, `api_secret`, `user_id`, and `SignatureSpec` with `X-API-Key`, `X-Timestamp`, `X-Nonce`, `X-Signature`; docs: `https://docs.smoobu.com/`).
  - **Lodgify (`lodgify`):** `api_key` (`X-ApiKey` HTTP header; docs: `https://docs.lodgify.com/reference/getting-started-with-your-api`).
  - **Channex (`channex`):** `api_key` (`user-api-key` HTTP header, `environment`, `property_id`; docs: `https://docs.channex.io/api-v.1-documentation/api-reference`).

### 2.4 Security, Database & CSP Hardening
- **Audit Log Immutability (`0003_phase1_5.sql`):** Added `BEFORE TRUNCATE` statement trigger `trg_audit_logs_no_truncate` preventing `TRUNCATE TABLE audit_logs` unless running on a `*_test`/`*-test` DB with `SET LOCAL tirbach.test_reset_token = 'allow_test_reset'`.
- **Unprivileged Test Role (`scripts/setup-test-db.sh`):** Provisioned `tir_bach_test_runner` (`NOSUPERUSER NOCREATEDB NOCREATEROLE`); `verifySafeTestDatabase` rejects superusers, `postgres`, `tir_bach`, non-loopback hosts, and non-`*_test` DBs.
- **Branded Web UI Error Handling (`internal/webui/`):** Removed shadowed package-level `redirectErr` (`http.Error(..., "Forbidden", 403)`) and routed all HTML error paths cleanly through `error.html` (`h.renderError` / `h.renderDomainError`).
- **Strict CSP Compliance (`app.css` & `templates/*.html`):** Replaced all ~40 inline `style="..."` attributes with CSS utility classes (`ZERO INLINE STYLES` verified) so `style-src 'self'` produces zero browser console violations.

### 2.5 LOVELACE Platform Rebrand & Responsive Polish
- Created [`internal/branding/branding.go`](internal/branding/branding.go), `properties.branding_config`, and generated PNG web derivatives under [`internal/webui/static/brand/`](internal/webui/static/brand/) from master files in `assets/brand/`.
- Applied coastal heritage palette (`#2F4F4A`, `#C49A45`, `#F6F1E5`, `#6B7B4E`, `#1E211E`) plus automatic `@media (prefers-color-scheme: dark)` theming in `app.css`.
- Fixed mobile CSS Grid overflow (`min-width: 0` on `.main` and `.card`, single-column media rule for `.grid-2`/`.grid-4`) verified at `390x844` in Chromium (`mobileOverflowOnCalendar: false`).

---

## 3. Verification Summary

| Check | Outcome | Evidence & Notes |
|---|---|---|
| `gofmt -l cmd internal` | **PASS** | Empty output (all Go files formatted) |
| `go build ./... && go vet ./...` | **PASS** | Clean build and static analysis |
| `go test -v -race -count=1 ./...` | **PASS** | `internal/app` (6 tests incl. `TestPhase1EndToEnd`, `TestVerifySafeTestDatabase`, `TestConfigAndSecretsUnit`, `TestDomainPhase1_1Rules`, `TestPhase1_1EndToEnd`, `TestPhase1_5HardeningChannelAllocationsAndRebrand` — 20.893s), `internal/auth` (2 tests — 3.870s), `internal/domain` (3 tests — 1.954s). Run against `tir_bach_test` as non-superuser `tir_bach_test_runner`. |
| Phase 1.1 → Phase 1.5 Docker Upgrade | **PASS** | Live `tir-bach-app` container rebuilt (`tir-bach:phase1.5-dev`); existing volume migrated cleanly from `0002_phase1_1.sql` to `0003_phase1_5.sql`; `/readyz` returned `{"status":"ready"}`. |
| Backup & Restore (`pg_dump` / `pg_restore`) | **PASS** | Custom dump restored cleanly via `pg_restore --no-owner --exit-on-error` into throwaway DB; all 3 migrations & `Tir Bach Campsite` property verified. |
| Real Browser Verification (Chromium Headless) | **PASS** | Playwright (`chrome-headless-shell` 1223) against `http://127.0.0.1:8080`: login, dashboard, calendar, reservation creation, bulk allocation preview, releases, channel manager (`hmac_sha256` / `user-api-key`), dark mode, `390x844` mobile layout (`mobileOverflowOnCalendar: false`), **0** console errors, **0** page errors, **0** failed asset requests. |
| Production Compose Live Run (`docker-compose.yml`) | **NOT RUN** | Verified fail-closed env validation via `TestConfigAndSecretsUnit`; production compose stack not deployed to a public TLS proxy in this local workspace. |

---

## 4. Agnostic Handoff Directions (Codex · Claude Code · Antigravity)

For any autonomous engineering agent resuming from this bundle (whether on OpenAI Codex, Claude Code, or Google Antigravity):

1. **Preserve Phase Boundaries & Immutable History:**
   - **Never modify** `migrations/0001_init.sql` or `migrations/0002_phase1_1.sql`; create sequential additive migrations (`0004_*.sql`, etc.) for Phase 2+.
   - **Keep `tir-bach` strictly as a property record**, while all software identity, UI chrome, and assets derive from `internal/branding`.
2. **Database & Test Setup:**
   - Start local services: `docker compose -f docker-compose.dev.yml up -d --build`
   - Ensure the isolated unprivileged test role exists: `./scripts/setup-test-db.sh`
   - Always run full `-race` tests via `tir_bach_test_runner` against `tir_bach_test`:
     ```bash
     TEST_DATABASE_URL="postgres://tir_bach_test_runner:tir_bach_test_runner_pw@127.0.0.1:5432/tir_bach_test?sslmode=disable" go test -v -race -count=1 ./...
     ```
3. **Verify Before Editing / Before Reporting:**
   - Keep `gofmt -l cmd internal` empty and avoid inline `<script>` / `style="..."` in templates (strict CSP `style-src 'self'`).
   - Next milestone (**Phase 2 — Direct Booking Engine**) should build atop `inventory_claims` `HOLD` claims (`EXCL` already in place), `rate_plans`/`rate_rules`, and `reservations`, adding temporary hold expiry, payment intents, and public search endpoints without altering existing claim exclusion semantics.
