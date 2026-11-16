package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/channels"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
)

func newFollowingClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{Jar: jar}
}

func postWebForm(t *testing.T, client *http.Client, targetURL string, form url.Values) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, targetURL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new POST %s: %v", targetURL, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", targetURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST %s: %v", targetURL, err)
	}
	return resp, string(raw)
}

func getWebPage(t *testing.T, client *http.Client, targetURL string) (*http.Response, string) {
	t.Helper()
	resp, err := client.Get(targetURL)
	if err != nil {
		t.Fatalf("GET %s: %v", targetURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET %s: %v", targetURL, err)
	}
	return resp, string(raw)
}

func TestPWE2_UserPersonalisationIsolationSecurityAndRecovery(t *testing.T) {
	srv, migratorStore, appSvc, propA, _, _ := setupPhase4AppServer(t, "pwe2-suite", channels.AdapterConfig{})
	ctx := context.Background()

	// Locate seeded owner and create second property + two distinct staff/manager users.
	var ownerID uuid.UUID
	if err := migratorStore.Pool().QueryRow(ctx, `SELECT id FROM admin_users WHERE email = 'owner@tirbach.wales'`).Scan(&ownerID); err != nil {
		t.Fatalf("find owner: %v", err)
	}
	ownerActor := service.Actor{UserID: ownerID, PropertyID: nil, Email: "owner@tirbach.wales", Role: domain.RoleOwnerAdmin}

	// Create Property B with its own unit type + channel to prove cross-property isolation.
	propBID := uuid.New()
	utBID := uuid.New()
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO properties (id, slug, name, region, timezone, check_in_time, check_out_time, currency)
		VALUES ($1, 'coastal-haven-two', 'Coastal Haven Two', 'Ceredigion', 'Europe/London', '15:00', '10:00', 'GBP')
	`, propBID); err != nil {
		t.Fatalf("insert property B: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO unit_types (id, property_id, code, name, category, base_capacity_adults, max_adults, base_nightly_rate_pence)
		VALUES ($1, $2, 'pod-b', 'Property B Luxury Pod', 'glamping', 2, 2, 12000)
	`, utBID, propBID); err != nil {
		t.Fatalf("insert unit_type B: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO booking_channels (id, property_id, code, name, category, is_active, sort_order)
		VALUES ($1, $2, 'PROP_B_CH', 'Property B Direct', 'direct', TRUE, 10)
	`, uuid.New(), propBID); err != nil {
		t.Fatalf("insert channel B: %v", err)
	}

	// Create User A (Manager on Prop A), User B (Staff on Prop A), User C (must_rotate_password = TRUE on Prop A).
	userA, err := appSvc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		PropertyID: &propA.ID,
		Email:      "pwe2.usera@tirbach.wales",
		FullName:   "PWE2 Manager A",
		Role:       domain.RoleManager,
		Password:   "Pwe2ManagerPass!2026",
	})
	if err != nil {
		t.Fatalf("create user A: %v", err)
	}
	userB, err := appSvc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		PropertyID: &propA.ID,
		Email:      "pwe2.userb@tirbach.wales",
		FullName:   "PWE2 Staff B",
		Role:       domain.RoleStaff,
		Password:   "Pwe2StaffPass!2026",
	})
	if err != nil {
		t.Fatalf("create user B: %v", err)
	}
	userRot, err := appSvc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		PropertyID:         &propA.ID,
		Email:              "pwe2.rotate@tirbach.wales",
		FullName:           "PWE2 Rotate User",
		Role:               domain.RoleManager,
		Password:           "TempRotatePass!2026",
		MustRotatePassword: true,
	})
	if err != nil {
		t.Fatalf("create rotate user: %v", err)
	}

	clientA := newFollowingClient(t)
	statusA, csrfA := apiLogin(t, clientA, srv.URL, userA.Email, "Pwe2ManagerPass!2026")
	if statusA != http.StatusOK {
		t.Fatalf("userA login status = %d", statusA)
	}
	clientB := newFollowingClient(t)
	statusB, csrfB := apiLogin(t, clientB, srv.URL, userB.Email, "Pwe2StaffPass!2026")
	if statusB != http.StatusOK {
		t.Fatalf("userB login status = %d", statusB)
	}

	// 1. User A updates global preferences via /account/preferences/global (CSRF protected).
	respNoCSRF, _ := postWebForm(t, clientA, srv.URL+"/account/preferences/global", url.Values{
		"csrf_token": {"invalid-token"},
		"theme_mode": {"dark"},
	})
	if respNoCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for invalid CSRF token, got %d", respNoCSRF.StatusCode)
	}

	// Unauthenticated client redirected to /login.
	anonClient := newFollowingClient(t)
	respAnon, _ := getWebPage(t, anonClient, srv.URL+"/account")
	if respAnon.Request.URL.Path != "/login" {
		t.Fatalf("expected unauthenticated /account to redirect to /login, landed on %s", respAnon.Request.URL.Path)
	}

	formGlobal := url.Values{
		"csrf_token":         {csrfA},
		"theme_mode":         {"dark"},
		"accent":             {"brass"},
		"density":            {"compact"},
		"text_scale":         {"xl"},
		"high_contrast":      {"1"},
		"reduced_motion":     {"1"},
		"default_route":      {"/calendar"},
		"shortcuts":          {"/calendar", "/guests", "/reports"},
		"collapsed_sections": {"inventory"},
		"calendar_horizon":   {"14d"},
		"calendar_grouping":  {"unit_type"},
		"table_page_size":    {"10"},
	}
	respSaveA, bodySaveA := postWebForm(t, clientA, srv.URL+"/account/preferences/global", formGlobal)
	if respSaveA.StatusCode != http.StatusOK {
		t.Fatalf("save global preferences status=%d", respSaveA.StatusCode)
	}
	for _, want := range []string{
		`data-theme="dark"`,
		`data-accent="brass"`,
		`data-contrast="high"`,
		`data-motion="reduced"`,
		`data-density="compact"`,
		`data-text-scale="xl"`,
		`Pinned Shortcuts`,
		`Global personalisation preferences saved`,
	} {
		if !strings.Contains(bodySaveA, want) {
			t.Errorf("User A rendered account page missing %q", want)
		}
	}

	// Verify User B (staff -> Reception profile) on the same property remains on clean defaults without User A overrides (strict per-user isolation).
	respGetB, bodyGetB := getWebPage(t, clientB, srv.URL+"/account")
	if respGetB.StatusCode != http.StatusOK {
		t.Fatalf("GET /account user B status=%d", respGetB.StatusCode)
	}
	if !strings.Contains(bodyGetB, `data-theme="system" data-accent="teal" data-contrast="standard" data-motion="system" data-density="compact" data-text-scale="default"`) {
		t.Errorf("User B leaked User A theme tokens! HTML excerpt: %.300s", bodyGetB)
	}
	if strings.Contains(bodyGetB, `href="/reports">Reports</a>`) || strings.Contains(bodyGetB, `data-accent="brass"`) || strings.Contains(bodyGetB, `data-theme="dark"`) {
		t.Errorf("User B must not see User A pinned shortcuts or dark/brass overrides")
	}

	// Verify User A cannot read or modify User B's preferences via service or HTTP tampering.
	actorA := service.Actor{UserID: userA.ID, PropertyID: &propA.ID, Email: userA.Email, Role: domain.RoleManager}
	actorB := service.Actor{UserID: userB.ID, PropertyID: &propA.ID, Email: userB.Email, Role: domain.RoleStaff}
	if _, _, err := appSvc.GetUserGlobalPreferences(ctx, actorA, userB.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden reading User B prefs as User A, got %v", err)
	}
	if _, err := appSvc.UpdateUserGlobalPreferences(ctx, actorA, userB.ID, domain.DefaultUserPreferences(userB.ID)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden updating User B prefs as User A, got %v", err)
	}
	respTamper, _ := postWebForm(t, clientA, srv.URL+"/account/preferences/global", url.Values{
		"csrf_token": {csrfA},
		"user_id":    {userB.ID.String()},
		"theme_mode": {"light"},
	})
	if respTamper.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when POSTing user_id belonging to another user, got %d", respTamper.StatusCode)
	}

	// Verify User A logging in via web form POST /login lands on permitted DefaultRoute (/calendar).
	webClientA := newClient(t)
	respWebLogin, _ := postWebForm(t, webClientA, srv.URL+"/login", url.Values{
		"email":    {userA.Email},
		"password": {"Pwe2ManagerPass!2026"},
	})
	if respWebLogin.StatusCode != http.StatusSeeOther || respWebLogin.Header.Get("Location") != "/calendar" {
		t.Fatalf("expected web login redirect to /calendar, got status=%d loc=%q", respWebLogin.StatusCode, respWebLogin.Header.Get("Location"))
	}

	// 2. Password rotation gate: UserRot sees /account rotation form with personalisation locked,
	// cannot post preference updates to bypass rotation, and unlocks after changing password.
	clientRot := newFollowingClient(t)
	_, _ = postWebForm(t, clientRot, srv.URL+"/login", url.Values{
		"email":    {userRot.Email},
		"password": {"TempRotatePass!2026"},
	})
	respRotAcct, bodyRotAcct := getWebPage(t, clientRot, srv.URL+"/account")
	if respRotAcct.StatusCode != http.StatusOK {
		t.Fatalf("GET /account while must_rotate_password=true failed with status %d", respRotAcct.StatusCode)
	}
	if !strings.Contains(bodyRotAcct, "Set Your New Personal Password") || strings.Contains(bodyRotAcct, "/account/preferences/global") {
		t.Errorf("expected password rotation UI with personalisation disabled until rotation")
	}
	// Extract CSRF token from rendered /account page and verify HTTP POST to /account/preferences/global is blocked by rotation gate,
	// and unlocks cleanly once temporary password is rotated.
	m := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(bodyRotAcct)
	if len(m) != 2 {
		t.Fatalf("expected csrf_token input in /account page")
	}
	rotCSRF := m[1]
	rotNoFollow := &http.Client{
		Jar: clientRot.Jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	respRotPref, _ := postWebForm(t, rotNoFollow, srv.URL+"/account/preferences/global", url.Values{
		"csrf_token": {rotCSRF},
		"theme_mode": {"dark"},
	})
	if respRotPref.StatusCode != http.StatusSeeOther || respRotPref.Header.Get("Location") != "/account?rotate=1" {
		t.Fatalf("expected password rotation gate to redirect /account/preferences/global to /account?rotate=1, got status=%d loc=%q", respRotPref.StatusCode, respRotPref.Header.Get("Location"))
	}
	respRotPass, bodyRotPass := postWebForm(t, clientRot, srv.URL+"/account/password", url.Values{
		"csrf_token":       {rotCSRF},
		"current_password": {"TempRotatePass!2026"},
		"new_password":     {"RotatedPersonalPass!2026"},
	})
	if respRotPass.StatusCode != http.StatusOK || !strings.Contains(bodyRotPass, "Password updated and prior active sessions revoked") || !strings.Contains(bodyRotPass, "Save Global Preferences") {
		t.Fatalf("expected /account after password rotation to unlock personalisation, got status=%d", respRotPass.StatusCode)
	}
	m2 := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(bodyRotPass)
	if len(m2) != 2 {
		t.Fatalf("expected new csrf_token after password rotation")
	}
	respRotSave, bodyRotSave := postWebForm(t, clientRot, srv.URL+"/account/preferences/global", url.Values{
		"csrf_token":    {m2[1]},
		"theme_mode":    {"light"},
		"accent":        {"olive"},
		"density":       {"comfortable"},
		"text_scale":    {"default"},
		"default_route": {"/"},
	})
	if respRotSave.StatusCode != http.StatusOK || !strings.Contains(bodyRotSave, "Global personalisation preferences saved") {
		t.Fatalf("expected preference save to succeed after password rotation, status=%d", respRotSave.StatusCode)
	}

	// 3. Property-scoped workspace preferences & saved operational views + cross-property rejection.
	respSaveWS, bodySaveWS := postWebForm(t, clientA, srv.URL+"/account/preferences/workspace", url.Values{
		"csrf_token":        {csrfA},
		"property_id":       {propA.ID.String()},
		"calendar_horizon":  {"30d"},
		"calendar_grouping": {"unit_type"},
		"table_page_size":   {"10"},
		"reservations_sort": {"stay_desc"},
		"guests_sort":       {"stays_desc"},
	})
	if respSaveWS.StatusCode != http.StatusOK || !strings.Contains(bodySaveWS, "Property workspace preferences saved") {
		t.Fatalf("expected property workspace save success, got status=%d", respSaveWS.StatusCode)
	}

	// Valid same-property saved operational view succeeds.
	utsA, err := migratorStore.ListUnitTypes(ctx, propA.ID, false)
	if err != nil || len(utsA) == 0 {
		t.Fatalf("list Property A unit types: %v", err)
	}
	respViewSave, bodyViewSave := postWebForm(t, clientA, srv.URL+"/account/preferences/views", url.Values{
		"csrf_token":   {csrfA},
		"property_id":  {propA.ID.String()},
		"screen":       {"calendar"},
		"return_to":    {"/calendar"},
		"name":         {"Unit Type A Horizon"},
		"horizon":      {"14d"},
		"grouping":     {"unit_type"},
		"unit_type_id": {utsA[0].ID.String()},
		"channel_code": {"DIRECT"},
	})
	if respViewSave.StatusCode != http.StatusOK || !strings.Contains(bodyViewSave, "Unit Type A Horizon") {
		t.Fatalf("expected saved operational view on /calendar, status=%d", respViewSave.StatusCode)
	}
	// Verify /calendar applies workspace defaults & saved view link works cleanly.
	respCalDefault, bodyCalDefault := getWebPage(t, clientA, srv.URL+"/calendar")
	if respCalDefault.StatusCode != http.StatusOK || !strings.Contains(bodyCalDefault, "Unit Type A Horizon") {
		t.Fatalf("expected /calendar workspace default & saved view pill, status=%d", respCalDefault.StatusCode)
	}
	// Verify /reservations and /guests saved view create + apply + delete + workspace defaults.
	respResViewSave, bodyResViewSave := postWebForm(t, clientA, srv.URL+"/account/preferences/views", url.Values{
		"csrf_token":   {csrfA},
		"property_id":  {propA.ID.String()},
		"screen":       {"reservations"},
		"return_to":    {"/reservations"},
		"name":         {"Confirmed Direct"},
		"status":       {"CONFIRMED"},
		"channel_code": {"DIRECT"},
	})
	if respResViewSave.StatusCode != http.StatusOK || !strings.Contains(bodyResViewSave, "Confirmed Direct") {
		t.Fatalf("expected saved view on /reservations, status=%d", respResViewSave.StatusCode)
	}
	wsAfterViews, _, err := appSvc.GetUserPropertyWorkspace(ctx, actorA, userA.ID, propA.ID)
	if err != nil || len(wsAfterViews.SavedViews) < 2 {
		t.Fatalf("expected 2 saved views on workspace, got %d err=%v", len(wsAfterViews.SavedViews), err)
	}
	resViewID := wsAfterViews.SavedViews[1].ID
	respResApplied, bodyResApplied := getWebPage(t, clientA, srv.URL+"/reservations?saved_view="+resViewID)
	if respResApplied.StatusCode != http.StatusOK || !strings.Contains(bodyResApplied, `option value="CONFIRMED" selected`) {
		t.Fatalf("expected saved view applied on /reservations, status=%d", respResApplied.StatusCode)
	}
	respDelView, bodyDelView := postWebForm(t, clientA, srv.URL+"/account/preferences/views/"+resViewID+"/delete", url.Values{
		"csrf_token":  {csrfA},
		"property_id": {propA.ID.String()},
		"return_to":   {"/account"},
	})
	if respDelView.StatusCode != http.StatusOK || strings.Contains(bodyDelView, "Confirmed Direct") {
		t.Fatalf("expected saved view deleted cleanly, status=%d", respDelView.StatusCode)
	}
	respGuestsDefault, bodyGuestsDefault := getWebPage(t, clientA, srv.URL+"/guests")
	if respGuestsDefault.StatusCode != http.StatusOK || !strings.Contains(bodyGuestsDefault, "50 / page") {
		t.Fatalf("expected /guests workspace default page size 50, status=%d", respGuestsDefault.StatusCode)
	}

	// Cross-property unit_type_id from Property B MUST be rejected on save!
	if _, err := appSvc.SaveOperationalView(ctx, actorA, userA.ID, propA.ID, domain.SavedOperationalView{
		Name:   "Cross Prop Attack",
		Screen: domain.SavedViewScreenCalendar,
		Filters: map[string]string{
			"unit_type_id": utBID.String(),
		},
	}); err == nil {
		t.Fatalf("expected ValidationError saving Property B unit_type_id in Property A view")
	}

	// User A (scoped to Property A) cannot write or read Property B workspace!
	if _, _, err := appSvc.GetUserPropertyWorkspace(ctx, actorA, userA.ID, propBID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden reading Property B workspace for Property A user, got %v", err)
	}
	respCrossProp, _ := postWebForm(t, clientA, srv.URL+"/account/preferences/workspace", url.Values{
		"csrf_token":       {csrfA},
		"property_id":      {propBID.String()},
		"calendar_horizon": {"7d"},
	})
	if respCrossProp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden saving workspace for Property B, got %d", respCrossProp.StatusCode)
	}

	// 4. Role downgrade in DB immediately filters shortcuts, landing route, and saved views at render time,
	// while backend route still enforces 403 Forbidden.
	if _, err := migratorStore.Pool().Exec(ctx, `UPDATE admin_users SET role = 'housekeeping_staff' WHERE id = $1`, userA.ID); err != nil {
		t.Fatalf("downgrade role: %v", err)
	}
	// ResolveWorkspace with stale actor role reads live DB role -> hides /reports & /calendar, resets landing to /.
	downgradedWS, err := appSvc.ResolveWorkspace(ctx, actorA, &propA.ID)
	if err != nil {
		t.Fatalf("ResolveWorkspace after role change: %v", err)
	}
	if downgradedWS.DefaultRoute != "/" {
		t.Fatalf("expected landing route fallback to /, got %q", downgradedWS.DefaultRoute)
	}
	if len(downgradedWS.Shortcuts) != 0 || len(downgradedWS.SavedViews) != 0 {
		t.Fatalf("expected /calendar, /guests, /reports shortcuts and calendar saved view filtered for housekeeping_staff; got shortcuts=%v views=%v", downgradedWS.Shortcuts, downgradedWS.SavedViews)
	}
	respReports, _ := getWebPage(t, clientA, srv.URL+"/reports")
	if respReports.StatusCode != http.StatusForbidden {
		t.Fatalf("expected /reports to still enforce 403 Forbidden after downgrade, got %d", respReports.StatusCode)
	}

	// Restore role to manager for next checks.
	if _, err := migratorStore.Pool().Exec(ctx, `UPDATE admin_users SET role = 'manager' WHERE id = $1`, userA.ID); err != nil {
		t.Fatalf("restore role: %v", err)
	}

	// 5. Stale last_property_id hint & Property B workspace row when user loses access to Property B:
	// Owner writes global last_property_id = PropB + PropB workspace, then becomes scoped to PropA only.
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO admin_user_preferences (user_id, last_property_id, default_route)
		VALUES ($1, $2, '/calendar')
		ON CONFLICT (user_id) DO UPDATE SET last_property_id = EXCLUDED.last_property_id
	`, ownerID, propBID); err != nil {
		t.Fatalf("seed owner last_property_id: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO admin_user_property_workspaces (user_id, property_id, preferences, saved_views)
		VALUES ($1, $2, '{"calendar_horizon":"7d"}'::jsonb, $3::jsonb)
	`, ownerID, propBID, `[{"id":"b1","name":"Secret Prop B View","screen":"calendar","filters":{"unit_type_id":"`+utBID.String()+`"}}]`); err != nil {
		t.Fatalf("seed owner prop B workspace: %v", err)
	}
	ownerPropAActor := service.Actor{UserID: ownerID, PropertyID: &propA.ID, Email: "owner@tirbach.wales", Role: domain.RoleOwnerAdmin}
	if _, err := migratorStore.Pool().Exec(ctx, `UPDATE admin_users SET property_id = $2 WHERE id = $1`, ownerID, propA.ID); err != nil {
		t.Fatalf("restrict owner property_id in DB: %v", err)
	}
	// Resolve against active PropA -> PropB hint discarded, PropB workspace never leaked!
	resolvedRestricted, err := appSvc.ResolveWorkspace(ctx, ownerPropAActor, &propBID)
	if err != nil {
		t.Fatalf("ResolveWorkspace with revoked propB: %v", err)
	}
	if resolvedRestricted.ActivePropertyID != nil || resolvedRestricted.Global.LastPropertyID != nil || len(resolvedRestricted.SavedViews) != 0 {
		t.Fatalf("expected revoked Prop B state completely discarded, got %+v", resolvedRestricted)
	}
	// Restore owner unrestricted property_id.
	if _, err := migratorStore.Pool().Exec(ctx, `UPDATE admin_users SET property_id = NULL WHERE id = $1`, ownerID); err != nil {
		t.Fatalf("restore owner property_id: %v", err)
	}

	// 6. Malformed stored JSONB in DB recovers field-by-field without locking out SSR.
	if _, err := migratorStore.Pool().Exec(ctx, `
		UPDATE admin_user_preferences
		SET schema_version = 42,
		    default_route = 'https://evil.example/xss',
		    preferences = '{"calendar_horizon":"century","shortcuts":["/guests","http://bad"],"table_page_size":7777}'::jsonb
		WHERE user_id = $1
	`, userA.ID); err != nil {
		t.Fatalf("inject malformed preference row: %v", err)
	}
	malformedViews, _ := json.Marshal([]domain.SavedOperationalView{
		{ID: "bad-screen", Name: "Corrupt", Screen: "dropped_screen", Filters: map[string]string{}},
		{ID: "cross-ref", Name: "Stale Prop Ref", Screen: domain.SavedViewScreenCalendar, Filters: map[string]string{"unit_type_id": utBID.String(), "status": "BOOKED"}},
	})
	if _, err := migratorStore.Pool().Exec(ctx, `
		UPDATE admin_user_property_workspaces
		SET saved_views = $3::jsonb
		WHERE user_id = $1 AND property_id = $2
	`, userA.ID, propA.ID, string(malformedViews)); err != nil {
		t.Fatalf("inject malformed saved views: %v", err)
	}
	respRecover, bodyRecover := getWebPage(t, clientA, srv.URL+"/account")
	if respRecover.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK even with corrupt stored JSONB, got %d", respRecover.StatusCode)
	}
	if !strings.Contains(bodyRecover, "Preference recovery notices") || !strings.Contains(bodyRecover, `data-theme="dark"`) || !strings.Contains(bodyRecover, `<a href="/guests">Guest Directory</a>`) {
		t.Errorf("expected malformed recovery notices while preserving valid theme/shortcut siblings")
	}

	// Reset User A global and property workspace cleanly via POST.
	respResetWS, bodyResetWS := postWebForm(t, clientA, srv.URL+"/account/preferences/workspace/reset", url.Values{
		"csrf_token":  {csrfA},
		"property_id": {propA.ID.String()},
	})
	if respResetWS.StatusCode != http.StatusOK || !strings.Contains(bodyResetWS, "Property workspace preferences and saved views reset") {
		t.Fatalf("expected workspace reset confirmation, got status=%d", respResetWS.StatusCode)
	}
	respResetGlobal, bodyResetGlobal := postWebForm(t, clientA, srv.URL+"/account/preferences/global/reset", url.Values{
		"csrf_token": {csrfA},
	})
	if respResetGlobal.StatusCode != http.StatusOK || !strings.Contains(bodyResetGlobal, `data-theme="system"`) {
		t.Fatalf("expected global preferences reset to system theme, got status=%d", respResetGlobal.StatusCode)
	}

	// 7. Soft-deactivation retains preference rows cleanly, while hard-delete cascades cleanly.
	if _, err := appSvc.UpdateUserGlobalPreferences(ctx, actorB, userB.ID, domain.DefaultUserPreferences(userB.ID)); err != nil {
		t.Fatalf("seed user B global pref: %v", err)
	}
	if _, err := appSvc.UpdateUserPropertyWorkspace(ctx, actorB, userB.ID, propA.ID, domain.PropertyWorkspacePayload{CalendarHorizon: domain.Horizon7Days}); err != nil {
		t.Fatalf("seed user B workspace: %v", err)
	}
	if _, err := appSvc.UpdateUser(ctx, ownerActor, service.UpdateUserInput{
		ID: userB.ID, FullName: userB.FullName, Role: userB.Role, IsActive: false, Version: userB.Version,
	}); err != nil {
		t.Fatalf("soft deactivate user B: %v", err)
	}
	var retainedCount int
	if err := migratorStore.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM admin_user_preferences WHERE user_id = $1`, userB.ID).Scan(&retainedCount); err != nil || retainedCount != 1 {
		t.Fatalf("expected preference row retained across soft deactivation, count=%d err=%v", retainedCount, err)
	}
	// Hard delete a user without append-only audit_logs actor FKs -> ON DELETE CASCADE leaves zero orphaned preference/workspace rows.
	userC, err := appSvc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		PropertyID: &propA.ID,
		Email:      "cascade-user@tirbach.wales",
		FullName:   "Cascade Delete User",
		Role:       domain.RoleStaff,
		Password:   "Cascade-Passw0rd!2026",
	})
	if err != nil {
		t.Fatalf("create user C for cascade check: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `INSERT INTO admin_user_preferences (user_id, default_route) VALUES ($1, '/')`, userC.ID); err != nil {
		t.Fatalf("seed user C global pref: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `INSERT INTO admin_user_property_workspaces (user_id, property_id) VALUES ($1, $2)`, userC.ID, propA.ID); err != nil {
		t.Fatalf("seed user C workspace: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `DELETE FROM admin_users WHERE id = $1`, userC.ID); err != nil {
		t.Fatalf("hard delete user C: %v", err)
	}
	var orphanGlobal, orphanWS int
	_ = migratorStore.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM admin_user_preferences WHERE user_id = $1`, userC.ID).Scan(&orphanGlobal)
	_ = migratorStore.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM admin_user_property_workspaces WHERE user_id = $1`, userC.ID).Scan(&orphanWS)
	if orphanGlobal != 0 || orphanWS != 0 {
		t.Fatalf("expected zero orphaned preference rows after hard delete, global=%d ws=%d", orphanGlobal, orphanWS)
	}

	// 8. Public API stays completely separate with zero personalisation exposure.
	respPub, bodyPub := getWebPage(t, anonClient, srv.URL+"/api/public/v1/properties/"+propA.Slug)
	if respPub.StatusCode == http.StatusOK && (strings.Contains(bodyPub, "saved_views") || strings.Contains(bodyPub, "collapsed_sections")) {
		t.Errorf("public API leaked admin personalisation fields: %s", bodyPub)
	}
	_ = csrfB
}
