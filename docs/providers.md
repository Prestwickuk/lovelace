# Channel Manager Provider Registry & Auth Models

All providers in [`internal/channels/registry.go`](../internal/channels/registry.go) remain **scaffolding & encrypted configuration vault only** (`has_live_adapter: false`). Live synchronization workers, inbound webhooks, and OTA rate/availability pushes are deferred to **Phase 4**. Attempting to promote any connection to `CONNECTED` returns `422`.

## Verified Provider Authentication Matrix

| Code | Provider | Auth Method | Required Inputs & Contract | Official Documentation Reference |
|---|---|---|---|---|
| `beds24` | Beds24 | `token` | `property_id` + secret `refresh_token` (`refreshToken` header on `GET /authentication/token` → 24h access token) | https://beds24.com/api/v2/ |
| `smoobu` | Smoobu | `hmac_sha256` | `api_key` + `api_secret` ONLY (account details resolved in Phase 4 via `GET /api/me`). Canonical signing spec: Base64 `HMAC-SHA256` over HTTP method, path, RFC 3986 query string, UTC timestamp, nonce, SHA-256 body hash, and API key | https://docs.smoobu.com/ |
| `lodgify` | Lodgify | `api_key` | `account_id` (optional) + secret `api_key` (`X-ApiKey` HTTP header against Public API v2) | https://docs.lodgify.com/reference/getting-started-with-your-api |
| `channex` | Channex | `api_key` | `property_id` (optional), `environment` (`production` / `staging`), + secret `user_api_key` (`user-api-key` HTTP header; NOT OAuth2 client credentials) | https://docs.channex.io/api-v.1-documentation/api-reference |

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
