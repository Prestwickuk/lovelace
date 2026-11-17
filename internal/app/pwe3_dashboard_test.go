package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/channels"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
)

func apiJSONRequest(
	t *testing.T,
	client *http.Client,
	method, targetURL, csrfToken string,
	payload any,
) (*http.Response, string) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal %s %s: %v", method, targetURL, err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, targetURL, body)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, targetURL, err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrfToken != "" {
		req.Header.Set("X-CSRF-Token", csrfToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, targetURL, err)
	}
	defer resp.Body.Close()
	rawResp, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, targetURL, err)
	}
	return resp, string(rawResp)
}

func TestPWE3_CustomisableDashboardSecurityPermissionsAndAllWidgets(t *testing.T) {
	srv, migratorStore, appSvc, propA, _, _ := setupPhase4AppServer(t, "pwe3-suite", channels.AdapterConfig{})
	defer srv.Close()
	ctx := context.Background()

	var ownerID uuid.UUID
	if err := migratorStore.Pool().QueryRow(ctx, `SELECT id FROM admin_users WHERE email = 'owner@tirbach.wales'`).Scan(&ownerID); err != nil {
		t.Fatalf("find owner: %v", err)
	}
	ownerActor := service.Actor{UserID: ownerID, PropertyID: nil, Email: "owner@tirbach.wales", Role: domain.RoleOwnerAdmin}

	// Property B (empty dataset for empty-state + cross-property checks)
	propBID := uuid.New()
	utBID := uuid.New()
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO properties (id, slug, name, region, timezone, check_in_time, check_out_time, currency)
		VALUES ($1, 'pwe3-prop-b', 'Coastal Haven PWE3', 'Ceredigion', 'Europe/London', '15:00', '10:00', 'GBP')
	`, propBID); err != nil {
		t.Fatalf("insert prop B: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO unit_types (id, property_id, code, name, category, base_capacity_adults, max_adults, base_nightly_rate_pence)
		VALUES ($1, $2, 'pod-b', 'Foreign Property Pod', 'glamping', 2, 2, 12000)
	`, utBID, propBID); err != nil {
		t.Fatalf("insert unit_type B: %v", err)
	}

	// Users on Property A + one on Property B
	mgrA, err := appSvc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		PropertyID: &propA.ID, Email: "pwe3.mgra@tirbach.wales", FullName: "PWE3 Manager A", Role: domain.RoleManager, Password: "Pwe3ManagerPass!2026",
	})
	if err != nil {
		t.Fatalf("create mgrA: %v", err)
	}
	hkA, err := appSvc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		PropertyID: &propA.ID, Email: "pwe3.hka@tirbach.wales", FullName: "PWE3 Housekeeping A", Role: domain.RoleHousekeepingStaff, Password: "Pwe3HousePass!2026",
	})
	if err != nil {
		t.Fatalf("create hkA: %v", err)
	}
	viewerA, err := appSvc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		PropertyID: &propA.ID, Email: "pwe3.viewera@tirbach.wales", FullName: "PWE3 Viewer A", Role: domain.RoleViewer, Password: "Pwe3ViewerPass!2026",
	})
	if err != nil {
		t.Fatalf("create viewerA: %v", err)
	}
	mgrB, err := appSvc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		PropertyID: &propBID, Email: "pwe3.mgrb@tirbach.wales", FullName: "PWE3 Manager B", Role: domain.RoleManager, Password: "Pwe3MgrBPass!2026",
	})
	if err != nil {
		t.Fatalf("create mgrB: %v", err)
	}
	rotUser, err := appSvc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		PropertyID: &propA.ID, Email: "pwe3.rot@tirbach.wales", FullName: "PWE3 Rotate", Role: domain.RoleManager, Password: "Pwe3RotPass!2026", MustRotatePassword: true,
	})
	if err != nil {
		t.Fatalf("create rotUser: %v", err)
	}

	// Seed rich operational, maintenance, reservation, CRM, channel, and multi-currency reporting data for Property A.
	unitsA, err := migratorStore.ListUnits(ctx, propA.ID, nil, false)
	if err != nil || len(unitsA) == 0 {
		t.Fatalf("list Property A units: %v", err)
	}
	utsA, err := migratorStore.ListUnitTypes(ctx, propA.ID, false)
	if err != nil || len(utsA) == 0 {
		t.Fatalf("list Property A unit types: %v", err)
	}

	todayUTC := time.Now().UTC().Truncate(24 * time.Hour)
	arrToday := todayUTC
	depPlus2 := todayUTC.AddDate(0, 0, 2)
	pastArr := todayUTC.AddDate(0, 0, -5)
	pastDep := todayUTC.AddDate(0, 0, -3)

	// 1) Confirmed arrival today + pending hold + EUR settled booking in last 7 days
	resConfirmed, err := appSvc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    propA.ID,
		ChannelCode:   "DIRECT",
		GuestName:     "Rowan Davies",
		GuestEmail:    "rowan.davies@example.test",
		ArrivalDate:   arrToday,
		DepartureDate: depPlus2,
		Adults:        2,
		TotalPence:    24000,
		Currency:      "GBP",
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unitsA[0].ID},
	})
	if err != nil {
		t.Fatalf("create confirmed reservation: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `UPDATE reservations SET total_pence = 24000, currency = 'GBP' WHERE id = $1`, resConfirmed.ID); err != nil {
		t.Fatalf("set GBP total on confirmed reservation: %v", err)
	}

	// Pending provisional hold
	if _, err := appSvc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    propA.ID,
		ChannelCode:   "DIRECT",
		GuestName:     "Sian Thomas",
		GuestEmail:    "sian.thomas@example.test",
		ArrivalDate:   todayUTC.AddDate(0, 0, 4),
		DepartureDate: todayUTC.AddDate(0, 0, 6),
		Adults:        2,
		TotalPence:    18000,
		Currency:      "GBP",
		Status:        domain.ResStatusPending,
		UnitIDs:       []uuid.UUID{unitsA[0].ID},
	}); err != nil {
		t.Fatalf("create pending reservation: %v", err)
	}

	// EUR past booking inside 30d report window to verify currency separation in reports.kpi_snapshot
	resEUR, err := appSvc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    propA.ID,
		ChannelCode:   "DIRECT",
		GuestName:     "Lukas Weber",
		GuestEmail:    "lukas.weber@example.test",
		ArrivalDate:   pastArr,
		DepartureDate: pastDep,
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unitsA[0].ID},
	})
	if err != nil {
		t.Fatalf("create EUR reservation: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `UPDATE reservations SET total_pence = 15000, currency = 'EUR' WHERE id = $1`, resEUR.ID); err != nil {
		t.Fatalf("set EUR currency and total: %v", err)
	}

	// Seed an inventory_history_gap in the 30-day window so reports.kpi_snapshot renders partial-history warning cleanly!
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO inventory_history_gaps (id, property_id, reliable_from, reason)
		VALUES ($1, $2, $3::date, 'historical status gap seeded for PWE-3 completeness check')
	`, uuid.New(), propA.ID, todayUTC.AddDate(0, 0, -5).Format("2006-01-02")); err != nil {
		t.Fatalf("insert inventory_history_gap: %v", err)
	}

	// Open payment exception (payments alert)
	payAttemptID := uuid.New()
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO payment_attempts (id, property_id, reservation_id, provider, payment_type, amount_pence, currency, status, idempotency_key)
		VALUES ($1, $2, $3, 'stripe', 'FULL', 24000, 'GBP', 'RECONCILIATION_REQUIRED', $4)
	`, payAttemptID, propA.ID, resConfirmed.ID, "pwe3-attempt-"+payAttemptID.String()); err != nil {
		t.Fatalf("insert payment_attempt: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO payment_exceptions (id, property_id, payment_attempt_id, reservation_id, exception_type, status, amount_pence, currency, details)
		VALUES ($1, $2, $3, $4, 'AMOUNT_MISMATCH', 'OPEN', 24000, 'GBP', 'Simulated payment mismatch for alert filtering')
	`, uuid.New(), propA.ID, payAttemptID, resConfirmed.ID); err != nil {
		t.Fatalf("insert payment_exception: %v", err)
	}

	// Maintenance category + urgent request + due preventative schedule
	cats, err := appSvc.ListMaintenanceCategories(ctx, ownerActor, propA.ID, true)
	if err != nil || len(cats) == 0 {
		t.Fatalf("list maintenance categories: %v", err)
	}
	maintReq, err := appSvc.CreateMaintenanceRequest(ctx, ownerActor, service.CreateMaintenanceRequestInput{
		PropertyID: propA.ID,
		Title:      "Leaking Standpipe Tap",
		CategoryID: cats[0].ID,
		Priority:   domain.MaintPriorityUrgent,
		UnitID:     &unitsA[0].ID,
	})
	if err != nil {
		t.Fatalf("create maintenance request: %v", err)
	}
	if _, err := appSvc.CreateMaintenanceSchedule(ctx, ownerActor, domain.MaintenanceSchedule{
		PropertyID:  propA.ID,
		Title:       "Weekly Fire Point Check",
		CategoryID:  cats[0].ID,
		UnitID:      &unitsA[0].ID,
		Priority:    domain.MaintPriorityHigh,
		Frequency:   domain.MaintFreqWeekly,
		NextDueDate: todayUTC.AddDate(0, 0, 2),
		IsActive:    true,
	}); err != nil {
		t.Fatalf("create maintenance schedule: %v", err)
	}

	// Housekeeping overdue task
	if _, err := appSvc.CreateHousekeepingTask(
		ctx, ownerActor, propA.ID, unitsA[0].ID,
		domain.TaskTypeChangeoverClean, domain.StageCleaningRequired, domain.TaskPriorityUrgent,
		todayUTC, "Urgent Turnaround Check", "Urgent PWE-3 test task", nil,
	); err != nil {
		t.Fatalf("create housekeeping task: %v", err)
	}

	// Secret CRM guest note & secret internal enquiry message (MUST NEVER appear in /workspace HTML or JSON!)
	const secretNoteText = "SECRET_PRIVATE_NOTE_DO_NOT_LEAK"
	const secretInternalEnquiryMsg = "SECRET_INTERNAL_STAFF_DISCUSSION_NEVER_EXPOSE"
	if _, err := appSvc.AddGuestNote(ctx, ownerActor, domain.GuestNote{
		PropertyID: propA.ID, GuestID: resConfirmed.GuestID, Category: domain.NoteCatGeneral, Content: secretNoteText,
	}); err != nil {
		t.Fatalf("add secret guest note: %v", err)
	}
	enq, err := appSvc.CreateGuestEnquiry(ctx, ownerActor, domain.GuestEnquiry{
		PropertyID: propA.ID, GuestID: &resConfirmed.GuestID, Category: domain.EnqCatComplaint,
		Priority: domain.EnqPriorityUrgent, Source: domain.EnqSourceEmail,
		Subject: "Early Arrival Inquiry", Description: "Public description should not leak in list",
	})
	if err != nil {
		t.Fatalf("create guest enquiry: %v", err)
	}
	if _, err := appSvc.AddGuestEnquiryMessage(ctx, ownerActor, domain.GuestEnquiryMessage{
		PropertyID: propA.ID, EnquiryID: enq.ID, Direction: domain.CommDirInternal,
		IsInternal: true, Body: secretInternalEnquiryMsg,
	}); err != nil {
		t.Fatalf("add internal enquiry message: %v", err)
	}

	// Failed email_outbox item (comms alert) + channel connection error (channel alert)
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO email_outbox (id, property_id, recipient_email, template, subject, body_text, body_html, status, last_error)
		VALUES ($1, $2, 'rowan.davies@example.test', 'booking_confirmed', 'Confirmed', 'txt', '<p>html</p>', 'FAILED', 'SMTP relay timeout')
	`, uuid.New(), propA.ID); err != nil {
		t.Fatalf("insert failed outbox: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO channel_connections (id, property_id, provider_code, display_name, auth_method, status, last_error)
		VALUES ($1, $2, 'beds24', 'Beds24 Main', 'api_key', 'CONNECTION_ERROR', 'Token expired')
		ON CONFLICT (property_id, provider_code) DO UPDATE SET status = 'CONNECTION_ERROR', last_error = 'Token expired'
	`, uuid.New(), propA.ID); err != nil {
		t.Fatalf("upsert channel error: %v", err)
	}

	// Authenticate HTTP clients
	clientMgrA := newFollowingClient(t)
	stMgrA, csrfMgrA := apiLogin(t, clientMgrA, srv.URL, mgrA.Email, "Pwe3ManagerPass!2026")
	if stMgrA != http.StatusOK {
		t.Fatalf("mgrA login failed: %d", stMgrA)
	}
	clientHkA := newFollowingClient(t)
	stHkA, csrfHkA := apiLogin(t, clientHkA, srv.URL, hkA.Email, "Pwe3HousePass!2026")
	if stHkA != http.StatusOK {
		t.Fatalf("hkA login failed: %d", stHkA)
	}
	clientViewerA := newFollowingClient(t)
	stViewerA, _ := apiLogin(t, clientViewerA, srv.URL, viewerA.Email, "Pwe3ViewerPass!2026")
	if stViewerA != http.StatusOK {
		t.Fatalf("viewerA login failed: %d", stViewerA)
	}
	clientMgrB := newFollowingClient(t)
	stMgrB, _ := apiLogin(t, clientMgrB, srv.URL, mgrB.Email, "Pwe3MgrBPass!2026")
	if stMgrB != http.StatusOK {
		t.Fatalf("mgrB login failed: %d", stMgrB)
	}
	clientRot := newFollowingClient(t)
	stRot, _ := apiLogin(t, clientRot, srv.URL, rotUser.Email, "Pwe3RotPass!2026")
	if stRot != http.StatusOK {
		t.Fatalf("rotUser login failed: %d", stRot)
	}

	// 1. Existing `/` Operations Centre MUST remain completely intact!
	respHome, bodyHome := getWebPage(t, clientMgrA, srv.URL+"/")
	if respHome.StatusCode != http.StatusOK || !strings.Contains(bodyHome, "Operations Centre") {
		t.Fatalf("expected Operations Centre intact on /, status=%d", respHome.StatusCode)
	}

	// 2. Manager A loads `/workspace` out-of-the-box -> renders Owner / Administrator profile default,
	// and saving the full 11-widget platform catalogue renders all widgets + multi-currency + history warning + zero internal CRM secrets!
	respWSDef, bodyWSDef := getWebPage(t, clientMgrA, srv.URL+"/workspace")
	if respWSDef.StatusCode != http.StatusOK || !strings.Contains(bodyWSDef, "Owner / Administrator Profile Default") {
		t.Fatalf("expected Owner / Administrator Profile Default out-of-the-box, status=%d", respWSDef.StatusCode)
	}
	mgrAActorSeed := service.Actor{UserID: mgrA.ID, PropertyID: &propA.ID, Email: mgrA.Email, Role: domain.RoleManager}
	if _, err := appSvc.SaveDashboardLayout(ctx, mgrAActorSeed, mgrA.ID, propA.ID, domain.DefaultDashboardLayout()); err != nil {
		t.Fatalf("save 11-widget layout for manager audit: %v", err)
	}
	respWS, bodyWS := getWebPage(t, clientMgrA, srv.URL+"/workspace")
	if respWS.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK on /workspace, got %d", respWS.StatusCode)
	}
	for _, want := range []string{
		"Arrivals &amp; Departures Today",
		resConfirmed.Reference,
		"Daily Changeover Queue",
		"Operational Alerts",
		maintReq.Request.Reference,
		"Weekly Fire Point Check",
		"Provisional Holds &amp; Inquiries",
		"Availability Horizon",
		enq.Reference,
		"Channel Sync Health",
		"7/30-Day Performance Snapshot",
		"Personal Quick Links",
		"GBP", "EUR",
		"Partial unit inventory history:",
	} {
		if !strings.Contains(bodyWS, want) {
			t.Errorf("manager /workspace missing expected widget content %q", want)
		}
	}
	for _, secret := range []string{secretNoteText, secretInternalEnquiryMsg, "rowan.davies@example.test"} {
		if strings.Contains(bodyWS, secret) {
			t.Fatalf("manager /workspace leaked protected CRM PII/internal message %q", secret)
		}
	}
	if _, err := appSvc.ResetDashboardLayout(ctx, mgrAActorSeed, mgrA.ID, propA.ID); err != nil {
		t.Fatalf("reset layout after 11-widget check: %v", err)
	}

	// 3. Property B (empty dataset) renders clean empty states and ZERO Property A records.
	respWSB, bodyWSB := getWebPage(t, clientMgrB, srv.URL+"/workspace")
	if respWSB.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK on Prop B /workspace, got %d", respWSB.StatusCode)
	}
	if strings.Contains(bodyWSB, resConfirmed.Reference) || strings.Contains(bodyWSB, maintReq.Request.Reference) || strings.Contains(bodyWSB, enq.Reference) {
		t.Fatalf("Property B workspace leaked Property A records")
	}

	// 4. Restricted role isolation & mixed-permission alert filtering:
	// Housekeeping staff gets changeover + maintenance + alerts (Housekeeping + Maintenance ONLY), ZERO bookings/payments/channels/comms/CRM/reports!
	respWSHk, bodyWSHk := getWebPage(t, clientHkA, srv.URL+"/workspace")
	if respWSHk.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for housekeeping /workspace, got %d", respWSHk.StatusCode)
	}
	if !strings.Contains(bodyWSHk, "Daily Changeover Queue") || !strings.Contains(bodyWSHk, maintReq.Request.Reference) {
		t.Errorf("housekeeping /workspace missing permitted operational/maintenance widgets")
	}
	for _, forbidden := range []string{
		"Arrivals &amp; Departures Today",
		"7/30-Day Performance Snapshot",
		"Channel Sync Health",
		"Guest Enquiries &amp; Complaints",
		"Open payment exception (",
		"AMOUNT_MISMATCH",
		"Beds24 connection error",
		"Token expired",
		"Email outbox FAILED",
		"SMTP relay timeout",
		resConfirmed.Reference,
		"Rowan Davies",
	} {
		if strings.Contains(bodyWSHk, forbidden) {
			t.Fatalf("housekeeping /workspace leaked restricted widget/alert/guest item %q", forbidden)
		}
	}

	// Viewer has guests:read WITHOUT comms:read -> crm.open_enquiries AND Email Outbox alert MUST be omitted!
	_, bodyWSViewer := getWebPage(t, clientViewerA, srv.URL+"/workspace")
	if strings.Contains(bodyWSViewer, enq.Reference) || strings.Contains(bodyWSViewer, "Email outbox FAILED") {
		t.Fatalf("viewer /workspace leaked comms-gated enquiry or email outbox alert")
	}

	// 5. Cross-property API & form isolation + self-ownership + validation (422)
	// Housekeeping trying to inject reports.kpi_snapshot via JSON API or SSR form gets 403 Forbidden.
	respHkAdd, _ := apiJSONRequest(t, clientHkA, http.MethodPut, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/workspace/dashboard", csrfHkA, domain.DashboardLayout{
		SchemaVersion: 1,
		Widgets: []domain.DashboardWidgetInstance{{
			InstanceID: "hk-inject", WidgetID: domain.WidgetReportsKPISnapshot, Size: domain.WidgetSizeStandard, Position: 1,
		}},
	})
	if respHkAdd.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when housekeeping tries to save reports widget, got %d", respHkAdd.StatusCode)
	}

	// Manager A cannot access or overwrite Property B workspace API or submit Property B property_id via SSR form.
	respCrossGet, _ := getWebPage(t, clientMgrA, srv.URL+"/api/v1/properties/"+propBID.String()+"/workspace/dashboard")
	if respCrossGet.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden reading Prop B dashboard API, got %d", respCrossGet.StatusCode)
	}
	respCrossForm, _ := postWebForm(t, clientMgrA, srv.URL+"/workspace/widgets/add", url.Values{
		"csrf_token":  {csrfMgrA},
		"property_id": {propBID.String()},
		"widget_id":   {domain.WidgetMaintOpenQueue},
	})
	if respCrossForm.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden on cross-property SSR form add, got %d", respCrossForm.StatusCode)
	}
	// Forged user_id in form is rejected with 403 Forbidden.
	respCrossUser, _ := postWebForm(t, clientMgrA, srv.URL+"/workspace/widgets/add", url.Values{
		"csrf_token":  {csrfMgrA},
		"property_id": {propA.ID.String()},
		"user_id":     {hkA.ID.String()},
		"widget_id":   {domain.WidgetMaintOpenQueue},
	})
	if respCrossUser.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden submitting mismatched user_id, got %d", respCrossUser.StatusCode)
	}

	// Invalid payloads on PUT /api/v1/properties/{id}/workspace/dashboard return 422.
	invalidLayouts := []domain.DashboardLayout{
		{SchemaVersion: 1, Widgets: []domain.DashboardWidgetInstance{{InstanceID: "bad", WidgetID: "evil.sql_widget", Size: "compact", Position: 1}}},
		{SchemaVersion: 1, Widgets: []domain.DashboardWidgetInstance{{InstanceID: "bad", WidgetID: domain.WidgetResMiniCalendar, Size: "compact", Position: 1}}},
		{SchemaVersion: 1, Widgets: []domain.DashboardWidgetInstance{{InstanceID: "bad", WidgetID: domain.WidgetResMiniCalendar, Size: "wide", Position: 1, Config: map[string]any{"days": 7, "unit_type_id": utBID.String()}}}},
		{SchemaVersion: 1, Widgets: []domain.DashboardWidgetInstance{{InstanceID: "bad", WidgetID: domain.WidgetReportsKPISnapshot, Size: "standard", Position: 1, Config: map[string]any{"window_days": 30, "html": "<script>"}}}},
	}
	for i, badLayout := range invalidLayouts {
		rBad, _ := apiJSONRequest(t, clientMgrA, http.MethodPut, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/workspace/dashboard", csrfMgrA, badLayout)
		if rBad.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("invalid layout case %d expected 422, got %d", i, rBad.StatusCode)
		}
	}

	// Service cross-user self-ownership check.
	mgrAActor := service.Actor{UserID: mgrA.ID, PropertyID: &propA.ID, Email: mgrA.Email, Role: domain.RoleManager}
	if _, err := appSvc.SaveDashboardLayout(ctx, mgrAActor, hkA.ID, propA.ID, domain.DefaultDashboardLayout()); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden saving layout for another user, got %v", err)
	}

	// 6. Preserve PWE-2 preferences/saved views while adding/updating/moving/removing/resetting PWE-3 widgets via SSR!
	if _, err := appSvc.UpdateUserPropertyWorkspace(ctx, mgrAActor, mgrA.ID, propA.ID, domain.PropertyWorkspacePayload{
		CalendarHorizon: domain.Horizon14Days, TablePageSize: 50,
	}); err != nil {
		t.Fatalf("seed PWE-2 property workspace: %v", err)
	}
	if _, err := appSvc.SaveOperationalView(ctx, mgrAActor, mgrA.ID, propA.ID, domain.SavedOperationalView{
		Name: "PWE2 Preserved View", Screen: domain.SavedViewScreenReservations, Filters: map[string]string{"status": "CONFIRMED"},
	}); err != nil {
		t.Fatalf("seed PWE-2 saved view: %v", err)
	}

	// SSR Add, Move, Update, Remove on /workspace?edit=1
	respAddSSR, bodyAddSSR := postWebForm(t, clientMgrA, srv.URL+"/workspace/widgets/add", url.Values{
		"csrf_token":  {csrfMgrA},
		"property_id": {propA.ID.String()},
		"widget_id":   {domain.WidgetMaintOpenQueue},
		"size":        {"wide"},
		"return_to":   {"/workspace?edit=1"},
	})
	if respAddSSR.StatusCode != http.StatusOK || !strings.Contains(bodyAddSSR, "Widget added to your workspace") {
		t.Fatalf("SSR widget add failed, status=%d", respAddSSR.StatusCode)
	}

	// Missing CSRF on POST /workspace/reset returns 403
	respNoCSRF, _ := postWebForm(t, clientMgrA, srv.URL+"/workspace/reset", url.Values{
		"property_id": {propA.ID.String()},
	})
	if respNoCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden without CSRF on /workspace/reset, got %d", respNoCSRF.StatusCode)
	}

	// Verify Move & Update via resolved layout instance ID
	resolvedAfterAdd, err := appSvc.GetDashboardLayout(ctx, mgrAActor, mgrA.ID, propA.ID)
	if err != nil || len(resolvedAfterAdd.Widgets) == 0 {
		t.Fatalf("load resolved layout after add: %v", err)
	}
	lastInst := resolvedAfterAdd.Widgets[len(resolvedAfterAdd.Widgets)-1]
	respMoveUp, bodyMoveUp := postWebForm(t, clientMgrA, srv.URL+"/workspace/widgets/"+lastInst.InstanceID+"/move", url.Values{
		"csrf_token":  {csrfMgrA},
		"property_id": {propA.ID.String()},
		"position":    {"1"},
		"return_to":   {"/workspace?edit=1"},
	})
	if respMoveUp.StatusCode != http.StatusOK || !strings.Contains(bodyMoveUp, "Widget order updated") {
		t.Fatalf("SSR widget move failed, status=%d", respMoveUp.StatusCode)
	}
	respUpdateWidget, bodyUpdateWidget := postWebForm(t, clientMgrA, srv.URL+"/workspace/widgets/"+lastInst.InstanceID+"/update", url.Values{
		"csrf_token":     {csrfMgrA},
		"property_id":    {propA.ID.String()},
		"widget_id":      {domain.WidgetMaintOpenQueue},
		"size":           {"standard"},
		"slot":           {"main"},
		"priority_min":   {"high"},
		"assigned_to_me": {"0"},
		"limit":          {"5"},
		"return_to":      {"/workspace?edit=1"},
	})
	if respUpdateWidget.StatusCode != http.StatusOK || !strings.Contains(bodyUpdateWidget, "Widget configuration saved") {
		t.Fatalf("SSR widget update failed, status=%d", respUpdateWidget.StatusCode)
	}

	// Remove reports.kpi_snapshot from Manager A layout -> proves presentation-only removal (direct /reports still returns 200!).
	resolvedBeforeRem, _ := appSvc.GetDashboardLayout(ctx, mgrAActor, mgrA.ID, propA.ID)
	var kpiInstID string
	for _, w := range resolvedBeforeRem.Widgets {
		if w.WidgetID == domain.WidgetReportsKPISnapshot {
			kpiInstID = w.InstanceID
			break
		}
	}
	if kpiInstID == "" {
		t.Fatalf("expected reports.kpi_snapshot in Manager A layout")
	}
	postWebForm(t, clientMgrA, srv.URL+"/workspace/widgets/"+kpiInstID+"/remove", url.Values{
		"csrf_token":  {csrfMgrA},
		"property_id": {propA.ID.String()},
		"return_to":   {"/workspace"},
	})
	_, bodyAfterRemove := getWebPage(t, clientMgrA, srv.URL+"/workspace")
	if strings.Contains(bodyAfterRemove, "7/30-Day Performance Snapshot") {
		t.Fatalf("expected removed reports.kpi_snapshot hidden from /workspace")
	}
	respDirectReports, _ := getWebPage(t, clientMgrA, srv.URL+"/reports")
	if respDirectReports.StatusCode != http.StatusOK {
		t.Fatalf("removing widget must not alter authorization: direct /reports returned %d", respDirectReports.StatusCode)
	}

	// PWE-2 settings & saved view remain untouched by PWE-3 edits, AND PWE-2 update keeps PWE-3 custom layout untouched!
	if _, err := appSvc.UpdateUserPropertyWorkspace(ctx, mgrAActor, mgrA.ID, propA.ID, domain.PropertyWorkspacePayload{
		CalendarHorizon: domain.Horizon30Days, TablePageSize: 100,
	}); err != nil {
		t.Fatalf("update PWE-2 workspace while PWE-3 active: %v", err)
	}
	postPWE2Layout, _ := appSvc.GetDashboardLayout(ctx, mgrAActor, mgrA.ID, propA.ID)
	if postPWE2Layout.IsDefault {
		t.Fatalf("PWE-2 workspace update unexpectedly reset custom PWE-3 dashboard layout")
	}

	// Reset PWE-3 layout via SSR POST -> reverts to default while preserving PWE-2 Horizon30Days and SavedViews!
	respResetSSR, bodyResetSSR := postWebForm(t, clientMgrA, srv.URL+"/workspace/reset", url.Values{
		"csrf_token":  {csrfMgrA},
		"property_id": {propA.ID.String()},
		"return_to":   {"/workspace"},
	})
	if respResetSSR.StatusCode != http.StatusOK || !strings.Contains(bodyResetSSR, "Owner / Administrator Profile Default") {
		t.Fatalf("SSR workspace reset failed, status=%d", respResetSSR.StatusCode)
	}
	pwe2WS, _, err := appSvc.GetUserPropertyWorkspace(ctx, mgrAActor, mgrA.ID, propA.ID)
	if err != nil || pwe2WS.Payload.CalendarHorizon != domain.Horizon30Days || len(pwe2WS.SavedViews) != 1 {
		t.Fatalf("expected PWE-2 horizon & saved views preserved after PWE-3 reset, got %+v err=%v", pwe2WS, err)
	}

	// 7. Role downgrade hides permitted widgets dynamically while backend routes return 403 Forbidden.
	if _, err := migratorStore.Pool().Exec(ctx, `UPDATE admin_users SET role = 'housekeeping_staff' WHERE id = $1`, mgrA.ID); err != nil {
		t.Fatalf("downgrade mgrA role: %v", err)
	}
	_, bodyDowngraded := getWebPage(t, clientMgrA, srv.URL+"/workspace")
	if strings.Contains(bodyDowngraded, "7/30-Day Performance Snapshot") || strings.Contains(bodyDowngraded, enq.Reference) {
		t.Fatalf("downgraded role /workspace leaked restricted widgets")
	}
	respDowngradedReports, _ := getWebPage(t, clientMgrA, srv.URL+"/reports")
	if respDowngradedReports.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden on /reports after downgrade, got %d", respDowngradedReports.StatusCode)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `UPDATE admin_users SET role = 'manager' WHERE id = $1`, mgrA.ID); err != nil {
		t.Fatalf("restore mgrA role: %v", err)
	}

	// 8. Corrupt DB dashboard JSONB & cross-property unit_type_id recovery renders 200 OK with diagnostics and sibling widgets.
	corruptJSON := `{
		"schema_version": 7,
		"widgets": [
			{"instance_id": "gone-1", "widget_id": "obsolete.widget", "size": "compact", "position": 1},
			{"instance_id": "cal-rec", "widget_id": "res.mini_calendar", "size": "wide", "position": 2, "config": {"days": 7, "unit_type_id": "` + utBID.String() + `"}},
			{"instance_id": "ok-maint", "widget_id": "maint.open_queue", "size": "standard", "position": 3}
		]
	}`
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO admin_user_property_workspaces (user_id, property_id, dashboard_schema_version, dashboard_layout)
		VALUES ($1, $2, 7, $3::jsonb)
		ON CONFLICT (user_id, property_id) DO UPDATE SET dashboard_schema_version = 7, dashboard_layout = EXCLUDED.dashboard_layout
	`, mgrA.ID, propA.ID, corruptJSON); err != nil {
		t.Fatalf("inject corrupt dashboard JSON: %v", err)
	}
	respRec, bodyRec := getWebPage(t, clientMgrA, srv.URL+"/workspace")
	if respRec.StatusCode != http.StatusOK || !strings.Contains(bodyRec, "Workspace layout notices:") || !strings.Contains(bodyRec, maintReq.Request.Reference) {
		t.Fatalf("expected 200 OK partial recovery with notices & sibling widget, status=%d", respRec.StatusCode)
	}
	if strings.Contains(bodyRec, utBID.String()) {
		t.Fatalf("cross-property unit_type_id leaked in recovered /workspace output")
	}

	// 9. must_rotate_password blocks /workspace (redirects to /account?rotate=1) & API returns 403!
	respRotPage, _ := getWebPage(t, clientRot, srv.URL+"/workspace")
	if !strings.Contains(respRotPage.Request.URL.Path, "/account") {
		t.Fatalf("expected MustRotatePassword user redirected to /account, got %s", respRotPage.Request.URL.String())
	}
	respRotAPI, _ := getWebPage(t, clientRot, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/workspace/dashboard")
	if respRotAPI.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 on workspace API while MustRotatePassword active, got %d", respRotAPI.StatusCode)
	}

	// 10. Public API remains completely isolated from workspace layouts/widgets.
	anonClient := newFollowingClient(t)
	respPub, bodyPub := getWebPage(t, anonClient, srv.URL+"/api/public/v1/properties/"+propA.Slug)
	if respPub.StatusCode == http.StatusOK && (strings.Contains(bodyPub, "dashboard_layout") || strings.Contains(bodyPub, "ops.arrivals_summary")) {
		t.Fatalf("public API leaked PWE-3 workspace data")
	}
}
