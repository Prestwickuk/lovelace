package app_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/app"
	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
)

func signEmailWebhookBody(secret string, raw []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(raw)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestPhase3Milestone3GuestProfileSegmentationAndMerge(t *testing.T) {
	srv, store, svc, prop, _, _, clk := setupPhase2TestServer(t, "pub-p33-merge")
	defer srv.Close()

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	units, err := svc.ListUnits(ctx, ownerActor, prop.ID, nil)
	if err != nil || len(units) < 2 {
		t.Fatalf("list seeded units: %v (len=%d)", err, len(units))
	}
	unit1, unit2 := units[0], units[1]

	// Ensure default CRM templates & automation rules were seeded automatically.
	tpls, err := svc.ListCommunicationTemplates(ctx, ownerActor, prop.ID)
	if err != nil || len(tpls) < 4 {
		t.Fatalf("expected seeded communication templates >= 4, got len=%d err=%v", len(tpls), err)
	}

	now := clk.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	// 1. Cancelled + Inquiry reservations do NOT count a guest as returning.
	inqyRes, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:      prop.ID,
		GuestName:       "BrynCancelled Guest",
		GuestEmail:      "BRYN.CANCELLED@Example.Test ",
		GuestPhone:      "0044 7700 900111",
		ChannelCode:     "DIRECT",
		ArrivalDate:     today.AddDate(0, 0, 10),
		DepartureDate:   today.AddDate(0, 0, 12),
		Adults:          2,
		OccupantDetails: "2 adults walking Coast Path",
		Status:          domain.ResStatusInquiry,
		UnitIDs:         []uuid.UUID{unit1.ID},
	})
	if err != nil {
		t.Fatalf("create inquiry res: %v", err)
	}
	if inqyRes.OccupantDetails != "2 adults walking Coast Path" {
		t.Fatalf("expected occupant_details persisted, got %q", inqyRes.OccupantDetails)
	}

	cancelRes, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "BrynCancelled Guest",
		GuestEmail:    "bryn.cancelled@example.test",
		GuestPhone:    "+447700900111",
		ChannelCode:   "DIRECT",
		ArrivalDate:   today.AddDate(0, 0, 14),
		DepartureDate: today.AddDate(0, 0, 16),
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unit1.ID},
	})
	if err != nil {
		t.Fatalf("create confirmed-then-cancelled res: %v", err)
	}
	if _, err := svc.TransitionReservationStatus(ctx, ownerActor, prop.ID, cancelRes.ID, domain.ResStatusCancelled, "Guest could not travel", cancelRes.Version); err != nil {
		t.Fatalf("cancel reservation: %v", err)
	}

	newSeg, err := svc.ListGuests(ctx, ownerActor, prop.ID, domain.GuestDirectoryFilter{Segment: "NEW", Query: "bryn"})
	if err != nil || len(newSeg) != 1 {
		t.Fatalf("expected normalised email/phone match to share 1 guest in NEW segment, got len=%d err=%v", len(newSeg), err)
	}
	if newSeg[0].IsReturningGuest || newSeg[0].CompletedStays != 0 || newSeg[0].CancelledBookings != 1 {
		t.Fatalf("cancelled/inquiry stays must not mark guest returning: %+v", newSeg[0])
	}

	// 2. Checked-out completed stay + new upcoming stay classifies guest as RETURNING.
	pastStay, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Lowri Returning",
		GuestEmail:    "lowri@pembrokeshire.test",
		GuestPhone:    "+44 7700 900222",
		ChannelCode:   "DIRECT",
		ArrivalDate:   today.AddDate(0, 0, -5),
		DepartureDate: today.AddDate(0, 0, -2),
		Adults:        2,
		Status:        domain.ResStatusCheckedIn,
		UnitIDs:       []uuid.UUID{unit1.ID},
	})
	if err != nil {
		t.Fatalf("create past checked-in stay: %v", err)
	}
	if _, err := svc.TransitionReservationStatus(ctx, ownerActor, prop.ID, pastStay.ID, domain.ResStatusCheckedOut, "", pastStay.Version); err != nil {
		t.Fatalf("check out past stay: %v", err)
	}
	if _, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Lowri Returning",
		GuestEmail:    "lowri@pembrokeshire.test",
		ChannelCode:   "DIRECT",
		ArrivalDate:   today.AddDate(0, 0, 20),
		DepartureDate: today.AddDate(0, 0, 22),
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unit2.ID},
	}); err != nil {
		t.Fatalf("create upcoming returning stay: %v", err)
	}

	retSeg, err := svc.ListGuests(ctx, ownerActor, prop.ID, domain.GuestDirectoryFilter{Segment: "RETURNING"})
	if err != nil || len(retSeg) != 1 || !retSeg[0].IsReturningGuest || retSeg[0].CompletedStays != 1 || retSeg[0].UpcomingStays != 1 {
		t.Fatalf("expected Lowri in RETURNING segment with 1 completed + 1 upcoming stay, got %+v err=%v", retSeg, err)
	}

	// 3. Create likely duplicate guest profile sharing surname + phone, attach CRM entities, and merge atomically.
	dupSrc, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID,
		FirstName:  "Lowri",
		LastName:   "Returning",
		Email:      "lowri.alt@example.test",
		Phone:      "0044 7700 900222",
	})
	if err != nil {
		t.Fatalf("create duplicate source guest: %v", err)
	}
	if _, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestID:       dupSrc.ID,
		GuestName:     "Lowri Returning",
		GuestEmail:    "lowri.alt@example.test",
		ChannelCode:   "DIRECT",
		ArrivalDate:   today.AddDate(0, 0, 28),
		DepartureDate: today.AddDate(0, 0, 30),
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unit1.ID},
	}); err != nil {
		t.Fatalf("create reservation on duplicate guest: %v", err)
	}
	if _, err := svc.AddGuestPreference(ctx, ownerActor, domain.GuestPreference{
		PropertyID:      prop.ID,
		GuestID:         dupSrc.ID,
		Category:        domain.PrefCatPet,
		PreferenceKey:   "Dog friendly pitch",
		PreferenceValue: "Far from playground",
	}); err != nil {
		t.Fatalf("add dup preference: %v", err)
	}
	if _, err := svc.AddGuestNote(ctx, ownerActor, domain.GuestNote{
		PropertyID: prop.ID,
		GuestID:    dupSrc.ID,
		Category:   domain.NoteCatOperational,
		Content:    "Prefers quiet corner near orchard gate",
	}); err != nil {
		t.Fatalf("add dup note: %v", err)
	}
	if _, err := svc.CreateGuestEnquiry(ctx, ownerActor, domain.GuestEnquiry{
		PropertyID: prop.ID,
		GuestID:    &dupSrc.ID,
		Category:   domain.EnqCatSpecialRequest,
		Priority:   domain.EnqPriorityNormal,
		Source:     domain.EnqSourcePhone,
		Subject:    "Extra log basket on arrival",
	}); err != nil {
		t.Fatalf("add dup enquiry: %v", err)
	}
	if _, err := svc.LogGuestInteraction(ctx, ownerActor, domain.GuestCommunication{
		PropertyID:      prop.ID,
		GuestID:         &dupSrc.ID,
		InteractionType: domain.InteractionPhoneCall,
		Direction:       domain.CommDirInbound,
		Subject:         "Called about firewood",
		Summary:         "Requested kiln-dried logs",
		OccurredAt:      now,
	}); err != nil {
		t.Fatalf("add dup comm: %v", err)
	}

	dupScan, err := svc.ListDuplicateCandidates(ctx, ownerActor, prop.ID)
	if err != nil || len(dupScan.Candidates) == 0 || !dupScan.IsComplete {
		t.Fatalf("expected heuristic duplicate candidate pair, got len=%d complete=%v err=%v", len(dupScan.Candidates), dupScan.IsComplete, err)
	}

	targetID := retSeg[0].ID
	mergedTarget, mergeEv, err := svc.MergeGuests(ctx, ownerActor, prop.ID, dupSrc.ID, targetID, "Same guest via alternate email")
	if err != nil {
		t.Fatalf("merge guests: %v", err)
	}
	if mergedTarget.ID != targetID || mergeEv.SourceGuestID != dupSrc.ID || mergeEv.TargetGuestID != targetID {
		t.Fatalf("unexpected merge result: guest=%+v ev=%+v", mergedTarget, mergeEv)
	}

	// Opening the old source ID redirects transparently to the merged target with all combined records!
	bundle, err := svc.GetGuestDetail(ctx, ownerActor, prop.ID, dupSrc.ID)
	if err != nil {
		t.Fatalf("get detail for merged source id: %v", err)
	}
	if bundle.Guest.ID != targetID || len(bundle.Reservations) != 3 || len(bundle.Preferences) != 1 || len(bundle.Notes) != 1 || len(bundle.Enquiries) != 1 || len(bundle.Communications) != 2 || len(bundle.MergeHistory) != 1 {
		t.Fatalf("expected merged 360 history preserved on canonical target, got res=%d pref=%d notes=%d enq=%d comms=%d merges=%d",
			len(bundle.Reservations), len(bundle.Preferences), len(bundle.Notes), len(bundle.Enquiries), len(bundle.Communications), len(bundle.MergeHistory))
	}
}

func TestPhase3Milestone3NotesVsPreferencesAndTemplateIsolation(t *testing.T) {
	srv, store, svc, prop, _, mailMock, clk := setupPhase2TestServer(t, "pub-p33-tpl")
	defer srv.Close()

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}
	units, _ := svc.ListUnits(ctx, ownerActor, prop.ID, nil)

	res, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:      prop.ID,
		GuestName:       `Dafydd <script>alert(1)</script> Evans`,
		GuestEmail:      "dafydd.evans@example.test",
		ChannelCode:     "DIRECT",
		ArrivalDate:     clk.Now().AddDate(0, 0, 5),
		DepartureDate:   clk.Now().AddDate(0, 0, 7),
		Adults:          2,
		OccupantDetails: "2 adults + dog",
		Status:          domain.ResStatusConfirmed,
		UnitIDs:         []uuid.UUID{units[0].ID},
	})
	if err != nil {
		t.Fatalf("create reservation: %v", err)
	}
	guestID := res.GuestID

	// Add operational preference and internal note with strict confidentiality sentinel.
	if _, err := svc.AddGuestPreference(ctx, ownerActor, domain.GuestPreference{
		PropertyID:      prop.ID,
		GuestID:         guestID,
		Category:        domain.PrefCatAccessibility,
		PreferenceKey:   "Step-free pitch",
		PreferenceValue: "Needs level access",
	}); err != nil {
		t.Fatalf("add preference: %v", err)
	}
	const secretNote = "INTERNAL-ONLY-VIP-CAUTION-DO-NOT-EMAIL"
	if _, err := svc.AddGuestNote(ctx, ownerActor, domain.GuestNote{
		PropertyID: prop.ID,
		GuestID:    guestID,
		Category:   domain.NoteCatCustomerService,
		Content:    secretNote,
		IsPinned:   true,
	}); err != nil {
		t.Fatalf("add internal note: %v", err)
	}

	// 1. Disallowed placeholders must be rejected when creating/updating templates.
	if _, err := svc.CreateCommunicationTemplate(ctx, ownerActor, domain.CommunicationTemplate{
		PropertyID:       prop.ID,
		Code:             "bad_tpl",
		Name:             "Bad Template",
		Category:         "GENERAL",
		Locale:           "en",
		SubjectTemplate:  "Hi {{guest_first_name}}",
		BodyTextTemplate: "Secret: {{internal_notes}}",
		IsActive:         true,
	}); err == nil {
		t.Fatalf("expected validation error for disallowed placeholder {{internal_notes}}")
	}

	// 2. Valid versioned template preview/render escapes HTML and never includes internal notes.
	tpl, err := svc.CreateCommunicationTemplate(ctx, ownerActor, domain.CommunicationTemplate{
		PropertyID:       prop.ID,
		Code:             "welcome_note",
		Name:             "Custom Welcome",
		Category:         "PRE_ARRIVAL",
		Locale:           "en",
		SubjectTemplate:  "Welcome to {{property_name}} ({{reservation_reference}})",
		BodyTextTemplate: "Hello {{guest_full_name}}, check-in starts at {{check_in_time}} for {{unit_names}}.",
		BodyHTMLTemplate: "<p>Hello <strong>{{guest_full_name}}</strong> for {{unit_names}}</p>",
		IsActive:         true,
	})
	if err != nil {
		t.Fatalf("create valid template: %v", err)
	}
	// Update template increments immutable version snapshot.
	tpl.Name = "Custom Welcome v2"
	tpl.SubjectTemplate = "Your stay at {{property_name}}: {{reservation_reference}}"
	tpl2, err := svc.UpdateCommunicationTemplate(ctx, ownerActor, tpl)
	if err != nil || tpl2.Version != 2 {
		t.Fatalf("expected template version 2, got v=%d err=%v", tpl2.Version, err)
	}

	// 3. Reject SMTP header injection in manual subject.
	if _, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID:    prop.ID,
		GuestID:       guestID,
		ReservationID: &res.ID,
		Subject:       "Hello\r\nBcc: attacker@evil.test",
		BodyText:      "Test body",
	}); err == nil {
		t.Fatalf("expected SMTP header injection attempt with CRLF to be rejected")
	}

	// 4. Send templated email & dispatch; inspect rendered body for HTML escaping & zero internal note leak.
	comm, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID:    prop.ID,
		GuestID:       guestID,
		ReservationID: &res.ID,
		TemplateID:    &tpl2.ID,
	})
	if err != nil {
		t.Fatalf("send templated email: %v", err)
	}
	if _, err := svc.DispatchPendingEmails(ctx); err != nil {
		t.Fatalf("dispatch email: %v", err)
	}
	if len(mailMock.sent) != 1 {
		t.Fatalf("expected 1 sent email, got %d", len(mailMock.sent))
	}
	sentMsg := mailMock.sent[0]
	if strings.Contains(sentMsg.BodyText, secretNote) || strings.Contains(sentMsg.BodyHTML, secretNote) || strings.Contains(sentMsg.Subject, secretNote) {
		t.Fatalf("internal guest note leaked into outbound email!")
	}
	if strings.Contains(sentMsg.BodyHTML, "<script>") || !strings.Contains(sentMsg.BodyHTML, "&lt;script&gt;") {
		t.Fatalf("expected HTML template auto-escaping for guest name, got BodyHTML=%s", sentMsg.BodyHTML)
	}
	if comm.TemplateVersionID == nil {
		t.Fatalf("expected communication linked to immutable template_version_id")
	}

	// 5. GDPR Export includes preferences, communications, notes (with staff author minimized), and explicit scope notice.
	exported, err := svc.ExportGuestData(ctx, ownerActor, prop.ID, guestID)
	if err != nil {
		t.Fatalf("export guest data: %v", err)
	}
	if strings.TrimSpace(exported.ExportScopeNotice) == "" {
		t.Fatalf("expected explicit export scope notice in ExportGuestData")
	}
	if len(exported.Notes) == 0 {
		t.Fatalf("expected guest notes holding subject records in ExportGuestData, got 0")
	}
	if len(exported.Preferences) != 1 || len(exported.Communications) == 0 {
		t.Fatalf("expected preferences and communications in export, got prefs=%d comms=%d", len(exported.Preferences), len(exported.Communications))
	}
}

func TestPhase3Milestone3EnquiriesMaintenanceCommsWebhookAndAutomation(t *testing.T) {
	// Configure dedicated server with EmailWebhookSecret to test HMAC signature enforcement.
	_, store, _, prop, stripeMock, mailMock, clk := setupPhase2TestServer(t, "pub-p33-auto")
	_ = stripeMock
	emailSecret := "email-webhook-secret-p33"
	dbURL := ""
	_ = dbURL

	cfg := config.Config{
		AppEnv:              "test",
		CookieSecure:        false,
		EncryptionKey:       testEncryptionKey,
		SessionTTL:          time.Hour,
		SessionIdleTTL:      30 * time.Minute,
		LoginMaxFailIP:      25,
		LoginMaxFailAcc:     20,
		LoginWindow:         15 * time.Minute,
		PublicOrigin:        "http://127.0.0.1:3000",
		PublicAPISecret:     "pub-p33-auto",
		StripeWebhookSecret: "whsec_test_lovelace_phase2_secret",
		EmailFrom:           "bookings@tirbach.wales",
		EmailWebhookSecret:  emailSecret,
	}
	svc := service.New(store, cfg)
	svc.SetPaymentProvider(&fakeStripeProvider{})
	svc.SetEmailSender(mailMock)
	svc.SetClock(clk.Now)
	router, err := app.NewRouter(cfg, store, svc)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}
	cats, _ := svc.ListMaintenanceCategories(ctx, ownerActor, prop.ID, false)
	units, _ := svc.ListUnits(ctx, ownerActor, prop.ID, nil)

	// 1. Public hold + Stripe webhook confirmation -> auto outbox confirmation email -> ACCEPTED on guest timeline.
	pubClient := newClient(t)
	arrDate := clk.Now().AddDate(0, 0, 5)
	depDate := clk.Now().AddDate(0, 0, 8)
	holdRes := createTestHoldViaAPI(t, srv, pubClient, cfg.PublicAPISecret, arrDate.Format("2006-01-02"), depDate.Format("2006-01-02"))
	coResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/checkout", cfg.PublicAPISecret, cfg.PublicOrigin, map[string]any{
		"hold_id":         holdRes.Hold.ID.String(),
		"idempotency_key": "idem-crm-auto-1",
	})
	var coOut service.CreateCheckoutResponse
	_ = json.NewDecoder(coResp.Body).Decode(&coOut)
	coResp.Body.Close()

	whStatus, _ := signAndSendStripeWebhook(t, pubClient, srv.URL, cfg.StripeWebhookSecret, clk.Now(), map[string]any{
		"id":       "evt_crm_paid_1",
		"type":     "checkout.session.completed",
		"livemode": false,
		"data": map[string]any{
			"object": map[string]any{
				"id":             coOut.Attempt.ProviderSessionID,
				"payment_intent": "pi_crm_1",
				"amount_total":   holdRes.Quote.TotalPence,
				"currency":       "gbp",
				"payment_status": "paid",
				"metadata":       map[string]any{"payment_attempt_id": coOut.PaymentAttemptID.String()},
			},
		},
	})
	if whStatus != http.StatusOK {
		t.Fatalf("stripe webhook status=%d", whStatus)
	}
	if _, err := svc.DispatchPendingEmails(ctx); err != nil {
		t.Fatalf("dispatch booking confirmation: %v", err)
	}

	guests, err := svc.ListGuests(ctx, ownerActor, prop.ID, domain.GuestDirectoryFilter{Query: "carys@example.wales"})
	if err != nil || len(guests) != 1 {
		t.Fatalf("expected booked guest Carys in CRM directory, got len=%d err=%v", len(guests), err)
	}
	carysID := guests[0].ID
	detail, err := svc.GetGuestDetail(ctx, ownerActor, prop.ID, carysID)
	if err != nil || len(detail.Communications) == 0 || detail.Communications[0].EmailOutboxID == nil {
		t.Fatalf("expected booking confirmation on guest timeline, got %+v err=%v", detail.Communications, err)
	}
	confComm := detail.Communications[0]
	if confComm.DeliveryStatus != domain.DeliveryAccepted {
		t.Fatalf("SMTP handoff must record ACCEPTED before provider webhook confirms delivery, got %s", confComm.DeliveryStatus)
	}

	// 2. Signed delivery webhook verifies signature, updates ACCEPTED -> DELIVERED, then BOUNCED alert + retry.
	evBody, _ := json.Marshal(map[string]any{
		"provider":          "postmark",
		"provider_event_id": "pm-deliv-1",
		"event_type":        "DELIVERED",
		"email_outbox_id":   confComm.EmailOutboxID,
		"detail":            "Delivered cleanly to recipient MX",
		"occurred_at":       clk.Now().UTC(),
	})
	badWHReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/webhooks/email-events", strings.NewReader(string(evBody)))
	badWHReq.Header.Set("X-Email-Signature", "sha256=deadbeef")
	badWHResp, err := pubClient.Do(badWHReq)
	if err != nil || badWHResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad X-Email-Signature, got status=%v err=%v", badWHResp.StatusCode, err)
	}
	badWHResp.Body.Close()

	goodWHReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/webhooks/email-events", strings.NewReader(string(evBody)))
	goodWHReq.Header.Set("X-Email-Signature", signEmailWebhookBody(emailSecret, evBody))
	goodWHResp, err := pubClient.Do(goodWHReq)
	if err != nil || goodWHResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for valid email delivery webhook, got %v err=%v", goodWHResp.StatusCode, err)
	}
	goodWHResp.Body.Close()

	// Replay same provider_event_id is idempotent (200).
	dupWHReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/webhooks/email-events", strings.NewReader(string(evBody)))
	dupWHReq.Header.Set("X-Email-Signature", signEmailWebhookBody(emailSecret, evBody))
	dupWHResp, _ := pubClient.Do(dupWHReq)
	dupWHResp.Body.Close()
	if dupWHResp.StatusCode != http.StatusOK {
		t.Fatalf("expected idempotent 200 on duplicate email webhook, got %d", dupWHResp.StatusCode)
	}

	detailAfterWH, _ := svc.GetGuestDetail(ctx, ownerActor, prop.ID, carysID)
	if detailAfterWH.Communications[0].DeliveryStatus != domain.DeliveryDelivered {
		t.Fatalf("expected DELIVERED after delivery webhook, got %s", detailAfterWH.Communications[0].DeliveryStatus)
	}

	// Send another manual email and mark BOUNCED via webhook -> shows up in failed emails + Ops Centre -> retry succeeds.
	manualComm, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID,
		GuestID:    carysID,
		Subject:    "Site map attachment note",
		BodyText:   "Please see gate keypad code 4491.",
	})
	if err != nil {
		t.Fatalf("send manual email: %v", err)
	}
	_, _ = svc.DispatchPendingEmails(ctx)
	bounceBody, _ := json.Marshal(map[string]any{
		"provider":          "postmark",
		"provider_event_id": "pm-bounce-1",
		"event_type":        "BOUNCED",
		"communication_id":  manualComm.ID,
		"detail":            "550 Mailbox unavailable",
		"occurred_at":       clk.Now().UTC(),
	})
	bReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/webhooks/email-events", strings.NewReader(string(bounceBody)))
	bReq.Header.Set("X-Email-Signature", signEmailWebhookBody(emailSecret, bounceBody))
	bResp, _ := pubClient.Do(bReq)
	bResp.Body.Close()
	if bResp.StatusCode != http.StatusOK {
		t.Fatalf("bounce webhook failed: %d", bResp.StatusCode)
	}
	failedList, err := svc.ListFailedPropertyEmails(ctx, ownerActor, prop.ID)
	if err != nil || len(failedList) != 1 {
		t.Fatalf("expected 1 failed/bounced email for retry review, got len=%d err=%v", len(failedList), err)
	}
	if err := svc.RetryFailedOutboxEmail(ctx, ownerActor, prop.ID, failedList[0].OutboxID); err != nil {
		t.Fatalf("retry failed email: %v", err)
	}
	if _, err := svc.DispatchPendingEmails(ctx); err != nil {
		t.Fatalf("dispatch retried email: %v", err)
	}

	// 3. Guest Enquiry + same-property Maintenance Request link + thread workflow.
	mntReq, err := svc.CreateMaintenanceRequest(ctx, ownerActor, service.CreateMaintenanceRequestInput{
		PropertyID:  prop.ID,
		CategoryID:  cats[0].ID,
		UnitID:      &units[0].ID,
		Title:       "Tap dripping on pitch water post",
		Description: "Guest reported slight drip",
		Priority:    domain.MaintPriorityNormal,
		WorkType:    domain.MaintWorkReactive,
	})
	if err != nil {
		t.Fatalf("create maintenance request for enquiry link: %v", err)
	}
	enq, err := svc.CreateGuestEnquiry(ctx, ownerActor, domain.GuestEnquiry{
		PropertyID:           prop.ID,
		GuestID:              &carysID,
		Category:             domain.EnqCatComplaint,
		Priority:             domain.EnqPriorityHigh,
		Source:               domain.EnqSourcePhone,
		Subject:              "Water tap drip near pitch",
		Description:          "Guest flagged tap dripping when arriving",
		MaintenanceRequestID: &mntReq.Request.ID,
	})
	if err != nil || !strings.HasPrefix(enq.Reference, "ENQ-") {
		t.Fatalf("create enquiry failed: %+v err=%v", enq, err)
	}
	if _, err := svc.AddGuestEnquiryMessage(ctx, ownerActor, domain.GuestEnquiryMessage{
		PropertyID: prop.ID,
		EnquiryID:  enq.ID,
		Direction:  domain.CommDirInternal,
		Body:       "Passed to maintenance team",
		IsInternal: true,
	}); err != nil {
		t.Fatalf("add internal enquiry message: %v", err)
	}
	if _, err := svc.AddGuestEnquiryMessage(ctx, ownerActor, domain.GuestEnquiryMessage{
		PropertyID: prop.ID,
		EnquiryID:  enq.ID,
		Direction:  domain.CommDirOutbound,
		Body:       "Thanks Carys — our team will tighten the washer shortly.",
		IsInternal: false,
	}); err != nil {
		t.Fatalf("add outbound enquiry reply: %v", err)
	}

	// Verify Operations Centre exposes guest CRM alerts while respecting permissions.
	ops, err := svc.GetOperationsCentre(ctx, ownerActor, prop.ID, nil, "all")
	if err != nil || !ops.GuestCRM.Visible || ops.GuestCRM.UnresolvedComplaints == 0 {
		t.Fatalf("expected Ops Centre CRM alerts with open complaint, got %+v err=%v", ops.GuestCRM, err)
	}

	// Resolve and close enquiry.
	enq.Status = domain.EnqStatusResolved
	enq.ResolutionNotes = "Washer replaced and confirmed dry"
	enq, err = svc.UpdateGuestEnquiry(ctx, ownerActor, enq)
	if err != nil || enq.ResolvedAt == nil {
		t.Fatalf("resolve enquiry: %+v err=%v", enq, err)
	}
	enq.Status = domain.EnqStatusClosed
	if _, err := svc.UpdateGuestEnquiry(ctx, ownerActor, enq); err != nil {
		t.Fatalf("close enquiry: %v", err)
	}

	// 4. Timezone-aware Automation Rules & Jobs:
	// Scheduled pre-arrival rule fires on stay arriving in 2 days; advancing clock triggers exactly once;
	// amending date supersedes stale job; cancelling stay cancels scheduled jobs.
	jobsBefore, err := svc.ListCommunicationJobs(ctx, ownerActor, prop.ID)
	if err != nil || len(jobsBefore) == 0 {
		t.Fatalf("expected automation jobs scheduled for confirmed stay, got len=%d err=%v", len(jobsBefore), err)
	}
	// Advance clock to trigger pre-arrival (-24h / -48h before arrival).
	clk.Advance(4 * 24 * time.Hour)
	processed1, err := svc.SweepDueCommunicationJobs(ctx)
	if err != nil || processed1 == 0 {
		t.Fatalf("expected due automation jobs processed after advancing clock, processed=%d err=%v", processed1, err)
	}
	// Re-sweeping at identical time must send zero duplicates.
	processed2, err := svc.SweepDueCommunicationJobs(ctx)
	if err != nil || processed2 != 0 {
		t.Fatalf("expected idempotent sweep with 0 duplicates, got %d err=%v", processed2, err)
	}
	_, _ = svc.DispatchPendingEmails(ctx)

	// Create second confirmed stay 6 days from now, then amend & cancel -> verify job superseding/cancellation.
	stay2, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestID:       carysID,
		GuestName:     "Carys Pritchard",
		GuestEmail:    "carys@example.wales",
		ChannelCode:   "DIRECT",
		ArrivalDate:   clk.Now().AddDate(0, 0, 6),
		DepartureDate: clk.Now().AddDate(0, 0, 8),
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{units[1].ID},
	})
	if err != nil {
		t.Fatalf("create second stay for job lifecycle: %v", err)
	}
	stay2.ArrivalDate = clk.Now().AddDate(0, 0, 8)
	stay2.DepartureDate = clk.Now().AddDate(0, 0, 10)
	stay2, err = svc.UpdateReservation(ctx, ownerActor, stay2)
	if err != nil {
		t.Fatalf("amend stay2 dates: %v", err)
	}
	if _, err := svc.TransitionReservationStatus(ctx, ownerActor, prop.ID, stay2.ID, domain.ResStatusCancelled, "Guest cancelled", stay2.Version); err != nil {
		t.Fatalf("cancel stay2: %v", err)
	}
	jobsAfterCancel, _ := svc.ListCommunicationJobs(ctx, ownerActor, prop.ID)
	for _, j := range jobsAfterCancel {
		if j.ReservationID == stay2.ID && j.Status == "SCHEDULED" {
			t.Fatalf("cancelled stay2 must have zero active SCHEDULED automation jobs, found %+v", j)
		}
	}
}

func TestPhase3Milestone3GDPRConsentExportAnonymizeRBACIsolationAndWebUI(t *testing.T) {
	srv, store, svc, propA, _, _, clk := setupPhase2TestServer(t, "pub-p33-rbac")
	defer srv.Close()

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	// Provision multi-role users & isolated Property B.
	staffUser, _ := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{Email: "crmstaff@tirbach.wales", FullName: "Ceri Staff", Role: domain.RoleStaff, Password: "crm-staff-pass-2026"})
	viewerUser, _ := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{Email: "crmviewer@tirbach.wales", FullName: "Iwan Viewer", Role: domain.RoleViewer, Password: "crm-viewer-pass-2026"})
	hkUser, _ := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{Email: "crmhk@tirbach.wales", FullName: "Nia Cleaner", Role: domain.RoleHousekeepingStaff, Password: "crm-hk-pass-2026"})
	mntUser, _ := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{Email: "crmmnt@tirbach.wales", FullName: "Trystan Fixer", Role: domain.RoleMaintenanceStaff, Password: "crm-mnt-pass-2026"})

	propB, err := store.CreateProperty(ctx, nil, domain.Property{
		ID:           uuid.New(),
		Slug:         "cwm-du-glamping",
		Name:         "Cwm Du Glamping",
		CountryCode:  "GB",
		Timezone:     "Europe/London",
		Currency:     "GBP",
		CheckInTime:  "15:00",
		CheckOutTime: "10:00",
		ContactEmail: "hello@cwmdu.example",
	})
	if err != nil {
		t.Fatalf("create Property B: %v", err)
	}
	propBStaff, _ := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		PropertyID: &propB.ID,
		Email:      "b@cwmdu.example",
		FullName:   "B Manager",
		Role:       domain.RoleManager,
		Password:   "prop-b-manager-pass-2026",
	})
	_ = propBStaff

	unitsA, _ := svc.ListUnits(ctx, ownerActor, propA.ID, nil)
	catsB, _ := svc.ListMaintenanceCategories(ctx, ownerActor, propB.ID, false)

	// Create Guest A on Property A with upcoming stay -> consent default is WITHDRAWN/unset, anonymisation must block while upcoming!
	resA, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    propA.ID,
		GuestName:     "Eleri Morgan",
		GuestEmail:    "eleri.morgan@example.test",
		GuestPhone:    "+44 7700 900777",
		ChannelCode:   "DIRECT",
		ArrivalDate:   clk.Now().AddDate(0, 0, 12),
		DepartureDate: clk.Now().AddDate(0, 0, 14),
		Adults:        2,
		TotalPence:    24000,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unitsA[0].ID},
	})
	if err != nil {
		t.Fatalf("create resA: %v", err)
	}
	guestAID := resA.GuestID
	_, _ = svc.AddGuestNote(ctx, ownerActor, domain.GuestNote{PropertyID: propA.ID, GuestID: guestAID, Category: domain.NoteCatGeneral, Content: "Confidential staff note"})
	enqA, err := svc.CreateGuestEnquiry(ctx, ownerActor, domain.GuestEnquiry{PropertyID: propA.ID, GuestID: &guestAID, Category: domain.EnqCatGeneral, Priority: domain.EnqPriorityNormal, Source: domain.EnqSourcePhone, Subject: "Parking query"})
	if err != nil {
		t.Fatalf("create enquiry A: %v", err)
	}

	// Cross-property maintenance link must be rejected!
	var catBID uuid.UUID
	if len(catsB) > 0 {
		catBID = catsB[0].ID
	} else {
		catB, cErr := svc.CreateMaintenanceCategory(ctx, ownerActor, domain.MaintenanceCategory{PropertyID: propB.ID, Code: "PLUMB", Name: "Plumbing"})
		if cErr != nil {
			t.Fatalf("create propB maintenance category: %v", cErr)
		}
		catBID = catB.ID
	}
	locB, cLocErr := svc.CreateMaintenanceLocation(ctx, ownerActor, domain.MaintenanceLocation{
		PropertyID: propB.ID, Code: "FAC-B", Name: "Property B Shed", LocationType: "FACILITY", IsActive: true,
	})
	if cLocErr != nil {
		t.Fatalf("create propB maintenance location: %v", cLocErr)
	}
	mntB, err := svc.CreateMaintenanceRequest(ctx, ownerActor, service.CreateMaintenanceRequestInput{
		PropertyID: propB.ID, CategoryID: catBID, LocationID: &locB.ID, Title: "B fault", Priority: domain.MaintPriorityLow, WorkType: domain.MaintWorkReactive,
	})
	if err != nil {
		t.Fatalf("create propB maintenance request: %v", err)
	}
	if _, err := svc.CreateGuestEnquiry(ctx, ownerActor, domain.GuestEnquiry{
		PropertyID: propA.ID, GuestID: &guestAID, Category: domain.EnqCatFacilities, Priority: domain.EnqPriorityNormal, Source: domain.EnqSourcePhone, Subject: "Cross-prop link", MaintenanceRequestID: &mntB.Request.ID,
	}); err == nil {
		t.Fatalf("expected cross-property maintenance_request_id on enquiry to be rejected")
	}

	// Active future reservation blocks anonymisation (conflict).
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, propA.ID, guestAID, "Erasure request"); err == nil {
		t.Fatalf("expected 409 conflict anonymising guest with active upcoming stay")
	}

	// Consent & retention policy lifecycle.
	if _, err := svc.RecordGuestConsent(ctx, ownerActor, domain.GuestConsentRecord{
		PropertyID: propA.ID, GuestID: guestAID, ConsentType: "MARKETING_EMAIL", Status: domain.ConsentGranted, Method: "ADMIN_VERBAL", Source: "desk", PolicyVersion: "2026-v1",
	}); err != nil {
		t.Fatalf("grant consent: %v", err)
	}
	if _, err := svc.RecordGuestConsent(ctx, ownerActor, domain.GuestConsentRecord{
		PropertyID: propA.ID, GuestID: guestAID, ConsentType: "MARKETING_EMAIL", Status: domain.ConsentWithdrawn, Method: "GUEST_REQUEST", Source: "email", PolicyVersion: "2026-v1",
	}); err != nil {
		t.Fatalf("withdraw consent: %v", err)
	}
	pol, err := svc.UpdatePropertyRetentionPolicy(ctx, ownerActor, domain.PropertyRetentionPolicy{
		PropertyID: propA.ID, EnquiryRetentionDays: 365, InactiveGuestRetentionDays: 1095, CommsRetentionDays: 730, AutoAnonymizeEnabled: false,
	})
	if err != nil || pol.EnquiryRetentionDays != 365 {
		t.Fatalf("update retention policy: %+v err=%v", pol, err)
	}

	// Cancel upcoming stay so anonymisation succeeds while preserving reservation financial total.
	if _, err := svc.TransitionReservationStatus(ctx, ownerActor, propA.ID, resA.ID, domain.ResStatusCancelled, "Guest requested erasure", resA.Version); err != nil {
		t.Fatalf("cancel resA: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE reservations SET total_pence = 24000 WHERE id = $1`, resA.ID); err != nil {
		t.Fatalf("seed ledger amount on resA: %v", err)
	}
	anon, err := svc.AnonymizeGuest(ctx, ownerActor, propA.ID, guestAID, "UK GDPR Art 17 request")
	if err != nil {
		t.Fatalf("anonymize guest: %v", err)
	}
	if anon.Status != domain.GuestStatusAnonymized || anon.Email != "" || strings.Contains(anon.FullName, "Eleri") {
		t.Fatalf("expected scrubbed anonymized guest profile, got %+v", anon)
	}
	resAfterAnon, err := svc.GetReservation(ctx, ownerActor, propA.ID, resA.ID)
	if err != nil || resAfterAnon.TotalPence != 24000 || strings.Contains(resAfterAnon.GuestName, "Eleri") {
		t.Fatalf("expected anonymized reservation snapshot keeping 24000 financial amount, got %+v err=%v", resAfterAnon, err)
	}

	// RBAC HTTP checks across roles!
	hkClient, hkCSRF := loginAdminClient(t, srv.URL, hkUser.Email, "crm-hk-pass-2026")
	mntClient, mntCSRF := loginAdminClient(t, srv.URL, mntUser.Email, "crm-mnt-pass-2026")
	viewerClient, _ := loginAdminClient(t, srv.URL, viewerUser.Email, "crm-viewer-pass-2026")
	staffClient, staffCSRF := loginAdminClient(t, srv.URL, staffUser.Email, "crm-staff-pass-2026")
	bClient, _ := loginAdminClient(t, srv.URL, propBStaff.Email, "prop-b-manager-pass-2026")
	ownerClient, _ := loginAdminClient(t, srv.URL, "owner@tirbach.wales", "tirbach-owner-password-2026")
	_, _ = hkCSRF, mntCSRF

	// Housekeeping & maintenance staff have zero Guest CRM access + GuestCRM.Visible == false on Ops Centre.
	for _, pair := range []struct {
		name   string
		client *http.Client
	}{{"housekeeping_staff", hkClient}, {"maintenance_staff", mntClient}} {
		r := authedJSON(t, pair.client, http.MethodGet, fmt.Sprintf("%s/api/v1/properties/%s/guests", srv.URL, propA.ID), "", nil)
		r.Body.Close()
		if r.StatusCode != http.StatusForbidden {
			t.Fatalf("%s expected 403 on /guests, got %d", pair.name, r.StatusCode)
		}
		ops := fetchOpsJSON(t, pair.client, fmt.Sprintf("%s/api/v1/properties/%s/operations", srv.URL, propA.ID))
		if ops.GuestCRM.Visible {
			t.Fatalf("%s Ops Centre must hide GuestCRM card", pair.name)
		}
	}

	// Viewer gets 200 on guest detail with redacted notes & comms, and 403 on export / templates / mutations.
	vRes := authedJSON(t, viewerClient, http.MethodGet, fmt.Sprintf("%s/api/v1/properties/%s/guests/%s", srv.URL, propA.ID, guestAID), "", nil)
	var vDetail domain.GuestDetailBundle
	_ = json.NewDecoder(vRes.Body).Decode(&vDetail)
	vRes.Body.Close()
	if vRes.StatusCode != http.StatusOK || vDetail.ShowInternalNotes || vDetail.ShowCommunications || vDetail.ShowEnquiries || vDetail.ShowConsentHistory || len(vDetail.Enquiries) != 0 || len(vDetail.ConsentHistory) != 0 || len(vDetail.MergeHistory) != 0 {
		t.Fatalf("expected viewer detail 200 with notes/comms/enquiries/consent/merge history redacted, got status=%d detail=%+v", vRes.StatusCode, vDetail)
	}
	vExp := authedJSON(t, viewerClient, http.MethodGet, fmt.Sprintf("%s/api/v1/properties/%s/guests/%s/export", srv.URL, propA.ID, guestAID), "", nil)
	vExp.Body.Close()
	if vExp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected viewer 403 on /export, got %d", vExp.StatusCode)
	}

	// Staff cannot merge or anonymize (403).
	sMerge := authedJSON(t, staffClient, http.MethodPost, fmt.Sprintf("%s/api/v1/properties/%s/guests/merge", srv.URL, propA.ID), staffCSRF, mustJSON(t, map[string]any{"source_guest_id": guestAID, "target_guest_id": guestAID}))
	sMerge.Body.Close()
	if sMerge.StatusCode != http.StatusForbidden {
		t.Fatalf("expected staff 403 on guest merge, got %d", sMerge.StatusCode)
	}

	// Property B manager gets 403 reading Property A guest or enquiry.
	bGuest := authedJSON(t, bClient, http.MethodGet, fmt.Sprintf("%s/api/v1/properties/%s/guests/%s", srv.URL, propA.ID, guestAID), "", nil)
	bGuest.Body.Close()
	if bGuest.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 reading Property A guest as Property B manager, got %d", bGuest.StatusCode)
	}

	// Verify all LOVELACE WebUI CRM pages execute cleanly (HTTP 200) and comply with zero-inline-style CSP!
	for _, u := range []string{
		"/",
		"/guests",
		"/guests?segment=ALL",
		"/guests/merge",
		"/guests/" + guestAID.String(),
		"/enquiries",
		"/enquiries/" + enqA.ID.String(),
		"/communications/settings",
		"/reservations/" + resA.ID.String(),
	} {
		resp, err := ownerClient.Get(srv.URL + u)
		if err != nil {
			t.Fatalf("GET %s: %v", u, err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("WebUI %s expected 200, got %d body=%s", u, resp.StatusCode, string(raw))
		}
		if strings.Contains(string(raw), `style="`) {
			t.Fatalf("WebUI %s contains CSP-disallowed inline style attribute", u)
		}
	}
}
