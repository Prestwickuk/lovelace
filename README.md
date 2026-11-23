# LOVELACE Booking System — Phase 5 (Reporting & Business Intelligence)

Multi-property campsite & accommodation booking platform built with a **Go 1.24 + PostgreSQL 17 + `pgx/v5`** backend, a **server-rendered LOVELACE Administrative Console** (`:8080`), and a **completely separate Next.js / React / TypeScript public booking frontend** (`apps/public-web/`, `:3000`).

- **Administrative Identity:** **LOVELACE Booking System** (`internal/branding`, `internal/webui`, `:8080`)
- **Public Customer Experience:** Property-branded public storefront (`apps/public-web/`, `:3000`, subtle *"Booking system powered by LOVELACE"* footer)
- **Phase Boundary:** Implements **Phases 1–2.1.1** (Core Inventory, Administrative Reservations, Bulk Blockouts, Scheduled Releases, Channel Config Vault, Direct Public Booking Engine, Stripe Checkout + Signed Webhooks, Outbox Confirmation Emails, Payment Exceptions), **Phase 3 Milestone 1 (+ 3.1)** (daily **Operations Centre** `/`, `/operations`, **5-stage Daily Changeover Board**, payment exception links `/payment-exceptions`), **Phase 3 Milestone 2 (+ 3.2.1/3.2.2)** (**Maintenance Management & Facilities**: `/maintenance`, composite property isolation, post-completion inspection enforcement, calendar-aware preventative scheduling with assignee validation), **Phase 3 Milestone 3 (+ 3.3.1 / 3.3.2)** (**Guest Management & Communications CRM**: `/guests`, `/guests/{id}`, `/guests/merge`, `/enquiries`, `/communications/settings`, 360 guest profiles, returning-stay classification, complete paginated duplicate scans, multi-hop merge lineage `A -> B -> C`, dismissal privacy controls, structured preferences vs. internal staff notes, enquiry & complaint workflow with `INTERNAL` note isolation, unified communications timeline with strict guest/reservation/enquiry validation, lease-coordinated outbox cancellation on anonymisation, signed email delivery webhooks & retries, versioned templates, timezone-aware pre-arrival/post-stay automation, hardened append-only consent ledger defaulting to `NOT_SUBSCRIBED`, booking-hold guest linkage & GDPR export/erasure coverage, PII-minimized `audit_logs` with RBAC-gated append-only historical redaction overlay, advisory-only retention settings, and Operations Centre CRM alerts), **Phase 3 Milestone 4** (**Self-Service Public Guest Portal**: `0026_phase3_m4_guest_portal.sql`–`0028_phase3_m4_challenge_outbox_fencing.sql`, `/api/public/v1/properties/{slug}/portal/*`, `apps/public-web/src/app/portal/*` & server-only BFF, disabled by default via `LOVELACE_GUEST_PORTAL_ENABLED=false` and `property_booking_policies.guest_portal_enabled=false`, passwordless email OTP / browser-bound POST magic link alongside single-booking confirmation-token access, property-scoped guest sessions, OTA read-only enforcement, request-only booking changes/cancellations, marketing consent, and step-up gated request-only SAR/erasure submission with owner-approved `D1`–`D10` & `Section 7.2` policies and separately gated hosted staging sign-off / pilot enablement: [docs/phase3-milestone4-guest-portal.md](docs/phase3-milestone4-guest-portal.md), [docs/phase3-milestone4-operational-readiness-report.md](docs/phase3-milestone4-operational-readiness-report.md)), **Phase 4** (Live OTA & Channel Sync for Beds24, Smoobu, Lodgify, Channex), **Phase 5** (**Reporting & Business Intelligence**: `/reports`, property-scoped historical occupancy, per-currency ADR/RevPAR and cash/refund reconciliation, channel performance & completeness notices, seasonal comparisons, strictly read-only OTB + trailing-90d forecasting, privacy-safe aggregate canonical guest retention analytics, and audited formula-safe `lovelace.accounting.v1` CSV exports), **`PWE-1`–`PWE-5`** (Admin Design System & Theme Engine, User Personalisation, Customisable Dashboard Builder, Property Branding with sanitized public storefront projection, and Role-Aligned Workspace Profiles), and **Cross-Cutting Security & Integrity Hardening (`0029_security_and_integrity_hardening.sql`)** (multi-tenant composite foreign keys, DB-authoritative property scoping, Argon2id memory/concurrency caps, pre-session Login CSRF & `Origin: null` fail-closed guards, out-of-transaction Stripe auto-refunds, GDPR erasure cluster freeze, strict Next.js CSP & BFF rate-limit hardening, and 1:1 Go/PostgreSQL audit allowlist parity).

## Official Deployment, Setup & Maintenance Documentation

- **[The Property Owner's Everyday Guide (Plain-English Illustrated Handbook)](docs/owner-handbook/README.md)** — Warm, jargon-free, step-by-step instructions with **20 annotated screenshots** covering every everyday hospitality task (signing in, making text larger, personal workspaces, phone bookings, check-in/out & ledgers, rooms/pitches & extras, blocking dates & scheduled releases, seasonal prices, repairs & recurring safety checks, guest profiles & automated emails, OTA channel managers, accountant CSVs, WordPress/website booking boxes, and safety backups).

For technical setup and hosting, start with the **[Lovelace Documentation Hub](docs/README.md)**:

1. **[Local Evaluation & Self-Hosted Linux Server Guide](docs/guides/01-local-and-self-hosted-server.md)** — 2-minute local Docker evaluation (`docker-compose.dev.yml`), production Ubuntu/Debian Linux setup (`docker-compose.yml`), automatic HTTPS with **Caddy** or **Nginx**, firewall hardening, and native `systemd` installation.
2. **[Cloud Deployment Guide (AWS, GCP & Azure)](docs/guides/02-cloud-deployments-aws-gcp-azure.md)** — Production architectures and step-by-step commands for **AWS** (ECS Fargate + RDS PostgreSQL 17 + EFS + S3), **Google Cloud** (Cloud Run + Cloud SQL + GCS), and **Microsoft Azure** (Container Apps + Flexible Server + Azure Files/Blob).
3. **[Railway & PaaS Deployment Guide](docs/guides/03-railway-and-paas-deployment.md)** — Deploying Lovelace on **Railway** (plus **Fly.io** & **Render** notes) with private `.railway.internal` IPv6 networking, persistent volumes, and custom HTTPS domains.
4. **[First-Boot Setup, Advanced Configuration, Embeds & Maintenance Guide](docs/guides/04-first-login-configuration-and-maintenance.md)** — Complete illustrated setup manual (**14 annotated screenshots**) covering first-login password rotation & 8 recovery codes, all **7 steps** of the Guided Setup Wizard, multi-property routing, custom logo & WCAG branding preview, workspace profiles, live OTA sync, Stripe & SMTP webhooks, Guest Portal dual-gate setup, the **Official WordPress Plugin (`.zip`)** & HTML embeds, and Argon2id `.zip.enc` backups.
5. **[Deep Troubleshooting & Hospitality Catastrophe Playbook](docs/guides/05-troubleshooting-and-disaster-recovery.md)** — Deep technical fixes across **Local/Self-Hosted**, **AWS/GCP/Azure**, and **Railway**, **21 real-world catastrophe rescues** (Campsites, Glamping, B&Bs, Holiday Cottages, Hotels, Multi-Site Operators), plus **Part III: 15 code-verified operational, security, webhook, OTA, Guest Portal, reporting & WordPress troubleshooting scenarios (Issues 22–36)**.
6. **[Complete Feature, Tool, CLI, API & Permission Reference](docs/guides/06-complete-feature-and-tool-reference.md)** — Exhaustive technical index of every Admin WebUI screen, CLI binary & flag (`noteG-server`, `lovelace-migrate`, `lovelace-seed`), operational script, all **8 background workers**, the **6-role × 35-permission RBAC matrix**, allowed template `{{placeholders}}`, inbound webhooks, Next.js storefront/Guest Portal routes, and WordPress blocks/shortcodes.

## Architecture & Specialist Specifications
- [Phase 2 Architecture & Isolation Specification](docs/phase2-architecture.md)
- [Phase 3 Milestone 4: Self-Service Public Guest Portal](docs/phase3-milestone4-guest-portal.md)
- [Phase 3 Milestone 4: Operational Readiness & Hosted Staging Gate Report](docs/phase3-milestone4-operational-readiness-report.md)
- [Production Foundation & Launch-Readiness Plan (Portal Disabled)](docs/production-foundation-plan.md)
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

- **Public Booking Storefront:** [http://127.0.0.1:3000](http://127.0.0.1:3000)
- **LOVELACE Admin Console:** [http://127.0.0.1:8080/login](http://127.0.0.1:8080/login)

## Production Deployment & Upgrades

1. **Fresh installation**: leave `POSTGRES_BOOTSTRAP_USER=postgres` (default) and set strong random secrets (`POSTGRES_BOOTSTRAP_PASSWORD`, `LOVELACE_MIGRATOR_PASSWORD`, `LOVELACE_APP_PASSWORD`, and `LOVELACE_ENCRYPTION_KEY` via `openssl rand -hex 32`).
2. **Existing volume upgrade (`lovelace_pgdata`)**:
   - If upgrading a pre-existing PostgreSQL data volume initialized with a custom bootstrap role or database name, explicitly set `POSTGRES_BOOTSTRAP_USER`, `POSTGRES_DB`, and `POSTGRES_BOOTSTRAP_PASSWORD` to match the existing volume (**PostgreSQL ignores env-var credential changes on an already-initialized data volume**).
   - Take a pre-upgrade `pg_dump` backup first and **never delete or recreate persistent volumes** (`docker compose down -v`).
   - Run the upgrade stack cleanly (`db-bootstrap` preserves data and transfers schema/table ownership to `lovelace_migrator` without printing secrets). See [docs/operations.md](docs/operations.md) for SQL password rotation and backup/restore commands using your configured bootstrap user.
3. Start the hardened standalone compose stack (fails closed if required secrets are missing or if `app` connects with privileged DB credentials):
   ```bash
   docker compose up -d --build
   ```
4. See [docs/operations.md](docs/operations.md) for PostgreSQL role separation, reverse-proxy TLS termination, explicit `cmd/seed` bootstrapping, and `pg_dump` / `pg_restore`.

## Provisioning Isolated Test DB & Running Tests

Provision the dedicated unprivileged test role (`lovelace_test_runner`) and database (`lovelace_test`):
```bash
./scripts/setup-test-db.sh
```

Run unit & full PostgreSQL 17 `-race` integration tests:
```bash
TEST_DATABASE_URL="postgres://lovelace_test_runner:lovelace_test_runner_pw@127.0.0.1:5432/lovelace_test?sslmode=disable" \
  go test -v -race -count=1 ./...
```
