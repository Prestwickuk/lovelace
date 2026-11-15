package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

func TestPhase3Milestone1OperationsCentreWorkflow(t *testing.T) {
	srv, store, svc, prop, _, _, _ := setupPhase2TestServer(t, "pub-p3-ops-secret")
	defer srv.Close()

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	// Provision a staff operator and a read-only viewer for RBAC & assignment tests.
	staffUser, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email:    "warden@tirbach.wales",
		FullName: "Morfudd Warden",
		Role:     domain.RoleStaff,
		Password: "warden-ops-pass-2026",
	})
	if err != nil {
		t.Fatalf("create staff user: %v", err)
	}
	viewerUser, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email:    "auditor@tirbach.wales",
		FullName: "Iwan Viewer",
		Role:     domain.RoleViewer,
		Password: "viewer-ops-pass-2026",
	})
	if err != nil {
		t.Fatalf("create viewer user: %v", err)
	}
	_ = viewerUser

	units, err := svc.ListUnits(ctx, ownerActor, prop.ID, nil)
	if err != nil || len(units) < 4 {
		t.Fatalf("expected seeded units >= 4, got len=%d err=%v", len(units), err)
	}
	unit1 := units[0]
	unit2 := units[1]
	unit3 := units[2]
	unit4 := units[3]

	// Determine today's operational date in the property timezone (Europe/London).
	loc, _ := time.LoadLocation(prop.Timezone)
	if loc == nil {
		loc = time.UTC
	}
	nowLocal := time.Now().In(loc)
	today := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, time.UTC)
	todayStr := domain.FormatDate(today)
	yesterday := today.AddDate(0, 0, -1)
	yesterday2 := today.AddDate(0, 0, -2)
	tomorrow := today.AddDate(0, 0, 1)

	// 1. Departing checked-in stay on unit1 today (same-day turnaround before arrival C).
	departingStay, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Gareth Departing",
		GuestEmail:    "gareth@example.test",
		ChannelCode:   "DIRECT",
		ArrivalDate:   yesterday2,
		DepartureDate: today,
		Adults:        2,
		Status:        domain.ResStatusCheckedIn,
		UnitIDs:       []uuid.UUID{unit1.ID},
	})
	if err != nil {
		t.Fatalf("create departing stay: %v", err)
	}

	// 2. Same-day arriving confirmed stay on unit1 today -> turnaround!
	turnaroundArrival, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Eleri Turnaround",
		GuestEmail:    "eleri@example.test",
		ChannelCode:   "DIRECT",
		ArrivalDate:   today,
		DepartureDate: today.AddDate(0, 0, 2),
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		InternalNotes: "Arriving early with dog — needs welcome fire-pit kit",
		UnitIDs:       []uuid.UUID{unit1.ID},
	})
	if err != nil {
		t.Fatalf("create turnaround arrival: %v", err)
	}

	// 3. Currently in-house stay on unit3 across today.
	inHouseStay, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Sian Stayover",
		GuestEmail:    "sian@example.test",
		ChannelCode:   "PHONE",
		ArrivalDate:   yesterday,
		DepartureDate: tomorrow,
		Adults:        2,
		Status:        domain.ResStatusCheckedIn,
		UnitIDs:       []uuid.UUID{unit3.ID},
	})
	if err != nil {
		t.Fatalf("create in-house stay: %v", err)
	}

	// 4. Missed arrival from yesterday on unit4 + failed payment & balance due -> operational attention!
	missedArrival, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Dylan Overdue",
		GuestEmail:    "dylan@example.test",
		ChannelCode:   "DIRECT",
		ArrivalDate:   yesterday,
		DepartureDate: today.AddDate(0, 0, 2),
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unit4.ID},
	})
	if err != nil {
		t.Fatalf("create missed arrival: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `
		UPDATE reservations
		SET payment_status = 'FAILED', total_pence = 18000, balance_due_pence = 18000
		WHERE id = $1
	`, missedArrival.ID); err != nil {
		t.Fatalf("mark failed payment on missed arrival: %v", err)
	}

	// 5. Active MAINTENANCE blockout on unit2 overlapping today (proving independence from housekeeping).
	_, _, err = svc.CreateBulkBlockout(ctx, ownerActor, domain.BulkBlockoutInput{
		PropertyID: prop.ID,
		ScopeType:  domain.BlockScopeUnit,
		UnitIDs:    []uuid.UUID{unit2.ID},
		Category:   domain.ClaimMaintenance,
		StartDate:  today,
		EndDate:    today.AddDate(0, 0, 3),
		Reason:     "Canvas zip repair",
	})
	if err != nil {
		t.Fatalf("apply maintenance blockout: %v", err)
	}

	// Unauthenticated Operations API call must be rejected (guest/public separation).
	unauthResp, err := http.Get(srv.URL + "/api/v1/properties/" + prop.ID.String() + "/operations")
	if err != nil {
		t.Fatalf("unauth ops req: %v", err)
	}
	unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated operations API call, got %d", unauthResp.StatusCode)
	}

	// Sign in as staff operator via API and verify Operations Centre summary.
	staffClient, staffCSRF := loginAdminClient(t, srv.URL, "warden@tirbach.wales", "warden-ops-pass-2026")
	opsBefore := fetchOpsJSON(t, staffClient, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/operations?date="+todayStr)

	if opsBefore.Summary.ArrivalsTotal != 1 || opsBefore.Summary.DeparturesTotal != 1 || opsBefore.Summary.InHouseStays != 1 {
		t.Fatalf("unexpected daily movement summary (arr=%d dep=%d inHouse=%d)",
			opsBefore.Summary.ArrivalsTotal, opsBefore.Summary.DeparturesTotal, opsBefore.Summary.InHouseStays)
	}
	if opsBefore.ChangeoverBoard.GuestDepartingCount != 1 || opsBefore.ChangeoverBoard.GuestDeparting[0].ReservationID != departingStay.ID {
		t.Fatalf("expected departingStay on Stage 1 Guest departing, got %+v", opsBefore.ChangeoverBoard.GuestDeparting)
	}
	if !opsBefore.ChangeoverBoard.GuestDeparting[0].SameDayArrival {
		t.Fatal("expected SameDayArrival=true when turnaroundArrival arrives on departing unit today")
	}
	if opsBefore.Occupancy.BlockedUnits != 1 || opsBefore.Occupancy.OccupiedUnits < 3 {
		t.Fatalf("unexpected occupancy counts: %+v", opsBefore.Occupancy)
	}
	assertHasAttentionCategory(t, opsBefore.AttentionItems, "MISSED_ARRIVAL")
	assertHasAttentionCategory(t, opsBefore.AttentionItems, "FAILED_PAYMENT")
	assertHasAttentionCategory(t, opsBefore.AttentionItems, "MAINTENANCE_BLOCK")

	// 6. Check out departingStay -> automatically creates CHANGEOVER_CLEAN in CLEANING_REQUIRED (NEVER READY).
	coBody, _ := json.Marshal(map[string]any{
		"status":  "CHECKED_OUT",
		"version": departingStay.Version,
	})
	coResp := authedJSON(t, staffClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations/"+departingStay.ID.String()+"/transition", staffCSRF, coBody)
	if coResp.StatusCode != http.StatusOK {
		t.Fatalf("checkout transition failed (%d): %s", coResp.StatusCode, readBodyStr(coResp))
	}
	coResp.Body.Close()

	opsAfterCO := fetchOpsJSON(t, staffClient, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/operations?date="+todayStr)
	if opsAfterCO.ChangeoverBoard.GuestDepartingCount != 0 {
		t.Fatalf("expected Stage 1 Guest departing empty after checkout, got %d", opsAfterCO.ChangeoverBoard.GuestDepartingCount)
	}
	if opsAfterCO.ChangeoverBoard.CleaningRequiredCount != 1 {
		t.Fatalf("expected 1 auto-created Cleaning required task on checkout, got %d", opsAfterCO.ChangeoverBoard.CleaningRequiredCount)
	}
	task := opsAfterCO.ChangeoverBoard.CleaningRequired[0]
	if task.UnitID != unit1.ID || task.Stage != domain.StageCleaningRequired || task.Priority != domain.TaskPriorityUrgent {
		t.Fatalf("unexpected auto-created task properties: %+v", task)
	}
	if !task.SameDayArrival {
		t.Fatal("expected auto-created turnaround task to flag SameDayArrival=true")
	}
	assertHasAttentionCategory(t, opsAfterCO.AttentionItems, "UNREADY_ARRIVAL_UNIT")

	// Verify unit1 is NOT automatically marked ready just because stay ended, and audit log exists.
	var hkAuditCount int
	if err := store.Pool().QueryRow(ctx, `
		SELECT COUNT(*) FROM audit_logs WHERE property_id = $1 AND action = 'housekeeping_task.auto_created' AND resource_id = $2
	`, prop.ID, task.ID.String()).Scan(&hkAuditCount); err != nil || hkAuditCount != 1 {
		t.Fatalf("expected 1 housekeeping_task.auto_created audit log, got count=%d err=%v", hkAuditCount, err)
	}

	// Attempting to skip CLEANING_REQUIRED -> READY must be rejected 422.
	skipBody, _ := json.Marshal(map[string]any{"stage": "READY", "version": task.Version})
	skipResp := authedJSON(t, staffClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/housekeeping/tasks/"+task.ID.String()+"/stage", staffCSRF, skipBody)
	if skipResp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 skipping CLEANING_REQUIRED -> READY, got %d", skipResp.StatusCode)
	}
	skipResp.Body.Close()

	// Assign task to staffUser, advance CLEANING_REQUIRED -> CLEANING_IN_PROGRESS -> INSPECTION_REQUIRED -> fail back -> re-advance -> READY.
	assignBody, _ := json.Marshal(map[string]any{"assigned_to": staffUser.ID, "version": task.Version})
	assignResp := authedJSON(t, staffClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/housekeeping/tasks/"+task.ID.String()+"/assign", staffCSRF, assignBody)
	if assignResp.StatusCode != http.StatusOK {
		t.Fatalf("assign task failed (%d): %s", assignResp.StatusCode, readBodyStr(assignResp))
	}
	var assignedTask domain.HousekeepingTask
	_ = json.NewDecoder(assignResp.Body).Decode(&assignedTask)
	assignResp.Body.Close()
	if assignedTask.AssigneeName != staffUser.FullName {
		t.Fatalf("expected assignee %q, got %q", staffUser.FullName, assignedTask.AssigneeName)
	}

	version := assignedTask.Version
	for _, st := range []string{"CLEANING_IN_PROGRESS", "INSPECTION_REQUIRED", "CLEANING_REQUIRED", "CLEANING_IN_PROGRESS", "INSPECTION_REQUIRED", "READY"} {
		stBody, _ := json.Marshal(map[string]any{"stage": st, "notes": "step " + st, "version": version})
		stResp := authedJSON(t, staffClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/housekeeping/tasks/"+task.ID.String()+"/stage", staffCSRF, stBody)
		if stResp.StatusCode != http.StatusOK {
			t.Fatalf("stage -> %s failed (%d): %s", st, stResp.StatusCode, readBodyStr(stResp))
		}
		var updatedTask domain.HousekeepingTask
		_ = json.NewDecoder(stResp.Body).Decode(&updatedTask)
		stResp.Body.Close()
		version = updatedTask.Version
	}

	opsReady := fetchOpsJSON(t, staffClient, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/operations?date="+todayStr)
	if opsReady.ChangeoverBoard.CleaningRequiredCount != 0 || opsReady.ChangeoverBoard.InspectionRequiredCount != 0 {
		t.Fatalf("expected open changeover stages cleared after READY, got clean=%d inspect=%d",
			opsReady.ChangeoverBoard.CleaningRequiredCount, opsReady.ChangeoverBoard.InspectionRequiredCount)
	}
	if len(opsReady.Arrivals) != 1 || opsReady.Arrivals[0].HasUnreadyUnit {
		t.Fatalf("expected turnaround arrival on unit1 to report ready unit after READY approval, got %+v", opsReady.Arrivals)
	}

	// 7. Maintenance blockout independence test: complete a cleaning task on blocked unit2 -> unit2 MUST stay BLOCKED in occupancy & public availability.
	createU2Body, _ := json.Marshal(map[string]any{
		"unit_id":        unit2.ID,
		"task_type":      "INSPECTION",
		"stage":          "CLEANING_REQUIRED",
		"priority":       "HIGH",
		"scheduled_date": todayStr,
		"title":          "Pre-reopen clean on maintenance unit",
	})
	u2CreateResp := authedJSON(t, staffClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/housekeeping/tasks", staffCSRF, createU2Body)
	if u2CreateResp.StatusCode != http.StatusCreated {
		t.Fatalf("create task on unit2 failed (%d): %s", u2CreateResp.StatusCode, readBodyStr(u2CreateResp))
	}
	var u2Task domain.HousekeepingTask
	_ = json.NewDecoder(u2CreateResp.Body).Decode(&u2Task)
	u2CreateResp.Body.Close()

	u2Ver := u2Task.Version
	for _, st := range []string{"CLEANING_IN_PROGRESS", "INSPECTION_REQUIRED", "READY"} {
		stBody, _ := json.Marshal(map[string]any{"stage": st, "version": u2Ver})
		stResp := authedJSON(t, staffClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/housekeeping/tasks/"+u2Task.ID.String()+"/stage", staffCSRF, stBody)
		if stResp.StatusCode != http.StatusOK {
			t.Fatalf("unit2 stage -> %s failed (%d)", st, stResp.StatusCode)
		}
		var up domain.HousekeepingTask
		_ = json.NewDecoder(stResp.Body).Decode(&up)
		stResp.Body.Close()
		u2Ver = up.Version
	}

	opsBlockedCheck := fetchOpsJSON(t, staffClient, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/operations?date="+todayStr)
	foundBlockedReady := false
	for _, uSnap := range opsBlockedCheck.Occupancy.Units {
		if uSnap.UnitID == unit2.ID {
			foundBlockedReady = true
			if uSnap.OccupancyState != "BLOCKED" || !uSnap.HasMaintenanceBlock || uSnap.HousekeepingStage != domain.StageReady {
				t.Fatalf("expected unit2 READY in housekeeping yet independently BLOCKED in occupancy, got %+v", uSnap)
			}
		}
	}
	if !foundBlockedReady {
		t.Fatal("expected unit2 in Occupancy.Units snapshot")
	}
	// Verify public availability remains blocked for unit2 despite housekeeping completion.
	pubAvailBody, _ := json.Marshal(map[string]any{
		"arrival_date":   todayStr,
		"departure_date": domain.FormatDate(tomorrow),
		"adults":         2,
	})
	pubAvailResp, err := http.Post(srv.URL+"/api/public/properties/"+prop.Slug+"/availability", "application/json", bytes.NewReader(pubAvailBody))
	if err != nil {
		t.Fatalf("public availability check: %v", err)
	}
	var pubAvail struct {
		AvailableUnitTypes []struct {
			UnitTypeID     uuid.UUID `json:"unit_type_id"`
			AvailableCount int       `json:"available_count"`
		} `json:"available_unit_types"`
	}
	_ = json.NewDecoder(pubAvailResp.Body).Decode(&pubAvail)
	pubAvailResp.Body.Close()
	for _, utAvail := range pubAvail.AvailableUnitTypes {
		if utAvail.UnitTypeID == unit2.UnitTypeID && unit2.UnitTypeID == unit1.UnitTypeID {
			// unit1 is claimed by turnaroundArrival, unit2 is MAINTENANCE blocked -> 0 available in that unit type
			if utAvail.AvailableCount != 0 {
				t.Fatalf("expected 0 public availability for blocked+occupied unit type, got %d", utAvail.AvailableCount)
			}
		}
	}

	// 8. Verify RBAC: Viewer can read Operations Centre (200) but cannot mutate housekeeping (403).
	viewerClient, viewerCSRF := loginAdminClient(t, srv.URL, "auditor@tirbach.wales", "viewer-ops-pass-2026")
	vOpsResp, err := viewerClient.Get(srv.URL + "/api/v1/properties/" + prop.ID.String() + "/operations?date=" + todayStr)
	if err != nil {
		t.Fatalf("viewer ops GET: %v", err)
	}
	vOpsResp.Body.Close()
	if vOpsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected viewer GET operations 200, got %d", vOpsResp.StatusCode)
	}
	vMutResp := authedJSON(t, viewerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/housekeeping/tasks", viewerCSRF, createU2Body)
	vMutResp.Body.Close()
	if vMutResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected viewer housekeeping POST 403 Forbidden, got %d", vMutResp.StatusCode)
	}

	// 9. Verify HTML WebUI Operations Centre rendering across all focus modes + interactive form Check-In returning to /.
	webJar, _ := cookiejar.New(nil)
	webClient := &http.Client{
		Jar: webJar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	loginForm := url.Values{}
	loginForm.Set("email", "warden@tirbach.wales")
	loginForm.Set("password", "warden-ops-pass-2026")
	wLogin, err := webClient.PostForm(srv.URL+"/login", loginForm)
	if err != nil {
		t.Fatalf("web login: %v", err)
	}
	wLogin.Body.Close()

	// Fetch /api/v1/auth/me to grab CSRF token for web form test.
	meResp, err := webClient.Get(srv.URL + "/api/v1/auth/me")
	if err != nil {
		t.Fatalf("web auth me: %v", err)
	}
	var mePayload struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.NewDecoder(meResp.Body).Decode(&mePayload)
	meResp.Body.Close()

	for _, focus := range []string{"all", "front_desk", "housekeeping", "attention"} {
		pageURL := srv.URL + "/?date=" + todayStr + "&focus=" + focus
		pResp, err := webClient.Get(pageURL)
		if err != nil {
			t.Fatalf("GET %s: %v", pageURL, err)
		}
		html := readBodyStr(pResp)
		pResp.Body.Close()
		if pResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d", pageURL, pResp.StatusCode)
		}
		if !strings.Contains(html, "Operations Centre") || !strings.Contains(html, "LOVELACE") {
			t.Fatalf("missing Operations Centre / LOVELACE branding on %s", pageURL)
		}
		if focus == "housekeeping" && !strings.Contains(html, "Daily Changeover Board") {
			t.Fatalf("expected Daily Changeover Board on %s", pageURL)
		}
	}

	// Perform 1-click Web UI Check-In on turnaroundArrival with return_to back to Operations Centre.
	retTarget := "/?date=" + todayStr
	ciForm := url.Values{}
	ciForm.Set("csrf_token", mePayload.CSRFToken)
	ciForm.Set("status", "CHECKED_IN")
	ciForm.Set("version", "1")
	ciForm.Set("return_to", retTarget)
	ciResp, err := webClient.PostForm(srv.URL+"/reservations/"+turnaroundArrival.ID.String()+"/transition", ciForm)
	if err != nil {
		t.Fatalf("webui check-in POST: %v", err)
	}
	ciResp.Body.Close()
	if ciResp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(ciResp.Header.Get("Location"), "/?date="+todayStr) {
		t.Fatalf("expected 303 redirect back to Operations Centre, got status=%d location=%q", ciResp.StatusCode, ciResp.Header.Get("Location"))
	}

	// Ensure Date selector can view an alternate operational date cleanly.
	nextOps := fetchOpsJSON(t, staffClient, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/operations?date="+domain.FormatDate(tomorrow))
	if domain.FormatDate(nextOps.OperationalDate) != domain.FormatDate(tomorrow) {
		t.Fatalf("expected operational date %s, got %s", domain.FormatDate(tomorrow), domain.FormatDate(nextOps.OperationalDate))
	}
	if nextOps.Summary.DeparturesTotal < 1 {
		t.Fatalf("expected at least 1 scheduled departure tomorrow (%s), got %d", inHouseStay.Reference, nextOps.Summary.DeparturesTotal)
	}
}

func loginAdminClient(t *testing.T, baseURL, email, password string) (*http.Client, string) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	resp, err := client.Post(baseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login %s: %v", email, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s failed (%d)", email, resp.StatusCode)
	}
	var out struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return client, out.CSRFToken
}

func authedJSON(t *testing.T, client *http.Client, method, targetURL, csrf string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, targetURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build req %s %s: %v", method, targetURL, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, targetURL, err)
	}
	return resp
}

func fetchOpsJSON(t *testing.T, client *http.Client, targetURL string) domain.OperationsCentreData {
	t.Helper()
	resp, err := client.Get(targetURL)
	if err != nil {
		t.Fatalf("GET %s: %v", targetURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s failed (%d): %s", targetURL, resp.StatusCode, readBodyStr(resp))
	}
	var out domain.OperationsCentreData
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode ops payload: %v", err)
	}
	return out
}

func assertHasAttentionCategory(t *testing.T, items []domain.OpsAttentionItem, cat string) {
	t.Helper()
	for _, it := range items {
		if it.Category == cat {
			return
		}
	}
	t.Fatalf("expected attention category %s in %+v", cat, items)
}

func readBodyStr(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestPhase31HousekeepingInspectionTimestampsAndReclean(t *testing.T) {
	srv, store, svc, prop, _, _, _ := setupPhase2TestServer(t, "pub-p31-insp-secret")
	defer srv.Close()

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	managerUser, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email:    "manager-p31@tirbach.wales",
		FullName: "Nia Manager",
		Role:     domain.RoleManager,
		Password: "manager-ops-pass-2026",
	})
	if err != nil {
		t.Fatalf("create manager user: %v", err)
	}
	managerActor := service.Actor{UserID: managerUser.ID, Email: managerUser.Email, FullName: managerUser.FullName, Role: managerUser.Role}

	units, err := svc.ListUnits(ctx, ownerActor, prop.ID, nil)
	if err != nil || len(units) == 0 {
		t.Fatalf("list units: %v", err)
	}
	unit := units[0]
	today := time.Now().UTC()

	task, err := svc.CreateHousekeepingTask(
		ctx, ownerActor, prop.ID, unit.ID,
		domain.TaskTypeChangeoverClean, domain.StageCleaningRequired, domain.TaskPriorityHigh,
		today, "Deep changeover clean", "", nil,
	)
	if err != nil {
		t.Fatalf("create housekeeping task: %v", err)
	}
	if task.InspectedAt != nil || task.InspectedBy != nil {
		t.Fatalf("expected new task not to have inspected_at/inspected_by, got at=%v by=%v", task.InspectedAt, task.InspectedBy)
	}

	task, err = svc.TransitionHousekeepingTaskStage(ctx, ownerActor, prop.ID, task.ID, domain.StageCleaningInProgress, "Started", task.Version)
	if err != nil {
		t.Fatalf("transition to cleaning_in_progress: %v", err)
	}
	task, err = svc.TransitionHousekeepingTaskStage(ctx, ownerActor, prop.ID, task.ID, domain.StageInspectionRequired, "Ready for check", task.Version)
	if err != nil {
		t.Fatalf("transition to inspection_required: %v", err)
	}
	if task.InspectedAt != nil || task.InspectedBy != nil || task.CompletedAt != nil {
		t.Fatalf("entering INSPECTION_REQUIRED must NOT record inspection completion, got inspected_at=%v inspected_by=%v completed_at=%v",
			task.InspectedAt, task.InspectedBy, task.CompletedAt)
	}

	// First inspection sign-off by ownerActor -> READY must record inspected_at and inspected_by.
	task, err = svc.TransitionHousekeepingTaskStage(ctx, ownerActor, prop.ID, task.ID, domain.StageReady, "First inspection passed", task.Version)
	if err != nil {
		t.Fatalf("transition to ready #1: %v", err)
	}
	if task.InspectedAt == nil || task.InspectedBy == nil || *task.InspectedBy != owner.ID {
		t.Fatalf("READY must record inspected_at and owner in inspected_by, got at=%v by=%v", task.InspectedAt, task.InspectedBy)
	}
	if task.InspectedByName != owner.FullName || task.CompletedAt == nil || task.CompletedBy == nil || *task.CompletedBy != owner.ID {
		t.Fatalf("READY completion metadata mismatch: %+v", task)
	}
	firstInspectedAt := *task.InspectedAt

	// Return previously completed task to CLEANING_REQUIRED -> clears current inspection state.
	task, err = svc.TransitionHousekeepingTaskStage(ctx, ownerActor, prop.ID, task.ID, domain.StageCleaningRequired, "Muddy footprint found — re-clean", task.Version)
	if err != nil {
		t.Fatalf("return READY -> CLEANING_REQUIRED: %v", err)
	}
	if task.InspectedAt != nil || task.InspectedBy != nil || task.InspectedByName != "" || task.CompletedAt != nil || task.CompletedBy != nil {
		t.Fatalf("returning to CLEANING_REQUIRED must clear inspection state, got at=%v by=%v name=%q completed=%v",
			task.InspectedAt, task.InspectedBy, task.InspectedByName, task.CompletedAt)
	}

	// Preserve historical transitions (including prior READY inspected_at/inspected_by) in audit_logs.
	var auditCount int
	var firstReadyPayload string
	if err := store.Pool().QueryRow(ctx, `
		SELECT COUNT(*),
		       COALESCE(MAX(CASE WHEN after_state->>'stage' = 'READY' THEN after_state::text ELSE '' END), '')
		FROM audit_logs
		WHERE resource_type = 'housekeeping_task' AND resource_id = $1 AND action = 'housekeeping_task.stage_changed'
	`, task.ID.String()).Scan(&auditCount, &firstReadyPayload); err != nil {
		t.Fatalf("query housekeeping audit logs: %v", err)
	}
	if auditCount < 4 || !strings.Contains(firstReadyPayload, owner.ID.String()) || !strings.Contains(firstReadyPayload, `"inspected_at"`) {
		t.Fatalf("expected >=4 immutable stage_changed audit entries preserving first inspection, got count=%d payload=%s", auditCount, firstReadyPayload)
	}

	// Second cleaning + inspection cycle by a different admin (managerActor).
	time.Sleep(15 * time.Millisecond)
	task, err = svc.TransitionHousekeepingTaskStage(ctx, managerActor, prop.ID, task.ID, domain.StageCleaningInProgress, "Re-cleaning", task.Version)
	if err != nil {
		t.Fatalf("reclean start: %v", err)
	}
	task, err = svc.TransitionHousekeepingTaskStage(ctx, managerActor, prop.ID, task.ID, domain.StageInspectionRequired, "Awaiting second inspection", task.Version)
	if err != nil {
		t.Fatalf("reclean awaiting inspection: %v", err)
	}
	if task.InspectedAt != nil || task.InspectedBy != nil {
		t.Fatalf("second INSPECTION_REQUIRED must still have nil inspected_at/inspected_by, got at=%v by=%v", task.InspectedAt, task.InspectedBy)
	}

	task, err = svc.TransitionHousekeepingTaskStage(ctx, managerActor, prop.ID, task.ID, domain.StageReady, "Second inspection passed", task.Version)
	if err != nil {
		t.Fatalf("second transition to READY: %v", err)
	}
	if task.InspectedAt == nil || !task.InspectedAt.After(firstInspectedAt) {
		t.Fatalf("expected new inspected_at strictly after first (%v), got %v", firstInspectedAt, task.InspectedAt)
	}
	if task.InspectedBy == nil || *task.InspectedBy != managerUser.ID || task.InspectedByName != managerUser.FullName {
		t.Fatalf("expected second inspector %s (%s), got by=%v name=%q", managerUser.ID, managerUser.FullName, task.InspectedBy, task.InspectedByName)
	}
}

func TestPhase31PaymentExceptionDestinationAndRBAC(t *testing.T) {
	srv, store, svc, prop, _, _, _ := setupPhase2TestServer(t, "pub-p31-payex-secret")
	defer srv.Close()

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	owner2User, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email:    "owner2-p31@tirbach.wales",
		FullName: "Sian Owner Two",
		Role:     domain.RoleOwnerAdmin,
		Password: "owner2-ops-pass-2026",
	})
	if err != nil {
		t.Fatalf("create owner2: %v", err)
	}
	viewerUser, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email:    "viewer-p31@tirbach.wales",
		FullName: "Bryn Viewer",
		Role:     domain.RoleViewer,
		Password: "viewer-ops-pass-2026",
	})
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	viewerActor := service.Actor{UserID: viewerUser.ID, Email: viewerUser.Email, FullName: viewerUser.FullName, Role: viewerUser.Role}

	attID, err := postgres.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	exID, err := postgres.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO payment_attempts (
			id, property_id, hold_id, reservation_id, provider, mode,
			payment_type, amount_pence, currency, status, idempotency_key, failure_reason
		) VALUES ($1, $2, NULL, NULL, 'stripe', 'checkout_session', 'FULL', 14500, 'GBP', 'RECONCILIATION_REQUIRED', $3, 'hold expired before webhook')
	`, attID, prop.ID, "p31-idem-"+attID.String()); err != nil {
		t.Fatalf("insert payment_attempt: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO payment_exceptions (
			id, property_id, hold_id, payment_attempt_id, reservation_id,
			exception_type, amount_pence, currency, details, status
		) VALUES ($1, $2, NULL, $3, NULL, 'EXPIRED_HOLD_PAID', 14500, 'GBP', 'Orphan payment from expired checkout hold', 'OPEN')
	`, exID, prop.ID, attID); err != nil {
		t.Fatalf("insert orphan payment_exception: %v", err)
	}

	// Operations Centre must point orphan exception to a functional /payment-exceptions destination, never broken /property#payment-exceptions.
	ownerOps, err := svc.GetOperationsCentre(ctx, ownerActor, prop.ID, nil, "")
	if err != nil {
		t.Fatalf("owner GetOperationsCentre: %v", err)
	}
	var foundURL, foundLabel string
	for _, item := range ownerOps.AttentionItems {
		if item.Category == "PAYMENT_EXCEPTION" {
			foundURL = item.ActionURL
			foundLabel = item.ActionLabel
			break
		}
	}
	wantURL := "/payment-exceptions#ex-" + exID.String()
	if foundURL != wantURL || foundLabel != "Resolve payment" {
		t.Fatalf("expected ActionURL=%q ActionLabel=%q, got url=%q label=%q", wantURL, "Resolve payment", foundURL, foundLabel)
	}

	// Read-only viewer gets non-mutating View exception label.
	viewerOps, err := svc.GetOperationsCentre(ctx, viewerActor, prop.ID, nil, "")
	if err != nil {
		t.Fatalf("viewer GetOperationsCentre: %v", err)
	}
	var viewerLabel string
	for _, item := range viewerOps.AttentionItems {
		if item.Category == "PAYMENT_EXCEPTION" {
			viewerLabel = item.ActionLabel
		}
	}
	if viewerLabel != "View exception" {
		t.Fatalf("expected read-only ActionLabel 'View exception' for viewer, got %q", viewerLabel)
	}

	ownerClient, ownerCSRF := loginAdminClient(t, srv.URL, owner2User.Email, "owner2-ops-pass-2026")
	viewerClient, viewerCSRF := loginAdminClient(t, srv.URL, viewerUser.Email, "viewer-ops-pass-2026")

	// Destination route renders the exception row and resolve workflow without 404/dead links.
	pageResp, err := ownerClient.Get(srv.URL + wantURL)
	if err != nil {
		t.Fatalf("GET %s: %v", wantURL, err)
	}
	pageHTML := readBodyStr(pageResp)
	pageResp.Body.Close()
	if pageResp.StatusCode != http.StatusOK || !strings.Contains(pageHTML, `id="ex-`+exID.String()+`"`) || !strings.Contains(pageHTML, "EXPIRED_HOLD_PAID") {
		t.Fatalf("expected 200 /payment-exceptions page containing ex row, status=%d", pageResp.StatusCode)
	}

	// /property also preserves id="payment-exceptions" anchor and links to resolution.
	propResp, err := ownerClient.Get(srv.URL + "/property")
	if err != nil {
		t.Fatalf("GET /property: %v", err)
	}
	propHTML := readBodyStr(propResp)
	propResp.Body.Close()
	if propResp.StatusCode != http.StatusOK || !strings.Contains(propHTML, `id="payment-exceptions"`) || !strings.Contains(propHTML, "/payment-exceptions#ex-"+exID.String()) {
		t.Fatalf("expected /property to expose #payment-exceptions anchor and resolve link, status=%d", propResp.StatusCode)
	}

	// Viewer attempting to resolve via JSON API or WebUI must receive 403 Forbidden.
	apiURL := srv.URL + "/api/v1/properties/" + prop.ID.String() + "/payment-exceptions/" + exID.String() + "/resolve"
	vAPIResp := authedJSON(t, viewerClient, http.MethodPost, apiURL, viewerCSRF, []byte(`{"resolution_notes":"attempt"}`))
	vAPIResp.Body.Close()
	if vAPIResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer resolving payment exception via API, got %d", vAPIResp.StatusCode)
	}

	vForm := url.Values{"csrf_token": {viewerCSRF}, "resolution_notes": {"attempt"}}
	vWebResp, err := viewerClient.PostForm(srv.URL+"/payment-exceptions/"+exID.String()+"/resolve", vForm)
	if err != nil {
		t.Fatalf("viewer webui resolve POST: %v", err)
	}
	vWebResp.Body.Close()
	if vWebResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer resolving via webui, got %d", vWebResp.StatusCode)
	}

	// Empty resolution_notes must fail validation (422).
	emptyResp := authedJSON(t, ownerClient, http.MethodPost, apiURL, ownerCSRF, []byte(`{"resolution_notes":"   "}`))
	emptyResp.Body.Close()
	if emptyResp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for blank resolution_notes, got %d", emptyResp.StatusCode)
	}

	// Owner resolves via WebUI form; redirect must succeed and update status + audit log.
	noRedirectClient := &http.Client{
		Jar: ownerClient.Jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resForm := url.Values{
		"csrf_token":       {ownerCSRF},
		"resolution_notes": {"Refunded in Stripe pi_test_123"},
		"return_to":        {wantURL},
	}
	resResp, err := noRedirectClient.PostForm(srv.URL+"/payment-exceptions/"+exID.String()+"/resolve", resForm)
	if err != nil {
		t.Fatalf("owner resolve POST: %v", err)
	}
	resResp.Body.Close()
	if resResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on resolve POST, got %d", resResp.StatusCode)
	}

	// Verify open exception alert cleared from Operations Centre and audit log recorded.
	afterOps, err := svc.GetOperationsCentre(ctx, ownerActor, prop.ID, nil, "")
	if err != nil {
		t.Fatalf("after GetOperationsCentre: %v", err)
	}
	for _, item := range afterOps.AttentionItems {
		if item.Category == "PAYMENT_EXCEPTION" {
			t.Fatalf("expected resolved payment exception no longer in open attention items, got %+v", item)
		}
	}
	var resolvedAuditCount int
	if err := store.Pool().QueryRow(ctx, `
		SELECT COUNT(*) FROM audit_logs
		WHERE resource_type = 'payment_exception' AND resource_id = $1 AND action = 'payment_exception.resolved'
	`, exID.String()).Scan(&resolvedAuditCount); err != nil || resolvedAuditCount != 1 {
		t.Fatalf("expected 1 payment_exception.resolved audit entry, got count=%d err=%v", resolvedAuditCount, err)
	}
	if _, err := svc.ResolvePaymentException(ctx, ownerActor, prop.ID, exID, "duplicate"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict resolving already closed exception, got %v", err)
	}
}

func TestPhase31InquiryPendingConfirmedClassification(t *testing.T) {
	srv, store, svc, prop, _, _, _ := setupPhase2TestServer(t, "pub-p31-inq-secret")
	defer srv.Close()

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	units, err := svc.ListUnits(ctx, ownerActor, prop.ID, nil)
	if err != nil || len(units) < 4 {
		t.Fatalf("expected >=4 units, got len=%d err=%v", len(units), err)
	}

	loc, _ := time.LoadLocation(prop.Timezone)
	if loc == nil {
		loc = time.UTC
	}
	nowLocal := time.Now().In(loc)
	today := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, time.UTC)
	tomorrow := today.AddDate(0, 0, 1)
	dayAfter := today.AddDate(0, 0, 2)

	confirmedRes, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Confirmed Cariad",
		GuestEmail:    "cariad@example.test",
		ChannelCode:   "DIRECT",
		ArrivalDate:   today,
		DepartureDate: tomorrow,
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{units[0].ID},
	})
	if err != nil {
		t.Fatalf("create confirmed arrival: %v", err)
	}

	pendingRes, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Pending Dewi",
		GuestEmail:    "dewi@example.test",
		ChannelCode:   "DIRECT",
		ArrivalDate:   today,
		DepartureDate: tomorrow,
		Adults:        2,
		Status:        domain.ResStatusPending,
		UnitIDs:       []uuid.UUID{units[1].ID},
	})
	if err != nil {
		t.Fatalf("create pending arrival: %v", err)
	}

	inquiryToday, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Inquiry Elen",
		GuestEmail:    "elen@example.test",
		ChannelCode:   "DIRECT",
		ArrivalDate:   today,
		DepartureDate: tomorrow,
		Adults:        2,
		Status:        domain.ResStatusInquiry,
		UnitIDs:       []uuid.UUID{units[2].ID},
	})
	if err != nil {
		t.Fatalf("create today inquiry: %v", err)
	}

	inquiryFuture, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Inquiry Harri",
		GuestEmail:    "harri@example.test",
		ChannelCode:   "DIRECT",
		ArrivalDate:   tomorrow,
		DepartureDate: dayAfter,
		Adults:        2,
		Status:        domain.ResStatusInquiry,
		UnitIDs:       []uuid.UUID{units[3].ID},
	})
	if err != nil {
		t.Fatalf("create upcoming inquiry: %v", err)
	}

	ops, err := svc.GetOperationsCentre(ctx, ownerActor, prop.ID, &today, "")
	if err != nil {
		t.Fatalf("GetOperationsCentre: %v", err)
	}

	// Daily Arrival KPI counts ONLY accepted arrivals (CONFIRMED / CHECKED_IN).
	if ops.Summary.ArrivalsTotal != 1 || ops.Occupancy.ArrivalsToday != 1 || len(ops.Arrivals) != 1 {
		t.Fatalf("expected ArrivalsTotal=1, Occupancy.ArrivalsToday=1, len(Arrivals)=1; got summary=%d occ=%d len=%d",
			ops.Summary.ArrivalsTotal, ops.Occupancy.ArrivalsToday, len(ops.Arrivals))
	}
	if ops.Arrivals[0].ReservationID != confirmedRes.ID {
		t.Fatalf("expected CONFIRMED reservation %s in Arrivals, got %s (%s)", confirmedRes.ID, ops.Arrivals[0].ReservationID, ops.Arrivals[0].Status)
	}

	if ops.Summary.PendingArrivalsCount != 1 || len(ops.PendingArrivals) != 1 || ops.PendingArrivals[0].ReservationID != pendingRes.ID {
		t.Fatalf("expected PendingArrivals to contain only %s, got count=%d items=%+v", pendingRes.ID, ops.Summary.PendingArrivalsCount, ops.PendingArrivals)
	}
	if ops.Summary.InquiriesCount != 2 || len(ops.Inquiries) != 2 {
		t.Fatalf("expected InquiriesCount=2, got count=%d items=%+v", ops.Summary.InquiriesCount, ops.Inquiries)
	}
	assertHasAttentionCategory(t, ops.AttentionItems, "PENDING_CONFIRMATION")
	assertHasAttentionCategory(t, ops.AttentionItems, "INQUIRY_FOLLOW_UP")

	// Inquiries must NOT hold inventory or appear as MISSED_ARRIVAL / OUTSTANDING_BALANCE.
	for _, it := range ops.AttentionItems {
		if it.ReservationID != nil && (*it.ReservationID == inquiryToday.ID || *it.ReservationID == inquiryFuture.ID) {
			if it.Category == "MISSED_ARRIVAL" || it.Category == "OUTSTANDING_BALANCE" || it.Category == "UNREADY_ARRIVAL_UNIT" {
				t.Fatalf("inquiry %s unexpectedly triggered %s alert", *it.ReservationID, it.Category)
			}
		}
	}
	for _, u := range ops.Occupancy.Units {
		if u.UnitID == units[2].ID && u.OccupancyState != "AVAILABLE" {
			t.Fatalf("unit with INQUIRY only must remain AVAILABLE, got %s", u.OccupancyState)
		}
		if u.UnitID == units[1].ID && u.OccupancyState != "PROVISIONAL" {
			t.Fatalf("unit with PENDING hold must show PROVISIONAL occupancy state, got %s", u.OccupancyState)
		}
	}

	// Render Operations Centre HTML to verify separate panels.
	opViewer, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email:    "ops-p31-inq@tirbach.wales",
		FullName: "Seren Operator",
		Role:     domain.RoleManager,
		Password: "manager-inq-pass-2026",
	})
	if err != nil {
		t.Fatalf("create operator: %v", err)
	}
	ownerClient, _ := loginAdminClient(t, srv.URL, opViewer.Email, "manager-inq-pass-2026")
	resp, err := ownerClient.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	dashHTML := readBodyStr(resp)
	resp.Body.Close()
	for _, want := range []string{
		"Confirmed Arrivals (1)",
		"Pending / Provisional Arrivals (1)",
		"Inquiries Requiring Follow-Up (2)",
		"Confirmed Cariad",
		"Pending Dewi",
		"Inquiry Elen",
		"Inquiry Harri",
	} {
		if !strings.Contains(dashHTML, want) {
			t.Fatalf("expected dashboard HTML to contain %q", want)
		}
	}

	// Confirming an inquiry uses existing reservation lifecycle and immediately moves it into Confirmed Arrivals.
	if _, err := svc.TransitionReservationStatus(ctx, ownerActor, prop.ID, inquiryToday.ID, domain.ResStatusConfirmed, "Guest confirmed over phone", inquiryToday.Version); err != nil {
		t.Fatalf("confirm inquiryToday via existing lifecycle: %v", err)
	}
	opsAfter, err := svc.GetOperationsCentre(ctx, ownerActor, prop.ID, &today, "")
	if err != nil {
		t.Fatalf("GetOperationsCentre after confirm: %v", err)
	}
	if opsAfter.Summary.ArrivalsTotal != 2 || opsAfter.Summary.InquiriesCount != 1 {
		t.Fatalf("after confirming today's inquiry, expected ArrivalsTotal=2 InquiriesCount=1, got %d / %d",
			opsAfter.Summary.ArrivalsTotal, opsAfter.Summary.InquiriesCount)
	}
}
