package branding

import "encoding/json"

// Palette defines the coastal heritage LOVELACE colour tokens derived directly
// from the supplied circular emblem artwork.
type Palette struct {
	CoastalTeal   string `json:"coastal_teal"`
	DeepTeal      string `json:"deep_teal"`
	AntiqueGold   string `json:"antique_gold"`
	Parchment     string `json:"parchment"`
	WarmSand      string `json:"warm_sand"`
	MutedOlive    string `json:"muted_olive"`
	WarmCharcoal  string `json:"warm_charcoal"`
	DarkSlateInk  string `json:"dark_slate_ink"`
	TerracottaErr string `json:"terracotta_err"`
}

// Assets defines immutable static asset paths served under /static/brand/.
type Assets struct {
	MasterPath  string `json:"master_path"`
	FullLogo    string `json:"full_logo"`
	CompactLogo string `json:"compact_logo"`
	EmblemLogo  string `json:"emblem_logo"`
	Favicon     string `json:"favicon"`
	AltText     string `json:"alt_text"`
}

// PlatformBrand centralises the permanent LOVELACE Booking System identity,
// distinct from any managed hospitality property.
type PlatformBrand struct {
	Name            string  `json:"name"`
	Descriptor      string  `json:"descriptor"`
	FullName        string  `json:"full_name"`
	ConsoleTitle    string  `json:"console_title"`
	AttributionText string  `json:"attribution_text"`
	BrandVersion    string  `json:"brand_version"`
	Palette         Palette `json:"palette"`
	Assets          Assets  `json:"assets"`
}

// PropertyBranding represents optional future Phase 2-5 property-level visual
// customization stored on properties.branding_config without overriding LOVELACE identity.
type PropertyBranding struct {
	DisplayTagline string `json:"display_tagline,omitempty"`
	PrimaryColor   string `json:"primary_color,omitempty"`
	AccentColor    string `json:"accent_color,omitempty"`
	LogoURL        string `json:"logo_url,omitempty"`
}

// ParsePropertyBranding decodes properties.branding_config safely, returning zero value if empty.
func ParsePropertyBranding(raw json.RawMessage) PropertyBranding {
	if len(raw) == 0 {
		return PropertyBranding{}
	}
	var out PropertyBranding
	_ = json.Unmarshal(raw, &out)
	return out
}

// Default returns the authoritative LOVELACE Booking System configuration.
func Default() PlatformBrand {
	return PlatformBrand{
		Name:            "LOVELACE",
		Descriptor:      "Booking System",
		FullName:        "LOVELACE Booking System",
		ConsoleTitle:    "LOVELACE Admin",
		AttributionText: "Powered by LOVELACE Booking System",
		BrandVersion:    "1.5",
		Palette: Palette{
			CoastalTeal:   "#2F4F4A",
			DeepTeal:      "#213B37",
			AntiqueGold:   "#C49A45",
			Parchment:     "#F6F1E5",
			WarmSand:      "#EDE6D6",
			MutedOlive:    "#6B7B4E",
			WarmCharcoal:  "#1E211E",
			DarkSlateInk:  "#1B201E",
			TerracottaErr: "#A84232",
		},
		Assets: Assets{
			MasterPath:  "/static/brand/lovelace-logo-full.png",
			FullLogo:    "/static/brand/lovelace-logo-full.png",
			CompactLogo: "/static/brand/lovelace-logo-compact.png",
			EmblemLogo:  "/static/brand/lovelace-logo-emblem.png",
			Favicon:     "/static/brand/lovelace-favicon.png",
			AltText:     "LOVELACE Booking System logo",
		},
	}
}
