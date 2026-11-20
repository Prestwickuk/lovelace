package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/service"
)

func TestSecurityHardening_MultiTenantUserIsolationAndPublicHoldPrivacy(t *testing.T) {
	srv, store, svc, propA, _, _, clk := setupPhase2TestServer(t, "pub-sec-harden")
	defer srv.Close()

	ctx := context.Background()
	globalOwner, err := store.GetAdminUserByEmail(ctx, nil, "owner@example.com")
	if err != nil {
		t.Fatalf("get global owner: %v", err)
	}
	globalActor := service.Actor{
		UserID: globalOwner.ID, Email: globalOwner.Email, FullName: globalOwner.FullName, Role: globalOwner.Role,
	}

	propBID, _ := uuid.NewV7()
	propB, err := store.CreateProperty(ctx, nil, domain.Property{
		ID: propBID, Slug: "sec-prop-b", Name: "Property B", CountryCode: "GB",
		Timezone: "Europe/London", Currency: "GBP", ContactEmail: "b@example.com",
		CheckInTime: "15:00", CheckOutTime: "10:00",
	})
	if err != nil {
		t.Fatalf("create propB: %v", err)
	}

	// Provision property-scoped owner_admins on Property A and Property B, plus a rotating staff user on A.
	scopedAdminA, err := svc.CreateUser(ctx, globalActor, service.CreateUserInput{
		PropertyID: &propA.ID, Email: "scoped-owner-a@example.com", FullName: "Scoped Owner A",
		Role: domain.RoleOwnerAdmin, Password: "ScopedOwnerA-2026!Pass",
	})
	if err != nil {
		t.Fatalf("create scopedAdminA: %v", err)
	}
	scopedAdminB, err := svc.CreateUser(ctx, globalActor, service.CreateUserInput{
		PropertyID: &propB.ID, Email: "scoped-owner-b@example.com", FullName: "Scoped Owner B",
		Role: domain.RoleOwnerAdmin, Password: "ScopedOwnerB-2026!Pass",
	})
	if err != nil {
		t.Fatalf("create scopedAdminB: %v", err)
	}
	rotatingStaffA, err := svc.CreateUser(ctx, globalActor, service.CreateUserInput{
		PropertyID: &propA.ID, Email: "rot-staff-a@example.com", FullName: "Rotating Staff A",
		Role: domain.RoleStaff, Password: "RotStaffA-2026!Pass", MustRotatePassword: true,
	})
	if err != nil {
		t.Fatalf("create rotatingStaffA: %v", err)
	}

	// Deliberately omit PropertyID on Actor struct to verify DB-authoritative scoping still blocks escalation!
	actorAdminA := service.Actor{
		UserID: scopedAdminA.ID, Email: scopedAdminA.Email, FullName: scopedAdminA.FullName, Role: domain.RoleOwnerAdmin,
	}

	// 1. Scoped owner_admin A cannot list global owner or Property B's users.
	visibleUsers, err := svc.ListUsers(ctx, actorAdminA)
	if err != nil {
		t.Fatalf("ListUsers as scopedAdminA: %v", err)
	}
	for _, u := range visibleUsers {
		if u.PropertyID == nil || *u.PropertyID != propA.ID {
			t.Fatalf("scopedAdminA enumerated out-of-scope user %s (property_id=%v)", u.Email, u.PropertyID)
		}
	}

	// 2. Scoped owner_admin A cannot mint a global owner_admin (PropertyID: nil) or a user in Property B.
	if _, err := svc.CreateUser(ctx, actorAdminA, service.CreateUserInput{
		PropertyID: nil, Email: "escalate-global@example.com", FullName: "Escalated Global",
		Role: domain.RoleOwnerAdmin, Password: "EscalateGlobal-2026!Pass",
	}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden minting global owner_admin from scoped admin, got %v", err)
	}
	if _, err := svc.CreateUser(ctx, actorAdminA, service.CreateUserInput{
		PropertyID: &propB.ID, Email: "plant-in-b@example.com", FullName: "Planted B",
		Role: domain.RoleManager, Password: "PlantInB-2026!Pass",
	}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden creating user in Property B, got %v", err)
	}

	// 3. Scoped owner_admin A cannot modify global owner or Property B's owner, and cannot self-demote.
	if _, err := svc.UpdateUser(ctx, actorAdminA, service.UpdateUserInput{
		ID: globalOwner.ID, FullName: globalOwner.FullName, Role: globalOwner.Role, IsActive: false, Version: globalOwner.Version,
	}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden updating global owner from scoped admin, got %v", err)
	}
	if _, err := svc.UpdateUser(ctx, actorAdminA, service.UpdateUserInput{
		ID: scopedAdminB.ID, FullName: scopedAdminB.FullName, Role: scopedAdminB.Role, IsActive: false, Version: scopedAdminB.Version,
	}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden updating Property B admin from Property A admin, got %v", err)
	}
	if _, err := svc.UpdateUser(ctx, actorAdminA, service.UpdateUserInput{
		ID: scopedAdminA.ID, FullName: scopedAdminA.FullName, Role: domain.RoleViewer, IsActive: true, Version: scopedAdminA.Version,
	}); err == nil {
		t.Fatal("expected self-role-demotion to be rejected")
	}

	// 4. Updating a user with MustRotatePassword=nil preserves MustRotatePassword=true instead of wiping it!
	updatedStaffA, err := svc.UpdateUser(ctx, actorAdminA, service.UpdateUserInput{
		ID: rotatingStaffA.ID, FullName: "Renamed Rotating Staff A", Role: domain.RoleManager, IsActive: true, Version: rotatingStaffA.Version,
	})
	if err != nil {
		t.Fatalf("UpdateUser rotatingStaffA: %v", err)
	}
	if !updatedStaffA.MustRotatePassword {
		t.Fatal("expected MustRotatePassword=true to be preserved when omitted from UpdateUserInput")
	}

	// 5. Cross-property UpdateRateRule must fail closed when linking foreign rate_plan_id or unit_type_id.
	utB, err := svc.CreateUnitType(ctx, globalActor, domain.UnitType{
		PropertyID: propB.ID, Code: "B-HUT", Name: "Foreign Hut B", Category: "cabin",
		BaseCapacityAdults: 2, MaxAdults: 2, MaxGuests: 2, BaseNightlyRatePence: 9900,
		MinNights: 1, MaxNights: 14, IsActive: true,
	})
	if err != nil {
		t.Fatalf("create utB: %v", err)
	}
	utsA, _ := svc.ListUnitTypes(ctx, actorAdminA, propA.ID)
	plansA, _ := svc.ListRatePlans(ctx, actorAdminA, propA.ID)
	sd, _ := domain.ParseDate("2026-09-01")
	ed, _ := domain.ParseDate("2026-09-10")
	ruleA, err := svc.CreateRateRule(ctx, actorAdminA, domain.RateRule{
		PropertyID: propA.ID, RatePlanID: plansA[0].ID, UnitTypeID: utsA[0].ID,
		Name: "Autumn Rule A", StartDate: sd, EndDate: ed, DaysOfWeek: 127,
		NightlyRatePence: 11000, Priority: 5, IsActive: true,
	})
	if err != nil {
		t.Fatalf("create ruleA: %v", err)
	}
	ruleA.UnitTypeID = utB.ID
	if _, err := svc.UpdateRateRule(ctx, actorAdminA, ruleA); err == nil {
		t.Fatal("expected cross-property UpdateRateRule with foreign unit_type_id to be rejected")
	}

	// 6. Public hold cancel & checkout must scrub guest PII, and confirmed holds cannot be cancelled via public hold release.
	pubClient := newClient(t)
	arr := clk.Now().AddDate(0, 0, 95).Format("2006-01-02")
	dep := clk.Now().AddDate(0, 0, 97).Format("2006-01-02")
	holdCreated := createTestHoldViaAPI(t, srv, pubClient, "pub-sec-harden", arr, dep)

	coResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/checkout", "pub-sec-harden", "", map[string]any{
		"hold_id":         holdCreated.Hold.ID.String(),
		"idempotency_key": "sec-co-1",
	})
	var coOut service.CreateCheckoutResponse
	_ = json.NewDecoder(coResp.Body).Decode(&coOut)
	coResp.Body.Close()
	if coResp.StatusCode != http.StatusOK {
		t.Fatalf("checkout status=%d", coResp.StatusCode)
	}
	if coOut.Hold.GuestEmail != "" || coOut.Hold.GuestName != "" || coOut.Hold.GuestPhone != "" {
		t.Fatalf("expected checkout hold response to scrub guest PII, got %+v", coOut.Hold)
	}

	cancelResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/booking/holds/"+holdCreated.Hold.ID.String()+"/cancel", "pub-sec-harden", "", map[string]any{})
	var cancelOut struct {
		Hold domain.BookingHold `json:"hold"`
	}
	_ = json.NewDecoder(cancelResp.Body).Decode(&cancelOut)
	cancelResp.Body.Close()
	if cancelResp.StatusCode != http.StatusOK {
		t.Fatalf("cancel hold status=%d", cancelResp.StatusCode)
	}
	if cancelOut.Hold.GuestEmail != "" || cancelOut.Hold.GuestName != "" || cancelOut.Hold.GuestPhone != "" {
		t.Fatalf("expected cancel hold response to scrub guest PII, got %+v", cancelOut.Hold)
	}

	// 7. Verify 0029 + Phase 3 M4 (0026) audit allowlist parity: consent_status, 8-digit external_reservation_id,
	// guest_portal_identity, PORTAL source/method, and arrival_details changed_fields survive redaction.
	guest, err := svc.CreateGuest(ctx, actorAdminA, domain.Guest{
		PropertyID: propA.ID, FirstName: "Carys", LastName: "Consent", Email: "carys.consent@example.com",
	})
	if err != nil {
		t.Fatalf("create guest: %v", err)
	}
	if _, err := svc.RecordGuestConsent(ctx, actorAdminA, domain.GuestConsentRecord{
		PropertyID: propA.ID, GuestID: guest.ID, ConsentType: "MARKETING_EMAIL",
		Status: domain.ConsentGranted, Method: "PORTAL", Source: "apps/public-web", PolicyVersion: "v1",
	}); err != nil {
		t.Fatalf("record consent: %v", err)
	}
	var consentStatusInAudit string
	if err := store.Pool().QueryRow(ctx, `
		SELECT COALESCE(after_state->>'consent_status', '')
		FROM audit_logs
		WHERE property_id = $1 AND action = 'guest_consent.recorded' AND resource_id = $2
		ORDER BY id DESC LIMIT 1
	`, propA.ID, guest.ID.String()).Scan(&consentStatusInAudit); err != nil || consentStatusInAudit != "GRANTED" {
		t.Fatalf("expected consent_status='GRANTED' preserved in audit_logs, got %q (err=%v)", consentStatusInAudit, err)
	}

	nowStr := clk.Now().UTC().Format(time.RFC3339Nano)
	var otaAuditID int64
	otaSnap := `{"id":"` + uuid.NewString() + `","property_id":"` + propA.ID.String() + `","channel_connection_id":"` + uuid.NewString() + `","provider_code":"beds24","external_reservation_id":"58291047","import_status":"IMPORTED","created_at":"` + nowStr + `","updated_at":"` + nowStr + `","guest_name":"Raw Leak"}`
	if err := store.Pool().QueryRow(ctx, `
		INSERT INTO audit_logs (property_id, actor_user_id, actor_email, actor_role, action, resource_type, resource_id, request_id, after_state)
		VALUES ($1, $2, 'legacy@example.test', 'owner_admin', 'channel.imported', 'channel_external_reservation', $3, 'req-ota', $4::jsonb)
		RETURNING id
	`, propA.ID, globalOwner.ID, uuid.NewString(), otaSnap).Scan(&otaAuditID); err != nil {
		t.Fatalf("insert ota audit row: %v", err)
	}
	redEntry, err := svc.RedactHistoricalAuditLog(ctx, actorAdminA, propA.ID, otaAuditID, "Verify 8-digit OTA booking ID survives redaction")
	if err != nil {
		t.Fatalf("RedactHistoricalAuditLog on 8-digit OTA ID failed: %v", err)
	}
	var redMap map[string]any
	_ = json.Unmarshal(redEntry.AfterState, &redMap)
	if redMap["external_reservation_id"] != "58291047" {
		t.Fatalf("expected 8-digit external_reservation_id '58291047' preserved across redaction, got %+v", redMap)
	}

	var portalAuditID int64
	portalIdentID := uuid.NewString()
	portalSnap := `{"id":"` + portalIdentID + `","property_id":"` + propA.ID.String() + `","guest_id":"` + guest.ID.String() + `","status":"ACTIVE","purpose":"LOGIN","challenge_id":"` + uuid.NewString() + `","sessions_revoked":2,"version":1,"created_at":"` + nowStr + `","updated_at":"` + nowStr + `","verified_email":"leak@example.test"}`
	if err := store.Pool().QueryRow(ctx, `
		INSERT INTO audit_logs (property_id, actor_user_id, actor_email, actor_role, action, resource_type, resource_id, request_id, after_state)
		VALUES ($1, NULL, 'guest@public.lovelace', 'public_guest', 'guest_portal_identity.verified', 'guest_portal_identity', $2, 'req-m4-parity', $3::jsonb)
		RETURNING id
	`, propA.ID, portalIdentID, portalSnap).Scan(&portalAuditID); err != nil {
		t.Fatalf("insert portal audit row: %v", err)
	}
	redPortal, err := svc.RedactHistoricalAuditLog(ctx, actorAdminA, propA.ID, portalAuditID, "Verify Phase 3 M4 portal resource allowlist survives 0029")
	if err != nil {
		t.Fatalf("RedactHistoricalAuditLog on guest_portal_identity failed after 0029: %v", err)
	}
	var redPortalMap map[string]any
	_ = json.Unmarshal(redPortal.AfterState, &redPortalMap)
	if redPortalMap["status"] != "ACTIVE" || redPortalMap["purpose"] != "LOGIN" {
		t.Fatalf("expected M4 guest_portal_identity status/purpose preserved across 0029 redaction, got %+v", redPortalMap)
	}
	if _, leaked := redPortalMap["verified_email"]; leaked {
		t.Fatalf("expected non-allowlisted verified_email stripped during redaction, got %+v", redPortalMap)
	}

	// 8. Cross-property maintenance_request_id on guest enquiry must be rejected (service + 0029 FK).
	catsB, err := svc.ListMaintenanceCategories(ctx, globalActor, propB.ID, false)
	if err != nil || len(catsB) == 0 {
		t.Fatalf("ListMaintenanceCategories propB: %v", err)
	}
	locsB, err := svc.ListMaintenanceLocations(ctx, globalActor, propB.ID, false)
	if err != nil || len(locsB) == 0 {
		t.Fatalf("ListMaintenanceLocations propB: %v", err)
	}
	locBID := locsB[0].ID
	maintB, err := svc.CreateMaintenanceRequest(ctx, globalActor, service.CreateMaintenanceRequestInput{
		PropertyID: propB.ID, Title: "Foreign Pump B", Description: "Property B pump issue",
		CategoryID: catsB[0].ID, Priority: domain.MaintPriorityHigh, LocationID: &locBID,
	})
	if err != nil {
		t.Fatalf("create maintB: %v", err)
	}
	maintBID := maintB.Request.ID
	if _, err := svc.CreateGuestEnquiry(ctx, actorAdminA, domain.GuestEnquiry{
		PropertyID: propA.ID, GuestID: &guest.ID, Category: domain.EnqCatFacilities,
		Priority: domain.EnqPriorityNormal, Source: domain.EnqSourceEmail,
		Subject: "Link foreign maintenance", Description: "Attempt cross-tenant link",
		RequesterName: "Carys Consent", RequesterEmail: guest.Email,
		MaintenanceRequestID: &maintBID,
	}); err == nil {
		t.Fatal("expected cross-property maintenance_request_id on CreateGuestEnquiry to be rejected")
	}

	// 9. Erasure-pending cluster freeze: UpdateGuest, MergeGuests, and DismissGuestDuplicate must reject with ErrConflict.
	guest2, err := svc.CreateGuest(ctx, actorAdminA, domain.Guest{
		PropertyID: propA.ID, FirstName: "Bryn", LastName: "Pending", Email: "bryn.pending@example.com",
	})
	if err != nil {
		t.Fatalf("create guest2: %v", err)
	}
	commOut, err := svc.SendGuestEmail(ctx, actorAdminA, service.SendGuestEmailInput{
		PropertyID: propA.ID, GuestID: guest2.ID, Recipient: guest2.Email,
		Subject: "Pre-erasure in-flight dispatch", BodyText: "Testing erasure-pending cluster freeze.",
	})
	if err != nil || commOut.EmailOutboxID == nil {
		t.Fatalf("SendGuestEmail guest2: comm=%+v err=%v", commOut, err)
	}
	claimedOut, err := store.ClaimDueOutboxEmails(ctx, clk.Now().UTC(), 10)
	if err != nil || len(claimedOut) == 0 {
		t.Fatalf("ClaimDueOutboxEmails: %+v err=%v", claimedOut, err)
	}
	var targetClaim domain.EmailOutboxItem
	for _, it := range claimedOut {
		if it.ID == *commOut.EmailOutboxID {
			targetClaim = it
			break
		}
	}
	if okLease, _, err := store.BeginOutboxDispatchLease(ctx, targetClaim.ID, targetClaim.WorkerToken, "sec-worker-1", clk.Now().UTC(), 60*time.Second); err != nil || !okLease {
		t.Fatalf("BeginOutboxDispatchLease: ok=%v err=%v", okLease, err)
	}
	if _, err := svc.AnonymizeGuest(ctx, actorAdminA, propA.ID, guest2.ID, "GDPR Art. 17 request while email in flight"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict entering erasure-pending barrier, got %v", err)
	}
	guest2Fresh, err := store.GetGuest(ctx, nil, propA.ID, guest2.ID)
	if err != nil || guest2Fresh.ErasureRequestedAt == nil || guest2Fresh.AnonymizedAt != nil {
		t.Fatalf("expected erasure_requested_at set on guest2, got %+v err=%v", guest2Fresh, err)
	}
	guest2Fresh.Phone = "+447700900999"
	if _, err := svc.UpdateGuest(ctx, actorAdminA, guest2Fresh); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict updating erasure-pending guest, got %v", err)
	}
	if _, _, err := svc.MergeGuests(ctx, actorAdminA, propA.ID, guest2.ID, guest.ID, "Attempt merge bypass"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict merging erasure-pending guest, got %v", err)
	}
	if err := svc.DismissGuestDuplicate(ctx, actorAdminA, propA.ID, guest2.ID, guest.ID, "Attempt dismiss"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict dismissing duplicate on erasure-pending guest, got %v", err)
	}

	// 10. Operations Centre RBAC field scrubbing for viewer (no InternalNotes) and housekeeping_staff (no PII/financials).
	unitsA, _ := svc.ListUnits(ctx, actorAdminA, propA.ID, nil)
	stayArr := clk.Now().UTC().AddDate(0, 0, 14)
	stayDep := stayArr.AddDate(0, 0, 2)
	activeRes, err := svc.CreateReservation(ctx, actorAdminA, domain.Reservation{
		PropertyID: propA.ID, GuestID: guest.ID, GuestName: "Carys Consent", GuestEmail: guest.Email,
		GuestPhone: "+447700900111", ChannelCode: "DIRECT", ArrivalDate: stayArr, DepartureDate: stayDep, Adults: 2,
		Status: domain.ResStatusConfirmed, InternalNotes: "VIP allergy confidential note",
		UnitIDs: []uuid.UUID{unitsA[0].ID},
	})
	if err != nil {
		t.Fatalf("create activeRes: %v", err)
	}
	viewerUserA, err := svc.CreateUser(ctx, actorAdminA, service.CreateUserInput{
		PropertyID: &propA.ID, Email: "viewer-ops-sec@example.com", FullName: "Viewer Ops Sec",
		Role: domain.RoleViewer, Password: "ViewerOpsSec-2026!Pass",
	})
	if err != nil {
		t.Fatalf("create viewerUserA: %v", err)
	}
	hkUserA, err := svc.CreateUser(ctx, actorAdminA, service.CreateUserInput{
		PropertyID: &propA.ID, Email: "hk-ops-sec@example.com", FullName: "HK Ops Sec",
		Role: domain.RoleHousekeepingStaff, Password: "HKOpsSec-2026!Pass",
	})
	if err != nil {
		t.Fatalf("create hkUserA: %v", err)
	}
	viewerActor := service.Actor{UserID: viewerUserA.ID, PropertyID: &propA.ID, Email: viewerUserA.Email, Role: domain.RoleViewer}
	hkActor := service.Actor{UserID: hkUserA.ID, PropertyID: &propA.ID, Email: hkUserA.Email, Role: domain.RoleHousekeepingStaff}
	opsViewer, err := svc.GetOperationsCentre(ctx, viewerActor, propA.ID, &stayArr, "")
	if err != nil {
		t.Fatalf("GetOperationsCentre viewer: %v", err)
	}
	for _, item := range opsViewer.Arrivals {
		if item.ReservationID == activeRes.ID && item.InternalNotes != "" {
			t.Fatalf("viewer without guests:notes leaked InternalNotes: %q", item.InternalNotes)
		}
	}
	opsHK, err := svc.GetOperationsCentre(ctx, hkActor, propA.ID, &stayArr, "")
	if err != nil {
		t.Fatalf("GetOperationsCentre hk: %v", err)
	}
	for _, item := range opsHK.Arrivals {
		if item.ReservationID == activeRes.ID {
			if item.GuestEmail != "" || item.GuestPhone != "" || item.InternalNotes != "" || item.TotalPence != 0 || item.BalanceDuePence != 0 {
				t.Fatalf("housekeeping_staff leaked restricted reservation/guest fields: %+v", item)
			}
		}
	}

	// 11. Pre-session Login CSRF protection & Portal Origin: null fail-closed rejection.
	csrfLoginReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/login", strings.NewReader(`{"email":"owner@example.com","password":"pw"}`))
	csrfLoginReq.Header.Set("Content-Type", "application/json")
	csrfLoginReq.Header.Set("Origin", "https://evil.example")
	csrfLoginResp, err := pubClient.Do(csrfLoginReq)
	if err != nil {
		t.Fatalf("POST /api/v1/auth/login cross-origin: %v", err)
	}
	csrfLoginResp.Body.Close()
	if csrfLoginResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-origin POST /api/v1/auth/login, got %d", csrfLoginResp.StatusCode)
	}

	nullOriginWebReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/login", strings.NewReader("email=owner%40example.com&password=pw"))
	nullOriginWebReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	nullOriginWebReq.Header.Set("Origin", "null")
	nullOriginWebResp, err := pubClient.Do(nullOriginWebReq)
	if err != nil {
		t.Fatalf("POST /login Origin: null: %v", err)
	}
	nullOriginWebResp.Body.Close()
	if nullOriginWebResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for Origin: null POST /login, got %d", nullOriginWebResp.StatusCode)
	}

	portalNullReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/public/v1/portal/auth/challenge", strings.NewReader(`{"property_slug":"demo-property","email":"carys.consent@example.com"}`))
	portalNullReq.Header.Set("Content-Type", "application/json")
	portalNullReq.Header.Set("X-Lovelace-Public-Key", "pub-sec-harden")
	portalNullReq.Header.Set("Origin", "null")
	portalNullReq.Header.Set("Referer", srv.URL+"/portal")
	portalNullResp, err := pubClient.Do(portalNullReq)
	if err != nil {
		t.Fatalf("POST /api/public/v1/portal/auth/challenge Origin: null: %v", err)
	}
	portalNullResp.Body.Close()
	if portalNullResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for Origin: null on portal mutation, got %d", portalNullResp.StatusCode)
	}
}
