package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/app"
	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/seed"
	"github.com/tirbach/tir-bach/internal/service"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

var testEncryptionKey = []byte("0123456789abcdef0123456789abcdef")

func verifySafeTestDatabase(dbURL, currentDB, currentUser string, isSuperuser bool) error {
	u, err := url.Parse(dbURL)
	if err != nil {
		return fmt.Errorf("invalid TEST_DATABASE_URL: %w", err)
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if os.Getenv("TIRBACH_ALLOW_REMOTE_TEST_DB") != "true" {
		switch host {
		case "127.0.0.1", "localhost", "::1", "postgres", "db":
		default:
			return fmt.Errorf("refusing destructive test reset on non-local host %q", host)
		}
	}
	urlDB := strings.TrimPrefix(u.Path, "/")
	if !isExplicitTestDBName(urlDB) || !isExplicitTestDBName(currentDB) || urlDB != currentDB {
		return fmt.Errorf("refusing destructive test reset on non-test database (url_db=%q, current_database=%q)", urlDB, currentDB)
	}
	lowUser := strings.ToLower(strings.TrimSpace(currentUser))
	if isSuperuser || lowUser == "" || lowUser == "postgres" || strings.Contains(lowUser, "prod") {
		return fmt.Errorf("refusing destructive test reset as privileged or non-test role (user=%q, superuser=%v); run scripts/setup-test-db.sh and use tir_bach_test_runner", currentUser, isSuperuser)
	}
	return nil
}

func isExplicitTestDBName(name string) bool {
	low := strings.ToLower(strings.TrimSpace(name))
	if low == "" || low == "tir_bach" || low == "postgres" || strings.Contains(low, "prod") {
		return false
	}
	return strings.HasSuffix(low, "_test") || strings.HasSuffix(low, "-test") || strings.Contains(low, "_test_")
}

func setupTestServer(t *testing.T) (*httptest.Server, *postgres.Store, domain.Property) {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping PostgreSQL integration test")
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
		t.Fatalf("inspect current_database and role: %v", err)
	}
	if err := verifySafeTestDatabase(dbURL, currentDB, currentUser, isSuperuser); err != nil {
		t.Fatalf("unsafe test database rejected: %v", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}

	// Reset tables cleanly for deterministic test runs inside an explicitly token-scoped TX.
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
		AppEnv:          "test",
		DatabaseURL:     dbURL,
		CookieSecure:    false,
		EncryptionKey:   testEncryptionKey,
		SessionTTL:      1 * time.Hour,
		SessionIdleTTL:  30 * time.Minute,
		LoginMaxFailIP:  5,
		LoginMaxFailAcc: 4,
		LoginWindow:     15 * time.Minute,
	}
	svc := service.New(store, cfg)
	router, err := app.NewRouter(cfg, store, svc)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, store, prop
}

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func apiLogin(t *testing.T, client *http.Client, baseURL, email, password string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	resp, err := client.Post(baseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, ""
	}
	var out struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return resp.StatusCode, out.CSRFToken
}

func doJSON(t *testing.T, client *http.Client, method, targetURL, csrf string, payload any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&buf).Encode(payload); err != nil {
			t.Fatalf("encode payload: %v", err)
		}
	}
	req, err := http.NewRequest(method, targetURL, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request %s %s: %v", method, targetURL, err)
	}
	return resp
}

func TestPhase1EndToEnd(t *testing.T) {
	srv, store, prop := setupTestServer(t)
	ownerClient := newClient(t)

	// 1. Health & readiness checks + security headers
	res, err := ownerClient.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatalf("readyz: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 readyz, got %d", res.StatusCode)
	}
	if res.Header.Get("X-Frame-Options") != "DENY" || res.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("missing security headers: %v", res.Header)
	}

	// 2. Unauthenticated access rejected
	unauthResp, err := ownerClient.Get(srv.URL + "/api/v1/properties")
	if err != nil {
		t.Fatalf("unauth properties: %v", err)
	}
	unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated API call, got %d", unauthResp.StatusCode)
	}

	// 3. Login as owner_admin
	code, ownerCSRF := apiLogin(t, ownerClient, srv.URL, "owner@tirbach.wales", "tirbach-owner-password-2026")
	if code != http.StatusOK || ownerCSRF == "" {
		t.Fatalf("expected 200 login with CSRF token, got %d", code)
	}

	// 4. CSRF protection blocks mutation without X-CSRF-Token
	noCSRFResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/unit-types", "", map[string]any{
		"code": "yurt", "name": "Yurt", "category": "glamping", "base_capacity_adults": 2,
		"max_adults": 2, "max_guests": 4, "base_nightly_rate_pence": 10000, "min_nights": 2, "max_nights": 14,
	})
	noCSRFResp.Body.Close()
	if noCSRFResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when CSRF header omitted, got %d", noCSRFResp.StatusCode)
	}

	// 5. Create Unit Type, Physical Unit, and test optimistic concurrency conflict (409)
	createUTResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/unit-types", ownerCSRF, map[string]any{
		"code": "safari-tent", "name": "Safari Tent", "category": "glamping",
		"base_capacity_adults": 2, "max_adults": 4, "max_children": 2, "max_guests": 6,
		"pets_allowed": true, "max_pets": 2, "base_nightly_rate_pence": 14000,
		"tax_rate_bps": 2000, "min_nights": 2, "max_nights": 14,
		"amenities": []string{"deck", "en-suite"},
	})
	if createUTResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 create unit type, got %d", createUTResp.StatusCode)
	}
	var ut domain.UnitType
	_ = json.NewDecoder(createUTResp.Body).Decode(&ut)
	createUTResp.Body.Close()

	// Stale version update must fail with 409 Conflict
	staleResp := doJSON(t, ownerClient, http.MethodPatch, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/unit-types/"+ut.ID.String(), ownerCSRF, map[string]any{
		"code": "safari-tent", "name": "Safari Tent Updated", "category": "glamping",
		"base_capacity_adults": 2, "max_adults": 4, "max_children": 2, "max_guests": 6,
		"pets_allowed": true, "max_pets": 2, "base_nightly_rate_pence": 14500,
		"min_nights": 2, "max_nights": 14, "version": 99,
	})
	staleResp.Body.Close()
	if staleResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 on stale version, got %d", staleResp.StatusCode)
	}

	createUnitResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/units", ownerCSRF, map[string]any{
		"unit_type_id": ut.ID, "code": "ST-01", "name": "Coastal Safari Tent 1", "zone": "Sea View Ridge", "status": "active",
	})
	if createUnitResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 create unit, got %d", createUnitResp.StatusCode)
	}
	var unit domain.Unit
	_ = json.NewDecoder(createUnitResp.Body).Decode(&unit)
	createUnitResp.Body.Close()

	// 6. Create Availability Block + verify GiST overlap exclusion + adjacent dates + release
	b1Resp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/availability-blocks", ownerCSRF, map[string]any{
		"unit_id": unit.ID, "start_date": "2026-08-01", "end_date": "2026-08-05",
		"block_type": "maintenance", "reason": "Decking oil",
	})
	if b1Resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 create block, got %d", b1Resp.StatusCode)
	}
	var b1 domain.AvailabilityBlock
	_ = json.NewDecoder(b1Resp.Body).Decode(&b1)
	b1Resp.Body.Close()

	// Overlapping block [2026-08-04, 2026-08-07) MUST fail with 409
	overlapResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/availability-blocks", ownerCSRF, map[string]any{
		"unit_id": unit.ID, "start_date": "2026-08-04", "end_date": "2026-08-07",
		"block_type": "owner_hold", "reason": "Overlap check",
	})
	overlapResp.Body.Close()
	if overlapResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 on overlapping block, got %d", overlapResp.StatusCode)
	}

	// Adjacent back-to-back block [2026-08-05, 2026-08-07) MUST succeed (half-open interval)
	adjResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/availability-blocks", ownerCSRF, map[string]any{
		"unit_id": unit.ID, "start_date": "2026-08-05", "end_date": "2026-08-07",
		"block_type": "owner_hold", "reason": "Owner weekend",
	})
	adjResp.Body.Close()
	if adjResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 on adjacent back-to-back block, got %d", adjResp.StatusCode)
	}

	// Release first block -> re-creating [2026-08-01, 2026-08-05) now succeeds
	relResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/availability-blocks/"+b1.ID.String()+"/release", ownerCSRF, map[string]any{})
	relResp.Body.Close()
	if relResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 releasing block, got %d", relResp.StatusCode)
	}
	recreateResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/availability-blocks", ownerCSRF, map[string]any{
		"unit_id": unit.ID, "start_date": "2026-08-01", "end_date": "2026-08-05",
		"block_type": "maintenance", "reason": "Re-scheduled work",
	})
	recreateResp.Body.Close()
	if recreateResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 after releasing previous block, got %d", recreateResp.StatusCode)
	}

	// 7. Rate Preview via API
	plans, err := store.ListRatePlans(context.Background(), prop.ID)
	if err != nil || len(plans) == 0 {
		t.Fatalf("list rate plans: %v", err)
	}
	previewResp, err := ownerClient.Get(srv.URL + "/api/v1/properties/" + prop.ID.String() + "/rate-preview?unit_type_id=" + ut.ID.String() + "&rate_plan_id=" + plans[0].ID.String() + "&start_date=2026-08-10&end_date=2026-08-13")
	if err != nil {
		t.Fatalf("rate preview: %v", err)
	}
	var preview domain.RatePreview
	_ = json.NewDecoder(previewResp.Body).Decode(&preview)
	previewResp.Body.Close()
	if previewResp.StatusCode != http.StatusOK || preview.Nights != 3 || preview.TotalPence != 42000 {
		t.Fatalf("unexpected rate preview: status=%d preview=%+v", previewResp.StatusCode, preview)
	}

	// 8. RBAC: create a staff user and verify permissions
	staffCreate := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/users", ownerCSRF, map[string]any{
		"property_id": prop.ID, "email": "staff@tirbach.wales", "full_name": "Gareth Davies",
		"role": "staff", "password": "gareth-staff-password-2026",
	})
	staffCreate.Body.Close()
	if staffCreate.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating staff user, got %d", staffCreate.StatusCode)
	}

	staffClient := newClient(t)
	staffCode, staffCSRF := apiLogin(t, staffClient, srv.URL, "staff@tirbach.wales", "gareth-staff-password-2026")
	if staffCode != http.StatusOK {
		t.Fatalf("staff login failed: %d", staffCode)
	}
	// Staff cannot create unit types (403)
	staffForbidden := doJSON(t, staffClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/unit-types", staffCSRF, map[string]any{
		"code": "pod", "name": "Pod", "category": "cabin", "base_capacity_adults": 2,
		"max_adults": 2, "max_guests": 2, "base_nightly_rate_pence": 8000, "min_nights": 1, "max_nights": 7,
	})
	staffForbidden.Body.Close()
	if staffForbidden.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for staff creating unit type, got %d", staffForbidden.StatusCode)
	}

	// 9. Audit logs recorded mutations
	auditResp, err := ownerClient.Get(srv.URL + "/api/v1/audit-logs?limit=20")
	if err != nil {
		t.Fatalf("audit logs: %v", err)
	}
	var auditPayload struct {
		Items []domain.AuditLog `json:"items"`
	}
	_ = json.NewDecoder(auditResp.Body).Decode(&auditPayload)
	auditResp.Body.Close()
	if auditResp.StatusCode != http.StatusOK || len(auditPayload.Items) < 5 {
		t.Fatalf("expected >=5 audit logs, got status=%d len=%d", auditResp.StatusCode, len(auditPayload.Items))
	}

	// 10. Brute-force rate limiting triggers 429 after repeated failed attempts
	badClient := newClient(t)
	var lastStatus int
	for i := 0; i < 6; i++ {
		lastStatus, _ = apiLogin(t, badClient, srv.URL, "nobody@tirbach.wales", "wrong-password-12345")
	}
	if lastStatus != http.StatusTooManyRequests {
		t.Fatalf("expected 429 rate limited after repeated failures, got %d", lastStatus)
	}

	// 11. Web UI smoke test: login form -> dashboard & inventory pages return 200 HTML
	webClient := newClient(t)
	form := url.Values{}
	form.Set("email", "owner@tirbach.wales")
	form.Set("password", "tirbach-owner-password-2026")
	webLoginResp, err := webClient.PostForm(srv.URL+"/login", form)
	if err != nil {
		t.Fatalf("web login: %v", err)
	}
	webLoginResp.Body.Close()
	if webLoginResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect after web login, got %d", webLoginResp.StatusCode)
	}

	for _, path := range []string{"/", "/calendar", "/reservations", "/reservations/new", "/property", "/unit-types", "/units", "/extras", "/rates", "/blocks", "/releases", "/settings/integrations/channel-manager", "/users", "/audit-logs", "/account"} {
		pageResp, err := webClient.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(pageResp.Body)
		pageResp.Body.Close()
		if pageResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d", path, pageResp.StatusCode)
		}
		if !strings.Contains(buf.String(), "Tir Bach") {
			t.Fatalf("expected Tir Bach property content on %s", path)
		}
		if !strings.Contains(buf.String(), "LOVELACE") {
			t.Fatalf("expected LOVELACE platform branding on %s", path)
		}
	}
}
