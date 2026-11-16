# ADR-0001: Public/Administrative Separation and Personalisation Configuration Boundaries

- **Status:** Accepted (Existing Separation Boundary) / Proposed Extension (`PROPOSED / FUTURE` PWE Boundaries)
- **Date:** 2026-11-15
- **Scope:** Platform Architecture (`internal/webui`, `internal/branding`, `apps/public-web/`, `docs/personalisation-workspace-experience.md`)

---

## 1. Context

LOVELACE operates as a multi-property hospitality operations platform with two distinct user audiences and branding requirements:

1. **Administrative operators (`admin_users`)** using the Go server-rendered console (`internal/webui`, `:8080`), who require high-density operational workflows, strict RBAC, multi-property context, auditability, and — in the **PROPOSED / FUTURE** Personalisation & Workspace Experience (PWE) workstream — role-appropriate workspaces, themes, density controls, dashboards, and restrained property-identifying chrome.
2. **Prospective and booked guests** using the standalone Next.js public storefront (`apps/public-web/`, `:3000`), who expect a property-first guest experience (e.g., *Tir Bach Campsite*) with minimal *"Powered by LOVELACE Booking System"* attribution.

As LOVELACE delivers **PWE-1 through PWE-4** (Implemented) and scopes **`PWE-5`** (`PROPOSED / FUTURE`), adding property branding (`PWE-4`) and user/workspace customisation (`PWE-1`–`PWE-3`, `PWE-5`) creates architectural risk if public storefront concerns, admin UI preferences, or executable presentation payloads cross application, tenancy, or security boundaries.

---

## 2. Decision

LOVELACE establishes the following permanent architectural and configuration boundaries:

### 2.1 Permanent Two-Application Deployment & Component Boundary
- **LOVELACE Admin (`internal/webui` + `/api/v1/*`)** remains a Go `html/template` server-rendered application embedded in the Go monolith, served on the administrative origin under strict Content Security Policy (`default-src 'self'; script-src 'self'; style-src 'self'; object-src 'none'; frame-ancestors 'none'`), Argon2id cookie sessions (`tirbach_session`), and per-session CSRF validation.
- **Public Guest Storefront (`apps/public-web/`)** remains an independently built and deployed Next.js / React / TypeScript application on separate origin(s) with its own server-side BFF (`X-Lovelace-Public-Key`), completely isolated authentication boundary, and **zero** direct PostgreSQL access (`DATABASE_URL` and DB drivers prohibited).
- **No shared UI component library or CSS bundle:** Go SSR templates (`/static/*.css`) and Next.js (`apps/public-web/`) maintain separate design implementations and release lifecycles. Neither build may import templates, components, assets, or routing code from the other.

### 2.2 Three-Layer Identity Separation
1. **Platform Identity (`LOVELACE Booking System`):** Owned by `internal/branding` (`branding.Default()`). Permanent platform identity for the administrative login shell, legal/error screens, platform headers, and unobtrusive public attribution. Never overwritten by property or personal settings.
2. **Property Identity (`PWE-4` — Implemented, building on `properties.branding_config` and `property_branding_settings`):** Owned by authorized property administrators (`PermPropertyWrite` scoped to `property_id`). Controls approved public storefront branding *and* subtle admin workspace identification (verified logo in `BlobStore`, display name, contrast-validated custom `#RRGGBB` primary/secondary palette, and bounded admin-only domain terminology) without erasing LOVELACE platform identity or leaking across tenants.
3. **User Preferences & Workspace Presentation (`PWE-1`–`PWE-3` Implemented; `PWE-5` `PROPOSED / FUTURE`):** Owned strictly by each authenticated `admin_user` within platform-approved bounds (theme mode, curated accent, density, text scale, reduced motion, navigation/shortcuts, saved operational filters, and dashboard widget layout). Never mutates property identity, never affects another user, and never applies to public guest sessions.

### 2.3 API & Data Access Boundary Between Public and Admin
- `apps/public-web/` may consume **only** explicitly approved public property-branding projections exposed via `/api/public/v1/properties/{slug}` (e.g., public display name, tagline, validated public logo URL, approved public primary/accent colours, booking policies, and public domain mapping).
- `apps/public-web/` MUST **never**:
  - read `admin_user` preferences, personal shortcuts, saved operational filters, or workspace profiles;
  - read internal `PWE-3` dashboard configurations, widget payloads, or internal terminology notes;
  - read `admin_users`, `admin_sessions`, `audit_logs`, `channel_connections`, maintenance records, or CRM guest notes;
  - invoke `/api/v1/*` administrative endpoints or connect directly to PostgreSQL.
- Every future PWE read/write API under `/api/v1/*` MUST enforce existing server-side authentication (`must_rotate_password = FALSE` gate), CSRF protection, RBAC permissions (`internal/auth`), and `requirePropertyScope` isolation.

### 2.4 Non-Executable Presentation & Authorization Invariance
- Future **PWE-1 – PWE-5** configurations MUST be strictly declarative, schema-validated data (enums, bounded tokens, verified asset references, and typed JSON widget configuration schemas). Arbitrary HTML, inline `<style>`, inline `<script>`, custom CSS injection, or executable expressions are permanently prohibited.
- Workspace profiles (`PWE-5`), personal shortcuts (`PWE-2`), and custom dashboard widget layouts (`PWE-3`) affect **presentation only**. Hiding a widget or selecting a preset never grants or removes backend authorization; every endpoint, template section, and widget data query MUST enforce `internal/auth` permissions and property scope independently.

---

## 3. Consequences

### Positive
- Zero regression risk to Phase 1–5 security posture, CSP `style-src 'self'`, and Next.js/Go separation.
- Clear upgrade path from existing `properties.branding_config` and `internal/branding/branding.go` without migration churn or tight coupling between internal staff UX and customer marketing pages.
- Multi-property operators can switch properties safely while keeping personal accessibility settings (dark mode, contrast, reduced motion, text scale) intact globally and property layouts isolated per tenant.

### Trade-offs & Constraints
- CSS tokens and component refinements in Go SSR (`internal/webui/static/app.css`) must be maintained separately from Tailwind/React styling in `apps/public-web/`, accepting deliberate duplication in exchange for deployment and blast-radius independence.
- Property branding in the admin console is intentionally restrained (approved tokens/header chrome rather than arbitrary custom themes or untrusted external image URLs) to preserve accessibility contrast and CSP guarantees.

---

## 4. Verification & Related Documents

- [Architecture & Delivery Blueprint](../architecture.md)
- [Personalisation & Workspace Experience Specification (`PROPOSED / FUTURE`)](../personalisation-workspace-experience.md)
- [Branding & Design System](../branding.md)
- [Phase 2 Direct Booking & Origin Isolation](../phase2-architecture.md)
- [Security Controls & Test Isolation](../security.md)
