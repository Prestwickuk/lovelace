import { describe, expect, it } from "vitest";
import {
  buildPropertyBrandCssVars,
  getPublicLogoSrc,
  sanitizeHexColor,
} from "./branding";
import type { PublicPropertyBranding } from "./types";

describe("Public branding sanitization helpers", () => {
  it("normalizes strict #RRGGBB hex values and drops unsafe values", () => {
    expect(sanitizeHexColor("#1e4d3b")).toBe("#1E4D3B");
    expect(sanitizeHexColor(" #6B4E14 ")).toBe("#6B4E14");
    expect(sanitizeHexColor("#fff")).toBeUndefined();
    expect(sanitizeHexColor("#1E4D3B; background:url(evil)")).toBeUndefined();
    expect(sanitizeHexColor("red")).toBeUndefined();
    expect(sanitizeHexColor("expression(alert(1))")).toBeUndefined();
  });

  it("maps public logo URLs strictly to same-origin BFF route", () => {
    const branding: PublicPropertyBranding = {
      schema_version: 1,
      display_name: "Tir Bach Campsite",
      tagline: "Coastal glamping",
      primary_color: "#2F4F4A",
      primary_on_color: "#FFFFFF",
      primary_hover_color: "#213B37",
      secondary_color: "#6B4E14",
      secondary_on_color: "#FFFFFF",
      has_custom_palette: true,
      has_logo: true,
      logo_url: "/api/public/v1/properties/tir-bach/logo?v=4",
      logo_alt: "Tir Bach emblem",
      logo_version: 4,
    };
    expect(getPublicLogoSrc(branding, "tir-bach")).toBe(
      "/api/public/property/logo?property=tir-bach&v=4",
    );
    expect(getPublicLogoSrc({ ...branding, has_logo: false }, "tir-bach")).toBeNull();
  });

  it("builds bounded CSS custom property map only for valid hex palettes", () => {
    const vars = buildPropertyBrandCssVars({
      schema_version: 1,
      display_name: "Test",
      tagline: "Test",
      primary_color: "#1e4d3b",
      primary_on_color: "#ffffff",
      primary_hover_color: "#173d2f",
      secondary_color: "#6b4e14",
      secondary_on_color: "#ffffff",
      has_custom_palette: true,
      has_logo: false,
    }) as Record<string, string>;

    expect(vars["--property-brand"]).toBe("#1E4D3B");
    expect(vars["--property-brand-on"]).toBe("#FFFFFF");
    expect(vars["--property-brand-secondary"]).toBe("#6B4E14");

    const unsafeVars = buildPropertyBrandCssVars({
      schema_version: 1,
      display_name: "Test",
      tagline: "Test",
      primary_color: "#123456;color:red",
      primary_on_color: "#ffffff",
      primary_hover_color: "#111111",
      secondary_color: "invalid",
      secondary_on_color: "#ffffff",
      has_custom_palette: true,
      has_logo: false,
    }) as Record<string, string>;
    expect(unsafeVars["--property-brand"]).toBeUndefined();
    expect(unsafeVars["--property-brand-secondary"]).toBeUndefined();
  });
});
