# LOVELACE Booking System — Phase 5 (Reporting & Business Intelligence)

Multi-property campsite & accommodation booking platform built with a **Go 1.24 + PostgreSQL 17 + `pgx/v5`** backend, a **server-rendered LOVELACE Administrative Console** (`:8080`), and a **completely separate Next.js / React / TypeScript public booking frontend** (`apps/public-web/`, `:3000`).

- **Administrative Identity:** **LOVELACE Booking System** (`internal/branding`, `internal/webui`, `:8080`)
- **Public Customer Experience:** **Tir Bach Campsite**, Pembrokeshire, Wales (`apps/public-web/`, `:3000`, subtle *"Booking system powered by LOVELACE"* footer)
- **Phase Boundary:** Implements **Phases 1–2.1.1** (Core Inventory, Administrative Reservations, Bulk Blockouts, Scheduled Releases, Channel Config Vault, Direct Public Booking Engine, Stripe Checkout + Signed Webhooks, Outbox Confirmation Emails, Payment Exceptions), **Phase 3 Milestone 1 (+ 3.1)** (daily **Operations Centre** `/`, `/operations`, **5-stage Daily Changeover Board**, payment exception links `/payment-exceptions`), **Phase 3 Milestone 2 (+ 3.2.1/3.2.2)** (**Maintenance Management & Facilities**: `/maintenance`, composite property isolation, post-completion inspection enforcement, calendar-aware preventative scheduling with assignee validation), **Phase 3 Milestone 3 (+ 3.3.1 / 3.3.2)** (**Guest Management & Communications CRM**: `/guests`, `/guests/{id}`, `/guests/merge`, `/enquiries`, `/communications/settings`, 360 guest profiles, returning-stay classification, complete paginated duplicate scans, multi-hop merge lineage `A -> B -> C`, dismissal privacy controls, structured preferences vs. internal staff notes, enquiry & complaint workflow with `INTERNAL` note isolation, unified communications timeline with strict guest/reservation/enquiry validation, lease-coordinated outbox cancellation on anonymisation, signed email delivery webhooks & retries, versioned templates, timezone-aware pre-arrival/post-stay automation, hardened append-only consent ledger defaulting to `NOT_SUBSCRIBED`, booking-hold guest linkage & GDPR export/erasure coverage, PII-minimized `audit_logs` with RBAC-gated append-only historical redaction overlay, advisory-only retention settings, and Operations Centre CRM alerts), **Phase 4** (Live OTA & Channel Sync for Beds24, Smoobu, Lodgify, Channex), **Phase 5** (**Reporting & Business Intelligence**: `/reports`, property-scoped historical occupancy, per-currency ADR/RevPAR and cash/refund reconciliation, channel performance & completeness notices, seasonal comparisons, strictly read-only OTB + trailing-90d forecasting, privacy-safe aggregate canonical guest retention analytics, and audited formula-safe `lovelace.accounting.v1` CSV exports), and **`PWE-1`–`PWE-5`** (Admin Design System & Theme Engine, User Personalisation, Customisable Dashboard Builder, Property Branding with sanitized public storefront projection, and Role-Aligned Workspace Profiles). **Phase 3 Milestone 4** (self-service public guest portal) remains deferred (`PROPOSED / OWNER REVIEW REQUIRED` discovery specification only: [docs/phase3-milestone4-guest-portal.md](docs/phase3-milestone4-guest-portal.md)).

## Quick Links
- [Phase 2 Architecture & Isolation Specification](docs/phase2-architecture.md)
- [Phase 3 Milestone 4: Public Guest Portal (`PROPOSED / OWNER REVIEW REQUIRED`)](docs/phase3-milestone4-guest-portal.md)
- [Public Booking OpenAPI Contract (`/api/public/v1`)](docs/openapi-public-v1.yaml)
- [Admin Operations & Maintenance OpenAPI Contract (`/api/v1`)](docs/openapi-admin-v1.yaml)
- [Architecture & Five-Phase Blueprint](docs/architecture.md)
- [Personal Data Inventory & Privacy Lifecycle (`Phase 3.3.2`)](docs/privacy-pii-inventory.md)
- [Admin REST API Reference (`/api/v1`)](docs/api.md)
- [Operations, Hardening & Backup/Restore Runbook](docs/operations.md)
- [Security Controls & Test Isolation](docs/security.md)
- [Branding & Design System](docs/branding.md)
- [ADR-0001: Public/Admin Separation & PWE Boundaries](docs/adr/0001-public-admin-separation-and-pwe-boundaries.md)
- [Personalisation & Workspace Experience (`PWE-1`–`PWE-5`)](docs/personalisation-workspace-experience.md)

## Quick Start (Local Development)

```bash
docker compose -f docker-compose.dev.yml up -d --build
```

- **Tir Bach Public Booking Site:** [http://127.0.0.1:3000](http://127.0.0.1:3000)
- **LOVELACE Admin Console:** [http://127.0.0.1:8080/login](http://127.0.0.1:8080/login)

## Production Deployment & Upgrades

1. **Fresh installation**: leave `POSTGRES_BOOTSTRAP_USER=postgres` (default) and set strong random secrets (`POSTGRES_BOOTSTRAP_PASSWORD`, `LOVELACE_MIGRATOR_PASSWORD`, `LOVELACE_APP_PASSWORD`, and `TIRBACH_ENCRYPTION_KEY` via `openssl rand -hex 32`).
2. **Existing Phase 1.5 volume upgrade (`tir_bach_pgdata`)**:
   - Original Phase 1.5 volumes were initialized with the `tir_bach` superuser. Explicitly set `POSTGRES_BOOTSTRAP_USER=tir_bach` and set `POSTGRES_BOOTSTRAP_PASSWORD` to match the existing database password (**PostgreSQL ignores env-var credential changes on an already-initialized data volume**).
   - Take a pre-upgrade `pg_dump` backup first and **never delete or recreate persistent volumes** (`docker compose down -v`).
   - Run the upgrade stack cleanly (`db-bootstrap` preserves data and transfers schema/table ownership to `lovelace_migrator` without printing secrets). See [docs/operations.md](docs/operations.md) for SQL password rotation and backup/restore commands using your configured bootstrap user.
3. Start the hardened standalone compose stack (fails closed if required secrets are missing or if `app` connects with privileged DB credentials):
   ```bash
   docker compose up -d --build
   ```
4. See [docs/operations.md](docs/operations.md) for PostgreSQL role separation, reverse-proxy TLS termination, explicit `cmd/seed` bootstrapping, and `pg_dump` / `pg_restore`.

## Provisioning Isolated Test DB & Running Tests

Provision the dedicated unprivileged test role (`tir_bach_test_runner`) and database (`tir_bach_test`):
```bash
./scripts/setup-test-db.sh
```

Run unit & full PostgreSQL 17 `-race` integration tests:
```bash
TEST_DATABASE_URL="postgres://tir_bach_test_runner:tir_bach_test_runner_pw@127.0.0.1:5432/tir_bach_test?sslmode=disable" \
  go test -v -race -count=1 ./...
```
