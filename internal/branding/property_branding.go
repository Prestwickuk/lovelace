package branding

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"
)

const (
	// PropertyBrandingSchemaVersion versions stored PWE-4 property branding payloads.
	PropertyBrandingSchemaVersion = 1

	ContrastMinNormalText   = 4.5
	ContrastMinLargeUI      = 3.0
	ContrastMinHighContrast = 7.0

	defaultPropertyPrimaryHex   = "#2F4F4A"
	defaultPropertySecondaryHex = "#6B4E14"
)

var (
	reNormalizedHex = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

	lightSurfaceCheckHexes = []string{"#FFFFFF", "#FFFDF8", "#F6F1E5", "#FAF7F1"}
	darkSurfaceCheckHexes  = []string{"#1D2320", "#141816", "#233C38", "#17221D"}
	hcLightSurfaceHexes    = []string{"#FFFFFF", "#EDE9DF"}
	hcDarkSurfaceHexes     = []string{"#111614", "#080A09"}
)

// PublicPropertyBranding holds strictly safe public-facing branding data derived
// from property settings. It intentionally omits admin terminology, storage keys,
// internal user metadata, and draft/admin-only settings.
type PublicPropertyBranding struct {
	SchemaVersion        int    `json:"schema_version"`
	DisplayName          string `json:"display_name"`
	Tagline              string `json:"tagline"`
	RegionLabel          string `json:"region_label,omitempty"`
	PrimaryColor         string `json:"primary_color"`
	PrimaryOnColor       string `json:"primary_on_color"`
	PrimaryHoverColor    string `json:"primary_hover_color"`
	PrimaryDarkColor     string `json:"primary_dark_color"`
	PrimaryDarkOnColor   string `json:"primary_dark_on_color"`
	SecondaryColor       string `json:"secondary_color"`
	SecondaryOnColor     string `json:"secondary_on_color"`
	SecondaryHoverColor  string `json:"secondary_hover_color"`
	SecondaryDarkColor   string `json:"secondary_dark_color"`
	SecondaryDarkOnColor string `json:"secondary_dark_on_color"`
	HasCustomPalette     bool   `json:"has_custom_palette"`
	HasLogo              bool   `json:"has_logo"`
	LogoURL              string `json:"logo_url,omitempty"`
	LogoAlt              string `json:"logo_alt,omitempty"`
	LogoWidth            int    `json:"logo_width,omitempty"`
	LogoHeight           int    `json:"logo_height,omitempty"`
	LogoVersion          int    `json:"logo_version,omitempty"`
}

// AdminTerminology stores bounded, admin-only vocabulary overrides for standard
// console headings and labels. Never emitted to public endpoints, routes, email
// variables, or audit enums.
type AdminTerminology struct {
	UnitSingular  string `json:"unit_singular,omitempty"`
	UnitPlural    string `json:"unit_plural,omitempty"`
	GuestSingular string `json:"guest_singular,omitempty"`
	GuestPlural   string `json:"guest_plural,omitempty"`
	StayLabel     string `json:"stay_label,omitempty"`
}

// DefaultAdminTerminology returns LOVELACE's canonical admin console terms.
func DefaultAdminTerminology() AdminTerminology {
	return AdminTerminology{
		UnitSingular:  "Unit",
		UnitPlural:    "Units",
		GuestSingular: "Guest",
		GuestPlural:   "Guests",
		StayLabel:     "Reservation",
	}
}

// ResolveTerminology fills any empty override slot with its canonical default.
func ResolveTerminology(t AdminTerminology) AdminTerminology {
	def := DefaultAdminTerminology()
	out := t
	if strings.TrimSpace(out.UnitSingular) == "" {
		out.UnitSingular = def.UnitSingular
	}
	if strings.TrimSpace(out.UnitPlural) == "" {
		out.UnitPlural = def.UnitPlural
	}
	if strings.TrimSpace(out.GuestSingular) == "" {
		out.GuestSingular = def.GuestSingular
	}
	if strings.TrimSpace(out.GuestPlural) == "" {
		out.GuestPlural = def.GuestPlural
	}
	if strings.TrimSpace(out.StayLabel) == "" {
		out.StayLabel = def.StayLabel
	}
	return out
}

// HasCustomTerms reports whether any non-default admin terminology override is configured.
func (t AdminTerminology) HasCustomTerms() bool {
	return strings.TrimSpace(t.UnitSingular) != "" ||
		strings.TrimSpace(t.UnitPlural) != "" ||
		strings.TrimSpace(t.GuestSingular) != "" ||
		strings.TrimSpace(t.GuestPlural) != "" ||
		strings.TrimSpace(t.StayLabel) != ""
}

// SanitizeAndValidateTerminology validates bounded administrative terminology labels.
func SanitizeAndValidateTerminology(in AdminTerminology) (AdminTerminology, error) {
	fields := []struct {
		name  string
		value string
		dst   *string
	}{
		{name: "unit singular label", value: in.UnitSingular},
		{name: "unit plural label", value: in.UnitPlural},
		{name: "guest singular label", value: in.GuestSingular},
		{name: "guest plural label", value: in.GuestPlural},
		{name: "stay label", value: in.StayLabel},
	}

	var out AdminTerminology
	fields[0].dst = &out.UnitSingular
	fields[1].dst = &out.UnitPlural
	fields[2].dst = &out.GuestSingular
	fields[3].dst = &out.GuestPlural
	fields[4].dst = &out.StayLabel

	for _, f := range fields {
		cleaned, err := sanitizeBoundedPlainLabel(f.name, f.value, 24)
		if err != nil {
			return AdminTerminology{}, err
		}
		*f.dst = cleaned
	}
	return out, nil
}

// AdminBrandingSettings holds internal administrative branding and terminology settings.
type AdminBrandingSettings struct {
	HeaderLabel string           `json:"header_label,omitempty"`
	Terminology AdminTerminology `json:"terminology,omitempty"`
}

// PropertyBrandingInput is the validated write payload for PWE-4 branding updates.
type PropertyBrandingInput struct {
	DisplayName    string
	Tagline        string
	RegionLabel    string
	PrimaryColor   string
	SecondaryColor string
	LogoAlt        string
	HeaderLabel    string
	Terminology    AdminTerminology
}

// ResolvedColorRamp captures validated contrast-safe tokens across Light, Dark, and High-Contrast modes.
type ResolvedColorRamp struct {
	Base      string
	Hover     string
	OnColor   string
	Dark      string
	DarkHover string
	DarkOn    string
	HCLight   string
	HCLightOn string
	HCDark    string
	HCDarkOn  string
	SoftRGBA  string
	DarkSoft  string
}

// ResolvedPropertyPalette holds validated, contrast-verified primary and secondary token sets.
type ResolvedPropertyPalette struct {
	HasCustomPrimary   bool
	HasCustomSecondary bool
	Primary            ResolvedColorRamp
	Secondary          ResolvedColorRamp
}

// NormalizeOptionalHex validates that raw (once trimmed) is empty or a strict 6-digit #RRGGBB hex value.
func NormalizeOptionalHex(fieldName, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if !reNormalizedHex.MatchString(trimmed) {
		return "", fmt.Errorf("%s must be a six-digit hex colour such as #2F4F4A", fieldName)
	}
	return strings.ToUpper(trimmed), nil
}

// SanitizeAndValidatePublicText validates printable single-line branding text without markup or URLs.
func SanitizeAndValidatePublicText(fieldName, raw string, maxLen int) (string, error) {
	return sanitizeBoundedPlainLabel(fieldName, raw, maxLen)
}

func sanitizeBoundedPlainLabel(fieldName, raw string, maxLen int) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if len([]rune(trimmed)) > maxLen {
		return "", fmt.Errorf("%s must be %d characters or fewer", fieldName, maxLen)
	}
	lower := strings.ToLower(trimmed)
	if strings.ContainsAny(trimmed, "<>\"`\r\n\t;{}") ||
		strings.Contains(lower, "javascript:") ||
		strings.Contains(lower, "http://") ||
		strings.Contains(lower, "https://") {
		return "", fmt.Errorf("%s contains disallowed characters or markup", fieldName)
	}
	for _, r := range trimmed {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%s contains invalid control characters", fieldName)
		}
	}
	return trimmed, nil
}

// DeriveAndValidatePropertyPalette validates optional custom Primary and Secondary #RRGGBB hex colours
// against light, dark, hover, high-contrast, sidebar, and public surface pairs server-side before save.
func DeriveAndValidatePropertyPalette(primaryRaw, secondaryRaw string) (ResolvedPropertyPalette, error) {
	primaryHex, err := NormalizeOptionalHex("primary colour", primaryRaw)
	if err != nil {
		return ResolvedPropertyPalette{}, err
	}
	secondaryHex, err := NormalizeOptionalHex("secondary colour", secondaryRaw)
	if err != nil {
		return ResolvedPropertyPalette{}, err
	}

	effPrimary := primaryHex
	if effPrimary == "" {
		effPrimary = defaultPropertyPrimaryHex
	}
	effSecondary := secondaryHex
	if effSecondary == "" {
		effSecondary = defaultPropertySecondaryHex
	}

	primaryRamp, err := buildAndValidateRamp("primary colour", effPrimary)
	if err != nil {
		return ResolvedPropertyPalette{}, err
	}
	secondaryRamp, err := buildAndValidateRamp("secondary colour", effSecondary)
	if err != nil {
		return ResolvedPropertyPalette{}, err
	}

	return ResolvedPropertyPalette{
		HasCustomPrimary:   primaryHex != "",
		HasCustomSecondary: secondaryHex != "",
		Primary:            primaryRamp,
		Secondary:          secondaryRamp,
	}, nil
}

func buildAndValidateRamp(label, baseHex string) (ResolvedColorRamp, error) {
	r, g, b, ok := parseHexRGB(baseHex)
	if !ok {
		return ResolvedColorRamp{}, fmt.Errorf("%s must be a valid #RRGGBB colour", label)
	}
	baseNorm := formatHexRGB(r, g, b)

	// 1. Choose foreground for solid buttons/badges (#FFFFFF or #111614) and require >= 4.5:1.
	onWhite, _ := ContrastRatio(baseNorm, "#FFFFFF")
	onInk, _ := ContrastRatio(baseNorm, "#111614")
	onColor := "#FFFFFF"
	if onInk > onWhite {
		onColor = "#111614"
	}
	if best, _ := ContrastRatio(baseNorm, onColor); best < ContrastMinNormalText {
		return ResolvedColorRamp{}, fmt.Errorf("%s (%s) fails button/badge text contrast (%.2f:1, need %.1f:1)", label, baseNorm, best, ContrastMinNormalText)
	}

	// 2. Require direct normal-text & control-boundary contrast against EVERY light/cream/public surface.
	for _, surf := range lightSurfaceCheckHexes {
		ratio, _ := ContrastRatio(baseNorm, surf)
		if ratio < ContrastMinNormalText {
			return ResolvedColorRamp{}, fmt.Errorf("%s (%s) lacks sufficient contrast on light surface %s (%.2f:1, minimum %.1f:1 required)", label, baseNorm, surf, ratio, ContrastMinNormalText)
		}
	}

	// 3. Derive hover state safely maintaining >= 4.5:1 text contrast and >= 3.0:1 boundary contrast.
	hoverHex := shiftColorToward(r, g, b, 0, 0, 0, 0.16)
	if onColor == "#111614" {
		hoverHex = shiftColorToward(r, g, b, 255, 255, 255, 0.16)
	}
	if hr, _ := ContrastRatio(hoverHex, onColor); hr < ContrastMinNormalText {
		return ResolvedColorRamp{}, fmt.Errorf("%s hover pairing (%s on %s) fails WCAG contrast (%.2f:1, need %.1f:1)", label, onColor, hoverHex, hr, ContrastMinNormalText)
	}
	for _, surf := range lightSurfaceCheckHexes {
		if br, _ := ContrastRatio(hoverHex, surf); br < ContrastMinLargeUI {
			return ResolvedColorRamp{}, fmt.Errorf("%s hover boundary fails contrast against %s (%.2f:1, need %.1f:1)", label, surf, br, ContrastMinLargeUI)
		}
	}

	// 4. Derive and verify Dark mode accessible luminous variant against all dark/sidebar/hero surfaces.
	darkOn := "#0D1412"
	darkHex := deriveLuminousVariant(r, g, b, darkSurfaceCheckHexes, darkOn, ContrastMinNormalText)
	dr, dg, db, _ := parseHexRGB(darkHex)
	darkHover := shiftColorToward(dr, dg, db, 255, 255, 255, 0.12)
	for _, surf := range darkSurfaceCheckHexes {
		if r1, _ := ContrastRatio(darkHex, surf); r1 < ContrastMinNormalText {
			return ResolvedColorRamp{}, fmt.Errorf("%s dark-mode pairing fails contrast against %s (%.2f:1)", label, surf, r1)
		}
		if r2, _ := ContrastRatio(darkHover, surf); r2 < ContrastMinLargeUI {
			return ResolvedColorRamp{}, fmt.Errorf("%s dark-mode hover boundary fails contrast against %s (%.2f:1)", label, surf, r2)
		}
	}
	if rOn, _ := ContrastRatio(darkHex, darkOn); rOn < ContrastMinNormalText {
		return ResolvedColorRamp{}, fmt.Errorf("%s dark-mode button text contrast failed (%.2f:1)", label, rOn)
	}
	if rHoverOn, _ := ContrastRatio(darkHover, darkOn); rHoverOn < ContrastMinNormalText {
		return ResolvedColorRamp{}, fmt.Errorf("%s dark-mode hover text contrast failed (%.2f:1)", label, rHoverOn)
	}

	// 5. Derive and verify High-Contrast Light & High-Contrast Dark variants (>= 7.0:1).
	hcLightOn := "#FFFFFF"
	hcLightHex := deriveDarkenedVariant(r, g, b, hcLightSurfaceHexes, hcLightOn, ContrastMinHighContrast)
	for _, surf := range hcLightSurfaceHexes {
		if rr, _ := ContrastRatio(hcLightHex, surf); rr < ContrastMinHighContrast {
			return ResolvedColorRamp{}, fmt.Errorf("%s high-contrast light pairing failed against %s (%.2f:1)", label, surf, rr)
		}
	}
	if rr, _ := ContrastRatio(hcLightHex, hcLightOn); rr < ContrastMinHighContrast {
		return ResolvedColorRamp{}, fmt.Errorf("%s high-contrast light button pairing failed (%.2f:1)", label, rr)
	}

	hcDarkOn := "#080A09"
	hcDarkHex := deriveLuminousVariant(r, g, b, hcDarkSurfaceHexes, hcDarkOn, ContrastMinHighContrast)
	for _, surf := range hcDarkSurfaceHexes {
		if rr, _ := ContrastRatio(hcDarkHex, surf); rr < ContrastMinHighContrast {
			return ResolvedColorRamp{}, fmt.Errorf("%s high-contrast dark pairing failed against %s (%.2f:1)", label, surf, rr)
		}
	}
	if rr, _ := ContrastRatio(hcDarkHex, hcDarkOn); rr < ContrastMinHighContrast {
		return ResolvedColorRamp{}, fmt.Errorf("%s high-contrast dark button pairing failed (%.2f:1)", label, rr)
	}

	return ResolvedColorRamp{
		Base:      baseNorm,
		Hover:     hoverHex,
		OnColor:   onColor,
		Dark:      darkHex,
		DarkHover: darkHover,
		DarkOn:    darkOn,
		HCLight:   hcLightHex,
		HCLightOn: hcLightOn,
		HCDark:    hcDarkHex,
		HCDarkOn:  hcDarkOn,
		SoftRGBA:  fmt.Sprintf("rgba(%d, %d, %d, 0.12)", r, g, b),
		DarkSoft:  fmt.Sprintf("rgba(%d, %d, %d, 0.18)", dr, dg, db),
	}, nil
}

// RenderPropertyCSS generates a strictly bounded, CSP-safe same-origin CSS variable sheet
// for the administrative workspace without modifying platform identity or focus tokens.
func RenderPropertyCSS(pal ResolvedPropertyPalette) string {
	var b strings.Builder
	b.WriteString("/* LOVELACE PWE-4 Bounded Property Chrome Tokens (CSP style-src 'self') */\n")

	if !pal.HasCustomPrimary && !pal.HasCustomSecondary {
		b.WriteString("/* Using default platform Coastal Teal / Antique Brass property tokens. */\n")
		return b.String()
	}

	writeRamp := func(p, s ResolvedColorRamp, dark bool) {
		if !dark {
			fmt.Fprintf(&b, "  --property-brand: %s;\n", p.Base)
			fmt.Fprintf(&b, "  --property-brand-hover: %s;\n", p.Hover)
			fmt.Fprintf(&b, "  --property-brand-on: %s;\n", p.OnColor)
			fmt.Fprintf(&b, "  --property-brand-soft: %s;\n", p.SoftRGBA)
			fmt.Fprintf(&b, "  --property-brand-border: %s;\n", s.Base)
			fmt.Fprintf(&b, "  --property-brand-secondary: %s;\n", s.Base)
			fmt.Fprintf(&b, "  --property-brand-secondary-on: %s;\n", s.OnColor)
			fmt.Fprintf(&b, "  --property-brand-secondary-soft: %s;\n", s.SoftRGBA)
		} else {
			fmt.Fprintf(&b, "  --property-brand: %s;\n", p.Dark)
			fmt.Fprintf(&b, "  --property-brand-hover: %s;\n", p.DarkHover)
			fmt.Fprintf(&b, "  --property-brand-on: %s;\n", p.DarkOn)
			fmt.Fprintf(&b, "  --property-brand-soft: %s;\n", p.DarkSoft)
			fmt.Fprintf(&b, "  --property-brand-border: %s;\n", s.Dark)
			fmt.Fprintf(&b, "  --property-brand-secondary: %s;\n", s.Dark)
			fmt.Fprintf(&b, "  --property-brand-secondary-on: %s;\n", s.DarkOn)
			fmt.Fprintf(&b, "  --property-brand-secondary-soft: %s;\n", p.DarkSoft)
		}
	}

	b.WriteString(":root {\n")
	writeRamp(pal.Primary, pal.Secondary, false)
	b.WriteString("}\n")

	b.WriteString(":root[data-theme=\"dark\"] {\n")
	writeRamp(pal.Primary, pal.Secondary, true)
	b.WriteString("}\n")

	b.WriteString("@media (prefers-color-scheme: dark) {\n  :root:not([data-theme]), :root[data-theme=\"system\"] {\n")
	writeRamp(pal.Primary, pal.Secondary, true)
	b.WriteString("  }\n}\n")

	b.WriteString(":root[data-contrast=\"high\"] {\n")
	fmt.Fprintf(&b, "  --property-brand: %s;\n", pal.Primary.HCLight)
	fmt.Fprintf(&b, "  --property-brand-hover: %s;\n", pal.Primary.HCLight)
	fmt.Fprintf(&b, "  --property-brand-on: %s;\n", pal.Primary.HCLightOn)
	fmt.Fprintf(&b, "  --property-brand-border: %s;\n", pal.Secondary.HCLight)
	fmt.Fprintf(&b, "  --property-brand-secondary: %s;\n", pal.Secondary.HCLight)
	fmt.Fprintf(&b, "  --property-brand-secondary-on: %s;\n", pal.Secondary.HCLightOn)
	b.WriteString("}\n")

	b.WriteString(":root[data-theme=\"dark\"][data-contrast=\"high\"] {\n")
	fmt.Fprintf(&b, "  --property-brand: %s;\n", pal.Primary.HCDark)
	fmt.Fprintf(&b, "  --property-brand-hover: %s;\n", pal.Primary.HCDark)
	fmt.Fprintf(&b, "  --property-brand-on: %s;\n", pal.Primary.HCDarkOn)
	fmt.Fprintf(&b, "  --property-brand-border: %s;\n", pal.Secondary.HCDark)
	fmt.Fprintf(&b, "  --property-brand-secondary: %s;\n", pal.Secondary.HCDark)
	fmt.Fprintf(&b, "  --property-brand-secondary-on: %s;\n", pal.Secondary.HCDarkOn)
	b.WriteString("}\n")

	b.WriteString("@media (prefers-color-scheme: dark) {\n  :root[data-theme=\"system\"][data-contrast=\"high\"] {\n")
	fmt.Fprintf(&b, "  --property-brand: %s;\n", pal.Primary.HCDark)
	fmt.Fprintf(&b, "  --property-brand-hover: %s;\n", pal.Primary.HCDark)
	fmt.Fprintf(&b, "  --property-brand-on: %s;\n", pal.Primary.HCDarkOn)
	fmt.Fprintf(&b, "  --property-brand-border: %s;\n", pal.Secondary.HCDark)
	fmt.Fprintf(&b, "  --property-brand-secondary: %s;\n", pal.Secondary.HCDark)
	fmt.Fprintf(&b, "  --property-brand-secondary-on: %s;\n", pal.Secondary.HCDarkOn)
	b.WriteString("  }\n}\n")

	return b.String()
}

func parseHexRGB(hex string) (uint8, uint8, uint8, bool) {
	if !reNormalizedHex.MatchString(hex) {
		return 0, 0, 0, false
	}
	var r, g, b uint8
	if _, err := fmt.Sscanf(strings.ToUpper(hex), "#%02X%02X%02X", &r, &g, &b); err != nil {
		return 0, 0, 0, false
	}
	return r, g, b, true
}

func formatHexRGB(r, g, b uint8) string {
	return fmt.Sprintf("#%02X%02X%02X", r, g, b)
}

func shiftColorToward(r, g, b, tr, tg, tb uint8, factor float64) string {
	lerp := func(a, b uint8) uint8 {
		v := math.Round(float64(a)*(1.0-factor) + float64(b)*factor)
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v)
	}
	return formatHexRGB(lerp(r, tr), lerp(g, tg), lerp(b, tb))
}

func deriveLuminousVariant(r, g, b uint8, darkSurfaces []string, onDark string, minRatio float64) string {
	// Blend toward soft parchment-mint white so hue character stays recognizable while meeting contrast.
	const targetR, targetG, targetB uint8 = 238, 244, 240
	for step := 0; step <= 100; step++ {
		cand := shiftColorToward(r, g, b, targetR, targetG, targetB, float64(step)/100.0)
		ok := true
		for _, s := range darkSurfaces {
			if ratio, _ := ContrastRatio(cand, s); ratio < minRatio {
				ok = false
				break
			}
		}
		if ok {
			if onRatio, _ := ContrastRatio(cand, onDark); onRatio >= minRatio {
				return cand
			}
		}
	}
	return "#E6ECE9"
}

func deriveDarkenedVariant(r, g, b uint8, lightSurfaces []string, onLight string, minRatio float64) string {
	const targetR, targetG, targetB uint8 = 10, 14, 12
	for step := 0; step <= 100; step++ {
		cand := shiftColorToward(r, g, b, targetR, targetG, targetB, float64(step)/100.0)
		ok := true
		for _, s := range lightSurfaces {
			if ratio, _ := ContrastRatio(cand, s); ratio < minRatio {
				ok = false
				break
			}
		}
		if ok {
			if onRatio, _ := ContrastRatio(cand, onLight); onRatio >= minRatio {
				return cand
			}
		}
	}
	return "#0E1714"
}
