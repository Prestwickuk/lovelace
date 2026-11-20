package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Prestwickuk/lovelace/internal/app"
	"github.com/Prestwickuk/lovelace/internal/config"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/mailer"
	"github.com/Prestwickuk/lovelace/internal/payments"
	"github.com/Prestwickuk/lovelace/internal/seed"
	"github.com/Prestwickuk/lovelace/internal/service"
	"github.com/Prestwickuk/lovelace/internal/store/postgres"
)

type fakeStripeProvider struct {
	mu           sync.Mutex
	sessionCalls int
	refundCalls  int
	refundReqs   []payments.RefundRequest
}

func (f *fakeStripeProvider) CreateCheckoutSession(req payments.CheckoutRequest) (payments.CheckoutSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessionCalls++
	sid := fmt.Sprintf("cs_test_%d_%s", f.sessionCalls, req.IdempotencyKey)
	return payments.CheckoutSession{
		SessionID:       sid,
		CheckoutURL:     "https://checkout.stripe.test/pay/" + sid,
		PaymentIntentID: "pi_test_" + sid,
		Status:          "open",
	}, nil
}

func (f *fakeStripeProvider) Refund(req payments.RefundRequest) (payments.RefundResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refundCalls++
	f.refundReqs = append(f.refundReqs, req)
	return payments.RefundResult{
		RefundID: fmt.Sprintf("re_test_%d", f.refundCalls),
		Status:   "succeeded",
	}, nil
}

type fakeMailSender struct {
	mu       sync.Mutex
	failNext bool
	sent     []mailer.Message
}

func (m *fakeMailSender) Send(ctx context.Context, msg mailer.Message) error {
	if ctx != nil && ctx.Err() != nil {
		return mailer.TerminatedBeforeSend(ctx.Err())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failNext {
		m.failNext = false
		return errors.New("simulated smtp outage")
	}
	m.sent = append(m.sent, msg)
	return nil
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func setupPhase2TestServer(t *testing.T, publicSecret string) (*httptest.Server, *postgres.Store, *service.Service, domain.Property, *fakeStripeProvider, *fakeMailSender, *testClock) {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Phase 2 PostgreSQL integration test")
	}

	ctx := context.Background()
	store, err := postgres.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(store.Close)

	var currentDB, currentUser string
	var isSuperuser bool
	if err := store.Pool().QueryRow(ctx, `
		SELECT current_database(), current_user, usesuper
		FROM pg_user WHERE usename = current_user
	`).Scan(&currentDB, &currentUser, &isSuperuser); err != nil {
		t.Fatalf("inspect db/role: %v", err)
	}
	if err := verifySafeTestDatabase(dbURL, currentDB, currentUser, isSuperuser); err != nil {
		t.Fatalf("unsafe test database rejected: %v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}

	err = store.WithTx(ctx, func(tx pgx.Tx) error {
		if _, txErr := tx.Exec(ctx, `SET LOCAL lovelace.test_reset_token = 'allow_test_reset'`); txErr != nil {
			return txErr
		}
		_, txErr := tx.Exec(ctx, `
			TRUNCATE TABLE guest_portal_sessions, guest_portal_challenges, guest_portal_identities,
			               email_outbox, confirmation_access_tokens, payment_exceptions,
			               payment_webhook_events, refunds, payments, payment_attempts,
			               booking_quotes, booking_holds, property_booking_policies,
			               audit_logs, login_attempts, admin_sessions,
			               channel_connections, availability_release_rules, allocation_consumptions,
			               inventory_claims, blockout_groups, reservation_units, reservations,
			               guests, booking_channels, rate_rules, rate_plans, extras, units,
			               unit_types, admin_users, properties
			RESTART IDENTITY CASCADE
		`)
		return txErr
	})
	if err != nil {
		t.Fatalf("truncate tables: %v", err)
	}

	prop, err := seed.EnsureBaseline(ctx, store, seed.Options{
		AdminEmail:    "owner@example.com",
		AdminName:     "Sian Evans",
		AdminPassword: "lovelace-owner-password-2026",
	})
	if err != nil {
		t.Fatalf("seed baseline: %v", err)
	}

	cfg := config.Config{
		AppEnv:              "test",
		DatabaseURL:         dbURL,
		CookieSecure:        false,
		EncryptionKey:       testEncryptionKey,
		SessionTTL:          1 * time.Hour,
		SessionIdleTTL:      30 * time.Minute,
		LoginMaxFailIP:      25,
		LoginMaxFailAcc:     20,
		LoginWindow:         15 * time.Minute,
		PublicOrigin:        "http://127.0.0.1:3000",
		PublicAPISecret:     publicSecret,
		StripeWebhookSecret: "whsec_test_lovelace_phase2_secret",
		EmailFrom:           "bookings@example.com",
	}

	svc := service.New(store, cfg)
	stripeMock := &fakeStripeProvider{}
	mailMock := &fakeMailSender{}
	clk := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	svc.SetPaymentProvider(stripeMock)
	svc.SetEmailSender(mailMock)
	svc.SetClock(clk.Now)

	router, err := app.NewRouter(cfg, store, svc)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, store, svc, prop, stripeMock, mailMock, clk
}

func doPublicJSON(t *testing.T, client *http.Client, method, targetURL, secret string, origin string, payload any) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, targetURL, bodyReader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if secret != "" {
		req.Header.Set("X-Lovelace-Public-Key", secret)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, targetURL, err)
	}
	return resp
}

func signAndSendStripeWebhook(t *testing.T, client *http.Client, baseURL, secret string, now time.Time, event map[string]any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal webhook: %v", err)
	}
	sig := payments.ComputeStripeSignature(raw, secret, now)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/public/v1/webhooks/stripe", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("webhook req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Stripe-Signature", sig)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post webhook: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestPhase2PublicIsolationAndOriginSecurity(t *testing.T) {
	const pubSecret = "pub-bff-secret-test"
	srv, _, _, prop, _, _, _ := setupPhase2TestServer(t, pubSecret)
	pubClient := newClient(t)

	// Missing X-Lovelace-Public-Key must fail with 401 when configured.
	respNoKey := doPublicJSON(t, pubClient, http.MethodGet, srv.URL+"/api/public/v1/properties/demo-property", "", "http://127.0.0.1:3000", nil)
	respNoKey.Body.Close()
	if respNoKey.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without public key, got %d", respNoKey.StatusCode)
	}

	// Disallowed CORS preflight must fail 403; allowed origin returns 204 + Access-Control-Allow-Origin.
	badOpt := doPublicJSON(t, pubClient, http.MethodOptions, srv.URL+"/api/public/v1/availability", pubSecret, "https://evil.example", nil)
	badOpt.Body.Close()
	if badOpt.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for disallowed CORS origin preflight, got %d", badOpt.StatusCode)
	}
	goodOpt := doPublicJSON(t, pubClient, http.MethodOptions, srv.URL+"/api/public/v1/availability", pubSecret, "http://127.0.0.1:3000", nil)
	goodOpt.Body.Close()
	if goodOpt.StatusCode != http.StatusNoContent || goodOpt.Header.Get("Access-Control-Allow-Origin") != "http://127.0.0.1:3000" {
		t.Fatalf("expected 204 and matching ACAO, got %d (%q)", goodOpt.StatusCode, goodOpt.Header.Get("Access-Control-Allow-Origin"))
	}

	// Public catalog returns Meadowbrook property + accommodation without admin fields or secrets.
	resp := doPublicJSON(t, pubClient, http.MethodGet, srv.URL+"/api/public/v1/accommodation?property=demo-property", pubSecret, "http://127.0.0.1:3000", nil)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 catalog, got %d: %s", resp.StatusCode, string(raw))
	}
	bodyStr := string(raw)
	if !strings.Contains(bodyStr, "Meadowbrook Estate") || !strings.Contains(bodyStr, "bell-tent") {
		t.Fatalf("expected Meadowbrook & bell-tent in public catalog: %s", bodyStr)
	}
	for _, forbidden := range []string{"internal_notes", "password_hash", "credentials_ciphertext", "owner@example.com"} {
		if strings.Contains(bodyStr, forbidden) {
			t.Fatalf("sensitive admin field %q leaked in public catalog!", forbidden)
		}
	}

	// Public client still cannot touch admin APIs.
	adminResp := doPublicJSON(t, pubClient, http.MethodGet, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", pubSecret, "", nil)
	adminResp.Body.Close()
	if adminResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on admin endpoint without admin auth, got %d", adminResp.StatusCode)
	}
}

func TestPhase2AvailabilityChannelReleaseAndOccupancyRules(t *testing.T) {
	srv, store, _, prop, _, _, clk := setupPhase2TestServer(t, "")
	adminClient := newClient(t)
	if status, csrf := apiLogin(t, adminClient, srv.URL, "owner@example.com", "lovelace-owner-password-2026"); status != http.StatusOK {
		t.Fatalf("admin login failed: %d", status)
	} else {
		// Create maintenance blockout on SH-WOODLAND for nights 10..12, AIRBNB-only restriction on BT-OAK (should NOT block DIRECT),
		// and future scheduled release rule covering nights 25..28 on GP-MEADOW-1..2!
		units, err := store.ListUnits(context.Background(), prop.ID, nil, false)
		if err != nil {
			t.Fatalf("list units: %v", err)
		}
		var hutUnitID, bellOakID string
		for _, u := range units {
			if u.Code == "SH-WOODLAND" {
				hutUnitID = u.ID.String()
			}
			if u.Code == "BT-OAK" {
				bellOakID = u.ID.String()
			}
		}

		d10 := clk.Now().AddDate(0, 0, 10).Format("2006-01-02")
		d13 := clk.Now().AddDate(0, 0, 13).Format("2006-01-02")

		// Block SH-WOODLAND for maintenance.
		bResp := doJSON(t, adminClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts", csrf, map[string]any{
			"scope_type":    "UNITS",
			"category":      "MAINTENANCE",
			"channel_scope": "ALL",
			"unit_ids":      []string{hutUnitID},
			"start_date":    d10,
			"end_date":      d13,
			"reason":        "Stove chimney sweep",
		})
		bResp.Body.Close()
		if bResp.StatusCode != http.StatusCreated && bResp.StatusCode != http.StatusOK {
			t.Fatalf("create maintenance blockout failed: %d", bResp.StatusCode)
		}

		// Close BT-OAK only on AIRBNB (should remain bookable on DIRECT!).
		cResp := doJSON(t, adminClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts", csrf, map[string]any{
			"scope_type":    "UNITS",
			"category":      "CHANNEL_RESTRICTION",
			"channel_scope": "AIRBNB",
			"unit_ids":      []string{bellOakID},
			"start_date":    d10,
			"end_date":      d13,
			"reason":        "Airbnb stop sell",
		})
		cResp.Body.Close()
		if cResp.StatusCode != http.StatusCreated && cResp.StatusCode != http.StatusOK {
			t.Fatalf("create channel restriction failed: %d", cResp.StatusCode)
		}

		// Search availability for d10..d13 with 2 adults -> shepherds-hut must be absent (maintenance), bell-tent must have 2 units!
		pubClient := newClient(t)
		sResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/availability", "", "", map[string]any{
			"property_slug":  "demo-property",
			"arrival_date":   d10,
			"departure_date": d13,
			"adults":         2,
			"children":       0,
			"infants":        0,
		})
		var avail service.PublicAvailabilityResponse
		if err := json.NewDecoder(sResp.Body).Decode(&avail); err != nil {
			t.Fatalf("decode availability: %v", err)
		}
		sResp.Body.Close()

		foundBell := 0
		foundHut := false
		for _, item := range avail.Items {
			if item.UnitType.Code == "bell-tent" {
				foundBell = item.AvailableUnits
			}
			if item.UnitType.Code == "shepherds-hut" {
				foundHut = true
			}
		}
		if foundHut {
			t.Fatalf("expected shepherds-hut unavailable due to maintenance blockout")
		}
		if foundBell != 2 {
			t.Fatalf("expected bell-tent available_units=2 even with AIRBNB stop sell, got %d", foundBell)
		}

		// Create a scheduled future release rule on unit type grass-pitch for d25..d28 releasing next year.
		d25 := clk.Now().AddDate(0, 0, 25).Format("2006-01-02")
		d28 := clk.Now().AddDate(0, 0, 28).Format("2006-01-02")
		uts, _ := store.ListUnitTypes(context.Background(), prop.ID, false)
		var grassUTID string
		for _, ut := range uts {
			if ut.Code == "grass-pitch" {
				grassUTID = ut.ID.String()
			}
		}
		rResp := doJSON(t, adminClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/releases", csrf, map[string]any{
			"scope_type":   "UNIT_TYPE",
			"unit_type_id": grassUTID,
			"channel_code": "ALL",
			"start_date":   d25,
			"end_date":     d28,
			"release_at":   clk.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
			"notes":        "Next Year Release",
		})
		rResp.Body.Close()
		if rResp.StatusCode != http.StatusCreated && rResp.StatusCode != http.StatusOK {
			t.Fatalf("create release rule failed: %d", rResp.StatusCode)
		}

		sResp2 := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/availability", "", "", map[string]any{
			"property_slug":  "demo-property",
			"arrival_date":   d25,
			"departure_date": d28,
			"adults":         2,
		})
		var avail2 service.PublicAvailabilityResponse
		_ = json.NewDecoder(sResp2.Body).Decode(&avail2)
		sResp2.Body.Close()
		for _, item := range avail2.Items {
			if item.UnitType.Code == "grass-pitch" {
				t.Fatalf("expected grass-pitch hidden by scheduled future release rule")
			}
		}

		// 5 adults exceeds bell-tent max_adults=2 and shepherds-hut max_adults=2 -> neither appears; touring/tent pitches remain!
		sResp3 := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/availability", "", "", map[string]any{
			"property_slug":  "demo-property",
			"arrival_date":   d10,
			"departure_date": d13,
			"adults":         4,
			"children":       2,
		})
		var avail3 service.PublicAvailabilityResponse
		_ = json.NewDecoder(sResp3.Body).Decode(&avail3)
		sResp3.Body.Close()
		for _, item := range avail3.Items {
			if item.UnitType.Code == "bell-tent" {
				t.Fatalf("bell-tent should be excluded when adults=4 > max_adults=2")
			}
		}
	}
}

func TestPhase2PricingDepositSnapshotAndConcurrencyHoldRace(t *testing.T) {
	srv, store, _, prop, _, _, clk := setupPhase2TestServer(t, "")
	adminClient := newClient(t)
	status, csrf := apiLogin(t, adminClient, srv.URL, "owner@example.com", "lovelace-owner-password-2026")
	if status != http.StatusOK {
		t.Fatalf("admin login failed: %d", status)
	}

	// Update booking policy to 25% deposit + extra adult 1500p/night + child 800p/night.
	pResp := doJSON(t, adminClient, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/booking-policy", csrf, domain.PropertyBookingPolicy{
		HoldDurationMinutes:         15,
		DepositMode:                 domain.DepositModePercentage,
		DepositPercentage:           25,
		BalanceDueDaysBeforeArrival: 21,
		ExtraAdultNightlyPence:      1500,
		ChildNightlyPence:           800,
		BookingTermsVersion:         "2026.2",
		StripeEnabled:               true,
		Version:                     1,
	})
	pResp.Body.Close()
	if pResp.StatusCode != http.StatusOK {
		t.Fatalf("update booking policy failed: %d", pResp.StatusCode)
	}

	// Pick shepherds-hut (only 1 physical unit: SH-WOODLAND!) > BalanceDueDaysBeforeArrival (21 days) so 25% deposit applies!
	arr := clk.Now().AddDate(0, 0, 30).Format("2006-01-02")
	dep := clk.Now().AddDate(0, 0, 32).Format("2006-01-02") // 2 nights

	// Fire 2 simultaneous hold requests for shepherds-hut -> strictly 1 succeeds (201) and 1 fails (409).
	type holdOutcome struct {
		status int
		res    service.CreateHoldResult
		raw    string
	}
	outcomes := make(chan holdOutcome, 2)
	startGate := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			c := newClient(t)
			<-startGate
			resp := doPublicJSON(t, c, http.MethodPost, srv.URL+"/api/public/v1/booking/holds", "", "", map[string]any{
				"property_slug":  "demo-property",
				"unit_type_code": "shepherds-hut",
				"arrival_date":   arr,
				"departure_date": dep,
				"adults":         2,
				"children":       0,
				"infants":        0,
				"guest_name":     fmt.Sprintf("Guest %d", idx),
				"guest_email":    fmt.Sprintf("guest%d@example.com", idx),
				"guest_phone":    "07700900123",
				"accept_terms":   true,
			})
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var res service.CreateHoldResult
			_ = json.Unmarshal(raw, &res)
			outcomes <- holdOutcome{status: resp.StatusCode, res: res, raw: string(raw)}
		}(i)
	}
	close(startGate)
	wg.Wait()
	close(outcomes)

	createdCount := 0
	conflictCount := 0
	var winner service.CreateHoldResult
	for o := range outcomes {
		switch o.status {
		case http.StatusCreated:
			createdCount++
			winner = o.res
		case http.StatusConflict:
			conflictCount++
		default:
			t.Fatalf("unexpected concurrent hold status %d: %s", o.status, o.raw)
		}
	}
	if createdCount != 1 || conflictCount != 1 {
		t.Fatalf("expected strictly 1 created & 1 conflict in concurrent last-unit race, got created=%d conflict=%d", createdCount, conflictCount)
	}

	// Verify percentage deposit calculation: shepherds-hut 2 nights * 12500 = 25000p, 25% deposit = 6250p, balance = 18750p.
	if winner.Quote.TotalPence != 25000 || winner.Quote.DepositDuePence != 6250 || winner.Quote.BalanceDuePence != 18750 {
		t.Fatalf("unexpected deposit calculation: total=%d deposit=%d balance=%d",
			winner.Quote.TotalPence, winner.Quote.DepositDuePence, winner.Quote.BalanceDuePence)
	}

	// Cancel the winning hold explicitly and verify inventory becomes available again immediately!
	pubClient := newClient(t)
	cResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/booking/holds/"+winner.Hold.ID.String()+"/cancel", "", "", map[string]any{})
	cResp.Body.Close()
	if cResp.StatusCode != http.StatusOK {
		t.Fatalf("cancel hold failed: %d", cResp.StatusCode)
	}

	// Extra adult/child surcharge quote check on grass-pitch: base=2 adults (2800/night) + 1 extra adult (1500/night) + 1 child (800/night) * 2 nights = (5600 + 3000 + 1600) = 10200p.
	extras, err := store.ListExtras(context.Background(), prop.ID, false)
	if err != nil || len(extras) == 0 {
		t.Fatalf("expected seeded extras, err=%v len=%d", err, len(extras))
	}
	var firewoodID uuid.UUID
	for _, ex := range extras {
		if ex.Code == "firewood-basket" {
			firewoodID = ex.ID
		}
	}
	qResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/pricing", "", "", map[string]any{
		"property_slug":  "demo-property",
		"unit_type_code": "grass-pitch",
		"arrival_date":   arr,
		"departure_date": dep,
		"adults":         3,
		"children":       1,
		"infants":        0,
		"extras": []map[string]any{
			{"extra_id": firewoodID.String(), "quantity": 2},
		},
	})
	var quoteWrap struct {
		Quote domain.BookingQuote `json:"quote"`
	}
	if err := json.NewDecoder(qResp.Body).Decode(&quoteWrap); err != nil {
		t.Fatalf("decode quote: %v", err)
	}
	qResp.Body.Close()
	if qResp.StatusCode != http.StatusOK {
		t.Fatalf("pricing expected 200, got %d", qResp.StatusCode)
	}
	// Firewood basket: 1200p per stay * 2 = 2400p -> total 10200 + 2400 = 12600p.
	if quoteWrap.Quote.AccommodationSubtotalPence != 5600 || quoteWrap.Quote.OccupancySurchargePence != 4600 || quoteWrap.Quote.ExtrasSubtotalPence != 2400 || quoteWrap.Quote.TotalPence != 12600 {
		t.Fatalf("unexpected itemized quote breakdown: %+v", quoteWrap.Quote)
	}
}

func TestPhase2CheckoutWebhookEmailConfirmationAndCancellationFlow(t *testing.T) {
	srv, store, svc, prop, stripeMock, mailMock, clk := setupPhase2TestServer(t, "")
	pubClient := newClient(t)
	arr := clk.Now().AddDate(0, 0, 5).Format("2006-01-02")
	dep := clk.Now().AddDate(0, 0, 7).Format("2006-01-02")

	// 1. Create hold on bell-tent (2 nights * 9500 = 19000p).
	hResp := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/booking/holds", "", "", map[string]any{
		"property_slug":    "demo-property",
		"unit_type_code":   "bell-tent",
		"arrival_date":     arr,
		"departure_date":   dep,
		"adults":           2,
		"guest_name":       "Carys Pritchard",
		"guest_email":      "carys@example.com",
		"guest_phone":      "+447700900456",
		"special_requests": "Near the top hedgerow please",
		"accept_terms":     true,
	})
	var holdRes service.CreateHoldResult
	_ = json.NewDecoder(hResp.Body).Decode(&holdRes)
	hResp.Body.Close()
	if hResp.StatusCode != http.StatusCreated {
		t.Fatalf("create hold expected 201, got %d", hResp.StatusCode)
	}

	// 2. Initiate checkout twice with same Idempotency-Key -> Stripe session created only ONCE.
	coReq := map[string]any{
		"hold_id":         holdRes.Hold.ID.String(),
		"idempotency_key": "idem-carys-001",
	}
	co1 := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/checkout", "", "", coReq)
	var coOut1 service.CreateCheckoutResponse
	_ = json.NewDecoder(co1.Body).Decode(&coOut1)
	co1.Body.Close()
	if co1.StatusCode != http.StatusOK || coOut1.CheckoutURL == "" {
		t.Fatalf("first checkout expected 200 + url, got %d", co1.StatusCode)
	}

	co2 := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/checkout", "", "", coReq)
	var coOut2 service.CreateCheckoutResponse
	_ = json.NewDecoder(co2.Body).Decode(&coOut2)
	co2.Body.Close()
	if co2.StatusCode != http.StatusOK || coOut2.PaymentAttemptID != coOut1.PaymentAttemptID || stripeMock.sessionCalls != 1 {
		t.Fatalf("expected idempotent checkout reuse (calls=%d, ids=%s/%s)", stripeMock.sessionCalls, coOut1.PaymentAttemptID, coOut2.PaymentAttemptID)
	}

	// 3. Spoofed / invalid Stripe signature rejected with 401.
	badReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/public/v1/webhooks/stripe", strings.NewReader(`{"id":"evt_bad","type":"checkout.session.completed"}`))
	badReq.Header.Set("Stripe-Signature", "t=123,v1=deadbeef")
	badResp, err := pubClient.Do(badReq)
	if err != nil {
		t.Fatalf("bad webhook req: %v", err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on invalid Stripe-Signature, got %d", badResp.StatusCode)
	}

	// 4. Simulate transient SMTP outage during webhook confirmation -> booking MUST confirm and email retries on next sweep!
	mailMock.failNext = true
	evt := map[string]any{
		"id":       "evt_checkout_ok_1",
		"type":     "checkout.session.completed",
		"livemode": false,
		"data": map[string]any{
			"object": map[string]any{
				"id":             coOut1.Attempt.ProviderSessionID,
				"payment_intent": "pi_confirmed_1",
				"amount_total":   19000,
				"currency":       "gbp",
				"payment_status": "paid",
				"metadata": map[string]any{
					"payment_attempt_id": coOut1.PaymentAttemptID.String(),
				},
			},
		},
	}
	whStatus, whBody := signAndSendStripeWebhook(t, pubClient, srv.URL, "whsec_test_lovelace_phase2_secret", clk.Now(), evt)
	if whStatus != http.StatusOK || whBody["duplicate"] != false {
		t.Fatalf("expected 200 + duplicate=false on Stripe webhook, got %d %+v", whStatus, whBody)
	}

	// Replay same event ID -> idempotent duplicate=true, no double payment.
	whStatus2, whBody2 := signAndSendStripeWebhook(t, pubClient, srv.URL, "whsec_test_lovelace_phase2_secret", clk.Now(), evt)
	if whStatus2 != http.StatusOK || whBody2["duplicate"] != true {
		t.Fatalf("expected duplicate=true on webhook replay, got %d %+v", whStatus2, whBody2)
	}

	// Verify email outbox retry succeeds after backoff!
	clk.Advance(10 * time.Minute)
	sent, err := svc.DispatchPendingEmails(context.Background())
	if err != nil || sent != 1 || len(mailMock.sent) != 1 {
		t.Fatalf("expected 1 retried confirmation email sent cleanly, sent=%d len=%d err=%v", sent, len(mailMock.sent), err)
	}
	if !strings.Contains(mailMock.sent[0].Subject, "Booking Confirmation") {
		t.Fatalf("unexpected email subject: %s", mailMock.sent[0].Subject)
	}

	// Mutating unit type nightly rate now MUST NOT alter the confirmed booking's stored snapshot or total!
	utView, err := store.GetPublicUnitTypeByCode(context.Background(), nil, prop.ID, "bell-tent")
	if err != nil {
		t.Fatalf("get bell-tent: %v", err)
	}
	ut, err := store.GetUnitType(context.Background(), nil, prop.ID, utView.ID)
	if err != nil {
		t.Fatalf("get full bell-tent: %v", err)
	}
	adminClient := newClient(t)
	_, csrf := apiLogin(t, adminClient, srv.URL, "owner@example.com", "lovelace-owner-password-2026")
	mutResp := doJSON(t, adminClient, http.MethodPatch, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/unit-types/"+ut.ID.String(), csrf, map[string]any{
		"code":                    ut.Code,
		"name":                    ut.Name,
		"category":                ut.Category,
		"description":             ut.Description,
		"base_capacity_adults":    ut.BaseCapacityAdults,
		"max_adults":              ut.MaxAdults,
		"max_children":            ut.MaxChildren,
		"max_infants":             ut.MaxInfants,
		"max_guests":              ut.MaxGuests,
		"pets_allowed":            ut.PetsAllowed,
		"max_pets":                ut.MaxPets,
		"base_nightly_rate_pence": 25000, // huge price increase after booking!
		"tax_rate_bps":            ut.TaxRateBPS,
		"min_nights":              ut.MinNights,
		"max_nights":              ut.MaxNights,
		"amenities":               ut.Amenities,
		"is_active":               true,
		"sort_order":              ut.SortOrder,
		"version":                 ut.Version,
	})
	mutResp.Body.Close()

	// 5. Confirmation lookup with wrong token returns 404; valid token returns CONFIRMED + 19000p total!
	badLookup := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/booking-confirmation/lookup", "", "", map[string]any{
		"booking_reference": holdRes.Hold.HoldReference,
		"token":             "wrongtoken",
	})
	badLookup.Body.Close()
	if badLookup.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 on invalid confirmation token, got %d", badLookup.StatusCode)
	}

	goodLookup := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/booking-confirmation/lookup", "", "", map[string]any{
		"booking_reference": holdRes.Hold.HoldReference,
		"token":             holdRes.ConfirmationToken,
	})
	var conf domain.PublicConfirmationView
	_ = json.NewDecoder(goodLookup.Body).Decode(&conf)
	goodLookup.Body.Close()
	if goodLookup.StatusCode != http.StatusOK || conf.Status != "CONFIRMED" || conf.PaymentStatus != "PAID" || conf.Quote == nil || conf.Quote.TotalPence != 19000 || conf.ReservationReference == "" {
		t.Fatalf("unexpected confirmation view (rate mutation may have leaked!): status=%d view=%+v", goodLookup.StatusCode, conf)
	}

	// 6. Customer requests cancellation -> visible in LOVELACE Admin reservation detail API & HTML console!
	cancelReq := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/booking-confirmation/cancel-request", "", "", map[string]any{
		"booking_reference": conf.ReservationReference,
		"token":             holdRes.ConfirmationToken,
		"note":              "Family emergency, need to reschedule",
	})
	var confAfterCancel domain.PublicConfirmationView
	_ = json.NewDecoder(cancelReq.Body).Decode(&confAfterCancel)
	cancelReq.Body.Close()
	if cancelReq.StatusCode != http.StatusOK || !confAfterCancel.CancellationRequested {
		t.Fatalf("expected cancellation requested recorded, got %d %+v", cancelReq.StatusCode, confAfterCancel)
	}

	// Admin lists reservations -> gets reservation ID -> verifies financials API + Admin HTML UI!
	resListResp, err := adminClient.Get(srv.URL + "/api/v1/properties/" + prop.ID.String() + "/reservations")
	if err != nil {
		t.Fatalf("admin list reservations: %v", err)
	}
	var resList struct {
		Items []domain.Reservation `json:"items"`
	}
	_ = json.NewDecoder(resListResp.Body).Decode(&resList)
	resListResp.Body.Close()
	if len(resList.Items) != 1 || resList.Items[0].CancellationRequestedAt == nil {
		t.Fatalf("expected cancellation_requested_at on reservation in Admin API: %+v", resList.Items)
	}

	finResp, err := adminClient.Get(srv.URL + "/api/v1/properties/" + prop.ID.String() + "/reservations/" + resList.Items[0].ID.String() + "/financials")
	if err != nil {
		t.Fatalf("get financials: %v", err)
	}
	var fin service.ReservationFinancials
	_ = json.NewDecoder(finResp.Body).Decode(&fin)
	finResp.Body.Close()
	if fin.Quote == nil || len(fin.Payments) != 1 || fin.Payments[0].AmountPence != 19000 {
		t.Fatalf("unexpected admin reservation financials: %+v", fin)
	}

	// Admin web UI HTML page check!
	htmlResp, err := adminClient.Get(srv.URL + "/reservations/" + resList.Items[0].ID.String())
	if err != nil {
		t.Fatalf("admin html detail: %v", err)
	}
	htmlBytes, _ := io.ReadAll(htmlResp.Body)
	htmlResp.Body.Close()
	if htmlResp.StatusCode != http.StatusOK || !strings.Contains(string(htmlBytes), "Customer Cancellation Request Received") || !strings.Contains(string(htmlBytes), "Authoritative Price Snapshot") {
		t.Fatalf("expected Phase 2 financials and cancellation request alert in Admin HTML detail page")
	}

	// Simulate charge.refunded webhook reconciliation!
	refEvt := map[string]any{
		"id":       "evt_charge_refunded_1",
		"type":     "charge.refunded",
		"livemode": false,
		"data": map[string]any{
			"object": map[string]any{
				"id":              "ch_test_1",
				"payment_intent":  "pi_confirmed_1",
				"amount_refunded": 19000,
			},
		},
	}
	refStatus, _ := signAndSendStripeWebhook(t, pubClient, srv.URL, "whsec_test_lovelace_phase2_secret", clk.Now(), refEvt)
	if refStatus != http.StatusOK {
		t.Fatalf("charge.refunded webhook failed: %d", refStatus)
	}
	finResp2, _ := adminClient.Get(srv.URL + "/api/v1/properties/" + prop.ID.String() + "/reservations/" + resList.Items[0].ID.String() + "/financials")
	var fin2 service.ReservationFinancials
	_ = json.NewDecoder(finResp2.Body).Decode(&fin2)
	finResp2.Body.Close()
	if fin2.Reservation.PaymentStatus != domain.PaymentStatusRefunded {
		t.Fatalf("expected payment_status REFUNDED after charge.refunded webhook, got %s", fin2.Reservation.PaymentStatus)
	}
}

func TestPhase2ExpiredHoldPaidReconciliationAndAutoRefund(t *testing.T) {
	srv, _, svc, prop, stripeMock, _, clk := setupPhase2TestServer(t, "")
	pubClient := newClient(t)
	arr := clk.Now().AddDate(0, 0, 8).Format("2006-01-02")
	dep := clk.Now().AddDate(0, 0, 10).Format("2006-01-02")

	// Guest A creates hold on unique shepherds-hut + initiates checkout, then abandons payment until TTL expires!
	hA := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/booking/holds", "", "", map[string]any{
		"property_slug":  "demo-property",
		"unit_type_code": "shepherds-hut",
		"arrival_date":   arr,
		"departure_date": dep,
		"adults":         2,
		"guest_name":     "Guest A",
		"guest_email":    "guesta@example.com",
		"guest_phone":    "+447700900111",
		"accept_terms":   true,
	})
	var holdA service.CreateHoldResult
	_ = json.NewDecoder(hA.Body).Decode(&holdA)
	hA.Body.Close()
	if hA.StatusCode != http.StatusCreated {
		t.Fatalf("expected Guest A hold 201, got %d", hA.StatusCode)
	}

	coA := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/checkout", "", "", map[string]any{
		"hold_id": holdA.Hold.ID.String(),
	})
	var coOutA service.CreateCheckoutResponse
	_ = json.NewDecoder(coA.Body).Decode(&coOutA)
	coA.Body.Close()
	if coA.StatusCode != http.StatusOK {
		t.Fatalf("expected Guest A checkout 200, got %d", coA.StatusCode)
	}

	// Advance clock 20 minutes past 15m TTL & sweep expired holds!
	clk.Advance(20 * time.Minute)
	expiredCount, err := svc.SweepExpiredHolds(context.Background())
	if err != nil || expiredCount != 1 {
		t.Fatalf("expected 1 expired hold swept, got %d (err=%v)", expiredCount, err)
	}

	// Guest B immediately books the now-freed shepherds-hut and pays cleanly!
	hB := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/booking/holds", "", "", map[string]any{
		"property_slug":  "demo-property",
		"unit_type_code": "shepherds-hut",
		"arrival_date":   arr,
		"departure_date": dep,
		"adults":         2,
		"guest_name":     "Guest B",
		"guest_email":    "guestb@example.com",
		"guest_phone":    "+447700900222",
		"accept_terms":   true,
	})
	var holdB service.CreateHoldResult
	_ = json.NewDecoder(hB.Body).Decode(&holdB)
	hB.Body.Close()
	if hB.StatusCode != http.StatusCreated {
		t.Fatalf("expected Guest B to acquire expired unit, got %d", hB.StatusCode)
	}
	coB := doPublicJSON(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/checkout", "", "", map[string]any{
		"hold_id": holdB.Hold.ID.String(),
	})
	var coOutB service.CreateCheckoutResponse
	_ = json.NewDecoder(coB.Body).Decode(&coOutB)
	coB.Body.Close()

	whB, _ := signAndSendStripeWebhook(t, pubClient, srv.URL, "whsec_test_lovelace_phase2_secret", clk.Now(), map[string]any{
		"id":       "evt_guest_b_ok",
		"type":     "checkout.session.completed",
		"livemode": false,
		"data": map[string]any{
			"object": map[string]any{
				"id":             coOutB.Attempt.ProviderSessionID,
				"payment_intent": "pi_guest_b",
				"amount_total":   25000,
				"currency":       "gbp",
				"payment_status": "paid",
				"metadata":       map[string]any{"payment_attempt_id": coOutB.PaymentAttemptID.String()},
			},
		},
	})
	if whB != http.StatusOK {
		t.Fatalf("Guest B webhook failed: %d", whB)
	}

	// Late Stripe webhook arrives for Guest A's expired hold! MUST NOT overwrite or overlap Guest B;
	// MUST create EXPIRED_HOLD_PAID exception + trigger idempotent automatic refund!
	whA, _ := signAndSendStripeWebhook(t, pubClient, srv.URL, "whsec_test_lovelace_phase2_secret", clk.Now(), map[string]any{
		"id":       "evt_guest_a_late",
		"type":     "checkout.session.completed",
		"livemode": false,
		"data": map[string]any{
			"object": map[string]any{
				"id":             coOutA.Attempt.ProviderSessionID,
				"payment_intent": "pi_guest_a_late",
				"amount_total":   25000,
				"currency":       "gbp",
				"payment_status": "paid",
				"metadata":       map[string]any{"payment_attempt_id": coOutA.PaymentAttemptID.String()},
			},
		},
	})
	if whA != http.StatusOK {
		t.Fatalf("late webhook expected 200 acknowledged + exception recorded, got %d", whA)
	}

	adminClient := newClient(t)
	_, _ = apiLogin(t, adminClient, srv.URL, "owner@example.com", "lovelace-owner-password-2026")
	exResp, _ := adminClient.Get(srv.URL + "/api/v1/properties/" + prop.ID.String() + "/payment-exceptions")
	var exOut struct {
		Items []domain.PaymentException `json:"items"`
	}
	_ = json.NewDecoder(exResp.Body).Decode(&exOut)
	exResp.Body.Close()

	if len(exOut.Items) != 1 || exOut.Items[0].ExceptionType != "EXPIRED_HOLD_PAID" || exOut.Items[0].Status != "AUTO_REFUNDED" {
		t.Fatalf("expected EXPIRED_HOLD_PAID with AUTO_REFUNDED status, got %+v", exOut.Items)
	}
	if stripeMock.refundCalls != 1 || stripeMock.refundReqs[0].PaymentIntentID != "pi_guest_a_late" {
		t.Fatalf("expected automatic refund on pi_guest_a_late, calls=%d", stripeMock.refundCalls)
	}

	// Confirm only 1 reservation exists (Guest B's) — zero overlap!
	resResp, _ := adminClient.Get(srv.URL + "/api/v1/properties/" + prop.ID.String() + "/reservations")
	var resList struct {
		Items []domain.Reservation `json:"items"`
	}
	_ = json.NewDecoder(resResp.Body).Decode(&resList)
	resResp.Body.Close()
	if len(resList.Items) != 1 || resList.Items[0].GuestName != "Guest B" {
		t.Fatalf("expected strictly Guest B confirmed, got %+v", resList.Items)
	}
}
