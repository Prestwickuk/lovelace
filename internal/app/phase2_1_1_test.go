package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Prestwickuk/lovelace/internal/app"
	"github.com/Prestwickuk/lovelace/internal/config"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/mailer"
	"github.com/Prestwickuk/lovelace/internal/service"
	"github.com/Prestwickuk/lovelace/internal/store/postgres"
	"github.com/jackc/pgx/v5"
)

func TestPhase211StripeKeyPrefixValidationAndProductionStartup(t *testing.T) {
	cases := []struct {
		name      string
		prod      bool
		key       string
		wantValid bool
		wantLive  bool
		wantOK    bool
	}{
		{"prod sk_live", true, "sk_live_lovelace_prod_0123456789abcdef", true, true, true},
		{"prod rk_live", true, "rk_live_lovelace_prod_0123456789abcdef", true, true, true},
		{"prod sk_test rejected", true, "sk_test_lovelace_dev_0123456789abcdef", false, false, true},
		{"prod rk_test rejected", true, "rk_test_lovelace_dev_0123456789abcdef", false, false, true},
		{"dev sk_test accepted", false, "sk_test_lovelace_dev_0123456789abcdef", true, false, true},
		{"dev rk_test accepted", false, "rk_test_lovelace_dev_0123456789abcdef", true, false, true},
		{"dev sk_live accepted", false, "sk_live_lovelace_prod_0123456789abcdef", true, true, true},
		{"dev rk_live accepted", false, "rk_live_lovelace_prod_0123456789abcdef", true, true, true},
		{"placeholder rejected", true, "sk_live_or_test_replace_me", false, false, false},
		{"replace_me rejected", true, "sk_live_replace_me_12345678", false, false, false},
		{"publishable key rejected", true, "pk_live_lovelace_0123456789abcdef", false, false, false},
		{"short key rejected", true, "sk_live_short", false, false, false},
		{"malformed chars rejected", true, "sk_live_bad!@#key_123456", false, false, false},
	}
	for _, tc := range cases {
		live, ok := config.StripeKeyIsLive(tc.key)
		if ok != tc.wantOK || (ok && live != tc.wantLive) {
			t.Fatalf("%s: StripeKeyIsLive=%v,%v want %v,%v", tc.name, live, ok, tc.wantLive, tc.wantOK)
		}
		if got := config.IsValidStripeSecretKey(tc.key, tc.prod); got != tc.wantValid {
			t.Fatalf("%s: IsValidStripeSecretKey(prod=%v)=%v want %v", tc.name, tc.prod, got, tc.wantValid)
		}
	}

	// Production ValidateStripeKeys + ValidateProductionStartup must fail closed on sk_test_ and rk_test_.
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		return
	}
	srv, store, _, prop, _, _, _ := setupPhase2TestServer(t, "pub-p211-keys")
	defer srv.Close()

	// Ensure stripe_enabled=true on seeded property.
	pol, err := store.GetBookingPolicy(context.Background(), nil, prop.ID)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	if !pol.StripeEnabled {
		pol.StripeEnabled = true
		_ = store.WithTx(context.Background(), func(tx pgx.Tx) error {
			_, err = store.UpdateBookingPolicy(context.Background(), tx, pol)
			return err
		})
	}

	baseProdCfg := config.Config{
		AppEnv:              "production",
		DatabaseURL:         dbURL,
		CookieSecure:        true,
		EncryptionKey:       testEncryptionKey,
		PublicOrigin:        "https://book.example.com",
		PublicAPISecret:     "prod-strong-secret-0123456789abcdef",
		StripeWebhookSecret: "whsec_live_valid_0123456789abcdef",
		SMTPAddr:            "smtp.example.com:587",
		EmailFrom:           "bookings@example.com",
	}

	for _, testKey := range []string{"sk_test_lovelace_dev_0123456789", "rk_test_lovelace_dev_0123456789"} {
		badCfg := baseProdCfg
		badCfg.StripeSecretKey = testKey
		if err := badCfg.ValidateStripeKeys(); err == nil {
			t.Fatalf("expected production ValidateStripeKeys to reject %s", testKey)
		}
		badSvc := service.New(store, badCfg)
		if err := badSvc.ValidateProductionStartup(context.Background()); err == nil {
			t.Fatalf("expected production startup to fail closed for %s when stripe_enabled=true", testKey)
		}
	}

	for _, liveKey := range []string{"sk_live_lovelace_prod_0123456789", "rk_live_lovelace_prod_0123456789"} {
		okCfg := baseProdCfg
		okCfg.StripeSecretKey = liveKey
		okSvc := service.New(store, okCfg)
		if err := okSvc.ValidateProductionStartup(context.Background()); err != nil {
			t.Fatalf("expected production startup to accept %s, got err=%v", liveKey, err)
		}
	}
}

func TestPhase211StripeWebhookLiveModeAndOutOfOrder(t *testing.T) {
	srv, store, _, prop, stripeMock, mailMock, clk := setupPhase2TestServer(t, "pub-p211-wh")
	pubClient := newClient(t)
	dbURL := os.Getenv("TEST_DATABASE_URL")

	// Build a separate production router sharing the test DB, configured with sk_live_ & live webhook secret.
	prodSecret := "whsec_live_lovelace_p211_secret"
	prodCfg := config.Config{
		AppEnv:              "production",
		DatabaseURL:         dbURL,
		CookieSecure:        true,
		EncryptionKey:       testEncryptionKey,
		PublicOrigin:        "https://book.example.com",
		PublicAPISecret:     "prod-p211-bff-secret",
		StripeSecretKey:     "sk_live_lovelace_prod_0123456789abcdef",
		StripeWebhookSecret: prodSecret,
		SMTPAddr:            "127.0.0.1:2525",
		EmailFrom:           "bookings@example.com",
	}
	prodSvc := service.New(store, prodCfg)
	prodSvc.SetPaymentProvider(stripeMock)
	prodSvc.SetEmailSender(mailMock)
	prodSvc.SetClock(clk.Now)
	prodRouter, err := app.NewRouter(prodCfg, store, prodSvc)
	if err != nil {
		t.Fatalf("prod router: %v", err)
	}
	prodSrv := httptest.NewServer(prodRouter)
	defer prodSrv.Close()

	// Build a development/test router configured explicitly with sk_test_
	devSecret := "whsec_test_lovelace_p211_secret"
	devCfg := config.Config{
		AppEnv:              "development",
		DatabaseURL:         dbURL,
		EncryptionKey:       testEncryptionKey,
		PublicOrigin:        "http://127.0.0.1:3000",
		PublicAPISecret:     "dev-p211-bff-secret",
		StripeSecretKey:     "sk_test_lovelace_dev_0123456789abcdef",
		StripeWebhookSecret: devSecret,
		EmailFrom:           "bookings@example.com",
	}
	devSvc := service.New(store, devCfg)
	devSvc.SetPaymentProvider(stripeMock)
	devSvc.SetEmailSender(mailMock)
	devSvc.SetClock(clk.Now)
	devRouter, err := app.NewRouter(devCfg, store, devSvc)
	if err != nil {
		t.Fatalf("dev router: %v", err)
	}
	devSrv := httptest.NewServer(devRouter)
	defer devSrv.Close()

	// Helper to create hold + checkout on devSrv.
	arr1 := clk.Now().AddDate(0, 0, 60).Format("2006-01-02")
	dep1 := clk.Now().AddDate(0, 0, 62).Format("2006-01-02")
	hold1 := createTestHoldViaAPI(t, srv, pubClient, "pub-p211-wh", arr1, dep1)
	co1 := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/checkout", "pub-p211-wh", "", map[string]any{
		"hold_id":         hold1.Hold.ID.String(),
		"idempotency_key": "p211-co-1",
	})
	var coOut1 service.CreateCheckoutResponse
	_ = json.NewDecoder(co1.Body).Decode(&coOut1)
	co1.Body.Close()

	// 1. Test-mode event (livemode=false) hitting PRODUCTION endpoint with valid live signature MUST BE REJECTED!
	testEvtInProd := map[string]any{
		"id":       "evt_test_in_prod_1",
		"type":     "checkout.session.completed",
		"livemode": false,
		"data": map[string]any{
			"object": map[string]any{
				"id":             coOut1.Attempt.ProviderSessionID,
				"payment_intent": "pi_fake_test_in_prod",
				"amount_total":   coOut1.Attempt.AmountPence,
				"currency":       "gbp",
				"payment_status": "paid",
				"metadata":       map[string]any{"payment_attempt_id": coOut1.PaymentAttemptID.String()},
			},
		},
	}
	st1, body1 := signAndSendStripeWebhook(t, pubClient, prodSrv.URL, prodSecret, clk.Now(), testEvtInProd)
	if st1 != http.StatusOK || body1["rejected"] != true || body1["duplicate"] != false {
		t.Fatalf("expected rejected=true duplicate=false for livemode=false in prod, got status=%d body=%+v", st1, body1)
	}

	// Verify hold is STILL ACTIVE (no reservation confirmed, attempt NOT succeeded) and webhook/audit rows recorded!
	var resCount int
	if err := store.Pool().QueryRow(context.Background(), `SELECT COUNT(*) FROM reservations WHERE property_id = $1`, prop.ID).Scan(&resCount); err != nil || resCount != 0 {
		t.Fatalf("expected 0 reservations after test event in prod, got %d (err=%v)", resCount, err)
	}
	var whStatus string
	var whLive *bool
	if err := store.Pool().QueryRow(context.Background(), `SELECT status, livemode FROM payment_webhook_events WHERE event_id = 'evt_test_in_prod_1'`).Scan(&whStatus, &whLive); err != nil || whStatus != "REJECTED" || whLive == nil || *whLive {
		t.Fatalf("expected REJECTED webhook row with livemode=false, got status=%q live=%v err=%v", whStatus, whLive, err)
	}
	var auditCount int
	if err := store.Pool().QueryRow(context.Background(), `SELECT COUNT(*) FROM audit_logs WHERE action = 'payment.webhook_livemode_mismatch' AND resource_id = 'evt_test_in_prod_1'`).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("expected 1 audit anomaly row, got %d err=%v", auditCount, err)
	}

	// Replaying the same mismatched event ID must NOT reprocess or duplicate anomaly logs!
	st1Dup, body1Dup := signAndSendStripeWebhook(t, pubClient, prodSrv.URL, prodSecret, clk.Now(), testEvtInProd)
	if st1Dup != http.StatusOK || body1Dup["duplicate"] != true {
		t.Fatalf("expected idempotent duplicate=true on mismatched event replay, got %d %+v", st1Dup, body1Dup)
	}
	var auditCountAfter int
	_ = store.Pool().QueryRow(context.Background(), `SELECT COUNT(*) FROM audit_logs WHERE action = 'payment.webhook_livemode_mismatch' AND resource_id = 'evt_test_in_prod_1'`).Scan(&auditCountAfter)
	if auditCountAfter != 1 {
		t.Fatalf("expected duplicate mismatched event not to reprocess anomaly log, got count=%d", auditCountAfter)
	}

	// 2. Live-mode event (livemode=true) hitting DEVELOPMENT (sk_test_) endpoint MUST ALSO BE REJECTED!
	liveEvtInDev := map[string]any{
		"id":       "evt_live_in_dev_1",
		"type":     "checkout.session.completed",
		"livemode": true,
		"data": map[string]any{
			"object": map[string]any{
				"id":             coOut1.Attempt.ProviderSessionID,
				"payment_intent": "pi_live_in_dev",
				"amount_total":   coOut1.Attempt.AmountPence,
				"currency":       "gbp",
				"payment_status": "paid",
				"metadata":       map[string]any{"payment_attempt_id": coOut1.PaymentAttemptID.String()},
			},
		},
	}
	st2, body2 := signAndSendStripeWebhook(t, pubClient, devSrv.URL, devSecret, clk.Now(), liveEvtInDev)
	if st2 != http.StatusOK || body2["rejected"] != true {
		t.Fatalf("expected live event rejected in dev sk_test_ mode, got %d %+v", st2, body2)
	}
	_ = store.Pool().QueryRow(context.Background(), `SELECT COUNT(*) FROM reservations WHERE property_id = $1`, prop.ID).Scan(&resCount)
	if resCount != 0 {
		t.Fatalf("expected 0 reservations after live event in dev, got %d", resCount)
	}

	// 3. Invalid signature rejected with 401 before DB recording
	badSigReq, _ := http.NewRequest(http.MethodPost, prodSrv.URL+"/api/public/v1/webhooks/stripe", strings.NewReader(`{"id":"evt_bad_sig","type":"checkout.session.completed","livemode":true}`))
	badSigReq.Header.Set("Stripe-Signature", "t=123,v1=deadbeef")
	badSigResp, _ := pubClient.Do(badSigReq)
	badSigResp.Body.Close()
	if badSigResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on invalid webhook signature, got %d", badSigResp.StatusCode)
	}

	// 4. Valid live event (livemode=true) in PRODUCTION confirms reservation cleanly!
	liveEvtInProd := map[string]any{
		"id":       "evt_live_in_prod_ok_1",
		"type":     "checkout.session.completed",
		"livemode": true,
		"data": map[string]any{
			"object": map[string]any{
				"id":             coOut1.Attempt.ProviderSessionID,
				"payment_intent": "pi_live_ok_1",
				"amount_total":   coOut1.Attempt.AmountPence,
				"currency":       "gbp",
				"payment_status": "paid",
				"metadata":       map[string]any{"payment_attempt_id": coOut1.PaymentAttemptID.String()},
			},
		},
	}
	st3, body3 := signAndSendStripeWebhook(t, pubClient, prodSrv.URL, prodSecret, clk.Now(), liveEvtInProd)
	if st3 != http.StatusOK || body3["duplicate"] != false || body3["rejected"] != nil {
		t.Fatalf("expected valid live event in prod to succeed, got %d %+v", st3, body3)
	}
	_ = store.Pool().QueryRow(context.Background(), `SELECT COUNT(*) FROM reservations WHERE property_id = $1 AND status = 'CONFIRMED' AND payment_status IN ('PAID', 'PARTIALLY_PAID')`, prop.ID).Scan(&resCount)
	if resCount != 1 {
		var dbgStatus, dbgErr string
		var dbgPaid, dbgPayCount int
		_ = store.Pool().QueryRow(context.Background(), `SELECT status, error_message FROM payment_webhook_events WHERE event_id = 'evt_live_in_prod_ok_1'`).Scan(&dbgStatus, &dbgErr)
		_ = store.Pool().QueryRow(context.Background(), `SELECT COUNT(*) FROM reservations WHERE property_id = $1`, prop.ID).Scan(&dbgPaid)
		_ = store.Pool().QueryRow(context.Background(), `SELECT COUNT(*) FROM payment_exceptions WHERE property_id = $1`, prop.ID).Scan(&dbgPayCount)
		t.Fatalf("expected 1 CONFIRMED reservation after valid live webhook, got %d (co1=%d whStatus=%q whErr=%q allRes=%d exceptions=%d)", resCount, co1.StatusCode, dbgStatus, dbgErr, dbgPaid, dbgPayCount)
	}

	// Duplicate valid event is idempotent
	st3Dup, body3Dup := signAndSendStripeWebhook(t, pubClient, prodSrv.URL, prodSecret, clk.Now(), liveEvtInProd)
	if st3Dup != http.StatusOK || body3Dup["duplicate"] != true {
		t.Fatalf("expected duplicate=true on valid event replay, got %d %+v", st3Dup, body3Dup)
	}

	// 5. Out-of-order late failure/expired event after confirmation must NOT revert reservation or SUCCEEDED attempt!
	outOfOrderFail := map[string]any{
		"id":       "evt_late_fail_after_ok_1",
		"type":     "payment_intent.payment_failed",
		"livemode": true,
		"data": map[string]any{
			"object": map[string]any{
				"id":             coOut1.Attempt.ProviderSessionID,
				"payment_intent": "pi_live_ok_1",
				"metadata":       map[string]any{"payment_attempt_id": coOut1.PaymentAttemptID.String()},
			},
		},
	}
	st4, _ := signAndSendStripeWebhook(t, pubClient, prodSrv.URL, prodSecret, clk.Now(), outOfOrderFail)
	if st4 != http.StatusOK {
		t.Fatalf("expected 200 acknowledging out-of-order failure event safely, got %d", st4)
	}
	var attStatus string
	_ = store.Pool().QueryRow(context.Background(), `SELECT status FROM payment_attempts WHERE id = $1`, coOut1.PaymentAttemptID).Scan(&attStatus)
	if attStatus != domain.AttemptStatusSucceeded {
		t.Fatalf("out-of-order payment_failed corrupted SUCCEEDED attempt status to %s", attStatus)
	}

	// 6. Valid test event (livemode=false) in DEVELOPMENT confirms a second hold cleanly!
	arr2 := clk.Now().AddDate(0, 0, 70).Format("2006-01-02")
	dep2 := clk.Now().AddDate(0, 0, 72).Format("2006-01-02")
	hold2 := createTestHoldViaAPI(t, srv, pubClient, "pub-p211-wh", arr2, dep2)
	co2 := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/checkout", "pub-p211-wh", "", map[string]any{
		"hold_id":         hold2.Hold.ID.String(),
		"idempotency_key": "p211-co-2",
	})
	var coOut2 service.CreateCheckoutResponse
	_ = json.NewDecoder(co2.Body).Decode(&coOut2)
	co2.Body.Close()

	testEvtInDev := map[string]any{
		"id":       "evt_test_in_dev_ok_1",
		"type":     "checkout.session.completed",
		"livemode": false,
		"data": map[string]any{
			"object": map[string]any{
				"id":             coOut2.Attempt.ProviderSessionID,
				"payment_intent": "pi_dev_ok_2",
				"amount_total":   coOut2.Attempt.AmountPence,
				"currency":       "gbp",
				"payment_status": "paid",
				"metadata":       map[string]any{"payment_attempt_id": coOut2.PaymentAttemptID.String()},
			},
		},
	}
	st5, body5 := signAndSendStripeWebhook(t, pubClient, devSrv.URL, devSecret, clk.Now(), testEvtInDev)
	if st5 != http.StatusOK || body5["duplicate"] != false || body5["rejected"] != nil {
		t.Fatalf("expected valid test event in dev to succeed, got %d %+v", st5, body5)
	}
	var att2Status string
	_ = store.Pool().QueryRow(context.Background(), `SELECT status FROM payment_attempts WHERE id = $1`, coOut2.PaymentAttemptID).Scan(&att2Status)
	if att2Status != domain.AttemptStatusSucceeded {
		t.Fatalf("expected dev attempt SUCCEEDED after valid test webhook, got %s", att2Status)
	}
}

func TestPhase211MandatoryEmailReadinessForPublicBookings(t *testing.T) {
	srv, store, _, prop, stripeMock, _, clk := setupPhase2TestServer(t, "pub-p211-email")
	defer srv.Close()
	dbURL := os.Getenv("TEST_DATABASE_URL")

	pol, err := store.GetBookingPolicy(context.Background(), nil, prop.ID)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}

	// Case 1: No email provider + direct bookings/payments DISABLED (stripe_enabled=false, RequireEmail=false) -> ALLOWED in prod!
	pol.StripeEnabled = false
	if err := store.WithTx(context.Background(), func(tx pgx.Tx) error {
		var uErr error
		pol, uErr = store.UpdateBookingPolicy(context.Background(), tx, pol)
		return uErr
	}); err != nil {
		t.Fatalf("disable stripe policy: %v", err)
	}
	noMailCfg := config.Config{
		AppEnv:          "production",
		DatabaseURL:     dbURL,
		CookieSecure:    true,
		EncryptionKey:   testEncryptionKey,
		PublicOrigin:    "https://book.example.com",
		PublicAPISecret: "prod-p211-mail-secret",
		RequireEmail:    false,
	}
	svcAdminOnly := service.New(store, noMailCfg)
	if err := svcAdminOnly.ValidateProductionStartup(context.Background()); err != nil {
		t.Fatalf("expected admin-only / payments-disabled startup without SMTP to pass: %v", err)
	}
	if rep := svcAdminOnly.Readiness(context.Background()); !rep.Ready || rep.Email.Status != "unconfigured" || rep.Payments.Status != "disabled" {
		t.Fatalf("unexpected readiness for admin-only no-SMTP: %+v", rep)
	}

	// Case 2: No email provider + direct payments ENABLED (stripe_enabled=true), even with RequireEmail=false -> MUST FAIL!
	pol.StripeEnabled = true
	if err := store.WithTx(context.Background(), func(tx pgx.Tx) error {
		var uErr error
		pol, uErr = store.UpdateBookingPolicy(context.Background(), tx, pol)
		return uErr
	}); err != nil {
		t.Fatalf("enable stripe policy: %v", err)
	}
	noMailLivePayCfg := config.Config{
		AppEnv:              "production",
		DatabaseURL:         dbURL,
		CookieSecure:        true,
		EncryptionKey:       testEncryptionKey,
		PublicOrigin:        "https://book.example.com",
		PublicAPISecret:     "prod-p211-mail-secret",
		StripeSecretKey:     "sk_live_lovelace_prod_0123456789abcdef",
		StripeWebhookSecret: "whsec_live_lovelace_p211_mail",
		RequireEmail:        false, // Still must fail because stripe_enabled=true!
	}
	svcMissingEmailForLive := service.New(store, noMailLivePayCfg)
	svcMissingEmailForLive.SetPaymentProvider(stripeMock)
	if err := svcMissingEmailForLive.ValidateProductionStartup(context.Background()); err == nil {
		t.Fatal("expected production startup to FAIL when stripe_enabled=true and no email provider configured")
	}
	if rep := svcMissingEmailForLive.Readiness(context.Background()); rep.Ready || rep.Email.Status != "misconfigured" {
		t.Fatalf("expected Ready=false and email.status=misconfigured when stripe_enabled=true without SMTP, got %+v", rep)
	}

	// Also verify runtime checkout refusal when email provider is unconfigured in production!
	pubClient := newClient(t)
	arr := clk.Now().AddDate(0, 0, 80).Format("2006-01-02")
	dep := clk.Now().AddDate(0, 0, 82).Format("2006-01-02")
	hold := createTestHoldViaAPI(t, srv, pubClient, "pub-p211-email", arr, dep)
	if _, err := svcMissingEmailForLive.InitiatePublicCheckout(context.Background(), service.Actor{}, service.CreateCheckoutRequest{
		HoldID: hold.Hold.ID,
	}); !errors.Is(err, domain.ErrStripeDisabled) {
		t.Fatalf("expected runtime production checkout refusal when SMTP missing, got %v", err)
	}

	// Case 3: Genuine SMTP provider configured -> startup & readiness PASS!
	withMailCfg := noMailLivePayCfg
	withMailCfg.SMTPAddr = "smtp.lovelace.example:587"
	withMailCfg.EmailFrom = "bookings@example.com"
	svcReady := service.New(store, withMailCfg)
	if err := svcReady.ValidateProductionStartup(context.Background()); err != nil {
		t.Fatalf("expected startup with SMTP + live Stripe configured to succeed, got %v", err)
	}
	if rep := svcReady.Readiness(context.Background()); !rep.Ready || rep.Email.Status != "configured" || rep.Payments.Status != "ok" {
		t.Fatalf("unexpected readiness when SMTP + live Stripe configured: %+v", rep)
	}

	// Case 4 & 5: Email provider unavailable (transient dial/network error) keeps outbox PENDING (never SENT) and recovers on retry!
	_, err = store.QueueEmail(context.Background(), nil, domain.EmailOutboxItem{
		PropertyID:     prop.ID,
		RecipientEmail: "carys.outbox@example.com",
		Template:       "booking_confirmation",
		Subject:        "Outbox Test",
		BodyText:       "Body",
		NextAttemptAt:  clk.Now(),
	})
	if err != nil {
		t.Fatalf("queue outbox item: %v", err)
	}

	transientSender := &configurableMailSender{errToRet: errors.New("dial tcp 127.0.0.1:2525: connect: connection refused")}
	svcReady.SetClock(clk.Now)
	svcReady.SetEmailSender(transientSender)
	if sent, dErr := svcReady.DispatchPendingEmails(context.Background()); dErr != nil || sent != 0 {
		t.Fatalf("expected 0 sent on transient SMTP outage, got sent=%d err=%v", sent, dErr)
	}

	var status string
	var attempts int
	_ = store.Pool().QueryRow(context.Background(), `SELECT status, attempts FROM email_outbox WHERE recipient_email = 'carys.outbox@example.com'`).Scan(&status, &attempts)
	if status != "PENDING" || attempts != 1 {
		t.Fatalf("expected PENDING with attempts=1 after transient outage, got status=%q attempts=%d", status, attempts)
	}

	// Provider recovers after backoff -> delivers cleanly and marks SENT!
	clk.Advance(10 * time.Minute)
	transientSender.errToRet = nil
	if sent, dErr := svcReady.DispatchPendingEmails(context.Background()); dErr != nil || sent != 1 || len(transientSender.sent) != 1 {
		t.Fatalf("expected 1 sent after SMTP recovery, got sent=%d len=%d err=%v", sent, len(transientSender.sent), dErr)
	}
	_ = store.Pool().QueryRow(context.Background(), `SELECT status FROM email_outbox WHERE recipient_email = 'carys.outbox@example.com'`).Scan(&status)
	if status != "SENT" {
		t.Fatalf("expected SENT status after genuine provider acceptance, got %q", status)
	}
}

func TestPhase211CanonicalDomainNoSilentFallbackInProduction(t *testing.T) {
	srv, store, _, prop, stripeMock, _, clk := setupPhase2TestServer(t, "pub-p211-dom")
	defer srv.Close()
	pubClient := newClient(t)
	dbURL := os.Getenv("TEST_DATABASE_URL")

	// Simulate custom_domain set in DB while PublicOrigin is intentionally empty in production.
	if _, err := store.Pool().Exec(context.Background(), `UPDATE properties SET custom_domain = 'legacy-alt.example.com' WHERE id = $1`, prop.ID); err != nil {
		t.Fatalf("set custom_domain: %v", err)
	}

	arr := clk.Now().AddDate(0, 0, 90).Format("2006-01-02")
	dep := clk.Now().AddDate(0, 0, 92).Format("2006-01-02")
	hold := createTestHoldViaAPI(t, srv, pubClient, "pub-p211-dom", arr, dep)

	// Missing PublicOrigin in production must FAIL rather than silently substituting custom_domain!
	missingOriginSvc := service.New(store, config.Config{
		AppEnv:              "production",
		DatabaseURL:         dbURL,
		CookieSecure:        true,
		EncryptionKey:       testEncryptionKey,
		PublicAPISecret:     "prod-p211-dom-secret",
		StripeSecretKey:     "sk_live_lovelace_prod_0123456789abcdef",
		StripeWebhookSecret: "whsec_live_lovelace_p211_dom",
		SMTPAddr:            "smtp.lovelace.example:587",
	})
	missingOriginSvc.SetPaymentProvider(stripeMock)
	missingOriginSvc.SetEmailSender(&fakeMailSender{})
	if _, err := missingOriginSvc.InitiatePublicCheckout(context.Background(), service.Actor{}, service.CreateCheckoutRequest{
		HoldID: hold.Hold.ID,
	}); err == nil {
		t.Fatal("expected InitiatePublicCheckout to fail when production PublicOrigin is omitted (must not silently substitute domain)")
	}

	// Explicit canonical https://book.example.com succeeds and derives approved return URLs.
	rec := &failingStripeProvider{}
	okSvc := service.New(store, config.Config{
		AppEnv:              "production",
		DatabaseURL:         dbURL,
		CookieSecure:        true,
		EncryptionKey:       testEncryptionKey,
		PublicOrigin:        "https://book.example.com",
		PublicAPISecret:     "prod-p211-dom-secret",
		StripeSecretKey:     "sk_live_lovelace_prod_0123456789abcdef",
		StripeWebhookSecret: "whsec_live_lovelace_p211_dom",
		SMTPAddr:            "smtp.lovelace.example:587",
	})
	okSvc.SetPaymentProvider(rec)
	okSvc.SetEmailSender(&fakeMailSender{})
	if _, err := okSvc.InitiatePublicCheckout(context.Background(), service.Actor{}, service.CreateCheckoutRequest{
		HoldID: hold.Hold.ID,
	}); err != nil {
		t.Fatalf("expected checkout with explicit https://book.example.com to succeed, got %v", err)
	}
	if len(rec.requests) != 1 ||
		!strings.HasPrefix(rec.requests[0].SuccessURL, "https://book.example.com/booking/confirmation?") ||
		!strings.HasPrefix(rec.requests[0].CancelURL, "https://book.example.com/book?") {
		t.Fatalf("expected return URLs under canonical https://book.example.com, got %+v", rec.requests)
	}
	_ = mailer.ErrProviderUnconfigured
}

func withSearchPathDSN(t *testing.T, baseDSN, searchPath string) string {
	t.Helper()
	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse db dsn: %v", err)
	}
	q := u.Query()
	q.Set("options", "-c search_path="+searchPath)
	u.RawQuery = q.Encode()
	return u.String()
}

func fetchReadyz(t *testing.T, srvURL string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(srvURL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /readyz: %v", err)
	}
	return resp.StatusCode, body
}

func TestPhase211ReadinessQueryPermissionFailuresFailClosed(t *testing.T) {
	srv, store, _, prop, _, _, _ := setupPhase2TestServer(t, "pub-p211-readyz")
	defer srv.Close()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	ctx := context.Background()

	// Disable stripe at property level so a failing policy query would otherwise falsely appear "disabled".
	pol, err := store.GetBookingPolicy(ctx, nil, prop.ID)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	pol.StripeEnabled = false
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		var uErr error
		pol, uErr = store.UpdateBookingPolicy(ctx, tx, pol)
		return uErr
	}); err != nil {
		t.Fatalf("disable stripe policy: %v", err)
	}

	// Provision isolated schemas in the test DB whose views trigger PostgreSQL SQLSTATE 42501
	// ("permission denied") on the target tables while DB Ping() stays completely reachable.
	if _, err := store.Pool().Exec(ctx, `
		CREATE SCHEMA IF NOT EXISTS readiness_fault_policy;
		CREATE OR REPLACE FUNCTION readiness_fault_policy.deny_access() RETURNS boolean
		LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'permission denied for relation during readiness check' USING ERRCODE = '42501';
		END $$;
		CREATE OR REPLACE VIEW readiness_fault_policy.property_booking_policies AS
			SELECT NULL::uuid AS property_id, readiness_fault_policy.deny_access() AS stripe_enabled;

		CREATE SCHEMA IF NOT EXISTS readiness_fault_outbox;
		CREATE OR REPLACE VIEW readiness_fault_outbox.email_outbox AS
			SELECT 'PENDING'::text AS status WHERE readiness_fault_policy.deny_access();
	`); err != nil {
		t.Fatalf("setup fault schemas: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.Pool().Exec(context.Background(), `
			DROP SCHEMA IF EXISTS readiness_fault_outbox CASCADE;
			DROP SCHEMA IF EXISTS readiness_fault_policy CASCADE;
		`)
	})

	baseProdCfg := config.Config{
		AppEnv:          "production",
		DatabaseURL:     dbURL,
		CookieSecure:    true,
		EncryptionKey:   testEncryptionKey,
		PublicOrigin:    "https://book.example.com",
		PublicAPISecret: "prod-readyz-secret-0123456789",
	}

	// 0. Baseline healthy check: all queries succeed -> Ready=true, /readyz HTTP 200.
	healthySvc := service.New(store, baseProdCfg)
	healthyRouter, err := app.NewRouter(baseProdCfg, store, healthySvc)
	if err != nil {
		t.Fatalf("healthy router: %v", err)
	}
	healthySrv := httptest.NewServer(healthyRouter)
	defer healthySrv.Close()
	code0, body0 := fetchReadyz(t, healthySrv.URL)
	if code0 != http.StatusOK || body0["ready"] != true {
		t.Fatalf("expected healthy baseline 200 ready=true, got %d %+v", code0, body0)
	}
	payments0 := body0["payments"].(map[string]any)
	if payments0["status"] != "disabled" {
		t.Fatalf("expected payments disabled in baseline when stripe_enabled=false, got %+v", payments0)
	}

	// 1. Simulate CountActiveStripeEnabledProperties() permission failure while DB Ping() succeeds.
	policyFaultStore, err := postgres.New(ctx, withSearchPathDSN(t, dbURL, "readiness_fault_policy,public"))
	if err != nil {
		t.Fatalf("open policy fault store: %v", err)
	}
	defer policyFaultStore.Close()

	faultPolicySvc := service.New(policyFaultStore, baseProdCfg)
	repPolicy := faultPolicySvc.Readiness(ctx)
	if repPolicy.Ready || repPolicy.Database.Status != "ok" || repPolicy.Payments.Status != "unknown" || repPolicy.Email.Status != "unknown" {
		t.Fatalf("expected Ready=false, db=ok, payments=unknown (never disabled), email=unknown; got %+v", repPolicy)
	}

	faultPolicyRouter, err := app.NewRouter(baseProdCfg, policyFaultStore, faultPolicySvc)
	if err != nil {
		t.Fatalf("fault policy router: %v", err)
	}
	faultPolicySrv := httptest.NewServer(faultPolicyRouter)
	defer faultPolicySrv.Close()
	code1, body1 := fetchReadyz(t, faultPolicySrv.URL)
	if code1 != http.StatusServiceUnavailable || body1["ready"] != false || body1["status"] != "not_ready" {
		t.Fatalf("expected 503 not_ready on policy query error, got %d %+v", code1, body1)
	}
	pay1 := body1["payments"].(map[string]any)
	if pay1["status"] != "unknown" || pay1["status"] == "disabled" {
		t.Fatalf("payments must report unknown (never disabled) on policy query error: %+v", pay1)
	}

	// Non-production also must not report payments as disabled when policy query fails.
	devPolicySvc := service.New(policyFaultStore, config.Config{AppEnv: "development", DatabaseURL: dbURL, PublicOrigin: "http://127.0.0.1:3000"})
	if devRep := devPolicySvc.Readiness(ctx); devRep.Database.Status != "ok" || devRep.Payments.Status != "unknown" {
		t.Fatalf("expected dev database=ok payments=unknown when policy query fails, got %+v", devRep)
	}

	// 2. Simulate GetOutboxSummary() permission failure while DB Ping() & policy queries succeed.
	outboxFaultStore, err := postgres.New(ctx, withSearchPathDSN(t, dbURL, "readiness_fault_outbox,public"))
	if err != nil {
		t.Fatalf("open outbox fault store: %v", err)
	}
	defer outboxFaultStore.Close()

	outboxProdCfg := baseProdCfg
	outboxProdCfg.SMTPAddr = "smtp.lovelace.example:587"
	faultOutboxSvc := service.New(outboxFaultStore, outboxProdCfg)
	repOutbox := faultOutboxSvc.Readiness(ctx)
	if repOutbox.Ready || repOutbox.Database.Status != "ok" || repOutbox.Payments.Status != "disabled" || repOutbox.Email.Status != "unhealthy" {
		t.Fatalf("expected Ready=false, db=ok, payments=disabled, email=unhealthy on outbox failure; got %+v", repOutbox)
	}

	faultOutboxRouter, err := app.NewRouter(outboxProdCfg, outboxFaultStore, faultOutboxSvc)
	if err != nil {
		t.Fatalf("fault outbox router: %v", err)
	}
	faultOutboxSrv := httptest.NewServer(faultOutboxRouter)
	defer faultOutboxSrv.Close()
	code2, body2 := fetchReadyz(t, faultOutboxSrv.URL)
	if code2 != http.StatusServiceUnavailable || body2["ready"] != false || body2["status"] != "not_ready" {
		t.Fatalf("expected 503 not_ready on outbox query error, got %d %+v", code2, body2)
	}
	email2 := body2["email"].(map[string]any)
	if email2["status"] != "unhealthy" {
		t.Fatalf("expected email status unhealthy, got %+v", email2)
	}
	if _, hasPending := email2["pending"]; hasPending {
		t.Fatalf("expected outbox query error not to present pending count: %+v", email2)
	}
	if _, hasFailed := email2["failed"]; hasFailed {
		t.Fatalf("expected outbox query error not to present failed count: %+v", email2)
	}
}
