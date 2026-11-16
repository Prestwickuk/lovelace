# LOVELACE Personalisation & Workspace Experience (`PWE-1`–`PWE-3` Implemented; `PWE-4`–`PWE-5` `PROPOSED / FUTURE`)

> **Document Status:** `PWE-1` (Design System & Theme Engine), `PWE-2` (User Personalisation), and `PWE-3` (Customisable Dashboard Builder) are **Implemented** in Go SSR & REST (`internal/branding/theme.go`, `internal/domain/personalisation.go`, `internal/domain/dashboard.go`, `migrations/0022_pwe2_user_personalisation.sql`, `migrations/0023_pwe3_dashboard_builder.sql`, `internal/store/postgres/personalisation_repo.go`, `internal/store/postgres/dashboard_repo.go`, `internal/service/personalisation_service.go`, `internal/service/dashboard_service.go`, `internal/httpapi/handlers_workspace.go`, `internal/webui`). `PWE-4` and `PWE-5` remain **`PROPOSED / FUTURE`** specification only.
>
> **Implementation Status:** **PWE-1, PWE-2 & PWE-3 Implemented** (`0022` adds `admin_user_preferences` and `admin_user_property_workspaces`; `0023` adds `dashboard_schema_version` and `dashboard_layout` to `admin_user_property_workspaces` under `lovelace_app` DML grants while keeping `must_rotate_password`, live DB RBAC, and `PWE-2` retention rules intact; `PWE-4` property branding extensions, `PWE-5` workspace profiles, and `apps/public-web/` changes do not exist).

---

## 1. Roadmap Placement & Dependencies (`PROPOSED / FUTURE`)

**Workstream Title:** **LOVELACE Personalisation & Workspace Experience (`PWE`)**

**Proposed Timing:** Planned as an optional, modular product workstream positioned **after** Phase 3 guest-management and operations foundations (**Phase 3 Milestone 1 Operations Centre**, **Milestone 2 + 3.2.x Maintenance**, and **Milestone 3 + 3.3.x Guest CRM & Privacy**) are operationally mature. It runs independently of **Phases 4–5** and **Phase 3 Milestone 4** without delaying or renumbering any committed milestone (`Phases 1–5` and `Phase 3 M1–M4` remain untouched).

### Rationale & Non-Blocking Positioning
- **Not a launch prerequisite:** Neither Phase 3.2 acceptance, direct public bookings (`Phase 2`), live OTA channel sync (`Phase 4`), nor historical BI reporting (`Phase 5`) depends on `PWE`. Existing `internal/branding`, `internal/webui`, and `properties.branding_config` continue operating safely without `PWE`.
- **Depends on mature Phase 3 operational roles & data:** Role-oriented workspace presets (`PWE-5`) and operational dashboard widgets (`PWE-3`) are only meaningful once real housekeeping, maintenance, reservations, payment exceptions, guest CRM, and reporting workflows exist in production (`owner_admin`, `manager`, `staff`, `maintenance_staff`, `housekeeping_staff`, `viewer`).
- **Foundational prerequisites already in place:**
  - Server-rendered Go console (`internal/webui`) with centralized brand/token definitions ([`internal/branding/branding.go`](../internal/branding/branding.go), [`internal/webui/static/app.css`](../internal/webui/static/app.css)).
  - Strict Content Security Policy (`style-src 'self'; script-src 'self'; object-src 'none'`; zero inline styles or scripts).
  - Fine-grained permission model ([`internal/auth/rbac.go`](../internal/auth/rbac.go)) and composite property-scoped data boundaries.
  - Per-property JSONB hook `properties.branding_config` (`migrations/0003_phase1_5.sql`) consumed today solely by public branding foundations.

---

## 2. Design System Architecture & Administrative Design Standard (`PROPOSED / FUTURE`)

This section defines the permanent design governance for **PWE-1** and all future administrative console views (`internal/webui`).

### 2.1 Technical Architecture (Go SSR + Static CSS Tokens)
- **Server-Rendered Go Templates Only:** Must operate strictly inside LOVELACE's existing Go `html/template` + static asset server architecture (`internal/webui/templates/`, `internal/webui/static/`). **No** JavaScript SPA framework (React/Vue/etc.), CSS-in-JS runtime, Tailwind build chain, or client runtime dependency is introduced into the admin console.
- **Strict CSP Compliance (`style-src 'self'`):** Themes, accents, density modes, text scale, and high-contrast variants MUST be applied cleanly via server-emitted `<html>`/`<body>` attributes or classes (e.g., `data-theme="dark"`, `data-accent="teal"`, `data-density="compact"`, `data-contrast="high"`) mapped to CSS custom properties inside static stylesheet files (`/static/*.css`). Inline `style="..."` attributes and `<style>` blocks remain prohibited.
- **Public Storefront Independence:** `apps/public-web/` (Next.js / React / TypeScript) retains an independent design system, styling pipeline, and deployment boundary per [ADR-0001](adr/0001-public-admin-separation-and-pwe-boundaries.md).

### 2.2 Product Aesthetic & Design Direction
LOVELACE Admin is **refined, restrained, information-dense hospitality operations software** built for daily reception, reservations, housekeeping, maintenance, and finance work — never a consumer marketing page or generic template dashboard.

| Element | Administrative Design Standard (`PROPOSED / FUTURE`) |
|---|---|
| **Typography** | Clean system sans stack (`system-ui`, `Inter`, `-apple-system`, sans-serif) paired with tabular/monospace numerals (`--font-mono`, `font-variant-numeric: tabular-nums`) for rates, money, dates, stay nights, booking refs (`TB-*`, `MNT-*`, `ENQ-*`), and audit timestamps. Clear modular heading hierarchy (`h1` <= 22–24px, crisp section labels) without oversized hero typography. |
| **Spacing & Density** | Compact spacing scale (`4px` base grid) supporting explicit `compact` and `comfortable` density tokens without breaking row alignment or touch/keyboard hit targets (`>= 32px` compact controls, `>= 40px` comfortable). |
| **Colour & Tokens** | Warm parchment light surface (`#F6F1E5` / `#FFFDF8`) and deep slate-charcoal dark surfaces (`#151917` / `#1D2320`), anchored by Coastal Heritage slate-teal (`#2F4F4A`) and brass-gold (`#C49A45`) defaults, plus curated WCAG-verified accent options. Status tokens (`ok`, `warn`, `err`, `info`, `alloc`, `unrel`) are kept distinct from brand accents. |
| **Buttons & Contextual Actions** | Restrained rectangular controls (`border-radius <= 6px–8px`), clear primary/secondary/danger hierarchy, concise labels, inline contextual actions next to table rows or panel headers, and quiet ordinary links without button bloat. |
| **Forms & Validation** | Grouped, scannable inputs on logical two-column/responsive grids with persistent `<label>` bindings, concise inline help text, field-level server validation errors (`role="alert"` / `aria-describedby`), and explicit unsaved/destructive confirmation patterns. |
| **Tables & Operational Lists** | Bordered/zebra-subtle full-width operational tables with sticky or crisp headers, right-aligned numeric/currency columns, unambiguous unit/guest/reservation links, predictable sort/filter bars, and clean horizontal overflow containment on narrow viewports. |
| **Calendars** | High-density multi-unit date grids with unmistakable distinction between `CONFIRMED`/`CHECKED_IN`, `PENDING`, `HOLD`, `MAINTENANCE`, `OWNER_USE`, `CHANNEL_ALLOCATION`, and `CHANNEL_RESTRICTION` using border styles, pattern/text tags, and status badges alongside colour. |
| **Navigation** | Predictable left rail grouped by operational domain, explicit active property context indicator, permission-filtered entries, and personal keyboard-accessible shortcuts without oversized CTA tiles or floating popovers. |
| **Status Indicators** | Compact uppercase/pill semantic tags with text + icon/symbol or explicit state word (never bare colour dots alone). |
| **Dialogs & Confirmations** | Native semantic confirmation workflows (or accessible `<dialog>` with focus trap and Esc close) reserved strictly for destructive or scope-confirming operations (e.g., anonymisation, claim release, bulk OTA sync). |
| **Empty & Error States** | Direct operational explanation + exact next valid action or filter reset; server errors map cleanly to LOVELACE `error.html` without leaking internal tracebacks. |
| **Dashboard Widgets** | Flat/lightly bordered operational containers with tight header + single primary action link + tabular/list/metric payload — zero multi-layer card nesting. |

### 2.3 Explicit Anti-Patterns (Prohibited in Admin UI)
To prevent visual drift and usability degradation, future `PWE` implementations MUST reject:
- Decorative hero cards, marketing-style dashboard copy, or filler onboarding banners inside operational pages.
- Excessive rounded corners (`pill` panels) or nested cards-inside-cards.
- Unnecessary gradients or purple/blue gradient-heavy SaaS styling.
- Excessive vertical/horizontal whitespace that pushes operational schedules, arrivals, or ledger rows below the fold.
- Repeated generic 4-box metric grids where a structured queue, table, or changeover strip belongs.
- Unnecessary heading icons cluttering section titles.
- Floating overlays obscuring calendar grids or table rows.
- Oversized ordinary navigation items.
- Imposing identical property branding/layout presentation across all properties or wiping out individual property identification in multi-property accounts.

### 2.4 Accessibility Standards (Mandatory Acceptance Criteria)
No theme, accent colour, compact density mode, or workspace profile may degrade or gate core LOVELACE functionality:
1. **Keyboard Navigation:** Every interactive control (sidebar nav, property switcher, filter forms, calendar cells/actions, dashboard widget edit controls, dialogs) is reachable via `Tab` / `Shift+Tab` and operable with `Enter` / `Space` / `Esc`.
2. **Focus Visibility:** Unsuppressed, high-contrast `:focus-visible` outlines (`>= 3:1` against adjacent surfaces) across light, dark, and high-contrast themes.
3. **Colour Contrast:** WCAG 2.1 AA minimum (`4.5:1` normal text, `3:1` large text & UI borders/icons) enforced across every platform theme, curated accent, and `PWE-4` property palette pairing; High-Contrast mode targets stronger boundary legibility.
4. **Screen-Reader Semantics:** Native semantic HTML (`<nav>`, `<main>`, `<header>`, `<table>`/`<th scope>`, `<button>`, `<fieldset>`/`<legend>`, `aria-live="polite"` status announcements, accurate `aria-label` on icon-only reorder/action controls).
5. **Text Scaling:** `normal`, `large`, and `xl` text scale tokens resize cleanly via relative units (`rem`) up to 200% browser zoom without clipping text, hiding table columns catastrophically, or overlapping labels.
6. **Reduced Motion:** Honours OS `@media (prefers-reduced-motion: reduce)` and explicit user toggle (`reduced_motion = true`), eliminating non-essential transitions and animations.
7. **Status Independence:** Operational states (occupancy, changeover stage, payment status, channel sync status, maintenance priority) never rely on colour alone — text labels or distinct symbols are always rendered.
8. **Responsive Operation:** Core arrivals, changeover, maintenance, reservation lookup, and dashboard workflows remain usable across desktop, tablet, and narrow mobile widths without horizontal page breakage.

---

## 3. Workstream Milestones (`PWE-1`–`PWE-3` Implemented; `PWE-4`–`PWE-5` `PROPOSED / FUTURE`)

### PWE-1 — Design System and Theme Engine (**Implemented**)
- **Implemented Files & Contract:**
  - [`internal/branding/theme.go`](../internal/branding/theme.go) (`ThemeSelection`, `ResolveTheme`, `CuratedAccents`, `ContrastRatio`) strictly allowlists `data-theme` (`system` default via `prefers-color-scheme`, `light`, `dark`), `data-accent` (`teal` default, `olive`, `slate`, `brass`), `data-contrast` (`standard`, `high`), `data-motion` (`system` default via `prefers-reduced-motion`, `reduced`), `data-density` (`comfortable`, `compact`), and `data-text-scale` (`default`, `large`, `xl`).
  - [`internal/webui/static/app.css`](../internal/webui/static/app.css) defines semantic surface, ink, muted ink, border, solid sidebar chrome (zero decorative gradients), separate `--property-brand` vs `--accent-*` tokens, immutable semantic status tokens (`--ok-*`, `--warn-*`, `--alloc-*`, `--err-*`, `--info-*`, `--unrel-*`), `:focus-visible`, `.skip-link`, `.sr-only`, `rem`-scaled typography, native `<dialog>` styles, and responsive narrow-width layouts under strict `style-src 'self'` CSP.
  - [`internal/webui/templates/partials.html`](../internal/webui/templates/partials.html), [`layout.html`](../internal/webui/templates/layout.html), [`login.html`](../internal/webui/templates/login.html), [`error.html`](../internal/webui/templates/error.html), and representative screens (`dashboard.html`, `calendar.html`, `reservations.html`, `guests_list.html`, `account.html`, `comms_settings.html`) adopt shared partials (`html_attrs`, `head_assets`, `page_header`, `status_badge`, `empty_state`), semantic landmarks/table `scope`, and non-colour status glyphs.
  - Kept Next.js `apps/public-web/` styling, components, and origin completely separate; no preference persistence or DB migrations added.

### PWE-2 — User Personalisation (**Implemented**)
- **Implemented Files & Scope:**
  - **Migration (`0022_pwe2_user_personalisation.sql`):** Adds `public.admin_user_preferences` (`user_id PK ON DELETE CASCADE`, typed PWE-1 theme/accessibility columns, `default_route`, `last_property_id ON DELETE SET NULL`, `preferences JSONB`, `schema_version >= 1`) and `public.admin_user_property_workspaces` (`PK (user_id, property_id) ON DELETE CASCADE`, `preferences JSONB`, `saved_views JSONB`, `schema_version >= 1`), with DML grants for `lovelace_app` and zero PII/secrets stored or audited.
  - **Domain & Validation ([`internal/domain/personalisation.go`](../internal/domain/personalisation.go)):** Strict allowlisted global admin routes (`AdminRoutes`), navigation sections (`NavSections`), validated theme/accessibility/layout fields, property-scoped `SavedOperationalView` presets (`calendar`, `reservations`, `guests` referencing only verified property `unit_type_id` / `channel_code` resources), `ResolveLayerCascade` (`Platform -> [Property/Profile future] -> User Global -> User Active Property`), and field-by-field `DecodeAndNormalize*` upcasting/recovery so malformed JSONB degrades safely without blocking SSR.
  - **Persistence & Service ([`internal/store/postgres/personalisation_repo.go`](../internal/store/postgres/personalisation_repo.go), [`internal/service/personalisation_service.go`](../internal/service/personalisation_service.go)):** Self-ownership (`ErrForbidden` cross-user), live DB `GetAdminUserByID` role/property re-verification, `MustRotatePassword` gate blocking preference mutations and omitting personalisation overrides during mandatory rotation, cross-property `unit_type_id`/`channel_code` rejection, and dynamic filtering of landing routes, pinned shortcuts, and saved views against current server-side permissions.
  - **Admin Console SSR (`internal/webui/handlers_personalisation.go`, `templates/account.html`, `layout.html`, `calendar.html`, `reservations.html`, `guests_list.html`):** Separate Personalisation settings and Reset controls on `/account` (distinct from Change Password), CSRF-protected `/account/preferences/*` POST handlers, post-login allowed `DefaultRoute` redirect (`handleLoginPost`), sidebar collapsed-section persistence & pinned shortcuts, and per-property calendar/reservations/guests defaults and saved views (`?saved_view=`).
- **Dependencies:** `PWE-1` CSS tokens/attributes; authenticated `admin_users` identity (`internal/auth`).

### PWE-3 — Customisable Dashboard Builder (**Implemented**)
- **Implemented Files & Scope:**
  - **Dedicated Route & Preserved Operations Centre (`/`):** Adds `/workspace` (`GET /workspace`, `/workspace?edit=1`, `POST /workspace/widgets/*`, `POST /workspace/reset`) and `/api/v1/properties/{propertyID}/workspace/dashboard` (`GET`, `PUT`, `POST /reset`), plus `/workspace` in the PWE-2 allowlisted route catalogue while preserving `/` Operations Centre intact.
  - **Additive Persistence (`0023_pwe3_dashboard_builder.sql`):** Adds `dashboard_schema_version` and nullable `dashboard_layout JSONB` onto `public.admin_user_property_workspaces` without altering PWE-2 `preferences`, `saved_views`, or retention rules (`NULL` resolves to Layer 1 Platform default layout, leaving `PWE-4` property and `PWE-5` profile hooks explicitly deferred).
  - **Typed Registry & Service ([`internal/domain/dashboard.go`](../internal/domain/dashboard.go), [`internal/service/dashboard_service.go`](../internal/service/dashboard_service.go), [`internal/store/postgres/dashboard_repo.go`](../internal/store/postgres/dashboard_repo.go)):** Implements all 11 typed widgets (`ops.arrivals_summary`, `ops.changeover_board`, `ops.alerts_attention`, `maint.open_queue`, `maint.preventative_due`, `res.pending_inquiries`, `res.mini_calendar`, `crm.open_enquiries`, `channels.sync_health`, `reports.kpi_snapshot`, `workspace.quick_shortcuts`), `422` validation (`ValidateDashboardLayoutInput`), per-widget & per-subsection permission checks before querying data, CRM PII/note exclusion, per-currency KPI + incomplete inventory history warnings, and field-by-field/item-by-item recovery on malformed DB rows.
  - **Zero-JS Accessible Editor & Presentation ([`internal/webui/handlers_workspace.go`](../internal/webui/handlers_workspace.go), [`workspace.html`](../internal/webui/templates/workspace.html), [`app.css`](../internal/webui/static/app.css)):** Supports add/remove, keyboard-accessible Up/Down/Position reordering, supported-size selection (`compact`/`standard`/`wide`), typed config forms, and non-JS `<details>` reset confirmation under strict `style-src 'self'; script-src 'self'` CSP.

#### Typed Widget Registry (**Implemented**)
Every canonical widget below is registered in [`internal/domain/dashboard.go`](../internal/domain/dashboard.go) and resolved through permission-verified read-only service queries in [`internal/service/dashboard_service.go`](../internal/service/dashboard_service.go):

| Widget Identifier | Display Name & Description | Supported Sizes | Required Permissions (`internal/auth`) & Backing Data | Property Scope | Typed Config Schema (`schema_version: 1`) | Default Presentation |
|---|---|---|---|---|---|---|
| `ops.arrivals_summary` | **Arrivals & Departures Today:** Confirmed arrivals, pending holds, due-outs | `standard`, `wide` | `ops:read` + `reservations:read`; Phase 3 Operations summary query | Required (`property_id`) | `{ show_pending_holds: bool, show_in_house_count: bool, limit: 5..25 }` | Table strip grouped by Arrival / Departing with guest status links |
| `ops.changeover_board` | **Daily Changeover Queue:** 5-stage housekeeping readiness & quick advance | `standard`, `wide` | `ops:read` (+ `housekeeping:write` for actions); `housekeeping_tasks` | Required (`property_id`) | `{ stage_filter: ["ALL"\|stages], limit: 5..30 }` | Dense rows ordered `CLEANING_REQUIRED -> INSPECTION_REQUIRED -> READY` |
| `ops.alerts_attention` | **Operational Alerts:** Open payment exceptions, failed OTA sync/conflicts, stale outbox | `compact`, `standard` | `ops:read` (+ filtered sub-counts gated by `reservations:read`, `channels:read`, `comms:read`) | Required (`property_id`) | `{ include_categories: enum[] }` | Prioritised action list; alerts lacking user permission omitted silently |
| `maint.open_queue` | **Open Maintenance Queue:** Urgent/high defects & pending inspections | `standard`, `wide` | `maintenance:read`; `maintenance_requests` | Required (`property_id`) | `{ priority_min: "low"\|"medium"\|"high"\|"urgent", assigned_to_me: bool, limit: 5..20 }` | Tabular queue with `MNT-*` ID, asset/unit, priority text+badge, due date |
| `maint.preventative_due` | **Due Preventative Schedules:** Upcoming recurring tasks | `compact`, `standard` | `maintenance:read`; `maintenance_schedules` | Required (`property_id`) | `{ horizon_days: 7\|14\|30, limit: 5..15 }` | Chronological compact checklist |
| `res.pending_inquiries` | **Provisional Holds & Inquiries:** Unconfirmed bookings needing follow-up | `compact`, `standard` | `reservations:read`; `reservations` (`PENDING` / `INQUIRY`) | Required (`property_id`) | `{ include_inquiries: bool, limit: 5..20 }` | List with expiry/age indicator and reservation link |
| `res.mini_calendar` | **Availability Horizon:** Compact 7- or 14-day unit availability overview | `wide` | `inventory:read` + `reservations:read` + `blocks:read`; availability grid | Required (`property_id`) | `{ days: 7\|14, unit_type_id: uuid\|null }` | Horizontal compact calendar linking straight to `/calendar` |
| `crm.open_enquiries` | **Guest Enquiries & Complaints:** Open `ENQ-*` SLA queue | `standard`, `wide` | `guests:read` + `comms:read`; `guest_enquiries` | Required (`property_id`) | `{ priority_filter: enum\|"ALL", only_complaints: bool, limit: 5..20 }` | Queue without exposing restricted `guest_notes` or `INTERNAL` messages |
| `channels.sync_health` | **Channel Sync Health:** Provider status, failures, unmapped/overbooked counts | `compact`, `standard` | `channels:read`; `channel_connections` + `channel_sync_jobs` | Required (`property_id`) | `{ show_ok_providers: bool }` | Provider status table (`Beds24`, `Smoobu`, `Lodgify`, `Channex`) |
| `reports.kpi_snapshot` | **7/30-Day Performance Snapshot:** Occupancy, ADR, RevPAR (per currency) | `compact`, `standard` | `reports:read`; Phase 5 read-only reporting queries | Required (`property_id`) | `{ window_days: 7\|30, show_prior_period: bool }` | Tabular KPI summary noting incomplete history if applicable |
| `workspace.quick_shortcuts` | **Personal Quick Links:** Pinned routes & saved operational views | `compact` | Authenticated user; dynamically filtered by active user's permissions | Property + User | `{ max_items: 4..12, show_saved_filters: bool }` | Keyboard-accessible concise link list |

### PWE-4 — Property Branding (`PROPOSED / FUTURE`)
- **Scope:**
  - Extend administrative configuration (`PermPropertyWrite`, property-scoped) for authorized operators to configure approved property branding:
    - **Property Logo:** Uploaded image asset validated identically to LOVELACE's attachment security rules (magic-byte sniffing `image/png`, `image/jpeg`, `image/webp` — **strictly rejecting `.svg`**, executables, or arbitrary external hotlinks — sized within bounds and stored securely outside the web root or served via `/api/...` with `X-Content-Type-Options: nosniff`).
    - **Approved Palette / Primary & Secondary Brand Colours:** Server-validated palette selection or contrast-checked hex pair (`>= 4.5:1` verified on registration/save against light/dark chrome surfaces so brand choices cannot produce illegible headers or buttons).
    - **Display Name & Subtitle / Region Label:** Property-facing workspace display title alongside persistent LOVELACE attribution.
    - **Optional Bounded Terminology:** Safe UI vocabulary map (e.g., `"Pitch"`, `"Bell Tent"`, `"Unit"`, `"Cabin"`, `"Site"`, `"Guest"`) limited strictly to allowlisted display slots — never modifying database column names, REST paths, audit resource types, or template variable identifiers.
    - **Workspace Defaults & Server-Rendered Preview:** Property-level default theme/density suggestion and side-by-side non-destructive preview before saving.
  - **Three-Way Identity Boundary:**
    1. **LOVELACE Platform Identity** remains permanent in login chrome, system error screens, attribution (`Powered by LOVELACE Booking System`), and system headers.
    2. **Property Identity** differentiates the active managed property in the workspace header/badge and feeds the public storefront's approved projection.
    3. **User Personalisation** (`PWE-2`) never overwrites property logos, property display names, or property terminology.
  - **Relationship to Public Booking-Page Branding:** Builds cleanly on the existing `properties.branding_config` JSONB column (`0003_phase1_5.sql`, `internal/branding.ParsePropertyBranding`). `PWE-4` may project approved *public-safe* fields (public display name, tagline, validated logo URL, approved public primary/accent colours) to `/api/public/v1/properties/{slug}` for `apps/public-web/` without sharing components, admin tokens, terminology-only internal notes, or deployments (see [ADR-0001](adr/0001-public-admin-separation-and-pwe-boundaries.md)).
- **Dependencies:** `PWE-1` token separation (specifically separating `--property-brand` badge/header accents from user interactive `--accent` and semantic status tokens `--ok-*`/`--err-*`), attachment storage hardening (`TIRBACH_ATTACHMENT_DIR` model).

### PWE-5 — Workspace Profiles (`PROPOSED / FUTURE`)
- **Scope:**
  - Curated role-aligned **Workspace Profiles** that seed recommended default widgets, shortcuts, landing route, and density for operators without requiring manual dashboard assembly on day one:
    1. **Owner / Administrator Profile (`owner_admin` / `manager`):** Financial KPI snapshot (`reports.kpi_snapshot`), operational alerts (`ops.alerts_attention`), channel sync health (`channels.sync_health`), arrivals summary, open maintenance summary, `comfortable` or `compact` default, landing `/`.
    2. **Reception Profile (`staff` / `manager`):** Arrivals & departures (`ops.arrivals_summary`), provisional holds & inquiries (`res.pending_inquiries`), mini availability calendar (`res.mini_calendar`), guest enquiries (`crm.open_enquiries`), `compact` density default, landing `/` or `/calendar`.
    3. **Housekeeping Profile (`housekeeping_staff`):** Daily changeover board (`ops.changeover_board`), arrivals/departures turnarounds (`ops.arrivals_summary`), quick report-maintenance shortcut, large accessible tap/focus spacing (`comfortable` density), landing `/` or changeover board.
    4. **Maintenance Profile (`maintenance_staff`):** Open maintenance queue (`maint.open_queue`), preventative schedules due (`maint.preventative_due`), alerts requiring restriction/asset attention, quick links to `/maintenance/schedule` and `/maintenance/facilities`, landing `/maintenance`.
  - **User Freedom Within Bounds:** Operators assigned or matched to a profile may personalise permitted `PWE-1`/`PWE-2`/`PWE-3` settings and reset back to their profile default at any time.
  - **Mandatory Security Boundary:** Workspace profiles affect **presentation only**. Assigning or previewing a profile MUST NEVER grant RBAC permissions, bypass `HasPermission(...)`, expose cross-property records, or suppress server-side `must_rotate_password` / `403 Forbidden` enforcement.
- **Dependencies:** `PWE-1`, `PWE-2`, `PWE-3`; existing [`internal/auth/rbac.go`](../internal/auth/rbac.go) role-permission matrix.

---

## 4. Configuration Resolution, Tenancy & Persistence Architecture (`PROPOSED / FUTURE`)

### 4.1 Deterministic Four-Layer Resolution Model
When rendering any LOVELACE Admin view for authenticated user `U` operating inside authorized property `P`, presentation settings MUST resolve deterministically top-to-bottom across four layers:

```text
Layer 1: Platform Defaults        (Code/platform-owned, immutable at runtime; guaranteed valid fallback)
   │  overridden by permitted fields in ▼
Layer 2: Property Configuration   (Owner/manager-configured per property_id: branding + property workspace defaults)
   │  seeded for workspace by permitted fields in ▼
Layer 3: Workspace Profile        (Role-aligned presentation preset: layout, shortcuts, default landing & density)
   │  overridden ONLY for user-personalizable allowlisted fields by ▼
Layer 4: User Preferences         (4a: User global defaults ─► 4b: User + Property scoped overrides)
```

#### Layer Ownership & Override Rules

| Setting Category | Layer 1: Platform | Layer 2: Property (`property_id`) | Layer 3: Workspace Profile | Layer 4a: User Global | Layer 4b: User + Property (`user_id, property_id`) |
|---|---|---|---|---|---|
| **LOVELACE Brand & Attribution** | Authoritative | Cannot alter | Cannot alter | Cannot alter | Cannot alter |
| **Property Logo, Display Name, Brand Palette, Terminology** | Platform fallback only | **Authoritative** (`PermPropertyWrite`) | Cannot alter | Cannot alter | Cannot alter |
| **Semantic Status Palette (`ok`/`warn`/`err`) & Focus Rings** | **Authoritative** | Cannot alter | Cannot alter | High-contrast enhancement only | Cannot alter |
| **Accessibility (`high_contrast`, `reduced_motion`, `text_scale`)** | Safe defaults | Optional property default suggestion | Cannot weaken | **User Authoritative** | Optional override |
| **Theme (`system`/`light`/`dark`), Curated Accent, Density** | Safe defaults | Property suggested default | Profile suggested density | **User Override** | Optional per-property override |
| **Dashboard Layout & Typed Widgets** | Platform default layout | Property default layout override (optional) | Profile preset layout | Global fallback prefs | **User + Property Layout** |
| **Saved Filters / Views & Pinned Property Context** | Empty | Optional property shared presets (read-only template) | Profile default shortcuts | Global shortcuts (safe routes) | **User + Property Filters/Shortcuts** |

#### Graceful Fallback & Error Handling Rules
- **Missing Value:** Fall through immediately to the next higher layer (`4b -> 4a -> 3 -> 2 -> 1`).
- **Invalid / Corrupted / Out-of-Schema Value:** Server-side decoder logs a sanitized warning (no PII/secrets), ignores solely the invalid field/widget rather than failing the page request (`200 OK` maintained), and resolves from the fallback layer.
- **Obsolete / Removed Widget Type:** Unknown `widget_id` entries stored in saved `PWE-3` layouts are silently omitted during SSR rendering while preserving the remaining valid widgets (with a gentle notice/repair option).
- **Revoked Permission / Changed Role:** Widgets, shortcuts, or landing pages requiring a permission no longer held by `U` are filtered out server-side before rendering; if `U`'s saved landing route returns unauthorized, landing safely falls back to `/` (or first permitted route).
- **Revoked Property Access / Cross-Property Switching:**
  - If a property-scoped user loses access to `P`, or a global `owner_admin` switches from `P1` to `P2`:
    - Any endpoint targeting `P1` returns `403 Forbidden` under existing `requirePropertyScope`.
    - Active workspace context clears `P1`'s logo, terminology, `4b` overrides, saved filters, and `P1` dashboard layout immediately — **never** carrying `P1`'s brand chrome or data widgets into `P2`.
    - Historical `4b` user-property rows for `P1` remain unreachable unless access is later restored (or cleaned up on user deletion/revocation).

### 4.2 Multi-Property & Multi-Tenancy Isolation Rules
- `admin_users.property_id` may be property-scoped (`UUID`) or platform-wide (`NULL`, e.g., global `owner_admin`). Future multi-property workflows MUST distinguish:
  - **Global user preferences** (travel with `U` across all properties: dark mode, text scale, reduced motion, high contrast).
  - **Property-scoped workspace data** (`last_visited` defaults per property, per-property dashboard layouts, saved calendar filters mentioning `unit_type_id`s or `channel`s, and property branding).
- **Zero Cross-Property Leakage:** Every future PWE read/write API MUST run through existing authentication + `must_rotate_password = FALSE` verification + `HasPermission` + `requirePropertyScope`. Saved filter presets, widget configs referencing UUIDs (such as `unit_type_id`), and property logos MUST validate same-property ownership in service & PostgreSQL constraints before save and on read. Preference APIs must never disclose existence, names, or configs of properties the caller is not authorized to access.

### 4.3 Persistence Design (`PWE-2` & `PWE-3` Implemented; `PWE-4`–`PWE-5` `PROPOSED / FUTURE`)
`PWE-2` (`migrations/0022_pwe2_user_personalisation.sql`) and `PWE-3` (`migrations/0023_pwe3_dashboard_builder.sql`) implement the hybrid relational + schema-versioned JSONB design under `lovelace_migrator` DDL and `lovelace_app` DML-only grants, while keeping `PWE-4` and `PWE-5` persistence deferred:

1. **Property Branding & Workspace Defaults (`PWE-4` — `PROPOSED / FUTURE`):**
   - Evolve/validate `properties.branding_config` (or introduce `property_workspace_settings` `PRIMARY KEY (property_id)` referencing `properties(id)` with `schema_version INTEGER NOT NULL`, `public_branding JSONB`, `admin_branding JSONB`, `workspace_defaults JSONB`, `updated_at`, `updated_by`) so public branding (`public_branding`) and admin-only defaults (`admin_branding`, `workspace_defaults`) stay structurally separated even at rest.
2. **Global User Preferences (`PWE-1`, `PWE-2` — Implemented in `0022`):**
   - `public.admin_user_preferences` (`user_id UUID PRIMARY KEY REFERENCES admin_users(id) ON DELETE CASCADE`, `schema_version INTEGER NOT NULL`, typed columns `theme_mode`, `accent`, `density`, `text_scale`, `high_contrast`, `reduced_motion`, `default_route`, optional re-validated `last_property_id UUID NULL REFERENCES properties(id) ON DELETE SET NULL`, validated `preferences JSONB`, `created_at`, `updated_at`).
3. **User + Property Scoped Workspaces & Dashboard Layouts (`PWE-2` in `0022`, `PWE-3` in `0023`; `PWE-5` `PROPOSED / FUTURE`):**
   - `public.admin_user_property_workspaces` (`PRIMARY KEY (user_id, property_id)` referencing `admin_users(id) ON DELETE CASCADE` and `properties(id) ON DELETE CASCADE`, `schema_version INTEGER NOT NULL`, `preferences JSONB`, `saved_views JSONB`, `dashboard_schema_version SMALLINT NOT NULL DEFAULT 1`, `dashboard_layout JSONB NULL`, `created_at`, `updated_at`); service/repository layer enforces `admin_users.property_id IS NULL OR admin_users.property_id = property_id` and active property resource ownership on every write and read. Future `PWE-5` (`selected_profile_code`) extensions remain deferred (`PROPOSED / FUTURE`).
4. **Workspace Profile Definitions (`PWE-5` — `PROPOSED / FUTURE`):**
   - Code-defined platform presets in Go (`owner_admin`, `reception`, `housekeeping`, `maintenance`) with optional future property override table `property_workspace_profiles` (`(property_id, profile_code)`) validated strictly against the typed widget registry.
5. **Validation, Schema Versioning & Recovery:**
   - Every JSONB payload carries `schema_version` (`int`) and is unmarshaled through `DisallowUnknownFields()` / strict typed validators in Go (bounded array sizes, enum checks, valid UUID formats, zero raw HTML/CSS).
   - **Forward Migration:** Read-time deterministic upcaster converts v1 -> vN safely in memory (with lazy write-back or explicit `cmd/migrate` backfill for breaking shape changes).
   - **Corrupt-State Recovery:** Both API (`POST .../workspace/reset`) and DB-safe SSR loader fall back to Profile -> Property -> Platform defaults if validation fails, ensuring a bad preference payload can never lock an operator out of LOVELACE Admin.
   - **Auditing:** Property branding changes (`PWE-4`) and profile defaults record sanitized structural diffs into `audit_logs` via `audit_sanitizer.go` and an allowlist rule matching `0018`, with zero guest PII or image blobs stored in `audit_logs`.

---

## 5. Per-Milestone Testable Acceptance Checklists (`PWE-1`–`PWE-3` Implemented; `PWE-4`–`PWE-5` `PROPOSED / FUTURE`)

### PWE-1 Acceptance Checklist (**Implemented & Verified**)
- [x] Zero JS frameworks or inline `style="..."` / `<script>` attributes added; strict `style-src 'self'` CSP remains intact across all pages.
- [x] All design tokens (light, dark, system, high contrast, reduced motion, density, typography scale) resolve deterministically via CSS custom properties in static `/static/*.css`.
- [x] Keyboard navigation, `:focus-visible`, WCAG 2.1 AA contrast, screen-reader landmarks/labels, 200% zoom/text-scale legibility, and non-colour status indicators pass automated & visual checks.
- [x] Shared partials cover headers, status badges, and empty states across shared chrome and representative admin screens, removing decorative sidebar/login gradients.
- [x] `apps/public-web/` builds, CSS, and origin boundaries remain completely untouched and independent.

### PWE-2 Acceptance Checklist (**Implemented & Verified**)
- [x] Preferences persist per `admin_user` across sessions without affecting any other user's UI or defaults.
- [x] Global accessibility/appearance choices follow multi-property operators across property switches, while property-scoped filters/shortcuts/views isolate cleanly per `(user_id, property_id)`.
- [x] Invalid/obsolete/missing preference fields degrade cleanly to fallback layers with HTTP 200 SSR rendering and zero panics or lockouts.
- [x] Pinned shortcuts and landing routes filter dynamically by current server-side RBAC permissions (`Perm*`); unauthorized landing route falls back cleanly without bypassing `403` checks.

### PWE-3 Acceptance Checklist (**Implemented & Verified**)
- [x] Typed widget registry validates all widget IDs, sizes, and typed config schemas server-side, rejecting unknown widgets, arbitrary HTML, CSS, or executable payloads (`422`).
- [x] Operators can add, remove, reorder, resize (within widget `SupportedSizes`), save layouts, pin shortcuts, and reset to default deterministically.
- [x] Default dashboard renders usable out-of-the-box without user setup.
- [x] Removing a user's permission hides the corresponding widget automatically on next render, while backend endpoints continue returning `403 Forbidden` regardless of dashboard state.

### PWE-4 Acceptance Checklist
- [ ] Authorized `PermPropertyWrite` operator can configure property logo, approved contrast-verified primary/secondary colours, display name, optional domain terminology, and preview changes before saving.
- [ ] Uploaded logo assets enforce magic-byte verification (PNG/JPEG/WebP only; SVG/scripts/HTML rejected), strict size limits, and safe serving headers (`X-Content-Type-Options: nosniff`).
- [ ] LOVELACE platform branding stays intact on login, system errors, and attribution; personal user themes (`PWE-2`) never overwrite property identity.
- [ ] Cross-property isolation rejects edits or reads outside `property_id`; `/api/public/v1/properties/{slug}` exposes solely approved public branding subset (zero admin preferences/internal layout data).

### PWE-5 Acceptance Checklist
- [ ] Owner/Administrator, Reception, Housekeeping, and Maintenance workspace presets resolve valid default widgets, shortcuts, and density aligned with LOVELACE RBAC roles.
- [ ] Selecting or switching a workspace profile seeds/updates presentation defaults within the user's actual RBAC boundaries without changing `admin_users.role` or granting any backend permission.
- [ ] Users can customize permitted layout/personal preferences on top of their profile and reset back to profile defaults cleanly.
- [ ] Server-side RBAC and property-scope integration tests confirm identical `403` denial behavior regardless of active workspace profile.

---

## 6. Unresolved Product Decisions & Open Owner Questions

The following product questions require explicit owner decision prior to scheduling `PWE-4`–`PWE-5` implementation or changing established reversible `PWE-2` defaults:

1. **Custom Brand Palette Scope (`PWE-4`):** Should property admin brand colours be restricted exclusively to a curated set of pre-tested WCAG-compliant heritage palettes, or allow arbitrary hex input validated dynamically against `>= 4.5:1` contrast thresholds?
2. **Global Multi-Property Operator UX (`PWE-2` — Open Owner Decision):** When a platform-wide `owner_admin` manages multiple properties, should LOVELACE remember their last active `property_id` per session/user preference cleanly, and should global saved filters ever span multiple properties? *(Implemented PWE-2 default stays reversible: stores an optional server-revalidated `last_property_id` hint only without cross-property switcher promotion, and keeps saved operational views strictly property-scoped.)*
3. **Custom Terminology Breadth (`PWE-4`):** Should optional domain terminology (e.g., `"Pitch"` vs `"Pod"` vs `"Cabin"`) apply strictly to admin UI labels, or eventually feed approved guest-facing labels in `apps/public-web/` (while keeping email automation template placeholders immutable)?
4. **Property Logo Storage & Limits (`PWE-4`):** Should property logos reuse the existing `TIRBACH_ATTACHMENT_DIR` storage volume pattern or dedicated object storage once multi-instance scaling is needed?
5. **Preference Erasure & Retention (`PWE-2` — Open Owner Decision):** Should `admin_users` preferences be deleted immediately upon account deactivation, or retained until explicit user record purge? *(Implemented PWE-2 default stays reversible: retains rows on soft deactivation `is_active = FALSE` for admin reactivation and cascades deletion on hard `admin_users` purge via `ON DELETE CASCADE`.)*

---

## Related Documentation
- [Architecture & Delivery Blueprint](architecture.md)
- [ADR-0001: Public/Administrative Separation & PWE Boundaries](adr/0001-public-admin-separation-and-pwe-boundaries.md)
- [Branding & Design System](branding.md)
- [Security Controls & Test Isolation](security.md)
- [Phase 2 Direct Booking Architecture](phase2-architecture.md)
