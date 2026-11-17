package app_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/app"
	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/mailer"
	"github.com/tirbach/tir-bach/internal/seed"
	"github.com/tirbach/tir-bach/internal/service"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

func setupPortalTestServer(
	t *testing.T,
	publicSecret string,
	globalPortalEnabled bool,
) (*httptest.Server, *postgres.Store, *service.Service, domain.Property, *fakeStripeProvider, *fakeMailSender, *testClock) {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Phase 3 Milestone 4 PostgreSQL integration test")
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
		if _, txErr := tx.Exec(ctx, `SET LOCAL tirbach.test_reset_token = 'allow_test_reset'`); txErr != nil {
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
		AdminEmail:    "owner@tirbach.wales",
		AdminName:     "Sian Evans",
		AdminPassword: "tirbach-owner-password-2026",
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
		GuestPortalEnabled:  globalPortalEnabled,
		StripeWebhookSecret: "whsec_test_lovelace_phase3_m4_secret",
		EmailFrom:           "bookings@tirbach.wales",
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

func doPortalReqWithOrigin(
	t *testing.T,
	client *http.Client,
	method, url, publicSecret, sessionToken, csrfToken, clientIP, origin string,
	body any,
) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal portal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new portal request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if publicSecret != "" {
		req.Header.Set("X-Lovelace-Public-Key", publicSecret)
	}
	if sessionToken != "" {
		req.Header.Set("X-Guest-Session", sessionToken)
	}
	if csrfToken != "" {
		req.Header.Set("X-Guest-CSRF", csrfToken)
	}
	if clientIP != "" {
		req.Header.Set("X-Lovelace-Client-IP", clientIP)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do portal request %s %s: %v", method, url, err)
	}
	respBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, respBytes
}

func doPortalReq(
	t *testing.T,
	client *http.Client,
	method, url, publicSecret, sessionToken, csrfToken, clientIP string,
	body any,
) (*http.Response, []byte) {
	t.Helper()
	return doPortalReqWithOrigin(t, client, method, url, publicSecret, sessionToken, csrfToken, clientIP, "http://127.0.0.1:3000", body)
}

var (
	reExtractOTP       = regexp.MustCompile(`\b(\d{6})\b`)
	reExtractMagicHash = regexp.MustCompile(`#t=([0-9a-f]{64})`)
)

func latestOutboxBodyForRecipient(t *testing.T, store *postgres.Store, recipient string) string {
	t.Helper()
	var body string
	err := store.Pool().QueryRow(context.Background(), `
		SELECT body_text
		FROM email_outbox
		WHERE LOWER(recipient_email) = LOWER($1)
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, recipient).Scan(&body)
	if err != nil {
		t.Fatalf("latest outbox body for %s: %v", recipient, err)
	}
	return body
}

func TestPhase3Milestone4_GlobalDisabledByDefault(t *testing.T) {
	const pubSecret = "pub-portal-global-off"
	srv, store, svc, prop, _, _, _ := setupPortalTestServer(t, pubSecret, false)

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{
		UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role,
	}

	pol, err := svc.GetBookingPolicy(ctx, ownerActor, prop.ID)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	if pol.GuestPortalEnabled {
		t.Fatal("expected property guest_portal_enabled=false by default")
	}
	pol.GuestPortalEnabled = true
	if _, err := svc.UpdateBookingPolicy(ctx, ownerActor, pol); err != nil {
		t.Fatalf("update booking policy: %v", err)
	}

	pubClient := newClient(t)
	resp, _ := doPortalReq(t, pubClient, http.MethodPost, srv.URL+"/api/public/v1/properties/"+prop.Slug+"/portal/auth/challenge", pubSecret, "", "", "203.0.113.1", map[string]any{
		"email": "any@example.wales",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 not_found when global portal flag is disabled, got %d", resp.StatusCode)
	}

	propResp, propBody := doPortalReq(t, pubClient, http.MethodGet, srv.URL+"/api/public/v1/properties/"+prop.Slug, pubSecret, "", "", "203.0.113.1", nil)
	if propResp.StatusCode != http.StatusOK || strings.Contains(string(propBody), `"guest_portal_enabled":true`) {
		t.Fatalf("expected public property projection to report guest_portal_enabled=false when global flag is off, got %d: %s", propResp.StatusCode, string(propBody))
	}
}

func TestPhase3Milestone4_SelfServiceGuestPortalEndToEnd(t *testing.T) {
	const pubSecret = "pub-portal-m4-secret"
	srv, store, svc, prop, _, mailMock, clk := setupPortalTestServer(t, pubSecret, true)

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{
		UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role,
	}

	pubClient := newClient(t)
	portalBase := srv.URL + "/api/public/v1/properties/" + prop.Slug + "/portal"

	// 1. Property dark-rollout default: guest_portal_enabled defaults to FALSE and returns uniform 404 not_found.
	pol, err := svc.GetBookingPolicy(ctx, ownerActor, prop.ID)
	if err != nil || pol.GuestPortalEnabled {
		t.Fatalf("expected default guest_portal_enabled=false: err=%v enabled=%v", err, pol.GuestPortalEnabled)
	}
	disabledResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/challenge", pubSecret, "", "", "203.0.113.10", map[string]any{
		"email": "sian@example.wales",
	})
	if disabledResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected uniform 404 not_found when property flag is off, got %d", disabledResp.StatusCode)
	}

	// Enable controlled pilot for tir-bach while keeping a second property disabled.
	pol.GuestPortalEnabled = true
	pol, err = svc.UpdateBookingPolicy(ctx, ownerActor, pol)
	if err != nil || !pol.GuestPortalEnabled {
		t.Fatalf("enable guest_portal_enabled on pilot property: %v", err)
	}

	// 2. Boundary & Session Disjointness: Admin tirbach_session NEVER authenticates portal routes,
	// query-string tokens are forbidden, cross-origin mutations are rejected, and missing X-Lovelace-Public-Key fails 401.
	adminClient := newClient(t)
	loginStatus, _ := apiLogin(t, adminClient, srv.URL, "owner@tirbach.wales", "tirbach-owner-password-2026")
	if loginStatus != http.StatusOK {
		t.Fatalf("admin login status=%d", loginStatus)
	}
	crossResp, _ := doPortalReq(t, adminClient, http.MethodGet, portalBase+"/bookings", pubSecret, "", "", "203.0.113.11", nil)
	if crossResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected admin session cookie to fail closed (401) on portal routes, got %d", crossResp.StatusCode)
	}
	qTokResp, qTokBody := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/bookings?token=secret123", pubSecret, "", "", "203.0.113.11", nil)
	if qTokResp.StatusCode != http.StatusBadRequest || !strings.Contains(string(qTokBody), "query_token_forbidden") {
		t.Fatalf("expected 400 query_token_forbidden, got %d: %s", qTokResp.StatusCode, string(qTokBody))
	}
	badOriginResp, badOriginBody := doPortalReqWithOrigin(t, pubClient, http.MethodPost, portalBase+"/auth/challenge", pubSecret, "", "", "203.0.113.11", "https://evil.example", map[string]any{
		"email": "sian@example.wales",
	})
	if badOriginResp.StatusCode != http.StatusForbidden || !strings.Contains(string(badOriginBody), "origin_not_allowed") {
		t.Fatalf("expected 403 origin_not_allowed on cross-origin mutation, got %d: %s", badOriginResp.StatusCode, string(badOriginBody))
	}
	noKeyResp, _ := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/bookings", "", "anything", "", "203.0.113.11", nil)
	if noKeyResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without X-Lovelace-Public-Key, got %d", noKeyResp.StatusCode)
	}

	// Seed guest + two bookings (one DIRECT confirmed, one AIRBNB OTA confirmed) + internal notes & internal enquiry message.
	units, err := svc.ListUnits(ctx, ownerActor, prop.ID, nil)
	if err != nil || len(units) < 2 {
		t.Fatalf("list units: %v", err)
	}
	arr1, _ := domain.ParseDate(clk.Now().AddDate(0, 0, 20).Format("2006-01-02"))
	dep1, _ := domain.ParseDate(clk.Now().AddDate(0, 0, 23).Format("2006-01-02"))
	directRes, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Nia Griffiths",
		GuestEmail:    "nia.portal@example.wales",
		GuestPhone:    "07700900111",
		ChannelCode:   "DIRECT",
		ArrivalDate:   arr1,
		DepartureDate: dep1,
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{units[0].ID},
		InternalNotes: "TOP_SECRET_STAFF_RESERVATION_NOTE",
	})
	if err != nil {
		t.Fatalf("create directRes: %v", err)
	}

	plainConfTok, hashConfTok, _ := postgres.GenerateConfirmationToken()
	tokID, _ := uuid.NewV7()
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO confirmation_access_tokens (id, property_id, reservation_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, tokID, prop.ID, directRes.ID, hashConfTok, dep1.AddDate(0, 0, 30)); err != nil {
		t.Fatalf("insert confirmation token: %v", err)
	}

	_, _ = svc.CreateBookingChannel(ctx, ownerActor, domain.BookingChannel{
		PropertyID: prop.ID, Code: "AIRBNB", Name: "Airbnb", Category: "ota", IsActive: true, SortOrder: 10,
	})
	arr2, _ := domain.ParseDate(clk.Now().AddDate(0, 0, 35).Format("2006-01-02"))
	dep2, _ := domain.ParseDate(clk.Now().AddDate(0, 0, 38).Format("2006-01-02"))
	otaRes, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Nia Griffiths",
		GuestEmail:    "nia.portal@example.wales",
		GuestPhone:    "07700900111",
		ChannelCode:   "AIRBNB",
		ArrivalDate:   arr2,
		DepartureDate: dep2,
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{units[1].ID},
		TotalPence:    45000,
		InternalNotes: "OTA_INTERNAL_SECRET_NOTE",
	})
	if err != nil {
		t.Fatalf("create otaRes: %v", err)
	}

	// Add sensitive internal GuestNote, structured preference with internal note, and Enquiry with INTERNAL + staff OUTBOUND message.
	if _, err := svc.AddGuestNote(ctx, ownerActor, domain.GuestNote{
		PropertyID: prop.ID, GuestID: directRes.GuestID, Category: domain.NoteCatOperational,
		Content: "TOP_SECRET_CRM_GUEST_NOTE", IsPinned: true,
	}); err != nil {
		t.Fatalf("add guest note: %v", err)
	}
	if _, err := svc.AddGuestPreference(ctx, ownerActor, domain.GuestPreference{
		PropertyID: prop.ID, GuestID: directRes.GuestID, Category: domain.PrefCatArrival,
		PreferenceKey: "gate_access", PreferenceValue: "Late arrival", Notes: "STAFF_ONLY_PREF_NOTE",
	}); err != nil {
		t.Fatalf("add guest preference: %v", err)
	}
	seededEnq, err := svc.CreateGuestEnquiry(ctx, ownerActor, domain.GuestEnquiry{
		PropertyID: prop.ID, GuestID: &directRes.GuestID, ReservationID: &directRes.ID,
		Category: domain.EnqCatGeneral, Subject: "Firewood question", Description: "Can we buy logs on site?",
		RequesterName: "Nia Griffiths", RequesterEmail: "nia.portal@example.wales",
	})
	if err != nil {
		t.Fatalf("create seededEnq: %v", err)
	}
	if _, err := svc.AddGuestEnquiryMessage(ctx, ownerActor, domain.GuestEnquiryMessage{
		PropertyID: prop.ID, EnquiryID: seededEnq.ID, IsInternal: true,
		Direction: domain.CommDirInternal, Body: "INTERNAL_WHISPER_DO_NOT_LEAK",
	}); err != nil {
		t.Fatalf("add internal enquiry msg: %v", err)
	}
	if _, err := svc.AddGuestEnquiryMessage(ctx, ownerActor, domain.GuestEnquiryMessage{
		PropertyID: prop.ID, EnquiryID: seededEnq.ID, IsInternal: false,
		Direction: domain.CommDirOutbound, Body: "Yes, kiln-dried logs are available at reception!",
	}); err != nil {
		t.Fatalf("add outbound enquiry msg: %v", err)
	}

	// 3. Non-Enumeration: unknown email and OTA proxy email return identical 202 Accepted shape with zero outbox row queued.
	unkResp, unkBody := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/challenge", pubSecret, "", "", "203.0.113.20", map[string]any{
		"email": "nobody.unknown@example.wales",
	})
	proxyResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/challenge", pubSecret, "", "", "203.0.113.21", map[string]any{
		"email": "relay-12345@guest.booking.com",
	})
	if unkResp.StatusCode != http.StatusAccepted || proxyResp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected uniform 202 on unknown/proxy emails, got %d / %d (%s)", unkResp.StatusCode, proxyResp.StatusCode, string(unkBody))
	}
	var outboxNoise int
	_ = store.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM email_outbox WHERE recipient_email IN ('nobody.unknown@example.wales', 'relay-12345@guest.booking.com')`).Scan(&outboxNoise)
	if outboxNoise != 0 {
		t.Fatalf("expected 0 outbox emails for unknown/proxy addresses, got %d", outboxNoise)
	}

	// 4. Tier A (BOOKING-scoped) Session via Confirmation Token (D1): accesses ONLY directRes + booking-scoped enquiry, blocked from otaRes & profile/privacy!
	tierAResp, tierABody := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/booking-token", pubSecret, "", "", "203.0.113.25", map[string]any{
		"booking_reference":  directRes.Reference,
		"confirmation_token": plainConfTok,
	})
	if tierAResp.StatusCode != http.StatusOK {
		t.Fatalf("Tier A booking-token login failed (%d): %s", tierAResp.StatusCode, string(tierABody))
	}
	if !strings.Contains(tierAResp.Header.Get("Cache-Control"), "no-store") || tierAResp.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("missing no-store / no-referrer headers on portal auth response: %+v", tierAResp.Header)
	}
	var tierALogin service.PortalLoginResult
	_ = json.Unmarshal(tierABody, &tierALogin)
	if tierALogin.Session.ScopeType != domain.PortalScopeBooking || tierALogin.Session.StepUpFresh {
		t.Fatalf("expected BOOKING scope with StepUpFresh=false, got %+v", tierALogin.Session)
	}

	// Tier A cannot use its token on Admin /api/v1/*
	adminTryReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/auth/me", nil)
	adminTryReq.AddCookie(&http.Cookie{Name: "tirbach_session", Value: tierALogin.SessionToken})
	adminTryResp, err := pubClient.Do(adminTryReq)
	if err != nil {
		t.Fatalf("adminTryReq: %v", err)
	}
	adminTryResp.Body.Close()
	if adminTryResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected portal session token rejected on /api/v1/auth/me (401), got %d", adminTryResp.StatusCode)
	}

	taListResp, taListBody := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/bookings", pubSecret, tierALogin.SessionToken, "", "203.0.113.25", nil)
	if taListResp.StatusCode != http.StatusOK || strings.Contains(string(taListBody), otaRes.Reference) || !strings.Contains(string(taListBody), directRes.Reference) {
		t.Fatalf("Tier A booking list leaked sibling booking or missed scoped booking: %s", string(taListBody))
	}
	taOtherResp, _ := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/bookings/"+otaRes.Reference, pubSecret, tierALogin.SessionToken, "", "203.0.113.25", nil)
	if taOtherResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected Tier A 404 on sibling booking, got %d", taOtherResp.StatusCode)
	}
	taProfResp, _ := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/profile", pubSecret, tierALogin.SessionToken, "", "203.0.113.25", nil)
	if taProfResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected Tier A 403 on /portal/profile, got %d", taProfResp.StatusCode)
	}
	taEnqResp, taEnqBody := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/enquiries", pubSecret, tierALogin.SessionToken, "", "203.0.113.25", nil)
	if taEnqResp.StatusCode != http.StatusOK || !strings.Contains(string(taEnqBody), "Firewood question") {
		t.Fatalf("expected Tier A to see booking-linked enquiry, got %d: %s", taEnqResp.StatusCode, string(taEnqBody))
	}

	// 5. Tier B Browser-Bound Magic Link & Outbox Security Scrubbing (Section 7.1 #1 & Section 9.2).
	chResp, chBody := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/challenge", pubSecret, "", "", "203.0.113.30", map[string]any{
		"email": "nia.portal@example.wales",
	})
	if chResp.StatusCode != http.StatusAccepted {
		t.Fatalf("challenge status=%d: %s", chResp.StatusCode, string(chBody))
	}
	var chInit service.PortalChallengeInitiated
	_ = json.Unmarshal(chBody, &chInit)

	// Dispatch outbox via genuine worker and verify SMTP received code while email_outbox scrubbed body_text
	// and ZERO guest_communications rows were created for portal_verification!
	sentCount, err := svc.DispatchPendingEmails(ctx)
	if err != nil || sentCount != 1 {
		t.Fatalf("DispatchPendingEmails: count=%d err=%v", sentCount, err)
	}
	mailMock.mu.Lock()
	msgs := append([]mailer.Message(nil), mailMock.sent...)
	mailMock.mu.Unlock()
	if len(msgs) == 0 {
		t.Fatal("expected fakeMailSender to capture portal_verification message")
	}
	deliveredBody := msgs[len(msgs)-1].BodyText
	magicMatch := reExtractMagicHash.FindStringSubmatch(deliveredBody)
	if len(magicMatch) < 2 {
		t.Fatalf("expected #t=<magic_token> in dispatched email, got: %s", deliveredBody)
	}
	magicToken := magicMatch[1]

	scrubbedBody := latestOutboxBodyForRecipient(t, store, "nia.portal@example.wales")
	if scrubbedBody != "[redacted security challenge]" {
		t.Fatalf("expected email_outbox body_text scrubbed to '[redacted security challenge]' upon SENT, got %q", scrubbedBody)
	}
	var challengeCommCount int
	_ = store.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM guest_communications WHERE subject LIKE '%Verification Code%'`).Scan(&challengeCommCount)
	if challengeCommCount != 0 {
		t.Fatalf("CRITICAL: expected 0 guest_communications rows for portal verification challenge, got %d", challengeCommCount)
	}

	// Attempting to redeem magic_token WITHOUT matching browser_nonce (forwarded link / scanner) fails 401!
	fwdResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/verify", pubSecret, "", "", "203.0.113.31", map[string]any{
		"magic_token":   magicToken,
		"browser_nonce": "wrong-or-missing-browser-nonce",
	})
	if fwdResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 when redeeming magic_token without browser_nonce, got %d", fwdResp.StatusCode)
	}

	// Redeeming magic_token WITH matching browser_nonce succeeds once, and replay fails 401!
	verResp, verBody := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/verify", pubSecret, "", "", "203.0.113.30", map[string]any{
		"magic_token":   magicToken,
		"browser_nonce": chInit.BrowserNonce,
	})
	if verResp.StatusCode != http.StatusOK {
		t.Fatalf("magic_token + browser_nonce verify failed (%d): %s", verResp.StatusCode, string(verBody))
	}
	var tierB1 service.PortalLoginResult
	_ = json.Unmarshal(verBody, &tierB1)
	if tierB1.Session.ScopeType != domain.PortalScopeAccount || tierB1.Session.CSRFToken == "" {
		t.Fatalf("unexpected Tier B session view: %+v", tierB1.Session)
	}
	replayResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/verify", pubSecret, "", "", "203.0.113.30", map[string]any{
		"magic_token":   magicToken,
		"browser_nonce": chInit.BrowserNonce,
	})
	if replayResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected single-use magic_token replay to fail 401, got %d", replayResp.StatusCode)
	}

	// Section 7.2 #3: Verify guest_portal_sessions has zero IP/UA hash columns and portal audit_logs collect no IP/UA!
	var portalAuditIPOrUACount int
	_ = store.Pool().QueryRow(ctx, `
		SELECT COUNT(*) FROM audit_logs_effective
		WHERE actor_email = 'guest@public.lovelace'
		  AND (COALESCE(ip_address, '') <> '' OR COALESCE(user_agent, '') <> '')
	`).Scan(&portalAuditIPOrUACount)
	if portalAuditIPOrUACount != 0 {
		t.Fatalf("expected 0 portal audit entries storing IP or user-agent per Section 7.2 #3, got %d", portalAuditIPOrUACount)
	}

	// 6. Strict Privacy Hygiene: Zero internal notes, INTERNAL messages, OTA financials, or staff personal names!
	_, bListRaw := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/bookings", pubSecret, tierB1.SessionToken, "", "203.0.113.30", nil)
	_, bDetRaw := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/bookings/"+directRes.Reference, pubSecret, tierB1.SessionToken, "", "203.0.113.30", nil)
	_, otaDetRaw := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/bookings/"+otaRes.Reference, pubSecret, tierB1.SessionToken, "", "203.0.113.30", nil)
	_, profRaw := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/profile", pubSecret, tierB1.SessionToken, "", "203.0.113.30", nil)
	_, enqRaw := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/enquiries", pubSecret, tierB1.SessionToken, "", "203.0.113.30", nil)

	combinedPayloads := string(bListRaw) + "\n" + string(bDetRaw) + "\n" + string(otaDetRaw) + "\n" + string(profRaw) + "\n" + string(enqRaw)
	for _, forbidden := range []string{
		"TOP_SECRET_STAFF_RESERVATION_NOTE",
		"OTA_INTERNAL_SECRET_NOTE",
		"TOP_SECRET_CRM_GUEST_NOTE",
		"STAFF_ONLY_PREF_NOTE",
		"INTERNAL_WHISPER_DO_NOT_LEAK",
		"Sian Evans",
		"owner@tirbach.wales",
		"45000", // OTA total_pence must not be exposed as a property invoice!
	} {
		if strings.Contains(combinedPayloads, forbidden) {
			t.Fatalf("CRITICAL PRIVACY LEAK: portal payload contained forbidden string %q:\n%s", forbidden, combinedPayloads)
		}
	}
	var otaDetDecoded struct {
		Booking domain.PortalBookingDetail `json:"booking"`
	}
	if err := json.Unmarshal(otaDetRaw, &otaDetDecoded); err != nil {
		t.Fatalf("unmarshal OTA booking detail: %v", err)
	}
	if !strings.Contains(otaDetDecoded.Booking.BillingNotice, "Managed & billed via AIRBNB") || otaDetDecoded.Booking.Quote != nil || len(otaDetDecoded.Booking.Payments) != 0 {
		t.Fatalf("expected OTA billing notice without property quote/payments on OTA booking detail, got: %s", string(otaDetRaw))
	}
	if !strings.Contains(string(enqRaw), "Reservations Team") || !strings.Contains(string(enqRaw), "kiln-dried logs") {
		t.Fatalf("expected guest-visible outbound message attributed to 'Reservations Team', got: %s", string(enqRaw))
	}

	// 7. CSRF enforcement, Arrival Update, OTA Read-Only Protection (D4), Max-3 Change Cap, & Request-Only Cancel (D5).
	noCSRFResp, noCSRFBody := doPortalReq(t, pubClient, http.MethodPatch, portalBase+"/bookings/"+directRes.Reference+"/arrival", pubSecret, tierB1.SessionToken, "", "203.0.113.30", map[string]any{
		"estimated_arrival_time": "16:30",
		"guest_arrival_notes":    "Arriving by EV, 1 dog",
	})
	if noCSRFResp.StatusCode != http.StatusForbidden || !strings.Contains(string(noCSRFBody), "csrf_invalid") {
		t.Fatalf("expected 403 csrf_invalid without X-Guest-CSRF, got %d: %s", noCSRFResp.StatusCode, string(noCSRFBody))
	}

	arrUpdResp, arrUpdBody := doPortalReq(t, pubClient, http.MethodPatch, portalBase+"/bookings/"+directRes.Reference+"/arrival", pubSecret, tierB1.SessionToken, tierB1.Session.CSRFToken, "203.0.113.30", map[string]any{
		"estimated_arrival_time": "16:30",
		"guest_arrival_notes":    "Arriving by EV, 1 dog",
	})
	if arrUpdResp.StatusCode != http.StatusOK || !strings.Contains(string(arrUpdBody), "16:30") {
		t.Fatalf("update arrival failed (%d): %s", arrUpdResp.StatusCode, string(arrUpdBody))
	}

	for _, otaEndpoint := range []struct {
		method string
		path   string
		body   map[string]any
	}{
		{http.MethodPatch, "/bookings/" + otaRes.Reference + "/arrival", map[string]any{"estimated_arrival_time": "17:00"}},
		{http.MethodPost, "/bookings/" + otaRes.Reference + "/change-request", map[string]any{"notes": "Change OTA dates"}},
		{http.MethodPost, "/bookings/" + otaRes.Reference + "/cancel-request", map[string]any{"note": "Cancel OTA stay"}},
	} {
		otaMutResp, otaMutBody := doPortalReq(t, pubClient, otaEndpoint.method, portalBase+otaEndpoint.path, pubSecret, tierB1.SessionToken, tierB1.Session.CSRFToken, "203.0.113.30", otaEndpoint.body)
		if otaMutResp.StatusCode != http.StatusConflict || !strings.Contains(string(otaMutBody), "ota_managed_booking") {
			t.Fatalf("expected 409 ota_managed_booking on %s %s, got %d: %s", otaEndpoint.method, otaEndpoint.path, otaMutResp.StatusCode, string(otaMutBody))
		}
	}

	// Enforce max 3 open change requests per reservation (Section 6.1 Row 4).
	for i := 1; i <= 3; i++ {
		chgResp, chgBody := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/bookings/"+directRes.Reference+"/change-request", pubSecret, tierB1.SessionToken, tierB1.Session.CSRFToken, "203.0.113.30", map[string]any{
			"requested_arrival":   arr1.AddDate(0, 0, i).Format("2006-01-02"),
			"requested_departure": dep1.AddDate(0, 0, i).Format("2006-01-02"),
			"requested_adults":    2,
			"notes":               fmt.Sprintf("Change request #%d", i),
		})
		if chgResp.StatusCode != http.StatusCreated {
			t.Fatalf("change-request #%d failed (%d): %s", i, chgResp.StatusCode, string(chgBody))
		}
	}
	chg4Resp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/bookings/"+directRes.Reference+"/change-request", pubSecret, tierB1.SessionToken, tierB1.Session.CSRFToken, "203.0.113.30", map[string]any{
		"notes": "4th open change request exceeding cap",
	})
	if chg4Resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 4th open change request rejected with 409, got %d", chg4Resp.StatusCode)
	}

	// Request-only cancellation (D5): sets cancellation_requested_at WITHOUT releasing inventory_claims!
	canResp, canBody := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/bookings/"+directRes.Reference+"/cancel-request", pubSecret, tierB1.SessionToken, tierB1.Session.CSRFToken, "203.0.113.30", map[string]any{
		"note": "Unexpected work commitment",
	})
	if canResp.StatusCode != http.StatusOK || !strings.Contains(string(canBody), `"cancellation_requested":true`) {
		t.Fatalf("cancel-request failed (%d): %s", canResp.StatusCode, string(canBody))
	}
	var activeClaimCount int
	_ = store.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM inventory_claims WHERE reservation_id = $1 AND status = 'ACTIVE'`, directRes.ID).Scan(&activeClaimCount)
	if activeClaimCount != 1 {
		t.Fatalf("expected inventory_claims to remain ACTIVE on request-only cancellation, got %d", activeClaimCount)
	}

	// Self-service profile & structured preferences update (D10 Welsh preferred_language + preserving staff notes).
	profUpdResp, profUpdBody := doPortalReq(t, pubClient, http.MethodPatch, portalBase+"/profile", pubSecret, tierB1.SessionToken, tierB1.Session.CSRFToken, "203.0.113.30", map[string]any{
		"phone":                    "+447700900999",
		"preferred_contact_method": "EMAIL",
		"preferred_language":       "cy",
		"preferences": []map[string]string{
			{"category": "ARRIVAL", "preference_key": "gate_access", "preference_value": "Arriving 16:30"},
			{"category": "PET", "preference_key": "dogs", "preference_value": "1 border collie"},
		},
	})
	if profUpdResp.StatusCode != http.StatusOK || !strings.Contains(string(profUpdBody), `"preferred_language":"cy"`) || !strings.Contains(string(profUpdBody), "border collie") || strings.Contains(string(profUpdBody), "STAFF_ONLY_PREF_NOTE") {
		t.Fatalf("PATCH /profile with preferences failed (%d): %s", profUpdResp.StatusCode, string(profUpdBody))
	}
	var preservedStaffPrefNote string
	_ = store.Pool().QueryRow(ctx, `SELECT notes FROM guest_preferences WHERE property_id = $1 AND guest_id = $2 AND preference_key = 'gate_access'`, prop.ID, directRes.GuestID).Scan(&preservedStaffPrefNote)
	if preservedStaffPrefNote != "STAFF_ONLY_PREF_NOTE" {
		t.Fatalf("expected staff-authored preference note preserved intact, got %q", preservedStaffPrefNote)
	}

	// CLOSED enquiry rejects guest replies with 409 Conflict (Section 6.1 Row 7).
	curSeededEnq, _ := store.GetGuestEnquiry(ctx, nil, prop.ID, seededEnq.ID)
	curSeededEnq.Status = domain.EnqStatusClosed
	if _, err := svc.UpdateGuestEnquiry(ctx, ownerActor, curSeededEnq); err != nil {
		t.Fatalf("close seededEnq: %v", err)
	}
	closedReplyResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/enquiries/"+seededEnq.ID.String()+"/messages", pubSecret, tierB1.SessionToken, tierB1.Session.CSRFToken, "203.0.113.30", map[string]any{
		"message": "Replying to closed thread",
	})
	if closedReplyResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 when replying to CLOSED enquiry, got %d", closedReplyResp.StatusCode)
	}

	// 8. Step-Up Freshness Gate (D8), Section 7.2 SAR/Erasure Invisibility, & Email Change Dual Notification.
	_, _ = doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/challenge", pubSecret, "", "", "203.0.113.32", map[string]any{
		"email": "nia.portal@example.wales",
	})
	otpMatch := reExtractOTP.FindStringSubmatch(latestOutboxBodyForRecipient(t, store, "nia.portal@example.wales"))
	ver2Resp, ver2Body := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/verify", pubSecret, "", "", "203.0.113.32", map[string]any{
		"email": "nia.portal@example.wales",
		"code":  otpMatch[1],
	})
	if ver2Resp.StatusCode != http.StatusOK {
		t.Fatalf("second Tier B login failed (%d): %s", ver2Resp.StatusCode, string(ver2Body))
	}
	var tierB2 service.PortalLoginResult
	_ = json.Unmarshal(ver2Body, &tierB2)

	// Advance clock by 20 minutes (>15m step-up freshness, <60m idle TTL).
	clk.Advance(20 * time.Minute)
	staleStepResp, staleStepBody := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/privacy/sar", pubSecret, tierB2.SessionToken, tierB2.Session.CSRFToken, "203.0.113.32", map[string]any{
		"notes": "My data copy please",
	})
	if staleStepResp.StatusCode != http.StatusForbidden || !strings.Contains(string(staleStepBody), "step_up_required") {
		t.Fatalf("expected 403 step_up_required after 20m, got %d: %s", staleStepResp.StatusCode, string(staleStepBody))
	}

	// Complete step-up OTP challenge on tierB2.
	suChResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/step-up/challenge", pubSecret, tierB2.SessionToken, tierB2.Session.CSRFToken, "203.0.113.32", map[string]any{})
	if suChResp.StatusCode != http.StatusAccepted {
		t.Fatalf("step-up challenge failed: %d", suChResp.StatusCode)
	}
	suOTP := reExtractOTP.FindStringSubmatch(latestOutboxBodyForRecipient(t, store, "nia.portal@example.wales"))[1]
	suVerResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/step-up/verify", pubSecret, tierB2.SessionToken, tierB2.Session.CSRFToken, "203.0.113.32", map[string]any{
		"code": suOTP,
	})
	if suVerResp.StatusCode != http.StatusOK {
		t.Fatalf("step-up verify failed: %d", suVerResp.StatusCode)
	}

	// Section 7.2: SAR returns uniform 202 receipt (no enquiry ID/status/details!), deduplicates silently,
	// and NEVER appears in ListPortalEnquiries!
	sarResp1, sarBody1 := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/privacy/sar", pubSecret, tierB2.SessionToken, tierB2.Session.CSRFToken, "203.0.113.32", map[string]any{
		"notes": "PRIVATE_SAR_SCOPE_TEXT",
	})
	sarResp2, sarBody2 := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/privacy/sar", pubSecret, tierB2.SessionToken, tierB2.Session.CSRFToken, "203.0.113.32", map[string]any{
		"notes": "Duplicate SAR attempt",
	})
	if sarResp1.StatusCode != http.StatusAccepted || sarResp2.StatusCode != http.StatusAccepted || string(sarBody1) != string(sarBody2) {
		t.Fatalf("expected identical 202 uniform SAR receipt on initial and duplicate requests, got %d (%s) vs %d (%s)", sarResp1.StatusCode, string(sarBody1), sarResp2.StatusCode, string(sarBody2))
	}
	if strings.Contains(string(sarBody1), "ENQ-") || strings.Contains(string(sarBody1), "PRIVATE_SAR_SCOPE_TEXT") {
		t.Fatalf("SAR response leaked enquiry details/status in violation of Section 7.2: %s", string(sarBody1))
	}
	var sarDBCount int
	var sarEnqID uuid.UUID
	_ = store.Pool().QueryRow(ctx, `SELECT COUNT(*), MAX(id::text)::uuid FROM guest_enquiries WHERE property_id = $1 AND subject LIKE $2 || '%'`, prop.ID, domain.PortalSARSubjectPrefix).Scan(&sarDBCount, &sarEnqID)
	if sarDBCount != 1 {
		t.Fatalf("expected exactly 1 open SAR enquiry recorded for staff review, got %d", sarDBCount)
	}
	_, enqAfterSAR := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/enquiries", pubSecret, tierB2.SessionToken, "", "203.0.113.32", nil)
	if strings.Contains(string(enqAfterSAR), "PRIVATE_SAR_SCOPE_TEXT") || strings.Contains(string(enqAfterSAR), domain.PortalSARSubjectPrefix) {
		t.Fatalf("CRITICAL Section 7.2 violation: ListPortalEnquiries revealed SAR request details: %s", string(enqAfterSAR))
	}
	sarReplyProbe, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/enquiries/"+sarEnqID.String()+"/messages", pubSecret, tierB2.SessionToken, tierB2.Session.CSRFToken, "203.0.113.32", map[string]any{
		"message": "Probe SAR status",
	})
	if sarReplyProbe.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 when probing SAR enquiry ID via /enquiries/{id}/messages, got %d", sarReplyProbe.StatusCode)
	}

	// Email change: nia.portal@example.wales -> nia.new@example.wales; verify alert to OLD email and revocation of tierB1!
	emInitResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/profile/email-change", pubSecret, tierB2.SessionToken, tierB2.Session.CSRFToken, "203.0.113.32", map[string]any{
		"new_email": "nia.new@example.wales",
	})
	if emInitResp.StatusCode != http.StatusAccepted {
		t.Fatalf("email-change init status=%d", emInitResp.StatusCode)
	}
	newEmailOTP := reExtractOTP.FindStringSubmatch(latestOutboxBodyForRecipient(t, store, "nia.new@example.wales"))[1]
	emConfResp, emConfBody := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/profile/email-change/confirm", pubSecret, tierB2.SessionToken, tierB2.Session.CSRFToken, "203.0.113.32", map[string]any{
		"code": newEmailOTP,
	})
	if emConfResp.StatusCode != http.StatusOK || !strings.Contains(string(emConfBody), "nia.new@example.wales") {
		t.Fatalf("email-change confirm failed (%d): %s", emConfResp.StatusCode, string(emConfBody))
	}
	oldAlertBody := latestOutboxBodyForRecipient(t, store, "nia.portal@example.wales")
	if !strings.Contains(oldAlertBody, "revoked") {
		t.Fatalf("expected security alert queued to old email address, got: %s", oldAlertBody)
	}
	b1Check, _ := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/auth/session", pubSecret, tierB1.SessionToken, "", "203.0.113.30", nil)
	b2Check, _ := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/auth/session", pubSecret, tierB2.SessionToken, "", "203.0.113.32", nil)
	if b1Check.StatusCode != http.StatusUnauthorized || b2Check.StatusCode != http.StatusOK {
		t.Fatalf("expected tierB1=401 and tierB2=200 after email change, got %d and %d", b1Check.StatusCode, b2Check.StatusCode)
	}

	// 9. Duplicate Shared Email Disambiguation (D3) & MergeGuests Continuity.
	dupA, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Owen", LastName: "Shared", Email: "family.shared@example.wales",
	})
	if err != nil {
		t.Fatalf("create dupA: %v", err)
	}
	dupB, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Ceri", LastName: "Shared", Email: "family.shared@example.wales",
	})
	if err != nil {
		t.Fatalf("create dupB: %v", err)
	}
	arr3, _ := domain.ParseDate(clk.Now().AddDate(0, 0, 50).Format("2006-01-02"))
	dep3, _ := domain.ParseDate(clk.Now().AddDate(0, 0, 52).Format("2006-01-02"))
	dupResA, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID: prop.ID, GuestName: "Owen Shared", GuestEmail: "family.shared@example.wales",
		ChannelCode: "DIRECT", ArrivalDate: arr3, DepartureDate: dep3, Adults: 2,
		Status: domain.ResStatusConfirmed, UnitIDs: []uuid.UUID{units[0].ID},
	})
	if err != nil {
		t.Fatalf("create dupResA: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE reservations SET guest_id = $2 WHERE id = $1`, dupResA.ID, dupA.ID); err != nil {
		t.Fatalf("repoint dupResA: %v", err)
	}
	arr4, _ := domain.ParseDate(clk.Now().AddDate(0, 0, 55).Format("2006-01-02"))
	dep4, _ := domain.ParseDate(clk.Now().AddDate(0, 0, 57).Format("2006-01-02"))
	dupResB, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID: prop.ID, GuestName: "Ceri Shared", GuestEmail: "family.shared@example.wales",
		ChannelCode: "DIRECT", ArrivalDate: arr4, DepartureDate: dep4, Adults: 2,
		Status: domain.ResStatusConfirmed, UnitIDs: []uuid.UUID{units[1].ID},
	})
	if err != nil {
		t.Fatalf("create dupResB: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE reservations SET guest_id = $2 WHERE id = $1`, dupResB.ID, dupB.ID); err != nil {
		t.Fatalf("repoint dupResB: %v", err)
	}

	_, _ = doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/challenge", pubSecret, "", "", "203.0.113.40", map[string]any{
		"email": "family.shared@example.wales",
	})
	dupOTP := reExtractOTP.FindStringSubmatch(latestOutboxBodyForRecipient(t, store, "family.shared@example.wales"))[1]

	dupAmbResp, dupAmbBody := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/verify", pubSecret, "", "", "203.0.113.40", map[string]any{
		"email": "family.shared@example.wales",
		"code":  dupOTP,
	})
	if dupAmbResp.StatusCode != http.StatusConflict || !strings.Contains(string(dupAmbBody), "duplicate_guest_verification_required") {
		t.Fatalf("expected 409 duplicate_guest_verification_required, got %d: %s", dupAmbResp.StatusCode, string(dupAmbBody))
	}

	dupVerResp, dupVerBody := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/verify", pubSecret, "", "", "203.0.113.40", map[string]any{
		"email":             "family.shared@example.wales",
		"code":              dupOTP,
		"booking_reference": dupResB.Reference,
	})
	if dupVerResp.StatusCode != http.StatusOK {
		t.Fatalf("disambiguated verify failed (%d): %s", dupVerResp.StatusCode, string(dupVerBody))
	}
	var dupLogin service.PortalLoginResult
	_ = json.Unmarshal(dupVerBody, &dupLogin)

	// Before CRM merge, dupB's portal session must NOT expose dupResA!
	_, dupPreMergeList := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/bookings", pubSecret, dupLogin.SessionToken, "", "203.0.113.40", nil)
	if strings.Contains(string(dupPreMergeList), dupResA.Reference) {
		t.Fatalf("CRITICAL D3 leak: unmerged duplicate guest session saw sibling guest's booking %s", dupResA.Reference)
	}

	// Merge dupB (source, has portal identity) into dupA (target, same email) -> portal identity repoints seamlessly!
	if _, _, err := svc.MergeGuests(ctx, ownerActor, prop.ID, dupB.ID, dupA.ID, "Consolidate household duplicate"); err != nil {
		t.Fatalf("MergeGuests(dupB -> dupA) failed: %v", err)
	}
	postMergeResp, postMergeBody := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/bookings", pubSecret, dupLogin.SessionToken, "", "203.0.113.40", nil)
	if postMergeResp.StatusCode != http.StatusOK || !strings.Contains(string(postMergeBody), dupResB.Reference) || !strings.Contains(string(postMergeBody), dupResA.Reference) {
		t.Fatalf("expected merged guest portal session to show consolidated bookings, got %d: %s", postMergeResp.StatusCode, string(postMergeBody))
	}

	// 10. DO_NOT_CONTACT (D9), Cross-Property Isolation (D2), & AnonymizeGuest Lockout.
	optInResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/consent", pubSecret, tierB2.SessionToken, tierB2.Session.CSRFToken, "203.0.113.32", map[string]any{
		"status": "GRANTED",
	})
	if optInResp.StatusCode != http.StatusOK {
		t.Fatalf("consent GRANTED failed: %d", optInResp.StatusCode)
	}

	niaCurrent, _ := store.GetGuest(ctx, nil, prop.ID, directRes.GuestID)
	niaCurrent.Status = domain.GuestStatusDoNotContact
	if _, err := svc.UpdateGuest(ctx, ownerActor, niaCurrent); err != nil {
		t.Fatalf("set Nia DO_NOT_CONTACT: %v", err)
	}
	dncOptResp, dncOptBody := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/consent", pubSecret, tierB2.SessionToken, tierB2.Session.CSRFToken, "203.0.113.32", map[string]any{
		"status": "GRANTED",
	})
	if dncOptResp.StatusCode != http.StatusForbidden || !strings.Contains(string(dncOptBody), "do_not_contact_restricted") {
		t.Fatalf("expected 403 do_not_contact_restricted on marketing opt-in, got %d: %s", dncOptResp.StatusCode, string(dncOptBody))
	}
	dncEraseResp, dncEraseBody := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/privacy/erasure", pubSecret, tierB2.SessionToken, tierB2.Session.CSRFToken, "203.0.113.32", map[string]any{
		"reason": "Please remove my profile after my stay",
	})
	if dncEraseResp.StatusCode != http.StatusAccepted || strings.Contains(string(dncEraseBody), "ENQ-") {
		t.Fatalf("expected DO_NOT_CONTACT guest allowed to submit erasure request with uniform 202 receipt, got %d: %s", dncEraseResp.StatusCode, string(dncEraseBody))
	}

	// Cross-property isolation: Property B (disabled -> 404; enabled -> 401 on Property A session!).
	propBID, _ := uuid.NewV7()
	propB, err := store.CreateProperty(ctx, nil, domain.Property{
		ID: propBID, Slug: "portal-prop-b", Name: "Portal Property B", CountryCode: "GB",
		Timezone: "Europe/London", Currency: "GBP", ContactEmail: "b@tirbach.wales",
		CheckInTime: "15:00", CheckOutTime: "10:00",
	})
	if err != nil {
		t.Fatalf("create propB: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `INSERT INTO property_booking_policies (property_id) VALUES ($1) ON CONFLICT DO NOTHING`, propB.ID); err != nil {
		t.Fatalf("seed propB policy: %v", err)
	}
	propBDisabledResp, _ := doPortalReq(t, pubClient, http.MethodGet, srv.URL+"/api/public/v1/properties/portal-prop-b/portal/bookings", pubSecret, tierB2.SessionToken, "", "203.0.113.32", nil)
	if propBDisabledResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 on un-enabled Property B while Tir Bach pilot is active, got %d", propBDisabledResp.StatusCode)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE property_booking_policies SET guest_portal_enabled = TRUE WHERE property_id = $1`, propB.ID); err != nil {
		t.Fatalf("enable propB portal: %v", err)
	}

	crossPropResp, _ := doPortalReq(t, pubClient, http.MethodGet, srv.URL+"/api/public/v1/properties/portal-prop-b/portal/bookings", pubSecret, tierB2.SessionToken, "", "203.0.113.32", nil)
	if crossPropResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 using Property A session on Property B, got %d", crossPropResp.StatusCode)
	}

	// Support recovery service methods: SendGuestPortalVerification.
	if err := svc.SendGuestPortalVerification(ctx, ownerActor, prop.ID, directRes.GuestID); err != nil {
		t.Fatalf("SendGuestPortalVerification failed: %v", err)
	}

	// Cancel active reservations so we can exercise outbox DISPATCHING lease barrier + final AnonymizeGuest!
	curDirect, _ := store.GetReservation(ctx, nil, prop.ID, directRes.ID)
	if _, err := svc.TransitionReservationStatus(ctx, ownerActor, prop.ID, curDirect.ID, domain.ResStatusCancelled, "Approved customer cancellation", curDirect.Version); err != nil {
		t.Fatalf("cancel directRes: %v", err)
	}
	curOTA, _ := store.GetReservation(ctx, nil, prop.ID, otaRes.ID)
	if _, err := svc.TransitionReservationStatus(ctx, ownerActor, prop.ID, curOTA.ID, domain.ResStatusCancelled, "OTA cancelled", curOTA.Version); err != nil {
		t.Fatalf("cancel otaRes: %v", err)
	}

	// Simulate an active outbox worker DISPATCHING lease on Nia's latest queued email when AnonymizeGuest runs!
	var leasedOutboxID uuid.UUID
	workerTok, _ := uuid.NewV7()
	if err := store.Pool().QueryRow(ctx, `
		UPDATE email_outbox
		SET status = 'PROCESSING', locked_at = $2, worker_token = $3
		WHERE id = (
			SELECT id FROM email_outbox WHERE guest_id = $1 AND status = 'PENDING' ORDER BY created_at DESC LIMIT 1
		)
		RETURNING id
	`, directRes.GuestID, clk.Now(), workerTok).Scan(&leasedOutboxID); err != nil {
		t.Fatalf("claim outbox row for lease test: %v", err)
	}
	leased, fenceEpoch, err := store.BeginOutboxDispatchLease(ctx, leasedOutboxID, &workerTok, "test-worker-1", clk.Now(), 60*time.Second)
	if err != nil || !leased {
		t.Fatalf("BeginOutboxDispatchLease: leased=%v err=%v", leased, err)
	}

	// AnonymizeGuest returns ErrConflict while DISPATCHING is live, sets erasure_requested_at, AND immediately locks out Tier A + Tier B + confirmation token!
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, directRes.GuestID, "Attempt during live dispatch"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict while outbox row is DISPATCHING, got %v", err)
	}
	pendingCheckB, _ := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/bookings", pubSecret, tierB2.SessionToken, "", "203.0.113.32", nil)
	pendingCheckA, _ := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/bookings", pubSecret, tierALogin.SessionToken, "", "203.0.113.25", nil)
	pendingTokMint, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/booking-token", pubSecret, "", "", "203.0.113.25", map[string]any{
		"booking_reference":  directRes.Reference,
		"confirmation_token": plainConfTok,
	})
	if pendingCheckB.StatusCode != http.StatusUnauthorized || pendingCheckA.StatusCode != http.StatusUnauthorized || pendingTokMint.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected instant lockout as soon as erasure_requested_at is set, got B=%d A=%d tok=%d", pendingCheckB.StatusCode, pendingCheckA.StatusCode, pendingTokMint.StatusCode)
	}

	// Complete the in-flight outbox dispatch, then complete final AnonymizeGuest cleanly!
	if err := store.MarkOutboxEmailSentWithLease(ctx, leasedOutboxID, &workerTok, fenceEpoch, clk.Now()); err != nil {
		t.Fatalf("MarkOutboxEmailSentWithLease: %v", err)
	}
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, directRes.GuestID, "Fulfil GDPR erasure"); err != nil {
		t.Fatalf("final AnonymizeGuest failed: %v", err)
	}

	anonRes, err := store.GetReservation(ctx, nil, prop.ID, directRes.ID)
	if err != nil {
		t.Fatalf("get anonymized reservation: %v", err)
	}
	if anonRes.EstimatedArrivalTime != "" || anonRes.GuestArrivalNotes != "" {
		t.Fatalf("expected arrival ETA and notes scrubbed on AnonymizeGuest, got eta=%q notes=%q", anonRes.EstimatedArrivalTime, anonRes.GuestArrivalNotes)
	}
}

func TestPhase3Milestone4_ConcurrencyBruteForceAndExpiryCleanup(t *testing.T) {
	const pubSecret = "pub-portal-race-secret"
	srv, store, svc, prop, _, _, clk := setupPortalTestServer(t, pubSecret, true)

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	pol, _ := svc.GetBookingPolicy(ctx, ownerActor, prop.ID)
	pol.GuestPortalEnabled = true
	_, _ = svc.UpdateBookingPolicy(ctx, ownerActor, pol)

	if _, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Rhys", LastName: "Morgan", Email: "rhys.race@example.wales",
	}); err != nil {
		t.Fatalf("create guest: %v", err)
	}

	pubClient := newClient(t)
	portalBase := srv.URL + "/api/public/v1/properties/" + prop.Slug + "/portal"

	// 1. Concurrent Race Redemption: 8 goroutines redeeming the same single-use OTP simultaneously -> exactly 1 succeeds!
	_, _ = doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/challenge", pubSecret, "", "", "198.51.100.1", map[string]any{
		"email": "rhys.race@example.wales",
	})
	raceOTP := reExtractOTP.FindStringSubmatch(latestOutboxBodyForRecipient(t, store, "rhys.race@example.wales"))[1]

	var wg sync.WaitGroup
	var wins atomic.Int32
	var winningLogin service.PortalLoginResult
	var winMu sync.Mutex

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ip := fmt.Sprintf("198.51.100.%d", 10+idx)
			resp, body := doPortalReq(t, newClient(t), http.MethodPost, portalBase+"/auth/verify", pubSecret, "", "", ip, map[string]any{
				"email": "rhys.race@example.wales",
				"code":  raceOTP,
			})
			if resp.StatusCode == http.StatusOK {
				wins.Add(1)
				winMu.Lock()
				_ = json.Unmarshal(body, &winningLogin)
				winMu.Unlock()
			} else if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("unexpected concurrent verify status=%d: %s", resp.StatusCode, string(body))
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("expected exactly 1 concurrent OTP redemption to succeed, got %d", wins.Load())
	}

	// 2. Tier B Idle TTL (60m): advancing 61m expires the session, and PruneExpiredPortalState cleans DB rows while keeping audit_logs!
	clk.Advance(61 * time.Minute)
	expSessResp, _ := doPortalReq(t, pubClient, http.MethodGet, portalBase+"/auth/session", pubSecret, winningLogin.SessionToken, "", "198.51.100.2", nil)
	if expSessResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected Tier B session expired after 61m idle, got %d", expSessResp.StatusCode)
	}
	clk.Advance(6 * time.Minute)
	if err := svc.PruneExpiredPortalState(ctx); err != nil {
		t.Fatalf("PruneExpiredPortalState: %v", err)
	}
	var remainingSessions int
	_ = store.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM guest_portal_sessions`).Scan(&remainingSessions)
	if remainingSessions != 0 {
		t.Fatalf("expected expired portal session pruned cleanly, got %d", remainingSessions)
	}
	var preservedAudits int
	_ = store.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs_effective WHERE resource_type = 'guest_portal_session'`).Scan(&preservedAudits)
	if preservedAudits == 0 {
		t.Fatal("expected audit_logs evidence preserved after expired session pruning")
	}

	// 3. Max 5 Brute-Force Attempts (D8): 5 wrong codes lock out the challenge so the 6th attempt with the RIGHT code still fails 401!
	_, _ = svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Elin", LastName: "Brute", Email: "elin.brute@example.wales",
	})
	_, _ = doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/challenge", pubSecret, "", "", "198.51.100.50", map[string]any{
		"email": "elin.brute@example.wales",
	})
	validOTP := reExtractOTP.FindStringSubmatch(latestOutboxBodyForRecipient(t, store, "elin.brute@example.wales"))[1]
	wrongOTP := "000000"
	if validOTP == wrongOTP {
		wrongOTP = "999999"
	}
	for attempt := 1; attempt <= 5; attempt++ {
		ip := fmt.Sprintf("198.51.100.%d", 60+attempt)
		r, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/verify", pubSecret, "", "", ip, map[string]any{
			"email": "elin.brute@example.wales",
			"code":  wrongOTP,
		})
		if r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d expected 401, got %d", attempt, r.StatusCode)
		}
	}
	sixthResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/verify", pubSecret, "", "", "198.51.100.70", map[string]any{
		"email": "elin.brute@example.wales",
		"code":  validOTP,
	})
	if sixthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 6th attempt rejected with 401 after 5 failed guesses, got %d", sixthResp.StatusCode)
	}

	// 4. 15m OTP Expiry (D8): advancing 16m expires an unredeemed challenge!
	_, _ = svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Guto", LastName: "Expiry", Email: "guto.expiry@example.wales",
	})
	_, _ = doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/challenge", pubSecret, "", "", "198.51.100.80", map[string]any{
		"email": "guto.expiry@example.wales",
	})
	expOTP := reExtractOTP.FindStringSubmatch(latestOutboxBodyForRecipient(t, store, "guto.expiry@example.wales"))[1]
	clk.Advance(16 * time.Minute)
	expiredResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/verify", pubSecret, "", "", "198.51.100.81", map[string]any{
		"email": "guto.expiry@example.wales",
		"code":  expOTP,
	})
	if expiredResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 after 16m OTP expiry, got %d", expiredResp.StatusCode)
	}
}

func TestPhase3Milestone4_KeyedOTPHMACPathsAndLegacyInvalidation(t *testing.T) {
	pubSecret := "pub-m4-hmac-p1-secret"
	srv, store, svc, prop, _, _, clk := setupPortalTestServer(t, pubSecret, true)
	ctx := context.Background()
	pubClient := newClient(t)

	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{
		UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role,
	}
	pol, err := svc.GetBookingPolicy(ctx, ownerActor, prop.ID)
	if err != nil {
		t.Fatalf("get booking policy: %v", err)
	}
	pol.GuestPortalEnabled = true
	if _, err := svc.UpdateBookingPolicy(ctx, ownerActor, pol); err != nil {
		t.Fatalf("enable guest portal: %v", err)
	}

	portalBase := srv.URL + "/api/public/v1/properties/" + prop.Slug + "/portal"

	guest, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID,
		FirstName:  "Carys",
		LastName:   "HMAC",
		Email:      "carys.hmac@example.wales",
		Phone:      "+447700900555",
	})
	if err != nil {
		t.Fatalf("create guest: %v", err)
	}

	assertChallengeIsKeyedHMAC := func(normEmail string, purpose domain.PortalChallengePurpose, plainOTP string) uuid.UUID {
		t.Helper()
		var chID uuid.UUID
		var otpHash, otpAlg string
		var maxAttempts int
		var expiresAt time.Time
		if qErr := store.Pool().QueryRow(ctx, `
			SELECT id, otp_hash, otp_hash_alg, max_attempts, expires_at
			FROM guest_portal_challenges
			WHERE property_id = $1 AND normalized_email = $2 AND purpose = $3
			ORDER BY created_at DESC LIMIT 1
		`, prop.ID, normEmail, string(purpose)).Scan(&chID, &otpHash, &otpAlg, &maxAttempts, &expiresAt); qErr != nil {
			t.Fatalf("query challenge (%s/%s): %v", normEmail, purpose, qErr)
		}
		if otpAlg != domain.PortalOTPHashAlgHMACSHA256V1 {
			t.Fatalf("expected otp_hash_alg=%s, got %q", domain.PortalOTPHashAlgHMACSHA256V1, otpAlg)
		}
		if maxAttempts != domain.PortalChallengeMaxAttempts {
			t.Fatalf("expected max_attempts=%d, got %d", domain.PortalChallengeMaxAttempts, maxAttempts)
		}
		ttl := expiresAt.Sub(clk.Now())
		if ttl < 14*time.Minute+55*time.Second || ttl > 15*time.Minute+5*time.Second {
			t.Fatalf("expected ~15m challenge TTL, got %s", ttl)
		}
		unkeyedLegacy := sha256.Sum256([]byte(fmt.Sprintf("lovelace-portal-otp:%s:%s:%s", chID.String(), normEmail, plainOTP)))
		if otpHash == hex.EncodeToString(unkeyedLegacy[:]) {
			t.Fatal("CRITICAL: persisted otp_hash matched unkeyed SHA-256 construction")
		}
		return chID
	}

	// 1. Login OTP path + Wrong-Key Server Rejection:
	// Build a second server connected to the exact same PostgreSQL DB with a distinct 32-byte server key.
	altKey := make([]byte, 32)
	if _, err := rand.Read(altKey); err != nil {
		t.Fatal("failed to generate alternate 32-byte key")
	}
	altCfg := config.Config{
		AppEnv:             "test",
		DatabaseURL:        os.Getenv("TEST_DATABASE_URL"),
		EncryptionKey:      altKey,
		PublicOrigin:       "http://127.0.0.1:3000",
		PublicAPISecret:    pubSecret,
		GuestPortalEnabled: true,
		EmailFrom:          "bookings@tirbach.wales",
	}
	altSvc := service.New(store, altCfg)
	altSvc.SetClock(clk.Now)
	altRouter, err := app.NewRouter(altCfg, store, altSvc)
	if err != nil {
		t.Fatalf("alt router: %v", err)
	}
	altSrv := httptest.NewServer(altRouter)
	defer altSrv.Close()
	altPortalBase := altSrv.URL + "/api/public/v1/properties/" + prop.Slug + "/portal"

	chResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/challenge", pubSecret, "", "", "203.0.113.90", map[string]any{
		"email": "carys.hmac@example.wales",
	})
	if chResp.StatusCode != http.StatusAccepted {
		t.Fatalf("login challenge expected 202, got %d", chResp.StatusCode)
	}
	loginOTP := reExtractOTP.FindStringSubmatch(latestOutboxBodyForRecipient(t, store, "carys.hmac@example.wales"))[1]
	_ = assertChallengeIsKeyedHMAC("carys.hmac@example.wales", domain.PortalChallengeLogin, loginOTP)

	// Attempting to redeem the exact OTP against a server holding a different HMAC key must fail with 401.
	wrongKeyResp, _ := doPortalReq(t, pubClient, http.MethodPost, altPortalBase+"/auth/verify", pubSecret, "", "", "203.0.113.91", map[string]any{
		"email": "carys.hmac@example.wales",
		"code":  loginOTP,
	})
	if wrongKeyResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 when verifying OTP under mismatched server HMAC key, got %d", wrongKeyResp.StatusCode)
	}

	// Redeeming under the matching server key succeeds (even with whitespace/case variations).
	okLoginResp, okLoginBody := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/verify", pubSecret, "", "", "203.0.113.92", map[string]any{
		"email": "  Carys.HMAC@Example.Wales ",
		"code":  " " + loginOTP + " ",
	})
	if okLoginResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on matching-key login OTP verify, got %d: %s", okLoginResp.StatusCode, string(okLoginBody))
	}
	var loginRes service.PortalLoginResult
	_ = json.Unmarshal(okLoginBody, &loginRes)

	// 2. Step-Up OTP path (keyed HMAC + 5-attempt lockout + 15m TTL + successful redemption).
	clk.Advance(20 * time.Minute)
	stepChResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/step-up/challenge", pubSecret, loginRes.SessionToken, loginRes.Session.CSRFToken, "203.0.113.92", map[string]any{})
	if stepChResp.StatusCode != http.StatusAccepted {
		t.Fatalf("step-up challenge expected 202, got %d", stepChResp.StatusCode)
	}
	stepOTP1 := reExtractOTP.FindStringSubmatch(latestOutboxBodyForRecipient(t, store, "carys.hmac@example.wales"))[1]
	_ = assertChallengeIsKeyedHMAC("carys.hmac@example.wales", domain.PortalChallengeStepUp, stepOTP1)

	wrongStep := "000000"
	if stepOTP1 == wrongStep {
		wrongStep = "999999"
	}
	for i := 0; i < 5; i++ {
		r, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/step-up/verify", pubSecret, loginRes.SessionToken, loginRes.Session.CSRFToken, fmt.Sprintf("203.0.113.%d", 100+i), map[string]any{"code": wrongStep})
		if r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("step-up wrong attempt %d expected 401, got %d", i+1, r.StatusCode)
		}
	}
	lockedStepResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/step-up/verify", pubSecret, loginRes.SessionToken, loginRes.Session.CSRFToken, "203.0.113.106", map[string]any{"code": stepOTP1})
	if lockedStepResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 6th step-up attempt rejected 401 after 5 failures, got %d", lockedStepResp.StatusCode)
	}

	_, _ = doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/step-up/challenge", pubSecret, loginRes.SessionToken, loginRes.Session.CSRFToken, "203.0.113.107", map[string]any{})
	stepOTP2 := reExtractOTP.FindStringSubmatch(latestOutboxBodyForRecipient(t, store, "carys.hmac@example.wales"))[1]
	stepVerResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/step-up/verify", pubSecret, loginRes.SessionToken, loginRes.Session.CSRFToken, "203.0.113.107", map[string]any{"code": stepOTP2})
	if stepVerResp.StatusCode != http.StatusOK {
		t.Fatalf("expected step-up verification 200, got %d", stepVerResp.StatusCode)
	}

	// 3. Pending-Email Change OTP path (keyed HMAC + 15m TTL + successful confirmation).
	emChResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/profile/email-change", pubSecret, loginRes.SessionToken, loginRes.Session.CSRFToken, "203.0.113.108", map[string]any{
		"new_email": "carys.updated@example.wales",
	})
	if emChResp.StatusCode != http.StatusAccepted {
		t.Fatalf("email-change challenge expected 202, got %d", emChResp.StatusCode)
	}
	emOTP := reExtractOTP.FindStringSubmatch(latestOutboxBodyForRecipient(t, store, "carys.updated@example.wales"))[1]
	_ = assertChallengeIsKeyedHMAC("carys.updated@example.wales", domain.PortalChallengeVerifyEmail, emOTP)

	emConfResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/profile/email-change/confirm", pubSecret, loginRes.SessionToken, loginRes.Session.CSRFToken, "203.0.113.108", map[string]any{
		"code": emOTP,
	})
	if emConfResp.StatusCode != http.StatusOK {
		t.Fatalf("expected email-change confirm 200, got %d", emConfResp.StatusCode)
	}

	// 4. Staff-Triggered Support Verification OTP path (keyed HMAC + successful guest login redemption).
	if err := svc.SendGuestPortalVerification(ctx, ownerActor, prop.ID, guest.ID); err != nil {
		t.Fatalf("SendGuestPortalVerification: %v", err)
	}
	staffOTP := reExtractOTP.FindStringSubmatch(latestOutboxBodyForRecipient(t, store, "carys.updated@example.wales"))[1]
	_ = assertChallengeIsKeyedHMAC("carys.updated@example.wales", domain.PortalChallengeLogin, staffOTP)

	staffRedeemResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/verify", pubSecret, "", "", "203.0.113.109", map[string]any{
		"email": "carys.updated@example.wales",
		"code":  staffOTP,
	})
	if staffRedeemResp.StatusCode != http.StatusOK {
		t.Fatalf("expected staff-triggered portal OTP to verify 200, got %d", staffRedeemResp.StatusCode)
	}

	// 5. Fail-closed when a safe HMAC key is unavailable (missing or zero EncryptionKey, or dev key in production).
	missingKeyCfg := config.Config{
		AppEnv:             "test",
		DatabaseURL:        os.Getenv("TEST_DATABASE_URL"),
		EncryptionKey:      nil,
		PublicOrigin:       "http://127.0.0.1:3000",
		PublicAPISecret:    pubSecret,
		GuestPortalEnabled: true,
		EmailFrom:          "bookings@tirbach.wales",
	}
	missingKeySvc := service.New(store, missingKeyCfg)
	missingKeySvc.SetClock(clk.Now)
	if _, err := missingKeySvc.RequestPortalAuthChallenge(ctx, service.Actor{}, service.RequestPortalChallengeInput{
		PropertySlug: prop.Slug,
		Email:        "carys.updated@example.wales",
		Purpose:      domain.PortalChallengeLogin,
	}); !errors.Is(err, service.ErrPortalKeyUnavailable) {
		t.Fatalf("expected RequestPortalAuthChallenge to fail closed with ErrPortalKeyUnavailable, got %v", err)
	}
	if err := missingKeySvc.SendGuestPortalVerification(ctx, ownerActor, prop.ID, guest.ID); !errors.Is(err, service.ErrPortalKeyUnavailable) {
		t.Fatalf("expected SendGuestPortalVerification to fail closed with ErrPortalKeyUnavailable, got %v", err)
	}

	insecureProdSvc := service.New(store, config.Config{
		AppEnv:          "production",
		DatabaseURL:     os.Getenv("TEST_DATABASE_URL"),
		CookieSecure:    true,
		EncryptionKey:   []byte("tirbach-dev-only-32byte-key-0001"),
		PublicOrigin:    "https://book.tirbach.wales",
		PublicAPISecret: "prod-m4-strong-public-api-secret-2026",
	})
	if err := insecureProdSvc.ValidateProductionStartup(ctx); !errors.Is(err, service.ErrPortalKeyUnavailable) {
		t.Fatalf("expected ValidateProductionStartup to reject dev fallback key in production with ErrPortalKeyUnavailable, got %v", err)
	}

	// 6. Invalidated legacy challenge row (migration 0027 INVALIDATED_UNKEYED) is rejected while audit evidence stays intact,
	// and requesting a fresh challenge succeeds cleanly.
	legacyID, _ := uuid.NewV7()
	legacyTokPlain, _ := postgres.GeneratePortalRandomHex(32)
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO guest_portal_challenges (
			id, property_id, guest_id, normalized_email, purpose,
			otp_hash, otp_hash_alg, token_hash, browser_nonce_hash,
			failed_attempts, max_attempts, expires_at, invalidated_at
		) VALUES (
			$1, $2, $3, 'carys.updated@example.wales', 'LOGIN',
			repeat('0', 64), 'INVALIDATED_UNKEYED', $4, $5,
			0, 5, $6, $7
		)
	`, legacyID, prop.ID, guest.ID, postgres.HashPortalValue(legacyTokPlain), postgres.HashPortalValue("legacy-nonce"), clk.Now().Add(15*time.Minute), clk.Now()); err != nil {
		t.Fatalf("insert invalidated legacy challenge: %v", err)
	}
	legacyOTPResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/verify", pubSecret, "", "", "203.0.113.115", map[string]any{
		"email": "carys.updated@example.wales",
		"code":  "000000",
	})
	if legacyOTPResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected invalidated legacy challenge OTP rejected 401, got %d", legacyOTPResp.StatusCode)
	}
	legacyMagicResp, _ := doPortalReq(t, pubClient, http.MethodPost, portalBase+"/auth/verify", pubSecret, "", "", "203.0.113.116", map[string]any{
		"magic_token":   legacyTokPlain,
		"browser_nonce": "legacy-nonce",
	})
	if legacyMagicResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected invalidated legacy challenge magic link rejected 401, got %d", legacyMagicResp.StatusCode)
	}
	var challengeAudits int
	_ = store.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs_effective WHERE property_id = $1 AND action = 'guest_portal_identity.challenge_issued'`, prop.ID).Scan(&challengeAudits)
	if challengeAudits == 0 {
		t.Fatal("expected historical challenge_issued audit evidence preserved intact")
	}
}
