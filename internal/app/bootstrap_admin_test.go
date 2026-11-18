package app_test

import (
	"context"
	"errors"
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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/app"
	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

func TestFirstBootAdminBootstrapRecoveryCodesAndStaffReset(t *testing.T) {
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
			TRUNCATE TABLE admin_recovery_codes,
			               guest_portal_sessions, guest_portal_challenges, guest_portal_identities,
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

	// 1. Empty DB GET /login shows both first-time setup banner and Forgot password link.
	resp0, err := http.Get(srv.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	body0Bytes, _ := io.ReadAll(resp0.Body)
	resp0.Body.Close()
	body0 := string(body0Bytes)
	if resp0.StatusCode != http.StatusOK || !strings.Contains(body0, "First-time setup pending") || !strings.Contains(body0, "/login/recover") {
		t.Fatalf("expected GET /login setup notice and /login/recover link, status=%d", resp0.StatusCode)
	}

	// 2. First-boot BootstrapInitialAdmin provisions owner_admin with MustRotatePassword=true and seeds 0 properties.
	const initialPw = "InitialBootPass-2026!"
	const chosenPw = "PermanentOwnerPass-2026!"
	const recoveredPw = "RecoveredOwnerPass-2026!"
	created, bootUser, err := svc.BootstrapInitialAdmin(ctx, "owner@example.test", "Bootstrapped Owner", initialPw)
	if err != nil {
		t.Fatalf("BootstrapInitialAdmin: %v", err)
	}
	if !created || !bootUser.MustRotatePassword {
		t.Fatalf("expected created=true and MustRotatePassword=true, got created=%v mustRotate=%v", created, bootUser.MustRotatePassword)
	}
	if createdAgain, _, err := svc.BootstrapInitialAdmin(ctx, "other@example.test", "Other", "AnotherTempPass-2026!"); err != nil || createdAgain {
		t.Fatalf("expected second BootstrapInitialAdmin to be no-op, created=%v err=%v", createdAgain, err)
	}

	// 3. Sign in with bootstrap credentials -> redirected to /account?rotate=1.
	jar, _ := cookiejar.New(nil)
	noRedirectClient := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	loginForm := url.Values{"email": {"owner@example.test"}, "password": {initialPw}}
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

	// 4. Setting personal password on /account automatically generates and shows 8 one-time recovery codes!
	followClient := &http.Client{Jar: jar}
	acctResp, err := followClient.Get(srv.URL + "/account?rotate=1")
	if err != nil {
		t.Fatalf("GET /account?rotate=1: %v", err)
	}
	acctBytes, _ := io.ReadAll(acctResp.Body)
	acctResp.Body.Close()
	csrfRe := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
	m := csrfRe.FindStringSubmatch(string(acctBytes))
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
	rotHTML := string(rotBytes)
	if rotResp.StatusCode != http.StatusOK || !strings.Contains(rotHTML, "Your One-Time Password Recovery Codes") {
		t.Fatalf("expected initial recovery codes card after setting password, status=%d", rotResp.StatusCode)
	}

	codeBlockRe := regexp.MustCompile(`(?s)<pre id="recovery-codes-plain"[^>]*>(.*?)</pre>`)
	cm := codeBlockRe.FindStringSubmatch(rotHTML)
	if len(cm) < 2 {
		t.Fatal("failed to find recovery-codes-plain block in /account response")
	}
	initialCodes := strings.Fields(strings.TrimSpace(cm[1]))
	if len(initialCodes) != 8 {
		t.Fatalf("expected 8 recovery codes, got %d: %v", len(initialCodes), initialCodes)
	}
	if !strings.Contains(rotHTML, "8 of 8 unused") {
		t.Fatal("expected 8 of 8 unused badge on /account")
	}

	// Empty-property views render 200 OK cleanly.
	for _, p := range []string{"/", "/workspace", "/users", "/audit-logs"} {
		r, err := followClient.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		_, _ = io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK on %s with empty properties table, got %d", p, r.StatusCode)
		}
	}

	// 5. Email-free password recovery on /login/recover:
	// 5a. Origin: null rejected with 403.
	nullRecReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/login/recover", strings.NewReader("email=owner@example.test"))
	nullRecReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	nullRecReq.Header.Set("Origin", "null")
	nullRecResp, err := http.DefaultClient.Do(nullRecReq)
	if err != nil {
		t.Fatalf("POST /login/recover Origin: null: %v", err)
	}
	nullRecResp.Body.Close()
	if nullRecResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for Origin: null on /login/recover, got %d", nullRecResp.StatusCode)
	}

	// 5b. Too-short new password returns 422 WITHOUT burning the recovery code!
	shortRecForm := url.Values{
		"email":         {"owner@example.test"},
		"recovery_code": {initialCodes[0]},
		"new_password":  {"too-short"},
	}
	shortRecReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/login/recover", strings.NewReader(shortRecForm.Encode()))
	shortRecReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	shortRecReq.Header.Set("Origin", srv.URL)
	shortRecResp, err := http.DefaultClient.Do(shortRecReq)
	if err != nil {
		t.Fatalf("POST /login/recover short password: %v", err)
	}
	shortRecResp.Body.Close()
	if shortRecResp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for short new password, got %d", shortRecResp.StatusCode)
	}

	// 5c. Wrong recovery code returns 401.
	badRecForm := url.Values{
		"email":         {"owner@example.test"},
		"recovery_code": {"ZZZZ-ZZZZ-ZZZZ-ZZZZ"},
		"new_password":  {recoveredPw},
	}
	badRecReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/login/recover", strings.NewReader(badRecForm.Encode()))
	badRecReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badRecReq.Header.Set("Origin", srv.URL)
	badRecResp, err := http.DefaultClient.Do(badRecReq)
	if err != nil {
		t.Fatalf("POST /login/recover bad code: %v", err)
	}
	badRecResp.Body.Close()
	if badRecResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid recovery code, got %d", badRecResp.StatusCode)
	}

	// 5d. Valid recovery code (lowercase with spaces instead of hyphens) resets password, revokes prior session, and signs in!
	recJar, _ := cookiejar.New(nil)
	recClient := &http.Client{Jar: recJar}
	messyCode0 := strings.ToLower(strings.ReplaceAll(initialCodes[0], "-", " "))
	goodRecForm := url.Values{
		"email":         {"owner@example.test"},
		"recovery_code": {messyCode0},
		"new_password":  {recoveredPw},
	}
	goodRecReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/login/recover", strings.NewReader(goodRecForm.Encode()))
	goodRecReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	goodRecReq.Header.Set("Origin", srv.URL)
	goodRecResp, err := recClient.Do(goodRecReq)
	if err != nil {
		t.Fatalf("POST /login/recover valid code: %v", err)
	}
	goodRecBytes, _ := io.ReadAll(goodRecResp.Body)
	goodRecResp.Body.Close()
	goodRecHTML := string(goodRecBytes)
	if goodRecResp.StatusCode != http.StatusOK || !strings.Contains(goodRecHTML, "7 of 8 unused") {
		t.Fatalf("expected recovery redirect to /account showing 7 of 8 unused, status=%d", goodRecResp.StatusCode)
	}

	// Prior browser session (followClient) was revoked during password recovery -> redirects to /login!
	oldSessResp, err := noRedirectClient.Get(srv.URL + "/account")
	if err != nil {
		t.Fatalf("GET /account with revoked session: %v", err)
	}
	oldSessResp.Body.Close()
	if oldSessResp.StatusCode != http.StatusSeeOther || oldSessResp.Header.Get("Location") != "/login" {
		t.Fatalf("expected prior session to be revoked on recovery, got %d %q", oldSessResp.StatusCode, oldSessResp.Header.Get("Location"))
	}

	// 5e. Replaying the already-used recovery code #0 fails with 401!
	replayReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/login/recover", strings.NewReader(goodRecForm.Encode()))
	replayReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	replayReq.Header.Set("Origin", srv.URL)
	replayResp, err := http.DefaultClient.Do(replayReq)
	if err != nil {
		t.Fatalf("POST /login/recover replay: %v", err)
	}
	replayResp.Body.Close()
	if replayResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on replaying consumed recovery code, got %d", replayResp.StatusCode)
	}

	// 6. Regenerate recovery codes from /account -> invalidates remaining initialCodes[1..7]!
	mRec := csrfRe.FindStringSubmatch(goodRecHTML)
	if len(mRec) < 2 {
		t.Fatal("failed to extract csrf_token after recovery")
	}
	regenForm := url.Values{
		"csrf_token":       {mRec[1]},
		"current_password": {recoveredPw},
	}
	regenReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/account/recovery-codes", strings.NewReader(regenForm.Encode()))
	regenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	regenReq.Header.Set("Origin", srv.URL)
	regenResp, err := recClient.Do(regenReq)
	if err != nil {
		t.Fatalf("POST /account/recovery-codes: %v", err)
	}
	regenBytes, _ := io.ReadAll(regenResp.Body)
	regenResp.Body.Close()
	if regenResp.StatusCode != http.StatusOK || !strings.Contains(string(regenBytes), "New recovery codes generated") {
		t.Fatalf("expected regeneration to succeed, status=%d", regenResp.StatusCode)
	}

	// Old unused code #1 is now invalid.
	staleForm := url.Values{
		"email":         {"owner@example.test"},
		"recovery_code": {initialCodes[1]},
		"new_password":  {"AnotherNewPass-2026!"},
	}
	staleReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/login/recover", strings.NewReader(staleForm.Encode()))
	staleReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	staleReq.Header.Set("Origin", srv.URL)
	staleResp, err := http.DefaultClient.Do(staleReq)
	if err != nil {
		t.Fatalf("POST /login/recover stale code: %v", err)
	}
	staleResp.Body.Close()
	if staleResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on invalidated old recovery code, got %d", staleResp.StatusCode)
	}

	// 7. Owner-Admin staff password reset via /users/{id}/reset-password + cross-property isolation!
	propA, _ := store.CreateProperty(ctx, nil, domain.Property{
		ID: mustV7(), Slug: "reset-prop-a", Name: "Prop A", CountryCode: "GB",
		Timezone: "Europe/London", Currency: "GBP", ContactEmail: "a@example.test",
		CheckInTime: "15:00", CheckOutTime: "10:00",
	})
	propB, _ := store.CreateProperty(ctx, nil, domain.Property{
		ID: mustV7(), Slug: "reset-prop-b", Name: "Prop B", CountryCode: "GB",
		Timezone: "Europe/London", Currency: "GBP", ContactEmail: "b@example.test",
		CheckInTime: "15:00", CheckOutTime: "10:00",
	})
	globalActor := service.Actor{UserID: bootUser.ID, Email: bootUser.Email, Role: domain.RoleOwnerAdmin}
	scopedOwnerA, err := svc.CreateUser(ctx, globalActor, service.CreateUserInput{
		PropertyID: &propA.ID, Email: "owner.a@example.test", FullName: "Owner A",
		Role: domain.RoleOwnerAdmin, Password: "OwnerAPassword-2026!",
	})
	if err != nil {
		t.Fatalf("create scopedOwnerA: %v", err)
	}
	staffB, err := svc.CreateUser(ctx, globalActor, service.CreateUserInput{
		PropertyID: &propB.ID, Email: "staff.b@example.test", FullName: "Staff B",
		Role: domain.RoleStaff, Password: "StaffBInitial-2026!",
	})
	if err != nil {
		t.Fatalf("create staffB: %v", err)
	}

	// Property-scoped Owner A CANNOT reset Staff B's password on Property B!
	actorOwnerA := service.Actor{UserID: scopedOwnerA.ID, PropertyID: &propA.ID, Email: scopedOwnerA.Email, Role: domain.RoleOwnerAdmin}
	if err := svc.AdminResetUserPassword(ctx, actorOwnerA, staffB.ID, "CrossTenantAttempt-2026!"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden for cross-property AdminResetUserPassword, got %v", err)
	}

	// Global owner_admin resets Staff B via POST /users/{id}/reset-password -> forces MustRotatePassword=true on next login!
	const staffTempPw = "StaffTempReset-2026!"
	resetForm := url.Values{
		"csrf_token":         {mRec[1]},
		"temporary_password": {staffTempPw},
	}
	resetReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/users/"+staffB.ID.String()+"/reset-password", strings.NewReader(resetForm.Encode()))
	resetReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resetReq.Header.Set("Origin", srv.URL)
	resetResp, err := recClient.Do(resetReq)
	if err != nil {
		t.Fatalf("POST /users/{id}/reset-password: %v", err)
	}
	resetBytes, _ := io.ReadAll(resetResp.Body)
	resetResp.Body.Close()
	if resetResp.StatusCode != http.StatusOK || !strings.Contains(string(resetBytes), "Temporary password set") {
		t.Fatalf("expected staff password reset flash, status=%d", resetResp.StatusCode)
	}

	// Staff B logs in with temporary password -> immediately sent to /account?rotate=1!
	staffJar, _ := cookiejar.New(nil)
	staffNoRedir := &http.Client{
		Jar: staffJar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	staffLogin := url.Values{"email": {"staff.b@example.test"}, "password": {staffTempPw}}
	sReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/login", strings.NewReader(staffLogin.Encode()))
	sReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	sReq.Header.Set("Origin", srv.URL)
	sResp, err := staffNoRedir.Do(sReq)
	if err != nil {
		t.Fatalf("Staff B POST /login: %v", err)
	}
	sResp.Body.Close()
	if sResp.StatusCode != http.StatusSeeOther || sResp.Header.Get("Location") != "/account?rotate=1" {
		t.Fatalf("expected Staff B after admin reset to redirect to /account?rotate=1, got %d %q", sResp.StatusCode, sResp.Header.Get("Location"))
	}
}

func mustV7() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.New()
	}
	return id
}
