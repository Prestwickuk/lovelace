package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/service"
)

func TestPhase331LifecyclePermissionsAndGuestDetailPrivacy(t *testing.T) {
	srv, store, svc, prop, _, _, _ := setupPhase2TestServer(t, "pub-p331-life")
	defer srv.Close()

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@example.com")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	staffUser, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "p331staff@example.com", FullName: "Lowri Staff", Role: domain.RoleStaff, Password: "p331-staff-pass-2026",
	})
	if err != nil {
		t.Fatalf("create staff user: %v", err)
	}
	viewerUser, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "p331viewer@example.com", FullName: "Tomos Viewer", Role: domain.RoleViewer, Password: "p331-viewer-pass-2026",
	})
	if err != nil {
		t.Fatalf("create viewer user: %v", err)
	}
	managerUser, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "p331mgr@example.com", FullName: "Mared Manager", Role: domain.RoleManager, Password: "p331-mgr-pass-2026",
	})
	if err != nil {
		t.Fatalf("create manager user: %v", err)
	}

	staffActor := service.Actor{UserID: staffUser.ID, Email: staffUser.Email, FullName: staffUser.FullName, Role: staffUser.Role}
	staffClient, staffCSRF := loginAdminClient(t, srv.URL, staffUser.Email, "p331-staff-pass-2026")
	viewerClient, _ := loginAdminClient(t, srv.URL, viewerUser.Email, "p331-viewer-pass-2026")
	mgrClient, _ := loginAdminClient(t, srv.URL, managerUser.Email, "p331-mgr-pass-2026")

	// 1. Staff with PermGuestsWrite cannot create guests with MERGED or ANONYMIZED status (service + HTTP).
	for _, protected := range []domain.GuestStatus{domain.GuestStatusMerged, domain.GuestStatusAnonymized} {
		if _, err := svc.CreateGuest(ctx, staffActor, domain.Guest{
			PropertyID: prop.ID, FirstName: "Bypass", LastName: "Attempt", Email: "bypass@example.test", Status: protected,
		}); err == nil {
			t.Fatalf("staff must not create guest with status %s via service", protected)
		}
		res := authedJSON(t, staffClient, http.MethodPost, fmt.Sprintf("%s/api/v1/properties/%s/guests", srv.URL, prop.ID), staffCSRF, mustJSON(t, map[string]any{
			"first_name": "Bypass", "last_name": "HTTP", "email": "bypass-http@example.test", "status": string(protected),
		}))
		res.Body.Close()
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 creating guest with protected status %s via HTTP, got %d", protected, res.StatusCode)
		}
	}

	// Create normal guest as staff, then try PATCHing status to MERGED or ANONYMIZED.
	guestA, err := svc.CreateGuest(ctx, staffActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Angharad", LastName: "Evans", Email: "angharad@example.test",
	})
	if err != nil {
		t.Fatalf("create valid active guest: %v", err)
	}
	guestDup, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "A.", LastName: "Evans", Email: "angharad.alt@example.test", Notes: "Merge secret snapshot note",
	})
	if err != nil {
		t.Fatalf("create guestDup: %v", err)
	}

	for _, protected := range []domain.GuestStatus{domain.GuestStatusMerged, domain.GuestStatusAnonymized} {
		if _, err := svc.UpdateGuest(ctx, staffActor, domain.Guest{
			ID: guestA.ID, PropertyID: prop.ID, FirstName: "Angharad", LastName: "Evans", Status: protected, Version: guestA.Version,
		}); err == nil {
			t.Fatalf("staff must not update guest to status %s", protected)
		}
		res := authedJSON(t, staffClient, http.MethodPatch, fmt.Sprintf("%s/api/v1/properties/%s/guests/%s", srv.URL, prop.ID, guestA.ID), staffCSRF, mustJSON(t, map[string]any{
			"first_name": "Angharad", "last_name": "Evans", "status": string(protected), "version": guestA.Version,
		}))
		res.Body.Close()
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 patching guest to protected status %s, got %d", protected, res.StatusCode)
		}
	}

	// DB check constraint directly prevents MERGED without merged_into_guest_id and ANONYMIZED without anonymized_at.
	if _, dbErr := store.Pool().Exec(ctx, `
		INSERT INTO guests (id, property_id, first_name, last_name, full_name, status)
		VALUES ($1, $2, 'Invalid', 'Merged', 'Invalid Merged', 'MERGED')
	`, uuid.New(), prop.ID); dbErr == nil {
		t.Fatalf("expected DB check constraint to reject MERGED row without merged_into_guest_id")
	}
	if _, dbErr := store.Pool().Exec(ctx, `
		INSERT INTO guests (id, property_id, first_name, last_name, full_name, status)
		VALUES ($1, $2, 'Invalid', 'Anon', 'Invalid Anon', 'ANONYMIZED')
	`, uuid.New(), prop.ID); dbErr == nil {
		t.Fatalf("expected DB check constraint to reject ANONYMIZED row without anonymized_at")
	}

	// Add enquiry, communication, note, and merge history on guestA (NO consent records yet!).
	if _, err := svc.CreateGuestEnquiry(ctx, ownerActor, domain.GuestEnquiry{
		PropertyID: prop.ID, GuestID: &guestA.ID, Category: domain.EnqCatComplaint, Priority: domain.EnqPriorityHigh,
		Source: domain.EnqSourceEmail, Subject: "Private complaint about heating", Description: "Heating fault details",
	}); err != nil {
		t.Fatalf("create enquiry: %v", err)
	}
	if _, err := svc.AddGuestNote(ctx, ownerActor, domain.GuestNote{
		PropertyID: prop.ID, GuestID: guestA.ID, Category: domain.NoteCatCustomerService, Content: "VIP internal discretion note",
	}); err != nil {
		t.Fatalf("add guest note: %v", err)
	}
	if _, _, err := svc.MergeGuests(ctx, ownerActor, prop.ID, guestDup.ID, guestA.ID, "Merged duplicate"); err != nil {
		t.Fatalf("merge guests: %v", err)
	}

	// Generic UpdateGuest on a MERGED guest profile must fail.
	if _, err := svc.UpdateGuest(ctx, ownerActor, domain.Guest{
		ID: guestDup.ID, PropertyID: prop.ID, FirstName: "Still", LastName: "Merged", Status: domain.GuestStatusActive, Version: 1,
	}); err == nil {
		t.Fatalf("expected UpdateGuest on MERGED record to be rejected")
	}

	// 2. GetGuestDetail permission & NOT_SUBSCRIBED verification.
	// Viewer (no PermCommsRead, no PermGuestsNotes, no PermGuestsMerge, no write/export)
	vResp := authedJSON(t, viewerClient, http.MethodGet, fmt.Sprintf("%s/api/v1/properties/%s/guests/%s", srv.URL, prop.ID, guestA.ID), "", nil)
	var vBundle domain.GuestDetailBundle
	_ = json.NewDecoder(vResp.Body).Decode(&vBundle)
	vResp.Body.Close()
	if vResp.StatusCode != http.StatusOK {
		t.Fatalf("viewer guest detail status = %d", vResp.StatusCode)
	}
	if vBundle.ShowEnquiries || len(vBundle.Enquiries) != 0 {
		t.Fatalf("viewer must not receive enquiries: show=%v len=%d", vBundle.ShowEnquiries, len(vBundle.Enquiries))
	}
	if vBundle.ShowCommunications || len(vBundle.Communications) != 0 {
		t.Fatalf("viewer must not receive communications")
	}
	if vBundle.ShowInternalNotes || len(vBundle.Notes) != 0 {
		t.Fatalf("viewer must not receive internal notes")
	}
	if vBundle.ShowConsentHistory || len(vBundle.ConsentHistory) != 0 {
		t.Fatalf("viewer must not receive consent history")
	}
	if len(vBundle.MergeHistory) != 0 {
		t.Fatalf("viewer must not receive merge history snapshots")
	}
	if vBundle.CurrentMarketingState != domain.ConsentNotSubscribed {
		t.Fatalf("expected guest with zero consent records to report NOT_SUBSCRIBED, got %q", vBundle.CurrentMarketingState)
	}

	// Staff (has PermCommsRead + PermGuestsWrite, lacks PermGuestsNotes & PermGuestsMerge)
	sResp := authedJSON(t, staffClient, http.MethodGet, fmt.Sprintf("%s/api/v1/properties/%s/guests/%s", srv.URL, prop.ID, guestA.ID), "", nil)
	var sBundle domain.GuestDetailBundle
	_ = json.NewDecoder(sResp.Body).Decode(&sBundle)
	sResp.Body.Close()
	if !sBundle.ShowEnquiries || len(sBundle.Enquiries) != 1 || !sBundle.ShowCommunications || !sBundle.ShowConsentHistory {
		t.Fatalf("staff should see enquiries, comms, and consent history: %+v", sBundle)
	}
	if len(sBundle.MergeHistory) != 0 {
		t.Fatalf("staff without PermGuestsMerge must not receive MergeHistory, got %d", len(sBundle.MergeHistory))
	}
	if !sBundle.ShowInternalNotes || len(sBundle.Notes) != 1 {
		t.Fatalf("staff with PermGuestsNotes should see notes, got show=%v len=%d", sBundle.ShowInternalNotes, len(sBundle.Notes))
	}

	// Manager (has PermGuestsMerge + PermGuestsNotes)
	mResp := authedJSON(t, mgrClient, http.MethodGet, fmt.Sprintf("%s/api/v1/properties/%s/guests/%s", srv.URL, prop.ID, guestA.ID), "", nil)
	var mBundle domain.GuestDetailBundle
	_ = json.NewDecoder(mResp.Body).Decode(&mBundle)
	mResp.Body.Close()
	if len(mBundle.MergeHistory) != 1 || len(mBundle.Notes) != 1 {
		t.Fatalf("manager should see MergeHistory and Notes: merges=%d notes=%d", len(mBundle.MergeHistory), len(mBundle.Notes))
	}
}

func TestPhase331CrossGuestReservationIsolationAndPreview(t *testing.T) {
	srv, store, svc, prop, _, mailMock, clk := setupPhase2TestServer(t, "pub-p331-cross")
	defer srv.Close()

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@example.com")
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}
	units, _ := svc.ListUnits(ctx, ownerActor, prop.ID, nil)
	tpls, _ := svc.ListCommunicationTemplates(ctx, ownerActor, prop.ID)

	now := clk.Now().UTC()
	resA, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID: prop.ID, GuestName: "Guest One", GuestEmail: "one@example.test", ChannelCode: "DIRECT",
		ArrivalDate: now.AddDate(0, 0, 5), DepartureDate: now.AddDate(0, 0, 7), Adults: 2, Status: domain.ResStatusConfirmed, UnitIDs: []uuid.UUID{units[0].ID},
	})
	if err != nil {
		t.Fatalf("create resA: %v", err)
	}
	resB, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID: prop.ID, GuestName: "Guest Two", GuestEmail: "two@example.test", ChannelCode: "DIRECT",
		ArrivalDate: now.AddDate(0, 0, 10), DepartureDate: now.AddDate(0, 0, 12), Adults: 2, Status: domain.ResStatusConfirmed, UnitIDs: []uuid.UUID{units[1].ID},
	})
	if err != nil {
		t.Fatalf("create resB: %v", err)
	}

	// SendGuestEmail with Guest A and Reservation B must fail and dispatch nothing.
	if _, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: resA.GuestID, ReservationID: &resB.ID, TemplateID: &tpls[0].ID,
	}); err == nil {
		t.Fatalf("expected SendGuestEmail to reject Guest A + Reservation B mismatch")
	}
	if _, err := svc.DispatchPendingEmails(ctx); err != nil {
		t.Fatalf("dispatch pending: %v", err)
	}
	if len(mailMock.sent) != 0 {
		t.Fatalf("expected 0 emails dispatched on mismatched link, got %d", len(mailMock.sent))
	}

	// PreviewCommunicationTemplate rejects mismatch AND fails cleanly on invalid guest/reservation IDs.
	if _, _, _, err := svc.PreviewCommunicationTemplate(ctx, ownerActor, prop.ID, tpls[0].ID, &resA.GuestID, &resB.ID); err == nil {
		t.Fatalf("expected PreviewCommunicationTemplate to reject Guest A + Reservation B mismatch")
	}
	missingID := uuid.New()
	if _, _, _, err := svc.PreviewCommunicationTemplate(ctx, ownerActor, prop.ID, tpls[0].ID, &missingID, nil); err == nil {
		t.Fatalf("expected PreviewCommunicationTemplate to return error on unknown guest_id instead of placeholder fallback")
	}
	if _, _, _, err := svc.PreviewCommunicationTemplate(ctx, ownerActor, prop.ID, tpls[0].ID, nil, &missingID); err == nil {
		t.Fatalf("expected PreviewCommunicationTemplate to return error on unknown reservation_id instead of placeholder fallback")
	}

	// Enquiry, Note, and Interaction must all reject cross-guest guest_id/reservation_id pairs.
	if _, err := svc.CreateGuestEnquiry(ctx, ownerActor, domain.GuestEnquiry{
		PropertyID: prop.ID, GuestID: &resA.GuestID, ReservationID: &resB.ID,
		Category: domain.EnqCatGeneral, Priority: domain.EnqPriorityNormal, Source: domain.EnqSourcePhone, Subject: "Mismatched enq",
	}); err == nil {
		t.Fatalf("expected CreateGuestEnquiry to reject cross-guest reservation link")
	}
	enqA, err := svc.CreateGuestEnquiry(ctx, ownerActor, domain.GuestEnquiry{
		PropertyID: prop.ID, GuestID: &resA.GuestID, ReservationID: &resA.ID,
		Category: domain.EnqCatGeneral, Priority: domain.EnqPriorityNormal, Source: domain.EnqSourcePhone, Subject: "Matched enq",
	})
	if err != nil {
		t.Fatalf("create matched enquiry: %v", err)
	}
	enqA.ReservationID = &resB.ID
	if _, err := svc.UpdateGuestEnquiry(ctx, ownerActor, enqA); err == nil {
		t.Fatalf("expected UpdateGuestEnquiry to reject cross-guest reservation link")
	}
	if _, err := svc.AddGuestNote(ctx, ownerActor, domain.GuestNote{
		PropertyID: prop.ID, GuestID: resA.GuestID, ReservationID: &resB.ID, Category: domain.NoteCatBooking, Content: "Wrong res",
	}); err == nil {
		t.Fatalf("expected AddGuestNote to reject cross-guest reservation link")
	}
	if _, err := svc.LogGuestInteraction(ctx, ownerActor, domain.GuestCommunication{
		PropertyID: prop.ID, GuestID: &resA.GuestID, ReservationID: &resB.ID, InteractionType: domain.InteractionPhoneCall, Direction: domain.CommDirInbound, Subject: "Mismatch call", Summary: "Details",
	}); err == nil {
		t.Fatalf("expected LogGuestInteraction to reject cross-guest reservation link")
	}

	// PostgreSQL trigger also blocks mismatched direct SQL inserts.
	if _, dbErr := store.Pool().Exec(ctx, `
		INSERT INTO guest_notes (id, property_id, guest_id, reservation_id, category, content)
		VALUES ($1, $2, $3, $4, 'GENERAL', 'Direct SQL bypass test')
	`, uuid.New(), prop.ID, resA.GuestID, resB.ID); dbErr == nil {
		t.Fatalf("expected PostgreSQL trigger to reject direct cross-guest reservation link on guest_notes")
	}

	// Merged alias referencing canonical guest's reservation works cleanly!
	alias, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{PropertyID: prop.ID, FirstName: "Alias", LastName: "One", Email: "one.alias@example.test"})
	if err != nil {
		t.Fatalf("create alias: %v", err)
	}
	if _, _, err := svc.MergeGuests(ctx, ownerActor, prop.ID, alias.ID, resA.GuestID, "Link alias"); err != nil {
		t.Fatalf("merge alias: %v", err)
	}
	if _, err := svc.AddGuestNote(ctx, ownerActor, domain.GuestNote{
		PropertyID: prop.ID, GuestID: alias.ID, ReservationID: &resA.ID, Category: domain.NoteCatBooking, Content: "Note via alias resolves cleanly",
	}); err != nil {
		t.Fatalf("expected note via merged alias to resolve to canonical reservation cleanly, got %v", err)
	}
}

func TestPhase331CanonicalAliasAnonymizationCompleteness(t *testing.T) {
	srv, store, svc, prop, _, _, clk := setupPhase2TestServer(t, "pub-p331-anon")
	defer srv.Close()

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@example.com")
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}
	units, _ := svc.ListUnits(ctx, ownerActor, prop.ID, nil)
	tpls, _ := svc.ListCommunicationTemplates(ctx, ownerActor, prop.ID)
	rules, _ := svc.ListCommunicationAutomationRules(ctx, ownerActor, prop.ID)

	const (
		piiName   = "CatrinPII"
		piiEmail  = "catrin.pii@example.test"
		piiPhone  = "+447700900444"
		piiSecret = "CatrinSecretPII-Token"
	)

	now := clk.Now().UTC()
	// Create confirmed past stay and transition through CHECKED_IN -> CHECKED_OUT so no active reservation blocks anonymisation; then set realistic financial ledger values.
	res, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID: prop.ID, GuestName: piiName + " Jones", GuestEmail: piiEmail, GuestPhone: piiPhone, ChannelCode: "DIRECT",
		ArrivalDate: now.AddDate(0, 0, -10), DepartureDate: now.AddDate(0, 0, -7), Adults: 2,
		Status: domain.ResStatusConfirmed, InternalNotes: "Booking secret: " + piiSecret, UnitIDs: []uuid.UUID{units[0].ID},
	})
	if err != nil {
		t.Fatalf("create past reservation: %v", err)
	}
	res, err = svc.TransitionReservationStatus(ctx, ownerActor, prop.ID, res.ID, domain.ResStatusCheckedIn, "arrived", res.Version)
	if err != nil {
		t.Fatalf("check-in past reservation: %v", err)
	}
	res, err = svc.TransitionReservationStatus(ctx, ownerActor, prop.ID, res.ID, domain.ResStatusCheckedOut, "departed", res.Version)
	if err != nil {
		t.Fatalf("check-out past reservation: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `
		UPDATE reservations
		SET total_pence = 36000, deposit_pence = 18000, amount_paid_pence = 36000, balance_due_pence = 0,
		    occupant_details = $2, cancellation_request_note = $2
		WHERE id = $1
	`, res.ID, "Occupant "+piiSecret); err != nil {
		t.Fatalf("seed ledger + occupant details: %v", err)
	}

	// Create duplicate alias with PII and merge into canonical guest.
	alias, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: piiName, LastName: "Alias", Email: "alias." + piiEmail, Phone: piiPhone, Notes: piiSecret,
	})
	if err != nil {
		t.Fatalf("create alias: %v", err)
	}
	if _, _, err := svc.MergeGuests(ctx, ownerActor, prop.ID, alias.ID, res.GuestID, "Merged "+piiSecret); err != nil {
		t.Fatalf("merge alias into canonical: %v", err)
	}

	// Add preference, enquiry + message, communication + delivery event, PENDING email_outbox (via reservation_id), and SCHEDULED communication_job.
	if _, err := svc.AddGuestPreference(ctx, ownerActor, domain.GuestPreference{
		PropertyID: prop.ID, GuestID: res.GuestID, Category: domain.PrefCatOther, PreferenceKey: "Key", PreferenceValue: piiSecret,
	}); err != nil {
		t.Fatalf("add pref: %v", err)
	}
	enq, err := svc.CreateGuestEnquiry(ctx, ownerActor, domain.GuestEnquiry{
		PropertyID: prop.ID, GuestID: &res.GuestID, ReservationID: &res.ID, Category: domain.EnqCatComplaint, Priority: domain.EnqPriorityHigh,
		Source: domain.EnqSourceEmail, Subject: "Subject " + piiSecret, Description: "Desc " + piiSecret, RequesterName: piiName, RequesterEmail: piiEmail, RequesterPhone: piiPhone,
	})
	if err != nil {
		t.Fatalf("create enquiry: %v", err)
	}
	if _, err := svc.AddGuestEnquiryMessage(ctx, ownerActor, domain.GuestEnquiryMessage{
		PropertyID: prop.ID, EnquiryID: enq.ID, Direction: domain.CommDirInbound, SenderName: piiName, SenderEmail: piiEmail, Body: "Message body " + piiSecret,
	}); err != nil {
		t.Fatalf("add enquiry message: %v", err)
	}
	comm, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: res.GuestID, ReservationID: &res.ID, TemplateID: &tpls[0].ID, Subject: "Email " + piiSecret, BodyText: "Hello " + piiSecret,
	})
	if err != nil {
		t.Fatalf("send guest email: %v", err)
	}

	// Seed a communication_delivery_event, a reservation-linked PENDING outbox row, and a SCHEDULED job.
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO communication_delivery_events (id, property_id, communication_id, event_type, provider, provider_event_id, detail, payload)
		VALUES ($1, $2, $3, 'DELIVERED', 'smtp', $4, $5, $6::jsonb)
	`, uuid.New(), prop.ID, comm.ID, "evt-"+uuid.NewString(), "Detail "+piiEmail, fmt.Sprintf(`{"email":%q}`, piiEmail)); err != nil {
		t.Fatalf("seed delivery event: %v", err)
	}
	pendingOutboxID := uuid.New()
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO email_outbox (id, property_id, reservation_id, recipient_email, template, subject, body_text, body_html, status)
		VALUES ($1, $2, $3, $4, 'booking_confirmation', $5, $5, $5, 'PENDING')
	`, pendingOutboxID, prop.ID, res.ID, piiEmail, "Pending body "+piiSecret); err != nil {
		t.Fatalf("seed reservation-linked outbox: %v", err)
	}
	jobID := uuid.New()
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO communication_jobs (id, property_id, rule_id, reservation_id, guest_id, trigger_event, scheduled_for_utc, stay_fingerprint, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'SCHEDULED')
	`, jobID, prop.ID, rules[0].ID, res.ID, res.GuestID, rules[0].TriggerEvent, now.Add(24*time.Hour), uuid.NewString()); err != nil {
		t.Fatalf("seed scheduled communication job: %v", err)
	}

	// Call AnonymizeGuest using the MERGED alias ID! It MUST resolve to canonical and scrub BOTH alias and canonical + all related tables.
	anonCanonical, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, alias.ID, "Right to erasure via alias ID")
	if err != nil {
		t.Fatalf("anonymize via merged alias ID: %v", err)
	}
	if anonCanonical.ID != res.GuestID || anonCanonical.Status != domain.GuestStatusAnonymized {
		t.Fatalf("expected canonical guest %s to become ANONYMIZED, got id=%s status=%s", res.GuestID, anonCanonical.ID, anonCanonical.Status)
	}

	// Verify reservations financial totals remain intact while PII is gone.
	resAfter, err := svc.GetReservation(ctx, ownerActor, prop.ID, res.ID)
	if err != nil {
		t.Fatalf("get anonymized reservation: %v", err)
	}
	if resAfter.TotalPence != 36000 || resAfter.AmountPaidPence != 36000 {
		t.Fatalf("expected reservation financial totals preserved, got %+v", resAfter)
	}

	// Assert ZERO PII remains across all scrubbed tables!
	checkZeroMatches := func(label, query string, args ...any) {
		var count int
		if err := store.Pool().QueryRow(ctx, query, args...).Scan(&count); err != nil {
			t.Fatalf("%s query: %v", label, err)
		}
		if count != 0 {
			t.Fatalf("%s: expected 0 PII matches remaining, found %d", label, count)
		}
	}
	checkZeroMatches("guests", `SELECT COUNT(*) FROM guests WHERE property_id = $1 AND (email <> '' OR phone <> '' OR full_name ILIKE $2 OR notes LIKE $3)`, prop.ID, "%"+piiName+"%", "%"+piiSecret+"%")
	checkZeroMatches("reservations", `SELECT COUNT(*) FROM reservations WHERE id = $1 AND (guest_name ILIKE $2 OR guest_email <> '' OR guest_phone <> '' OR occupant_details LIKE $3 OR internal_notes LIKE $3 OR cancellation_request_note LIKE $3)`, res.ID, "%"+piiName+"%", "%"+piiSecret+"%")
	checkZeroMatches("guest_merge_events", `SELECT COUNT(*) FROM guest_merge_events WHERE property_id = $1 AND (source_snapshot::text ILIKE $2 OR notes LIKE $3)`, prop.ID, "%"+piiName+"%", "%"+piiSecret+"%")
	checkZeroMatches("guest_enquiries", `SELECT COUNT(*) FROM guest_enquiries WHERE id = $1 AND (subject LIKE $2 OR description LIKE $2 OR requester_name ILIKE $3 OR requester_email <> '' OR requester_phone <> '')`, enq.ID, "%"+piiSecret+"%", "%"+piiName+"%")
	checkZeroMatches("guest_enquiry_messages", `SELECT COUNT(*) FROM guest_enquiry_messages WHERE enquiry_id = $1 AND (sender_email <> '' OR body LIKE $2)`, enq.ID, "%"+piiSecret+"%")
	checkZeroMatches("guest_communications", `SELECT COUNT(*) FROM guest_communications WHERE guest_id = $1 AND (subject LIKE $2 OR body_text LIKE $2 OR recipient_email = $3)`, res.GuestID, "%"+piiSecret+"%", piiEmail)
	checkZeroMatches("communication_delivery_events", `SELECT COUNT(*) FROM communication_delivery_events WHERE communication_id = $1 AND (detail LIKE $2 OR payload::text LIKE $2)`, comm.ID, "%"+piiEmail+"%")
	checkZeroMatches("email_outbox", `SELECT COUNT(*) FROM email_outbox WHERE property_id = $1 AND (recipient_email = $2 OR subject LIKE $3 OR body_text LIKE $3 OR status = 'PENDING')`, prop.ID, piiEmail, "%"+piiSecret+"%")
	checkZeroMatches("communication_jobs_scheduled", `SELECT COUNT(*) FROM communication_jobs WHERE id = $1 AND status = 'SCHEDULED'`, jobID)
}

func TestPhase331ExportConsentRetentionDuplicatesAndInternalMessages(t *testing.T) {
	srv, store, svc, prop, _, mailMock, clk := setupPhase2TestServer(t, "pub-p331-all")
	defer srv.Close()

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@example.com")
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}
	viewerUser, _ := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "p331enqviewer@example.com", FullName: "Emyr Viewer", Role: domain.RoleViewer, Password: "p331-enqviewer-2026",
	})
	// Grant a custom user without PermGuestsNotes? RoleStaff has PermCommsRead + PermCommsWrite, and NO PermGuestsNotes!
	staffUser, _ := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "p331enqstaff@example.com", FullName: "Beca Staff", Role: domain.RoleStaff, Password: "p331-enqstaff-2026",
	})
	_ = viewerUser
	staffActor := service.Actor{UserID: staffUser.ID, Email: staffUser.Email, FullName: staffUser.FullName, Role: staffUser.Role}

	adminUser, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "p331admin@example.com", FullName: "Owen Owner", Role: domain.RoleOwnerAdmin, Password: "p331-owner-pass-2026",
	})
	if err != nil {
		t.Fatalf("create adminUser: %v", err)
	}
	ownerClient, ownerCSRF := loginAdminClient(t, srv.URL, adminUser.Email, "p331-owner-pass-2026")
	_ = ownerCSRF

	// 1. Create guest and >250 enquiries (260) and >250 communications (260), plus guest notes & enquiry messages.
	heavyGuest, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Dilys", LastName: "HeavyExport", Email: "dilys.heavy@example.test",
	})
	if err != nil {
		t.Fatalf("create heavy guest: %v", err)
	}
	const confidentialNote = "CONFIDENTIAL_NOTE_TOKEN_NOT_FOR_EMAIL"
	if _, err := svc.AddGuestNote(ctx, ownerActor, domain.GuestNote{
		PropertyID: prop.ID, GuestID: heavyGuest.ID, Category: domain.NoteCatCustomerService, Content: confidentialNote,
	}); err != nil {
		t.Fatalf("add heavy note: %v", err)
	}
	baseTime := clk.Now().UTC()
	for i := 0; i < 260; i++ {
		if _, err := store.Pool().Exec(ctx, `
			INSERT INTO guest_enquiries (id, property_id, reference, guest_id, category, status, priority, source, subject, description, created_at, updated_at)
			VALUES ($1, $2, $3, $4, 'GENERAL', 'NEW', 'NORMAL', 'ADMIN', $5, 'desc', $6, $6)
		`, uuid.New(), prop.ID, fmt.Sprintf("ENQ-HEAVY-%03d", i), heavyGuest.ID, fmt.Sprintf("Enquiry %03d", i), baseTime.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("seed enquiry %d: %v", i, err)
		}
		if _, err := store.Pool().Exec(ctx, `
			INSERT INTO guest_communications (id, property_id, guest_id, interaction_type, direction, subject, summary, occurred_at, created_at, updated_at)
			VALUES ($1, $2, $3, 'PHONE_CALL', 'INBOUND', $4, 'call log', $5, $5, $5)
		`, uuid.New(), prop.ID, heavyGuest.ID, fmt.Sprintf("Comm %03d", i), baseTime.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("seed comm %d: %v", i, err)
		}
	}

	exp, err := svc.ExportGuestData(ctx, ownerActor, prop.ID, heavyGuest.ID)
	if err != nil {
		t.Fatalf("export heavy guest: %v", err)
	}
	if len(exp.Enquiries) != 260 || len(exp.Communications) != 260 {
		t.Fatalf("expected all 260 enquiries and 260 communications exported without 250 truncation, got enq=%d comms=%d", len(exp.Enquiries), len(exp.Communications))
	}
	if len(exp.Notes) != 1 || exp.Notes[0].AuthorName != "" || exp.ExportScopeNotice == "" {
		t.Fatalf("expected sanitized notes + scope notice in export, got notes=%+v notice=%q", exp.Notes, exp.ExportScopeNotice)
	}
	// Outbound emails STILL never include internal notes.
	if _, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: heavyGuest.ID, Subject: "Check", BodyText: "Plain greeting",
	}); err != nil {
		t.Fatalf("send email: %v", err)
	}
	_, _ = svc.DispatchPendingEmails(ctx)
	if len(mailMock.sent) == 0 || strings.Contains(mailMock.sent[0].BodyText, confidentialNote) {
		t.Fatalf("internal note must never leak into outbound emails")
	}

	// 2. Consent OccurredAt cannot be spoofed by client; append-only enforced in PostgreSQL.
	fakeBackdate := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	cResp := authedJSON(t, ownerClient, http.MethodPost, fmt.Sprintf("%s/api/v1/properties/%s/guests/%s/consent", srv.URL, prop.ID, heavyGuest.ID), ownerCSRF, mustJSON(t, map[string]any{
		"status": "GRANTED", "method": "WEB", "source": "api", "occurred_at": fakeBackdate.Format(time.RFC3339),
	}))
	var consentOut domain.GuestConsentRecord
	_ = json.NewDecoder(cResp.Body).Decode(&consentOut)
	cResp.Body.Close()
	if cResp.StatusCode != http.StatusCreated {
		t.Fatalf("record consent HTTP status=%d", cResp.StatusCode)
	}
	if consentOut.OccurredAt.Year() == 2020 || !consentOut.OccurredAt.Equal(clk.Now().UTC()) {
		t.Fatalf("expected server-generated OccurredAt %s, got client/other %s", clk.Now().UTC(), consentOut.OccurredAt)
	}
	if _, dbErr := store.Pool().Exec(ctx, `UPDATE guest_consent_records SET status = 'WITHDRAWN' WHERE id = $1`, consentOut.ID); dbErr == nil {
		t.Fatalf("expected DB trigger to block modifying guest_consent_records status")
	}
	if _, dbErr := store.Pool().Exec(ctx, `DELETE FROM guest_consent_records WHERE id = $1`, consentOut.ID); dbErr == nil {
		t.Fatalf("expected DB trigger to block deleting guest_consent_records rows")
	}

	// 3. Retention policy rejects AutoAnonymizeEnabled=true across Service, API, and WebUI form POST.
	retResp := authedJSON(t, ownerClient, http.MethodPut, fmt.Sprintf("%s/api/v1/properties/%s/retention-policy", srv.URL, prop.ID), ownerCSRF, mustJSON(t, map[string]any{
		"enquiry_retention_days": 365, "inactive_guest_retention_days": 1095, "comms_retention_days": 365, "auto_anonymize_enabled": true,
	}))
	retResp.Body.Close()
	if retResp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 rejecting auto_anonymize_enabled=true, got %d", retResp.StatusCode)
	}
	form := url.Values{"csrf_token": {ownerCSRF}, "enquiry_retention_days": {"365"}, "inactive_guest_retention_days": {"1095"}, "comms_retention_days": {"365"}, "auto_anonymize_enabled": {"true"}}
	webPost, err := ownerClient.PostForm(srv.URL+"/communications/retention-policy", form)
	if err != nil {
		t.Fatalf("post retention web form: %v", err)
	}
	webPost.Body.Close()
	if pol, err := svc.GetPropertyRetentionPolicy(ctx, ownerActor, prop.ID); err != nil || pol.AutoAnonymizeEnabled || pol.ExecutionMode != "ADVISORY_ONLY" {
		t.Fatalf("expected policy to remain manual/advisory only, got %+v err=%v", pol, err)
	}

	// 4. Enquiry direction=INTERNAL <-> is_internal invariant & GetGuestEnquiryDetail filtering.
	enqM, err := svc.CreateGuestEnquiry(ctx, ownerActor, domain.GuestEnquiry{
		PropertyID: prop.ID, GuestID: &heavyGuest.ID, Category: domain.EnqCatGeneral, Priority: domain.EnqPriorityNormal,
		Source: domain.EnqSourcePhone, Subject: "Enquiry visibility test",
	})
	if err != nil {
		t.Fatalf("create enqM: %v", err)
	}
	if _, err := svc.AddGuestEnquiryMessage(ctx, ownerActor, domain.GuestEnquiryMessage{
		PropertyID: prop.ID, EnquiryID: enqM.ID, Direction: domain.CommDirInternal, IsInternal: false, Body: "Bad inconsistent internal message",
	}); err == nil {
		t.Fatalf("expected direction=INTERNAL with is_internal=false to be rejected")
	}
	if _, err := svc.AddGuestEnquiryMessage(ctx, ownerActor, domain.GuestEnquiryMessage{
		PropertyID: prop.ID, EnquiryID: enqM.ID, Direction: domain.CommDirOutbound, IsInternal: true, Body: "Bad inconsistent outbound internal",
	}); err == nil {
		t.Fatalf("expected direction=OUTBOUND with is_internal=true to be rejected")
	}
	if _, dbErr := store.Pool().Exec(ctx, `
		INSERT INTO guest_enquiry_messages (id, property_id, enquiry_id, direction, body, is_internal)
		VALUES ($1, $2, $3, 'INTERNAL', 'Bypass check', FALSE)
	`, uuid.New(), prop.ID, enqM.ID); dbErr == nil {
		t.Fatalf("expected DB check constraint to reject direction=INTERNAL with is_internal=false")
	}
	if _, err := svc.AddGuestEnquiryMessage(ctx, ownerActor, domain.GuestEnquiryMessage{
		PropertyID: prop.ID, EnquiryID: enqM.ID, Direction: domain.CommDirInternal, IsInternal: true, Body: "Proper internal note",
	}); err != nil {
		t.Fatalf("add valid internal message: %v", err)
	}
	if _, err := svc.AddGuestEnquiryMessage(ctx, ownerActor, domain.GuestEnquiryMessage{
		PropertyID: prop.ID, EnquiryID: enqM.ID, Direction: domain.CommDirOutbound, IsInternal: false, Body: "External reply",
	}); err != nil {
		t.Fatalf("add valid external message: %v", err)
	}
	viewerActor := service.Actor{UserID: viewerUser.ID, Email: viewerUser.Email, FullName: viewerUser.FullName, Role: viewerUser.Role}
	if _, _, err := svc.GetGuestEnquiryDetail(ctx, viewerActor, prop.ID, enqM.ID); err == nil {
		t.Fatalf("viewer without PermCommsRead must not access GetGuestEnquiryDetail")
	}
	_, staffMsgs, err := svc.GetGuestEnquiryDetail(ctx, staffActor, prop.ID, enqM.ID)
	if err != nil {
		t.Fatalf("staff get enquiry detail: %v", err)
	}
	if len(staffMsgs) != 2 {
		t.Fatalf("staff with PermGuestsNotes should see both internal and external enquiry messages, got %d", len(staffMsgs))
	}

	// 5. Duplicate detection >250 active guests: create 270 guests with duplicate pair #266/#267 (older updated_at than first 250).
	for i := 0; i < 270; i++ {
		email := fmt.Sprintf("unique.%03d@example.test", i)
		first := fmt.Sprintf("Guest%03d", i)
		if i == 266 || i == 267 {
			email = "beyond250.dup@example.test"
			first = "BeyondTwoFifty"
		}
		if _, err := store.Pool().Exec(ctx, `
			INSERT INTO guests (id, property_id, first_name, last_name, full_name, email, normalized_email, status, updated_at)
			VALUES ($1, $2, $3, 'Tester', $4, $5, $5, 'ACTIVE', $6)
		`, uuid.New(), prop.ID, first, first+" Tester", email, baseTime.Add(-time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("seed guest %d: %v", i, err)
		}
	}
	dupScan, err := svc.ListDuplicateCandidates(ctx, ownerActor, prop.ID)
	if err != nil {
		t.Fatalf("scan duplicates >250: %v", err)
	}
	if !dupScan.IsComplete || dupScan.TotalActiveGuests < 270 || dupScan.ScannedGuests < 270 {
		t.Fatalf("expected complete scan across >=270 active guests, got %+v", dupScan)
	}
	foundLateDup := false
	for _, c := range dupScan.Candidates {
		if strings.EqualFold(c.GuestA.Email, "beyond250.dup@example.test") && strings.EqualFold(c.GuestB.Email, "beyond250.dup@example.test") {
			foundLateDup = true
			break
		}
	}
	if !foundLateDup {
		t.Fatalf("expected duplicate pair beyond 250th guest to be detected cleanly")
	}
}
