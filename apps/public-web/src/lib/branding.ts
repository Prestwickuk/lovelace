import type { CSSProperties } from "react";
import type { PublicPropertyBranding } from "@/lib/types";

const HEX_COLOR_RE = /^#[0-9A-Fa-f]{6}$/;
const SAFE_SLUG_RE = /^[a-z0-9][a-z0-9-]{0,63}$/;

/** Validates and normalizes strict 6-digit #RRGGBB hex values; drops any invalid input. */
export function sanitizeHexColor(raw?: string): string | undefined {
  if (!raw) return undefined;
  const trimmed = raw.trim();
  if (!HEX_COLOR_RE.test(trimmed)) return undefined;
  return trimmed.toUpperCase();
}

export function sanitizePropertySlug(raw?: string, fallback = "demo-property"): string {
  const cleaned = (raw || "").trim().toLowerCase();
  if (SAFE_SLUG_RE.test(cleaned)) {
    return cleaned;
  }
  return fallback;
}

/**
 * Maps public property branding metadata to our same-origin Next.js BFF logo route
 * so LOVELACE_PUBLIC_API_SECRET and internal Go service origins stay strictly server-only.
 */
export function getPublicLogoSrc(
  branding?: PublicPropertyBranding,
  propertySlug?: string,
): string | null {
  if (!branding?.has_logo) return null;
  const slug = sanitizePropertySlug(propertySlug);
  const version =
    typeof branding.logo_version === "number" && branding.logo_version > 0
      ? Math.floor(branding.logo_version)
      : 1;
  return `/api/public/property/logo?property=${encodeURIComponent(slug)}&v=${version}`;
}

/**
 * Builds bounded, hex-validated CSS custom properties for public property theming.
 * Never injects raw CSS rules or arbitrary strings.
 */
export function buildPropertyBrandCssVars(
  branding?: PublicPropertyBranding,
): CSSProperties | undefined {
  if (!branding?.has_custom_palette) return undefined;
  const primary = sanitizeHexColor(branding.primary_color);
  const primaryOn = sanitizeHexColor(branding.primary_on_color);
  const primaryHover = sanitizeHexColor(branding.primary_hover_color);
  const secondary = sanitizeHexColor(branding.secondary_color);
  const secondaryOn = sanitizeHexColor(branding.secondary_on_color);

  const vars: Record<string, string> = {};
  if (primary) vars["--property-brand"] = primary;
  if (primaryOn) vars["--property-brand-on"] = primaryOn;
  if (primaryHover) vars["--property-brand-hover"] = primaryHover;
  if (secondary) vars["--property-brand-secondary"] = secondary;
  if (secondaryOn) vars["--property-brand-secondary-on"] = secondaryOn;

  return Object.keys(vars).length > 0 ? (vars as CSSProperties) : undefined;
}
