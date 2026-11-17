# Channel Manager Provider Registry & Live Sync Adapters (Phase 4)

All four providers in [`internal/channels/registry.go`](../internal/channels/registry.go) now have live adapter implementations (`has_live_adapter: true`) backed by shared transport/security controls in `internal/channels/` and durable leased workers in `internal/service/channel_sync_service.go`. Unsupported or gated capabilities fail closed honestly (`BLOCKED` or `NOT_IMPLEMENTED`).

> **Offline Verification Note:** Automated CI and repository tests verify every adapter protocol flow, token exchange, HMAC signing/replay guard, rate-limit retry, webhook verification, and conflict import against offline `httptest` provider doubles + PostgreSQL 17. Live sandbox testing against real vendor accounts requires operator-supplied credentials in a staging environment.

## Verified Provider Contract & Capability Matrix

| Code | Base URL / Environment | API & Auth Flow | Supported (`SUPPORTED`) | Blocked / Not Implemented | Rate Limits, Pagination & Webhook Rules | Official Docs |
|---|---|---|---|---|---|---|
| `beds24` | `https://api.beds24.com/v2` | API v2 invite/refresh token exchange (`GET /authentication/setup` if `invite_code`, `GET /authentication/token` with `refreshToken` header → 24h short-lived `token`, validated with `GET /authentication/details`) | `listing_discovery`, `availability_sync`, `rate_sync`, `reservation_import`, `webhooks` | `token_revoke`: `NOT_IMPLEMENTED` (revoke tokens in Beds24 Control Panel) | `POST /inventory/rooms/calendar`, `GET /bookings` (`pages.nextPageExists` pagination); retry-after & 429 aware; webhook verified via `X-Lovelace-Webhook-Secret` / `Authorization: Bearer` or HMAC re-fetch | https://beds24.com/api/v2/ |
| `smoobu` | `https://login.smoobu.com/api` | API Key + canonical HMAC-SHA256 signing (`X-API-Key`, `X-Timestamp`, `X-Nonce`, `X-Signature`); verified via `GET /me` (no manual `user_id` input) | `listing_discovery`, `availability_sync`, `rate_sync`, `reservation_import`, `webhooks` | `token_revoke`: `NOT_IMPLEMENTED` (revoke keys in Smoobu Settings) | `POST /rates`, `GET /apartments`, `GET /reservations` (`page_count` pagination) & `GET /reservations/{id}` hydration; webhook HMAC + nonce replay guard (or `X-Lovelace-Webhook-Secret`), plus `POST /users/webhooks` registration helper | https://docs.smoobu.com/ |
| `lodgify` | `https://api.lodgify.com` | Public API v1 & v2 (`X-ApiKey` header); verified via `GET /v2/properties` | `listing_discovery`, `rate_sync` (`POST /v1/rates/savewithoutavailability`), `reservation_import` (`GET /v2/reservations/bookings` & `/{id}`), `webhooks` | `availability_sync`: **`BLOCKED`** (Public API v1/v2 has no direct outbound availability count write without Partner Connectivity approval); `token_revoke`: `NOT_IMPLEMENTED` | Attempting an availability push returns HTTP `422` (`capability_blocked`); webhook verified via `X-Lovelace-Webhook-Secret` or HMAC and re-fetched via v2 API | https://docs.lodgify.com/reference/getting-started-with-your-api |
| `channex` | `https://app.channex.io/api/v1` (`production`) / `https://staging.channex.io/api/v1` (`staging`) | `user-api-key` header (NOT OAuth2 client credentials); verified via `GET /properties` | `listing_discovery` (`/properties`, `/room_types`, `/rate_plans`), `availability_sync` (`POST /availability`), `rate_sync` (`POST /restrictions`), `reservation_import` (`GET /booking_revisions/feed` + `POST /booking_revisions/{id}/ack`), `webhooks` | `token_revoke`: `NOT_IMPLEMENTED` (revoke in Channex UI) | Dual-secret webhook rotation (`webhook_secret` + `webhook_secret_previous`) verified via `x-hmac-timestamp` (<= 5 min) + `x-hmac-signature-256`; revisions acknowledged via `/ack` ONLY after durable local DB commit | https://docs.channex.io/api-v.1-documentation/api-reference |

## Smoobu Canonical HMAC-SHA256 Contract (Phase 4 Target)

LOVELACE stores only `api_key` and `api_secret` for Smoobu (encrypted at rest); no manual `user_id` is requested because account identity is discovered via `GET /api/me` when live synchronization activates in Phase 4.

Canonical signing specification declared in [`internal/channels/registry.go`](../internal/channels/registry.go):

1. **Headers**: `X-API-Key`, `X-Timestamp` (UTC ISO-8601), `X-Nonce` (unique per request), `X-Signature`.
2. **Canonical Components**:
   - `HTTP_METHOD` (uppercase, e.g. `GET`, `POST`)
   - `REQUEST_PATH` (raw path)
   - `CANONICAL_QUERY_RFC3986` (RFC 3986 percent-encoded & deterministically ordered query parameters)
   - `UTC_TIMESTAMP` (`X-Timestamp`)
   - `NONCE` (`X-Nonce`)
   - `SHA256_BODY_HASH` (lowercase hex SHA-256 of request body bytes, empty string digest for empty bodies)
   - `API_KEY` (`api_key`)
3. **Digest Encoding**: `Base64(HMAC-SHA256(api_secret, canonical_string))`.
4. **Replay Protection**: Reject requests outside a bounded clock-skew window (≤ 5 minutes) and enforce single-use nonce tracking on inbound webhooks/requests.

## Encryption & Masking Invariants

1. Secret fields (`FieldSpec.Secret == true`) are encrypted together with AES-256-GCM (`internal/secrets`) keyed by `TIRBACH_ENCRYPTION_KEY` and persisted as opaque bytes in `channel_connections.encrypted_credentials`.
2. API responses and admin views return only `credential_hints` (e.g. `••••9988`).
3. `channel_connections.auth_method` accepts `'api_key'`, `'hmac_sha256'`, `'oauth2'`, `'client_credentials'`, and `'token'` (`0003_phase1_5.sql`).
