# LOVELACE Booking System — Branding & Design System

**Platform Product:** `LOVELACE Booking System` (`LOVELACE` wordmark)  
**Default Sample Property (Local Dev Seed):** `Meadowbrook Estate` (`properties.slug = 'demo-property'`)

## 1. Platform vs. Property Separation

- **Authoritative Platform Brand:** Configured in [`internal/branding/branding.go`](../internal/branding/branding.go) (`branding.Default()`):
  - `PlatformName`: `LOVELACE`
  - `Descriptor`: `Booking System`
  - `FullName`: `LOVELACE Booking System`
  - `Attribution`: `Powered by LOVELACE Booking System`
  - `LogoAlt`: `LOVELACE Booking System logo`
- **Per-Property Branding (`PWE-4` Implemented):** Persisted cleanly in `public.property_branding_settings` (`migrations/0024_pwe4_property_branding.sql`, projecting public-safe summaries back to `properties.branding_config` via `domain.PublicPropertyBranding`). The **separate Public storefront** renders allowlisted public branding (`display_name`, `tagline`, `region_label`, validated primary/secondary hex tokens, and BFF-proxied `/api/public/v1/properties/{slug}/logo` assets), while the **Administrative Console** (`internal/webui`) keeps permanent **LOVELACE Booking System** identity across login (`login.html`), error pages (`error.html`), and platform chrome alongside bounded per-property header/badge accents (`--property-brand-*` via same-origin `/branding/property.css`), optional property logo thumbnail, and admin-only terminology labels.

## 2. Authoritative Brand Assets

The original unmodified `2048 × 2048` master artwork is stored verbatim under [`assets/brand/lovelace-logo-original-2048x2048.jpg`](../assets/brand/lovelace-logo-original-2048x2048.jpg) alongside master backups in [`assets/brand/`](../assets/brand/). Runtime web derivatives are embedded in [`internal/webui/static/brand/`](../internal/webui/static/brand/) and served under `/static/brand/`:

| Asset | Purpose |
|---|---|
| `/static/brand/lovelace-logo-full.png` | Login screen (`login.html`) |
| `/static/brand/lovelace-logo-compact.png` | Sidebar administration header (`layout.html`) |
| `/static/brand/lovelace-logo-emblem.png` | Branded HTTP error views (`error.html`) & print/emblem attribution |
| `/static/brand/lovelace-favicon.png` | Browser `<link rel="icon">` across all templates |

Light parchment badge surfaces (`.brand-badge`, `.login-logo-wrap`) frame the logo so dark typography inside the emblem stays legible in both light and dark modes.

## 3. Shared Colour Palette & Dark Mode

Defined in [`internal/branding/branding.go`](../internal/branding/branding.go) and [`internal/webui/static/app.css`](../internal/webui/static/app.css):

| Token | Hex | Role |
|---|---|---|
| `--lovelace-teal` | `#2F4F4A` | Primary slate-teal (`PrimaryHex`), sidebar & primary buttons |
| `--lovelace-deep-teal` | `#213B37` | Deep forest teal gradient / active hover |
| `--lovelace-gold` | `#C49A45` | Heritage brass/gold accent & active nav indicator |
| `--lovelace-parchment` | `#F6F1E5` | Warm parchment light canvas |
| `--lovelace-sand` | `#EDE6D6` | Muted card / table header surface |
| `--lovelace-olive` | `#6B7B4E` | Olive-moss secondary tone |
| `--lovelace-charcoal` | `#1E211E` | Dark charcoal ink |

Dark mode (`[data-theme="dark"]` and OS-following `[data-theme="system"]` via `@media (prefers-color-scheme: dark)`), explicit `[data-theme="light"]`, curated WCAG AA accents (`data-accent="teal|olive|slate|brass"`), `[data-contrast="high"]`, `[data-density="compact"]`, `[data-text-scale="large|xl"]`, and reduced-motion rules (`[data-motion="reduced"]` + `@media (prefers-reduced-motion: reduce)`) are implemented in [`internal/branding/theme.go`](../internal/branding/theme.go) and [`internal/webui/static/app.css`](../internal/webui/static/app.css) under strict CSP (`style-src 'self'`, zero inline styles).

---

## 4. PWE Status (`PWE-1`–`PWE-5` Implemented)

`PWE-1` (Admin Design System & Theme Engine), `PWE-2` (per-user theme, accessibility, navigation, and property-scoped operational preferences in `0022_pwe2_user_personalisation.sql`), `PWE-3` (customisable permission-filtered `/workspace` dashboard builder in `0023_pwe3_dashboard_builder.sql`), `PWE-4` (Property Branding with server-validated custom `#RRGGBB` palettes, PNG/JPEG/WebP sanitized `BlobStore` logo management, non-destructive preview, admin-only terminology overrides, and strict public projection in `0024_pwe4_property_branding.sql`), and `PWE-5` (role-aligned Owner/Administrator, Reception, Housekeeping, and Maintenance workspace profiles in `0025_pwe5_workspace_profiles.sql`) are **Implemented**. See:

- [Personalisation & Workspace Experience](personalisation-workspace-experience.md)
- [ADR-0001: Public/Administrative Separation & PWE Boundaries](adr/0001-public-admin-separation-and-pwe-boundaries.md)
