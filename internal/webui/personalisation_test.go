package webui

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/branding"
	"github.com/Prestwickuk/lovelace/internal/config"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/service"
)

func TestAccountPersonalisationTemplateRenderingAndCSP(t *testing.T) {
	h, err := New(nil, config.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ws := domain.DefaultResolvedWorkspace()
	ws.Theme = branding.ResolveTheme(branding.ThemeSelection{
		Mode:      branding.ThemeModeDark,
		Accent:    branding.ThemeAccentBrass,
		Contrast:  branding.ThemeContrastHigh,
		Motion:    branding.ThemeMotionReduced,
		Density:   branding.ThemeDensityCompact,
		TextScale: branding.ThemeTextScaleXL,
	})
	ws.Global.ThemeMode = branding.ThemeModeDark
	ws.Global.Accent = branding.ThemeAccentBrass
	ws.Global.HighContrast = true
	ws.Global.ReducedMotion = true
	ws.Global.Density = branding.ThemeDensityCompact
	ws.Global.TextScale = branding.ThemeTextScaleXL
	ws.Global.DefaultRoute = "/calendar"
	ws.Global.Payload.Shortcuts = []string{"/calendar", "/guests"}
	ws.Global.Payload.CollapsedSections = []string{"inventory"}
	ws.CollapsedSections = map[string]bool{"inventory": true}
	ws.Shortcuts = []domain.ResolvedShortcut{
		{Path: "/calendar", Label: "Booking Calendar"},
		{Path: "/guests", Label: "Guest Directory"},
	}
	ws.Warnings = []string{"unrecognised theme field recovered"}
	ws.SavedViews = []domain.SavedOperationalView{
		{
			ID:      "view-1",
			Name:    "Direct Bookings",
			Screen:  domain.SavedViewScreenReservations,
			Filters: map[string]string{"channel_code": "DIRECT", "sort": "stay_desc"},
		},
	}

	base := baseView{
		Title:               "My Account",
		ActiveNav:           "account",
		Brand:               branding.Default(),
		Theme:               ws.Theme,
		Workspace:           ws,
		CSRFToken:           "csrf-abc",
		CanReadReservations: true,
		CanReadGuests:       true,
		CanReadMaintenance:  true,
		Property:            domain.Property{ID: uuid.New(), Slug: "demo-property", Name: "Meadowbrook Meadows", Region: "Countryside"},
		Actor:               service.Actor{UserID: uuid.New(), FullName: "Carys", Email: "carys@example.test", Role: domain.RoleManager},
		AccountPrefs: &accountPersonalisationData{
			Enabled:         true,
			Accents:         branding.CuratedAccents(),
			NavSections:     domain.NavSections(),
			PermittedRoutes: permittedAdminRoutesForRole(domain.RoleManager),
		},
	}

	rec := httptest.NewRecorder()
	h.renderPage(rec, "account.html", base)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	body := rec.Body.String()

	for _, want := range []string{
		`data-theme="dark"`,
		`data-accent="brass"`,
		`data-contrast="high"`,
		`data-motion="reduced"`,
		`data-density="compact"`,
		`data-text-scale="xl"`,
		`Pinned Shortcuts`,
		`<a href="/calendar">Booking Calendar</a>`,
		`id="personalisation-heading"`,
		`/account/preferences/global`,
		`/account/preferences/global/reset`,
		`/account/preferences/workspace`,
		`/account/preferences/workspace/reset`,
		`/account/preferences/views/view-1/delete`,
		`Direct Bookings`,
		`Preference recovery notices:`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered account page missing %q", want)
		}
	}

	// Inventory collapsed section must NOT have `open` when active nav is account (inside settings).
	if strings.Contains(body, `<details class="nav-group" open>`+"\n        "+`<summary class="nav-section">Inventory`) {
		t.Errorf("expected collapsed inventory section to omit open attribute on /account")
	}

	// Ensure zero inline style or script tags in rendered HTML.
	if regexp.MustCompile(`(?i)\bstyle\s*=|<style\b`).MatchString(body) {
		t.Errorf("rendered HTML violated style-src 'self' CSP")
	}

	// When MustRotatePassword == true, personalisation forms must NOT be rendered.
	rotBase := base
	rotBase.Actor.MustRotatePassword = true
	rotBase.AccountPrefs = &accountPersonalisationData{Enabled: false}
	rotRec := httptest.NewRecorder()
	h.renderPage(rotRec, "account.html", rotBase)
	rotBody := rotRec.Body.String()
	if strings.Contains(rotBody, `/account/preferences/global`) {
		t.Errorf("personalisation form must not be rendered while password rotation is pending")
	}
	if !strings.Contains(rotBody, "rotate your temporary password") {
		t.Errorf("expected password rotation gate guidance inside personalisation section")
	}
}

func TestReservationsAndGuestsSavedViewURLAndPaginationHelpers(t *testing.T) {
	start, end, pages := paginateRange(55, 2, 25)
	if start != 25 || end != 50 || pages != 3 {
		t.Fatalf("unexpected paginateRange result: start=%d end=%d pages=%d", start, end, pages)
	}
	start2, end2, pages2 := paginateRange(55, 3, 25)
	if start2 != 50 || end2 != 55 || pages2 != 3 {
		t.Fatalf("unexpected last page paginateRange: start=%d end=%d pages=%d", start2, end2, pages2)
	}
	res := []domain.Reservation{
		{ID: uuid.New(), ArrivalDate: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC), CreatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
		{ID: uuid.New(), ArrivalDate: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC), CreatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
	}
	sortReservationsForWorkspace(res, domain.SortReservationsStayDesc)
	if res[0].ArrivalDate.Day() != 20 {
		t.Fatalf("expected latest arrival first for stay_desc")
	}
}
