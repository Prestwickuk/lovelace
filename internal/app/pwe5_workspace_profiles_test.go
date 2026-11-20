package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/branding"
	"github.com/Prestwickuk/lovelace/internal/channels"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/service"
)

func TestPWE5_WorkspaceProfilesRolesSecurityPrecedenceAndResets(t *testing.T) {
	srv, migratorStore, appSvc, propA, _, _ := setupPhase4AppServer(t, "pwe5-suite", channels.AdapterConfig{})
	defer srv.Close()

	ctx := context.Background()
	var ownerID uuid.UUID
	if err := migratorStore.Pool().QueryRow(ctx, `SELECT id FROM admin_users WHERE email = 'owner@example.com'`).Scan(&ownerID); err != nil {
		t.Fatalf("find owner: %v", err)
	}
	globalOwnerActor := service.Actor{UserID: ownerID, PropertyID: nil, Email: "owner@example.com", Role: domain.RoleOwnerAdmin}

	propBID := uuid.New()
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO properties (id, slug, name, region, timezone, check_in_time, check_out_time, currency)
		VALUES ($1, 'pwe5-prop-b', 'Woodland Pines', 'Countryside', 'Europe/London', '15:00', '10:00', 'GBP')
	`, propBID); err != nil {
		t.Fatalf("insert prop B: %v", err)
	}

	mustCreateRoleUser := func(email, fullName string, role domain.Role, propID uuid.UUID, pass string, rotate bool) domain.AdminUser {
		u, err := appSvc.CreateUser(ctx, globalOwnerActor, service.CreateUserInput{
			PropertyID:         &propID,
			Email:              email,
			FullName:           fullName,
			Role:               role,
			Password:           pass,
			MustRotatePassword: rotate,
		})
		if err != nil {
			t.Fatalf("create user %s: %v", email, err)
		}
		return u
	}

	ownerA := mustCreateRoleUser("pwe5.owner@example.com", "PWE5 Owner A", domain.RoleOwnerAdmin, propA.ID, "Pwe5OwnerPass!2026", false)
	mgrA := mustCreateRoleUser("pwe5.mgr@example.com", "PWE5 Manager A", domain.RoleManager, propA.ID, "Pwe5MgrPass!2026", false)
	staffA := mustCreateRoleUser("pwe5.staff@example.com", "PWE5 Staff A", domain.RoleStaff, propA.ID, "Pwe5StaffPass!2026", false)
	hkA := mustCreateRoleUser("pwe5.hk@example.com", "PWE5 Housekeeping A", domain.RoleHousekeepingStaff, propA.ID, "Pwe5HousePass!2026", false)
	maintA := mustCreateRoleUser("pwe5.maint@example.com", "PWE5 Maintenance A", domain.RoleMaintenanceStaff, propA.ID, "Pwe5MaintPass!2026", false)
	viewerA := mustCreateRoleUser("pwe5.viewer@example.com", "PWE5 Viewer A", domain.RoleViewer, propA.ID, "Pwe5ViewerPass!2026", false)
	rotA := mustCreateRoleUser("pwe5.rot@example.com", "PWE5 Rotate A", domain.RoleManager, propA.ID, "Pwe5RotPass!2026", true)
	mustCreateRoleUser("pwe5.mgrb@example.com", "PWE5 Manager B", domain.RoleManager, propBID, "Pwe5MgrBPass!2026", false)

	//Seed via clean domain service methods so turnaround PII exclusion & role widgets are exercised.
	unitsA, err := migratorStore.ListUnits(ctx, propA.ID, nil, false)
	if err != nil || len(unitsA) == 0 {
		t.Fatalf("list Property A units: %v", err)
	}
	guestEmail := "guest.secret.pwe5@example.test"
	guestName := "Dafydd Secretguest"
	todayUTC := time.Now().UTC().Truncate(24 * time.Hour)
	resPWE5, err := appSvc.CreateReservation(ctx, globalOwnerActor, domain.Reservation{
		PropertyID:    propA.ID,
		ChannelCode:   "DIRECT",
		GuestName:     guestName,
		GuestEmail:    guestEmail,
		ArrivalDate:   todayUTC,
		DepartureDate: todayUTC.AddDate(0, 0, 2),
		Adults:        2,
		TotalPence:    12000,
		Currency:      "GBP",
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unitsA[0].ID},
	})
	if err != nil {
		t.Fatalf("create PWE5 reservation: %v", err)
	}
	_ = resPWE5
	cats, err := appSvc.ListMaintenanceCategories(ctx, globalOwnerActor, propA.ID, true)
	if err != nil || len(cats) == 0 {
		t.Fatalf("list maint cats: %v", err)
	}
	maintReq, err := appSvc.CreateMaintenanceRequest(ctx, globalOwnerActor, service.CreateMaintenanceRequestInput{
		PropertyID: propA.ID,
		Title:      "Boiler Pressure Check",
		CategoryID: cats[0].ID,
		Priority:   domain.MaintPriorityHigh,
		UnitID:     &unitsA[0].ID,
	})
	if err != nil {
		t.Fatalf("seed maint req: %v", err)
	}
	if _, err := appSvc.CreateMaintenanceSchedule(ctx, globalOwnerActor, domain.MaintenanceSchedule{
		PropertyID:  propA.ID,
		Title:       "Weekly Fire Point Check",
		CategoryID:  cats[0].ID,
		UnitID:      &unitsA[0].ID,
		Priority:    domain.MaintPriorityHigh,
		Frequency:   domain.MaintFreqWeekly,
		NextDueDate: todayUTC.AddDate(0, 0, 2),
		IsActive:    true,
	}); err != nil {
		t.Fatalf("seed maint schedule: %v", err)
	}
	if _, err := appSvc.CreateHousekeepingTask(
		ctx, globalOwnerActor, propA.ID, unitsA[0].ID,
		domain.TaskTypeChangeoverClean, domain.StageCleaningRequired, domain.TaskPriorityUrgent,
		todayUTC, "Urgent Turnaround Check", "Turnaround task without guest PII", nil,
	); err != nil {
		t.Fatalf("seed housekeeping task: %v", err)
	}

	verifyLoginLanding := func(email, pass, wantLanding string) (*http.Client, string) {
		noFollow := newClient(t)
		reqLogin, _ := http.NewRequest(http.MethodPost, srv.URL+"/login", strings.NewReader(url.Values{
			"email":    {email},
			"password": {pass},
		}.Encode()))
		reqLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		respLogin, err := noFollow.Do(reqLogin)
		if err != nil {
			t.Fatalf("login %s failed: %v", email, err)
		}
		_ = respLogin.Body.Close()
		if respLogin.StatusCode != http.StatusSeeOther || respLogin.Header.Get("Location") != wantLanding {
			t.Fatalf("login %s expected redirect to %s, got status=%d location=%q", email, wantLanding, respLogin.StatusCode, respLogin.Header.Get("Location"))
		}

		cl, csrf := loginAdminClient(t, srv.URL, email, pass)
		return cl, csrf
	}

	clientOwner, _ := verifyLoginLanding(ownerA.Email, "Pwe5OwnerPass!2026", "/")
	clientMgr, csrfMgr := verifyLoginLanding(mgrA.Email, "Pwe5MgrPass!2026", "/")
	clientStaff, csrfStaff := verifyLoginLanding(staffA.Email, "Pwe5StaffPass!2026", "/calendar")
	clientHK, csrfHK := verifyLoginLanding(hkA.Email, "Pwe5HousePass!2026", "/")
	clientMaint, csrfMaint := verifyLoginLanding(maintA.Email, "Pwe5MaintPass!2026", "/maintenance")
	clientViewer, csrfViewer := verifyLoginLanding(viewerA.Email, "Pwe5ViewerPass!2026", "/")

	// 1. Out-of-the-box role profiles: density, widgets, switcher visibility, strict CSP, zero inline CSS/JS, and zero PII leaks.
	_, bodyOwnerWS := getWebPage(t, clientOwner, srv.URL+"/workspace")
	if !strings.Contains(bodyOwnerWS, "Owner / Administrator Profile Default") || !strings.Contains(bodyOwnerWS, `data-density="comfortable"`) || !strings.Contains(bodyOwnerWS, "7/30-Day Performance Snapshot") {
		t.Fatalf("owner /workspace missing Owner / Administrator defaults")
	}
	if strings.Contains(bodyOwnerWS, `name="profile_code"`) {
		t.Fatalf("single-profile owner should not render profile switcher")
	}

	respMgrWS, bodyMgrWS := getWebPage(t, clientMgr, srv.URL+"/workspace")
	csp := respMgrWS.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "style-src 'self'") || !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("expected strict CSP headers, got %q", csp)
	}
	if strings.Contains(bodyMgrWS, " style=") || strings.Contains(bodyMgrWS, "<style") || strings.Contains(bodyMgrWS, "<script>") || strings.Contains(bodyMgrWS, "onclick=") {
		t.Fatalf("detected inline style or inline script in /workspace HTML")
	}
	if !strings.Contains(bodyMgrWS, "Owner / Administrator Profile Default") || !strings.Contains(bodyMgrWS, `name="profile_code"`) || !strings.Contains(bodyMgrWS, "Reception") {
		t.Fatalf("manager /workspace should show Owner default and Reception switcher option")
	}

	_, bodyStaffWS := getWebPage(t, clientStaff, srv.URL+"/workspace")
	if !strings.Contains(bodyStaffWS, "Reception Profile Default") || !strings.Contains(bodyStaffWS, `data-density="compact"`) || !strings.Contains(bodyStaffWS, "Provisional Holds &amp; Inquiries") || strings.Contains(bodyStaffWS, "7/30-Day Performance Snapshot") {
		t.Fatalf("staff /workspace did not resolve Reception compact profile cleanly")
	}
	if strings.Contains(bodyStaffWS, `name="profile_code"`) {
		t.Fatalf("single-profile staff must not see profile switcher")
	}

	_, bodyHKWS := getWebPage(t, clientHK, srv.URL+"/workspace")
	if !strings.Contains(bodyHKWS, "Housekeeping Profile Default") || !strings.Contains(bodyHKWS, "Daily Changeover Queue") || !strings.Contains(bodyHKWS, unitsA[0].Name) {
		t.Fatalf("housekeeping /workspace missing turnaround board")
	}
	if strings.Contains(bodyHKWS, guestName) || strings.Contains(bodyHKWS, guestEmail) || strings.Contains(bodyHKWS, "Arrivals &amp; Departures Today") {
		t.Fatalf("housekeeping /workspace leaked guest PII or arrivals widget")
	}

	_, bodyMaintWS := getWebPage(t, clientMaint, srv.URL+"/workspace")
	if !strings.Contains(bodyMaintWS, "Maintenance Profile Default") || !strings.Contains(bodyMaintWS, maintReq.Request.Reference) || !strings.Contains(bodyMaintWS, "Weekly Fire Point Check") {
		t.Fatalf("maintenance /workspace missing maintenance widgets")
	}

	_, bodyViewerWS := getWebPage(t, clientViewer, srv.URL+"/workspace")
	if !strings.Contains(bodyViewerWS, "Platform Default Layout") || strings.Contains(bodyViewerWS, "Channel Sync Health") || strings.Contains(bodyViewerWS, "name=\"profile_code\"") {
		t.Fatalf("viewer /workspace did not render safe permission-filtered defaults")
	}
	respViewerUsers, _ := getWebPage(t, clientViewer, srv.URL+"/users")
	if respViewerUsers.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for viewer on /users, got %d", respViewerUsers.StatusCode)
	}

	// 2. Manager switches to Reception profile -> updates density/landing/widgets while staying strictly within RBAC.
	respSwitch, bodySwitch := postWebForm(t, clientMgr, srv.URL+"/workspace/profile", url.Values{
		"csrf_token":   {csrfMgr},
		"property_id":  {propA.ID.String()},
		"profile_code": {"reception"},
		"return_to":    {"/workspace"},
	})
	if respSwitch.StatusCode != http.StatusOK || !strings.Contains(bodySwitch, "Reception Profile Default") || !strings.Contains(bodySwitch, `data-density="compact"`) || !strings.Contains(bodySwitch, "Provisional Holds &amp; Inquiries") {
		t.Fatalf("manager switching to reception failed, status=%d", respSwitch.StatusCode)
	}

	// 3. Tampering & cross-role escalation security checks (SSR + API).
	for _, tc := range []struct {
		name   string
		client *http.Client
		csrf   string
		code   string
	}{
		{"staff_escalate_owner", clientStaff, csrfStaff, domain.ProfileOwnerAdmin},
		{"hk_escalate_reception", clientHK, csrfHK, domain.ProfileReception},
		{"maint_escalate_owner", clientMaint, csrfMaint, domain.ProfileOwnerAdmin},
		{"viewer_escalate_owner", clientViewer, csrfViewer, domain.ProfileOwnerAdmin},
	} {
		respSSR, _ := postWebForm(t, tc.client, srv.URL+"/workspace/profile", url.Values{
			"csrf_token":   {tc.csrf},
			"property_id":  {propA.ID.String()},
			"profile_code": {tc.code},
			"return_to":    {"/workspace"},
		})
		if respSSR.StatusCode != http.StatusForbidden {
			t.Fatalf("%s SSR tamper expected 403, got %d", tc.name, respSSR.StatusCode)
		}

		reqAPI, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/workspace/profile", strings.NewReader(`{"profile_code":"`+tc.code+`"}`))
		reqAPI.Header.Set("Content-Type", "application/json")
		reqAPI.Header.Set("X-CSRF-Token", tc.csrf)
		respAPI, err := tc.client.Do(reqAPI)
		if err != nil {
			t.Fatalf("%s API error: %v", tc.name, err)
		}
		_ = respAPI.Body.Close()
		if respAPI.StatusCode != http.StatusForbidden {
			t.Fatalf("%s API tamper expected 403, got %d", tc.name, respAPI.StatusCode)
		}
	}

	// Cross-user tamper + cross-property tamper + unknown profile validation + bad CSRF.
	respCrossUser, _ := postWebForm(t, clientMgr, srv.URL+"/workspace/profile", url.Values{
		"csrf_token":   {csrfMgr},
		"user_id":      {staffA.ID.String()},
		"property_id":  {propA.ID.String()},
		"profile_code": {"owner_admin"},
	})
	if respCrossUser.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-user tamper expected 403, got %d", respCrossUser.StatusCode)
	}
	respCrossProp, _ := postWebForm(t, clientMgr, srv.URL+"/workspace/profile", url.Values{
		"csrf_token":   {csrfMgr},
		"property_id":  {propBID.String()},
		"profile_code": {"owner_admin"},
	})
	if respCrossProp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-property tamper expected 403, got %d", respCrossProp.StatusCode)
	}
	reqBadCSRF, _ := http.NewRequest(http.MethodPost, srv.URL+"/workspace/profile", strings.NewReader(url.Values{
		"csrf_token":   {"invalid-csrf"},
		"property_id":  {propA.ID.String()},
		"profile_code": {"owner_admin"},
	}.Encode()))
	reqBadCSRF.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	respBadCSRF, _ := clientMgr.Do(reqBadCSRF)
	_ = respBadCSRF.Body.Close()
	if respBadCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("bad CSRF expected 403, got %d", respBadCSRF.StatusCode)
	}
	reqUnknown, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/workspace/profile", strings.NewReader(`{"profile_code":"super_god_mode"}`))
	reqUnknown.Header.Set("Content-Type", "application/json")
	reqUnknown.Header.Set("X-CSRF-Token", csrfMgr)
	respUnknown, _ := clientMgr.Do(reqUnknown)
	_ = respUnknown.Body.Close()
	if respUnknown.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unknown profile expected 422, got %d", respUnknown.StatusCode)
	}

	// MustRotatePassword enforcement on /workspace and /workspace/profile API.
	clientRot, csrfRot := loginAdminClient(t, srv.URL, rotA.Email, "Pwe5RotPass!2026")
	respRotWS, _ := getWebPage(t, clientRot, srv.URL+"/workspace")
	if !strings.Contains(respRotWS.Request.URL.Path, "/account") {
		t.Fatalf("MustRotatePassword user should be redirected to /account, got %s", respRotWS.Request.URL.String())
	}
	reqRotAPI, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/workspace/profile", strings.NewReader(`{"profile_code":"reception"}`))
	reqRotAPI.Header.Set("Content-Type", "application/json")
	reqRotAPI.Header.Set("X-CSRF-Token", csrfRot)
	respRotAPI, _ := clientRot.Do(reqRotAPI)
	_ = respRotAPI.Body.Close()
	if respRotAPI.StatusCode != http.StatusForbidden {
		t.Fatalf("MustRotatePassword API expected 403, got %d", respRotAPI.StatusCode)
	}

	// 4. Non-destructive layer isolation + partial reset verification (Manager on Reception profile).
	respSaveGlobal, _ := postWebForm(t, clientMgr, srv.URL+"/account/preferences/global", url.Values{
		"csrf_token":        {csrfMgr},
		"theme_mode":        {"dark"},
		"accent":            {"brass"},
		"density":           {"inherit"},
		"text_scale":        {"large"},
		"high_contrast":     {"1"},
		"default_route":     {""},
		"calendar_horizon":  {"14d"},
		"calendar_grouping": {"unit_type"},
		"table_page_size":   {"25"},
	})
	if respSaveGlobal.StatusCode != http.StatusOK {
		t.Fatalf("save global prefs failed: %d", respSaveGlobal.StatusCode)
	}
	mgrActor := service.Actor{UserID: mgrA.ID, PropertyID: &propA.ID, Email: mgrA.Email, Role: domain.RoleManager}
	if _, err := appSvc.SaveOperationalView(ctx, mgrActor, mgrA.ID, propA.ID, domain.SavedOperationalView{
		ID: uuid.NewString(), Name: "Arrival Focus", Screen: "reservations",
	}); err != nil {
		t.Fatalf("save operational view failed: %v", err)
	}
	customLayout := domain.DashboardLayout{
		SchemaVersion: domain.CurrentDashboardLayoutSchemaVersion,
		Widgets: []domain.DashboardWidgetInstance{
			{InstanceID: "cust-alerts", WidgetID: domain.WidgetOpsAlertsAttention, Size: domain.WidgetSizeStandard, Slot: domain.DashboardSlotMain, Position: 1},
		},
	}
	if _, err := appSvc.SaveDashboardLayout(ctx, mgrActor, mgrA.ID, propA.ID, customLayout); err != nil {
		t.Fatalf("save custom layout failed: %v", err)
	}

	resolvedMgr, err := appSvc.ResolveWorkspace(ctx, mgrActor, &propA.ID)
	if err != nil {
		t.Fatalf("ResolveWorkspace failed: %v", err)
	}
	if resolvedMgr.Theme.Mode != "dark" || resolvedMgr.Theme.Accent != "brass" || resolvedMgr.Theme.Contrast != branding.ThemeContrastHigh ||
		resolvedMgr.Theme.Density != branding.ThemeDensityCompact || resolvedMgr.DefaultRoute != "/calendar" {
		t.Fatalf("layer cascade mismatch while inheriting: %+v", resolvedMgr)
	}

	// Verify PWE-2 property workspace reset preserves selected_profile_code ("reception") and custom PWE-3 layout!
	respWSResetPWE2, _ := postWebForm(t, clientMgr, srv.URL+"/account/preferences/workspace/reset", url.Values{
		"csrf_token":  {csrfMgr},
		"property_id": {propA.ID.String()},
	})
	if respWSResetPWE2.StatusCode != http.StatusOK {
		t.Fatalf("PWE-2 workspace reset failed: %d", respWSResetPWE2.StatusCode)
	}
	profStateAfterPWE2, err := appSvc.GetWorkspaceProfileState(ctx, mgrActor, mgrA.ID, propA.ID)
	if err != nil || profStateAfterPWE2.SelectedCode != domain.ProfileReception {
		t.Fatalf("PWE-2 workspace reset wiped selected_profile_code! got=%+v err=%v", profStateAfterPWE2, err)
	}
	layoutAfterPWE2, err := appSvc.GetDashboardLayout(ctx, mgrActor, mgrA.ID, propA.ID)
	if err != nil || layoutAfterPWE2.IsDefault || len(layoutAfterPWE2.Widgets) != 1 || layoutAfterPWE2.Widgets[0].InstanceID != "cust-alerts" {
		t.Fatalf("PWE-2 workspace reset wiped PWE-3 dashboard layout! got=%+v err=%v", layoutAfterPWE2, err)
	}

	// Verify PWE-3 layout-only reset preserves selected_profile_code & SavedView!
	_, _ = appSvc.SaveOperationalView(ctx, mgrActor, mgrA.ID, propA.ID, domain.SavedOperationalView{
		ID: uuid.NewString(), Name: "Preserved Saved View", Screen: "reservations",
	})
	respSaveExplicit, _ := postWebForm(t, clientMgr, srv.URL+"/account/preferences/global", url.Values{
		"csrf_token":        {csrfMgr},
		"theme_mode":        {"dark"},
		"accent":            {"brass"},
		"density":           {"comfortable"},
		"text_scale":        {"large"},
		"high_contrast":     {"1"},
		"default_route":     {"/reports"},
		"shortcuts":         {"/reports"},
		"calendar_horizon":  {"14d"},
		"calendar_grouping": {"unit_type"},
		"table_page_size":   {"25"},
	})
	if respSaveExplicit.StatusCode != http.StatusOK {
		t.Fatalf("save explicit overrides failed: %d", respSaveExplicit.StatusCode)
	}
	respLayoutReset, _ := postWebForm(t, clientMgr, srv.URL+"/workspace/reset", url.Values{
		"csrf_token":  {csrfMgr},
		"property_id": {propA.ID.String()},
	})
	if respLayoutReset.StatusCode != http.StatusOK {
		t.Fatalf("layout-only reset failed: %d", respLayoutReset.StatusCode)
	}
	profStateAfterPWE3, _ := appSvc.GetWorkspaceProfileState(ctx, mgrActor, mgrA.ID, propA.ID)
	if profStateAfterPWE3.SelectedCode != domain.ProfileReception {
		t.Fatalf("PWE-3 layout reset cleared selected_profile_code: got %q", profStateAfterPWE3.SelectedCode)
	}

	// Re-save custom layout then test PWE-5 reset WITHOUT clear_named_overrides:
	_, _ = appSvc.SaveDashboardLayout(ctx, mgrActor, mgrA.ID, propA.ID, customLayout)
	respProfResetKeep, _ := postWebForm(t, clientMgr, srv.URL+"/workspace/profile/reset", url.Values{
		"csrf_token":  {csrfMgr},
		"property_id": {propA.ID.String()},
		"return_to":   {"/workspace"},
	})
	if respProfResetKeep.StatusCode != http.StatusOK {
		t.Fatalf("profile reset keep-overrides failed: %d", respProfResetKeep.StatusCode)
	}
	resolvedKeep, _ := appSvc.ResolveWorkspace(ctx, mgrActor, &propA.ID)
	layoutKeep, _ := appSvc.GetDashboardLayout(ctx, mgrActor, mgrA.ID, propA.ID)
	if resolvedKeep.SelectedProfileCode != domain.ProfileOwnerAdmin || !layoutKeep.IsDefault || layoutKeep.ProfileCode != domain.ProfileOwnerAdmin {
		t.Fatalf("expected reset back to owner_admin role default, got code=%q layout=%+v", resolvedKeep.SelectedProfileCode, layoutKeep)
	}
	if resolvedKeep.Theme.Mode != "dark" || resolvedKeep.Theme.Accent != "brass" || resolvedKeep.DefaultRoute != "/reports" || len(resolvedKeep.SavedViews) != 1 {
		t.Fatalf("expected theme, saved views, and explicit overrides preserved when clear_named_overrides=false, got %+v", resolvedKeep)
	}

	// Switch back to reception + custom layout then test PWE-5 reset WITH clear_named_overrides=1:
	_, _ = appSvc.SelectWorkspaceProfile(ctx, mgrActor, mgrA.ID, propA.ID, domain.ProfileReception)
	_, _ = appSvc.SaveDashboardLayout(ctx, mgrActor, mgrA.ID, propA.ID, customLayout)
	respProfResetClear, _ := postWebForm(t, clientMgr, srv.URL+"/workspace/profile/reset", url.Values{
		"csrf_token":            {csrfMgr},
		"property_id":           {propA.ID.String()},
		"clear_named_overrides": {"1"},
		"return_to":             {"/workspace"},
	})
	if respProfResetClear.StatusCode != http.StatusOK {
		t.Fatalf("profile reset clear-overrides failed: %d", respProfResetClear.StatusCode)
	}
	resolvedCleared, _ := appSvc.ResolveWorkspace(ctx, mgrActor, &propA.ID)
	if resolvedCleared.SelectedProfileCode != domain.ProfileOwnerAdmin || resolvedCleared.DefaultRoute != "/" || resolvedCleared.Theme.Mode != "dark" || resolvedCleared.Theme.Accent != "brass" || len(resolvedCleared.SavedViews) != 1 {
		t.Fatalf("expected named overrides cleared back to Owner defaults while keeping theme/views, got %+v", resolvedCleared)
	}

	// 5. Live DB role downgrade recovers cleanly with warning and backend 403 enforcement.
	_, _ = appSvc.SelectWorkspaceProfile(ctx, mgrActor, mgrA.ID, propA.ID, domain.ProfileReception)
	if _, err := migratorStore.Pool().Exec(ctx, `UPDATE admin_users SET role = 'housekeeping_staff' WHERE id = $1`, mgrA.ID); err != nil {
		t.Fatalf("downgrade mgrA -> housekeeping_staff: %v", err)
	}
	respDowngradeAPI, bodyDowngradeAPI := getWebPage(t, clientMgr, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/workspace/profile")
	if respDowngradeAPI.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK on profile API after downgrade, got %d", respDowngradeAPI.StatusCode)
	}
	var downgradedState service.WorkspaceProfileState
	if err := json.Unmarshal([]byte(bodyDowngradeAPI), &downgradedState); err != nil {
		t.Fatalf("decode downgraded profile JSON: %v", err)
	}
	if downgradedState.SelectedCode != domain.ProfileHousekeeping || len(downgradedState.Warnings) == 0 {
		t.Fatalf("expected automatic switch to Housekeeping + diagnostic warning, got %+v", downgradedState)
	}
	respDowngradeReports, _ := getWebPage(t, clientMgr, srv.URL+"/reports")
	if respDowngradeReports.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden on /reports after downgrade, got %d", respDowngradeReports.StatusCode)
	}

	if _, err := migratorStore.Pool().Exec(ctx, `UPDATE admin_users SET role = 'viewer' WHERE id = $1`, mgrA.ID); err != nil {
		t.Fatalf("downgrade mgrA -> viewer: %v", err)
	}
	_, bodyViewerDowngraded := getWebPage(t, clientMgr, srv.URL+"/workspace")
	if !strings.Contains(bodyViewerDowngraded, "Platform Default Layout") || strings.Contains(bodyViewerDowngraded, "Channel Sync Health") {
		t.Fatalf("expected viewer downgrade to discard profile safely")
	}
	respViewerDowngradeUsers, _ := getWebPage(t, clientMgr, srv.URL+"/users")
	if respViewerDowngradeUsers.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden on /users after viewer downgrade, got %d", respViewerDowngradeUsers.StatusCode)
	}

	// 6. PWE-4 custom branding compatibility + public API isolation.
	respPropCSS, _ := getWebPage(t, clientOwner, srv.URL+"/branding/property.css")
	if respPropCSS.StatusCode != http.StatusOK {
		t.Fatalf("expected /branding/property.css intact, got %d", respPropCSS.StatusCode)
	}
	anonClient := newFollowingClient(t)
	respPub, bodyPub := getWebPage(t, anonClient, srv.URL+"/api/public/v1/properties/"+propA.Slug)
	if respPub.StatusCode == http.StatusOK && (strings.Contains(bodyPub, "selected_profile_code") || strings.Contains(bodyPub, "owner_admin")) {
		t.Fatalf("public API leaked PWE-5 workspace profile data")
	}
}
