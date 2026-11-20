# LOVELACE Booking System — Operations, Security & Deployment Runbook

## 1. Environment Variables Reference

| Variable | Default | Production Requirement | Purpose |
|---|---|---|---|
| `LOVELACE_ENV` | `development` | Set to `production` | Enables fail-closed startup checks (including non-privileged DB role enforcement). |
| `LOVELACE_LISTEN_ADDR` | `:8080` | `0.0.0.0:8080` (behind TLS proxy) | HTTP bind address. |
| `DATABASE_URL` | (required) | `lovelace_app` DML-only DSN + `sslmode=require`/`verify-full` | Runtime PostgreSQL 17 connection DSN (`NOSUPERUSER NOCREATEDB NOCREATEROLE`). |
| `MIGRATION_DATABASE_URL` | optional | `lovelace_migrator` DDL owner DSN | Used exclusively by `lovelace-migrate` (`cmd/migrate`) to apply embedded SQL migrations. |
| `LOVELACE_COOKIE_SECURE` | `false` (`true` in prod compose) | Must be `true` | Marks `lovelace_session` cookie `Secure`. |
| `LOVELACE_TRUST_PROXY` | `false` (`true` in prod compose) | `true` behind Caddy/Nginx/Cloudflare | Extracts real client IP from `X-Forwarded-For` for rate limiter & audit logs. |
| `LOVELACE_ENCRYPTION_KEY` | dev fallback outside prod | **Required** 32-byte key (64 hex chars or 44-char base64) | AES-256-GCM encryption for `channel_connections.encrypted_credentials`. |
| `LOVELACE_AUTO_SEED` | `false` | Must be `false` | Startup fails closed if `true` in `production`. |
| `LOVELACE_BOOTSTRAP_ADMIN_EMAIL` | unset | Optional first-boot email | When `admin_users` has `0` rows, provisions the initial global `owner_admin` on startup with `must_rotate_password=true` (without seeding demo property data). Ignored once any admin exists. |
| `LOVELACE_BOOTSTRAP_ADMIN_NAME` | `Owner Administrator` | Optional display name | Display name used when bootstrapping the initial `owner_admin`. |
| `LOVELACE_BOOTSTRAP_ADMIN_PASSWORD` | unset | `>= 12` chars (paired with email; dev placeholders rejected) | Temporary passphrase for the initial `owner_admin`; user is redirected to `/account?rotate=1` on first login to set their own permanent password, and restarts never overwrite rotated passwords. |
| `LOVELACE_ATTACHMENT_DIR` | `/var/lib/lovelace/attachments` | Persistent writable volume | Directory storing uploaded maintenance evidence attachments (`storage_key` SHA-256 blobs). |
| `LOVELACE_MAX_ATTACHMENT_BYTES` | `10485760` (10 MiB) | Positive integer (<= `26214400`) | Maximum byte size per uploaded maintenance attachment. |

### PostgreSQL Privilege Separation (Phase 1.6)
LOVELACE separates database duties into three distinct identities:
1. **Bootstrap Administrator** (`POSTGRES_BOOTSTRAP_USER`, default `postgres` on fresh production clusters; keep `lovelace` only when upgrading a pre-existing Phase 1.5 cluster): installs `btree_gist`, creates roles, and assigns ownership via `scripts/postgres/ensure-roles.sh`. Credentials are **never** passed to `app`.
2. **Migration / DDL Role** (`lovelace_migrator`, `NOSUPERUSER NOCREATEDB NOCREATEROLE`): owns schema `public` and all application tables, sequences, views, and functions; executes `lovelace-migrate` inside the one-shot `migrate` container before `app` starts.
3. **Runtime Application Role** (`lovelace_app`, `NOSUPERUSER NOCREATEDB NOCREATEROLE`): holds strictly DML privileges (`SELECT`, `INSERT`, `UPDATE`, `DELETE` on application tables, `USAGE`/`SELECT` on sequences, `EXECUTE` on functions, and read-only `SELECT` on `schema_migrations`), with zero `CREATE` privilege on `public` and zero table ownership. In `production`, `noteG-server` refuses to boot if connected as a `SUPERUSER`, `CREATEDB`, or `CREATEROLE` role.

### Generating Secrets
```bash
openssl rand -hex 32   # Use output for LOVELACE_ENCRYPTION_KEY
openssl rand -base64 24 # Strong PostgreSQL / initial admin passphrase
```

---

## 2. Running Locally vs Production Docker Compose

### Production Compose (`docker-compose.yml`)
`docker-compose.yml` is standalone — **never** combine it with `docker-compose.dev.yml`. It fails closed unless bootstrap, migrator, app passwords, and `LOVELACE_ENCRYPTION_KEY` are supplied:
```bash
export POSTGRES_BOOTSTRAP_PASSWORD="<strong-bootstrap-password>"
export LOVELACE_MIGRATOR_PASSWORD="<strong-migrator-password>"
export LOVELACE_APP_PASSWORD="<strong-app-password>"
export LOVELACE_ENCRYPTION_KEY="$(openssl rand -hex 32)"
docker compose up -d --build
```
Place behind a TLS-terminating reverse proxy (Nginx, Caddy, or Traefik) serving HTTPS on `:443` and forwarding `X-Forwarded-For` and `X-Forwarded-Proto: https` to `127.0.0.1:8080`.

On a fresh empty database (`admin_users = 0` rows), set `LOVELACE_BOOTSTRAP_ADMIN_EMAIL` and `LOVELACE_BOOTSTRAP_ADMIN_PASSWORD` (`>= 12` characters) in your container environment (or run `lovelace-seed -admin-only`) so `noteG-server` automatically provisions the initial `owner_admin` with `must_rotate_password=true` on first boot without seeding demo property/guest data. Signing in redirects immediately to `/account?rotate=1` to set your permanent password, automatically generates **8 one-time Recovery Codes** (downloadable/copyable in `/account`, stored solely as keyed HMAC-SHA256 digests in `admin_recovery_codes`) for email-free self-service reset at **`/login/recover`**, and never overwrites existing accounts on subsequent restarts. Once your password is set, **`/onboarding`** launches the **6-Step Guided Property Setup Wizard** (supporting Campsite/Glamping, Holiday Cottage/Airbnb, Boutique Hotel/B&B, or Custom venues, bulk unit/pitch generation, seasonal rate uplifts, extras, deposits, house rules, and optional staff accounts while keeping Stripe Checkout and Guest Portal safely dark by default), and **`/settings/backups`** provides self-service local `.zip` / encrypted `.zip.enc` backups, scheduled off-site sync to **AWS S3**, **S3-compatible buckets (Cloudflare R2 / Backblaze B2)**, **Google Cloud Storage**, and **Microsoft Azure Blob Storage**, SHA-256 archive verification, and safe restore into an empty environment. For other staff accounts, `owner_admin` can also issue a temporary reset password (`must_rotate_password=true`) directly in **`/users`**.

Initial bootstrap of both a baseline property and owner user (for standalone non-production demos) can also be run explicitly via `cmd/seed`:
```bash
DATABASE_URL="postgres://lovelace_app:<app-pw>@127.0.0.1:5432/lovelace?sslmode=disable" \
  go run ./cmd/seed \
  -admin-email "owner@example.com" \
  -admin-name "Sian Evans" \
  -admin-password "<strong-initial-passphrase>"
```
*Note: Existing admin passwords are never overwritten on subsequent runs.*

### Local Development Compose (`docker-compose.dev.yml`)
```bash
docker compose -f docker-compose.dev.yml up -d --build
```

---

## 3. Database Upgrades, Password Rotation, Backup & Recovery

Migrations in `migrations/` (`0001_init.sql` through `0018_phase3_3_2_final_acceptance.sql`) are embedded in the binary and applied transactionally by `lovelace-migrate` (`lovelace_migrator`) into `schema_migrations`. `noteG-server` (`lovelace_app`) verifies checksums via read-only `SELECT` on startup without executing DDL. Migration `0018` adds the resource-type-aware audit replacement snapshot validator, replaces the redaction function, and applies the same validation in the overlay insert trigger with restricted execution grants. The existing `audit_logs_effective` view continues to present overlays. The migration does not rewrite earlier migration files or existing raw audit rows and redaction overlays.

### Phase 3 Milestone 3.2 acceptance verification

The supported verification environment is **Go 1.24** and **PostgreSQL 17** (including the PostgreSQL 17 client tools). The workflow at `.github/workflows/phase3-m3.2-acceptance.yml` provisions a disposable PostgreSQL 17 service, separate non-superuser `lovelace_migrator`, `lovelace_app`, and `lovelace_test_runner` roles, and isolated `_test` databases. Its role passwords are CI-only fixtures; no production secrets are used. It runs formatting, build, vet, full PostgreSQL-backed Go tests, race tests, a fresh migration, and an upgrade from migration `0017` with existing audit history and an overlay.

For local verification, use a local PostgreSQL 17 server and create disposable databases whose names end in `_test`. Keep the migration role as the database/schema owner and give `lovelace_app` only the runtime grants. The ordinary application integration suite uses `TEST_DATABASE_URL` for the non-superuser test runner and `TEST_APP_DATABASE_URL` for the real app role; set both to the same isolated integration database. Never point either variable at `lovelace`, a shared development database, or production. Run:

With the project's isolated development Compose PostgreSQL container, `scripts/setup-test-db.sh` provisions/reset only its `lovelace_test` database and test-runner role. Use it only when that database is disposable. Set `TEST_DATABASE_URL` to the resulting test-runner connection and `TEST_APP_DATABASE_URL` to `lovelace_app` on the same test database; obtain credentials from the local development setup without copying them into source control. For local PostgreSQL without Docker, use the role and database provisioning pattern in the acceptance workflow, substituting local-only credentials.

```bash
gofmt -l .
go build ./...
go vet ./...
TEST_DATABASE_URL="$TEST_DATABASE_URL" TEST_APP_DATABASE_URL="$TEST_APP_DATABASE_URL" go test -count=1 ./...
TEST_DATABASE_URL="$TEST_DATABASE_URL" TEST_APP_DATABASE_URL="$TEST_APP_DATABASE_URL" go test -count=1 -race ./...
```

To verify the clean-install path, set `MIGRATION_DATABASE_URL` to an empty disposable `_test` database owned by `lovelace_migrator` and run `go run ./cmd/migrate`. To exercise the upgrade path, point the same variable at a separate empty disposable `_test` database owned by that role, run `scripts/test-migration-upgrade.sh` to apply `0001`–`0017` and create representative pre-existing audit and overlay rows, then run `go run ./cmd/migrate` to apply `0018`. Check `schema_migrations` and `audit_logs_effective`; the existing overlay must remain visible while the immutable raw `audit_logs` row remains unchanged. The script refuses any database that is not empty, `_test`-named, and connected as the non-superuser `lovelace_migrator`.

Before applying `0018` to an existing environment, take a verified PostgreSQL backup, quiesce writes during the migration window, and run the normal `lovelace-migrate` binary using `MIGRATION_DATABASE_URL` for `lovelace_migrator`. Do not edit or replay migrations `0001`–`0017` to install this change. Verify the recorded migration version and audit read path before starting the application with `lovelace_app`.

### Fresh Installation vs. Pre-Existing Phase 1.5 Database Volumes

1. **Fresh PostgreSQL Installation (`PGDATA` empty):**
   - `POSTGRES_BOOTSTRAP_USER` defaults to `postgres`.
   - `POSTGRES_BOOTSTRAP_PASSWORD` sets the initial bootstrap superuser password during cluster creation (`/docker-entrypoint-initdb.d/01-init-roles.sh`), and `db-bootstrap` creates/updates non-superuser `lovelace_migrator` and `lovelace_app` cleanly without logging passwords.
2. **Existing Phase 1.5 Volume (`lovelace_pgdata` already initialised):**
   - Earlier Phase 1.5 volumes were initialised using the `lovelace` superuser rather than `postgres`.
   - Before upgrading, explicitly configure:
     ```bash
     export POSTGRES_BOOTSTRAP_USER=lovelace
     export POSTGRES_BOOTSTRAP_PASSWORD="<EXISTING_LOVELACE_DB_PASSWORD>"
     ```
   - **Important PostgreSQL behaviour:** Changing Docker Compose environment variables (`POSTGRES_USER` or `POSTGRES_PASSWORD`) **does NOT** rename existing roles or change passwords inside an already-initialised `PGDATA` volume — the PostgreSQL Docker entrypoint skips cluster initialisation whenever persistent data already exists.
   - **Never delete or recreate persistent volumes (`docker compose down -v`) during upgrades.** Take a pre-upgrade snapshot first (below), then run `docker compose up -d --build`. `db-bootstrap` transfers schema and table ownership to `lovelace_migrator` idempotently while preserving all existing Phase 1.5 data.

### Optional Safe Password Rotation After Authentication

If you wish to rotate the bootstrap superuser password (or role credentials) on an already-initialised cluster, authenticate with the **existing** credential first, rotate via SQL, and then update your `.env` / secret store:
```bash
# 1. Authenticate using the existing bootstrap role and rotate its password in PostgreSQL:
docker exec -it lovelace-postgres psql \
  -U "${POSTGRES_BOOTSTRAP_USER:-postgres}" \
  -d "${POSTGRES_DB:-lovelace}" \
  -c "ALTER ROLE \"${POSTGRES_BOOTSTRAP_USER:-postgres}\" WITH PASSWORD '<NEW_STRONG_BOOTSTRAP_PASSWORD>';"

# 2. Update POSTGRES_BOOTSTRAP_PASSWORD (and LOVELACE_MIGRATOR_PASSWORD / LOVELACE_APP_PASSWORD if desired),
#    then re-run db-bootstrap + restart app so non-superuser role passwords sync safely via \getenv/\gset:
docker compose up -d --no-deps db-bootstrap migrate app
```

### Pre-Upgrade Backup & Restoration (Parameterised Bootstrap Identity)

Never hardcode `-U postgres` or `-U lovelace`; use your actual configured `${POSTGRES_BOOTSTRAP_USER}` (`postgres` on fresh installs, `lovelace` on pre-existing Phase 1.5 volumes):

```bash
# Mandatory pre-upgrade backup (custom compressed format):
BOOTSTRAP_USER="${POSTGRES_BOOTSTRAP_USER:-postgres}"
DB_NAME="${POSTGRES_DB:-lovelace}"
docker exec lovelace-postgres pg_dump -U "$BOOTSTRAP_USER" -Fc "$DB_NAME" > "lovelace_pre_upgrade_$(date +%Y%m%d_%H%M%S).dump"

# Routine scheduled backup:
docker exec lovelace-postgres pg_dump -U "$BOOTSTRAP_USER" -Fc "$DB_NAME" > "lovelace_$(date +%Y%m%d_%H%M%S).dump"

# Rollback / restore into a clean database using the actual bootstrap identity:
docker exec -i lovelace-postgres pg_restore -U "$BOOTSTRAP_USER" -d "$DB_NAME" --clean --if-exists < lovelace_pre_upgrade_backup.dump
```

---

## 4. Test Database Safety Guardrails
Provision the unprivileged test database role via `./scripts/setup-test-db.sh` (`lovelace_test_runner`).  
Integration tests (`internal/app`) call `verifySafeTestDatabase` prior to any `TRUNCATE`:
- Only executes when `TEST_DATABASE_URL` is set.
- Both URL DB path and PostgreSQL `SELECT current_database()` must explicitly match `*_test` or `*-test`, must agree, and must not contain `prod` or equal `lovelace` / `postgres`.
- Connected host must be loopback/local and role must be **non-superuser** (`rolsuper = false`, non-`postgres`, non-`lovelace`).
- `audit_logs` is reset only inside a transaction that sets `SET LOCAL lovelace.test_reset_token = 'allow_test_reset'` on a `*_test` DB.

---

## 5. Phase 2 / 2.1 / 2.1.1 Public Frontend, Stripe & Email Outbox Operations

- **Two-Frontend Architecture & BFF Isolation:** `public-web` runs as a separate Next.js container (`:3000`) communicating strictly via Go's `/api/public/v1/*` API (`LOVELACE_API_BASE_URL` + server-side `LOVELACE_PUBLIC_API_SECRET`). It must never receive `DATABASE_URL` or administrative credentials, and `src/lib/lovelace-api.ts` / `src/lib/bff-response.ts` enforce `import "server-only"` so server secrets cannot leak into client bundles. In `production`, `LOVELACE_PUBLIC_API_SECRET` is **mandatory**; startup and `/readyz` fail closed if missing.
- **Canonical Origin & Proxy Boundary:** Set `LOVELACE_PUBLIC_ORIGIN` explicitly in production (e.g., `https://book.example.com` for Meadowbrook Estate; comma-separated if multiple approved domains). Production compose and startup fail closed if `LOVELACE_PUBLIC_ORIGIN` is omitted rather than silently substituting an unconfigured domain. Configure `LOVELACE_TRUST_PROXY` + `LOVELACE_TRUSTED_PROXIES` and `LOVELACE_TRUST_PROXY` + `LOVELACE_TRUSTED_PROXIES` with trusted reverse-proxy CIDRs only; `X-Forwarded-For` is walked right-to-left only when the socket peer is trusted.
- **Process-Local Rate Limiting & Horizontal Scaling:** The current rate limiters (`internal/httpx/ratelimit.go` in Go and `apps/public-web/src/lib/rate-limit.ts` in Next.js) maintain **process-local** in-memory sliding counters designed for a single-instance deployment (1 Go `app` instance + 1 Next.js `public-web` instance). Do not introduce Redis solely for this initial single-site footprint, **but before horizontally scaling** either tier across multiple replicas, replace process-local counters with a shared backing store (e.g., Redis) so attackers cannot multiply rate budgets across instances.
- **Stripe Configuration, Live-Mode Enforcement & Webhook Livemode Validation:**
  - **Production live credentials only:** When `LOVELACE_ENV=production`, `LOVELACE_STRIPE_SECRET_KEY` must use a live key (`sk_live_...` or appropriately scoped `rk_live_...` with Checkout Sessions write, PaymentIntents read, and Refunds write permissions) paired with a live `LOVELACE_STRIPE_WEBHOOK_SECRET` (`whsec_...`), and `LOVELACE_STRIPE_SANDBOX` must be `false`. Test keys (`sk_test_`, `rk_test_`) and sandbox secrets fail startup closed in production (`sk_test_`/`rk_test_` remain supported only in non-production development/test environments).
  - **Webhook `livemode` verification:** After HMAC `Stripe-Signature` verification, `ProcessStripeWebhook` compares the event's top-level `livemode` boolean against the mode derived from the configured Stripe key (`true` in production; `false` for `sk_test_`/`rk_test_`/sandbox in dev). Mismatched events are **never** allowed to confirm bookings or record payments, are written immutably to `audit_logs` (`payment.webhook_livemode_mismatch`), and are persisted as `REJECTED` in `payment_webhook_events` so duplicate deliveries return idempotently (`duplicate: true`) without reprocessing.
  - Checkout `success_url` and `cancel_url` are derived strictly on the Go server from configured `LOVELACE_PUBLIC_ORIGIN` (e.g., `https://book.example.com`) and validated against an HTTPS/domain allowlist; client-supplied redirect URLs are rejected (`422`).
  - If online payments are intentionally disabled for all properties (`property_booking_policies.stripe_enabled=false`), production startup and `/readyz` succeed with `payments.status="disabled"`, while public checkout refuses initiation (`409 stripe_disabled`). When any active public property has `stripe_enabled=true`, missing, placeholder, or test-mode Stripe secrets fail startup closed.
  - Non-production Sandbox Mode: `LOVELACE_STRIPE_SANDBOX=true` (blocked in `LOVELACE_ENV=production`) routes guests through `/checkout/sandbox` to trigger locally signed HMAC webhooks (`livemode: false`) without external network keys.
- **Mandatory Transactional Email Policy for Live Public Bookings:** Confirmation emails are queued idempotently inside `email_outbox` and claimed atomically via `FOR UPDATE SKIP LOCKED`. Without `LOVELACE_SMTP_ADDR` configured, outbox items **remain `PENDING`** (never silently marked `SENT` by a log sink). In `production`:
  - **Live public bookings (`stripe_enabled=true`) ALWAYS require a genuine SMTP provider**: startup and `/readyz` (`email.status="misconfigured"`) fail closed AND live checkout refuses initiation if SMTP is missing, regardless of `LOVELACE_REQUIRE_EMAIL`.
  - **Administration-only or payments-disabled mode (`stripe_enabled=false` across properties)**: may operate without SMTP only when `LOVELACE_REQUIRE_EMAIL=false` (`docker-compose.yml` defaults `LOVELACE_REQUIRE_EMAIL` to `true`).
  - Permanent SMTP failures (e.g., 5xx) move items immediately to `FAILED`; transient errors remain retryable with backoff.

---

## 6. Phase 3 Milestone 1 (+ 3.1) Operations Centre, Housekeeping & Permissions

- **Operations Centre (`GET /`, `GET /operations`, `GET /api/v1/properties/{propertyID}/operations`):**
  - Default operational dashboard answering who is on site/arriving/departing, what needs cleaning or inspection, and what requires urgent operational attention for a selected operational date (`?date=YYYY-MM-DD`, views `focus=all|front_desk|housekeeping|attention`).
  - **Arrival & Inquiry Separation:** Daily arrival counts (`arrivals_total`, `occupancy.arrivals_today`) and same-day turnaround checks include **accepted reservations only** (`CONFIRMED` and `CHECKED_IN`). Provisional `PENDING` arrivals and unconfirmed `INQUIRY` requests are displayed separately so unconfirmed inquiries never inflate expected check-ins.
  - **5-Stage Daily Changeover Workflow (`housekeeping_tasks` & `units.housekeeping_status`):**
    1. `GUEST_DEPARTING` — Checked-in guests scheduled or overdue to depart on the operational date.
    2. `CLEANING_REQUIRED` — Automatically queued when a reservation transitions to `CHECKED_OUT` (same-day turnaround units receive `URGENT` priority), or created manually via `POST /operations/housekeeping/tasks`. Checked-out units are never marked ready automatically.
    3. `CLEANING_IN_PROGRESS` — Records `started_at` / `started_by`.
    4. `INSPECTION_REQUIRED` — Cleaning complete and awaiting sign-off (`inspected_at` remains unset while inspection is pending).
    5. `READY` — Inspection approved: records `inspected_at`, `inspected_by`, `completed_at`, and `completed_by`. Returning a task from `READY` (or `INSPECTION_REQUIRED`) back to `CLEANING_REQUIRED` clears `inspected_at`, `inspected_by`, `completed_at`, and `completed_by` so stale inspection data is invalidated while prior stage transitions remain preserved in `audit_logs`.
  - **Maintenance Independence:** Unit housekeeping stages never add, alter, or remove `inventory_claims` (`MAINTENANCE`, `WEATHER_CLOSURE`, etc.) or `units.status`.
- **Payment Exception Management (`GET /payment-exceptions`, `POST /payment-exceptions/{id}/resolve`, `POST /api/v1/properties/{propertyID}/payment-exceptions/{id}/resolve`):**
  - Open payment reconciliation exceptions without a linked reservation route staff directly to `/payment-exceptions#ex-{id}` (linked exceptions route to `/reservations/{reservationID}`), avoiding dead links. Resolving requires non-empty `resolution_notes`, transitions `OPEN -> MANUALLY_RESOLVED`, records `resolved_by` and `resolved_at`, and writes an immutable `payment_exception.resolved` audit entry.
- **Operational RBAC Matrix (`internal/auth/rbac.go`):**
  - `ops:read` (`PermOpsRead`): granted to `owner_admin`, `manager`, `staff`, `maintenance_staff`, `housekeeping_staff`, `viewer`.
  - `housekeeping:write` (`PermHousekeepingWrite`): granted to `owner_admin`, `manager`, `staff`, `housekeeping_staff` (denied with `403` to `maintenance_staff` and `viewer`). Returning a changeover task to `CLEANING_REQUIRED` clears `started_at`, `started_by`, `inspected_at`, `inspected_by`, `completed_at`, and `completed_by` while preserving prior stage transitions in `audit_logs`.
  - `reservations:read` / `reservations:write`: required to view/resolve payment exceptions (`owner_admin`, `manager`, `staff`; `reservations:read` also on `viewer`).

---

## 7. Phase 3 Milestone 2 — Maintenance Management, Attachment Storage & RBAC

- **Attachment Volume, Readiness & Backup (`lovelace_attachments`):**
  - Uploaded repair photographs and inspection documents are stored under `LOVELACE_ATTACHMENT_DIR` (`/var/lib/lovelace/attachments`, mounted via the `lovelace_attachments` Docker volume in `docker-compose.yml` and `docker-compose.dev.yml`).
  - Back up `LOVELACE_ATTACHMENT_DIR` alongside PostgreSQL `pg_dump` snapshots so metadata in `maintenance_attachments` stays consistent with blobs on disk during disaster recovery.
  - At startup and on `/readyz`, LOVELACE verifies that `LOVELACE_ATTACHMENT_DIR` exists, is a directory, and is writable (creating and deleting a transient `.healthcheck-*` probe). Unwritable or misconfigured attachment paths fail `ValidateProductionStartup` in production and report `attachments.status = "unhealthy"` (`HTTP 503` on `/readyz`).
- **Maintenance RBAC Matrix (`internal/auth/rbac.go`):**
  - `owner_admin`, `manager`: full maintenance access (`maintenance:read`, `maintenance:report`, `maintenance:write`, `maintenance:inspect`, `maintenance:manage`, plus `blocks:write` for inventory restrictions).
  - `staff`: `maintenance:read`, `maintenance:report`, `maintenance:write`, `blocks:write` (can report, update, add work notes, and trigger restrictions, but cannot record inspection sign-off, manage preventative schedules/facilities metadata, or manually release/manage restricted admin settings).
  - `maintenance_staff`: `ops:read`, `blocks:read`, `maintenance:read`, `maintenance:report`, `maintenance:write` (cannot inspect/approve own repairs, manage inventory releases without `blocks:write`, or mutate reservations/rates/channels/users).
  - `housekeeping_staff`: `ops:read`, `housekeeping:write`, `maintenance:read`, `maintenance:report` (can work the changeover board and report faults, cannot mutate maintenance repairs/schedules/restrictions).
  - `viewer`: read-only (`maintenance:read`, `ops:read`, zero mutation permissions).
- **Inspection & Reopen Policy (Phase 3.2.1):**
  - When `inspection_required = TRUE`, closing a completed request (`CLOSED`) requires a `PASSED` inspection whose `inspected_at` occurred **after** the current repair cycle's `completed_at`.
  - Reopening a completed or closed request clears `completed_at` and `closed_at` (invalidating the prior cycle's inspection approval) while preserving all historical rows in `maintenance_inspections` for auditability.
- **Preventative Maintenance Worker & Overdue Catch-Up Policy (Phase 3.2.1 / 3.2.2):**
  - `cmd/server/main.go` sweeps due `maintenance_schedules` every 30 seconds under `FOR UPDATE OF ms SKIP LOCKED` with `uq_maintenance_schedule_occurrence` (`schedule_id`, `scheduled_for_date`) uniqueness so retries never duplicate requests.
  - **Assignee Re-Validation at Sweep Time:** Before assigning generated requests, `AcquireDueSchedulesAndGenerateRequests` re-validates both the default staff member (must remain active and belong to the property or hold a global scope) and contractor (must remain active and belong to the property). Deactivated or cross-property assignees are cleared with `[Schedule Assignment Warning]` notes, falling back to `REPORTED` when neither assignee remains valid.
  - **Calendar-Aware Stepping & Anchor Preservation:** Calendar frequencies (`MONTHLY`, `QUARTERLY`, `SIX_MONTHLY`, `ANNUALLY`) clamp to the final day of shorter months (e.g., Jan 31 -> Feb 28/29; Feb 29 -> Feb 28 in non-leap years) while preserving `anchor_day` so subsequent full months return to the intended day (e.g., Mar 31; Feb 29 on leap years).
  - **Consolidated Overdue Catch-Up Policy:** If a recurring schedule falls substantially overdue (e.g., weeks or years behind), a sweep never emits an uncontrolled burst of historical work orders across every 30-second tick. Instead, it deterministically walks missed dates up to the run date (`asOfDate`), emits **at most one consolidated work order** stamped with the most recent aligned due date (`<= asOfDate`), records the count of skipped overdue intervals in status history and internal notes, preserves all previously generated historical requests untouched, and advances `next_due_date` strictly into the future (`> asOfDate`). One-off (`ONCE`) schedules run once and deactivate (`is_active = FALSE`).

---

## 8. Phase 3 Milestone 3 — Guest CRM, Communications Automation & GDPR Operations

- **Automated Communication Sweeper (`cmd/server/main.go`):**
  - Alongside `DispatchPendingEmails` and `SweepDueMaintenanceSchedules`, the 30-second background loop calls `SweepDueCommunicationJobs` (`FOR UPDATE OF cj SKIP LOCKED`) to render versioned property templates for due pre-arrival, check-in, and post-stay jobs into `email_outbox` + `guest_communications`.
  - When bookings are cancelled, checked out, or have stay dates amended, `SyncReservationAutomationJobs` cancels or supersedes stale `SCHEDULED` jobs immediately so cancelled guests never receive pre-arrival reminders.
- **Email Delivery Truthfulness & Webhook Configuration (`LOVELACE_EMAIL_WEBHOOK_SECRET`):**
  - `MarkOutboxEmailSent` records `guest_communications.delivery_status = 'ACCEPTED'` upon SMTP relay handoff (not `DELIVERED`).
  - Downstream provider `DELIVERED`, `BOUNCED`, `FAILED`, and `COMPLAINT` events arrive at `POST /api/v1/webhooks/email-events` signed with HMAC-SHA256 in `X-Email-Signature` (`sha256=<hex>` using `LOVELACE_EMAIL_WEBHOOK_SECRET`), deduplicated idempotently in `communication_delivery_events`.
  - Permanent failures (`FAILED`/`BOUNCED`) and uncertain timed-out dispatches (`DISPATCH_UNKNOWN`) surface in the Operations Centre **Email Delivery & Communication Alerts** panel and `/communications/settings` (`DISPATCH_UNKNOWN` cannot be auto-retried and exposes reconciliation only to roles holding `comms:reconcile`).
- **Active Dispatches, Worker Liveness Fencing, SMTP Timeouts & Unknown Dispatch Reconciliation (`0015` / `0016` / `0017`):**
  - **Worker Lease, DB Clock Authority & Heartbeat Liveness:** `BeginOutboxDispatchLease` claims `PROCESSING -> DISPATCHING` with a monotonic `dispatch_fence_epoch`, `dispatch_worker_id`, and renewable `dispatch_lease_expires_at`. Canonical production defaults (`service.DefaultDispatchConfig()`) are **`60s` `LeaseTTL`**, **`20s` `HeartbeatInterval`** (one-third of `LeaseTTL`), and **`30s` `TransportTimeout`** (`ValidateDispatchConfig` enforces `0 < HeartbeatInterval <= LeaseTTL/2` and `0 < TransportTimeout < LeaseTTL`, and bounds `SMTPSender.Timeout` strictly within the dispatch context deadline). Production lease creation, `RenewOutboxDispatchHeartbeat`, `SweepStaleOutboxDispatches`, settlement, and reconciliation evaluate **PostgreSQL `clock_timestamp()`** as the single distributed clock authority so worker host clock skew cannot prematurely fail or extend leases. `RenewOutboxDispatchHeartbeat` only extends leases where `dispatch_lease_expires_at > clock_timestamp()` is still true; expired or fenced workers cannot renew, settle, retry, or overwrite the row even before the sweeper runs. Any heartbeat rejection (`renewed = false`) or heartbeat DB error/timeout cancels `sendCtx` immediately, halting external transport and keeping local active registration held until both transport termination and PG state transition finish. While a worker renews cleanly, `SweepStaleOutboxDispatches` never fences it based solely on elapsed `dispatched_at`; expired leases (`dispatch_lease_expires_at <= clock_timestamp()`) are fenced atomically (`dispatch_fenced_at`, incremented `dispatch_fence_epoch`, `dispatch_outcome='WORKER_FENCED'`) into `DISPATCH_UNKNOWN`.
  - **Bounded Context-Aware SMTP & Pre/Post-DATA Semantics:** `SMTPSender.Send(ctx, msg)` caps `Timeout` to the remaining parent deadline, dials with timeout, and closes the underlying TCP socket immediately on `ctx.Done()`. Aborts before `DATA` submission return `ErrTerminatedBeforeSend` (`dispatch_outcome='TERMINATED_BEFORE_SEND'`), allowing clean retry or immediate `CANCELLED` if `guests.erasure_requested_at` is set; timeouts/disconnects after `DATA` starts return `ErrSendUncertain` (`dispatch_outcome='UNCERTAIN_TIMEOUT'`), transitioning deterministically to `DISPATCH_UNKNOWN`.
  - **Safe Upgrade Procedure for Migrations `0016`–`0018` (Outbox Fencing & Audit Snapshot Validation):**
    1. Stop existing application/outbox worker instances cleanly before upgrading (`docker compose stop backend`).
    2. Verify no pre-hardening outbox workers remain running against the database so no legacy unleased worker continues an external SMTP transaction without heartbeat fencing.
    3. Take a full PostgreSQL backup (`pg_dump -Fc`) prior to schema changes.
    4. Run `lovelace-migrate` (`lovelace_migrator`) cleanly to apply pending migrations, including `0018_phase3_3_2_final_acceptance.sql`. Note: migration `0016` backfills `dispatch_lease_expires_at = dispatched_at + INTERVAL '60 seconds'` on any pre-existing `DISPATCHING` rows missing an expiry timestamp; this backfill is intended strictly for quiet/upgraded deployments rather than live pre-0016 workers.
    5. Deploy the hardened `noteG-server` binary / container images (`lovelace_app`).
    6. Start the hardened workers.
    7. Immediately inspect `email_outbox` for any `DISPATCHING` or `DISPATCH_UNKNOWN` items carried across the upgrade window (`SELECT id, property_id, status, dispatched_at, dispatch_lease_expires_at, dispatch_outcome FROM email_outbox WHERE status IN ('DISPATCHING', 'DISPATCH_UNKNOWN')`).
    8. Reconcile any uncertain `DISPATCH_UNKNOWN` items **exclusively** using verified external SMTP/MTA or provider webhook evidence (`CONFIRMED_SENT`, `CONFIRMED_FAILED`, or `CANCEL`) — **never** auto-cancel or auto-retry `DISPATCH_UNKNOWN` items blindly.
    9. Confirm any pending `guests.erasure_requested_at` anonymisation barriers resolve only after all active/unknown dispatches for the guest cluster are durably settled or reconciled.
  - **Reconciliation Permissions, Live-Dispatch Guard & Required Evidence:** Manual reconciliation (`POST /api/v1/properties/{propertyID}/outbox/{outboxID}/reconcile` or `POST /communications/outbox/{outboxID}/reconcile`) requires `comms:reconcile` (`PermCommsReconcile`, granted exclusively to `owner_admin` and `manager`; denied with `403` to `staff` and hidden from the staff UI), an active non-rotating operator within property scope, no live active dispatch lease (`409` if `DISPATCHING` or active local worker), a non-PII `reason_code`, concrete `evidence_type` + non-PII `evidence_ref` + non-PII `note` (`CONFIRMED_FAILED` / `CANCEL` strictly require `MTA_REJECTED`, `PROVIDER_WEBHOOK_FAILED`, or `PROVIDER_NOT_SUBMITTED`; `CONFIRMED_SENT` accepts `MTA_ACCEPTED`, `PROVIDER_WEBHOOK_ACCEPTED`, or `CONSERVATIVE_ASSUME_SENT`). Every reconciliation records actor, timestamp, decision, reason code, and evidence in `email_outbox.reconciled_*`, `communication_delivery_events`, and `audit_logs` (`email_outbox.reconciled`).
  - **Pending Erasure Barriers & Non-Recallability:** When `AnonymizeGuest` encounters `DISPATCHING` or `DISPATCH_UNKNOWN` items for a guest cluster, it persists `guests.erasure_requested_at` (blocking new queueing, retries, and scheduled jobs immediately) and returns `409 Conflict` until active transports finish and any `DISPATCH_UNKNOWN` items are reconciled. Email accepted by an external SMTP relay or provider (`SENT` / `ACCEPTED`) cannot be recalled remotely.
- **Guest CRM RBAC & Governance:**
  - `housekeeping_staff` and `maintenance_staff` have zero `guests:*` or `comms:*` permissions (`403` on direct routes and `GuestCRM.Visible = false` on Operations Centre). `viewer` receives `guests:read` only (internal notes, enquiries, communications, consent history, and merge snapshots redacted). `staff` can read/write operational profiles (`ACTIVE`, `VIP`, `DO_NOT_CONTACT`), enquiries, and emails (without internal notes, merge snapshots, or `comms:reconcile`), while `owner_admin` and `manager` hold `guests:merge`, `guests:export`, `guests:anonymize`, `comms:templates`, and `comms:reconcile`.
  - `AnonymizeGuest` resolves merged aliases to their canonical guest, refuses (`409`) if any cluster member holds an active upcoming/checked-in stay or active/unknown email dispatch (`DISPATCHING` / `DISPATCH_UNKNOWN`), and scrubs personal identifiers across canonical + merged `guests`, `reservations` contact/note/cancellation fields, `booking_holds`, `guest_preferences`, `guest_notes`, `guest_merge_events.source_snapshot`, `guest_enquiries`, `guest_enquiry_messages`, `guest_communications`, `communication_delivery_events`, and `email_outbox` while preserving reservation amounts, payment ledgers, and tax/invoice records.
  - `ExportGuestData` provides an unbounded operational CRM export (with `export_scope_notice` distinguishing CRM portability from operator-reviewed Subject Access Request screening); configured retention policy windows in `property_retention_policies` are advisory only (`auto_anonymize_enabled=true` is rejected until a reviewed retention worker exists).
- **Deferred Scope Preserved:** Phase 3 Milestone 4 (Public Self-Service Guest Portal) and Phase 5 (Historical/Financial BI Analytics) remain intentionally out of Phase 4 scope.

---

## 9. Phase 4 Live OTA & Channel Sync Operations (`0019_phase4_live_ota_sync.sql`)

- **Connection Verification & Disconnect Governance:**
  - Saving credentials persists `PENDING_VERIFICATION` or `CONFIGURED` (never `CONNECTED` directly — explicit API `422` rejection). Promotion to `CONNECTED` happens exclusively via `POST /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/verify` after a live probe against the adapter succeeds and persists the capability matrix (`capability_statuses`), `last_verified_at`, and any exchanged token expiry (`token_expires_at`).
  - Disconnecting (`POST .../disconnect`) always clears local encrypted credentials and resets status to `DISABLED`, separates remote webhook deletion status (`SUPPORTED` / `BLOCKED` / `NOT_IMPLEMENTED` / `NOT_APPLICABLE`) from remote credential revocation (`NOT_IMPLEMENTED` `NO_REMOTE_REVOKE`), preserves `public_config["webhook_id"]` and surfaces safe `webhook_error_code` if webhook deletion fails so orphaned remote webhooks remain visible, and clears `webhook_id` only after remote deletion succeeds (operators must still revoke API keys in Beds24, Smoobu, Lodgify, or Channex dashboards).
- **Explicit Property-Scoped Mappings (`channel_listing_mappings`):**
  - Every listing mapping ties a property-owned `channel_connection_id` to a `unit_type_id` (optional `unit_id` and `rate_plan_id`), active `booking_channel_code`, matching ISO-4217 property `currency`, IANA `timezone`, sync toggles (`sync_availability`, `sync_rates`, `sync_reservations`), and explicit `ownership_mode` (`LOVELACE_ARI_PROVIDER_BOOKINGS` or `READ_ONLY_IMPORT`). PostgreSQL trigger `trg_validate_channel_listing_mapping` blocks cross-property references at the database boundary.
- **Preview-Before-Sync & High-Blast-Radius Safeguards:**
  - Operators preview affected mappings and date counts via `POST .../sync/preview` before pushing ARI updates.
  - Multi-listing pushes or date windows exceeding `14` days require explicit `"confirm_scope": true`; otherwise `POST .../sync` fails closed with `422`.
  - Attempting outbound availability pushes on `lodgify` fails closed (`422 capability_blocked`) unless Partner Connectivity support is implemented.
- **Durable Leased Worker Loop & Checkpoint Loop Prevention:**
  - `cmd/server/main.go` ticks `ProcessPendingChannelSyncJobs` on the background worker interval alongside outbox/maintenance/comms sweepers (`FOR UPDATE OF j SKIP LOCKED`).
  - Active jobs acquire a `lease_token`, monotonic `fence_epoch`, and DB-clock `lease_expires_at` (`45s` TTL, heartbeat renewal every `15s`). Any renewal failure cancels the execution context; stale/fenced workers cannot overwrite job completion, connection telemetry, or sync audit records.
  - Outbound availability/rate pushes compute canonical SHA-256 request digests stored per checkpoint key in `channel_sync_checkpoints`, skipping redundant pushes when state has not changed.
- **Webhook Ingress, Rotation & Overbooking Conflict Triage:**
  - Provider callbacks hit `POST /api/v1/webhooks/channels/{providerCode}/{connectionID}` (unauthenticated publicly, strictly signature/secret verified: `channel_hmac_nonces` for replay defense, `<= 5m` timestamp skew, and Channex `webhook_secret` / `webhook_secret_previous` dual-secret overlap for zero-downtime key rotation).
  - Verified webhooks deduplicate via `(channel_connection_id, external_event_id)` in `channel_webhook_events`, persist only safe metadata (`safe_summary` with zero guest PII or credentials), and enqueue asynchronous hydration jobs so authoritative reservation details come directly from the provider API (Channex revisions execute `/ack` only after durable DB commit).
  - Imported bookings honor `excl_inventory_claims_no_overlap`: overlapping arrivals never create phantom duplicate claims; instead they record `import_status = 'CONFLICT_OVERBOOKED'` (`conflict_reason = 'OVERBOOKING_INVENTORY_CONFLICT'`) in `channel_external_reservations`, surface in `/settings/integrations/channel-manager` and `GET .../health`, and remain excluded from Channex `/ack` until resolved or triaged. Stale out-of-order updates (`external_updated_at` older than stored timestamp) are safely ignored.

---

## 10. Phase 5 Reporting, Forecasting & Accounting Export Operations (`0020_phase5_reporting_bi.sql`, `0021_phase5_accuracy_remediation.sql`)

- **Half-Open Calendar Windows & Timezone Alignment:** All `/reports` web console queries and `/api/v1/properties/{propertyID}/reports/*` API routes use `[start_date, end_date)` (`end_date > start_date`, bounded at `366` days max; forecasts bounded `1..180` days). Stay nights use the property's calendar `DATE`; cash-flow payments (`paid_at`) and persisted refunds (`occurred_at`) convert via PostgreSQL `AT TIME ZONE properties.timezone` so late-evening UTC events align cleanly with BST/GMT business dates.
- **Interpreting Completeness Warnings:** Every overview, occupancy, revenue, channel, and forecast report includes `Completeness.Warnings` surfacing:
  - Phase 4 `UNMAPPED`, `CONFLICT_OVERBOOKED`, or `FAILED` `channel_external_reservations` so operators never assume OTA imports are complete while conflicts remain open.
  - Pre-`0021` unit history gaps (`inventory_history_incomplete` / `inventory_history_reliable_from`) when queried windows overlap units whose pre-migration status transitions cannot be reconstructed from audit history.
  - Legacy `REFUNDED`/`PARTIALLY_REFUNDED` payments lacking a persisted `refunds` row (`unresolved_legacy_refund_payments_count`), which are excluded from dated refund totals rather than misattributed onto `payments.paid_at`.
  - Manual unquoted reservations using `reservations.total_pence`, zero-value accepted stays, open `payment_exceptions`, and mixed currencies (forecasts apportion projected incremental nights across currencies proportionally via `allocated_additional_nights`).
- **Accounting Export Reconciliation (`lovelace.accounting.v1`):** Download `booking_line_items`, `payment_ledger`, or `refund_ledger` CSVs from `/reports` (or API `accounting-export`). Columns requiring external payout settlement data (`ota_commission_pence`, `payout_settlement_date`, `net_of_fee_settlement_pence`) intentionally emit `NOT IMPLEMENTED` until payout reconciliation is introduced in a future milestone.
- **Cross-Cutting Security & Container Hardening (`0029_security_and_integrity_hardening.sql`):** Production `docker-compose.yml` isolates `postgres`/`db-bootstrap`/`migrate` onto `backend_net` so `public-web` (`bff_net` only) has zero network path to PostgreSQL, drops all Linux capabilities (`cap_drop: [ALL]`, `no-new-privileges:true`), gates `public-web` startup on `noteG-server -healthcheck`, and preserves least-privilege `lovelace_app` grants idempotently across `scripts/postgres/ensure-roles.sh` and `0029_security_and_integrity_hardening.sql`.
