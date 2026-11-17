package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/branding"
	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/httpx"
	"github.com/tirbach/tir-bach/internal/service"
)

func TestNewParsesAllEmbeddedTemplates(t *testing.T) {
	h, err := New(nil, config.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if h.loginTmpl == nil || h.errorTmpl == nil {
		t.Fatalf("expected compiled loginTmpl and errorTmpl")
	}
	if len(h.pages) == 0 {
		t.Fatalf("expected compiled page templates")
	}
}

func TestAllTemplatesAndCSSRespectCSPRules(t *testing.T) {
	entries, err := fs.Glob(embeddedFS, "templates/*.html")
	if err != nil || len(entries) == 0 {
		t.Fatalf("failed to glob templates: %v", err)
	}

	inlineStyleAttr := regexp.MustCompile(`(?i)\bstyle\s*=`)
	styleTag := regexp.MustCompile(`(?i)<style\b`)
	scriptTag := regexp.MustCompile(`(?i)<script\b[^>]*>`)

	for _, path := range entries {
		raw, err := fs.ReadFile(embeddedFS, path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(raw)
		if inlineStyleAttr.MatchString(text) {
			t.Errorf("%s contains inline style= attribute (violates CSP style-src 'self')", path)
		}
		if styleTag.MatchString(text) {
			t.Errorf("%s contains inline <style> tag (violates CSP style-src 'self')", path)
		}
		for _, match := range scriptTag.FindAllString(text, -1) {
			if !strings.Contains(match, `src="/static/calendar.js"`) {
				t.Errorf("%s contains unexpected <script> tag %q", path, match)
			}
		}
	}

	cssRaw, err := fs.ReadFile(embeddedFS, "static/app.css")
	if err != nil {
		t.Fatalf("read static/app.css: %v", err)
	}
	cssText := string(cssRaw)
	for _, forbidden := range []string{"linear-gradient(", "radial-gradient("} {
		if strings.Contains(cssText, forbidden) {
			t.Errorf("static/app.css should avoid decorative gradients (%s)", forbidden)
		}
	}
	for _, requiredSelector := range []string{
		`:root[data-theme="dark"]`,
		`:root[data-theme="system"]`,
		`:root[data-accent="olive"]`,
		`:root[data-accent="slate"]`,
		`:root[data-accent="brass"]`,
		`:root[data-contrast="high"]`,
		`:root[data-density="compact"]`,
		`:root[data-text-scale="large"]`,
		`:root[data-text-scale="xl"]`,
		`:root[data-motion="reduced"]`,
		`prefers-reduced-motion: reduce`,
		`prefers-color-scheme: dark`,
		`:focus-visible`,
		`.skip-link`,
	} {
		if !strings.Contains(cssText, requiredSelector) {
			t.Errorf("static/app.css missing required PWE-1 token/selector %q", requiredSelector)
		}
	}
}

func TestCSSSemanticTokensWCAGContrast(t *testing.T) {
	cssRaw, err := fs.ReadFile(embeddedFS, "static/app.css")
	if err != nil {
		t.Fatalf("read static/app.css: %v", err)
	}
	css := string(cssRaw)

	extractBlock := func(marker string) map[string]string {
		idx := strings.Index(css, marker)
		if idx < 0 {
			t.Fatalf("CSS marker %q not found", marker)
		}
		open := strings.IndexByte(css[idx:], '{')
		if open < 0 {
			t.Fatalf("missing opening brace after %q", marker)
		}
		depth := 0
		end := -1
		for i := idx + open; i < len(css); i++ {
			switch css[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = i
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			t.Fatalf("unterminated CSS block after %q", marker)
		}
		vars := make(map[string]string)
		re := regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*(#[0-9a-fA-F]{6})\s*;`)
		for _, m := range re.FindAllStringSubmatch(css[idx+open:end], -1) {
			vars[m[1]] = strings.ToUpper(m[2])
		}
		return vars
	}

	assertPairs := func(name string, vars map[string]string, pairs [][2]string, minRatio float64) {
		t.Helper()
		for _, p := range pairs {
			fg, okFG := vars[p[0]]
			bg, okBG := vars[p[1]]
			if !okFG || !okBG {
				t.Errorf("%s: missing hex var for pair (%s=%q, %s=%q)", name, p[0], fg, p[1], bg)
				continue
			}
			r, ok := branding.ContrastRatio(fg, bg)
			if !ok || r < minRatio {
				t.Errorf("%s: contrast(%s=%s on %s=%s) = %.2f < %.2f", name, p[0], fg, p[1], bg, r, minRatio)
			}
		}
	}

	textPairs := [][2]string{
		{"--ink", "--bg"},
		{"--ink", "--surface"},
		{"--ink", "--surface-muted"},
		{"--ink-muted", "--surface"},
		{"--ink-muted", "--surface-muted"},
		{"--accent-ink", "--surface"},
		{"--accent-ink", "--bg"},
		{"--accent-on-solid", "--accent-solid"},
		{"--accent-on-solid", "--accent-solid-hover"},
		{"--sidebar-ink", "--sidebar-bg"},
		{"--sidebar-ink-muted", "--sidebar-bg"},
		{"--sidebar-section-ink", "--sidebar-bg"},
		{"--ok-ink", "--ok-bg"},
		{"--warn-ink", "--warn-bg"},
		{"--err-ink", "--err-bg"},
		{"--info-ink", "--info-bg"},
	}

	lightVars := extractBlock(":root {")
	assertPairs("light-default", lightVars, textPairs, 4.5)

	darkVars := extractBlock(`:root[data-theme="dark"] {`)
	assertPairs("dark-explicit", darkVars, textPairs, 4.5)

	hiLight := extractBlock(`:root[data-contrast="high"] {`)
	for k, v := range lightVars {
		if _, exists := hiLight[k]; !exists {
			hiLight[k] = v
		}
	}
	assertPairs("high-contrast-light", hiLight, textPairs, 7.0)

	hiDark := extractBlock(`:root[data-theme="dark"][data-contrast="high"] {`)
	for k, v := range darkVars {
		if _, exists := hiDark[k]; !exists {
			hiDark[k] = v
		}
	}
	assertPairs("high-contrast-dark", hiDark, textPairs, 7.0)
}

func TestThemeAttributeContractCSPAndStaticServing(t *testing.T) {
	mux := http.NewServeMux()
	h, err := NewWithTheme(nil, config.Config{}, branding.ThemeSelection{
		Mode:      "dark",
		Accent:    "slate",
		Contrast:  "high",
		Motion:    "reduced",
		Density:   "compact",
		TextScale: "xl",
	})
	if err != nil {
		t.Fatalf("NewWithTheme() error = %v", err)
	}
	h.Register(mux)
	wrapped := httpx.SecurityHeaders(mux)

	// Verify /static/app.css serves same-origin CSS with CSP headers.
	cssReq := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
	cssRec := httptest.NewRecorder()
	wrapped.ServeHTTP(cssRec, cssReq)
	if cssRec.Code != http.StatusOK {
		t.Fatalf("GET /static/app.css status = %d, want 200", cssRec.Code)
	}
	csp := cssRec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "style-src 'self'") || !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("unexpected CSP header: %q", csp)
	}

	// Verify GET /login emits validated attributes and CSP-safe HTML.
	loginReq := httptest.NewRequest(http.MethodGet, "/login", nil)
	loginRec := httptest.NewRecorder()
	wrapped.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("GET /login status = %d, want 200", loginRec.Code)
	}
	body := loginRec.Body.String()
	for _, want := range []string{
		`data-theme="dark"`,
		`data-accent="slate"`,
		`data-contrast="high"`,
		`data-motion="reduced"`,
		`data-density="compact"`,
		`data-text-scale="xl"`,
		`href="/static/app.css"`,
		`<main class="login-page">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /login HTML missing %q", want)
		}
	}
	if strings.Contains(body, `style="`) || strings.Contains(body, `<style`) {
		t.Fatalf("GET /login emitted inline style violating CSP")
	}

	// Verify unsafe theme input falls back cleanly and error template uses status_badge + alert landmark.
	fallbackHandler, err := NewWithTheme(nil, config.Config{}, branding.ThemeSelection{
		Mode:      `light"><script>alert(1)</script>`,
		Accent:    "invalid",
		Contrast:  "invalid",
		Motion:    "invalid",
		Density:   "invalid",
		TextScale: "invalid",
	})
	if err != nil {
		t.Fatalf("NewWithTheme fallback error = %v", err)
	}
	errRec := httptest.NewRecorder()
	fallbackHandler.renderError(errRec, http.StatusForbidden, "Access Denied", "Forbidden.")
	if errRec.Code != http.StatusForbidden {
		t.Fatalf("renderError status = %d, want 403", errRec.Code)
	}
	errHTML := errRec.Body.String()
	for _, want := range []string{
		`data-theme="system"`,
		`data-accent="teal"`,
		`data-contrast="standard"`,
		`data-motion="system"`,
		`data-density="comfortable"`,
		`data-text-scale="default"`,
		`role="alert"`,
		`HTTP 403`,
	} {
		if !strings.Contains(errHTML, want) {
			t.Errorf("renderError HTML missing %q", want)
		}
	}
}

func TestRepresentativeTemplatesRenderAccessibleChromeAndPartials(t *testing.T) {
	h, err := New(nil, config.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	actor := service.Actor{
		UserID:   uuid.New(),
		FullName: "Owain Griffiths",
		Email:    "owain@example.com",
		Role:     domain.RoleOwnerAdmin,
	}
	base := baseView{
		Title:                "Test View",
		ActiveNav:            "reservations",
		Brand:                h.brand,
		Theme:                h.theme,
		Actor:                actor,
		CSRFToken:            "csrf-token",
		Property:             domain.Property{ID: uuid.New(), Name: "Lovelace Meadows", Region: "Pembrokeshire", CheckInTime: "15:00", CheckOutTime: "10:00", Timezone: "Europe/London"},
		CanReadReservations:  true,
		CanWriteReservations: true,
		CanReadGuests:        true,
		CanReadReports:       true,
	}

	// Render reservations empty state via layout + partials.
	resRec := httptest.NewRecorder()
	h.renderPage(resRec, "reservations.html", struct {
		baseView
		Reservations      []domain.Reservation
		Channels          []domain.BookingChannel
		Statuses          []domain.ReservationStatus
		FilterStatus      string
		FilterChannelCode string
		FilterStartDate   string
		FilterEndDate     string
	}{baseView: base})
	if resRec.Code != http.StatusOK {
		t.Fatalf("reservations render status = %d", resRec.Code)
	}
	resHTML := resRec.Body.String()
	for _, want := range []string{
		`<a href="#main-content" class="skip-link">Skip to main content</a>`,
		`<aside class="sidebar" aria-label="Administrative sidebar">`,
		`<nav class="nav" aria-label="Administrative navigation">`,
		`aria-current="page"`,
		`<main id="main-content" class="main" tabindex="-1">`,
		`<div class="empty-state" role="status">`,
		`No matching reservations`,
	} {
		if !strings.Contains(resHTML, want) {
			t.Errorf("reservations.html rendered output missing %q", want)
		}
	}

	// Render calendar grid and guests list with table scope and status_badge glyphs.
	calBase := base
	calBase.ActiveNav = "calendar"
	day := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	calResID := uuid.New()
	calRec := httptest.NewRecorder()
	h.renderPage(calRec, "calendar.html", struct {
		baseView
		Calendar          service.CalendarViewData
		PrevStart         string
		NextStart         string
		FilterUnitTypeID  string
		FilterChannelCode string
		FilterStatus      string
	}{
		baseView: calBase,
		Calendar: service.CalendarViewData{
			View:      "week",
			StartDate: day,
			EndDate:   day.AddDate(0, 0, 7),
			Dates:     []time.Time{day},
			Units: []service.CalendarUnitRow{
				{
					Unit: domain.Unit{ID: uuid.New(), Code: "BT-01", Name: "Hedgerow Bell Tent", UnitTypeName: "Bell Tent"},
					Cells: []service.CalendarDayCell{
						{Date: "2026-07-01", Status: "BOOKED", ReservationID: &calResID, Reference: "TB-001", GuestName: "Jones"},
					},
				},
			},
		},
	})
	if calRec.Code != http.StatusOK {
		t.Fatalf("calendar render status = %d", calRec.Code)
	}
	calHTML := calRec.Body.String()
	if !strings.Contains(calHTML, `<th scope="col" class="cal-unit-col">`) || !strings.Contains(calHTML, `<th scope="row" class="cal-unit-col">`) {
		t.Errorf("calendar.html missing semantic <th scope> table headers")
	}

	guestBase := base
	guestBase.ActiveNav = "guests"
	guestRec := httptest.NewRecorder()
	h.renderPage(guestRec, "guests_list.html", struct {
		baseView
		Guests         []domain.GuestListItem
		Filter         domain.GuestDirectoryFilter
		DuplicateCount int
	}{
		baseView: guestBase,
		Guests: []domain.GuestListItem{
			{Guest: domain.Guest{ID: uuid.New(), FullName: "Carys Evans"}, IsReturningGuest: true, MarketingConsentState: "GRANTED"},
		},
	})
	if guestRec.Code != http.StatusOK {
		t.Fatalf("guests_list render status = %d", guestRec.Code)
	}
	guestHTML := guestRec.Body.String()
	if !strings.Contains(guestHTML, `aria-hidden="true">✓</span> Returning Guest`) {
		t.Errorf("guests_list.html missing non-colour status_badge indicator glyph")
	}

	acctBase := base
	acctBase.ActiveNav = "account"
	acctRec := httptest.NewRecorder()
	h.renderPage(acctRec, "account.html", acctBase)
	if acctRec.Code != http.StatusOK || !strings.Contains(acctRec.Body.String(), `id="new-password-hint"`) {
		t.Errorf("account.html missing expected form hint rendering")
	}

	dashBase := base
	dashBase.ActiveNav = "dashboard"
	dashRec := httptest.NewRecorder()
	h.renderPage(dashRec, "dashboard.html", struct {
		baseView
		Ops          domain.OperationsCentreData
		DateStr      string
		PrevDateStr  string
		NextDateStr  string
		TodayDateStr string
		ReturnURL    string
	}{
		baseView: dashBase,
		Ops: domain.OperationsCentreData{
			IsToday:        true,
			LocalTimeLabel: "09:15 GMT",
			Focus:          "all",
		},
		DateStr:      "2026-07-01",
		PrevDateStr:  "2026-06-30",
		NextDateStr:  "2026-07-02",
		TodayDateStr: "2026-07-01",
		ReturnURL:    "/",
	})
	if dashRec.Code != http.StatusOK {
		t.Fatalf("dashboard render status = %d", dashRec.Code)
	}
	dashHTML := dashRec.Body.String()
	for _, want := range []string{
		`aria-label="Operations view focus"`,
		`aria-hidden="true">✓</span> Today`,
		`aria-current="page"`,
	} {
		if !strings.Contains(dashHTML, want) {
			t.Errorf("dashboard.html missing %q", want)
		}
	}

	commsBase := base
	commsBase.ActiveNav = "comms-settings"
	commsBase.CanReconcileComms = true
	commsRec := httptest.NewRecorder()
	h.renderPage(commsRec, "comms_settings.html", struct {
		baseView
		Templates    []domain.CommunicationTemplate
		Rules        []domain.CommunicationAutomationRule
		Jobs         []domain.CommunicationJob
		FailedEmails []domain.FailedEmailSummary
		Retention    domain.PropertyRetentionPolicy
		Placeholders map[string]string
	}{
		baseView: commsBase,
		FailedEmails: []domain.FailedEmailSummary{
			{OutboxID: uuid.New(), Template: "BOOKING_CONFIRMED", Recipient: "guest@example.com", Subject: "Booking", Status: "DISPATCH_UNKNOWN", Attempts: 1},
		},
	})
	if commsRec.Code != http.StatusOK {
		t.Fatalf("comms_settings render status = %d", commsRec.Code)
	}
	commsHTML := commsRec.Body.String()
	if !strings.Contains(commsHTML, `class="inline-reconcile-form"`) || strings.Contains(commsHTML, `style="`) {
		t.Errorf("comms_settings.html should use inline-reconcile-form class without inline style=")
	}
}
