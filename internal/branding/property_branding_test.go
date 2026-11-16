package branding

import (
	"strings"
	"testing"
)

func TestDeriveAndValidatePropertyPaletteAcceptsAccessiblePairs(t *testing.T) {
	cases := []struct {
		name      string
		primary   string
		secondary string
	}{
		{"defaults", "", ""},
		{"lowercase_normalized", "#1e4d3b", "#6b4e14"},
		{"slate_and_burgundy", "#2E475D", "#6B2737"},
		{"forest_and_deep_earth", "#24523A", "#5B3A29"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pal, err := DeriveAndValidatePropertyPalette(tc.primary, tc.secondary)
			if err != nil {
				t.Fatalf("expected valid palette (%s, %s), got error: %v", tc.primary, tc.secondary, err)
			}
			for _, ramp := range []ResolvedColorRamp{pal.Primary, pal.Secondary} {
				if r, ok := ContrastRatio(ramp.Base, ramp.OnColor); !ok || r < ContrastMinNormalText {
					t.Fatalf("light button contrast failed: %s on %s = %.2f", ramp.OnColor, ramp.Base, r)
				}
				if r, ok := ContrastRatio(ramp.Hover, ramp.OnColor); !ok || r < ContrastMinNormalText {
					t.Fatalf("light hover contrast failed: %s on %s = %.2f", ramp.OnColor, ramp.Hover, r)
				}
				for _, surf := range lightSurfaceCheckHexes {
					if r, ok := ContrastRatio(ramp.Base, surf); !ok || r < ContrastMinNormalText {
						t.Fatalf("light surface contrast failed: %s on %s = %.2f", ramp.Base, surf, r)
					}
				}
				for _, surf := range darkSurfaceCheckHexes {
					if r, ok := ContrastRatio(ramp.Dark, surf); !ok || r < ContrastMinNormalText {
						t.Fatalf("dark surface contrast failed: %s on %s = %.2f", ramp.Dark, surf, r)
					}
				}
				for _, surf := range hcLightSurfaceHexes {
					if r, ok := ContrastRatio(ramp.HCLight, surf); !ok || r < ContrastMinHighContrast {
						t.Fatalf("HC light contrast failed: %s on %s = %.2f", ramp.HCLight, surf, r)
					}
				}
				for _, surf := range hcDarkSurfaceHexes {
					if r, ok := ContrastRatio(ramp.HCDark, surf); !ok || r < ContrastMinHighContrast {
						t.Fatalf("HC dark contrast failed: %s on %s = %.2f", ramp.HCDark, surf, r)
					}
				}
			}
			css := RenderPropertyCSS(pal)
			if strings.Contains(css, "--lovelace-") || strings.Contains(css, "--focus-ring") || strings.Contains(css, "url(") {
				t.Fatalf("rendered CSS attempted to override protected tokens or unsafe constructs:\n%s", css)
			}
		})
	}
}

func TestDeriveAndValidatePropertyPaletteRejectsUnsafeAndLowContrastHex(t *testing.T) {
	badHexes := []struct {
		name      string
		primary   string
		secondary string
	}{
		{"three_digit_shorthand", "#2F4", "#6B4E14"},
		{"eight_digit_alpha", "#2F4F4AFF", "#6B4E14"},
		{"named_css_colour", "teal", "#6B4E14"},
		{"css_injection_semicolon", "#2F4F4A;color:red", "#6B4E14"},
		{"low_contrast_pastel_mint", "#A8DADC", "#6B4E14"},
		{"low_contrast_yellow", "#FFFF00", "#6B4E14"},
		{"low_contrast_light_gray", "#CCCCCC", "#6B4E14"},
		{"low_contrast_bright_gold_secondary", "#2F4F4A", "#C49A45"},
		{"low_contrast_silver", "#2F4F4A", "#9E9E9E"},
	}

	for _, tc := range badHexes {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DeriveAndValidatePropertyPalette(tc.primary, tc.secondary); err == nil {
				t.Fatalf("expected palette (%q, %q) to fail validation", tc.primary, tc.secondary)
			}
		})
	}
}

func TestSanitizeAndValidateTerminology(t *testing.T) {
	valid, err := SanitizeAndValidateTerminology(AdminTerminology{
		UnitSingular:  "Pitch",
		UnitPlural:    "Pitches",
		GuestSingular: "Camper",
		GuestPlural:   "Campers",
		StayLabel:     "Booking",
	})
	if err != nil {
		t.Fatalf("expected valid terminology, got: %v", err)
	}
	resolved := ResolveTerminology(valid)
	if resolved.UnitPlural != "Pitches" || resolved.GuestSingular != "Camper" {
		t.Fatalf("unexpected resolved terminology: %+v", resolved)
	}

	badInputs := []AdminTerminology{
		{UnitSingular: "<script>alert(1)</script>"},
		{UnitPlural: "https://evil.example/pitch"},
		{GuestSingular: "Very Long Terminology Name Over 24 Chars"},
		{StayLabel: "Stay\nInjection"},
	}
	for _, bad := range badInputs {
		if _, err := SanitizeAndValidateTerminology(bad); err == nil {
			t.Fatalf("expected bad terminology %+v to fail", bad)
		}
	}
}
