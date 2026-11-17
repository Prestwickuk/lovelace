package branding

import "testing"

func TestResolveThemeDefaultsAndAllowlist(t *testing.T) {
	def := DefaultTheme()
	if def.Mode != ThemeModeSystem || def.Accent != ThemeAccentTeal ||
		def.Contrast != ThemeContrastStandard || def.Motion != ThemeMotionSystem ||
		def.Density != ThemeDensityComfortable || def.TextScale != ThemeTextScaleDefault {
		t.Fatalf("unexpected DefaultTheme: %+v", def)
	}

	got := ResolveTheme(ThemeSelection{
		Mode:      " DARK ",
		Accent:    "SLATE",
		Contrast:  "high",
		Motion:    "reduced",
		Density:   "compact",
		TextScale: "normal",
	})
	if got.Mode != ThemeModeDark || got.Accent != ThemeAccentSlate ||
		got.Contrast != ThemeContrastHigh || got.Motion != ThemeMotionReduced ||
		got.Density != ThemeDensityCompact || got.TextScale != ThemeTextScaleDefault {
		t.Fatalf("unexpected normalized ResolveTheme: %+v", got)
	}

	// Arbitrary/untrusted CSS values must always fall back to safe platform defaults.
	unsafe := ResolveTheme(ThemeSelection{
		Mode:      "custom;color:red",
		Accent:    "#ff00ff",
		Contrast:  "ultra",
		Motion:    "bounce",
		Density:   "micro",
		TextScale: "300%",
	})
	if unsafe != def {
		t.Fatalf("expected unsafe values to fall back to defaults %+v, got %+v", def, unsafe)
	}
}

func TestCuratedAccentsMeetWCAGAA(t *testing.T) {
	const (
		lightCardBG     = "#FFFDF8"
		lightPageBG     = "#F6F1E5"
		darkCardBG      = "#1D2320"
		darkPageBG      = "#141816"
		minTextContrast = 4.5
		minUIContrast   = 3.0
	)

	for _, opt := range CuratedAccents() {
		if r, ok := ContrastRatio(opt.LightSolidHex, opt.LightOnSolidHex); !ok || r < minTextContrast {
			t.Errorf("accent %q light button text contrast %.2f < %.1f", opt.Key, r, minTextContrast)
		}
		if r, ok := ContrastRatio(opt.LightHoverHex, opt.LightOnSolidHex); !ok || r < minTextContrast {
			t.Errorf("accent %q light hover button text contrast %.2f < %.1f", opt.Key, r, minTextContrast)
		}
		if r, ok := ContrastRatio(opt.LightInkHex, lightCardBG); !ok || r < minTextContrast {
			t.Errorf("accent %q light ink on card contrast %.2f < %.1f", opt.Key, r, minTextContrast)
		}
		if r, ok := ContrastRatio(opt.LightInkHex, lightPageBG); !ok || r < minTextContrast {
			t.Errorf("accent %q light ink on page contrast %.2f < %.1f", opt.Key, r, minTextContrast)
		}

		if r, ok := ContrastRatio(opt.DarkSolidHex, opt.DarkOnSolidHex); !ok || r < minTextContrast {
			t.Errorf("accent %q dark button text contrast %.2f < %.1f", opt.Key, r, minTextContrast)
		}
		if r, ok := ContrastRatio(opt.DarkHoverHex, opt.DarkOnSolidHex); !ok || r < minTextContrast {
			t.Errorf("accent %q dark hover button text contrast %.2f < %.1f", opt.Key, r, minTextContrast)
		}
		if r, ok := ContrastRatio(opt.DarkSolidHex, darkCardBG); !ok || r < minUIContrast {
			t.Errorf("accent %q dark button vs card contrast %.2f < %.1f", opt.Key, r, minUIContrast)
		}
		if r, ok := ContrastRatio(opt.DarkInkHex, darkCardBG); !ok || r < minTextContrast {
			t.Errorf("accent %q dark ink on card contrast %.2f < %.1f", opt.Key, r, minTextContrast)
		}
		if r, ok := ContrastRatio(opt.DarkInkHex, darkPageBG); !ok || r < minTextContrast {
			t.Errorf("accent %q dark ink on page contrast %.2f < %.1f", opt.Key, r, minTextContrast)
		}
	}
}
