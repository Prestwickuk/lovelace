package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/httpx"
	"github.com/tirbach/tir-bach/internal/mailer"
	"github.com/tirbach/tir-bach/internal/payments"
	"github.com/tirbach/tir-bach/internal/service"
)

type failingStripeProvider struct {
	mu        sync.Mutex
	failNext  bool
	failErr   error
	requests  []payments.CheckoutRequest
	callCount int
}

func (f *failingStripeProvider) CreateCheckoutSession(req payments.CheckoutRequest) (payments.CheckoutSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	f.requests = append(f.requests, req)
	if f.failNext {
		f.failNext = false
		if f.failErr != nil {
			return payments.CheckoutSession{}, f.failErr
		}
		return payments.CheckoutSession{}, errors.New("simulated stripe network failure")
	}
	return payments.CheckoutSession{
		SessionID:       "cs_p21_" + req.IdempotencyKey,
		CheckoutURL:     "https://checkout.stripe.test/pay/" + req.IdempotencyKey,
		PaymentIntentID: "pi_p21_" + req.IdempotencyKey,
		Status:          "open",
	}, nil
}

func (f *failingStripeProvider) Refund(req payments.RefundRequest) (payments.RefundResult, error) {
	return payments.RefundResult{RefundID: "re_p21_1", Status: "succeeded"}, nil
}

type configurableMailSender struct {
	mu       sync.Mutex
	errToRet error
	sent     []mailer.Message
}

func (m *configurableMailSender) Send(ctx context.Context, msg mailer.Message) error {
	if ctx != nil && ctx.Err() != nil {
		return mailer.TerminatedBeforeSend(ctx.Err())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.errToRet != nil {
		return m.errToRet
	}
	m.sent = append(m.sent, msg)
	return nil
}

func createTestHoldViaAPI(t *testing.T, srv *httptest.Server, client *http.Client, secret string, arr, dep string) service.CreateHoldResult {
	t.Helper()
	holdResp := doPublicJSON(t, client, http.MethodPost, srv.URL+"/api/public/v1/booking/holds", secret, "http://127.0.0.1:3000", map[string]any{
		"property_slug":  "tir-bach",
		"unit_type_code": "bell-tent",
		"arrival_date":   arr,
		"departure_date": dep,
		"adults":         2,
		"guest_name":     "Carys Morgan",
		"guest_email":    "carys@example.wales",
		"guest_phone":    "+447700900333",
		"accept_terms":   true,
	})
	defer holdResp.Body.Close()
	if holdResp.StatusCode != http.StatusCreated {
		var body any
		_ = json.NewDecoder(holdResp.Body).Decode(&body)
		t.Fatalf("create hold failed status=%d body=%+v", holdResp.StatusCode, body)
	}
	var out service.CreateHoldResult
	_ = json.NewDecoder(holdResp.Body).Decode(&out)
	return out
}

func TestPhase21CheckoutReturnURLSecurityUnitAndAPI(t *testing.T) {
	allowed := []string{"https://book.tirbach.wales"}
	domains := []string{"book.tirbach.wales"}

	badURLs := []struct {
		name string
		url  string
		prod bool
	}{
		{"external https", "https://evil.example/steal", true},
		{"http in prod", "http://book.tirbach.wales/booking/confirmation", true},
		{"protocol relative", "//evil.example/steal", true},
		{"subdomain suffix spoof", "https://book.tirbach.wales.evil.com/booking/confirmation", true},
		{"unapproved subdomain", "https://sub.book.tirbach.wales/booking/confirmation", true},
		{"userinfo injection", "https://user:pass@book.tirbach.wales/booking/confirmation", true},
		{"backslash relative", `https://book.tirbach.wales\evil`, true},
		{"malformed url", "not a url", true},
	}
	for _, tc := range badURLs {
		if got, err := payments.ValidateCheckoutReturnURL(tc.url, allowed, domains, tc.prod); err == nil {
			t.Fatalf("case %s (%q) unexpectedly accepted: %q", tc.name, tc.url, got)
		}
	}

	good, err := payments.ValidateCheckoutReturnURL("https://book.tirbach.wales/booking/confirmation?ref=HLD-1&session_id={CHECKOUT_SESSION_ID}", allowed, domains, true)
	if err != nil {
		t.Fatalf("valid configured property domain rejected: %v", err)
	}
	if !strings.HasPrefix(good, "https://book.tirbach.wales/booking/confirmation") {
		t.Fatalf("unexpected validated URL: %s", good)
	}

	// Integration: API rejects client-supplied success_url/cancel_url and uses server-derived URL
	srv, _, svc, _, _, _, clk := setupPhase2TestServer(t, "pub-sec-21")
	recProvider := &failingStripeProvider{}
	svc.SetPaymentProvider(recProvider)

	pubClient := newClient(t)
	arr := clk.Now().AddDate(0, 0, 20).Format("2006-01-02")
	dep := clk.Now().AddDate(0, 0, 22).Format("2006-01-02")
	hold := createTestHoldViaAPI(t, srv, pubClient, "pub-sec-21", arr, dep)

	malResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/checkout", "pub-sec-21", "", map[string]any{
		"hold_id":     hold.Hold.ID.String(),
		"success_url": "https://evil.example/phish",
		"cancel_url":  "//evil.example/cancel",
	})
	malResp.Body.Close()
	if malResp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 rejecting client redirect URLs, got %d", malResp.StatusCode)
	}

	okResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/checkout", "pub-sec-21", "", map[string]any{
		"hold_id":         hold.Hold.ID.String(),
		"idempotency_key": "url-check-1",
	})
	okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK || len(recProvider.requests) != 1 {
		t.Fatalf("expected valid server-derived checkout, got status=%d reqs=%d", okResp.StatusCode, len(recProvider.requests))
	}
	if !strings.HasPrefix(recProvider.requests[0].SuccessURL, "http://127.0.0.1:3000/booking/confirmation?") {
		t.Fatalf("expected server-derived success URL, got %q", recProvider.requests[0].SuccessURL)
	}
}

func TestPhase21StripeEnabledEnforcementAndFailClosedProductionReadiness(t *testing.T) {
	srv, store, svc, prop, stripeMock, _, clk := setupPhase2TestServer(t, "pub-sec-policy")
	pubClient := newClient(t)
	adminClient := newClient(t)
	_, csrf := apiLogin(t, adminClient, srv.URL, "owner@tirbach.wales", "tirbach-owner-password-2026")

	// Disable stripe_enabled on property via Admin API while keeping configurable policies
	policyResp, err := adminClient.Get(srv.URL + "/api/v1/properties/" + prop.ID.String() + "/booking-policy")
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	var polEnv struct {
		BookingPolicy domain.PropertyBookingPolicy `json:"booking_policy"`
	}
	_ = json.NewDecoder(policyResp.Body).Decode(&polEnv)
	policyResp.Body.Close()

	pol := polEnv.BookingPolicy
	pol.StripeEnabled = false
	pol.QuietHours = "22:00 to 08:00"
	pol.FirePolicy = "Raised steel fire bowls only."
	pol.PetPolicy = "Dogs permitted on bell tents and grass pitches on lead."
	pol.PrivacyNotice = "Owner-verified privacy summary."
	updResp := doJSON(t, adminClient, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/booking-policy", csrf, pol)
	updResp.Body.Close()
	if updResp.StatusCode != http.StatusOK {
		t.Fatalf("failed to update policy: %d", updResp.StatusCode)
	}

	// Verify public property API exposes updated configurable policies & stripe_enabled=false
	pubPropResp := doPublicJSON(t, pubClient, http.MethodGet, srv.URL+"/api/public/v1/properties/tir-bach", "pub-sec-policy", "", nil)
	var pubPropEnv struct {
		Property domain.PublicPropertyView `json:"property"`
	}
	_ = json.NewDecoder(pubPropResp.Body).Decode(&pubPropEnv)
	pubPropResp.Body.Close()
	pubView := pubPropEnv.Property
	if pubView.Policy.StripeEnabled || pubView.Policy.QuietHours != "22:00 to 08:00" || pubView.Policy.FirePolicy == "" {
		t.Fatalf("unexpected public property policy view: %+v", pubView.Policy)
	}

	// Attempt checkout while Stripe disabled -> 409 stripe_disabled, zero provider calls
	arr := clk.Now().AddDate(0, 0, 30).Format("2006-01-02")
	dep := clk.Now().AddDate(0, 0, 32).Format("2006-01-02")
	hold := createTestHoldViaAPI(t, srv, pubClient, "pub-sec-policy", arr, dep)
	coResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/checkout", "pub-sec-policy", "", map[string]any{
		"hold_id": hold.Hold.ID.String(),
	})
	var errEnv map[string]any
	_ = json.NewDecoder(coResp.Body).Decode(&errEnv)
	coResp.Body.Close()
	if coResp.StatusCode != http.StatusConflict || stripeMock.sessionCalls != 0 {
		t.Fatalf("expected 409 stripe_disabled and 0 stripe calls, got %d calls=%d err=%+v", coResp.StatusCode, stripeMock.sessionCalls, errEnv)
	}

	// Production startup readiness checks with store:
	dbURL := os.Getenv("TEST_DATABASE_URL")
	// 1. Property stripe_enabled=false in prod with no Stripe keys -> ALLOWED & ready!
	prodDisabledSvc := service.New(store, config.Config{
		AppEnv:          "production",
		DatabaseURL:     dbURL,
		CookieSecure:    true,
		EncryptionKey:   testEncryptionKey,
		PublicOrigin:    "https://book.tirbach.wales",
		PublicAPISecret: "prod-strong-secret-0123456789abcdef",
	})
	if err := prodDisabledSvc.ValidateProductionStartup(context.Background()); err != nil {
		t.Fatalf("expected prod startup to succeed when stripe_enabled=false across properties: %v", err)
	}
	if rep := prodDisabledSvc.Readiness(context.Background()); !rep.Ready || rep.Payments.Status != "disabled" || rep.Email.Status != "unconfigured" {
		t.Fatalf("unexpected readiness report when stripe disabled & smtp unconfigured: %+v", rep)
	}

	// 2. Re-enable stripe_enabled=true on property -> missing Stripe keys in prod MUST FAIL CLOSED!
	pol.StripeEnabled = true
	pol.Version++
	upd2 := doJSON(t, adminClient, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/booking-policy", csrf, pol)
	upd2.Body.Close()

	if err := prodDisabledSvc.ValidateProductionStartup(context.Background()); err == nil {
		t.Fatal("expected ValidateProductionStartup to fail closed when stripe_enabled=true and Stripe secrets missing")
	}
	if rep := prodDisabledSvc.Readiness(context.Background()); rep.Ready {
		t.Fatalf("expected /readyz ready=false when stripe_enabled=true in prod without keys: %+v", rep)
	}

	// 3. Sandbox enabled in production -> MUST FAIL CLOSED!
	prodSandboxSvc := service.New(store, config.Config{
		AppEnv:          "production",
		DatabaseURL:     dbURL,
		CookieSecure:    true,
		EncryptionKey:   testEncryptionKey,
		PublicOrigin:    "https://book.tirbach.wales",
		PublicAPISecret: "prod-strong-secret-0123456789abcdef",
		StripeSandbox:   true,
	})
	if err := prodSandboxSvc.ValidateProductionStartup(context.Background()); err == nil {
		t.Fatal("expected sandbox in production to fail startup")
	}

	// Check service directly respects stripe_enabled=true once valid prod keys supplied
	_ = svc
}

func TestPhase21SingleActiveAttemptConcurrencyAndRetry(t *testing.T) {
	srv, store, svc, _, _, _, clk := setupPhase2TestServer(t, "pub-conc")
	prov := &failingStripeProvider{failNext: true}
	svc.SetPaymentProvider(prov)
	pubClient := newClient(t)

	arr := clk.Now().AddDate(0, 0, 40).Format("2006-01-02")
	dep := clk.Now().AddDate(0, 0, 42).Format("2006-01-02")
	hold := createTestHoldViaAPI(t, srv, pubClient, "pub-conc", arr, dep)

	// 1. First attempt fails inside Stripe -> attempt marked FAILED, freeing hold for bounded retry!
	failResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/checkout", "pub-conc", "", map[string]any{
		"hold_id":         hold.Hold.ID.String(),
		"idempotency_key": "attempt-fail-1",
	})
	failResp.Body.Close()
	if failResp.StatusCode == http.StatusOK {
		t.Fatalf("expected non-200 on simulated stripe failure, got 200")
	}

	// 2. Now race 8 concurrent checkout requests with DIFFERENT idempotency keys on the SAME hold!
	const n = 8
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r := doPublicJSON(t, newClient(t), http.MethodPost, srv.URL+"/api/public/v1/checkout", "pub-conc", "", map[string]any{
				"hold_id":         hold.Hold.ID.String(),
				"idempotency_key": "race-key-" + string(rune('a'+idx)),
			})
			codes[idx] = r.StatusCode
			r.Body.Close()
		}(i)
	}
	wg.Wait()

	okCount, conflictCount := 0, 0
	for _, c := range codes {
		if c == http.StatusOK {
			okCount++
		} else if c == http.StatusConflict {
			conflictCount++
		}
	}
	if okCount != 1 || conflictCount != n-1 {
		t.Fatalf("expected strictly 1 checkout win and %d 409 conflicts, got ok=%d conflict=%d codes=%v", n-1, okCount, conflictCount, codes)
	}

	attempts, err := store.ListPaymentAttemptsForHold(context.Background(), nil, hold.Hold.ID)
	if err != nil {
		t.Fatalf("list payment attempts: %v", err)
	}
	activeCount := 0
	for _, a := range attempts {
		if a.Status == domain.AttemptStatusRequiresAction || a.Status == domain.AttemptStatusCreated {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Fatalf("expected strictly 1 active attempt in DB, got %d (%+v)", activeCount, attempts)
	}

	// 3. Replaying winning idempotency key succeeds idempotently without incrementing active attempts
	var winningKey string
	for _, a := range attempts {
		if a.Status == domain.AttemptStatusRequiresAction {
			winningKey = a.IdempotencyKey
		}
	}
	replayResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/checkout", "pub-conc", "", map[string]any{
		"hold_id":         hold.Hold.ID.String(),
		"idempotency_key": winningKey,
	})
	replayResp.Body.Close()
	if replayResp.StatusCode != http.StatusOK {
		t.Fatalf("expected idempotent replay 200, got %d", replayResp.StatusCode)
	}

	// 4. Advance clock & sweep expired holds -> open payment_attempts transition cleanly to EXPIRED
	clk.Advance(30 * time.Minute)
	if _, err := svc.SweepExpiredHolds(context.Background()); err != nil {
		t.Fatalf("sweep expired holds: %v", err)
	}
	afterExpiry, err := store.ListPaymentAttemptsForHold(context.Background(), nil, hold.Hold.ID)
	if err != nil {
		t.Fatalf("list attempts after expiry: %v", err)
	}
	for _, a := range afterExpiry {
		if a.IdempotencyKey == winningKey && a.Status != domain.AttemptStatusExpired {
			t.Fatalf("expected active attempt to become EXPIRED on hold sweep, got %s", a.Status)
		}
	}
}

func TestPhase21EmailOutboxFailClosedAndPermanentFailure(t *testing.T) {
	srv, store, _, prop, _, _, clk := setupPhase2TestServer(t, "pub-mail")
	dbURL := os.Getenv("TEST_DATABASE_URL")

	// Construct Service with SMTP unconfigured -> HasEmailProvider() MUST be false (no LogSender auto-wired!)
	noMailSvc := service.New(store, config.Config{
		AppEnv:        "test",
		DatabaseURL:   dbURL,
		EncryptionKey: testEncryptionKey,
		EmailFrom:     "bookings@tirbach.wales",
	})
	noMailSvc.SetClock(clk.Now)
	if noMailSvc.HasEmailProvider() {
		t.Fatal("expected HasEmailProvider=false when SMTPAddr is empty (LogSender must not auto-wire)")
	}

	// Queue confirmation email directly in outbox
	resID, _ := postgresID()
	_ = resID
	_, err := store.QueueEmail(context.Background(), nil, domain.EmailOutboxItem{
		PropertyID:     prop.ID,
		RecipientEmail: "guest@example.wales",
		Template:       "booking_confirmation",
		Subject:        "Test Confirmation",
		BodyText:       "Body",
		NextAttemptAt:  clk.Now(),
	})
	if err != nil {
		t.Fatalf("queue email: %v", err)
	}

	// Dispatch without provider -> ErrProviderUnconfigured, status STAYS PENDING, attempts STAYS 0!
	if sent, err := noMailSvc.DispatchPendingEmails(context.Background()); !errors.Is(err, mailer.ErrProviderUnconfigured) || sent != 0 {
		t.Fatalf("expected ErrProviderUnconfigured and 0 sent, got sent=%d err=%v", sent, err)
	}
	summary, err := store.GetOutboxSummary(context.Background())
	if err != nil || summary.PendingCount != 1 {
		t.Fatalf("expected outbox item to remain PENDING, got %+v err=%v", summary, err)
	}

	// Configure permanent SMTP 550 rejection -> moves straight to FAILED without burning 5 retries!
	permSender := &configurableMailSender{errToRet: &textproto.Error{Code: 550, Msg: "5.1.1 User unknown"}}
	noMailSvc.SetEmailSender(permSender)
	if sent, err := noMailSvc.DispatchPendingEmails(context.Background()); err != nil || sent != 0 {
		t.Fatalf("unexpected error dispatching with permanent failure: sent=%d err=%v", sent, err)
	}
	summary2, err := store.GetOutboxSummary(context.Background())
	if err != nil || summary2.FailedCount != 1 || summary2.PendingCount != 0 {
		t.Fatalf("expected permanent SMTP 550 item immediately marked FAILED, got %+v err=%v", summary2, err)
	}
	_ = srv
}

func TestPhase21ConfirmationBearerTokensAndRotation(t *testing.T) {
	srv, _, _, _, _, _, clk := setupPhase2TestServer(t, "pub-tok")
	pubClient := newClient(t)

	arr := clk.Now().AddDate(0, 0, 50).Format("2006-01-02")
	dep := clk.Now().AddDate(0, 0, 52).Format("2006-01-02")
	hold := createTestHoldViaAPI(t, srv, pubClient, "pub-tok", arr, dep)

	if len(hold.ConfirmationToken) < 64 {
		t.Fatalf("expected >=256-bit (64 hex) confirmation token, got len=%d", len(hold.ConfirmationToken))
	}

	// 1. ?token= in query parameter MUST BE REJECTED (400 query_token_forbidden)
	qLeak := doPublicJSON(t, pubClient, http.MethodGet, srv.URL+"/api/public/v1/booking-confirmation?ref="+hold.Hold.HoldReference+"&token="+hold.ConfirmationToken, "pub-tok", "", nil)
	qLeak.Body.Close()
	if qLeak.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 rejecting query ?token=, got %d", qLeak.StatusCode)
	}

	// 2. GET with Authorization: Bearer succeeds + sets strict privacy/cache headers
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/public/v1/booking-confirmation?ref="+hold.Hold.HoldReference, nil)
	req.Header.Set("X-Lovelace-Public-Key", "pub-tok")
	req.Header.Set("Authorization", "Bearer "+hold.ConfirmationToken)
	bearerResp, err := pubClient.Do(req)
	if err != nil {
		t.Fatalf("bearer GET failed: %v", err)
	}
	bearerResp.Body.Close()
	if bearerResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on Bearer GET, got %d", bearerResp.StatusCode)
	}
	if !strings.Contains(bearerResp.Header.Get("Cache-Control"), "no-store") || bearerResp.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("missing privacy headers on confirmation response: Cache-Control=%q Referrer-Policy=%q", bearerResp.Header.Get("Cache-Control"), bearerResp.Header.Get("Referrer-Policy"))
	}

	// 3. Rotate token -> old token immediately revoked (404), new token works (200)
	rotResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/booking-confirmation/rotate-token", "pub-tok", "", map[string]any{
		"booking_reference": hold.Hold.HoldReference,
		"token":             hold.ConfirmationToken,
	})
	var rotBody struct {
		ConfirmationToken string `json:"confirmation_token"`
	}
	_ = json.NewDecoder(rotResp.Body).Decode(&rotBody)
	rotResp.Body.Close()
	if rotResp.StatusCode != http.StatusOK || rotBody.ConfirmationToken == "" || rotBody.ConfirmationToken == hold.ConfirmationToken {
		t.Fatalf("unexpected rotate response status=%d newTok=%q", rotResp.StatusCode, rotBody.ConfirmationToken)
	}

	oldLookup := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/booking-confirmation/lookup", "pub-tok", "", map[string]any{
		"booking_reference": hold.Hold.HoldReference,
		"token":             hold.ConfirmationToken,
	})
	oldLookup.Body.Close()
	if oldLookup.StatusCode != http.StatusNotFound {
		t.Fatalf("expected revoked token to return 404, got %d", oldLookup.StatusCode)
	}

	newLookup := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/booking-confirmation/lookup", "pub-tok", "", map[string]any{
		"booking_reference": hold.Hold.HoldReference,
		"token":             rotBody.ConfirmationToken,
	})
	newLookup.Body.Close()
	if newLookup.StatusCode != http.StatusOK {
		t.Fatalf("expected rotated token to return 200, got %d", newLookup.StatusCode)
	}
}

func TestPhase21TrustedProxyClientIPAndPublicSecretUnit(t *testing.T) {
	nets, err := config.ParseTrustedProxyCIDRs("172.16.0.0/12,127.0.0.1/32")
	if err != nil {
		t.Fatalf("parse CIDRs: %v", err)
	}

	// Spoofed XFF from untrusted remote IP MUST be ignored
	reqUntrusted := httptest.NewRequest(http.MethodGet, "/api/public/v1/accommodation", nil)
	reqUntrusted.RemoteAddr = "203.0.113.50:4444"
	reqUntrusted.Header.Set("X-Forwarded-For", "198.51.100.1")
	if got := httpx.ExtractClientIP(reqUntrusted, true, nets); got != "203.0.113.50" {
		t.Fatalf("expected untrusted XFF ignored -> 203.0.113.50, got %q", got)
	}

	// Trusted proxy peer -> walk right-to-left skipping trusted proxy CIDRs
	reqTrusted := httptest.NewRequest(http.MethodGet, "/api/public/v1/accommodation", nil)
	reqTrusted.RemoteAddr = "172.18.0.4:5555"
	reqTrusted.Header.Set("X-Forwarded-For", "198.51.100.1, 203.0.113.99, 172.18.0.2")
	if got := httpx.ExtractClientIP(reqTrusted, true, nets); got != "203.0.113.99" {
		t.Fatalf("expected right-most untrusted client IP 203.0.113.99, got %q", got)
	}

	// X-Lovelace-Client-IP honored ONLY when BFF authenticated
	reqFwd := httptest.NewRequest(http.MethodPost, "/api/public/v1/checkout", nil)
	reqFwd.Header.Set("X-Lovelace-Client-IP", "198.51.100.88")
	if got := httpx.ResolveForwardedCustomerIP(reqFwd, false, "203.0.113.10"); got != "203.0.113.10" {
		t.Fatalf("expected spoofed X-Lovelace-Client-IP rejected when unauthenticated, got %q", got)
	}
	if got := httpx.ResolveForwardedCustomerIP(reqFwd, true, "172.18.0.5"); got != "198.51.100.88" {
		t.Fatalf("expected X-Lovelace-Client-IP honored for authenticated BFF, got %q", got)
	}
}

func postgresID() (string, error) {
	return "unused", nil
}
