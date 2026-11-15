package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

func uploadMultipartAttachment(
	t *testing.T,
	client *http.Client,
	endpoint, csrfToken, fieldName, filename string,
	payload []byte,
	extraFields map[string]string,
) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range extraFields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write multipart field %s: %v", k, err)
		}
	}
	fw, err := mw.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, &buf)
	if err != nil {
		t.Fatalf("new upload request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if csrfToken != "" {
		req.Header.Set("X-CSRF-Token", csrfToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload request failed: %v", err)
	}
	return resp
}

func TestPhase3Milestone2MaintenanceLifecycleAndInventoryIntegration(t *testing.T) {
	srv, store, svc, prop, _, _, _ := setupPhase2TestServer(t, "pub-p3-m2-life-secret")
	defer srv.Close()

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	techUser, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email:    "tech@tirbach.wales",
		FullName: "Dafydd Technician",
		Role:     domain.RoleMaintenanceStaff,
		Password: "maint-tech-pass-2026",
	})
	if err != nil {
		t.Fatalf("create maintenance_staff user: %v", err)
	}

	units, err := svc.ListUnits(ctx, ownerActor, prop.ID, nil)
	if err != nil || len(units) < 2 {
		t.Fatalf("expected seeded units >= 2, got %d err=%v", len(units), err)
	}
	unit1 := units[0]
	unit2 := units[1]

	cats, err := svc.ListMaintenanceCategories(ctx, ownerActor, prop.ID, false)
	if err != nil || len(cats) < 20 {
		t.Fatalf("expected >= 20 seeded categories, got %d err=%v", len(cats), err)
	}
	locs, err := svc.ListMaintenanceLocations(ctx, ownerActor, prop.ID, false)
	if err != nil || len(locs) < 10 {
		t.Fatalf("expected >= 10 seeded locations, got %d err=%v", len(locs), err)
	}
	var plumbingCatID, showerLocID uuid.UUID
	for _, c := range cats {
		if c.Code == "PLUMBING" {
			plumbingCatID = c.ID
		}
	}
	for _, l := range locs {
		if l.Code == "SHOWER_BLOCK" {
			showerLocID = l.ID
		}
	}
	if plumbingCatID == uuid.Nil || showerLocID == uuid.Nil {
		t.Fatalf("expected PLUMBING category and SHOWER_BLOCK location to exist")
	}

	// Register an asset and contractor.
	asset, err := svc.CreateMaintenanceAsset(ctx, ownerActor, domain.MaintenanceAsset{
		PropertyID:    prop.ID,
		AssetCode:     "BOILER-SHOWER-01",
		Name:          "Shower Block Combi Boiler",
		LocationID:    &showerLocID,
		AssetCategory: "Heating / Boiler",
		Manufacturer:  "Worcester Bosch",
		Model:         "Greenstar 4000",
		SerialNumber:  "WB-2026-991",
		Status:        domain.AssetStatusActive,
	})
	if err != nil {
		t.Fatalf("create asset: %v", err)
	}
	contractor, err := svc.CreateMaintenanceContractor(ctx, ownerActor, domain.MaintenanceContractor{
		PropertyID: prop.ID,
		Name:       "Owen Davies",
		Company:    "Cymru Plumbing & Gas",
		Trade:      "Gas Safe Engineer",
		Phone:      "01437 555019",
		IsActive:   true,
	})
	if err != nil {
		t.Fatalf("create contractor: %v", err)
	}

	// Reject request missing location/unit/asset or missing title.
	if _, err := svc.CreateMaintenanceRequest(ctx, ownerActor, service.CreateMaintenanceRequestInput{
		PropertyID: prop.ID,
		Title:      "No target selected",
		CategoryID: plumbingCatID,
		Priority:   domain.MaintPriorityNormal,
	}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected validation error when no location/unit/asset given, got %v", err)
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	dueSoon := today.AddDate(0, 0, -1) // overdue target date

	// 1. Create location + asset-linked urgent/safety request (no unit!).
	facilityReq, err := svc.CreateMaintenanceRequest(ctx, ownerActor, service.CreateMaintenanceRequestInput{
		PropertyID:         prop.ID,
		Title:              "Low pressure lockout on Shower Block Boiler",
		Description:        "Pressure gauge dropped below 0.5 bar in boiler room.",
		CategoryID:         plumbingCatID,
		Priority:           domain.MaintPriorityUrgent,
		LocationID:         &showerLocID,
		AssetID:            &asset.ID,
		TargetDate:         &dueSoon,
		GuestImpact:        true,
		SafetyImpact:       true,
		InspectionRequired: true,
	})
	if err != nil {
		t.Fatalf("create facility/asset request: %v", err)
	}
	if !strings.HasPrefix(facilityReq.Request.Reference, "MNT-") || facilityReq.Request.Status != domain.MaintStatusReported {
		t.Fatalf("unexpected request ref/status: %+v", facilityReq.Request)
	}

	// Verify asset maintenance history includes facilityReq.
	_, assetHist, err := svc.GetMaintenanceAssetWithHistory(ctx, ownerActor, prop.ID, asset.ID)
	if err != nil || len(assetHist) != 1 || assetHist[0].ID != facilityReq.Request.ID {
		t.Fatalf("expected 1 maintenance item on asset history, got len=%d err=%v", len(assetHist), err)
	}

	// Assign to internal maintenance_staff + external contractor, progress REPORTED -> TRIAGED -> SCHEDULED -> IN_PROGRESS -> COMPLETED.
	assigned, err := svc.AssignMaintenanceRequest(ctx, ownerActor, prop.ID, facilityReq.Request.ID, &techUser.ID, &contractor.ID, nil, "Isolate cold feed before repressurising", facilityReq.Request.Version)
	if err != nil {
		t.Fatalf("assign request: %v", err)
	}
	ver := assigned.Request.Version
	for _, st := range []domain.MaintenanceStatus{domain.MaintStatusTriaged, domain.MaintStatusScheduled, domain.MaintStatusInProgress} {
		det, err := svc.TransitionMaintenanceStatus(ctx, ownerActor, prop.ID, facilityReq.Request.ID, st, "Progressing "+string(st), nil, ver)
		if err != nil {
			t.Fatalf("transition to %s: %v", st, err)
		}
		ver = det.Request.Version
	}

	// Append work notes and verify preserved.
	if _, err := svc.AddMaintenanceWorkEntry(ctx, ownerActor, prop.ID, facilityReq.Request.ID, domain.WorkEntryDiagnosis, "Found weeping PRV seat", "15mm PRV cartridge"); err != nil {
		t.Fatalf("add diagnosis work entry: %v", err)
	}
	detAfterNote, err := svc.GetMaintenanceRequestDetail(ctx, ownerActor, prop.ID, facilityReq.Request.ID)
	if err != nil || len(detAfterNote.WorkEntries) != 1 {
		t.Fatalf("expected 1 work entry, got %d err=%v", len(detAfterNote.WorkEntries), err)
	}
	completedDet, err := svc.TransitionMaintenanceStatus(ctx, ownerActor, prop.ID, facilityReq.Request.ID, domain.MaintStatusCompleted, "Replaced PRV and bled radiators", nil, detAfterNote.Request.Version)
	if err != nil {
		t.Fatalf("complete request: %v", err)
	}

	// Owner records FAILED inspection -> returns automatically to IN_PROGRESS and keeps all notes!
	failedDet, _, err := svc.RecordMaintenanceInspection(ctx, ownerActor, prop.ID, facilityReq.Request.ID, domain.MaintInspectionFailed, "Slight weep still visible on union nut", false, completedDet.Request.Version)
	if err != nil {
		t.Fatalf("failed inspection: %v", err)
	}
	if failedDet.Request.Status != domain.MaintStatusInProgress || len(failedDet.WorkEntries) != 1 || len(failedDet.Inspections) != 1 {
		t.Fatalf("expected IN_PROGRESS with preserved work entries & inspection, got status=%s notes=%d insps=%d", failedDet.Request.Status, len(failedDet.WorkEntries), len(failedDet.Inspections))
	}

	// Tech logs follow-up, completes again -> Manager passes inspection & closes -> Reopen preserves everything.
	if _, err := svc.AddMaintenanceWorkEntry(ctx, ownerActor, prop.ID, facilityReq.Request.ID, domain.WorkEntryPerformed, "Re-tightened union nut with new fibre washer", "3/4 fibre washer"); err != nil {
		t.Fatalf("add repair entry: %v", err)
	}
	det2, _ := svc.GetMaintenanceRequestDetail(ctx, ownerActor, prop.ID, facilityReq.Request.ID)
	comp2, err := svc.TransitionMaintenanceStatus(ctx, ownerActor, prop.ID, facilityReq.Request.ID, domain.MaintStatusCompleted, "Pressure held steady for 2 hours", nil, det2.Request.Version)
	if err != nil {
		t.Fatalf("re-complete request: %v", err)
	}
	passedDet, _, err := svc.RecordMaintenanceInspection(ctx, ownerActor, prop.ID, facilityReq.Request.ID, domain.MaintInspectionPassed, "Dry and holding 1.5 bar", true, comp2.Request.Version)
	if err != nil {
		t.Fatalf("pass inspection: %v", err)
	}
	if passedDet.Request.Status != domain.MaintStatusClosed || passedDet.Request.ClosedAt == nil {
		t.Fatalf("expected CLOSED after passed+close inspection, got %s", passedDet.Request.Status)
	}
	reopenedDet, err := svc.TransitionMaintenanceStatus(ctx, ownerActor, prop.ID, facilityReq.Request.ID, domain.MaintStatusInProgress, "Reopened for annual flange check", nil, passedDet.Request.Version)
	if err != nil {
		t.Fatalf("reopen closed request: %v", err)
	}
	if reopenedDet.Request.Status != domain.MaintStatusInProgress || len(reopenedDet.WorkEntries) < 3 || len(reopenedDet.Inspections) != 2 {
		t.Fatalf("expected reopened request preserving work log & inspections, got status=%s entries=%d insps=%d", reopenedDet.Request.Status, len(reopenedDet.WorkEntries), len(reopenedDet.Inspections))
	}

	// 2. Inventory Restriction Integration & Booking Conflict Protection.
	// Create a confirmed booking on unit2 so we can verify maintenance CANNOT displace existing bookings.
	resStart := today.AddDate(0, 0, 5)
	resEnd := today.AddDate(0, 0, 8)
	confirmedRes, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Sian Booked",
		GuestEmail:    "sian@example.test",
		ChannelCode:   "DIRECT",
		ArrivalDate:   resStart,
		DepartureDate: resEnd,
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unit2.ID},
	})
	if err != nil {
		t.Fatalf("create confirmed booking on unit2: %v", err)
	}
	unit2Maint, err := svc.CreateMaintenanceRequest(ctx, ownerActor, service.CreateMaintenanceRequestInput{
		PropertyID:   prop.ID,
		Title:        "Replace window catch on " + unit2.Code,
		CategoryID:   plumbingCatID,
		Priority:     domain.MaintPriorityHigh,
		UnitID:       &unit2.ID,
		TargetDate:   &resStart,
		GuestImpact:  true,
		SafetyImpact: false,
	})
	if err != nil {
		t.Fatalf("create unit2 maintenance: %v", err)
	}
	if _, err := svc.CreateMaintenanceRestriction(ctx, ownerActor, prop.ID, unit2Maint.Request.ID, resStart, resEnd, "Attempt conflicting closure"); !errors.Is(err, domain.ErrOverlap) {
		t.Fatalf("expected ErrOverlap when blocking already-booked unit2, got %v", err)
	}
	// Verify confirmedRes remains CONFIRMED untouched.
	stillConfirmed, err := svc.GetReservation(ctx, ownerActor, prop.ID, confirmedRes.ID)
	if err != nil || stillConfirmed.Status != domain.ResStatusConfirmed {
		t.Fatalf("existing confirmed reservation was affected: %+v err=%v", stillConfirmed, err)
	}

	// Now create a unit-linked maintenance request with explicit inventory restriction on unit1 (today+10 .. today+13).
	lockStart := today.AddDate(0, 0, 10)
	lockEnd := today.AddDate(0, 0, 13)
	unit1Maint, err := svc.CreateMaintenanceRequest(ctx, ownerActor, service.CreateMaintenanceRequestInput{
		PropertyID:           prop.ID,
		Title:                "Decking structural beam repair on " + unit1.Code,
		CategoryID:           plumbingCatID,
		Priority:             domain.MaintPriorityUrgent,
		UnitID:               &unit1.ID,
		SafetyImpact:         true,
		GuestImpact:          true,
		AvailabilityImpact:   domain.MaintImpactUnavailable,
		InspectionRequired:   false,
		CreateRestriction:    true,
		RestrictionStartDate: &lockStart,
		RestrictionEndDate:   &lockEnd,
		RestrictionNotes:     "Out of service until beam replaced",
	})
	if err != nil {
		t.Fatalf("create unit1 maintenance with restriction: %v", err)
	}
	if !unit1Maint.Request.HasActiveRestriction || len(unit1Maint.RestrictionLinks) != 1 {
		t.Fatalf("expected 1 active restriction link on unit1, got %+v", unit1Maint.RestrictionLinks)
	}
	link := unit1Maint.RestrictionLinks[0]

	// Attempting a direct reservation over lockStart..lockEnd on unit1 MUST fail with ErrOverlap.
	if _, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Blocked Guest",
		GuestEmail:    "blocked@example.test",
		ChannelCode:   "DIRECT",
		ArrivalDate:   lockStart,
		DepartureDate: lockEnd,
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unit1.ID},
	}); !errors.Is(err, domain.ErrOverlap) {
		t.Fatalf("expected ErrOverlap booking maintenance-restricted unit1, got %v", err)
	}

	// Complete & close unit1Maint — MUST NOT auto-release inventory restriction!
	d1, err := svc.TransitionMaintenanceStatus(ctx, ownerActor, prop.ID, unit1Maint.Request.ID, domain.MaintStatusInProgress, "Repair started", nil, unit1Maint.Request.Version)
	if err != nil {
		t.Fatalf("start unit1Maint: %v", err)
	}
	d2, err := svc.TransitionMaintenanceStatus(ctx, ownerActor, prop.ID, unit1Maint.Request.ID, domain.MaintStatusCompleted, "Timber replaced", nil, d1.Request.Version)
	if err != nil {
		t.Fatalf("complete unit1Maint: %v", err)
	}
	d3, err := svc.TransitionMaintenanceStatus(ctx, ownerActor, prop.ID, unit1Maint.Request.ID, domain.MaintStatusClosed, "Closed without releasing blockout", nil, d2.Request.Version)
	if err != nil {
		t.Fatalf("close unit1Maint: %v", err)
	}
	if !d3.Request.HasActiveRestriction {
		t.Fatalf("CRITICAL INVARIANT VIOLATED: completing/closing maintenance auto-released inventory restriction!")
	}
	// Still unbookable while closed!
	if _, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Still Blocked Guest",
		GuestEmail:    "stillblocked@example.test",
		ChannelCode:   "DIRECT",
		ArrivalDate:   lockStart,
		DepartureDate: lockEnd,
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unit1.ID},
	}); !errors.Is(err, domain.ErrOverlap) {
		t.Fatalf("expected unit1 to remain blocked after request CLOSED until explicitly released, got %v", err)
	}

	// Operations Centre integration check before explicit release:
	// unit2Maint intersects confirmedRes within 7 days -> should surface MAINTENANCE_BOOKING_IMPACT;
	// facilityReq is SAFETY/URGENT -> MAINTENANCE_SAFETY.
	ops, err := svc.GetOperationsCentre(ctx, ownerActor, prop.ID, nil, "")
	if err != nil {
		t.Fatalf("GetOperationsCentre: %v", err)
	}
	if ops.Maintenance.OpenCount < 2 {
		t.Fatalf("expected at least 2 open maintenance jobs in OpsMaintenanceSummary, got %+v", ops.Maintenance)
	}
	var sawSafety, sawBookingImpact bool
	for _, item := range ops.AttentionItems {
		if item.Category == "MAINTENANCE_SAFETY" {
			sawSafety = true
		}
		if item.Category == "MAINTENANCE_BOOKING_IMPACT" {
			sawBookingImpact = true
		}
	}
	if !sawSafety || !sawBookingImpact {
		t.Fatalf("expected MAINTENANCE_SAFETY and MAINTENANCE_BOOKING_IMPACT alerts, got %+v", ops.AttentionItems)
	}

	// Extend restriction dates +1 night, then explicitly release restriction -> unit1 bookable again!
	extEnd := lockEnd.AddDate(0, 0, 1)
	resized, err := svc.UpdateMaintenanceRestrictionDates(ctx, ownerActor, prop.ID, unit1Maint.Request.ID, link.ID, lockStart, extEnd, link.GroupVersion)
	if err != nil || !resized.EndDate.Equal(extEnd) {
		t.Fatalf("resize restriction failed: %+v err=%v", resized, err)
	}
	released, err := svc.ReleaseMaintenanceRestriction(ctx, ownerActor, prop.ID, unit1Maint.Request.ID, link.ID)
	if err != nil || released.ReleasedAt == nil {
		t.Fatalf("explicit release failed: %+v err=%v", released, err)
	}
	if _, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Bookable After Release",
		GuestEmail:    "afterrelease@example.test",
		ChannelCode:   "DIRECT",
		ArrivalDate:   lockStart,
		DepartureDate: extEnd,
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unit1.ID},
	}); err != nil {
		t.Fatalf("expected unit1 bookable after explicit release, got %v", err)
	}
}

func TestPhase3Milestone2AttachmentsRBACAndPublicIsolation(t *testing.T) {
	srv, store, svc, prop, _, _, _ := setupPhase2TestServer(t, "pub-p3-m2-rbac-secret")
	defer srv.Close()

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	_, err = svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email:    "hk@tirbach.wales",
		FullName: "Ffion Housekeeper",
		Role:     domain.RoleHousekeepingStaff,
		Password: "hk-pass-secure-2026",
	})
	if err != nil {
		t.Fatalf("create housekeeping user: %v", err)
	}
	_, err = svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email:    "eng@tirbach.wales",
		FullName: "Bryn Engineer",
		Role:     domain.RoleMaintenanceStaff,
		Password: "eng-pass-secure-2026",
	})
	if err != nil {
		t.Fatalf("create maintenance user: %v", err)
	}
	_, err = svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email:    "mgr@tirbach.wales",
		FullName: "Nia Manager",
		Role:     domain.RoleManager,
		Password: "mgr-pass-secure-2026",
	})
	if err != nil {
		t.Fatalf("create manager user: %v", err)
	}
	_, err = svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email:    "view@tirbach.wales",
		FullName: "Aled Viewer",
		Role:     domain.RoleViewer,
		Password: "view-pass-secure-2026",
	})
	if err != nil {
		t.Fatalf("create viewer user: %v", err)
	}

	cats, _ := svc.ListMaintenanceCategories(ctx, ownerActor, prop.ID, false)
	locs, _ := svc.ListMaintenanceLocations(ctx, ownerActor, prop.ID, false)
	units, _ := svc.ListUnits(ctx, ownerActor, prop.ID, nil)

	hkClient, hkCSRF := loginAdminClient(t, srv.URL, "hk@tirbach.wales", "hk-pass-secure-2026")
	engClient, engCSRF := loginAdminClient(t, srv.URL, "eng@tirbach.wales", "eng-pass-secure-2026")
	mgrClient, mgrCSRF := loginAdminClient(t, srv.URL, "mgr@tirbach.wales", "mgr-pass-secure-2026")
	viewClient, viewCSRF := loginAdminClient(t, srv.URL, "view@tirbach.wales", "view-pass-secure-2026")

	// 1. Viewer cannot report a maintenance issue.
	viewCreateResp := authedJSON(t, viewClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/maintenance", viewCSRF, mustJSON(t, map[string]any{
		"title":       "Viewer denied",
		"category_id": cats[0].ID,
		"location_id": locs[0].ID,
	}))
	viewCreateResp.Body.Close()
	if viewCreateResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected viewer POST /maintenance 403, got %d", viewCreateResp.StatusCode)
	}

	// 2. Housekeeping staff CANNOT report with create_restriction=true (lacks blocks:write / maintenance:write),
	//    but CAN report an issue cleanly with location/unit and attach an initial photo.
	hkBlockResp := authedJSON(t, hkClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/maintenance", hkCSRF, mustJSON(t, map[string]any{
		"title":                  "Broken latch",
		"category_id":            cats[0].ID,
		"unit_id":                units[0].ID,
		"create_restriction":     true,
		"restriction_start_date": domain.FormatDate(time.Now().UTC()),
		"restriction_end_date":   domain.FormatDate(time.Now().UTC().AddDate(0, 0, 1)),
	}))
	hkBlockResp.Body.Close()
	if hkBlockResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected housekeeping create_restriction=true 403, got %d", hkBlockResp.StatusCode)
	}

	hkReportResp := authedJSON(t, hkClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/maintenance", hkCSRF, mustJSON(t, map[string]any{
		"title":               "Loose tap in Reception Kitchenette",
		"category_id":         cats[0].ID,
		"location_id":         locs[0].ID,
		"priority":            "HIGH",
		"availability_impact": "PARTIAL",
	}))
	if hkReportResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected housekeeping report 201, got %d (%s)", hkReportResp.StatusCode, readBodyStr(hkReportResp))
	}
	var hkDetail domain.MaintenanceRequestDetail
	if err := json.NewDecoder(hkReportResp.Body).Decode(&hkDetail); err != nil {
		t.Fatalf("decode hk detail: %v", err)
	}
	hkReportResp.Body.Close()
	reqID := hkDetail.Request.ID

	// Housekeeping can attach initial report photo via on_report=true, then attach upload rejects dangerous SVG/EXE.
	validPNG := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE,
	}
	attResp := uploadMultipartAttachment(
		t, hkClient,
		srv.URL+"/api/v1/properties/"+prop.ID.String()+"/maintenance/"+reqID.String()+"/attachments",
		hkCSRF, "file", "tap-photo.png", validPNG,
		map[string]string{"caption": "Initial leak view", "on_report": "true"},
	)
	if attResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected initial PNG upload 201, got %d (%s)", attResp.StatusCode, readBodyStr(attResp))
	}
	var att domain.MaintenanceAttachment
	_ = json.NewDecoder(attResp.Body).Decode(&att)
	attResp.Body.Close()

	// Reject dangerous SVG & executable uploads.
	badSVG := uploadMultipartAttachment(
		t, engClient,
		srv.URL+"/api/v1/properties/"+prop.ID.String()+"/maintenance/"+reqID.String()+"/attachments",
		engCSRF, "file", "xss.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		nil,
	)
	badSVG.Body.Close()
	if badSVG.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected SVG rejection 422, got %d", badSVG.StatusCode)
	}

	// Authenticated viewer CAN view/download attachment with nosniff header; anonymous caller gets 401!
	dlURL := srv.URL + "/api/v1/properties/" + prop.ID.String() + "/maintenance/attachments/" + att.ID.String() + "?inline=1"
	anonResp, err := http.Get(dlURL)
	if err != nil {
		t.Fatalf("anon attachment GET: %v", err)
	}
	anonResp.Body.Close()
	if anonResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected anonymous attachment download 401, got %d", anonResp.StatusCode)
	}
	authDl, err := viewClient.Get(dlURL)
	if err != nil {
		t.Fatalf("viewer attachment GET: %v", err)
	}
	gotBytes, _ := io.ReadAll(authDl.Body)
	authDl.Body.Close()
	if authDl.StatusCode != http.StatusOK || !bytes.Equal(gotBytes, validPNG) || authDl.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unexpected attachment download status=%d nosniff=%q len=%d", authDl.StatusCode, authDl.Header.Get("X-Content-Type-Options"), len(gotBytes))
	}

	// 3. Maintenance staff can log work notes & complete work, CANNOT record inspection and CANNOT place inventory restriction or edit config.
	trStart := authedJSON(t, engClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/maintenance/"+reqID.String()+"/transition", engCSRF, mustJSON(t, map[string]any{
		"status":  "IN_PROGRESS",
		"version": hkDetail.Request.Version,
	}))
	var inProg domain.MaintenanceRequestDetail
	_ = json.NewDecoder(trStart.Body).Decode(&inProg)
	trStart.Body.Close()
	if trStart.StatusCode != http.StatusOK {
		t.Fatalf("expected maintenance_staff IN_PROGRESS 200, got %d", trStart.StatusCode)
	}
	trComp := authedJSON(t, engClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/maintenance/"+reqID.String()+"/transition", engCSRF, mustJSON(t, map[string]any{
		"status":  "COMPLETED",
		"version": inProg.Request.Version,
	}))
	var comp domain.MaintenanceRequestDetail
	_ = json.NewDecoder(trComp.Body).Decode(&comp)
	trComp.Body.Close()
	if trComp.StatusCode != http.StatusOK {
		t.Fatalf("expected maintenance_staff COMPLETED 200, got %d", trComp.StatusCode)
	}

	engInsp := authedJSON(t, engClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/maintenance/"+reqID.String()+"/inspection", engCSRF, mustJSON(t, map[string]any{
		"result":          "PASSED",
		"close_if_passed": true,
		"version":         comp.Request.Version,
	}))
	engInsp.Body.Close()
	if engInsp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected maintenance_staff inspection 403 Forbidden, got %d", engInsp.StatusCode)
	}

	engCat := authedJSON(t, engClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/maintenance/categories", engCSRF, mustJSON(t, map[string]any{
		"code": "UNAUTHORIZED_CAT",
		"name": "Unauthorized",
	}))
	engCat.Body.Close()
	if engCat.StatusCode != http.StatusForbidden {
		t.Fatalf("expected maintenance_staff category creation 403, got %d", engCat.StatusCode)
	}

	// Manager CAN pass inspection and close.
	mgrInsp := authedJSON(t, mgrClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/maintenance/"+reqID.String()+"/inspection", mgrCSRF, mustJSON(t, map[string]any{
		"result":          "PASSED",
		"notes":           "Checked and watertight",
		"close_if_passed": true,
		"version":         comp.Request.Version,
	}))
	mgrInsp.Body.Close()
	if mgrInsp.StatusCode != http.StatusOK {
		t.Fatalf("expected manager inspection pass 200, got %d", mgrInsp.StatusCode)
	}

	// 4. Verify Web UI pages render cleanly for manager and contain LOVELACE Maintenance routes.
	jar, _ := cookiejar.New(nil)
	webClient := &http.Client{Jar: jar}
	if _, err := webClient.PostForm(srv.URL+"/login", url.Values{
		"email":    {"mgr@tirbach.wales"},
		"password": {"mgr-pass-secure-2026"},
	}); err != nil {
		t.Fatalf("web login: %v", err)
	}
	for _, path := range []string{"/maintenance", "/maintenance/new", "/maintenance/" + reqID.String(), "/maintenance/schedule", "/maintenance/facilities"} {
		resp, err := webClient.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body := readBodyStr(resp)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, "LOVELACE") {
			t.Fatalf("expected 200 LOVELACE page for %s, got %d", path, resp.StatusCode)
		}
	}

	// 5. Verify Public Booking API has ZERO access to maintenance endpoints or internal data.
	pubResp, err := http.Get(srv.URL + "/api/public/v1/maintenance")
	if err != nil {
		t.Fatalf("public maintenance probe: %v", err)
	}
	pubResp.Body.Close()
	if pubResp.StatusCode == http.StatusOK {
		t.Fatalf("public API must never expose maintenance endpoints!")
	}
}

func TestPhase3Milestone2PreventativeSchedulesIdempotent(t *testing.T) {
	srv, store, svc, prop, _, _, _ := setupPhase2TestServer(t, "pub-p3-m2-sched-secret")
	defer srv.Close()

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	cats, _ := svc.ListMaintenanceCategories(ctx, ownerActor, prop.ID, false)
	locs, _ := svc.ListMaintenanceLocations(ctx, ownerActor, prop.ID, false)
	today := time.Now().UTC().Truncate(24 * time.Hour)

	weeklySched, err := svc.CreateMaintenanceSchedule(ctx, ownerActor, domain.MaintenanceSchedule{
		PropertyID:         prop.ID,
		Title:              "Weekly Fire Alarm Call Point Test",
		Description:        "Test call point and log panel check",
		CategoryID:         cats[0].ID,
		LocationID:         &locs[0].ID,
		Priority:           domain.MaintPriorityHigh,
		Frequency:          domain.MaintFreqWeekly,
		NextDueDate:        today,
		AssignedUserID:     &owner.ID,
		InspectionRequired: true,
		IsActive:           true,
	})
	if err != nil {
		t.Fatalf("create weekly schedule: %v", err)
	}
	onceSched, err := svc.CreateMaintenanceSchedule(ctx, ownerActor, domain.MaintenanceSchedule{
		PropertyID:         prop.ID,
		Title:              "Pre-season Gutter Clear",
		CategoryID:         cats[0].ID,
		LocationID:         &locs[0].ID,
		Priority:           domain.MaintPriorityNormal,
		Frequency:          domain.MaintFreqOnce,
		NextDueDate:        today,
		InspectionRequired: false,
		IsActive:           true,
	})
	if err != nil {
		t.Fatalf("create once schedule: %v", err)
	}

	// First run generates 2 jobs; immediate second run for same date produces 0 duplicates.
	run1, err := svc.RunDuePreventativeSchedules(ctx, ownerActor, prop.ID, &today)
	if err != nil || len(run1) != 2 {
		t.Fatalf("expected 2 generated jobs on run1, got %d err=%v", len(run1), err)
	}
	run2, err := svc.RunDuePreventativeSchedules(ctx, ownerActor, prop.ID, &today)
	if err != nil || len(run2) != 0 {
		t.Fatalf("expected 0 duplicate jobs on run2, got %d err=%v", len(run2), err)
	}

	// Verify weekly next_due_date moved +7d and once schedule became inactive; archiving weekly preserves generated request!
	scheds, err := svc.ListMaintenanceSchedules(ctx, ownerActor, prop.ID, true)
	if err != nil {
		t.Fatalf("list schedules: %v", err)
	}
	var gotWeekly domain.MaintenanceSchedule
	for _, s := range scheds {
		if s.ID == weeklySched.ID {
			gotWeekly = s
			wantNext := today.AddDate(0, 0, 7)
			if !s.NextDueDate.Equal(wantNext) {
				t.Fatalf("weekly NextDueDate = %s, want %s", domain.FormatDate(s.NextDueDate), domain.FormatDate(wantNext))
			}
		}
		if s.ID == onceSched.ID && s.IsActive {
			t.Fatalf("expected ONCE schedule to deactivate after execution")
		}
	}

	gotWeekly.IsActive = false
	gotWeekly.Title = "Amended & Paused Weekly Fire Test"
	if _, err := svc.UpdateMaintenanceSchedule(ctx, ownerActor, gotWeekly); err != nil {
		t.Fatalf("update schedule: %v", err)
	}
	reqs, err := svc.ListMaintenanceRequests(ctx, ownerActor, prop.ID, domain.MaintenanceRequestFilter{
		WorkType: "PREVENTATIVE",
		Status:   "ALL",
	})
	if err != nil || len(reqs) != 2 {
		t.Fatalf("expected 2 historical preventative requests intact after schedule pause/edit, got %d err=%v", len(reqs), err)
	}
}

func TestPhase3Milestone2HousekeepingRegressClearStartedState(t *testing.T) {
	srv, store, svc, prop, _, _, _ := setupPhase2TestServer(t, "pub-p3-m2-hk-secret")
	defer srv.Close()

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	cleaner1, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email:    "cleaner1@tirbach.wales",
		FullName: "Cleaner One",
		Role:     domain.RoleHousekeepingStaff,
		Password: "cleaner-1-pass-2026",
	})
	if err != nil {
		t.Fatalf("create cleaner1: %v", err)
	}
	cleaner2, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email:    "cleaner2@tirbach.wales",
		FullName: "Cleaner Two",
		Role:     domain.RoleStaff,
		Password: "cleaner-2-pass-2026",
	})
	if err != nil {
		t.Fatalf("create cleaner2: %v", err)
	}
	c1Actor := service.Actor{UserID: cleaner1.ID, Email: cleaner1.Email, FullName: cleaner1.FullName, Role: cleaner1.Role}
	c2Actor := service.Actor{UserID: cleaner2.ID, Email: cleaner2.Email, FullName: cleaner2.FullName, Role: cleaner2.Role}

	units, _ := svc.ListUnits(ctx, ownerActor, prop.ID, nil)
	today := time.Now().UTC().Truncate(24 * time.Hour)

	task, err := svc.CreateHousekeepingTask(ctx, ownerActor, prop.ID, units[0].ID, domain.TaskTypeChangeoverClean, domain.StageCleaningRequired, domain.TaskPriorityNormal, today, "Cycle 1 clean", "", &cleaner1.ID)
	if err != nil {
		t.Fatalf("create housekeeping task: %v", err)
	}

	// Cycle 1: cleaner1 starts cleaning, moves to inspection, owner marks READY.
	inProg1, err := svc.TransitionHousekeepingTaskStage(ctx, c1Actor, prop.ID, task.ID, domain.StageCleaningInProgress, "Starting cycle 1", task.Version)
	if err != nil {
		t.Fatalf("cycle 1 start: %v", err)
	}
	if inProg1.StartedAt == nil || inProg1.StartedBy == nil || *inProg1.StartedBy != cleaner1.ID {
		t.Fatalf("expected Cycle 1 started_at and started_by=%s, got %+v", cleaner1.ID, inProg1)
	}
	inspReq1, err := svc.TransitionHousekeepingTaskStage(ctx, c1Actor, prop.ID, task.ID, domain.StageInspectionRequired, "Cycle 1 clean done", inProg1.Version)
	if err != nil {
		t.Fatalf("cycle 1 inspection req: %v", err)
	}
	ready1, err := svc.TransitionHousekeepingTaskStage(ctx, ownerActor, prop.ID, task.ID, domain.StageReady, "Cycle 1 inspected", inspReq1.Version)
	if err != nil {
		t.Fatalf("cycle 1 ready: %v", err)
	}
	if ready1.StartedAt == nil || ready1.StartedBy == nil || ready1.InspectedAt == nil || ready1.InspectedBy == nil {
		t.Fatalf("expected Cycle 1 started_* and inspected_* populated at READY, got %+v", ready1)
	}

	// Regress READY -> CLEANING_REQUIRED: must clear started_at, started_by, inspected_at, inspected_by, completed_at, completed_by!
	recleaned, err := svc.TransitionHousekeepingTaskStage(ctx, ownerActor, prop.ID, task.ID, domain.StageCleaningRequired, "Muddy boots found, reclean needed", ready1.Version)
	if err != nil {
		t.Fatalf("regress to CLEANING_REQUIRED: %v", err)
	}
	if recleaned.StartedAt != nil || recleaned.StartedBy != nil || recleaned.InspectedAt != nil || recleaned.InspectedBy != nil || recleaned.CompletedAt != nil || recleaned.CompletedBy != nil {
		t.Fatalf("expected ALL cycle timestamps/actors cleared on CLEANING_REQUIRED regression, got startedAt=%v startedBy=%v inspectedAt=%v inspectedBy=%v completedAt=%v completedBy=%v",
			recleaned.StartedAt, recleaned.StartedBy, recleaned.InspectedAt, recleaned.InspectedBy, recleaned.CompletedAt, recleaned.CompletedBy)
	}

	// Cycle 2: cleaner2 starts & manager inspects -> new StartedBy & new InspectedBy while Cycle 1 remains in audit_logs.
	inProg2, err := svc.TransitionHousekeepingTaskStage(ctx, c2Actor, prop.ID, task.ID, domain.StageCleaningInProgress, "Cycle 2 start", recleaned.Version)
	if err != nil {
		t.Fatalf("cycle 2 start: %v", err)
	}
	if inProg2.StartedAt == nil || inProg2.StartedBy == nil || *inProg2.StartedBy != cleaner2.ID {
		t.Fatalf("expected Cycle 2 started_by=%s, got %+v", cleaner2.ID, inProg2.StartedBy)
	}
	var auditTransitions int
	if err := store.Pool().QueryRow(ctx, `
		SELECT COUNT(*) FROM audit_logs
		WHERE resource_type = 'housekeeping_task' AND resource_id = $1 AND action = 'housekeeping_task.stage_changed'
	`, task.ID.String()).Scan(&auditTransitions); err != nil || auditTransitions < 5 {
		t.Fatalf("expected audit log to preserve all stage transitions across cycles (>=5), got %d err=%v", auditTransitions, err)
	}
}
