package domain

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/branding"
)

type storedPublicBrandingJSON struct {
	DisplayName    string `json:"display_name,omitempty"`
	Tagline        string `json:"tagline,omitempty"`
	RegionLabel    string `json:"region_label,omitempty"`
	PrimaryColor   string `json:"primary_color,omitempty"`
	SecondaryColor string `json:"secondary_color,omitempty"`
	LogoAlt        string `json:"logo_alt,omitempty"`
}

// PropertyBrandingRecord is the persisted database representation of PWE-4 branding.
type PropertyBrandingRecord struct {
	PropertyID      uuid.UUID
	PropertySlug    string
	PropertyName    string
	PropertyTagline string
	PropertyRegion  string
	LegacyBranding  json.RawMessage
	SchemaVersion   int
	DisplayName     string
	Tagline         string
	RegionLabel     string
	PrimaryColor    string
	SecondaryColor  string
	LogoAlt         string
	HeaderLabel     string
	Terminology     branding.AdminTerminology
	LogoStorageKey  string `json:"-"` // Never expose storage key over any HTTP/JSON interface.
	LogoContentType string
	LogoSizeBytes   int64
	LogoSHA256      string
	LogoWidth       int
	LogoHeight      int
	LogoVersion     int
	Version         int
	UpdatedBy       *uuid.UUID
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// PropertyBrandingView exposes validated PWE-4 branding state for authorized Admin UI and API clients.
type PropertyBrandingView struct {
	PropertyID       uuid.UUID                        `json:"property_id"`
	PropertySlug     string                           `json:"property_slug"`
	SchemaVersion    int                              `json:"schema_version"`
	Version          int                              `json:"version"`
	DisplayName      string                           `json:"display_name"`
	Tagline          string                           `json:"tagline"`
	RegionLabel      string                           `json:"region_label"`
	PrimaryColor     string                           `json:"primary_color"`
	SecondaryColor   string                           `json:"secondary_color"`
	LogoAlt          string                           `json:"logo_alt"`
	HeaderLabel      string                           `json:"header_label"`
	Terminology      branding.AdminTerminology        `json:"terminology"`
	ResolvedTerms    branding.AdminTerminology        `json:"resolved_terms"`
	HasLogo          bool                             `json:"has_logo"`
	LogoURL          string                           `json:"logo_url,omitempty"`
	LogoContentType  string                           `json:"logo_content_type,omitempty"`
	LogoSizeBytes    int64                            `json:"logo_size_bytes,omitempty"`
	LogoWidth        int                              `json:"logo_width,omitempty"`
	LogoHeight       int                              `json:"logo_height,omitempty"`
	LogoVersion      int                              `json:"logo_version,omitempty"`
	Palette          branding.ResolvedPropertyPalette `json:"-"`
	PublicProjection branding.PublicPropertyBranding  `json:"public_projection"`
	UpdatedAt        time.Time                        `json:"updated_at"`
}

// EncodePublicBrandingJSON builds the public_branding JSONB document and legacy compatible summary.
func EncodePublicBrandingJSON(in branding.PropertyBrandingInput) (json.RawMessage, error) {
	doc := storedPublicBrandingJSON{
		DisplayName:    in.DisplayName,
		Tagline:        in.Tagline,
		RegionLabel:    in.RegionLabel,
		PrimaryColor:   in.PrimaryColor,
		SecondaryColor: in.SecondaryColor,
		LogoAlt:        in.LogoAlt,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal public branding: %w", err)
	}
	return json.RawMessage(raw), nil
}

// EncodeAdminBrandingJSON builds the admin_branding JSONB document containing admin-only chrome & terminology.
func EncodeAdminBrandingJSON(headerLabel string, terms branding.AdminTerminology) (json.RawMessage, error) {
	doc := branding.AdminBrandingSettings{
		HeaderLabel: headerLabel,
		Terminology: terms,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal admin branding: %w", err)
	}
	return json.RawMessage(raw), nil
}

// DecodeStoredBranding populates parsed public/admin fields from raw JSONB columns.
func DecodeStoredBranding(rawPublic, rawAdmin, legacyBranding []byte) (storedPublicBrandingJSON, branding.AdminBrandingSettings) {
	var pub storedPublicBrandingJSON
	if len(rawPublic) > 0 {
		_ = json.Unmarshal(rawPublic, &pub)
	}
	if pub == (storedPublicBrandingJSON{}) && len(legacyBranding) > 0 {
		legacy := branding.ParsePropertyBranding(legacyBranding)
		pub.Tagline = legacy.DisplayTagline
		pub.PrimaryColor = legacy.PrimaryColor
		pub.SecondaryColor = legacy.AccentColor
	}

	var adm branding.AdminBrandingSettings
	if len(rawAdmin) > 0 {
		_ = json.Unmarshal(rawAdmin, &adm)
	}
	return pub, adm
}

// BuildPublicPropertyBranding projects strictly safe public branding metadata without admin-only fields.
func BuildPublicPropertyBranding(
	propSlug, propName, propTagline, propRegion string,
	rawPublic, legacyBranding []byte,
	hasLogo bool,
	logoWidth, logoHeight, logoVersion int,
) branding.PublicPropertyBranding {
	pub, _ := DecodeStoredBranding(rawPublic, nil, legacyBranding)

	displayName := strings.TrimSpace(pub.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(propName)
	}
	tagline := strings.TrimSpace(pub.Tagline)
	if tagline == "" {
		tagline = strings.TrimSpace(propTagline)
	}
	regionLabel := strings.TrimSpace(pub.RegionLabel)
	if regionLabel == "" {
		regionLabel = strings.TrimSpace(propRegion)
	}

	pal, err := branding.DeriveAndValidatePropertyPalette(pub.PrimaryColor, pub.SecondaryColor)
	if err != nil {
		pal, _ = branding.DeriveAndValidatePropertyPalette("", "")
	}

	out := branding.PublicPropertyBranding{
		SchemaVersion:        branding.PropertyBrandingSchemaVersion,
		DisplayName:          displayName,
		Tagline:              tagline,
		RegionLabel:          regionLabel,
		PrimaryColor:         pal.Primary.Base,
		PrimaryOnColor:       pal.Primary.OnColor,
		PrimaryHoverColor:    pal.Primary.Hover,
		PrimaryDarkColor:     pal.Primary.Dark,
		PrimaryDarkOnColor:   pal.Primary.DarkOn,
		SecondaryColor:       pal.Secondary.Base,
		SecondaryOnColor:     pal.Secondary.OnColor,
		SecondaryHoverColor:  pal.Secondary.Hover,
		SecondaryDarkColor:   pal.Secondary.Dark,
		SecondaryDarkOnColor: pal.Secondary.DarkOn,
		HasCustomPalette:     pal.HasCustomPrimary || pal.HasCustomSecondary,
		HasLogo:              hasLogo && logoVersion > 0,
	}

	if out.HasLogo && strings.TrimSpace(propSlug) != "" {
		out.LogoURL = fmt.Sprintf("/api/public/v1/properties/%s/logo?v=%d", url.PathEscape(strings.TrimSpace(propSlug)), logoVersion)
		alt := strings.TrimSpace(pub.LogoAlt)
		if alt == "" {
			alt = displayName + " logo"
		}
		out.LogoAlt = alt
		out.LogoWidth = logoWidth
		out.LogoHeight = logoHeight
		out.LogoVersion = logoVersion
	}

	return out
}

// ToView maps the internal DB record into an admin-safe PropertyBrandingView.
func (r PropertyBrandingRecord) ToView() PropertyBrandingView {
	hasLogo := strings.TrimSpace(r.LogoStorageKey) != "" && r.LogoVersion > 0
	pubRaw, _ := json.Marshal(storedPublicBrandingJSON{
		DisplayName:    r.DisplayName,
		Tagline:        r.Tagline,
		RegionLabel:    r.RegionLabel,
		PrimaryColor:   r.PrimaryColor,
		SecondaryColor: r.SecondaryColor,
		LogoAlt:        r.LogoAlt,
	})
	pubProj := BuildPublicPropertyBranding(
		r.PropertySlug,
		r.PropertyName,
		r.PropertyTagline,
		r.PropertyRegion,
		pubRaw,
		r.LegacyBranding,
		hasLogo,
		r.LogoWidth,
		r.LogoHeight,
		r.LogoVersion,
	)
	pal, err := branding.DeriveAndValidatePropertyPalette(r.PrimaryColor, r.SecondaryColor)
	if err != nil {
		pal, _ = branding.DeriveAndValidatePropertyPalette("", "")
	}

	view := PropertyBrandingView{
		PropertyID:       r.PropertyID,
		PropertySlug:     r.PropertySlug,
		SchemaVersion:    r.SchemaVersion,
		Version:          r.Version,
		DisplayName:      r.DisplayName,
		Tagline:          r.Tagline,
		RegionLabel:      r.RegionLabel,
		PrimaryColor:     r.PrimaryColor,
		SecondaryColor:   r.SecondaryColor,
		LogoAlt:          r.LogoAlt,
		HeaderLabel:      r.HeaderLabel,
		Terminology:      r.Terminology,
		ResolvedTerms:    branding.ResolveTerminology(r.Terminology),
		HasLogo:          hasLogo,
		Palette:          pal,
		PublicProjection: pubProj,
		UpdatedAt:        r.UpdatedAt,
	}
	if view.SchemaVersion <= 0 {
		view.SchemaVersion = branding.PropertyBrandingSchemaVersion
	}
	if view.Version <= 0 {
		view.Version = 1
	}
	if hasLogo {
		view.LogoURL = fmt.Sprintf("/property/logo?v=%d", r.LogoVersion)
		view.LogoContentType = r.LogoContentType
		view.LogoSizeBytes = r.LogoSizeBytes
		view.LogoWidth = r.LogoWidth
		view.LogoHeight = r.LogoHeight
		view.LogoVersion = r.LogoVersion
	}
	return view
}
