# LOVELACE — Phase 3 Milestone 4: Operational Readiness, Policy Closure & Hosted Staging Gate Report

- **Repository:** `Prestwickuk/lovelace`
- **Verified `main` Baseline:** `0e2a6fdc5ddca1007dd54dab09da9950edc2b23f` (consolidating Phases 1–5, `PWE-1`–`PWE-5`, Phase 3 Milestone 4 `0026`–`0028`, and cross-cutting security hardening `0029_security_and_integrity_hardening.sql`)
- **Review Date:** `2026-11-17`
- **Production & Default Feature Gates:** **`TIRBACH_GUEST_PORTAL_ENABLED=false`** and **`property_booking_policies.guest_portal_enabled=false`** (unchanged; portal remains disabled)

---

## 1. Outcome Summary (Distinct Readiness Gates)

Staging sign-off and production pilot readiness are evaluated as **strictly separate outcomes**:

| Readiness Dimension | Outcome | Summary & Authority |
|---|---|---|
| **1. Code, Schema & Hosted CI Evidence** | **`PASS` (Verified on `main`)** | `main` at `0e2a6fdc5ddca1007dd54dab09da9950edc2b23f` passed all three hosted GitHub Actions workflows (`44155146545`, `44155146841`, `44155146543`), including PostgreSQL 17 migrations `0001`–`0029`, Go `-race` integration tests, and standalone Next.js Playwright Chromium E2E (`booking.spec.ts` + `portal.spec.ts`). |
| **2. Section 7.2 Policy & Governance Gates** | **`CLOSED / OWNER-APPROVED`** | Explicit owner decisions and accountable owners confirmed on `2026-11-17` for **Section 7.2.1** (SAR/erasure review SLA & HMRC 6-year + current-year retention), **Section 7.2.2** (corroborating booking verification for shared/household inbox rights requests), and **Section 7.2.3** (zero portal IP/UA hashes + 5-minute post-expiry credential pruning). |
| **3. Hosted External Staging Sign-Off** | **`BLOCKED` (`0 / 6` Hosted Boundaries Verified)** | Coordinated with staging operator on `2026-11-17`: **no external hosted staging environment is provisioned yet** (`GitHub Environments = 0`). Local `TIRBACH_ENV=staging` / loopback SMTP checks validate code paths only and **do not** prove hosted physical separation. Hosted M4 staging execution is deferred until all 6 external boundaries below are provisioned and verified with masked resource identifiers. |
| **4. Production Pilot Readiness** | **`BLOCKED` (Separate Gate)** | Blocked behind (a) closing all 6 `BLOCKED` hosted staging separation boundaries and passing the M4 staging check sequence on that isolated hosted environment, **followed by** (b) a separate explicit owner decision to authorize pilot enablement for a specific property. |

---

## 2. Code & Hosted CI Evidence (`main` @ `0e2a6fdc5ddca1007dd54dab09da9950edc2b23f`)

### 2.1 Hosted GitHub Actions Workflows on `main`
All three required hosted workflows completed with `success` on `main` merge commit `0e2a6fdc5ddca1007dd54dab09da9950edc2b23f`:

| Workflow | Run ID | Status / Conclusion | Verified Scope |
|---|---|---|---|
| **Phase 3 Milestone 3.2 acceptance** | [`44155146545`](https://github.com/Prestwickuk/lovelace/actions/runs/44155146545) | `completed` / **`success`** | `gofmt`, `go vet`, fresh `0001`–`0029` PostgreSQL 17 apply, populated `0017`–`0029` upgrade, full Go integration + `-race` suite |
| **Phase 4 Live OTA and Channel Sync** | [`44155146841`](https://github.com/Prestwickuk/lovelace/actions/runs/44155146841) | `completed` / **`success`** | Non-superuser `lovelace_migrator` / `lovelace_app` separation, fresh & upgrade migrations through `0029`, full Go integration + `-race` suite |
| **Phase 5 Reporting and Business Intelligence** | [`44155146543`](https://github.com/Prestwickuk/lovelace/actions/runs/44155146543) | `completed` / **`success`** | `0020`–`0029` + `ensure-roles.sh` least-privilege idempotence + Go `-race` (`phase5` job) and Next.js ESLint + Vitest (`25/25`) + standalone build + Chromium Playwright E2E (`7/7` across `booking.spec.ts` & `portal.spec.ts` in `public-web` job) |

### 2.2 Scope Boundary of Local / CI Checks vs. Hosted Staging Evidence
- **What Code & CI Evidence Proves:** Server-keyed HMAC-SHA256 OTP binding (`0027`), outbox security-challenge scrubbing across `SENT`/`FAILED`/`DISPATCHING`/`DISPATCH_UNKNOWN` without breaking worker lease fencing (`0028`), multi-tenant composite foreign keys & audit allowlist parity (`0029`), `Origin` / `Host` / `X-Forwarded-Host` / `Origin: null` fail-closed guards, `tirbach_session` vs. `lovelace_guest_session` isolation, `D1`–`D10` & `Section 7.2` enforcement, and default-disabled startup (`TIRBACH_GUEST_PORTAL_ENABLED=false`, `guest_portal_enabled=false`).
- **What Code & CI Evidence Does NOT Prove:** Ephemeral CI containers and local loopback `TIRBACH_ENV=staging` processes do **not** establish physical separation of hosted compute, hosted PostgreSQL instances, cloud secret stores, external SMTP sinks, or live vs. sandbox third-party accounts.

---

## 3. Hosted External Staging Separation Boundaries (`6 / 6 BLOCKED`)

Inspection of `Prestwickuk/lovelace` (`repos/Prestwickuk/lovelace/environments` -> `total_count: 0`) and operator confirmation on `2026-11-17` verified that **no external hosted staging environment has been provisioned yet**. All six external separation boundaries are therefore marked **`BLOCKED`**.

> **Credential Safety Rule:** When recording evidence to close the boundaries below, record **masked resource identifiers, non-secret fingerprints, hostnames, and account/project IDs only** — never paste raw passwords, API keys, webhook secrets, or `TIRBACH_ENCRYPTION_KEY` material.

| # | External Separation Boundary | Status | Accountable Owner | Required Operator Action | Masked Evidence Required to Close Boundary |
|---|---|---|---|---|---|
| **B1** | **Deployment Compute & Public/Admin Origins** | **`BLOCKED`** | Platform / Security Operator | Provision dedicated staging compute/containers for `tirbach-server` and `public-web` on dedicated HTTPS staging origins (`https://staging-book.<domain>` and `https://staging-admin.<domain>`) with zero DNS, load-balancer, or `LOVELACE_API_BASE_URL` routing to production. Keep `TIRBACH_GUEST_PORTAL_ENABLED=false` in production. | Masked compute service/container IDs, staging HTTPS origin URLs, reverse-proxy CIDR config (`TIRBACH_TRUSTED_PROXIES`), and `GET /readyz` status output from staging. |
| **B2** | **PostgreSQL 17 Instance & Synthetic Dataset** | **`BLOCKED`** | Platform / Security Operator | Provision a physically separate staging PostgreSQL 17 instance/cluster with independent storage/backups, non-superuser roles (`lovelace_migrator`, `lovelace_app`), migrations `0001`–`0029` applied, and **synthetic test properties/guests/bookings only** (zero production `pg_dump` imports). | Masked DB instance identifier / hostname, `current_user` + `schema_migrations` count (`29`) query output, and attestation that zero production guest records exist. |
| **B3** | **Property Policy Configuration** | **`BLOCKED`** | Platform / Security Operator | Confirm production properties retain `property_booking_policies.guest_portal_enabled = FALSE` and `TIRBACH_GUEST_PORTAL_ENABLED=false`; in hosted staging, enable `guest_portal_enabled = TRUE` solely on a synthetic property (`e.g. synth-staging-camp`) during testing and revert cleanly afterward. | SQL output of `SELECT p.slug, pol.guest_portal_enabled FROM properties p JOIN property_booking_policies pol ON pol.property_id = p.id;` from hosted staging (before, during, and reverted after acceptance) and confirmation production remains `false`. |
| **B4** | **Secret Store & Distinct Cryptographic Keys** | **`BLOCKED`** | Platform / Security Operator | Store staging secrets in an isolated secret manager path/vault namespace with freshly generated CSPRNG 32-byte `TIRBACH_ENCRYPTION_KEY` and `TIRBACH_PUBLIC_API_SECRET` values completely distinct from production and local/dev keys. | Masked secret-store ARN / vault path + secret version IDs (or truncated SHA-256 fingerprint of secret metadata) proving non-overlap with production, plus clean `TIRBACH_ENV=staging` startup log (`database runtime role verified`). |
| **B5** | **Isolated Outbound Mail Sink** | **`BLOCKED`** | Platform / Security Operator | Configure `TIRBACH_SMTP_ADDR` in hosted staging to deliver exclusively to an isolated non-relaying test mail sink (e.g. hosted Mailpit / MailHog or sandbox-restricted SES/Postmark sink incapable of delivering to external guest domains). | Masked mail-sink service ID / SMTP endpoint hostname, transport firewall/sandbox rule confirmation blocking external internet delivery, and synthetic `@example.test` capture ID. |
| **B6** | **Stripe & OTA Sandbox Credentials** | **`BLOCKED`** | Platform / Security Operator | Configure hosted staging exclusively with Stripe test-mode keys (`sk_test_****`, test webhook `whsec_****` with `ExpectedStripeLiveMode() == false`) and keep all live OTA `channel_connections` absent/disabled (or pointed solely at vendor sandbox endpoints). | Masked Stripe test account ID (`acct_****`) & webhook endpoint ID (`we_****`), `/readyz` payment-mode output, and `SELECT provider_code, status FROM channel_connections;` output showing zero live OTA connections. |

---

## 4. Explicit Owner Policy Decisions & Accountable Owners (`Section 7.2` — `CLOSED`)

On `2026-11-17`, the owner confirmed explicit operational policies and accountable owners for all three remaining Section 7.2 gates (without reopening `D1`–`D10` or altering default-disabled feature flags):

| Gate ID | Policy Topic | Status | Explicit Owner Decision (`2026-11-17`) | Accountable Owner | Technical & Operational Enforcement |
|---|---|---|---|---|---|
| **Section 7.2.1** | **SAR / Erasure Review & Statutory Retention SOPs** | **`APPROVED / CLOSED`** | Portal SAR (`D6`) and erasure (`D7`) remain strictly request-only (`guest_enquiries` tickets returning uniform `202` receipts; zero automated export or self-service anonymisation). `owner_admin` or `manager` reviews and responds within the **UK GDPR 1-calendar-month SLA**, manually screening `ExportGuestData` outputs to redact third-party personal data and legal exemptions before secure out-of-band release. Financial reservation, payment, and refund shells (`reservations` financial/date/reference columns, `payments`, `refunds`, `payment_attempts`, `payment_exceptions`) are retained for **6 years plus the current tax year** (HMRC statutory accounting retention) while personal identifiers and free-text notes are scrubbed via `AnonymizeGuest`. Erasure execution is deferred (`409` / `erasure_requested_at` cluster freeze) while active/upcoming stays, unsettled `DISPATCHING`/`DISPATCH_UNKNOWN` email leases, or open financial/legal disputes remain open. | **Property Owner / Data Controller (`Tir Bach Owner-Admin`)** | `RequestPortalSAR` & `RequestPortalErasure` (`internal/service/portal_service.go`) never call `ExportGuestData` or `AnonymizeGuest`; `PrepareOrAnonymizeGuestAt` preserves financial shells while scrubbing PII and freezing `erasure_requested_at` clusters (`0029`). |
| **Section 7.2.2** | **Identity Assurance for Shared Household Inbox Requests** | **`APPROVED / CLOSED`** | A fresh (`<= 15m`) Tier B email OTP permits **intake only** of a request-only SAR or erasure ticket with a uniform non-disclosing receipt. **Before staff act on an SAR/erasure request or disclose any rights-request status or personal data**, staff MUST verify **corroborating booking ownership** (matching booking reference + stay dates and lead-guest details), with mandatory corroboration whenever a shared/household inbox, duplicate guest record, or third-party contact is involved. Portal endpoints never expose rights-request status or details. | **Property Owner / Data Controller (`Tir Bach Owner-Admin`)** | `IsPrivacyRightsEnquiry` (`internal/domain/portal.go`) excludes SAR/erasure tickets from `ListPortalEnquiries`, `GetPortalEnquiry`, and `ReplyPortalEnquiry`; `D3` blocks automatic duplicate-email linking without corroborating booking verification; staff follow corroboration SOP prior to fulfilment. |
| **Section 7.2.3** | **Portal Security Telemetry & Credential Retention** | **`APPROVED / CLOSED`** | **Zero IP-address or User-Agent hashes** are collected or stored on Guest Portal tables (`guest_portal_identities`, `guest_portal_challenges`, `guest_portal_sessions`) or pseudonymous `portalActor` audit rows. Expired, consumed, invalidated, or revoked `guest_portal_challenges` and `guest_portal_sessions` rows are hard-pruned **5 minutes after expiry/revocation** (`PruneExpiredPortalState`), and `email_outbox` security-challenge bodies are scrubbed immediately to `'[redacted security challenge]'` upon `SENT`, terminal failure, fencing, expiry, or redemption while preserving non-PII structural `audit_logs` records immutably. | **Platform / Security Operator** | Enforced in `migrations/0026`–`0028`, `portalActor` (`internal/service/portal_service.go`), `PruneExpiredPortalState` (`internal/store/postgres/portal_repo.go`), and verified in `TestPhase3Milestone4_SelfServiceGuestPortalEndToEnd`. |

---

## 5. Hosted Staging Execution Sequence & Remaining Blockers

### 5.1 Preconditions Before Running Hosted M4 Staging Checks
Do **not** deploy or enable the Guest Portal on any shared or unverified target. Once the **Platform / Security Operator** closes boundaries **B1–B6** in Section 3 and records their masked resource identifiers:

1. Deploy exact verified commit `0e2a6fdc5ddca1007dd54dab09da9950edc2b23f` (or current green `main` head) to the isolated hosted staging target with `TIRBACH_ENV=staging` and `LOVELACE_ENV=staging`.
2. Exercise both feature gates (`TIRBACH_GUEST_PORTAL_ENABLED` and `property_booking_policies.guest_portal_enabled`) on a **synthetic staging property** using synthetic `@example.test` guests/bookings and the isolated staging mail sink (**B5**).
3. Run the approved M4 Staging Acceptance check sequence (Sections A, B, and C of [phase3-milestone4-guest-portal.md](phase3-milestone4-guest-portal.md)): OTP + same-browser POST magic link, Tier A vs. Tier B boundaries, step-up freshness, duplicate-email corroboration, OTA read-only & `@guest.booking.com` challenge suppression, `Origin`/`Host`/`X-Forwarded-Host` spoofing rejection, outbox challenge redaction, support recovery (`send-verification` / `revoke-sessions`), and mobile/keyboard accessibility.
4. Revert `property_booking_policies.guest_portal_enabled = FALSE` and `TIRBACH_GUEST_PORTAL_ENABLED=false` on staging after testing, and record hosted run timestamps and masked evidence in Section 3.

### 5.2 Remaining Blockers
1. **Blocker for Hosted Staging Sign-Off:** External hosted staging separation boundaries **B1–B6** (Section 3) are not yet provisioned (`BLOCKED`, owned by **Platform / Security Operator**).
2. **Blocker for Production Pilot Readiness (Separate Outcome):** Requires (a) completion of hosted staging sign-off above, **and** (b) a separate explicit owner authorization from the **Property Owner / Data Controller (`Tir Bach Owner-Admin`)** before changing `TIRBACH_GUEST_PORTAL_ENABLED` or `property_booking_policies.guest_portal_enabled` from `false` on any pilot property.
