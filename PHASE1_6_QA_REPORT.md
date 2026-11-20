# LOVELACE Booking System — Phase 1.6 QA & Production Hardening Report

**Date:** 2026-11-13  
**Scope:** Targeted remediation for PostgreSQL privilege separation, safe post-consumption allocation date editing, allocation consumption vs. release lifecycle semantics, cancellation/amendment reconciliation, Smoobu provider contract refinement, and asset/logging cleanup.  
**Phase Boundary:** Phase 1, Phase 1.1, Phase 1.5, and Phase 1.6 only. Phases 2–5 (public booking checkout, payment intents/gateways, guest portal, live OTA sync, BI reporting) remain intentionally deferred.

---

## 1. Critical PostgreSQL Privilege Separation & Compose Hardening

- **Roles Implemented ([`scripts/postgres/ensure-roles.sh`](scripts/postgres/ensure-roles.sh), [`scripts/postgres/01-init-roles.sh`](scripts/postgres/01-init-roles.sh)):**
  1. **Bootstrap Administrator** (`POSTGRES_BOOTSTRAP_USER`, defaults to `postgres` in [`docker-compose.yml`](docker-compose.yml); preserves existing `lovelace` superuser when upgrading pre-existing Phase 1.5 volumes without renaming): installs `btree_gist`, creates/updates roles, sets schema & object ownership, and sets default privileges. Bootstrap credentials are never exposed to `app`.
  2. **Migration / DDL Role (`lovelace_migrator`)**: `NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT`; owns schema `public` and all application tables, sequences, views, and user-defined functions; runs exclusively inside [`cmd/migrate/main.go`](cmd/migrate/main.go) (`lovelace-migrate` service).
  3. **Runtime Application Role (`lovelace_app`)**: `NOSUPERUSER NOCREATEDB NOCREATEROLE`; holds strictly DML privileges (`SELECT`, `INSERT`, `UPDATE`, `DELETE` on application tables, `USAGE`/`SELECT` on sequences, `EXECUTE` on functions, read-only `SELECT` on `schema_migrations`), with zero `CREATE` on `public` and zero table ownership.
- **Read-Only Startup Verification & Production Fail-Closed Guard ([`internal/store/postgres/migrate.go`](internal/store/postgres/migrate.go), [`cmd/server/main.go`](cmd/server/main.go)):**
  - `Store.Migrate` verifies `schema_migrations` checksums via read-only `SELECT` when up to date so `lovelace_app` executes zero DDL on boot, and fails fast if pending migrations exist without DDL privileges.
  - `Store.InspectRolePrivileges` logs runtime role flags on startup and causes `cmd/server` to fail closed in `production` if connected as `SUPERUSER`, `CREATEDB`, or `CREATEROLE`.
- **Docker Compose Cleanup:** Removed the misleading `-f docker-compose.yml -f docker-compose.dev.yml` overlay comment from [`docker-compose.yml`](docker-compose.yml) and documented standalone usage in [`docker-compose.dev.yml`](docker-compose.dev.yml) and [`.env.example`](.env.example).

---

## 2. Safe `UpdateBlockoutGroupDates()` After Consumption

- Added `allocation_consumptions` (`migrations/0004_phase1_6.sql`) recording exact `[max(allocStart, resStart), min(allocEnd, resEnd))` consumed intervals linked to `blockout_group_id`, `source_claim_id`, `reservation_id`, and `unit_id`.
- Rewrote `Store.UpdateBlockoutGroupDates` ([`internal/store/postgres/blockouts_repo.go`](internal/store/postgres/blockouts_repo.go)) to lock the group, active consumptions, and active claims `FOR UPDATE`, reject shortening across any consumed reservation (`422 ValidationError`), compute `[newStart, newEnd) \ consumedRanges(unit)` via `subtractDateRanges`, rebuild residual `ACTIVE` segments atomically, and roll back cleanly on overlap conflicts (`409 Conflict`).

---

## 3. Inventory Claim Lifecycle vs. Manual Release Semantics

- Expanded `blockout_groups.status` (`0004_phase1_6.sql`, [`internal/domain/claims_releases.go`](internal/domain/claims_releases.go)) to distinguish `ACTIVE`, `PARTIALLY_CONSUMED`, `CONSUMED`, `PARTIALLY_RELEASED`, and `RELEASED`.
- Consuming an allocation in [`internal/store/postgres/reservations_repo.go`](internal/store/postgres/reservations_repo.go) sets `status = 'CONSUMED'` and `consumed_by_reservation_id` while keeping `released_at = NULL` and `released_by = NULL`.
- `recomputeBlockoutGroupStatus` uses active `allocation_consumptions` (`status = 'CONSUMED'`) alongside `inventory_claims` counts so consumed allocations accurately report `PARTIALLY_CONSUMED` / `CONSUMED`, never `RELEASED`.
- Updated [`internal/webui/templates/blocks.html`](internal/webui/templates/blocks.html) to display distinct badges, consumed/released claim counts, and clickable reservation provenance links (`/reservations/<id>`).

---

## 4. Channel Allocation Cancellation & Amendment Reconciliation

- Implemented `Store.restoreConsumedAllocationsForReservation` ([`internal/store/postgres/reservations_repo.go`](internal/store/postgres/reservations_repo.go)):
  - Triggered on transitions to `CANCELLED`, `NO_SHOW`, or `INQUIRY` (never on `CHECKED_OUT`), and atomically prior to re-allocating updated dates, units, or channels during `UpdateReservation`.
  - Restores consumed date intervals back to their originating `blockout_group_id` and coalesces adjacent `ACTIVE` segments unless the allocation or unit remainder was already manually released (in which case consumption is marked `DETACHED`).
  - Remaining unconsumed dates can still be explicitly released via `/release` without losing provenance.

---

## 5. Smoobu Provider Refinement

- Refined Smoobu in [`internal/channels/registry.go`](internal/channels/registry.go), [`internal/webui/templates/channel_manager.html`](internal/webui/templates/channel_manager.html), and [`docs/providers.md`](docs/providers.md):
  - Retains `hmac_sha256`, requires only `api_key` and `api_secret` (removed `user_id` input; rejects unsupported `user_id` with `422`).
  - Documents `SignatureSpec` canonical components (`HTTP_METHOD`, `REQUEST_PATH`, `CANONICAL_QUERY_RFC3986`, `UTC_TIMESTAMP`, `NONCE`, `SHA256_BODY_HASH`, `API_KEY`), Base64 `HMAC-SHA256` encoding, replay protection (`<=5m` skew + single-use nonce), and `GET /api/me` account discovery in Phase 4.

---

## 6. Brand Asset & Server Log Verification

- Stored the unmodified original `2048 × 2048` master artwork at `assets/brand/lovelace-logo-original-2048x2048.jpg` (verified SHA-256 `48efb3881fe19e2753349e6bba1a19fda78a44fa318b6781d027aa5230e7f154`, `2048 × 2048` via `sips`).
- Updated server startup log message in [`cmd/server/main.go`](cmd/server/main.go) to `"LOVELACE Booking System server listening"`.

---

## 7. Live Upgrade & Test Verification Evidence

- **Go Test Suite (`go test -v -race -count=1 ./...`)**: Passed all tests across `internal/app`, `internal/auth`, and `internal/domain` against PostgreSQL 17 under non-superuser `lovelace_test_runner` (`lovelace_test`).
- **Live Phase 1.5 → 1.6 Docker Upgrade (`docker compose -f docker-compose.dev.yml up -d --build`)**:
  - Preserved existing `lovelace_pgdata` volume data (`properties=1`, `reservations=2`, `audit_logs=8`) and applied `0004_phase1_6.sql` via `lovelace-migrate` (`role=lovelace_migrator, superuser=false`).
  - `lovelace-app` booted as `lovelace_app` (`superuser=false, createdb=false, createrole=false`), passed `/readyz` (`{"status":"ready"}`) and `GET /login` (`HTTP 200`), and lacked `POSTGRES_*` and `LOVELACE_MIGRATOR_*` secrets in container `env`.
  - Direct SQL check verified `CREATE TABLE should_fail (x int)` as `lovelace_app` failed with `ERROR: permission denied for schema public`.
  - `pg_dump` → `pg_restore` into `lovelace_restore_check` preserved `restored_props=1`, `restored_res=2`, `restored_migs=4`.

---

## 8. Phase 1.6.1 — Security & Inventory Regression Remediation

- **Eliminated Bootstrap Credential Leakage ([`scripts/postgres/ensure-roles.sh`](scripts/postgres/ensure-roles.sh)):**
  - Removed all `psql -v` command-line password arguments and removed bare `SELECT set_config(...)` result-set output.
  - Invokes `psql -X -q -v ON_ERROR_STOP=1` reading credentials from environment variables via `\getenv`, binding them silently via `\gset`, immediately `\unset`ting client password variables, and clearing server session GUCs inside the anonymous `DO $$` block before idempotently creating/updating `lovelace_migrator` and `lovelace_app`.
  - Verified against a fresh `postgres:17-alpine` cluster using synthetic passwords (`SynMigPass_9x7K_2026Q`, `SynAppPass_4m2Z_2026W`) and a `psql` argv/`ps` wrapper: zero secrets appeared in container logs, stdout/stderr, `psql` arguments, or `ps` output, while both roles authenticated cleanly (`rolsuper=f`, `rolcreatedb=f`, `rolcreaterole=f`, `rolbypassrls=f`) with `btree_gist` enabled.
  - Verified live recreated `lovelace-db-bootstrap` logs contain only `NOTICE: extension "btree_gist" already exists, skipping`.
- **Preserved Manually Released Inventory & Explicit Reinstate ([`internal/store/postgres/blockouts_repo.go`](internal/store/postgres/blockouts_repo.go), [`internal/service/blockouts_service.go`](internal/service/blockouts_service.go), [`internal/httpapi/handlers_blockouts_releases.go`](internal/httpapi/handlers_blockouts_releases.go), [`internal/webui/handlers_phase1_1.go`](internal/webui/handlers_phase1_1.go), [`internal/webui/templates/blocks.html`](internal/webui/templates/blocks.html)):**
  - Updated `Store.UpdateBlockoutGroupDates` to lock both active `allocation_consumptions` (`CONSUMED`) and manually `RELEASED` `inventory_claims` (`FOR UPDATE`), subtract `[newStart, newEnd) \ (CONSUMED ∪ RELEASED)`, keep existing `CONSUMED` and `RELEASED` segments (plus their original `id`, `released_at`, and `released_by`) completely immutable, and activate only net-new uncovered dates (`409 Conflict` if shortening/no-op leaves zero active/consumed coverage).
  - Added explicit administrative reinstatement (`Store.ReinstateBlockoutClaims`, `Service.ReinstateBlockout`, `POST /api/v1/properties/{propertyID}/blockouts/{id}/reinstate`, Web UI `POST /blocks/groups/{id}/reinstate` + `"Reinstate Claim"` button) enforcing RBAC (`PermBlocksWrite`), channel-aware conflict checks + GiST safety, and `"blockout_group.reinstated"` audit logging.
- **Upgrade & Backup/Restore Documentation ([`README.md`](README.md), [`docs/operations.md`](docs/operations.md), [`docker-compose.yml`](docker-compose.yml), [`docs/api.md`](docs/api.md)):**
  - Documented fresh install (`postgres` bootstrap user) vs. pre-existing Phase 1.5 upgrade (`POSTGRES_BOOTSTRAP_USER=lovelace` matching existing database password), PostgreSQL's initialized `PGDATA` env-var behavior, safe SQL password rotation (`ALTER ROLE`), removal of hardcoded `-U postgres` / `-U lovelace` assumptions in backup/restore commands, and mandatory pre-upgrade `pg_dump`.
- **Verification:**
  - `gofmt -l cmd internal`, `go build ./...`, `go vet ./...`, and `go test -v -race -count=1 ./...` passed across all packages (including `TestPhase1_6_1PreservesReleasedInventoryAndExplicitReinstate`).
  - Live `docker compose -f docker-compose.dev.yml` upgrade preserved existing records (`properties=1`, `reservations=2`, `audit_logs=8`, `schema_migrations` `0001`–`0004`), passed `/readyz` (`HTTP 200 {"status":"ready"}`), and served `GET /login` with LOVELACE branding.
