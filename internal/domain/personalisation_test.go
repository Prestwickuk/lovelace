package domain

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/branding"
)

func allowAllPerms(string) bool { return true }

func denyAdminPerms(perm string) bool {
	return perm == "ops:read" || perm == "maintenance:read" || perm == ""
}

func TestUserPreferencesValidationAndAllowlists(t *testing.T) {
	uid := uuid.New()
	in := UserPreferences{
		UserID:        uid,
		ThemeMode:     "DARK",
		Accent:        "brass",
		Density:       "compact",
		TextScale:     "large",
		HighContrast:  true,
		ReducedMotion: true,
		DefaultRoute:  "/calendar",
		Payload: UserPreferencePayload{
			Shortcuts:         []string{"/guests", "/reports", "/guests"},
			CollapsedSections: []string{"inventory", "settings"},
			CalendarHorizon:   "14d",
			CalendarGrouping:  "unit_type",
			TablePageSize:     50,
		},
	}

	got, err := ValidateUserPreferencesInput(in, allowAllPerms)
	if err != nil {
		t.Fatalf("expected valid input to pass, got %v", err)
	}
	if got.ThemeMode != "dark" || got.Accent != "brass" || got.Density != "compact" || got.TextScale != "large" {
		t.Fatalf("unexpected theme fields: %+v", got)
	}
	if len(got.Payload.Shortcuts) != 2 || got.Payload.CalendarHorizon != "14d" || got.Payload.TablePageSize != 50 {
		t.Fatalf("unexpected normalized payload: %+v", got.Payload)
	}

	// Reject arbitrary CSS/JS/external URLs/unknown enums/unauthorized routes.
	badCases := []struct {
		name   string
		mutate func(*UserPreferences)
		perm   PermCheckFunc
	}{
		{"bad_theme", func(p *UserPreferences) { p.ThemeMode = "purple" }, allowAllPerms},
		{"bad_accent_hex", func(p *UserPreferences) { p.Accent = "#ff00ff" }, allowAllPerms},
		{"external_route", func(p *UserPreferences) { p.DefaultRoute = "https://evil.example/phish" }, allowAllPerms},
		{"js_route", func(p *UserPreferences) { p.DefaultRoute = "javascript:alert(1)" }, allowAllPerms},
		{"unauthorized_route", func(p *UserPreferences) { p.DefaultRoute = "/users" }, denyAdminPerms},
		{"unauthorized_shortcut", func(p *UserPreferences) { p.Payload.Shortcuts = []string{"/reports"} }, denyAdminPerms},
		{"too_many_shortcuts", func(p *UserPreferences) {
			p.Payload.Shortcuts = []string{"/", "/calendar", "/reservations", "/guests", "/enquiries", "/blocks", "/rates"}
		}, allowAllPerms},
		{"bad_section", func(p *UserPreferences) { p.Payload.CollapsedSections = []string{"<script>"} }, allowAllPerms},
		{"bad_horizon", func(p *UserPreferences) { p.Payload.CalendarHorizon = "365d" }, allowAllPerms},
		{"bad_grouping", func(p *UserPreferences) { p.Payload.CalendarGrouping = "css-grid" }, allowAllPerms},
		{"bad_page_size", func(p *UserPreferences) { p.Payload.TablePageSize = 9999 }, allowAllPerms},
	}

	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			cand := in
			cand.Payload = in.Payload
			tc.mutate(&cand)
			if _, err := ValidateUserPreferencesInput(cand, tc.perm); err == nil {
				t.Fatalf("expected validation error for case %s", tc.name)
			} else {
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("expected *ValidationError for %s, got %T: %v", tc.name, err, err)
				}
			}
		})
	}
}

func TestFieldByFieldRecoveryAndSchemaUpcasting(t *testing.T) {
	uid := uuid.New()

	// Partial corrupt JSONB & unknown schema_version + legacy "normal" text scale.
	row := UserPreferences{
		UserID:        uid,
		SchemaVersion: 99,
		ThemeMode:     "dark",
		Accent:        "invalid-rainbow",
		Density:       "compact",
		TextScale:     "normal", // legacy alias
		HighContrast:  true,
		DefaultRoute:  "https://evil.example/",
	}
	rawJSON := []byte(`{
		"shortcuts": ["/guests", "https://evil.example", "/calendar"],
		"collapsed_sections": ["inventory", "bogus-section"],
		"calendar_horizon": "week",
		"calendar_grouping": "invalid_grouping",
		"table_page_size": 99999,
		"arbitrary_css": "body{display:none}"
	}`)

	got, warnings := DecodeAndNormalizeUserPreferences(row, rawJSON)
	if len(warnings) == 0 {
		t.Fatalf("expected warnings for obsolete/invalid preference fields")
	}
	// Valid sibling fields preserved!
	if got.ThemeMode != "dark" || got.Density != "compact" || !got.HighContrast {
		t.Fatalf("expected valid sibling fields to be preserved, got %+v", got)
	}
	// Legacy aliases upcasted cleanly!
	if got.TextScale != branding.ThemeTextScaleDefault {
		t.Fatalf("expected legacy text_scale 'normal' -> default, got %q", got.TextScale)
	}
	if got.Payload.CalendarHorizon != Horizon7Days {
		t.Fatalf("expected legacy calendar_horizon 'week' -> 7d, got %q", got.Payload.CalendarHorizon)
	}
	// Invalid fields recovered to safe defaults!
	if got.Accent != branding.ThemeAccentTeal || got.DefaultRoute != "/" || got.Payload.TablePageSize != DefaultTablePageSize || got.Payload.CalendarGrouping != GroupingUnit {
		t.Fatalf("unexpected fallback values: %+v", got)
	}
	if len(got.Payload.Shortcuts) != 2 || got.Payload.Shortcuts[0] != "/guests" || got.Payload.Shortcuts[1] != "/calendar" {
		t.Fatalf("expected valid shortcuts preserved and invalid dropped, got %+v", got.Payload.Shortcuts)
	}
	if len(got.Payload.CollapsedSections) != 1 || got.Payload.CollapsedSections[0] != "inventory" {
		t.Fatalf("expected valid collapsed section preserved, got %+v", got.Payload.CollapsedSections)
	}

	// Completely broken JSONB falls back cleanly without panic/error.
	broken, bWarnings := DecodeAndNormalizeUserPreferences(UserPreferences{UserID: uid, SchemaVersion: 1, ThemeMode: "light", Accent: "slate"}, []byte(`{not-json`))
	if len(bWarnings) == 0 || broken.ThemeMode != "light" || broken.Accent != "slate" {
		t.Fatalf("expected malformed JSONB to fall back cleanly while preserving valid columns, got %+v warnings=%v", broken, bWarnings)
	}
}

func TestResolveLayerCascadeAndPermissionFiltering(t *testing.T) {
	uid := uuid.New()
	propA := uuid.New()
	propB := uuid.New()

	global := DefaultUserPreferences(uid)
	global.ThemeMode = "dark"
	global.Accent = "olive"
	global.Density = "compact"
	global.DefaultRoute = "/reports"
	global.Payload.Shortcuts = []string{"/reports", "/maintenance", "/users"}
	global.Payload.CalendarHorizon = Horizon14Days
	global.Payload.TablePageSize = 50

	propWS := &UserPropertyWorkspace{
		UserID:        uid,
		PropertyID:    propA,
		SchemaVersion: CurrentWorkspaceSchemaVersion,
		Payload: PropertyWorkspacePayload{
			CalendarHorizon: Horizon30Days,
			TablePageSize:   10,
			DefaultRoute:    "/calendar",
		},
		SavedViews: []SavedOperationalView{
			{ID: "v1", Name: "Calendar Hold", Screen: SavedViewScreenCalendar, Filters: map[string]string{"status": "BOOKED"}},
			{ID: "v2", Name: "Returning VIPs", Screen: SavedViewScreenGuests, Filters: map[string]string{"segment": "RETURNING"}},
		},
	}

	// With full permissions: property overrides global display/landing, keeping global theme.
	full := ResolveLayerCascade(global, propWS, &propA, allowAllPerms, nil)
	if full.Theme.Mode != "dark" || full.Theme.Accent != "olive" || full.Theme.Density != "compact" {
		t.Fatalf("expected global theme to apply, got %+v", full.Theme)
	}
	if full.CalendarHorizon != Horizon30Days || full.TablePageSize != 10 || full.DefaultRoute != "/calendar" {
		t.Fatalf("expected property workspace overrides, got horizon=%s pageSize=%d default=%s", full.CalendarHorizon, full.TablePageSize, full.DefaultRoute)
	}
	if len(full.Shortcuts) != 3 || len(full.SavedViews) != 2 {
		t.Fatalf("expected 3 shortcuts and 2 saved views, got shortcuts=%v views=%v", full.Shortcuts, full.SavedViews)
	}

	// Downgrade permissions to maintenance-only: /calendar landing falls back to /,
	// /reports and /users shortcuts hidden, and calendar/guests saved views filtered out!
	restricted := ResolveLayerCascade(global, propWS, &propA, denyAdminPerms, nil)
	if restricted.DefaultRoute != "/" {
		t.Fatalf("expected unauthorized default route to fall back to /, got %q", restricted.DefaultRoute)
	}
	if len(restricted.Shortcuts) != 1 || restricted.Shortcuts[0].Path != "/maintenance" {
		t.Fatalf("expected only /maintenance shortcut permitted, got %+v", restricted.Shortcuts)
	}
	if len(restricted.SavedViews) != 0 {
		t.Fatalf("expected saved views filtered when screen permissions revoked, got %+v", restricted.SavedViews)
	}

	// Active property switch / mismatch discards Property A overrides cleanly.
	switched := ResolveLayerCascade(global, propWS, &propB, allowAllPerms, nil)
	if switched.CalendarHorizon != Horizon14Days || switched.TablePageSize != 50 || len(switched.SavedViews) != 0 {
		t.Fatalf("expected Property A state discarded when active property is B, got %+v", switched)
	}
}

func TestSavedViewResourceValidation(t *testing.T) {
	propA := uuid.New()
	utPropA := uuid.New()
	utPropB := uuid.New()
	catalogA := PropertyResourceCatalog{
		PropertyID:   propA,
		UnitTypeIDs:  map[uuid.UUID]struct{}{utPropA: {}},
		ChannelCodes: map[string]struct{}{"DIRECT": {}},
	}

	if _, err := ValidateSavedViewInput(SavedOperationalView{
		Name:   "Cross Property Attempt",
		Screen: SavedViewScreenCalendar,
		Filters: map[string]string{
			"unit_type_id": utPropB.String(),
		},
	}, catalogA); err == nil {
		t.Fatalf("expected cross-property unit_type_id to fail validation")
	}

	if _, err := ValidateSavedViewInput(SavedOperationalView{
		Name:   "Valid Bell Tent View",
		Screen: SavedViewScreenCalendar,
		Filters: map[string]string{
			"unit_type_id": utPropA.String(),
			"channel_code": "direct",
			"horizon":      "14d",
		},
	}, catalogA); err != nil {
		t.Fatalf("expected same-property resource catalog match to pass, got %v", err)
	}
}
