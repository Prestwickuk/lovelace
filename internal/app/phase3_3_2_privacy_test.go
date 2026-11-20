package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/mailer"
	"github.com/Prestwickuk/lovelace/internal/service"
	"github.com/google/uuid"
)

type recordingMailer struct {
	mu      sync.Mutex
	sent    []mailer.Message
	failErr error
}

func (m *recordingMailer) Send(ctx context.Context, msg mailer.Message) error {
	if ctx != nil && ctx.Err() != nil {
		return mailer.TerminatedBeforeSend(ctx.Err())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failErr != nil {
		return m.failErr
	}
	m.sent = append(m.sent, msg)
	return nil
}

func (m *recordingMailer) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func TestPhase332AuditMinimizationAndControlledRedaction(t *testing.T) {
	srv, store, svc, prop, _, _, clk := setupPhase2TestServer(t, "pub-p332-audit")
	defer srv.Close()

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@example.com")
	if err != nil {
		t.Fatalf("load owner: %v", err)
	}
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role, IPAddress: "127.0.0.1", UserAgent: "audit-test"}

	staffUser, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "p332staff@example.com", FullName: "P332 Staff", Role: domain.RoleStaff, Password: "p332-staff-pass-2026",
	})
	if err != nil {
		t.Fatalf("create staff: %v", err)
	}
	viewerUser, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "p332viewer@example.com", FullName: "P332 Viewer", Role: domain.RoleViewer, Password: "p332-viewer-pass-2026",
	})
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	propB, err := store.CreateProperty(ctx, nil, domain.Property{
		Slug: "p332-other-site", Name: "Other P332 Site", CountryCode: "GB", Timezone: "Europe/London", Currency: "GBP",
		ContactEmail: "other@example.test", CheckInTime: "15:00", CheckOutTime: "10:00",
	})
	if err != nil {
		t.Fatalf("create propB: %v", err)
	}
	ownerBUser, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "p332ownerb@example.test", FullName: "Owner B", Role: domain.RoleOwnerAdmin, PropertyID: &propB.ID, Password: "p332-ownerb-pass-2026",
	})
	if err != nil {
		t.Fatalf("create propB owner: %v", err)
	}

	const (
		secretName        = "Rhiannon PrivacyPII"
		secretFirst       = "Rhiannon"
		secretLast        = "PrivacyPII"
		secretEmail       = "rhiannon.privacy@example.test"
		secretPhone       = "+447700900333"
		secretGuestNote   = "SECRET_GUEST_NOTE_SHOULD_NOT_BE_IN_AUDIT"
		secretEnqBody     = "SECRET_ENQUIRY_MESSAGE_BODY_TOKEN"
		secretEmailBody   = "SECRET_OUTBOUND_EMAIL_BODY_TOKEN"
		secretResOccNote  = "SECRET_OCCUPANT_DETAILS_TOKEN"
		secretConsentAddr = "198.51.100.77"
	)

	guest, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: secretFirst, LastName: secretLast,
		Email: secretEmail, Phone: secretPhone, Notes: secretGuestNote, Status: domain.GuestStatusActive,
	})
	if err != nil {
		t.Fatalf("create guest: %v", err)
	}
	units, err := store.ListUnits(ctx, prop.ID, nil, true)
	if err != nil || len(units) == 0 {
		t.Fatalf("list units: %v", err)
	}
	res, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID: prop.ID, GuestID: guest.ID, GuestName: secretName, GuestEmail: secretEmail, GuestPhone: secretPhone,
		ChannelCode: "DIRECT", ArrivalDate: clk.Now().AddDate(0, 0, 10), DepartureDate: clk.Now().AddDate(0, 0, 12),
		Adults: 2, OccupantDetails: secretResOccNote, InternalNotes: secretGuestNote,
		Status: domain.ResStatusConfirmed, UnitIDs: []uuid.UUID{units[0].ID},
	})
	if err != nil {
		t.Fatalf("create reservation: %v", err)
	}
	enq, err := svc.CreateGuestEnquiry(ctx, ownerActor, domain.GuestEnquiry{
		PropertyID: prop.ID, GuestID: &guest.ID, ReservationID: &res.ID,
		Category: domain.EnqCatGeneral, Priority: domain.EnqPriorityNormal, Source: domain.EnqSourceEmail,
		RequesterName: secretName, RequesterEmail: secretEmail, RequesterPhone: secretPhone,
		Subject: secretEnqBody, Description: secretEnqBody,
	})
	if err != nil {
		t.Fatalf("create enquiry: %v", err)
	}
	if _, err := svc.AddGuestEnquiryMessage(ctx, ownerActor, domain.GuestEnquiryMessage{
		PropertyID: prop.ID, EnquiryID: enq.ID, Direction: "INBOUND", SenderName: secretName, SenderEmail: secretEmail, Body: secretEnqBody,
	}); err != nil {
		t.Fatalf("add enquiry message: %v", err)
	}
	if _, err := svc.AddGuestNote(ctx, ownerActor, domain.GuestNote{
		PropertyID: prop.ID, GuestID: guest.ID, Category: domain.NoteCatCustomerService, Content: secretGuestNote,
	}); err != nil {
		t.Fatalf("add guest note: %v", err)
	}
	if _, err := svc.AddGuestPreference(ctx, ownerActor, domain.GuestPreference{
		PropertyID: prop.ID, GuestID: guest.ID, Category: domain.PrefCatOther, PreferenceKey: "quiet", PreferenceValue: secretGuestNote, Notes: secretGuestNote,
	}); err != nil {
		t.Fatalf("set guest pref: %v", err)
	}
	if _, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: guest.ID, ReservationID: &res.ID, EnquiryID: &enq.ID,
		Recipient: secretEmail, Subject: secretEmailBody, BodyText: secretEmailBody,
	}); err != nil {
		t.Fatalf("send guest email: %v", err)
	}
	if _, err := svc.RecordGuestConsent(ctx, ownerActor, domain.GuestConsentRecord{
		PropertyID: prop.ID, GuestID: guest.ID, ConsentType: "MARKETING_EMAIL",
		Status: domain.ConsentGranted, Method: "WEB", Source: "admin", PolicyVersion: "2026-v2",
		Notes: secretConsentAddr,
	}); err != nil {
		t.Fatalf("record consent: %v", err)
	}

	// Trigger public booking hold + cancel to verify pseudonymous actor_email and minimized metadata.
	pubClient := newClient(t)
	hResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/booking/holds", "pub-p332-audit", "", map[string]any{
		"property_slug":  "demo-property",
		"unit_type_code": "grass-pitch",
		"arrival_date":   clk.Now().AddDate(0, 0, 20).Format("2006-01-02"),
		"departure_date": clk.Now().AddDate(0, 0, 22).Format("2006-01-02"),
		"adults":         2,
		"guest_name":     secretName,
		"guest_email":    secretEmail,
		"guest_phone":    secretPhone,
		"accept_terms":   true,
	})
	var holdOut service.CreateHoldResult
	_ = json.NewDecoder(hResp.Body).Decode(&holdOut)
	hResp.Body.Close()
	if hResp.StatusCode != http.StatusCreated {
		t.Fatalf("create hold status=%d", hResp.StatusCode)
	}
	cResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/booking/holds/"+holdOut.Hold.ID.String()+"/cancel", "pub-p332-audit", "", map[string]any{})
	cResp.Body.Close()
	if cResp.StatusCode != http.StatusOK {
		t.Fatalf("cancel hold status=%d", cResp.StatusCode)
	}

	// Cancel res and anonymize guest; this must also emit minimized audit events.
	if _, err := svc.TransitionReservationStatus(ctx, ownerActor, prop.ID, res.ID, domain.ResStatusCancelled, secretGuestNote, res.Version); err != nil {
		t.Fatalf("cancel res: %v", err)
	}
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, guest.ID, secretGuestNote); err != nil {
		t.Fatalf("anonymize guest: %v", err)
	}

	// Inspect raw audit_logs rows directly in PG17.
	piiTokens := []string{
		secretName, secretFirst, secretLast, secretEmail, secretPhone,
		secretGuestNote, secretEnqBody, secretEmailBody, secretResOccNote, secretConsentAddr,
	}
	rows, err := store.Pool().Query(ctx, `
		SELECT id, action, resource_type, actor_email,
		       COALESCE(before_state::text, ''), COALESCE(after_state::text, ''), COALESCE(metadata::text, '')
		FROM audit_logs
		ORDER BY created_at DESC, id DESC
	`)
	if err != nil {
		t.Fatalf("query audit_logs: %v", err)
	}
	defer rows.Close()
	var sawPublicHoldCreate, sawPublicHoldCancel, sawAnonymize bool
	for rows.Next() {
		var id int64
		var action, resType, actorEmail, beforeTxt, afterTxt, metaTxt string
		if err := rows.Scan(&id, &action, &resType, &actorEmail, &beforeTxt, &afterTxt, &metaTxt); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		if action == "booking_hold.created" {
			sawPublicHoldCreate = true
			if actorEmail != "guest@public.lovelace" {
				t.Fatalf("expected pseudonymous actor_email on public hold create, got %q", actorEmail)
			}
		}
		if action == "booking_hold.cancelled" {
			sawPublicHoldCancel = true
			if actorEmail != "guest@public.lovelace" {
				t.Fatalf("expected pseudonymous actor_email on public hold cancel, got %q", actorEmail)
			}
		}
		if action == "guest.anonymized" {
			sawAnonymize = true
		}
		combined := actorEmail + " " + beforeTxt + " " + afterTxt + " " + metaTxt
		for _, tok := range piiTokens {
			if strings.Contains(strings.ToLower(combined), strings.ToLower(tok)) {
				t.Fatalf("audit row %d (%s/%s) leaked forbidden PII token %q: before=%s after=%s meta=%s",
					id, action, resType, tok, beforeTxt, afterTxt, metaTxt)
			}
		}
	}
	if !sawPublicHoldCreate || !sawPublicHoldCancel || !sawAnonymize {
		t.Fatalf("missing expected minimized audit events: holdCreate=%v holdCancel=%v anon=%v",
			sawPublicHoldCreate, sawPublicHoldCancel, sawAnonymize)
	}

	// Verify append-only immutability on audit_logs and seed a simulated legacy pre-3.2 PII audit entry.
	var legacyAuditID int64
	if err := store.Pool().QueryRow(ctx, `
		INSERT INTO audit_logs (property_id, actor_email, actor_role, action, resource_type, resource_id, request_id, before_state, after_state, metadata)
		VALUES ($1, 'legacy.pii.guest@example.test', 'PUBLIC', 'reservation.updated', 'reservation', $2, 'req-legacy',
		        '{"guest_name":"Legacy Raw Name","guest_email":"legacy@raw.test"}'::jsonb,
		        '{"guest_phone":"+447700999111"}'::jsonb,
		        '{"raw_subject":"Historical PII Subject"}'::jsonb)
		RETURNING id
	`, prop.ID, res.ID.String()).Scan(&legacyAuditID); err != nil {
		t.Fatalf("insert simulated legacy audit row: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE audit_logs SET metadata = '{}' WHERE id = $1`, legacyAuditID); err == nil {
		t.Fatalf("expected UPDATE on audit_logs to fail trigger immutability")
	}
	if _, err := store.Pool().Exec(ctx, `DELETE FROM audit_logs WHERE id = $1`, legacyAuditID); err == nil {
		t.Fatalf("expected DELETE on audit_logs to fail trigger immutability")
	}

	ownerClient, ownerCSRF := loginAdminClient(t, srv.URL, "owner@example.com", "lovelace-owner-password-2026")
	staffClient, staffCSRF := loginAdminClient(t, srv.URL, staffUser.Email, "p332-staff-pass-2026")
	viewerClient, viewerCSRF := loginAdminClient(t, srv.URL, viewerUser.Email, "p332-viewer-pass-2026")
	ownerBClient, ownerBCSRF := loginAdminClient(t, srv.URL, ownerBUser.Email, "p332-ownerb-pass-2026")

	redactURL := fmt.Sprintf("%s/api/v1/properties/%s/audit-logs/%d/redact", srv.URL, prop.ID, legacyAuditID)
	// Unauthorized callers (staff, viewer, cross-property ownerB) must be denied.
	for _, tc := range []struct {
		name   string
		client *http.Client
		csrf   string
	}{
		{"staff", staffClient, staffCSRF},
		{"viewer", viewerClient, viewerCSRF},
		{"cross_property_owner", ownerBClient, ownerBCSRF},
	} {
		resp := authedJSON(t, tc.client, http.MethodPost, redactURL, tc.csrf, mustJSON(t, map[string]any{"reason": "Authorized privacy cleanup request"}))
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s expected 403 on audit redaction, got %d", tc.name, resp.StatusCode)
		}
	}

	// Short/blank reason rejected with 422.
	badReasonResp := authedJSON(t, ownerClient, http.MethodPost, redactURL, ownerCSRF, mustJSON(t, map[string]any{"reason": "bad"}))
	badReasonResp.Body.Close()
	if badReasonResp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on short redaction reason, got %d", badReasonResp.StatusCode)
	}

	// Authorized property owner redacts the legacy entry via HTTP.
	okResp := authedJSON(t, ownerClient, http.MethodPost, redactURL, ownerCSRF, mustJSON(t, map[string]any{
		"reason": "Phase 3.3.2 legacy raw PII historical redaction",
	}))
	var redactedOut domain.AuditLog
	_ = json.NewDecoder(okResp.Body).Decode(&redactedOut)
	okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK || !redactedOut.IsRedacted {
		t.Fatalf("expected 200 + IsRedacted=true, got %d %+v", okResp.StatusCode, redactedOut)
	}

	// Second redaction attempt returns 409 Conflict.
	dupResp := authedJSON(t, ownerClient, http.MethodPost, redactURL, ownerCSRF, mustJSON(t, map[string]any{
		"reason": "Duplicate redaction attempt",
	}))
	dupResp.Body.Close()
	if dupResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 on duplicate redaction, got %d", dupResp.StatusCode)
	}

	// Reading effective audit logs returns redacted view while keeping structural action/resource metadata.
	effectiveLogs, err := svc.ListAuditLogs(ctx, ownerActor, &prop.ID, "", 100)
	if err != nil {
		t.Fatalf("list effective audit logs: %v", err)
	}
	var foundRedacted, foundRedactionAudit bool
	for _, entry := range effectiveLogs {
		if entry.ID == legacyAuditID {
			foundRedacted = true
			if !entry.IsRedacted || entry.ActorEmail != "redacted@redacted.lovelace" {
				t.Fatalf("expected redacted effective view, got %+v", entry)
			}
			rawCombined := string(entry.BeforeState) + string(entry.AfterState) + string(entry.Metadata)
			if strings.Contains(rawCombined, "Legacy Raw Name") || strings.Contains(rawCombined, "legacy@raw.test") {
				t.Fatalf("effective audit entry still exposed historical PII: %s", rawCombined)
			}
			if entry.Action != "reservation.updated" || entry.ResourceID != res.ID.String() {
				t.Fatalf("structural audit fields were unexpectedly lost: %+v", entry)
			}
		}
		if entry.Action == "audit_log.redacted" && entry.ResourceID == fmt.Sprintf("%d", legacyAuditID) {
			foundRedactionAudit = true
			if strings.Contains(string(entry.Metadata), "Legacy Raw Name") {
				t.Fatalf("redaction audit event must not embed original PII")
			}
		}
	}
	if !foundRedacted || !foundRedactionAudit {
		t.Fatalf("missing redacted entry (%v) or audit_log.redacted (%v)", foundRedacted, foundRedactionAudit)
	}

	// audit_log_redactions overlay table itself must be strictly append-only too.
	if _, err := store.Pool().Exec(ctx, `UPDATE audit_log_redactions SET reason = 'tampered' WHERE audit_log_id = $1`, legacyAuditID); err == nil {
		t.Fatalf("expected UPDATE on audit_log_redactions to be rejected")
	}
	if _, err := store.Pool().Exec(ctx, `DELETE FROM audit_log_redactions WHERE audit_log_id = $1`, legacyAuditID); err == nil {
		t.Fatalf("expected DELETE on audit_log_redactions to be rejected")
	}
}

func TestPhase332BookingHoldExportAndAnonymisationLifecycle(t *testing.T) {
	srv, store, svc, prop, _, mailMock, clk := setupPhase2TestServer(t, "pub-p332-holds")
	defer srv.Close()
	_ = mailMock

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@example.com")
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}
	pubClient := newClient(t)

	// 1. Abandoned public hold never confirmed -> guest_id must remain NULL.
	abResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/booking/holds", "pub-p332-holds", "", map[string]any{
		"property_slug":    "demo-property",
		"unit_type_code":   "grass-pitch",
		"arrival_date":     clk.Now().AddDate(0, 0, 8).Format("2006-01-02"),
		"departure_date":   clk.Now().AddDate(0, 0, 10).Format("2006-01-02"),
		"adults":           2,
		"guest_name":       "Abandoned Unlinked Hold",
		"guest_email":      "abandoned.hold@example.test",
		"guest_phone":      "+447700900411",
		"special_requests": "Near water tap",
		"accept_terms":     true,
	})
	var abHold service.CreateHoldResult
	_ = json.NewDecoder(abResp.Body).Decode(&abHold)
	abResp.Body.Close()
	if abResp.StatusCode != http.StatusCreated || abHold.Hold.GuestID != nil {
		t.Fatalf("expected abandoned hold status=201 with nil guest_id, got %d %+v", abResp.StatusCode, abHold.Hold.GuestID)
	}

	// 2. Confirmed public booking hold -> links booking_holds.guest_id cleanly upon payment confirmation.
	const targetName = "Megan HoldPrivacy"
	const targetEmail = "megan.hold@example.test"
	const targetReq = "QUIET_CORNER_SPECIAL_REQ"
	hResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/booking/holds", "pub-p332-holds", "", map[string]any{
		"property_slug":    "demo-property",
		"unit_type_code":   "bell-tent",
		"arrival_date":     clk.Now().AddDate(0, 0, 14).Format("2006-01-02"),
		"departure_date":   clk.Now().AddDate(0, 0, 16).Format("2006-01-02"),
		"adults":           2,
		"guest_name":       targetName,
		"guest_email":      targetEmail,
		"guest_phone":      "+447700900422",
		"special_requests": targetReq,
		"accept_terms":     true,
	})
	var holdRes service.CreateHoldResult
	_ = json.NewDecoder(hResp.Body).Decode(&holdRes)
	hResp.Body.Close()
	if hResp.StatusCode != http.StatusCreated {
		t.Fatalf("create target hold status=%d", hResp.StatusCode)
	}

	coResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/checkout", "pub-p332-holds", "", map[string]any{
		"hold_id":         holdRes.Hold.ID.String(),
		"idempotency_key": "idem-megan-p332",
	})
	var coOut service.CreateCheckoutResponse
	_ = json.NewDecoder(coResp.Body).Decode(&coOut)
	coResp.Body.Close()
	if coResp.StatusCode != http.StatusOK {
		t.Fatalf("checkout status=%d", coResp.StatusCode)
	}

	whStatus, _ := signAndSendStripeWebhook(t, pubClient, srv.URL, "whsec_test_lovelace_phase2_secret", clk.Now(), map[string]any{
		"id":       "evt_p332_hold_link",
		"type":     "checkout.session.completed",
		"livemode": false,
		"data": map[string]any{
			"object": map[string]any{
				"id":             coOut.Attempt.ProviderSessionID,
				"payment_intent": "pi_p332_hold_1",
				"amount_total":   holdRes.Quote.DepositDuePence,
				"currency":       "gbp",
				"payment_status": "paid",
				"metadata": map[string]any{
					"payment_attempt_id": coOut.PaymentAttemptID.String(),
				},
			},
		},
	})
	if whStatus != http.StatusOK {
		t.Fatalf("webhook confirm status=%d", whStatus)
	}

	holdAfter, err := store.GetBookingHold(ctx, nil, holdRes.Hold.ID)
	if err != nil || holdAfter.GuestID == nil || holdAfter.ConfirmedReservationID == nil {
		t.Fatalf("expected hold linked to guest & confirmed reservation, got %+v err=%v", holdAfter, err)
	}

	// Verify export contains Megan's linked hold (without abandoned hold or secret hashes).
	exp, err := svc.ExportGuestData(ctx, ownerActor, prop.ID, *holdAfter.GuestID)
	if err != nil {
		t.Fatalf("export guest with linked hold: %v", err)
	}
	if len(exp.BookingHolds) != 1 || exp.BookingHolds[0].ID != holdRes.Hold.ID {
		t.Fatalf("expected exactly 1 linked hold in export, got %+v", exp.BookingHolds)
	}
	if exp.BookingHolds[0].CustomerSessionHash != "" {
		t.Fatalf("export must never include customer session hash")
	}

	// Cancel confirmed stay and anonymize Megan -> hold PII must be scrubbed while financials & unlinked abandoned hold remain intact.
	res, err := svc.GetReservation(ctx, ownerActor, prop.ID, *holdAfter.ConfirmedReservationID)
	if err != nil {
		t.Fatalf("load reservation: %v", err)
	}
	if _, err := svc.TransitionReservationStatus(ctx, ownerActor, prop.ID, res.ID, domain.ResStatusCancelled, "Erasure request", res.Version); err != nil {
		t.Fatalf("cancel reservation: %v", err)
	}
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, *holdAfter.GuestID, "UK GDPR Art 17 request"); err != nil {
		t.Fatalf("anonymize guest: %v", err)
	}

	scrubbedHold, err := store.GetBookingHold(ctx, nil, holdRes.Hold.ID)
	if err != nil {
		t.Fatalf("get scrubbed hold: %v", err)
	}
	if scrubbedHold.GuestEmail != "" || scrubbedHold.GuestPhone != "" || scrubbedHold.SpecialRequests != "" || strings.Contains(scrubbedHold.GuestName, "Megan") {
		t.Fatalf("expected anonymized booking hold PII scrubbed, got %+v", scrubbedHold)
	}
	if scrubbedHold.UnitTypeID != holdRes.Hold.UnitTypeID || scrubbedHold.GuestID == nil {
		t.Fatalf("expected hold accommodation metadata and guest_id reference preserved, got %+v", scrubbedHold)
	}

	// Abandoned unlinked hold must remain completely untouched and have NULL guest_id.
	abAfter, err := store.GetBookingHold(ctx, nil, abHold.Hold.ID)
	if err != nil || abAfter.GuestID != nil || abAfter.GuestEmail != "abandoned.hold@example.test" {
		t.Fatalf("abandoned hold should remain untouched with nil guest_id, got %+v err=%v", abAfter, err)
	}
}

func TestPhase332OutboxAnonymisationRaceAndConcurrency(t *testing.T) {
	srv, store, svc, prop, _, _, clk := setupPhase2TestServer(t, "pub-p332-outbox")
	defer srv.Close()

	recMailer := &recordingMailer{}
	svc.SetEmailSender(recMailer)

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@example.com")
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	// Helper to create a guest, queue an email, returning guest + outboxID.
	createGuestWithOutbox := func(email string) (domain.Guest, uuid.UUID) {
		g, gErr := svc.CreateGuest(ctx, ownerActor, domain.Guest{
			PropertyID: prop.ID, FirstName: "Outbox", LastName: "TestGuest", Email: email, Status: domain.GuestStatusActive,
		})
		if gErr != nil {
			t.Fatalf("create guest %s: %v", email, gErr)
		}
		comm, cErr := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
			PropertyID: prop.ID, GuestID: g.ID, Recipient: email, Subject: "Private Subject " + email, BodyText: "Private Body " + email,
		})
		if cErr != nil {
			t.Fatalf("queue email for %s: %v", email, cErr)
		}
		var outboxID uuid.UUID
		if qErr := store.Pool().QueryRow(ctx, `SELECT email_outbox_id FROM guest_communications WHERE id = $1`, comm.ID).Scan(&outboxID); qErr != nil {
			t.Fatalf("find outbox id: %v", qErr)
		}
		return g, outboxID
	}

	// Case A: Anonymize BEFORE dispatch -> queued PENDING email transitions to CANCELLED and never calls mailer.
	gA, outA := createGuestWithOutbox("race.casea@example.test")
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gA.ID, "Case A pre-dispatch erasure"); err != nil {
		t.Fatalf("anonymize gA: %v", err)
	}
	var stA, recA, subjA, bodyA string
	if err := store.Pool().QueryRow(ctx, `SELECT status, recipient_email, subject, body_text FROM email_outbox WHERE id = $1`, outA).Scan(&stA, &recA, &subjA, &bodyA); err != nil {
		t.Fatalf("inspect outA: %v", err)
	}
	if stA != domain.OutboxStatusCancelled || (recA != "" && recA != "[redacted]") || strings.Contains(recA+subjA+bodyA, "race.casea") {
		t.Fatalf("expected Case A cancelled + scrubbed, got st=%s rec=%q subj=%q body=%q", stA, recA, subjA, bodyA)
	}

	// Case B: Worker claims due item (status=PROCESSING, worker_token set), then BEFORE AcquireOutboxSendPermission,
	// AnonymizeGuest marks it CANCEL_REQUESTED -> AcquireOutboxSendPermission aborts send cleanly -> status CANCELLED, mailer Send NEVER called.
	gB, outB := createGuestWithOutbox("race.caseb@example.test")
	claimedB, err := store.ClaimDueOutboxEmails(ctx, clk.Now().UTC(), 10)
	if err != nil || len(claimedB) != 1 || claimedB[0].ID != outB || claimedB[0].WorkerToken == nil {
		t.Fatalf("expected claimed outB with worker_token, got %+v err=%v", claimedB, err)
	}
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gB.ID, "Case B pre-transport erasure"); err != nil {
		t.Fatalf("anonymize gB while PROCESSING: %v", err)
	}
	var stMid string
	_ = store.Pool().QueryRow(ctx, `SELECT status FROM email_outbox WHERE id = $1`, outB).Scan(&stMid)
	if stMid != domain.OutboxStatusCancelRequested {
		t.Fatalf("expected CANCEL_REQUESTED during active lease, got %s", stMid)
	}
	allowed, err := store.AcquireOutboxSendPermission(ctx, outB, claimedB[0].WorkerToken, clk.Now().UTC())
	if err != nil || allowed {
		t.Fatalf("expected AcquireOutboxSendPermission=false after cancel request, got allowed=%v err=%v", allowed, err)
	}
	var stAfterB, recB, bodyB string
	_ = store.Pool().QueryRow(ctx, `SELECT status, recipient_email, body_text FROM email_outbox WHERE id = $1`, outB).Scan(&stAfterB, &recB, &bodyB)
	if stAfterB != domain.OutboxStatusCancelled || (recB != "" && recB != "[redacted]") || strings.Contains(recB+bodyB, "race.caseb") {
		t.Fatalf("expected Case B CANCELLED + scrubbed, got st=%s rec=%q body=%q", stAfterB, recB, bodyB)
	}

	// Case C1: In-flight provider dispatch acquired (DISPATCHING), while AnonymizeGuest runs concurrently.
	// AnonymizeGuest records erasure_requested_at and returns ErrConflict while dispatch is in flight;
	// MarkOutboxEmailSentWithToken then records honest SENT outcome while scrubbing recipient/subject/body,
	// and retrying AnonymizeGuest finalises erasure cleanly.
	gC1, outC1 := createGuestWithOutbox("race.casec1@example.test")
	claimedC1, err := store.ClaimDueOutboxEmails(ctx, clk.Now().UTC(), 10)
	if err != nil || len(claimedC1) != 1 {
		t.Fatalf("claim C1: %+v err=%v", claimedC1, err)
	}
	allowedC1, err := store.AcquireOutboxSendPermission(ctx, outC1, claimedC1[0].WorkerToken, clk.Now().UTC())
	if err != nil || !allowedC1 {
		t.Fatalf("acquire send permission C1: allowed=%v err=%v", allowedC1, err)
	}
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gC1.ID, "Case C1 mid-flight erasure"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict while dispatch in-flight in C1, got: %v", err)
	}
	if err := store.MarkOutboxEmailSentWithToken(ctx, outC1, claimedC1[0].WorkerToken, clk.Now().UTC()); err != nil {
		t.Fatalf("mark sent after concurrent cancel: %v", err)
	}
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gC1.ID, "Case C1 mid-flight erasure"); err != nil {
		t.Fatalf("finalise anonymize C1 after dispatch settlement: %v", err)
	}
	var stC1, recC1, bodyC1 string
	_ = store.Pool().QueryRow(ctx, `SELECT status, recipient_email, body_text FROM email_outbox WHERE id = $1`, outC1).Scan(&stC1, &recC1, &bodyC1)
	if stC1 != domain.OutboxStatusSent || (recC1 != "" && recC1 != "[redacted]") || strings.Contains(recC1+bodyC1, "race.casec1") {
		t.Fatalf("expected Case C1 honest SENT + scrubbed PII, got st=%s rec=%q body=%q", stC1, recC1, bodyC1)
	}

	// Case C2: Worker transient failure after erasure_requested_at must settle to CANCELLED (NOT back to PENDING retry!),
	// and AnonymizeGuest finalises cleanly once DISPATCHING settles.
	gC2, outC2 := createGuestWithOutbox("race.casec2@example.test")
	claimedC2, err := store.ClaimDueOutboxEmails(ctx, clk.Now().UTC(), 10)
	if err != nil || len(claimedC2) != 1 {
		t.Fatalf("claim C2: %+v err=%v", claimedC2, err)
	}
	allowedC2, _ := store.AcquireOutboxSendPermission(ctx, outC2, claimedC2[0].WorkerToken, clk.Now().UTC())
	if !allowedC2 {
		t.Fatalf("expected acquire permission C2")
	}
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gC2.ID, "Case C2 retry-cancel erasure"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict while dispatch in-flight in C2, got: %v", err)
	}
	if err := store.MarkOutboxEmailRetryWithToken(ctx, outC2, claimedC2[0].WorkerToken, "simulated timeout", clk.Now().UTC().Add(5*time.Minute)); err != nil {
		t.Fatalf("mark retry after cancel: %v", err)
	}
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gC2.ID, "Case C2 retry-cancel erasure"); err != nil {
		t.Fatalf("finalise anonymize C2 after retry settlement: %v", err)
	}
	var stC2 string
	_ = store.Pool().QueryRow(ctx, `SELECT status FROM email_outbox WHERE id = $1`, outC2).Scan(&stC2)
	if stC2 != domain.OutboxStatusCancelled {
		t.Fatalf("expected CANCELLED instead of re-queueing PENDING retry, got %s", stC2)
	}

	// Case D: Crashed worker leaves PROCESSING lease expired -> next sweep cancels it without calling mailer.Send.
	gD, outD := createGuestWithOutbox("race.cased@example.test")
	claimedD, err := store.ClaimDueOutboxEmails(ctx, clk.Now().UTC(), 10)
	if err != nil || len(claimedD) != 1 || claimedD[0].ID != outD {
		t.Fatalf("claim D: %+v err=%v", claimedD, err)
	}
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gD.ID, "Case D crashed worker lease"); err != nil {
		t.Fatalf("anonymize D: %v", err)
	}
	// Advance clock beyond 5m lease expiry and dispatch.
	clk.Advance(6 * time.Minute)
	sent, err := svc.DispatchPendingEmails(ctx)
	if err != nil || sent != 0 || recMailer.Count() != 0 {
		t.Fatalf("expected 0 emails sent across all anonymised cases, sent=%d mailerCount=%d err=%v", sent, recMailer.Count(), err)
	}
	var stD string
	_ = store.Pool().QueryRow(ctx, `SELECT status FROM email_outbox WHERE id = $1`, outD).Scan(&stD)
	if stD != domain.OutboxStatusCancelled {
		t.Fatalf("expected expired CANCEL_REQUESTED lease settled to CANCELLED, got %s", stD)
	}

	// Sending/queuing a new email for anonymized guest or retrying a cancelled email must fail.
	if _, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: gA.ID, Subject: "Blocked", BodyText: "Should never send",
	}); err == nil {
		t.Fatalf("expected SendGuestEmail to reject anonymized guest")
	}
	if err := svc.RetryFailedOutboxEmail(ctx, ownerActor, prop.ID, outD); err == nil {
		t.Fatalf("expected RetryFailedOutboxEmail to reject cancelled/anonymized outbox row")
	}
}

func TestPhase332RelationshipsConsentMergeLineageAndDismissals(t *testing.T) {
	srv, store, svc, prop, _, _, clk := setupPhase2TestServer(t, "pub-p332-crm")
	defer srv.Close()

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@example.com")
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}
	ownerClient, ownerCSRF := loginAdminClient(t, srv.URL, "owner@example.com", "lovelace-owner-password-2026")
	units, _ := store.ListUnits(ctx, prop.ID, nil, true)

	// 1. Cross-guest enquiry/reservation validation in SendGuestEmail and template preview.
	g1, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{PropertyID: prop.ID, FirstName: "Gwyneth", LastName: "One", Email: "gwyneth.one@example.test"})
	if err != nil {
		t.Fatalf("create g1: %v", err)
	}
	g2, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{PropertyID: prop.ID, FirstName: "Iwan", LastName: "Two", Email: "iwan.two@example.test"})
	if err != nil {
		t.Fatalf("create g2: %v", err)
	}
	resG2, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID: prop.ID, GuestID: g2.ID, GuestName: "Iwan Two", GuestEmail: "iwan.two@example.test",
		ChannelCode: "DIRECT", ArrivalDate: clk.Now().AddDate(0, 0, 5), DepartureDate: clk.Now().AddDate(0, 0, 7),
		Adults: 2, Status: domain.ResStatusConfirmed, UnitIDs: []uuid.UUID{units[0].ID},
	})
	if err != nil {
		t.Fatalf("create resG2: %v", err)
	}
	enqG2, err := svc.CreateGuestEnquiry(ctx, ownerActor, domain.GuestEnquiry{
		PropertyID: prop.ID, GuestID: &g2.ID, ReservationID: &resG2.ID,
		Category: domain.EnqCatBooking, Priority: domain.EnqPriorityNormal, Source: domain.EnqSourceEmail,
		Subject: "Iwan booking question", Description: "Check-in question",
	})
	if err != nil {
		t.Fatalf("create enqG2: %v", err)
	}
	// Enquiry linked ONLY via reservation (nil guest_id on enquiry) belonging to g2!
	enqViaResG2, err := svc.CreateGuestEnquiry(ctx, ownerActor, domain.GuestEnquiry{
		PropertyID: prop.ID, ReservationID: &resG2.ID,
		Category: domain.EnqCatBooking, Priority: domain.EnqPriorityNormal, Source: domain.EnqSourceEmail,
		Subject: "Reservation-linked enquiry", Description: "Question on resG2",
	})
	if err != nil {
		t.Fatalf("create enqViaResG2: %v", err)
	}

	if _, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: g1.ID, ReservationID: &resG2.ID, Subject: "Mismatch res", BodyText: "Should fail",
	}); err == nil {
		t.Fatalf("expected SendGuestEmail to reject g1 + resG2")
	}
	if _, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: g1.ID, EnquiryID: &enqG2.ID, Subject: "Mismatch enq", BodyText: "Should fail",
	}); err == nil {
		t.Fatalf("expected SendGuestEmail to reject g1 + enqG2")
	}
	if _, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: g1.ID, EnquiryID: &enqViaResG2.ID, Subject: "Mismatch enq via res", BodyText: "Should fail",
	}); err == nil {
		t.Fatalf("expected SendGuestEmail to reject g1 + reservation-linked enqViaResG2")
	}

	templates, err := svc.ListCommunicationTemplates(ctx, ownerActor, prop.ID)
	if err != nil || len(templates) == 0 {
		t.Fatalf("list templates: %v", err)
	}
	if _, _, _, err := svc.PreviewCommunicationTemplateWithEnquiry(ctx, ownerActor, prop.ID, templates[0].ID, &g1.ID, &resG2.ID, &enqG2.ID); err == nil {
		t.Fatalf("expected PreviewCommunicationTemplateWithEnquiry to reject cross-guest reservation/enquiry")
	}
	prevResp := authedJSON(t, ownerClient, http.MethodPost, fmt.Sprintf("%s/api/v1/properties/%s/communication-templates/%s/preview", srv.URL, prop.ID, templates[0].ID), ownerCSRF, mustJSON(t, map[string]any{
		"guest_id":   g1.ID.String(),
		"enquiry_id": enqG2.ID.String(),
	}))
	prevResp.Body.Close()
	if prevResp.StatusCode != http.StatusUnprocessableEntity && prevResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 422/400 on cross-guest template preview HTTP endpoint, got %d", prevResp.StatusCode)
	}

	// 2. Consent immutability trigger: arbitrary UPDATE guest_id must fail even with session var attempts,
	// whereas legitimate MergeGuests succeeds and transfers consent records to canonical target.
	c1, err := svc.RecordGuestConsent(ctx, ownerActor, domain.GuestConsentRecord{
		PropertyID: prop.ID, GuestID: g1.ID, ConsentType: "MARKETING_EMAIL", Status: domain.ConsentGranted,
		Method: "CHECKBOX", Source: "web", PolicyVersion: "2026-v1",
	})
	if err != nil {
		t.Fatalf("record c1: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE guest_consent_records SET guest_id = $1 WHERE id = $2`, g2.ID, c1.ID); err == nil {
		t.Fatalf("expected direct SQL UPDATE guest_id on guest_consent_records to be rejected without verified merge")
	}
	tx, err := store.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	_, _ = tx.Exec(ctx, `SET LOCAL lovelace.allow_consent_merge_reparent = 'on'`)
	_, spoofErr := tx.Exec(ctx, `UPDATE guest_consent_records SET guest_id = $1 WHERE id = $2`, g2.ID, c1.ID)
	_ = tx.Rollback(ctx)
	if spoofErr == nil {
		t.Fatalf("expected session-variable spoof attempt on guest_consent_records to still fail hardened DB trigger")
	}

	// 3. Multi-hop merge lineage A -> B -> C preserves complete history on C and in export.
	gA, _ := svc.CreateGuest(ctx, ownerActor, domain.Guest{PropertyID: prop.ID, FirstName: "Lineage", LastName: "HopA", Email: "hop.a@example.test"})
	gB, _ := svc.CreateGuest(ctx, ownerActor, domain.Guest{PropertyID: prop.ID, FirstName: "Lineage", LastName: "HopB", Email: "hop.b@example.test"})
	gC, _ := svc.CreateGuest(ctx, ownerActor, domain.Guest{PropertyID: prop.ID, FirstName: "Lineage", LastName: "HopC", Email: "hop.c@example.test"})

	if _, err := svc.RecordGuestConsent(ctx, ownerActor, domain.GuestConsentRecord{
		PropertyID: prop.ID, GuestID: gA.ID, ConsentType: "MARKETING_EMAIL", Status: domain.ConsentGranted, Method: "WEB", Source: "form", PolicyVersion: "2026-v1",
	}); err != nil {
		t.Fatalf("record consent gA: %v", err)
	}

	clk.Advance(1 * time.Hour)
	if _, _, err := svc.MergeGuests(ctx, ownerActor, prop.ID, gA.ID, gB.ID, "First hop A->B"); err != nil {
		t.Fatalf("merge A->B: %v", err)
	}
	clk.Advance(1 * time.Hour)
	if _, _, err := svc.MergeGuests(ctx, ownerActor, prop.ID, gB.ID, gC.ID, "Second hop B->C"); err != nil {
		t.Fatalf("merge B->C: %v", err)
	}

	detailC, err := svc.GetGuestDetail(ctx, ownerActor, prop.ID, gC.ID)
	if err != nil || len(detailC.MergeHistory) != 2 {
		t.Fatalf("expected full 2-hop merge lineage on Guest C, got len=%d (%+v) err=%v", len(detailC.MergeHistory), detailC.MergeHistory, err)
	}
	history := detailC.MergeHistory
	var sawAB, sawBC bool
	for _, ev := range history {
		if ev.SourceGuestID == gA.ID && ev.TargetGuestID == gB.ID {
			sawAB = true
		}
		if ev.SourceGuestID == gB.ID && ev.TargetGuestID == gC.ID {
			sawBC = true
		}
	}
	if !sawAB || !sawBC {
		t.Fatalf("expected original A->B and B->C hops preserved verbatim, got %+v", history)
	}
	expC, err := svc.ExportGuestData(ctx, ownerActor, prop.ID, gC.ID)
	if err != nil || len(expC.MergeHistory) != 2 || len(expC.ConsentHistory) != 1 {
		t.Fatalf("expected Guest C export with 2-hop merge history & transferred consent, got merges=%d consent=%d err=%v", len(expC.MergeHistory), len(expC.ConsentHistory), err)
	}

	// Cross-property merge history check fails cleanly.
	otherProp, err := store.CreateProperty(ctx, nil, domain.Property{
		Slug: "p332-iso-check", Name: "Iso Prop", CountryCode: "GB", Timezone: "Europe/London", Currency: "GBP", ContactEmail: "iso@example.test", CheckInTime: "15:00", CheckOutTime: "10:00",
	})
	if err != nil {
		t.Fatalf("create otherProp: %v", err)
	}
	if foreignHist, err := store.ListGuestMergeHistory(ctx, nil, otherProp.ID, gC.ID); err != nil || len(foreignHist) != 0 {
		t.Fatalf("expected cross-property store query to return empty history, got %d err=%v", len(foreignHist), err)
	}

	// 4. Duplicate dismissal privacy: export includes dismissals (with staff name cleared) and anonymisation scrubs reason.
	dup1, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Teleri", LastName: "DupCheck", Email: "teleri.dup1@example.test", Phone: "+447700900888",
	})
	if err != nil {
		t.Fatalf("create dup1: %v", err)
	}
	dup2, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Teleri", LastName: "DupCheck", Email: "teleri.dup2@example.test", Phone: "+447700900888",
	})
	if err != nil {
		t.Fatalf("create dup2: %v", err)
	}
	const dismissNote = "Sisters sharing phone +447700900888"
	if err := svc.DismissGuestDuplicateWithCategory(ctx, ownerActor, prop.ID, dup1.ID, dup2.ID, "HOUSEHOLD", dismissNote); err != nil {
		t.Fatalf("dismiss duplicate: %v", err)
	}

	expDup, err := svc.ExportGuestData(ctx, ownerActor, prop.ID, dup1.ID)
	if err != nil || len(expDup.DuplicateDismissals) != 1 {
		t.Fatalf("expected 1 duplicate dismissal in dup1 export, got len=%d err=%v", len(expDup.DuplicateDismissals), err)
	}
	if expDup.DuplicateDismissals[0].DismissedBy != nil || expDup.DuplicateDismissals[0].ReasonCategory != "HOUSEHOLD" {
		t.Fatalf("expected cleared dismissed_by + category preserved in export, got %+v", expDup.DuplicateDismissals[0])
	}

	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, dup1.ID, "Erasure request"); err != nil {
		t.Fatalf("anonymize dup1: %v", err)
	}
	var disReason, disCat string
	var disAt time.Time
	var disBy *uuid.UUID
	aID, bID := dup1.ID, dup2.ID
	if strings.Compare(aID.String(), bID.String()) > 0 {
		aID, bID = bID, aID
	}
	if err := store.Pool().QueryRow(ctx, `
		SELECT reason, reason_category, dismissed_at, dismissed_by
		FROM guest_duplicate_dismissals
		WHERE property_id = $1 AND guest_a_id = $2 AND guest_b_id = $3
	`, prop.ID, aID, bID).Scan(&disReason, &disCat, &disAt, &disBy); err != nil {
		t.Fatalf("inspect anonymized dismissal: %v", err)
	}
	if disCat != "ANONYMISED" || strings.Contains(disReason, "Sisters") || strings.Contains(disReason, "+447700900888") || disAt.IsZero() || disBy == nil {
		t.Fatalf("expected scrubbed reason & category=ANONYMISED with structural metadata intact, got reason=%q cat=%q at=%v by=%v", disReason, disCat, disAt, disBy)
	}

	// Dismissed pair must remain excluded from candidate duplicate pairs even after anonymisation.
	scan, err := svc.ListDuplicateCandidates(ctx, ownerActor, prop.ID)
	if err != nil {
		t.Fatalf("list duplicate candidates: %v", err)
	}
	for _, c := range scan.Candidates {
		if (c.GuestA.ID == dup1.ID && c.GuestB.ID == dup2.ID) || (c.GuestA.ID == dup2.ID && c.GuestB.ID == dup1.ID) {
			t.Fatalf("dismissed pair must remain suppressed after anonymisation: %+v", c)
		}
	}
}
