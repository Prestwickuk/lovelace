# Guide 6: Complete Feature, Tool, CLI, API & Permission Reference

This reference manual indexes **every feature, WebUI screen, CLI binary, helper script, background worker, inbound webhook, REST API family, RBAC permission, template variable, Next.js storefront/Guest Portal route, and WordPress integration** in the **Lovelace Hospitality Platform**.

For plain-English everyday instructions, see the **[Property Owner's Everyday Handbook](../owner-handbook/README.md)**.

---

## 1. CLI Binaries & Command-Line Flags

Lovelace compiles three static `CGO_ENABLED=0` Go binaries (`cmd/`):

### 1.1 `noteG-server` (`./cmd/server`)
Named in homage to Ada Lovelace's 1843 *Note G*, `noteG-server` runs the Admin Web Console, Admin REST API (`/api/v1/*`), internal Public Booking & Guest Portal APIs (`/api/public/v1/*`), signed webhook receivers, and all 8 background workers.

| Flag | Default | Purpose |
| :--- | :--- | :--- |
| *(none)* | — | Starts the HTTP server (`LOVELACE_LISTEN_ADDR`), verifies PostgreSQL role least-privilege (`lovelace_app`), verifies applied migration checksums (`0001`–`0031`), runs `ValidateProductionStartup`, and launches background tickers. |
| `-healthcheck` | `false` | Probes local `http://<LOVELACE_LISTEN_ADDR>/readyz` (`3s` timeout) and exits `0` on `200 OK` or `1` otherwise. Used by `docker-compose.yml` to gate `public-web` startup. |

### 1.2 `lovelace-migrate` (`./cmd/migrate`)
Applies embedded forward-only SQL migrations (`migrations/0001_init.sql` through `0031_system_backups_and_recovery.sql`) transactionally into `schema_migrations` using `MIGRATION_DATABASE_URL` (`lovelace_migrator` DDL owner role). Takes zero CLI flags; validates historical checksums (`acceptedHistoricalChecksums` in `internal/store/postgres/migrate.go`) to prevent tampering.

### 1.3 `lovelace-seed` (`./cmd/seed`)
Provisions either an initial `owner_admin` account only (`-admin-only` for empty production/staging bootstrap) or a complete demo property (`Meadowbrook Estate`, slug `demo-property`) for local evaluation.

| Flag | Env Fallback | Description |
| :--- | :--- | :--- |
| `-admin-email` | `LOVELACE_ADMIN_EMAIL` / `LOVELACE_BOOTSTRAP_ADMIN_EMAIL` | Initial `owner_admin` sign-in email. |
| `-admin-name` | `LOVELACE_ADMIN_NAME` / `LOVELACE_BOOTSTRAP_ADMIN_NAME` | Display name (defaults to `Owner Administrator`). |
| `-admin-password` | `LOVELACE_ADMIN_PASSWORD` / `LOVELACE_BOOTSTRAP_ADMIN_PASSWORD` | Temporary password (`>= 12` chars; never overwrites an existing account). |
| `-admin-only` | `LOVELACE_SEED_ADMIN_ONLY=true` | Creates **only** the initial Global `owner_admin` (`must_rotate_password=true`) when `admin_users` has `0` rows — seeds zero demo property/unit data. |

---

## 2. Operational Shell Scripts (`scripts/` & `deploy/`)

| Script Path | Target Role / Context | Purpose |
| :--- | :--- | :--- |
| `scripts/postgres/ensure-roles.sh` | Bootstrap admin (`postgres` / cloud admin) | Idempotently enables `pgcrypto` + `btree_gist`, creates/updates non-superuser `lovelace_migrator` (DDL owner) and `lovelace_app` (strict DML-only: `SELECT, INSERT, UPDATE, DELETE`, zero DDL/ownership), and configures `ALTER DEFAULT PRIVILEGES`. Safe to re-run after every migration. |
| `deploy/postgres/01-init-roles.sh` | Docker entrypoint (`/docker-entrypoint-initdb.d`) | Runs automatically on first initialization of an empty Docker `PGDATA` volume (`docker-compose.yml` & `docker-compose.dev.yml`). |
| `scripts/setup-test-db.sh` | Local dev/CI PostgreSQL | Provisions disposable `lovelace_test` database and non-superuser `lovelace_test_runner` role for `go test ./...`. Refuses non-`_test` database names. |
| `scripts/test-migration-upgrade.sh` | `lovelace_migrator` on empty `*_test` DB | Verifies forward migration upgrades (`0001`–`0017` + historical audit/redaction fixtures → `0018`–`0031`) without mutating raw `audit_logs`. |

---

## 3. Background Workers Inside `noteG-server`

`cmd/server/main.go` runs a single coordinated background loop (`30s` base tick) using `FOR UPDATE SKIP LOCKED` and DB-clock (`clock_timestamp()`) leases:

| # | Worker Method | Cadence | Step Timeout | What It Does |
| :--- | :--- | :--- | :--- | :--- |
| 1 | `SweepExpiredHolds` | Every `30s` | `10s` | Expires `ACTIVE` `booking_holds` past `expires_at`, releases `HOLD` `inventory_claims`, and marks unpaid `CREATED`/`REQUIRES_ACTION` `payment_attempts` `EXPIRED`. |
| 2 | `DispatchPendingEmails` | Every `30s` | `20s` | Claims due `email_outbox` rows under a `60s` DB-clock lease (`20s` heartbeat, `30s` SMTP timeout), sends via `SMTPSender`, marks `SENT` (`ACCEPTED` in `guest_communications`), or fences uncertain post-`DATA` drops into `DISPATCH_UNKNOWN`. |
| 3 | `SweepDueMaintenanceSchedules` | Every `30s` | `10s` | Generates `MNT-YYYY-NNNN` work orders from due `maintenance_schedules` (`ONCE`..`ANNUALLY`), re-validating active assignees, preserving `anchor_day`, and consolidating overdue catch-ups into a single ticket. |
| 4 | `SweepDueCommunicationJobs` | Every `30s` | `10s` | Renders versioned templates for due `communication_jobs` (`PRE_ARRIVAL`, `ARRIVAL_INFO`, `DEPARTURE_INSTRUCTIONS`, `POST_STAY_THANK_YOU`, `PAYMENT_REMINDER`) into `email_outbox` while skipping cancelled/erasure-frozen guests. |
| 5 | `ProcessPendingChannelSyncJobs` | Every `30s` (`batch=10`) | `20s` | Processes leased (`45s` TTL, `15s` heartbeat) Phase 4 OTA jobs (`VERIFY_CONNECTION`, `DISCOVER_LISTINGS`, `PUSH_AVAILABILITY`, `PUSH_RATES`, `PULL_RESERVATIONS`, `PROCESS_WEBHOOK_EVENT`) with SHA-256 checkpoint deduplication and post-commit Channex `/ack`. |
| 6 | `PruneExpiredPortalState` | Every `30s` | `10s` | Hard-deletes expired Guest Portal OTP/magic-link challenges and sessions `5` minutes past expiry (`0` IP/UA telemetry retained). |
| 7 | `SweepScheduledBackups` | Every `2m` (`tick % 4 == 1`) | `60s` | Checks active `backup_targets` with `schedule_enabled=true`, builds `.zip` / Argon2id `.zip.enc` snapshots, uploads to S3/R2/GCS/Azure Blob, and prunes older objects beyond `retention_count`. |
| 8 | `SweepStaleAuthState` | Every `60m` (`tick % 120 == 1`) | `10s` | Purges expired `admin_sessions` and stale `login_attempts` outside the sliding brute-force window. |

---

## 4. Complete Admin WebUI Screen & Tool Directory (`internal/webui`)

All Admin WebUI routes enforce `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; object-src 'none'; frame-ancestors 'none'`, `X-Frame-Options: DENY`, `Cache-Control: no-store`, session authentication (`lovelace_session`), and per-session `csrf_token` validation.

| Screen / Route | Template(s) | Required Permission / Access | Key Capabilities & Form Actions |
| :--- | :--- | :--- | :--- |
| **Sign In & Sign Out** (`/login`, `POST /logout`) | `login.html` | Public (`Origin` verified) | Argon2id login with IP/account brute-force rate limiting; session revocation on logout. |
| **Password Recovery** (`/login/recover`) | `recover.html` | Public (`Origin` verified) | Email-free password reset using one of 8 single-use `HMAC-SHA256` recovery codes. |
| **My Account & Personalisation** (`/account`) | `account.html` | Any authenticated user | Mandatory first-login password rotation (`?rotate=1`), 8 recovery code generation/download, PWE-1 global theme (`system`/`light`/`dark`), 5 accents, text scale (`default`/`large`/`xl`), high contrast, reduced motion, up to 6 pinned shortcuts, per-property workspace overrides, and saved view management. |
| **Guided Property Setup Wizard** (`/onboarding`) | `onboarding.html`, `no_property.html` | Global `owner_admin` (new) / `property:write` | 7-step guided wizard (`?new=1` for additional properties): venue preset, bulk unit generator (`1..50`), VAT/uplifts/extras, deposits/hold timer/policies, staff invites, cloud backup target, WordPress `.zip` & HTML embeds. |
| **Operations Centre** (`/`, `/operations`) | `dashboard.html` | `ops:read` | Date-selectable (`?date=YYYY-MM-DD`) live occupancy, confirmed arrivals/departures, arrival dietary/room preferences, pending/inquiry follow-ups, 5-stage Daily Changeover board (`housekeeping:write`), maintenance backlog, failed/unknown email alerts, and open payment exceptions. |
| **Personal Workspace** (`/workspace`) | `workspace.html` | Authenticated (`ops:read`) | PWE-3/PWE-5 role profile switcher (`owner_executive`, `duty_manager`, `front_desk`, `housekeeping_lead`, `maintenance_ops`, `readonly_observer`) and `?edit=1` custom widget layout builder (`16` widget max, permission-filtered). |
| **Booking Calendar** (`/calendar`) | `calendar.html` | `reservations:read` / `inventory:read` | Horizon (`day`, `7d`, `14d`, `30d`, `month`) & unit vs unit-type grouping grid showing `Open`, `HOLD`, `CONFIRMED`/`CHECKED_IN`, `BLOCKED`, and `UNRELEASED` states + saved views. |
| **Reservations & Ledger** (`/reservations`, `/reservations/new`, `/reservations/{id}`) | `reservations.html`, `reservation_new.html`, `reservation_detail.html` | `reservations:read` (`:write` to mutate) | Multi-unit manual/phone reservation creation, status state machine (`INQUIRY`, `PENDING`, `CONFIRMED`, `CHECKED_IN`, `CHECKED_OUT`, `CANCELLED`, `NO_SHOW`), date/unit/party amendments, quote breakdown, and payment/refund ledger. |
| **Payment Exceptions** (`/payment-exceptions`) | `payment_exceptions.html` | `reservations:read` (`:write` to resolve) | Lists `EXPIRED_HOLD_PAID`, `OVERLAP_ON_CONFIRM`, and `AMOUNT_MISMATCH` Stripe webhook exceptions + audited manual resolution (`OPEN -> MANUALLY_RESOLVED`). |
| **Unit Types** (`/unit-types`) | `unit_types.html` | `inventory:read` (`:write` to mutate) | Create/update/archive accommodation categories (`cabin`, `glamping`, `tent_pitch`, `touring_pitch`, `other`), capacities, pet rules, min/max nights, base pence rate, and tax BPS. |
| **Units & Pitches** (`/units`) | `units.html` | `inventory:read` (`:write` to mutate) | Create/update/archive physical bookable spaces, zone labels, operational status (`active`, `maintenance`, `inactive`, `retired`), access notes, and operational notes. |
| **Extras & Add-ons** (`/extras`) | `extras.html` | `inventory:read` (`:write` to mutate) | Manage optional guest extras (`per_stay`, `per_night`, `per_person_per_stay`, `per_person_per_night`), max quantities, and tax BPS. |
| **Rates & Tariff Preview** (`/rates`) | `rates.html` | `rates:read` (`:write` to mutate) | Interactive night-by-night **Tariff Preview Calculator**, rate plans, and seasonal/day-of-week `rate_rules` (bitmask `days_of_week`, `min_nights_override`, priority). |
| **Bulk Blockouts** (`/blocks`) | `blocks.html` | `blocks:read` (`:write` to mutate) | Conflict-checked dry-run preview & atomic commit across `UNIT`, `UNITS`, `UNIT_TYPE`, or `PROPERTY` scopes (`OWNER_USE`, `MAINTENANCE`, `SEASONAL_CLOSURE`, `WEATHER_CLOSURE`, `OPERATIONAL_BUFFER`, `ADMINISTRATIVE_BLOCK`), partial/full release, reinstate, and date resize. |
| **Scheduled Releases** (`/releases`) | `releases.html` | `releases:read` (`:write` to mutate) | Hold future inventory windows (`PROPERTY`, `UNIT_TYPE`, `UNIT`) per channel (`ALL` or specific OTA) until a local timestamp `release_at`, with one-click `Release Now`. |
| **Maintenance Work Orders** (`/maintenance`, `/maintenance/new`, `/maintenance/{id}`) | `maintenance_list.html`, `maintenance_new.html`, `maintenance_detail.html` | `maintenance:read` (`:report`, `:write`, `:inspect`) | `MNT-YYYY-NNNN` reactive/preventative tickets, availability impact (`NONE`, `PARTIAL`, `UNAVAILABLE`), append-only work notes, magic-byte validated photo/PDF attachments (`50 MiB` max), `PASSED`/`FAILED` inspection sign-off, and explicit linked `MAINTENANCE` blockout management. |
| **Workload & Preventative Schedule** (`/maintenance/schedule`) | `maintenance_schedule.html` | `maintenance:read` (`:manage` to edit) | Staff workload filter, 7-day scheduled & overdue queues, recurring schedule rules (`ONCE`..`ANNUALLY`), and manual **Generate Due Jobs Now**. |
| **Facilities, Assets & Contractors** (`/maintenance/facilities`) | `maintenance_facilities.html` | `maintenance:read` (`:manage` to edit) | Property-scoped maintenance categories, site locations, trackable equipment/appliance assets (serials & warranties), and external contractors. |
| **Guest Directory & Profile** (`/guests`, `/guests/{id}`) | `guests_list.html`, `guest_detail.html` | `guests:read` (`:write`, `:notes`, `:export`, `:anonymize`) | Searchable CRM directory (`ACTIVE`, `VIP`, `DO_NOT_CONTACT`), stay history, structured preferences (`ACCESSIBILITY`, `PET`, `ARRIVAL`, `EQUIPMENT`, `COMMUNICATION`, `ACCOMMODATION`, `OTHER`), private `guest_notes`, append-only marketing consent ledger, outbound email composer, Guest Portal session/verification controls, GDPR JSON export, and cluster-wide GDPR anonymisation. |
| **Duplicate Merge Review** (`/guests/merge`) | `guest_merge.html` | `guests:merge` | Detects normalized email/phone/name matches; atomically merges alias lineage (`A -> B`) across reservations, holds, notes, enquiries, and consent, or records audited non-duplicate dismissals. |
| **Guest Enquiries** (`/enquiries`, `/enquiries/{id}`) | `enquiries_list.html`, `enquiry_detail.html` | `comms:read` (`:write` to mutate) | `ENQ-YYYY-NNNN` inbox across 12 categories (`BOOKING`, `SPECIAL_REQUEST`, `CANCELLATION`, `COMPLAINT`, `LOST_PROPERTY`, etc.), external replies vs `INTERNAL` staff notes, SLA target tracking, and optional maintenance ticket linkage. |
| **Templates, Automation & Outbox** (`/communications/settings`) | `comms_settings.html` | `comms:read` (`:templates`, `:reconcile`) | Versioned HTML/text templates, 6 lifecycle automation triggers, failed email retry, evidence-backed `DISPATCH_UNKNOWN` reconciliation (`comms:reconcile`), and advisory GDPR retention windows. |
| **Reports & Accounting Exports** (`/reports`) | `reports.html` | `reports:read` (`:export` for CSV/JSON) | Date-aware Occupancy (`unit_status_history`), realized vs cash-flow Revenue/ADR/RevPAR, Channel mix, Seasonal YoY/custom comparison, 30–180d OTB + trailing-90d weekday Forecast, PII-free Guest Retention, completeness banners, and formula-safe `lovelace.accounting.v1` CSV/JSON exports. |
| **Site Settings, Branding & Embeds** (`/property`) | `property.html` | `property:read` (`:write` to mutate) | Multi-property creation, core property details, booking/deposit/Stripe/Guest Portal policy toggles, PWE-4 logo upload + WCAG palette + `?preview=1` mode (`GET /branding/property.css`), pre-configured **WordPress Plugin `.zip` generator** (`/property/wordpress-plugin.zip`), and 3 HTML embed snippets. |
| **Channel Manager** (`/settings/integrations/channel-manager`) | `channel_manager.html` | `channels:read` (`:write` to mutate) | AES-256-GCM credential vault for **Beds24**, **Smoobu**, **Lodgify**, and **Channex**, live `Verify`, `Disconnect`, listing discovery, unit-type mappings, `confirm_scope` sync preview/push, and `UNMAPPED` / `CONFLICT_OVERBOOKED` triage. |
| **Admin Users** (`/users`) | `users.html` | `users:manage` | Create property-scoped staff accounts across all 6 roles, toggle `is_active`, change roles, and issue `12+` character temporary password resets (`must_rotate_password=true`). |
| **Backups & Cloud Sync** (`/settings/backups`) | `backups.html` | Global `owner_admin` only | On-demand `.zip` / Argon2id `.zip.enc` export, cloud target management (`AWS_S3`, `S3_COMPATIBLE`, `GCS`, `AZURE_BLOB`), scheduled retention sync, non-destructive SHA-256 archive verification, and empty-DB disaster restore. |
| **Audit Log** (`/audit-logs`) | `audit.html` | `audit:read` | Chronological tamper-evident view over `audit_logs_effective` (`Timestamp`, `Actor`, `Action`, `Resource`, `IP`); full sanitized `before_state`/`after_state` JSON & historical PII overlay redaction available via `/api/v1/audit-logs`. |

---

## 5. Unified 6-Role × 35-Permission RBAC Matrix (`internal/auth/rbac.go`)

Every administrative handler checks `auth.HasPermission(actor.Role, perm)` plus PostgreSQL-authoritative `requirePropertyScope`:

| Permission Constant | Wire String | `owner_admin` | `manager` | `staff` | `maintenance_staff` | `housekeeping_staff` | `viewer` |
| :--- | :--- | :---: | :---: | :---: | :---: | :---: | :---: |
| `PermPropertyRead` | `property:read` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `PermPropertyWrite` | `property:write` | ✓ | — | — | — | — | — |
| `PermInventoryRead` | `inventory:read` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `PermInventoryWrite` | `inventory:write` | ✓ | ✓ | — | — | — | — |
| `PermRatesRead` | `rates:read` | ✓ | ✓ | ✓ | — | — | ✓ |
| `PermRatesWrite` | `rates:write` | ✓ | ✓ | — | — | — | — |
| `PermBlocksRead` | `blocks:read` | ✓ | ✓ | ✓ | ✓ | — | ✓ |
| `PermBlocksWrite` | `blocks:write` | ✓ | ✓ | ✓ | — | — | — |
| `PermReservationsRead` | `reservations:read` | ✓ | ✓ | ✓ | — | — | ✓ |
| `PermReservationsWrite` | `reservations:write` | ✓ | ✓ | ✓ | — | — | — |
| `PermReleasesRead` | `releases:read` | ✓ | ✓ | ✓ | — | — | ✓ |
| `PermReleasesWrite` | `releases:write` | ✓ | ✓ | — | — | — | — |
| `PermChannelsRead` | `channels:read` | ✓ | ✓ | — | — | — | — |
| `PermChannelsWrite` | `channels:write` | ✓ | ✓ | — | — | — | — |
| `PermUsersManage` | `users:manage` | ✓ | — | — | — | — | — |
| `PermAuditRead` | `audit:read` | ✓ | ✓ | — | — | — | — |
| `PermOpsRead` | `ops:read` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `PermHousekeepingWrite` | `housekeeping:write` | ✓ | ✓ | ✓ | — | ✓ | — |
| `PermMaintenanceRead` | `maintenance:read` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `PermMaintenanceReport` | `maintenance:report` | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| `PermMaintenanceWrite` | `maintenance:write` | ✓ | ✓ | ✓ | ✓ | — | — |
| `PermMaintenanceInspect` | `maintenance:inspect` | ✓ | ✓ | — | — | — | — |
| `PermMaintenanceManage` | `maintenance:manage` | ✓ | ✓ | — | — | — | — |
| `PermGuestsRead` | `guests:read` | ✓ | ✓ | ✓ | — | — | ✓* |
| `PermGuestsWrite` | `guests:write` | ✓ | ✓ | ✓ | — | — | — |
| `PermGuestsNotes` | `guests:notes` | ✓ | ✓ | ✓ | — | — | — |
| `PermGuestsMerge` | `guests:merge` | ✓ | ✓ | — | — | — | — |
| `PermGuestsExport` | `guests:export` | ✓ | ✓ | — | — | — | — |
| `PermGuestsAnonymize` | `guests:anonymize` | ✓ | ✓ | — | — | — | — |
| `PermCommsRead` | `comms:read` | ✓ | ✓ | ✓ | — | — | — |
| `PermCommsWrite` | `comms:write` | ✓ | ✓ | ✓ | — | — | — |
| `PermCommsReconcile` | `comms:reconcile` | ✓ | ✓ | — | — | — | — |
| `PermCommsTemplates` | `comms:templates` | ✓ | ✓ | — | — | — | — |
| `PermReportsRead` | `reports:read` | ✓ | ✓ | — | — | — | ✓ |
| `PermReportsExport` | `reports:export` | ✓ | ✓ | — | — | — | — |

*\*Note: `viewer` `guests:read` redacts internal `guest_notes`, enquiries, communications, consent records, and merge snapshots. Multi-property creation (`/onboarding?new=1`) and system backups (`/settings/backups`, `/api/v1/backups/*`) additionally require a **Global `owner_admin`** (`admin_users.property_id IS NULL`).*

---

## 6. Allowed Email & Communication Template Placeholders

Templates in `/communications/settings` (`internal/domain/guest_crm.go:AllowedTemplatePlaceholders`) auto-escape HTML values via `html/template` and reject unknown placeholders (`422`) so private `guest_notes` or `INTERNAL` messages can never be leaked:

| Placeholder | Rendered Value |
| :--- | :--- |
| `{{guest_first_name}}` | Guest first name |
| `{{guest_last_name}}` | Guest last name |
| `{{guest_full_name}}` | Guest full name |
| `{{property_name}}` | Property display name |
| `{{property_email}}` | Property guest contact email |
| `{{property_phone}}` | Property guest contact phone |
| `{{reservation_reference}}` | Reservation reference (e.g. `LV-2026-000123`) |
| `{{arrival_date}}` | Check-in date (`YYYY-MM-DD`) |
| `{{departure_date}}` | Check-out date (`YYYY-MM-DD`) |
| `{{check_in_time}}` | Standard check-in time (`HH:MM`) |
| `{{check_out_time}}` | Standard check-out time (`HH:MM`) |
| `{{unit_names}}` | Assigned room, cottage, or pitch names |
| `{{adults}}` | Number of adults |
| `{{children}}` | Number of children |
| `{{infants}}` | Number of infants |
| `{{total_amount}}` | Formatted reservation total (e.g. `£290.00`) |
| `{{balance_due}}` | Formatted remaining balance due |
| `{{enquiry_reference}}` | Enquiry reference (e.g. `ENQ-2026-0001`) |
| `{{enquiry_subject}}` | Enquiry subject line |

---

## 7. Inbound Signed Webhook Endpoints (`lovelace-app`)

| Endpoint | Signature Header & Secret | Accepted Events / Contract |
| :--- | :--- | :--- |
| `POST /api/public/v1/webhooks/stripe` | `Stripe-Signature` (`LOVELACE_STRIPE_WEBHOOK_SECRET` `whsec_...`) + `livemode` match check | `checkout.session.completed`, `checkout.session.expired`, `charge.refunded`. Deduplicated in `payment_webhook_events`. |
| `POST /api/v1/webhooks/email-events` | `X-Email-Signature: sha256=<hex>` (`LOVELACE_EMAIL_WEBHOOK_SECRET`) | `DELIVERED`, `BOUNCED`, `FAILED`, `COMPLAINT` receipts updating `guest_communications` and `communication_delivery_events`. |
| `POST /api/v1/webhooks/channels/{providerCode}/{connectionID}` | Provider-specific (`Smoobu` Base64 HMAC `<= 5m` skew + `channel_hmac_nonces`; `Channex` hex `x-hmac-signature-256` with `webhook_secret` + `webhook_secret_previous`; shared secret / `webhook_endpoint_token`) | Deduplicates in `channel_webhook_events` (`safe_summary` only, zero PII) and queues async `PROCESS_WEBHOOK_EVENT` hydration jobs. |

---

## 8. Public Booking Storefront & Guest Portal (`apps/public-web`)

`lovelace-public-web` (`Next.js 16`, port `3000`) holds **zero database or encryption secrets** and communicates with `lovelace-app` over server-only BFF routes (`X-Lovelace-Public-Key`):

### 8.1 Guest-Facing Pages
- **`/`** — Property hero, highlights, availability search bar, featured accommodation, and house policy summary.
- **`/stays` & `/stays/[slug]`** — Live availability search, category filter tabs, transparent nightly + extra-guest + add-on quote breakdown, and category detail view.
- **`/book`** — Multi-step direct booking basket hold (`15m` default timer), guest details, terms acceptance, and server-validated redirect to Stripe Checkout (`/checkout/sandbox` in local dev only).
- **`/booking/confirmation` & `/lookup`** — Post-checkout confirmation view and token-authenticated single-booking lookup (`POST` / `Authorization: Bearer`, token rotation, and cancellation request submission).
- **`/policies` & `/location`** — House rules, deposit/cancellation terms, privacy notice, check-in/out times, and location details.
- **`/embed/[slug]`** — Clickjacking-safe (`CSP frame-ancestors *` on `/embed/*` only) embeddable live booking widget posting `lovelace:embed:resize` height updates to parent websites and breaking out to top window for Stripe 3DS.
- **`/portal`, `/portal/verify`, `/portal/bookings`, `/portal/bookings/[reference]`, `/portal/profile`, `/portal/enquiries`, `/portal/privacy`** — Optional Dual-Gated Self-Service Guest Portal (`LOVELACE_GUEST_PORTAL_ENABLED=true` AND `guest_portal_enabled=true`), isolated via host-only `lovelace_guest_session` + `lovelace_portal_nonce` cookies and `X-Guest-CSRF`.

### 8.2 Next.js Server-Only BFF Routes (`/api/*`)
- `GET /api/healthz` — Frontend container health probe (`200 OK`).
- `GET /api/public/catalog` & `GET /api/public/property/logo` — Sanitized catalog JSON (consumed by the official WordPress plugin) and proxied property logo.
- `POST /api/public/availability`, `POST /api/public/pricing`, `POST /api/public/booking/holds`, `POST /api/public/booking/holds/[id]/cancel`, `POST /api/public/booking/checkout`, `GET|POST /api/public/booking/confirmation`, `POST /api/public/booking/confirmation/lookup`, `POST /api/public/booking/confirmation/rotate-token`, `POST /api/public/booking/confirmation/cancel-request`.
- `/api/public/portal/*` — Origin-verified BFF proxy for Guest Portal auth challenges, OTP/magic-link redemption, step-up challenges (`15m` freshness), arrival ETA updates, change/cancel requests, profile/email/consent updates, portal enquiries, and tracked GDPR SAR/erasure requests.

---

## 9. Official WordPress Plugin Reference (`internal/wordpress`)

Generated dynamically as a pre-configured `.zip` at `GET /property/wordpress-plugin.zip?property_id=<UUID>`:

- **WordPress Settings Page (`Settings → Lovelace Booking`):** Configures HTTPS `booking_origin`, default `property_slug`, brand colours, **Test Connection & Sync Brand Colours**, and instant clearing of the `15`-minute catalog transient (`lovelace_cat_<md5>`).
- **Gutenberg Blocks & Shortcodes:**
  1. `lovelace/booking-widget` / `[lovelace_booking property="" unit_type="" height="760"]`
  2. `lovelace/search-bar` / `[lovelace_search property="" target="modal|new_tab|same_tab" button_label="Check Availability"]`
  3. `lovelace/book-button` / `[lovelace_book_button property="" unit_type="" mode="modal|new_tab|same_tab" label="Book Direct"]`
  4. `lovelace/accommodation-cards` / `[lovelace_accommodation property="" columns="1|2|3" cta_mode="modal|new_tab|same_tab" schema="true|false"]` (renders cached server-side HTML cards + `schema.org/LodgingBusiness` JSON-LD).

---

## 10. Complete REST API Endpoint Reference

See **[docs/api.md](../api.md)**, **[docs/openapi-admin-v1.yaml](../openapi-admin-v1.yaml)**, and **[docs/openapi-public-v1.yaml](../openapi-public-v1.yaml)** for full JSON request/response schemas across all `/api/v1/*` and `/api/public/v1/*` endpoints.
