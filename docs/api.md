# LOVELACE Booking System — REST API Reference (`/api/v1` & `/api/public/v1`)

Administrative endpoints under `/api/v1/*` return JSON and authenticate via the host-only `lovelace_session` (`HttpOnly`, `SameSite=Lax`, `Secure` in production) cookie.  
All state-changing administrative requests (`POST`, `PUT`, `PATCH`, `DELETE`) other than `/api/v1/auth/login` and `/api/v1/auth/recover` require header `X-CSRF-Token` matching the session's `csrf_token`.

Error responses share a standard envelope:
```json
{
  "error": {
    "code": "date_overlap",
    "message": "human-readable error description",
    "fields": { "optional_field": "validation detail" }
  }
}
```

---

## 1. Health, Readiness & Authentication
- `GET /healthz` — Lightweight process liveness probe (`200 OK`).
- `GET /readyz` — Deep readiness probe (`200 OK` / `503 Service Unavailable`) reporting `database`, `attachments`, `payments`, `email`, and `public_api` component states.
- `POST /api/v1/auth/login` — Body `{"email","password"}` (same-origin verified). Sets `lovelace_session` cookie; returns `user` + `csrf_token`.
- `POST /api/v1/auth/recover` — Body `{"email","recovery_code","new_password"}`. Resets password using one of 8 single-use `HMAC-SHA256` recovery codes without requiring email access; revokes existing sessions.
- `POST /api/v1/auth/logout` — Revokes current session & clears cookie.
- `GET /api/v1/auth/me` — Returns current actor summary (`must_rotate_password`, `role`, `property_id`, `csrf_token`).
- `POST /api/v1/auth/change-password` — Body `{"current_password","new_password"}`. Clears `must_rotate_password`, revokes all prior sessions, and rotates session cookie + `csrf_token`.
- `GET /api/v1/auth/recovery-codes` — Returns unused/total recovery code counts (`RecoverySummary`).
- `POST /api/v1/auth/recovery-codes` — Body `{"current_password"}`. Generates a fresh set of 8 one-time recovery codes (invalidating prior unused codes).

> **Password Rotation Gate:** When `must_rotate_password=true`, all `/api/v1/*` routes except `/api/v1/auth/me`, `/api/v1/auth/logout`, and `/api/v1/auth/change-password` return `403 password_rotation_required`.

---

## 2. Properties, Branding, Workspace Personalisation & Inventory
- `GET /api/v1/properties` / `GET /api/v1/properties/{propertyID}` / `PATCH /api/v1/properties/{propertyID}`
- **Property Branding & Sanitized Logo (`PWE-4`):**
  - `GET|PUT /api/v1/properties/{propertyID}/branding` — Inspect or update WCAG-validated brand colours, header label, and admin terminology (`property:read` / `property:write`).
  - `GET|POST|DELETE /api/v1/properties/{propertyID}/branding/logo` — Download, upload (magic-byte + re-encoded PNG/JPEG/WebP `<= 2 MiB`), or remove property logo.
- **Workspace Profiles & Custom Dashboard Layout (`PWE-3` / `PWE-5`):**
  - `GET|PUT /api/v1/properties/{propertyID}/workspace/dashboard` & `POST .../workspace/dashboard/reset`
  - `GET|PUT /api/v1/properties/{propertyID}/workspace/profile` & `POST .../workspace/profile/reset`
- **Inventory & Tariffs:**
  - `GET|POST /api/v1/properties/{propertyID}/unit-types`, `PATCH|DELETE /api/v1/properties/{propertyID}/unit-types/{id}`
  - `GET|POST /api/v1/properties/{propertyID}/units`, `PATCH|DELETE /api/v1/properties/{propertyID}/units/{id}`
  - `GET|POST /api/v1/properties/{propertyID}/extras`, `PATCH|DELETE /api/v1/properties/{propertyID}/extras/{id}`
  - `GET|POST /api/v1/properties/{propertyID}/rate-plans`, `PATCH /api/v1/properties/{propertyID}/rate-plans/{id}`
  - `GET|POST /api/v1/properties/{propertyID}/rate-rules`, `PATCH|DELETE /api/v1/properties/{propertyID}/rate-rules/{id}`
  - `GET /api/v1/properties/{propertyID}/rate-preview?unit_type_id=...&rate_plan_id=...&start_date=YYYY-MM-DD&end_date=YYYY-MM-DD`
  - `GET|POST /api/v1/properties/{propertyID}/availability-blocks` & `POST .../availability-blocks/{id}/release` (single-unit compatibility wrapper over `blockout_groups` + `inventory_claims`).

---

## 3. Reservations, Financials, Blockouts & Scheduled Releases
- `GET|POST /api/v1/properties/{propertyID}/channels` — List or add booking channels.
- `GET|POST /api/v1/properties/{propertyID}/reservations` — Filterable list and multi-unit reservation creation (`excl_inventory_claims_no_overlap` GiST enforced).
- `GET|PATCH /api/v1/properties/{propertyID}/reservations/{id}` — Detail and optimistic-concurrency (`version`) amendments.
- `POST /api/v1/properties/{propertyID}/reservations/{id}/transition` — Body `{"status","reason","version"}` (`INQUIRY`, `PENDING`, `CONFIRMED`, `CHECKED_IN`, `CHECKED_OUT`, `CANCELLED`, `NO_SHOW`).
- `GET /api/v1/properties/{propertyID}/reservations/{id}/financials` — Quote snapshot (`booking_quotes`), `payments`, and `refunds` ledger.
- `GET|PUT /api/v1/properties/{propertyID}/booking-policy` — Deposit mode (`FULL`/`PERCENTAGE`/`FIXED`), hold minutes, extra guest charges, terms version, `stripe_enabled`, and `guest_portal_enabled`.
- `GET /api/v1/properties/{propertyID}/payment-exceptions` & `POST /api/v1/properties/{propertyID}/payment-exceptions/{id}/resolve` — List and resolve (`resolution_notes`) Stripe payment reconciliation exceptions (`EXPIRED_HOLD_PAID`, `OVERLAP_ON_CONFIRM`, `AMOUNT_MISMATCH`).
- `GET|POST /api/v1/properties/{propertyID}/blockouts`, `POST .../blockouts/preview`, `GET .../blockouts/{id}`, `POST .../blockouts/{id}/release`, `POST .../blockouts/{id}/reinstate`, `PATCH .../blockouts/{id}/dates`.
- `GET|POST /api/v1/properties/{propertyID}/releases`, `PATCH .../releases/{id}`, `POST .../releases/{id}/release-now`, `POST .../releases/{id}/cancel`.
- `GET /api/v1/properties/{propertyID}/calendar` & `GET /api/v1/properties/{propertyID}/availability-check`.

---

## 4. Phase 4 — Live OTA Channel Manager Sync & Webhooks
- `GET /api/v1/properties/{propertyID}/integrations/channel-manager` — Provider definitions (`beds24`, `smoobu`, `lodgify`, `channex`) + sanitized connection metadata (`credential_hints` only).
- `PUT /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}` — Encrypt credentials via `AES-256-GCM` (`LOVELACE_ENCRYPTION_KEY`) and save as `CONFIGURED` / `PENDING_VERIFICATION` (direct `CONNECTED` status rejected until verified).
- `POST /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/verify` — Execute live adapter connectivity probe, populate capability matrix, and promote status to `CONNECTED`.
- `POST /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/disconnect` — Clear local credentials, set `DISABLED`, and attempt remote webhook deregistration while surfacing any orphaned `webhook_id` / `webhook_error_code`.
- `POST /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/status` & `DELETE .../channel-manager/{providerCode}`.
- `GET /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/listings` — Live external room/listing discovery.
- `GET /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/health` — Sync telemetry, checkpoint digests, and open `UNMAPPED` / `CONFLICT_OVERBOOKED` counts.
- `GET|PUT /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/mappings` & `DELETE .../mappings/{mappingID}` — Property-scoped listing mappings (`LOVELACE_ARI_PROVIDER_BOOKINGS` or `READ_ONLY_IMPORT`).
- `POST /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/sync/preview` & `POST .../sync` — Dry-run preview and leased outbound ARI / reservation pull sync (`confirm_scope: true` required for multi-listing or `> 14` days).
- `POST /api/v1/properties/{propertyID}/integrations/channel-manager/jobs/process` — Trigger immediate sync worker pass.
- `POST /api/v1/webhooks/channels/{providerCode}/{connectionID}` — Unauthenticated, strictly HMAC/secret-verified inbound OTA webhook receiver.

---

## 5. Operations Centre, Housekeeping & Maintenance (Phase 3 M1–M2)
- `GET /api/v1/properties/{propertyID}/operations?date=YYYY-MM-DD&focus=all|front_desk|housekeeping|attention`
- `POST /api/v1/properties/{propertyID}/housekeeping/tasks`, `POST .../housekeeping/tasks/{taskID}/stage`, `POST .../housekeeping/tasks/{taskID}/assign`
- `GET /api/v1/properties/{propertyID}/operations/maintenance-summary`
- `GET|POST /api/v1/properties/{propertyID}/maintenance` & `GET|PATCH .../maintenance/{requestID}`
- `POST /api/v1/properties/{propertyID}/maintenance/{requestID}/assign`, `.../transition`, `.../work-notes`, `.../inspection` (`PASSED`/`FAILED`), `.../attachments`
- `GET /api/v1/properties/{propertyID}/maintenance/attachments/{attachmentID}`
- `POST /api/v1/properties/{propertyID}/maintenance/{requestID}/inventory-restriction`, `PATCH .../inventory-restriction/{linkID}`, `POST .../release-restriction`
- `GET|POST|PATCH` on `/maintenance/categories`, `/maintenance/locations`, `/maintenance/assets`, `/maintenance/contractors`, `/maintenance/schedules`, plus `POST /maintenance/schedules/run`.

---

## 6. Guest CRM, Enquiries, Outbound Email, Webhooks & GDPR (Phase 3 M3–M4)
- **Inbound Email Delivery Webhook:** `POST /api/v1/webhooks/email-events` (`X-Email-Signature: sha256=<hex>` verified with `LOVELACE_EMAIL_WEBHOOK_SECRET`).
- **Guest Profiles, Merges & GDPR:**
  - `GET|POST /api/v1/properties/{propertyID}/guests` & `GET|PATCH .../guests/{guestID}`
  - `GET /api/v1/properties/{propertyID}/guests/duplicates`, `POST .../guests/duplicates/dismiss`, `POST .../guests/merge`
  - `POST|DELETE /api/v1/properties/{propertyID}/guests/{guestID}/preferences` (`/{prefID}`)
  - `POST|DELETE /api/v1/properties/{propertyID}/guests/{guestID}/notes` (`/{noteID}`)
  - `POST /api/v1/properties/{propertyID}/guests/{guestID}/consent` — Append-only marketing consent record.
  - `GET /api/v1/properties/{propertyID}/guests/{guestID}/export` — Operational CRM JSON export (`guests:export`).
  - `POST /api/v1/properties/{propertyID}/guests/{guestID}/anonymize` — Cluster-wide GDPR anonymisation (`guests:anonymize`; `409` if active stay or `DISPATCHING`/`DISPATCH_UNKNOWN` outbox item).
  - `POST /api/v1/properties/{propertyID}/guests/{guestID}/portal/send-verification` & `POST .../portal/revoke-sessions` — Staff-initiated Guest Portal email verification and session revocation.
- **Enquiries & Communications:**
  - `GET|POST /api/v1/properties/{propertyID}/enquiries`, `GET|PATCH .../enquiries/{enquiryID}`, `POST .../enquiries/{enquiryID}/messages`
  - `POST /api/v1/properties/{propertyID}/guests/{guestID}/interactions` & `POST .../guests/{guestID}/emails`
  - `POST /api/v1/properties/{propertyID}/communications/{commID}/complete-followup`
  - `GET /api/v1/properties/{propertyID}/failed-emails`, `POST .../failed-emails/{outboxID}/retry`, `POST .../outbox/{outboxID}/reconcile` (`comms:reconcile` — non-PII structured evidence required).
  - `GET|POST|PATCH /api/v1/properties/{propertyID}/communication-templates` (+ `POST .../preview`), `GET|POST|PATCH .../automation-rules`, `GET .../communication-jobs`, `GET|PUT .../retention-policy`.

---

## 7. Users, Audit Logs, Redaction & System Backups
- `GET|POST /api/v1/users`, `PATCH /api/v1/users/{id}`, `POST /api/v1/users/{id}/reset-password` (`users:manage`).
- `GET /api/v1/audit-logs?property_id=...&resource_type=...&limit=...` — Queries `audit_logs_effective` including sanitized `before_state` / `after_state` JSON snapshots (`audit:read`).
- `POST /api/v1/properties/{propertyID}/audit-logs/{auditLogID}/redact` — Appends a non-PII validated replacement snapshot to `audit_log_redactions` across all `47` allowlisted resource types (`audit:read` + `guests:anonymize`).
- **System Backups & Disaster Recovery (Global `owner_admin` only):**
  - `GET /api/v1/backups` — Configured cloud targets + recent backup/restore runs.
  - `POST /api/v1/backups/export` — Streams portable `.zip` or Argon2id + AES-256-GCM `.zip.enc` archive (`X-Backup-SHA256`).
  - `POST /api/v1/backups/targets`, `PATCH /api/v1/backups/targets/{id}`, `DELETE /api/v1/backups/targets/{id}`, `POST /api/v1/backups/targets/{id}/verify`, `POST /api/v1/backups/targets/{id}/run`.
  - `POST /api/v1/backups/verify` — Non-destructive multipart `.zip` / `.zip.enc` SHA-256 manifest verification.
  - `POST /api/v1/backups/restore` — Multipart restore strictly into an empty (`0`-property) environment.

---

## 8. Phase 5 — Reporting & Business Intelligence
- `GET /api/v1/properties/{propertyID}/reports/overview`, `.../occupancy`, `.../revenue`, `.../channels`, `.../seasons`, `.../forecast`, `.../retention` (`reports:read`).
- `GET /api/v1/properties/{propertyID}/reports/accounting-export?type=booking_line_items|payment_ledger|refund_ledger&format=csv|json` (`reports:export`, `lovelace.accounting.v1`).

---

## 9. Public Storefront & Guest Portal Internal API (`/api/public/v1/*`)
Authenticated server-to-server from `lovelace-public-web` via `X-Lovelace-Public-Key` (`LOVELACE_PUBLIC_API_SECRET`) and completely isolated from `lovelace_session`:
- **Storefront & Checkout:** `GET /api/public/v1/properties/{slug}`, `GET .../properties/{slug}/logo`, `GET /api/public/v1/accommodation`, `GET .../accommodation/{code}`, `POST /api/public/v1/availability`, `POST /api/public/v1/pricing`, `POST /api/public/v1/booking/holds`, `POST .../booking/holds/{id}/cancel`, `POST /api/public/v1/checkout`, `POST /api/public/v1/webhooks/stripe`, `GET /api/public/v1/booking-confirmation`, `POST .../lookup`, `POST .../rotate-token`, `POST .../cancel-request`.
- **Self-Service Guest Portal (Dual-Gated):** Mounted under `/api/public/v1/properties/{slug}/portal/*` (and `/api/public/v1/portal/*`): `/auth/challenge`, `/auth/verify` (`/auth/redeem`), `/auth/booking-token`, `/auth/session` (`/session`), `/auth/logout` (`/logout`), `/auth/step-up/challenge`, `/auth/step-up/verify`, `GET /bookings`, `GET /bookings/{reference}`, `PATCH /bookings/{reference}/arrival`, `POST /bookings/{reference}/change-request`, `POST /bookings/{reference}/cancel-request`, `GET|PATCH /profile`, `POST /profile/email-change`, `POST /profile/email-change/confirm`, `POST /consent`, `GET|POST /enquiries`, `POST /enquiries/{id}/messages`, `POST /privacy/sar` (`/privacy/sar-request`), and `POST /privacy/erasure` (`/privacy/erasure-request`).

Full OpenAPI 3.1 specifications: [openapi-admin-v1.yaml](openapi-admin-v1.yaml) · [openapi-public-v1.yaml](openapi-public-v1.yaml).
