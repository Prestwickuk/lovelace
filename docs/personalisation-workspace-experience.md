# LOVELACE Personalisation & Workspace Experience (`PWE-1`–`PWE-5` Implemented)

> **Document Status:** **All five PWE workstream milestones (`PWE-1` through `PWE-5`) are Implemented & Verified** (`internal/branding/theme.go`, `internal/branding/property_branding.go`, `internal/storage/logo.go`, `internal/domain/personalisation.go`, `internal/domain/dashboard.go`, `internal/domain/property_branding.go`, `internal/domain/workspace_profiles.go`, `migrations/0022_pwe2_user_personalisation.sql`, `migrations/0023_pwe3_dashboard_builder.sql`, `migrations/0024_pwe4_property_branding.sql`, `migrations/0025_pwe5_workspace_profiles.sql`, `internal/store/postgres/`, `internal/service/`, `internal/httpapi/`, `internal/webui`, and `apps/public-web/`).
>
> **Implementation Status:** **PWE-1 through PWE-5 Implemented** (`0022` adds `admin_user_preferences` and `admin_user_property_workspaces`; `0023` adds `dashboard_schema_version` and `dashboard_layout`; `0024` adds `property_branding_settings` and `property_branding` audit allowlist rules; `0025` adds `explicit_density`, `explicit_default_route`, and `selected_profile_code` under `lovelace_app` DML grants while keeping `must_rotate_password`, live DB RBAC, and `PWE-2`/`PWE-4` owner decisions intact).

---

## 1. Roadmap Context & Dependencies

**Workstream Title:** **LOVELACE Personalisation & Workspace Experience (`PWE`)**

**Delivery context:** PWE was delivered as an optional, modular workstream after the Phase 3 guest-management and operations foundations. It runs independently of **Phases 4–5** and **Phase 3 Milestone 4**; it does not renumber the committed milestones (`Phases 1–5` and `Phase 3 M1–M4`).

### Rationale & Non-Blocking Positioning
- **Not a launch prerequisite:** Neither Phase 3.2 acceptance, direct public bookings (`Phase 2`), live OTA channel sync (`Phase 4`), nor historical BI reporting (`Phase 5`) depends on `PWE`. Existing `internal/branding`, `internal/webui`, and `properties.branding_config` continue operating safely without `PWE`.
- **Depends on mature Phase 3 operational roles & data:** Role-oriented workspace presets (`PWE-5`) and operational dashboard widgets (`PWE-3`) are only meaningful once real housekeeping, maintenance, reservations, payment exceptions, guest CRM, and reporting workflows exist in production (`owner_admin`, `manager`, `staff`, `maintenance_staff`, `housekeeping_staff`, `viewer`).
- **Foundational prerequisites already in place:**
  - Server-rendered Go console (`internal/webui`) with centralized brand/token definitions ([`internal/branding/branding.go`](../internal/branding/branding.go), [`internal/webui/static/app.css`](../internal/webui/static/app.css)).
  - Strict Content Security Policy (`style-src 'self'; script-src 'self'; object-src 'none'`; zero inline styles or scripts).
  - Fine-grained permission model ([`internal/auth/rbac.go`](../internal/auth/rbac.go)) and composite property-scoped data boundaries.
  - Per-property JSONB hook `properties.branding_config` (`migrations/0003_phase1_5.sql`) consumed today solely by public branding foundations.

---

## 2. Design System Architecture & Administrative Design Standard

This section records the design governance implemented for **PWE-1** and used by administrative console views (`internal/webui`).

### 2.1 Technical Architecture (Go SSR + Static CSS Tokens)
- **Server-Rendered Go Templates Only:** Must operate strictly inside LOVELACE's existing Go `html/template` + static asset server architecture (`internal/webui/templates/`, `internal/webui/static/`). **No** JavaScript SPA framework (React/Vue/etc.), CSS-in-JS runtime, Tailwind build chain, or client runtime dependency is introduced into the admin console.
- **Strict CSP Compliance (`style-src 'self'`):** Themes, accents, density modes, text scale, and high-contrast variants MUST be applied cleanly via server-emitted `<html>`/`<body>` attributes or classes (e.g., `data-theme="dark"`, `data-accent="teal"`, `data-density="compact"`, `data-contrast="high"`) mapped to CSS custom properties inside static stylesheet files (`/static/*.css`). Inline `style="..."` attributes and `<style>` blocks remain prohibited.
- **Public Storefront Independence:** `apps/public-web/` (Next.js / React / TypeScript) retains an independent design system, styling pipeline, and deployment boundary per [ADR-0001](adr/0001-public-admin-separation-and-pwe-boundaries.md).

### 2.2 Product Aesthetic & Design Direction
LOVELACE Admin is **refined, restrained, information-dense hospitality operations software** built for daily reception, reservations, housekeeping, maintenance, and finance work — never a consumer marketing page or generic template dashboard.

| Element | Administrative design standard |
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
To prevent visual drift and usability degradation, current and future `PWE` changes must reject:
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

## 3. Workstream Milestones (`PWE-1`–`PWE-5` Implemented)

### PWE-1 — Design System and Theme Engine (**Implemented**)
- **Implemented Files & Contract:**
  - [`internal/branding/theme.go`](../internal/branding/theme.go) (`ThemeSelection`, `ResolveTheme`, `CuratedAccents`, `ContrastRatio`) strictly allowlists `data-theme` (`system` default via `prefers-color-scheme`, `light`, `dark`), `data-accent` (`teal` default, `olive`, `slate`, `brass`), `data-contrast` (`standard`, `high`), `data-motion` (`system` default via `prefers-reduced-motion`, `reduced`), `data-density` (`comfortable`, `compact`), and `data-text-scale` (`default`, `large`, `xl`).
  - [`internal/webui/static/app.css`](../internal/webui/static/app.css) defines semantic surface, ink, muted ink, border, solid sidebar chrome (zero decorative gradients), separate `--property-brand` vs `--accent-*` tokens, immutable semantic status tokens (`--ok-*`, `--warn-*`, `--alloc-*`, `--err-*`, `--info-*`, `--unrel-*`), `:focus-visible`, `.skip-link`, `.sr-only`, `rem`-scaled typography, native `<dialog>` styles, and responsive narrow-width layouts under strict `style-src 'self'` CSP.
  - [`internal/webui/templates/partials.html`](../internal/webui/templates/partials.html), [`layout.html`](../internal/webui/templates/layout.html), [`login.html`](../internal/webui/templates/login.html), [`error.html`](../internal/webui/templates/error.html), and representative screens (`dashboard.html`, `calendar.html`, `reservations.html`, `guests_list.html`, `account.html`, `comms_settings.html`) adopt shared partials (`html_attrs`, `head_assets`, `page_header`, `status_badge`, `empty_state`), semantic landmarks/table `scope`, and non-colour status glyphs.
  - Kept Next.js `apps/public-web/` styling, components, and origin completely separate; no preference persistence or DB migrations added.

### PWE-2 — User Personalisation (**Implemented**)
- **Implemented Files & Scope:**
  - **Migration (`0022_pwe2_user_personalisation.sql`):** Adds `public.admin_user_preferences` (`user_id PK ON DELETE CASCADE`, typed PWE-1 theme/accessibility columns, `default_route`, `last_property_id ON DELETE SET NULL`, `preferences JSONB`, `schema_version >= 1`) and `public.admin_user_property_workspaces` (`PK (user_id, property_id) ON DELETE CASCADE`, `preferences JSONB`, `saved_views JSONB`, `schema_version >= 1`), with DML grants for `lovelace_app` and zero PII/secrets stored or audited.
  - **Domain & Validation ([`internal/domain/personalisation.go`](../internal/domain/personalisation.go)):** Strict allowlisted global admin routes (`AdminRoutes`), navigation sections (`NavSections`), validated theme/accessibility/layout fields, property-scoped `SavedOperationalView` presets (`calendar`, `reservations`, `guests` referencing only verified property `unit_type_id` / `channel_code` resources), and field-by-field `DecodeAndNormalize*` upcasting/recovery so malformed JSONB degrades safely without blocking SSR. Workspace profile defaults and explicit overrides are resolved by the PWE-5 profile service.
  - **Persistence & Service ([`internal/store/postgres/personalisation_repo.go`](../internal/store/postgres/personalisation_repo.go), [`internal/service/personalisation_service.go`](../internal/service/personalisation_service.go)):** Self-ownership (`ErrForbidden` cross-user), live DB `GetAdminUserByID` role/property re-verification, `MustRotatePassword` gate blocking preference mutations and omitting personalisation overrides during mandatory rotation, cross-property `unit_type_id`/`channel_code` rejection, and dynamic filtering of landing routes, pinned shortcuts, and saved views against current server-side permissions.
  - **Admin Console SSR (`internal/webui/handlers_personalisation.go`, `templates/account.html`, `layout.html`, `calendar.html`, `reservations.html`, `guests_list.html`):** Separate Personalisation settings and Reset controls on `/account` (distinct from Change Password), CSRF-protected `/account/preferences/*` POST handlers, post-login allowed `DefaultRoute` redirect (`handleLoginPost`), sidebar collapsed-section persistence & pinned shortcuts, and per-property calendar/reservations/guests defaults and saved views (`?saved_view=`).
- **Dependencies:** `PWE-1` CSS tokens/attributes; authenticated `admin_users` identity (`internal/auth`).

### PWE-3 — Customisable Dashboard Builder (**Implemented**)
- **Implemented Files & Scope:**
  - **Dedicated Route & Preserved Operations Centre (`/`):** Adds `/workspace` (`GET /workspace`, `/workspace?edit=1`, `POST /workspace/widgets/*`, `POST /workspace/reset`) and `/api/v1/properties/{propertyID}/workspace/dashboard` (`GET`, `PUT`, `POST /reset`), plus `/workspace` in the PWE-2 allowlisted route catalogue while preserving `/` Operations Centre intact.
  - **Additive Persistence (`0023_pwe3_dashboard_builder.sql`):** Adds `dashboard_schema_version` and nullable `dashboard_layout JSONB` onto `public.admin_user_property_workspaces` without altering PWE-2 `preferences`, `saved_views`, or retention rules. A `NULL` layout resolves through the current workspace profile defaults, then the platform default; PWE-4 branding and PWE-5 profile support were added in migrations `0024` and `0025`.
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

### PWE-4 — Property Branding (**Implemented**)
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
  - **Relationship to Public Booking-Page Branding:** Builds on the existing `properties.branding_config` JSONB column (`0003_phase1_5.sql`, `internal/branding.ParsePropertyBranding`). PWE-4 projects approved *public-safe* fields (public display name, tagline, region label, validated logo, and approved public colours) to `/api/public/v1/properties/{slug}` for `apps/public-web/` without sharing components, admin tokens, or terminology-only internal notes (see [ADR-0001](adr/0001-public-admin-separation-and-pwe-boundaries.md)).
- **Dependencies:** `PWE-1` token separation (specifically separating `--property-brand` badge/header accents from user interactive `--accent` and semantic status tokens `--ok-*`/`--err-*`), attachment storage hardening (`TIRBACH_ATTACHMENT_DIR` model).

### PWE-5 — Workspace Profiles (**Implemented in `0025_pwe5_workspace_profiles.sql`, `internal/domain/workspace_profiles.go`, `internal/service/workspace_profile_service.go`, `/workspace/profile`, `/api/v1/properties/{propertyID}/workspace/profile`**)
- **Scope:**
  - Code-defined role-aligned **Workspace Profiles** that seed default widgets, shortcuts, landing route, and density without requiring manual dashboard assembly on day one:
    1. **Owner / Administrator Profile (`owner_admin` — default for `owner_admin` and `manager`):** 30-day Financial KPI snapshot (`reports.kpi_snapshot`), operational alerts (`ops.alerts_attention`), channel sync health (`channels.sync_health`), arrivals summary (`ops.arrivals_summary`), open maintenance summary (`maint.open_queue`), personal quick links (`workspace.quick_shortcuts`), `comfortable` density, landing `/`.
    2. **Reception Profile (`reception` — default for `staff`; selectable switch for `manager`):** Arrivals & departures (`ops.arrivals_summary`), provisional holds & inquiries (`res.pending_inquiries`), 7-day mini availability calendar (`res.mini_calendar`), guest enquiries (`crm.open_enquiries`), personal quick links (`workspace.quick_shortcuts`), `compact` density, landing `/calendar` (or `/`).
    3. **Housekeeping Profile (`housekeeping` — default for `housekeeping_staff`):** Daily changeover board turnaround flags (`ops.changeover_board` without guest PII when `reservations:read` is absent), housekeeping & maintenance alerts (`ops.alerts_attention`), personal quick links (`workspace.quick_shortcuts`), `comfortable` density, landing `/`.
    4. **Maintenance Profile (`maintenance` — default for `maintenance_staff`):** Open maintenance queue (`maint.open_queue`), preventative schedules due (`maint.preventative_due`), maintenance alerts (`ops.alerts_attention`), personal quick links to `/maintenance`, `/maintenance/schedule`, and `/maintenance/facilities`, `comfortable` density, landing `/maintenance`.
    5. **`viewer` & unmapped roles:** No workspace profile assigned; resolves cleanly to LOVELACE safe platform defaults filtered by live `internal/auth` permissions.
  - **User Freedom & Non-Destructive Resets:** `PWE-1`/`PWE-2` density and landing route default to profile inheritance (`Inherit from Workspace Profile`) unless explicitly overridden (`explicit_density`, `explicit_default_route`). Selecting a profile updates only `selected_profile_code`; partial PWE-2, PWE-3, and PWE-5 resets preserve unaffected layers (`POST /workspace/profile/reset` clears profile selection and custom dashboard layout back to role defaults, preserving global theme/accessibility and `saved_views`, with optional `clear_named_overrides`).
  - **Mandatory Security Boundary:** Workspace profiles affect **presentation only**. Live DB `admin_users.role` & property access are revalidated on every render and request; stale or downgraded stored profile codes discard cleanly with diagnostic warnings, cross-role escalation (`403`) and `must_rotate_password` enforcement stay unchanged, and backend handlers continue enforcing `internal/auth` and `requirePropertyScope`.

---

## 4. Configuration Resolution, Tenancy & Persistence Architecture (**Implemented**)

### 4.1 Presentation and Branding Scope
The implementation keeps property branding, role profiles, and user preferences in separate scopes. Where a setting supports inheritance, the applicable defaults resolve in this order for authenticated user `U` and authorized property `P`:

```text
Platform defaults
   └─ role-aligned workspace profile (when the role has one)
       └─ user global preferences
           └─ user + active-property preferences
```

Property branding is resolved separately from preference defaults: saved property identity, public-facing branding fields, logo, palette, and bounded admin terminology apply to that property. The current property branding controls do not define property-level theme, density, dashboard, or shared saved-view defaults. The order above applies only to settings that support those preference/profile layers; not every setting has a value at every layer.

#### Layer Ownership & Override Rules

| Setting Category | Layer 1: Platform | Layer 2: Property (`property_id`) | Layer 3: Workspace Profile | Layer 4a: User Global | Layer 4b: User + Property (`user_id, property_id`) |
|---|---|---|---|---|---|
| **LOVELACE Brand & Attribution** | Authoritative | Cannot alter | Cannot alter | Cannot alter | Cannot alter |
| **Property Logo, Display Name, Brand Palette, Terminology** | Platform fallback only | **Authoritative** (`PermPropertyWrite`) | Cannot alter | Cannot alter | Cannot alter |
| **Semantic Status Palette (`ok`/`warn`/`err`) & Focus Rings** | **Authoritative** | Cannot alter | Cannot alter | High-contrast enhancement only | Cannot alter |
| **Accessibility (`high_contrast`, `reduced_motion`, `text_scale`)** | Safe defaults | Not configured here | Cannot weaken | User preference | No separate property override for these fields |
| **Theme (`system`/`light`/`dark`) & Curated Accent** | Safe defaults | Not configured here | Cannot override user choice | User preference | No separate property override for these fields |
| **Density & Default Landing Route** | Safe defaults | Not configured here | Role profile default | User override when explicit | Per-property override when configured |
| **Dashboard Layout & Typed Widgets** | Platform default layout | Not configured here | Role profile preset | — | User + active-property layout |
| **Saved Operational Views & Shortcuts** | Empty/safe routes | Not configured here | Profile shortcuts | Global shortcuts (safe routes) | User + active-property saved views and shortcut overrides |

#### Graceful Fallback & Error Handling Rules
- **Missing Value:** For fields with inheritance, fall through to the next applicable layer (active-property override -> explicit global preference -> workspace profile -> platform default). Property branding uses its own property-scoped values and platform fallback.
- **Invalid / Corrupted / Out-of-Schema Value:** Server-side decoder logs a sanitized warning (no PII/secrets), ignores solely the invalid field/widget rather than failing the page request (`200 OK` maintained), and resolves from the fallback layer.
- **Obsolete / Removed Widget Type:** Unknown `widget_id` entries stored in saved `PWE-3` layouts are silently omitted during SSR rendering while preserving the remaining valid widgets (with a gentle notice/repair option).
- **Revoked Permission / Changed Role:** Widgets, shortcuts, or landing pages requiring a permission no longer held by `U` are filtered out server-side before rendering; if `U`'s saved landing route returns unauthorized, landing safely falls back to `/` (or first permitted route).
- **Revoked Property Access / Cross-Property Switching:**
  - If a property-scoped user loses access to `P`, or a global `owner_admin` switches from `P1` to `P2`:
    - Any endpoint targeting `P1` returns `403 Forbidden` under existing `requirePropertyScope`.
    - Active workspace context clears `P1`'s logo, terminology, `4b` overrides, saved filters, and `P1` dashboard layout immediately — **never** carrying `P1`'s brand chrome or data widgets into `P2`.
    - Historical `4b` user-property rows for `P1` remain unreachable unless access is later restored (or cleaned up on user deletion/revocation).

### 4.2 Multi-Property & Multi-Tenancy Isolation Rules
- `admin_users.property_id` may be property-scoped (`UUID`) or platform-wide (`NULL`, e.g., global `owner_admin`). Current multi-property workflows distinguish:
  - **Global user preferences** (travel with `U` across authorized properties: theme, accent, text scale, reduced motion, high contrast, and navigation defaults).
  - **Property-scoped workspace data** (`last_property_id` as a revalidated hint, per-property dashboard layouts and settings, saved operational filters, and property branding).
- **Property Isolation:** PWE read/write routes run through authentication, password-rotation gating where required, permission checks, and property scope. Saved filter presets, widget configs referencing UUIDs (such as `unit_type_id`), and property branding are validated against the selected property. Preference routes do not disclose properties the caller cannot access.

### 4.3 Persistence Design (`PWE-2`–`PWE-5` Implemented)
`PWE-2` (`migrations/0022_pwe2_user_personalisation.sql`), `PWE-3` (`migrations/0023_pwe3_dashboard_builder.sql`), `PWE-4` (`migrations/0024_pwe4_property_branding.sql`), and `PWE-5` (`migrations/0025_pwe5_workspace_profiles.sql`) implement the hybrid relational + schema-versioned JSONB design under `lovelace_migrator` DDL and `lovelace_app` DML-only grants:

1. **Property Branding (`PWE-4` — Implemented in `0024`):**
   - `public.property_branding_settings` (`property_id UUID PRIMARY KEY REFERENCES properties(id) ON DELETE CASCADE`, `schema_version`, structurally separated `public_branding JSONB` and `admin_branding JSONB`, validated `BlobStore` logo metadata `logo_storage_key`/`logo_content_type`/`logo_size_bytes`/`logo_sha256`/`logo_width`/`logo_height`/`logo_version`, optimistic-lock `version`, `updated_by`, `updated_at`) plus public-safe projection into `PublicPropertyBranding` (and `properties.branding_config`), keeping `admin_branding`, bounded admin terminology, and storage keys strictly off `/api/public/v1/*`.
2. **Global User Preferences (`PWE-1`, `PWE-2` in `0022`; `PWE-5` inherit flags in `0025`):**
   - `public.admin_user_preferences` (`user_id UUID PRIMARY KEY REFERENCES admin_users(id) ON DELETE CASCADE`, `schema_version INTEGER NOT NULL`, typed columns `theme_mode`, `accent`, `density`, `text_scale`, `high_contrast`, `reduced_motion`, `default_route`, `explicit_density BOOLEAN NOT NULL DEFAULT FALSE`, `explicit_default_route BOOLEAN NOT NULL DEFAULT FALSE`, optional re-validated `last_property_id UUID NULL REFERENCES properties(id) ON DELETE SET NULL`, validated `preferences JSONB`, `created_at`, `updated_at`).
3. **User + Property Scoped Workspaces, Dashboard Layouts & Selected Profile (`PWE-2` in `0022`, `PWE-3` in `0023`, `PWE-5` in `0025`):**
   - `public.admin_user_property_workspaces` (`PRIMARY KEY (user_id, property_id)` referencing `admin_users(id) ON DELETE CASCADE` and `properties(id) ON DELETE CASCADE`, `schema_version INTEGER NOT NULL`, `preferences JSONB`, `saved_views JSONB`, `dashboard_schema_version SMALLINT NOT NULL DEFAULT 1`, `dashboard_layout JSONB NULL`, `selected_profile_code TEXT NULL CHECK (...)`, `created_at`, `updated_at`); service/repository layer enforces `admin_users.property_id IS NULL OR admin_users.property_id = property_id` and active property resource ownership on every write and read.
4. **Workspace Profile Definitions (`PWE-5` — Implemented in `internal/domain/workspace_profiles.go`):**
   - Code-defined platform presets in Go (`owner_admin`, `reception`, `housekeeping`, `maintenance`) reusing PWE-3 widget schemas and `EligibleDashboardWidgets` permission gates; no DB-stored arbitrary profile layouts.
5. **Validation, Schema Versioning & Recovery:**
   - Every JSONB payload carries `schema_version` (`int`) and is unmarshaled through `DisallowUnknownFields()` / strict typed validators in Go (bounded array sizes, enum checks, valid UUID formats, zero raw HTML/CSS).
   - **Forward Migration:** Read-time deterministic upcaster converts v1 -> vN safely in memory (with lazy write-back or explicit `cmd/migrate` backfill for breaking shape changes).
   - **Corrupt-State Recovery:** Workspace API reset and SSR loading fall back through applicable profile and platform preference defaults if validation fails, ensuring a bad preference payload cannot lock an operator out of LOVELACE Admin. Property branding uses its own validated property values and platform fallback.
   - **Auditing:** Property branding changes (`PWE-4`) and profile defaults record sanitized structural diffs into `audit_logs` via `audit_sanitizer.go` and an allowlist rule matching `0018`, with zero guest PII or image blobs stored in `audit_logs`.

---

## 5. Per-Milestone Testable Acceptance Checklists (`PWE-1`–`PWE-5` Implemented)

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

### PWE-4 Acceptance Checklist (**Implemented & Verified**)
- [x] Authorized `PermPropertyWrite` operator can configure property logo, approved contrast-verified `#RRGGBB` primary/secondary colours, display name, bounded admin-only domain terminology, and preview changes non-destructively before saving.
- [x] Uploaded logo assets enforce magic-byte verification (PNG/JPEG/WebP only; SVG/scripts/HTML/dimension bombs rejected), EXIF/metadata stripping, `BlobStore` storage outside web roots, strict byte limits, and safe serving headers (`X-Content-Type-Options: nosniff`).
- [x] LOVELACE platform branding stays intact on login, system errors, and attribution; personal user themes (`PWE-2`) never overwrite property identity.
- [x] Cross-property isolation rejects edits or reads outside `property_id`; `/api/public/v1/properties/{slug}` exposes solely approved `PublicPropertyBranding` projection and `/api/public/v1/properties/{slug}/logo` streams sanitized logo bytes cleanly via Next.js BFF without exposing admin terminology or internal paths.

### PWE-5 Acceptance Checklist (**Implemented & Verified**)
- [x] Owner/Administrator, Reception, Housekeeping, and Maintenance workspace presets resolve valid default widgets, shortcuts, and density aligned with LOVELACE RBAC roles.
- [x] Selecting or switching a workspace profile seeds/updates presentation defaults within the user's actual RBAC boundaries without changing `admin_users.role` or granting any backend permission.
- [x] Users can customize permitted layout/personal preferences on top of their profile and reset back to profile defaults cleanly.
- [x] Server-side RBAC and property-scope integration tests confirm identical `403` denial behavior regardless of active workspace profile.

---

## 6. Owner-Confirmed Product Decisions

The owner has confirmed the following current PWE-2 and PWE-4 behaviors. Treat them as product decisions; changing them requires a new owner decision.

1. **Custom Brand Palette Scope (`PWE-4`):** Property branding accepts normalized six-digit `#RRGGBB` hex inputs validated server-side against every text, control, hover, light/dark, and high-contrast foreground/background pairing (`>= 4.5:1` normal text, `>= 3.0:1` UI boundaries, `>= 7.0:1` high contrast), deriving legible on-brand ink and applying tokens strictly through bounded `--property-brand-*` CSS custom properties over same-origin `/branding/property.css`.
2. **Global Multi-Property Operator UX (`PWE-2`):** Keep `last_property_id` as an optional server-revalidated hint only; it never grants access or promotes a property switcher. Keep all saved operational views scoped to one `(user_id, property_id)`; do not support cross-property saved views.
3. **Custom Terminology Breadth (`PWE-4`):** Bounded terminology overrides apply strictly to administrative UI labels inside Go SSR (`internal/webui`), keeping public guest wording (`apps/public-web/`), URLs/routes, API field names, email template variables, audit resource types, and database schemas immutable.
4. **Property Logo Storage & Limits (`PWE-4`):** Property logos persist via the existing `TIRBACH_ATTACHMENT_DIR` / `storage.BlobStore` topology outside web roots (`2 MiB` max, PNG/JPEG/WebP only, `16..2048px` bounds, metadata/EXIF stripped) and are served through dedicated safe handlers (`/property/logo`, `/api/public/v1/properties/{slug}/logo`, and Next.js BFF `/api/public/property/logo`) with fixed `Content-Type` and `X-Content-Type-Options: nosniff`.
5. **Preference Erasure & Retention (`PWE-2`):** Retain user preferences while an admin account is soft-deactivated (`is_active = FALSE`) so settings are available if the account is reactivated. Delete them through the existing `ON DELETE CASCADE` behavior on hard deletion of the admin user. Do not change this retention policy as part of PWE-5.

---

## Related Documentation
- [Architecture & Delivery Blueprint](architecture.md)
- [ADR-0001: Public/Administrative Separation & PWE Boundaries](adr/0001-public-admin-separation-and-pwe-boundaries.md)
- [Branding & Design System](branding.md)
- [Security Controls & Test Isolation](security.md)
- [Phase 2 Direct Booking Architecture](phase2-architecture.md)
