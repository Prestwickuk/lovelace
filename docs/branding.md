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

Dark mode activates automatically via `@media (prefers-color-scheme: dark)`, shifting page/card surfaces to deep forest charcoal (`#151917` / `#1D2320`) while keeping accessible contrast and zero inline styles under strict CSP `style-src 'self'`.

---

## 4. Proposed Future Extension (`PROPOSED / FUTURE` — Not Implemented)

The planned expansion of LOVELACE's design tokens, user appearance/accessibility preferences (`PWE-1`, `PWE-2`), property-level branding previews/terminology (`PWE-4` building on `properties.branding_config`), and three-way Platform / Property / User identity separation is documented in:

- [Personalisation & Workspace Experience (`PROPOSED / FUTURE`)](personalisation-workspace-experience.md)
- [ADR-0001: Public/Administrative Separation & PWE Boundaries](adr/0001-public-admin-separation-and-pwe-boundaries.md)
