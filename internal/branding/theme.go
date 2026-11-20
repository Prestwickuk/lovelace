package branding

import (
	"math"
	"strconv"
	"strings"
)

// ThemeMode constants define the allowlisted CSS colour-scheme modes for PWE-1.
const (
	ThemeModeSystem = "system"
	ThemeModeLight  = "light"
	ThemeModeDark   = "dark"
)

// ThemeAccent constants define the curated, WCAG-verified LOVELACE accent keys.
const (
	ThemeAccentTeal  = "teal"
	ThemeAccentOlive = "olive"
	ThemeAccentSlate = "slate"
	ThemeAccentBrass = "brass"
)

// ThemeContrast constants define standard and high-contrast admin surfaces.
const (
	ThemeContrastStandard = "standard"
	ThemeContrastHigh     = "high"
)

// ThemeMotion constants define OS-following or explicitly reduced motion.
const (
	ThemeMotionSystem  = "system"
	ThemeMotionReduced = "reduced"
)

// ThemeDensity constants define workspace information density presets.
const (
	ThemeDensityComfortable = "comfortable"
	ThemeDensityCompact     = "compact"
)

// ThemeTextScale constants define root typographic scale presets.
const (
	ThemeTextScaleDefault = "default"
	ThemeTextScaleLarge   = "large"
	ThemeTextScaleXL      = "xl"
)

// AccentOption describes one curated, contrast-verified administrative accent.
type AccentOption struct {
	Key             string
	Label           string
	Description     string
	LightSolidHex   string
	LightHoverHex   string
	LightInkHex     string
	LightOnSolidHex string
	DarkSolidHex    string
	DarkHoverHex    string
	DarkInkHex      string
	DarkOnSolidHex  string
}

// ThemeSelection represents a validated set of HTML root attributes for PWE-1.
type ThemeSelection struct {
	Mode      string
	Accent    string
	Contrast  string
	Motion    string
	Density   string
	TextScale string
}

var accentCatalog = []AccentOption{
	{
		Key:             ThemeAccentTeal,
		Label:           "Coastal Teal",
		Description:     "Default LOVELACE coastal teal chrome and actions",
		LightSolidHex:   "#2F4F4A",
		LightHoverHex:   "#213B37",
		LightInkHex:     "#23413C",
		LightOnSolidHex: "#FFFFFF",
		DarkSolidHex:    "#6CB0A4",
		DarkHoverHex:    "#83C1B6",
		DarkInkHex:      "#83C5B9",
		DarkOnSolidHex:  "#0F1716",
	},
	{
		Key:             ThemeAccentOlive,
		Label:           "Hedgerow Olive",
		Description:     "Muted botanical olive accent inspired by country hedgerows",
		LightSolidHex:   "#41512C",
		LightHoverHex:   "#313E20",
		LightInkHex:     "#384724",
		LightOnSolidHex: "#FFFFFF",
		DarkSolidHex:    "#9BB579",
		DarkHoverHex:    "#AEC58F",
		DarkInkHex:      "#A6BF84",
		DarkOnSolidHex:  "#12170C",
	},
	{
		Key:             ThemeAccentSlate,
		Label:           "Coastal Slate",
		Description:     "Restrained maritime slate blue for dense operational screens",
		LightSolidHex:   "#2E475D",
		LightHoverHex:   "#213547",
		LightInkHex:     "#273F54",
		LightOnSolidHex: "#FFFFFF",
		DarkSolidHex:    "#84ACD0",
		DarkHoverHex:    "#9BBEDF",
		DarkInkHex:      "#93B8DA",
		DarkOnSolidHex:  "#0D151D",
	},
	{
		Key:             ThemeAccentBrass,
		Label:           "Heritage Brass",
		Description:     "Warm dark ochre-brass calibrated for WCAG AA legibility",
		LightSolidHex:   "#6B4E14",
		LightHoverHex:   "#533B0D",
		LightInkHex:     "#5D4310",
		LightOnSolidHex: "#FFFFFF",
		DarkSolidHex:    "#D6B162",
		DarkHoverHex:    "#E2C27B",
		DarkInkHex:      "#DFBD72",
		DarkOnSolidHex:  "#181205",
	},
}

// DefaultTheme returns the platform-safe PWE-1 default theme attribute set.
func DefaultTheme() ThemeSelection {
	return ThemeSelection{
		Mode:      ThemeModeSystem,
		Accent:    ThemeAccentTeal,
		Contrast:  ThemeContrastStandard,
		Motion:    ThemeMotionSystem,
		Density:   ThemeDensityComfortable,
		TextScale: ThemeTextScaleDefault,
	}
}

// CuratedAccents returns a copy of the curated contrast-verified accent catalog.
func CuratedAccents() []AccentOption {
	out := make([]AccentOption, len(accentCatalog))
	copy(out, accentCatalog)
	return out
}

// ResolveTheme strictly allowlists every PWE-1 presentation attribute and falls
// back cleanly to safe platform defaults for empty or unrecognised values.
func ResolveTheme(in ThemeSelection) ThemeSelection {
	def := DefaultTheme()

	switch strings.ToLower(strings.TrimSpace(in.Mode)) {
	case ThemeModeSystem, ThemeModeLight, ThemeModeDark:
		def.Mode = strings.ToLower(strings.TrimSpace(in.Mode))
	}

	accent := strings.ToLower(strings.TrimSpace(in.Accent))
	for _, opt := range accentCatalog {
		if accent == opt.Key {
			def.Accent = opt.Key
			break
		}
	}

	switch strings.ToLower(strings.TrimSpace(in.Contrast)) {
	case ThemeContrastStandard, ThemeContrastHigh:
		def.Contrast = strings.ToLower(strings.TrimSpace(in.Contrast))
	}

	switch strings.ToLower(strings.TrimSpace(in.Motion)) {
	case ThemeMotionSystem, ThemeMotionReduced:
		def.Motion = strings.ToLower(strings.TrimSpace(in.Motion))
	}

	switch strings.ToLower(strings.TrimSpace(in.Density)) {
	case ThemeDensityComfortable, ThemeDensityCompact:
		def.Density = strings.ToLower(strings.TrimSpace(in.Density))
	}

	switch strings.ToLower(strings.TrimSpace(in.TextScale)) {
	case ThemeTextScaleDefault, "normal":
		def.TextScale = ThemeTextScaleDefault
	case ThemeTextScaleLarge, ThemeTextScaleXL:
		def.TextScale = strings.ToLower(strings.TrimSpace(in.TextScale))
	}

	return def
}

// ContrastRatio computes the WCAG 2.1 contrast ratio between two #RRGGBB hex colors.
func ContrastRatio(hexA, hexB string) (float64, bool) {
	la, okA := relativeLuminance(hexA)
	lb, okB := relativeLuminance(hexB)
	if !okA || !okB {
		return 0, false
	}
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05), true
}

func relativeLuminance(hex string) (float64, bool) {
	clean := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(clean) != 6 {
		return 0, false
	}
	r64, errR := strconv.ParseUint(clean[0:2], 16, 8)
	g64, errG := strconv.ParseUint(clean[2:4], 16, 8)
	b64, errB := strconv.ParseUint(clean[4:6], 16, 8)
	if errR != nil || errG != nil || errB != nil {
		return 0, false
	}
	r := linearizeChannel(float64(r64) / 255.0)
	g := linearizeChannel(float64(g64) / 255.0)
	b := linearizeChannel(float64(b64) / 255.0)
	return 0.2126*r + 0.7152*g + 0.0722*b, true
}

func linearizeChannel(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}
