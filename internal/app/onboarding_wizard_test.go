package app_test

import (
	"archive/zip"
	"bytes"
	"context"
	"html"
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

	"github.com/Prestwickuk/lovelace/internal/app"
	"github.com/Prestwickuk/lovelace/internal/config"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/service"
	"github.com/Prestwickuk/lovelace/internal/store/postgres"
)

func TestGuidedPropertyOnboardingWizardAndMultiPropertySwitch(t *testing.T) {
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
		if _, txErr := tx.Exec(ctx, `SET LOCAL lovelace.test_reset_token = 'allow_test_reset'`); txErr != nil {
			return txErr
		}
		_, txErr := tx.Exec(ctx, `
			TRUNCATE TABLE backup_runs, backup_storage_targets,
			               admin_recovery_codes,
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
		PublicAPISecret:      "onboarding-wizard-test-bff-secret",
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

	// Bootstrap global owner_admin on an empty DB and rotate initial password.
	bootPw := "TempSetupPass!987654"
	permPw := "OwnerChosenPass!246810"
	if _, _, err := svc.BootstrapInitialAdmin(ctx, "owner@wizard.test", "Wizard Owner", bootPw); err != nil {
		t.Fatalf("BootstrapInitialAdmin: %v", err)
	}

	jar, _ := cookiejar.New(nil)
	followClient := &http.Client{Jar: jar}
	noRedirectClient := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	csrfRe := regexp.MustCompile(`name="csrf_token"\s+value="([^"]+)"`)
	extractCSRF := func(html string) string {
		t.Helper()
		m := csrfRe.FindStringSubmatch(html)
		if len(m) < 2 {
			t.Fatalf("failed to find csrf_token in HTML")
		}
		return m[1]
	}
	postForm := func(client *http.Client, path string, vals url.Values) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(vals.Encode()))
		if err != nil {
			t.Fatalf("new request %s: %v", path, err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", srv.URL)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, string(b)
	}

	// Sign in + rotate password -> /account displays Launch Property Setup Wizard CTA.
	_, _ = postForm(noRedirectClient, "/login", url.Values{
		"email":    {"owner@wizard.test"},
		"password": {bootPw},
	})
	acctResp, err := followClient.Get(srv.URL + "/account?rotate=1")
	if err != nil {
		t.Fatalf("GET /account?rotate=1: %v", err)
	}
	acctBytes, _ := io.ReadAll(acctResp.Body)
	acctResp.Body.Close()
	csrf := extractCSRF(string(acctBytes))

	_, rotatedHTML := postForm(followClient, "/account/password", url.Values{
		"csrf_token":       {csrf},
		"current_password": {bootPw},
		"new_password":     {permPw},
	})
	if !strings.Contains(rotatedHTML, "Launch Property Setup Wizard") {
		t.Fatalf("expected /account to display Launch Property Setup Wizard CTA on empty DB")
	}
	csrf = extractCSRF(rotatedHTML)

	// Fresh login when 0 properties exist redirects straight to /onboarding!
	jar2, _ := cookiejar.New(nil)
	noRedirect2 := &http.Client{
		Jar: jar2,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	loginResp2, _ := postForm(noRedirect2, "/login", url.Values{
		"email":    {"owner@wizard.test"},
		"password": {permPw},
	})
	if loginResp2.StatusCode != http.StatusSeeOther || loginResp2.Header.Get("Location") != "/onboarding" {
		t.Fatalf("expected empty-DB login to redirect to /onboarding, got %d %q", loginResp2.StatusCode, loginResp2.Header.Get("Location"))
	}

	// Step 1: Create Campsite property with blank slug (auto-derived).
	s1Get, err := followClient.Get(srv.URL + "/onboarding")
	if err != nil {
		t.Fatalf("GET /onboarding: %v", err)
	}
	s1Bytes, _ := io.ReadAll(s1Get.Body)
	s1Get.Body.Close()
	if s1Get.StatusCode != http.StatusOK || !strings.Contains(string(s1Bytes), "Step 1 — Property Type") {
		t.Fatalf("expected Step 1 wizard view, got status %d", s1Get.StatusCode)
	}
	csrf = extractCSRF(string(s1Bytes))

	s1PostResp, s2HTML := postForm(followClient, "/onboarding/step1", url.Values{
		"csrf_token":     {csrf},
		"preset":         {"campsite"},
		"name":           {"Meadowbrook Coastal Glamping"},
		"slug":           {""},
		"tagline":        {"Wildflower camping and bell tents near Market Town"},
		"town":           {"Market Town"},
		"region":         {"Countryside"},
		"postcode":       {"AB1 2CD"},
		"country_code":   {"GB"},
		"currency":       {"GBP"},
		"timezone":       {"Europe/London"},
		"check_in_time":  {"15:00"},
		"check_out_time": {"10:30"},
		"contact_email":  {"hello@meadowbrook.test"},
		"unit_singular":  {"Pitch"},
		"unit_plural":    {"Pitches"},
		"guest_singular": {"Guest"},
		"guest_plural":   {"Guests"},
		"stay_label":     {"Booking"},
	})
	if s1PostResp.StatusCode != http.StatusOK || !strings.Contains(s2HTML, "Step 2 — Add Accommodation") {
		t.Fatalf("expected Step 1 submit to advance to Step 2, got status %d", s1PostResp.StatusCode)
	}

	props, err := store.ListProperties(ctx)
	if err != nil || len(props) != 1 {
		t.Fatalf("expected 1 created property, got %d (err=%v)", len(props), err)
	}
	campProp := props[0]
	if campProp.Slug != "meadowbrook-coastal-glamping" {
		t.Fatalf("expected auto-derived slug meadowbrook-coastal-glamping, got %q", campProp.Slug)
	}

	// Visiting / mid-setup shows the Resume Guided Setup banner!
	dashMid, err := followClient.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET / mid-onboarding: %v", err)
	}
	dashMidBytes, _ := io.ReadAll(dashMid.Body)
	dashMid.Body.Close()
	if !strings.Contains(string(dashMidBytes), "Resume Guided Setup") {
		t.Fatalf("expected Resume Guided Setup banner on / while onboarding is incomplete")
	}

	// Step 2: Bulk-generate 6 Meadow Grass Pitches (MP-01..MP-06) at £28.50/night!
	csrf = extractCSRF(s2HTML)
	s2PostResp, s3HTML := postForm(followClient, "/onboarding/step2/unit-batch", url.Values{
		"csrf_token":           {csrf},
		"property_id":          {campProp.ID.String()},
		"name":                 {"Meadow Grass Pitch"},
		"category":             {"tent_pitch"},
		"quantity":             {"6"},
		"unit_prefix":          {"MP"},
		"zone":                 {"Main Meadow"},
		"base_nightly_rate":    {"28.50"},
		"base_capacity_adults": {"2"},
		"max_adults":           {"4"},
		"max_children":         {"4"},
		"max_infants":          {"2"},
		"max_guests":           {"6"},
		"min_nights":           {"1"},
		"max_nights":           {"21"},
		"pets_allowed":         {"true"},
		"max_pets":             {"2"},
		"amenities":            {"hot-showers, fire-pit"},
		"after_action":         {"continue"},
	})
	if s2PostResp.StatusCode != http.StatusOK || !strings.Contains(s3HTML, "Step 3 — Pricing") {
		t.Fatalf("expected Step 2 batch submit to advance to Step 3, got status %d", s2PostResp.StatusCode)
	}

	units, err := store.ListUnits(ctx, campProp.ID, nil, false)
	if err != nil || len(units) != 6 {
		t.Fatalf("expected 6 auto-generated physical pitches, got %d (err=%v)", len(units), err)
	}
	if units[0].Code != "MP-01" || units[5].Code != "MP-06" {
		t.Fatalf("expected MP-01..MP-06, got %q..%q", units[0].Code, units[5].Code)
	}

	// Step 3: Add weekend peak rate rule + suggested dog-stay extra + custom EV extra.
	csrf = extractCSRF(s3HTML)
	s3PostResp, s4HTML := postForm(followClient, "/onboarding/step3", url.Values{
		"csrf_token":                 {csrf},
		"property_id":                {campProp.ID.String()},
		"extra_adult_nightly":        {"8.00"},
		"child_nightly":              {"4.50"},
		"infant_nightly":             {"0.00"},
		"add_peak_rule":              {"true"},
		"peak_rule_name":             {"Summer Weekend Pitch"},
		"peak_start_date":            {"2027-07-01"},
		"peak_end_date":              {"2027-09-01"},
		"peak_days_preset":           {"weekend"},
		"peak_nightly_rate":          {"36.00"},
		"peak_min_nights":            {"2"},
		"suggested_extras":           {"dog-stay"},
		"extra_price_dog-stay":       {"6.00"},
		"custom_extra_name":          {"EV Charger Pass"},
		"custom_extra_price":         {"15.00"},
		"custom_extra_pricing_model": {"per_stay"},
		"custom_extra_max_quantity":  {"1"},
	})
	if s3PostResp.StatusCode != http.StatusOK || !strings.Contains(s4HTML, "Step 4 — Deposits") {
		t.Fatalf("expected Step 3 submit to advance to Step 4, got status %d", s3PostResp.StatusCode)
	}

	extras, err := store.ListExtras(ctx, campProp.ID, false)
	if err != nil || len(extras) != 2 {
		t.Fatalf("expected 2 extras created in Step 3, got %d (err=%v)", len(extras), err)
	}
	rules, err := store.ListRateRules(ctx, campProp.ID, nil, nil)
	if err != nil || len(rules) != 1 || rules[0].NightlyRatePence != 3600 {
		t.Fatalf("expected 1 rate rule at 3600p, got %+v (err=%v)", rules, err)
	}

	// Step 4: Save 30% deposit policy + house rules; verify Stripe & Guest Portal stay dark (false).
	csrf = extractCSRF(s4HTML)
	s4PostResp, s5HTML := postForm(followClient, "/onboarding/step4", url.Values{
		"csrf_token":                      {csrf},
		"property_id":                     {campProp.ID.String()},
		"deposit_mode":                    {"PERCENTAGE"},
		"deposit_percentage":              {"30"},
		"deposit_fixed":                   {"0.00"},
		"balance_due_days_before_arrival": {"21"},
		"hold_duration_minutes":           {"20"},
		"booking_terms_version":           {"v1.0"},
		"check_in_time":                   {"14:30"},
		"check_out_time":                  {"10:30"},
		"cancellation_policy":             {"Free cancellation up to 14 days prior."},
		"quiet_hours":                     {"22:30 to 07:30."},
		"fire_policy":                     {"Raised fire-pits only."},
		"pet_policy":                      {"Max 2 dogs on leads."},
		"privacy_notice":                  {"Used only for managing your stay."},
	})
	if s4PostResp.StatusCode != http.StatusOK || !strings.Contains(s5HTML, "Step 5 — Team") {
		t.Fatalf("expected Step 4 submit to advance to Step 5, got status %d", s4PostResp.StatusCode)
	}

	pol, err := store.GetBookingPolicy(ctx, nil, campProp.ID)
	if err != nil {
		t.Fatalf("GetBookingPolicy: %v", err)
	}
	if pol.DepositMode != "PERCENTAGE" || pol.DepositPercentage != 30 || pol.StripeEnabled || pol.GuestPortalEnabled {
		t.Fatalf("unexpected policy state (must stay dark by default): %+v", pol)
	}

	// Step 5: Add a housekeeping team member + advance to Step 6.
	csrf = extractCSRF(s5HTML)
	staffInitPw := "StaffTempPass!135790"
	_, s5AfterUser := postForm(followClient, "/onboarding/step5/user", url.Values{
		"csrf_token":  {csrf},
		"property_id": {campProp.ID.String()},
		"full_name":   {"Sam Housekeeper"},
		"email":       {"sam@meadowbrook.test"},
		"role":        {"housekeeping_staff"},
		"password":    {staffInitPw},
	})
	if !strings.Contains(s5AfterUser, "Sam Housekeeper") {
		t.Fatalf("expected newly added staff user to appear in Step 5 table")
	}

	csrf = extractCSRF(s5AfterUser)
	_, s6HTML := postForm(followClient, "/onboarding/step5/next", url.Values{
		"csrf_token":  {csrf},
		"property_id": {campProp.ID.String()},
	})
	if !strings.Contains(s6HTML, "Step 6 — Off-Site Cloud Backup Source") {
		t.Fatalf("expected Step 6 optional cloud backup step")
	}

	// Step 6: Configure an optional S3-compatible off-site cloud backup destination, then advance to Step 7.
	csrf = extractCSRF(s6HTML)
	_, s6SavedHTML := postForm(followClient, "/onboarding/step6/backup-target", url.Values{
		"csrf_token":              {csrf},
		"property_id":             {campProp.ID.String()},
		"name":                    {"Meadowbrook R2 Off-Site"},
		"provider":                {"S3_COMPATIBLE"},
		"bucket_or_container":     {"meadowbrook-backups"},
		"key_prefix":              {"nightly/"},
		"region":                  {"auto"},
		"endpoint_url":            {"https://demo-account.r2.cloudflarestorage.com"},
		"access_key_id":           {"AKIAIOSFODNN7EXAMPLE"},
		"secret_access_key":       {"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"},
		"encrypt_archive":         {"true"},
		"archive_passphrase":      {"ArchivePassphrase!13579"},
		"schedule_enabled":        {"true"},
		"schedule_interval_hours": {"24"},
		"retention_count":         {"14"},
		"current_password":        {permPw},
		"after_action":            {"add_more"},
	})
	if !strings.Contains(s6SavedHTML, "Meadowbrook R2 Off-Site") || strings.Contains(s6SavedHTML, "wJalrXUtnFEMI") {
		t.Fatalf("expected saved cloud backup target in Step 6 without leaking secret access key")
	}

	csrf = extractCSRF(s6SavedHTML)
	_, s7HTML := postForm(followClient, "/onboarding/step6/next", url.Values{
		"csrf_token":  {csrf},
		"property_id": {campProp.ID.String()},
	})
	s7Decoded := html.UnescapeString(s7HTML)
	for _, want := range []string{
		"Step 7 — Review Summary, Website Booking Code",
		"Cloud Sync Ready",
		"http://127.0.0.1:3000/embed/meadowbrook-coastal-glamping",
		`<input type="hidden" name="property" value="meadowbrook-coastal-glamping">`,
		"lovelace:embed:resize",
	} {
		if !strings.Contains(s7Decoded, want) {
			t.Fatalf("expected Step 7 decoded HTML to contain %q", want)
		}
	}

	// Step 7: Finish setup -> redirects to Operations Centre without resume banner!
	csrf = extractCSRF(s7HTML)
	doneResp, opsHTML := postForm(followClient, "/onboarding/complete", url.Values{
		"csrf_token":  {csrf},
		"property_id": {campProp.ID.String()},
	})
	if doneResp.StatusCode != http.StatusOK || strings.Contains(opsHTML, "Resume Guided Setup") {
		t.Fatalf("expected completed setup to land on Operations Centre without resume banner")
	}

	// Verify /property exposes Website Booking Section copy-paste code blocks AND preserves onboarding completion state on save.
	propPageResp, _ := followClient.Get(srv.URL + "/property")
	propPageBytes, _ := io.ReadAll(propPageResp.Body)
	propPageResp.Body.Close()
	propPageHTML := string(propPageBytes)
	for _, want := range []string{
		`id="website-embed"`,
		`id="wordpress-plugin"`,
		`data-copy-target="embed-iframe-code"`,
		`data-copy-target="embed-searchbar-code"`,
		`data-copy-target="embed-button-code"`,
		"http://127.0.0.1:3000/embed/meadowbrook-coastal-glamping",
	} {
		if !strings.Contains(propPageHTML, want) {
			t.Fatalf("expected /property HTML to contain %q", want)
		}
	}
	csrf = extractCSRF(propPageHTML)

	// Verify downloading the pre-configured WordPress plugin .zip returns a valid archive with property defaults injected.
	wpZipResp, err := followClient.Get(srv.URL + "/property/wordpress-plugin.zip?property_id=" + campProp.ID.String())
	if err != nil {
		t.Fatalf("GET /property/wordpress-plugin.zip: %v", err)
	}
	wpZipBytes, _ := io.ReadAll(wpZipResp.Body)
	wpZipResp.Body.Close()
	if wpZipResp.StatusCode != http.StatusOK || wpZipResp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("expected 200 application/zip from /property/wordpress-plugin.zip, got %d %q", wpZipResp.StatusCode, wpZipResp.Header.Get("Content-Type"))
	}
	if !strings.Contains(wpZipResp.Header.Get("Content-Disposition"), "lovelace-booking-meadowbrook-coastal-glamping.zip") {
		t.Fatalf("unexpected Content-Disposition: %q", wpZipResp.Header.Get("Content-Disposition"))
	}
	wpZr, err := zip.NewReader(bytes.NewReader(wpZipBytes), int64(len(wpZipBytes)))
	if err != nil {
		t.Fatalf("zip.NewReader(wpZipBytes): %v", err)
	}
	foundDefaults := false
	for _, zf := range wpZr.File {
		if zf.Name == "lovelace-booking/includes/bundled-defaults.php" {
			foundDefaults = true
			rc, _ := zf.Open()
			rawPHP, _ := io.ReadAll(rc)
			_ = rc.Close()
			phpText := string(rawPHP)
			if !strings.Contains(phpText, "'http://127.0.0.1:3000'") || !strings.Contains(phpText, "'meadowbrook-coastal-glamping'") {
				t.Fatalf("expected pre-configured origin & slug inside bundled-defaults.php, got:\n%s", phpText)
			}
		}
	}
	if !foundDefaults {
		t.Fatalf("lovelace-booking/includes/bundled-defaults.php missing from WordPress plugin .zip")
	}
	freshCamp, _ := store.GetPropertyByID(ctx, nil, campProp.ID)
	_, _ = postForm(followClient, "/property", url.Values{
		"csrf_token":     {csrf},
		"version":        {"5"},
		"name":           {freshCamp.Name},
		"tagline":        {"Updated tagline"},
		"region":         {freshCamp.Region},
		"country_code":   {freshCamp.CountryCode},
		"town":           {freshCamp.Town},
		"postcode":       {freshCamp.Postcode},
		"timezone":       {freshCamp.Timezone},
		"currency":       {freshCamp.Currency},
		"check_in_time":  {freshCamp.CheckInTime},
		"check_out_time": {freshCamp.CheckOutTime},
		"contact_email":  {freshCamp.ContactEmail},
	})
	afterPropEdit, err := store.GetPropertyByID(ctx, nil, campProp.ID)
	if err != nil {
		t.Fatalf("GetPropertyByID: %v", err)
	}
	if st, ok := domain.DecodeOnboardingState(afterPropEdit.Settings); !ok || !st.IsComplete() {
		t.Fatalf("expected Property.Settings onboarding completion to be preserved after /property edit, got %+v (ok=%v)", st, ok)
	}

	// Add a SECOND property (Holiday Cottage / Airbnb) via /onboarding?new=1 and test switching!
	newPropGet, _ := followClient.Get(srv.URL + "/onboarding?new=1&preset=airbnb_cottage")
	newPropBytes, _ := io.ReadAll(newPropGet.Body)
	newPropGet.Body.Close()
	csrf = extractCSRF(string(newPropBytes))
	_, s2CottageHTML := postForm(followClient, "/onboarding/step1", url.Values{
		"csrf_token":     {csrf},
		"preset":         {"airbnb_cottage"},
		"name":           {"Harbour View Cottage"},
		"slug":           {"harbour-view-cottage"},
		"country_code":   {"GB"},
		"currency":       {"GBP"},
		"timezone":       {"Europe/London"},
		"check_in_time":  {"16:00"},
		"check_out_time": {"10:00"},
		"contact_email":  {"cottage@meadowbrook.test"},
		"unit_singular":  {"Property"},
		"unit_plural":    {"Properties"},
		"stay_label":     {"Stay"},
	})
	if !strings.Contains(s2CottageHTML, "Harbour View Cottage") {
		t.Fatalf("expected second property Harbour View Cottage in Step 2")
	}
	allProps, _ := store.ListProperties(ctx)
	if len(allProps) != 2 {
		t.Fatalf("expected 2 properties in DB, got %d", len(allProps))
	}

	// Switch back to first property via POST /property/switch.
	csrf = extractCSRF(s2CottageHTML)
	_, switchedHTML := postForm(followClient, "/property/switch", url.Values{
		"csrf_token":  {csrf},
		"property_id": {campProp.ID.String()},
		"return_to":   {"/property"},
	})
	if !strings.Contains(switchedHTML, "meadowbrook-coastal-glamping") {
		t.Fatalf("expected active property to switch back to Meadowbrook Coastal Glamping")
	}

	// Verify non-owner staff cannot access /onboarding.
	staffJar, _ := cookiejar.New(nil)
	staffClient := &http.Client{Jar: staffJar}
	_, staffLoginHTML := postForm(staffClient, "/login", url.Values{
		"email":    {"sam@meadowbrook.test"},
		"password": {staffInitPw},
	})
	staffCSRF := extractCSRF(staffLoginHTML)
	_, _ = postForm(staffClient, "/account/password", url.Values{
		"csrf_token":       {staffCSRF},
		"current_password": {staffInitPw},
		"new_password":     {"SamPersonalPass!246810"},
	})
	staffWizResp, err := staffClient.Get(srv.URL + "/onboarding")
	if err != nil {
		t.Fatalf("staff GET /onboarding: %v", err)
	}
	staffWizResp.Body.Close()
	if staffWizResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when housekeeping_staff accesses /onboarding, got %d", staffWizResp.StatusCode)
	}
}
