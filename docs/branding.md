# LOVELACE Booking System — Branding & Design System

**Platform Product:** `LOVELACE Booking System` (`LOVELACE` wordmark)  
**Default Managed Property:** `Tir Bach Campsite` (`properties.slug = 'tir-bach'`)

## 1. Platform vs. Property Separation

- **Authoritative Platform Brand:** Configured in [`internal/branding/branding.go`](../internal/branding/branding.go) (`branding.Default()`):
  - `PlatformName`: `LOVELACE`
  - `Descriptor`: `Booking System`
  - `FullName`: `LOVELACE Booking System`
  - `Attribution`: `Powered by LOVELACE Booking System`
  - `LogoAlt`: `LOVELACE Booking System logo`
- **Per-Property Public Branding:** Stored cleanly per property in `properties.branding_config` (`JSONB`, added in `migrations/0003_phase1_5.sql`), parsed via `branding.ParsePropertyBranding`. In Phases 2–5, the **separate Public frontend** renders property-specific branding (logos, themes, custom domains, multi-property routing), while the **separate Administrative frontend** (`internal/webui`) remains consistently **LOVELACE Booking System** branded regardless of which property or custom domain is being managed.

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

## 4. PWE Status & Future Extensions (`PWE-1` & `PWE-2` Implemented; `PWE-3`–`PWE-5` `PROPOSED / FUTURE`)

`PWE-1` (Admin Design System & Theme Engine) and `PWE-2` (per-user theme, accessibility, navigation, and property-scoped operational preferences in `0022_pwe2_user_personalisation.sql`) are **Implemented**. The planned expansion to customizable dashboards (`PWE-3`), property-level branding previews/terminology (`PWE-4` building on `properties.branding_config`), workspace presets (`PWE-5`), and three-way Platform / Property / User identity separation remains **`PROPOSED / FUTURE`** and is documented in:

- [Personalisation & Workspace Experience (`PROPOSED / FUTURE`)](personalisation-workspace-experience.md)
- [ADR-0001: Public/Administrative Separation & PWE Boundaries](adr/0001-public-admin-separation-and-pwe-boundaries.md)
