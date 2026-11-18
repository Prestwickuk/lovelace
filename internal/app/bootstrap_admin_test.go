package app_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/app"
	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/service"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

func TestFirstBootInitialAdminBootstrapAndEmptyPropertyUX(t *testing.T) {
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

	cfg := config.Config{
		AppEnv:               "test",
		DatabaseURL:          dbURL,
		CookieSecure:         false,
		EncryptionKey:        testEncryptionKey,
		SessionTTL:           1 * time.Hour,
		SessionIdleTTL:       30 * time.Minute,
		LoginMaxFailIP:       20,
		LoginMaxFailAcc:      8,
		LoginWindow:          15 * time.Minute,
		PublicOrigin:         "http://127.0.0.1:3000",
		PublicAPISecret:      "boot-admin-test-bff-secret",
		AttachmentStorageDir: t.TempDir(),
		MaxAttachmentBytes:   10 * 1024 * 1024,
	}
	svc := service.New(store, cfg)
	router, err := app.NewRouter(cfg, store, svc)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	srv := httptest.NewServer(router)
	defer srv.Close()

	// 1. Before bootstrap (0 admin_users, 0 properties), GET /login shows the first-time setup banner.
	resp0, err := http.Get(srv.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	body0Bytes, _ := io.ReadAll(resp0.Body)
	resp0.Body.Close()
	if resp0.StatusCode != http.StatusOK || !strings.Contains(string(body0Bytes), "First-time setup pending") {
		t.Fatalf("expected GET /login on empty DB to display setup notice, status=%d", resp0.StatusCode)
	}

	// 2. First-boot BootstrapInitialAdmin provisions owner_admin with MustRotatePassword=true and seeds 0 properties.
	const initialPw = "InitialBootPass-2026!"
	const chosenPw = "PermanentOwnerPass-2026!"
	created, bootUser, err := svc.BootstrapInitialAdmin(ctx, "owner@example.test", "Bootstrapped Owner", initialPw)
	if err != nil {
		t.Fatalf("BootstrapInitialAdmin: %v", err)
	}
	if !created || !bootUser.MustRotatePassword {
		t.Fatalf("expected created=true and MustRotatePassword=true, got created=%v mustRotate=%v", created, bootUser.MustRotatePassword)
	}

	// Second call (simulating container restart) is a strict no-op.
	createdAgain, _, err := svc.BootstrapInitialAdmin(ctx, "other@example.test", "Other", "AnotherTempPass-2026!")
	if err != nil || createdAgain {
		t.Fatalf("expected second BootstrapInitialAdmin to be no-op, created=%v err=%v", createdAgain, err)
	}

	// 0 properties exist in DB.
	var propCount int
	if err := store.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM properties`).Scan(&propCount); err != nil || propCount != 0 {
		t.Fatalf("expected 0 properties after admin-only bootstrap, count=%d err=%v", propCount, err)
	}

	// GET /login no longer shows the setup banner once an admin exists.
	resp1, err := http.Get(srv.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login after bootstrap: %v", err)
	}
	body1Bytes, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	if strings.Contains(string(body1Bytes), "First-time setup pending") {
		t.Fatal("did not expect setup notice once admin_users > 0")
	}

	// 3. Sign in with bootstrap credentials -> redirected to /account?rotate=1 to set personal password.
	jar, _ := cookiejar.New(nil)
	noRedirectClient := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	loginForm := url.Values{
		"email":    {"owner@example.test"},
		"password": {initialPw},
	}
	loginReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/login", strings.NewReader(loginForm.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.Header.Set("Origin", srv.URL)
	loginResp, err := noRedirectClient.Do(loginReq)
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusSeeOther || loginResp.Header.Get("Location") != "/account?rotate=1" {
		t.Fatalf("expected 303 redirect to /account?rotate=1, got %d %q", loginResp.StatusCode, loginResp.Header.Get("Location"))
	}

	// Dashboard is blocked while MustRotatePassword=true -> redirects to /account?rotate=1.
	dashRotResp, err := noRedirectClient.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET / before rotation: %v", err)
	}
	dashRotResp.Body.Close()
	if dashRotResp.StatusCode != http.StatusSeeOther || dashRotResp.Header.Get("Location") != "/account?rotate=1" {
		t.Fatalf("expected GET / before password rotation to redirect to /account?rotate=1, got %d %q", dashRotResp.StatusCode, dashRotResp.Header.Get("Location"))
	}

	// 4. Load /account?rotate=1, extract CSRF token, and rotate to chosen personal password.
	followClient := &http.Client{Jar: jar}
	acctResp, err := followClient.Get(srv.URL + "/account?rotate=1")
	if err != nil {
		t.Fatalf("GET /account?rotate=1: %v", err)
	}
	acctBytes, _ := io.ReadAll(acctResp.Body)
	acctResp.Body.Close()
	acctHTML := string(acctBytes)
	if acctResp.StatusCode != http.StatusOK || !strings.Contains(acctHTML, "Set Your New Personal Password") {
		t.Fatalf("expected /account?rotate=1 rotation prompt, status=%d", acctResp.StatusCode)
	}
	csrfRe := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
	m := csrfRe.FindStringSubmatch(acctHTML)
	if len(m) < 2 {
		t.Fatal("failed to extract csrf_token from /account")
	}

	rotForm := url.Values{
		"csrf_token":       {m[1]},
		"current_password": {initialPw},
		"new_password":     {chosenPw},
	}
	rotReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/account/password", strings.NewReader(rotForm.Encode()))
	rotReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rotReq.Header.Set("Origin", srv.URL)
	rotResp, err := followClient.Do(rotReq)
	if err != nil {
		t.Fatalf("POST /account/password: %v", err)
	}
	rotBytes, _ := io.ReadAll(rotResp.Body)
	rotResp.Body.Close()
	if rotResp.StatusCode != http.StatusOK || !strings.Contains(string(rotBytes), "Password updated") {
		t.Fatalf("expected password rotation to succeed, status=%d", rotResp.StatusCode)
	}

	// 5. Re-running BootstrapInitialAdmin after rotation MUST NOT overwrite the owner's chosen password.
	createdAfter, _, err := svc.BootstrapInitialAdmin(ctx, "owner@example.test", "Bootstrapped Owner", initialPw)
	if err != nil || createdAfter {
		t.Fatalf("expected BootstrapInitialAdmin after rotation to remain no-op, created=%v err=%v", createdAfter, err)
	}

	// 6. On an empty DB (0 properties), GET /, GET /workspace, GET /users, and GET /audit-logs all render 200 OK cleanly!
	for _, p := range []string{"/", "/workspace", "/users", "/audit-logs"} {
		r, err := followClient.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK on %s with empty properties table, got %d", p, r.StatusCode)
		}
		if (p == "/" || p == "/workspace") && !strings.Contains(string(b), "No Property Configured Yet") {
			t.Fatalf("expected empty-property notice on %s", p)
		}
	}
}
