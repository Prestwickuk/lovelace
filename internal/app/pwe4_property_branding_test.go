package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/branding"
	"github.com/Prestwickuk/lovelace/internal/channels"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/service"
	"github.com/Prestwickuk/lovelace/internal/storage"
)

func buildValidTestPNG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	return buf.Bytes()
}

func buildValidTestJPEGWithComment(t *testing.T, w, h int, comment string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 107, G: 78, B: 20, A: 255})
		}
	}
	var raw bytes.Buffer
	if err := jpeg.Encode(&raw, img, &jpeg.Options{Quality: 88}); err != nil {
		t.Fatalf("jpeg encode: %v", err)
	}
	b := raw.Bytes()
	var withComment bytes.Buffer
	withComment.Write(b[:2])
	segLen := len(comment) + 2
	withComment.Write([]byte{0xFF, 0xFE, byte(segLen >> 8), byte(segLen)})
	withComment.WriteString(comment)
	withComment.Write(b[2:])
	return withComment.Bytes()
}

func countFilesInDir(t *testing.T, root string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk blob dir: %v", err)
	}
	return count
}

func uploadMultipartLogoAPI(
	t *testing.T,
	client *http.Client,
	targetURL, csrfToken, fieldName, filename string,
	payload []byte,
	version string,
) (*http.Response, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if version != "" {
		_ = mw.WriteField("version", version)
		_ = mw.WriteField("branding_version", version)
	}
	if csrfToken != "" {
		_ = mw.WriteField("csrf_token", csrfToken)
	}
	part, err := mw.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, targetURL, &body)
	if err != nil {
		t.Fatalf("new upload req: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if csrfToken != "" {
		req.Header.Set("X-CSRF-Token", csrfToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do upload req: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

func TestPWE4_PropertyBrandingSecurityContrastLogosAndBoundaries(t *testing.T) {
	srv, migratorStore, appSvc, propA, _, _ := setupPhase4AppServer(t, "pwe4-suite", channels.AdapterConfig{})
	defer srv.Close()
	ctx := context.Background()

	blobDir := t.TempDir()
	bs, err := storage.NewLocalFSStore(blobDir)
	if err != nil {
		t.Fatalf("NewLocalFSStore: %v", err)
	}
	appSvc.SetBlobStore(bs)

	var ownerID uuid.UUID
	if err := migratorStore.Pool().QueryRow(ctx, `SELECT id FROM admin_users WHERE email = 'owner@example.com'`).Scan(&ownerID); err != nil {
		t.Fatalf("find owner: %v", err)
	}
	globalOwnerActor := service.Actor{UserID: ownerID, PropertyID: nil, Email: "owner@example.com", Role: domain.RoleOwnerAdmin}

	// Second property for cross-property isolation checks.
	propBID := uuid.New()
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO properties (id, slug, name, region, timezone, check_in_time, check_out_time, currency)
		VALUES ($1, 'pwe4-prop-b', 'Coastal Haven B', 'Gower', 'Europe/London', '15:00', '10:00', 'GBP')
	`, propBID); err != nil {
		t.Fatalf("insert prop B: %v", err)
	}

	// Create property-A owner_admin, property-A manager (read-only property perm), and property-B owner_admin.
	adminA, err := appSvc.CreateUser(ctx, globalOwnerActor, service.CreateUserInput{
		PropertyID: &propA.ID, Email: "pwe4.admina@example.com", FullName: "PWE4 Admin A", Role: domain.RoleOwnerAdmin, Password: "Pwe4AdminPass!2026",
	})
	if err != nil {
		t.Fatalf("create adminA: %v", err)
	}
	mgrA, err := appSvc.CreateUser(ctx, globalOwnerActor, service.CreateUserInput{
		PropertyID: &propA.ID, Email: "pwe4.mgra@example.com", FullName: "PWE4 Manager A", Role: domain.RoleManager, Password: "Pwe4ManagerPass!2026",
	})
	if err != nil {
		t.Fatalf("create mgrA: %v", err)
	}
	adminB, err := appSvc.CreateUser(ctx, globalOwnerActor, service.CreateUserInput{
		PropertyID: &propBID, Email: "pwe4.adminb@example.com", FullName: "PWE4 Admin B", Role: domain.RoleOwnerAdmin, Password: "Pwe4AdminBPass!2026",
	})
	if err != nil {
		t.Fatalf("create adminB: %v", err)
	}
	rotUser, err := appSvc.CreateUser(ctx, globalOwnerActor, service.CreateUserInput{
		PropertyID: &propA.ID, Email: "pwe4.rotate@example.com", FullName: "Rotate User", Role: domain.RoleOwnerAdmin, Password: "Pwe4RotatePass!2026", MustRotatePassword: true,
	})
	if err != nil {
		t.Fatalf("create rotUser: %v", err)
	}

	clientAdminA, csrfAdminA := loginAdminClient(t, srv.URL, adminA.Email, "Pwe4AdminPass!2026")
	clientMgrA, csrfMgrA := loginAdminClient(t, srv.URL, mgrA.Email, "Pwe4ManagerPass!2026")
	clientAdminB, csrfAdminB := loginAdminClient(t, srv.URL, adminB.Email, "Pwe4AdminBPass!2026")
	clientRot, csrfRot := loginAdminClient(t, srv.URL, rotUser.Email, "Pwe4RotatePass!2026")

	// Set non-default PWE-2 preferences and PWE-3 workspace state for Admin A to verify zero interference.
	_, _ = apiJSONRequest(t, clientAdminA, http.MethodPut, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/workspace/dashboard", csrfAdminA, domain.DashboardLayout{
		SchemaVersion: 1,
		Widgets: []domain.DashboardWidgetInstance{
			{InstanceID: "w-pwe4-keep", WidgetID: domain.WidgetReportsKPISnapshot, Size: domain.WidgetSizeStandard, Position: 1},
		},
	})
	pwe2Form := url.Values{
		"csrf_token":    {csrfAdminA},
		"theme_mode":    {branding.ThemeModeDark},
		"accent":        {branding.ThemeAccentBrass},
		"density":       {branding.ThemeDensityCompact},
		"text_scale":    {branding.ThemeTextScaleLarge},
		"default_route": {"/"},
	}
	rPWE2, bodyPWE2 := postWebForm(t, clientAdminA, srv.URL+"/account/preferences/global", pwe2Form)
	if rPWE2.StatusCode != http.StatusOK || !strings.Contains(bodyPWE2, `data-theme="dark"`) {
		t.Fatalf("failed saving baseline PWE-2 preferences status=%d", rPWE2.StatusCode)
	}

	// 1. Unauthenticated checks
	anonClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	rAnonAPI, _ := apiJSONRequest(t, anonClient, http.MethodGet, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/branding", "", nil)
	if rAnonAPI.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated branding API, got %d", rAnonAPI.StatusCode)
	}
	rAnonCSS, err := anonClient.Get(srv.URL + "/branding/property.css")
	if err != nil {
		t.Fatalf("anon CSS req: %v", err)
	}
	rAnonCSS.Body.Close()
	if rAnonCSS.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected redirect for unauthenticated /branding/property.css, got %d", rAnonCSS.StatusCode)
	}

	// 2. PermPropertyWrite, CSRF, MustRotatePassword, and cross-property scope enforcement
	rMgrRead, _ := apiJSONRequest(t, clientMgrA, http.MethodGet, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/branding", "", nil)
	if rMgrRead.StatusCode != http.StatusOK {
		t.Fatalf("expected manager to read branding (200), got %d", rMgrRead.StatusCode)
	}
	rMgrWrite, _ := apiJSONRequest(t, clientMgrA, http.MethodPut, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/branding", csrfMgrA, map[string]any{
		"version": 1, "primary_color": "#1E4D3B",
	})
	if rMgrWrite.StatusCode != http.StatusForbidden {
		t.Fatalf("expected manager write forbidden (403), got %d", rMgrWrite.StatusCode)
	}

	rNoCSRF, _ := apiJSONRequest(t, clientAdminA, http.MethodPut, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/branding", "", map[string]any{
		"version": 1, "primary_color": "#1E4D3B",
	})
	if rNoCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected missing CSRF forbidden (403), got %d", rNoCSRF.StatusCode)
	}

	rCrossProp, _ := apiJSONRequest(t, clientAdminB, http.MethodPut, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/branding", csrfAdminB, map[string]any{
		"version": 1, "primary_color": "#1E4D3B",
	})
	if rCrossProp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected cross-property branding write forbidden (403), got %d", rCrossProp.StatusCode)
	}

	rRot, _ := apiJSONRequest(t, clientRot, http.MethodPut, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/branding", csrfRot, map[string]any{
		"version": 1, "primary_color": "#1E4D3B",
	})
	if rRot.StatusCode != http.StatusForbidden && rRot.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("expected password-rotation user blocked on branding write, got %d", rRot.StatusCode)
	}

	// 3. Reject invalid hex and failing WCAG contrast pairings server-side
	badPalettes := []map[string]any{
		{"version": 1, "primary_color": "#2F4", "secondary_color": "#6B4E14"},
		{"version": 1, "primary_color": "#1E4D3B;background:red", "secondary_color": "#6B4E14"},
		{"version": 1, "primary_color": "#A8DADC", "secondary_color": "#6B4E14"}, // pale pastel
		{"version": 1, "primary_color": "#FFFF00", "secondary_color": "#6B4E14"}, // low contrast yellow
		{"version": 1, "primary_color": "#1E4D3B", "secondary_color": "#C49A45"}, // fails 4.5:1 on white card
	}
	for _, bad := range badPalettes {
		rBad, _ := apiJSONRequest(t, clientAdminA, http.MethodPut, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/branding", csrfAdminA, bad)
		if rBad.StatusCode != http.StatusUnprocessableEntity && rBad.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected low-contrast/bad hex %v rejected (400/422), got %d", bad, rBad.StatusCode)
		}
	}

	// 4. Valid Save: custom #RRGGBB palette + bounded admin terminology
	rSave, saveBody := apiJSONRequest(t, clientAdminA, http.MethodPut, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/branding", csrfAdminA, map[string]any{
		"version":         1,
		"display_name":    "Meadowbrook Sanctuary",
		"tagline":         "Countryside Meadow & Glamping",
		"region_label":    "Coastal Valley",
		"primary_color":   "#1e4d3b",
		"secondary_color": "#6b2737",
		"logo_alt":        "Meadowbrook Sanctuary Crest",
		"header_label":    "Selected Sanctuary",
		"terminology": map[string]string{
			"unit_singular":  "Pitch",
			"unit_plural":    "Pitches",
			"guest_singular": "Camper",
			"guest_plural":   "Campers",
			"stay_label":     "Booking",
		},
	})
	if rSave.StatusCode != http.StatusOK {
		t.Fatalf("expected save branding 200, got %d: %s", rSave.StatusCode, saveBody)
	}
	var savedView domain.PropertyBrandingView
	if err := json.Unmarshal([]byte(saveBody), &savedView); err != nil {
		t.Fatalf("decode saved view: %v", err)
	}
	if savedView.PrimaryColor != "#1E4D3B" || savedView.SecondaryColor != "#6B2737" || savedView.Version != 2 {
		t.Fatalf("unexpected saved normalized branding view: %+v", savedView)
	}

	// 5. Verify same-origin /branding/property.css CSP safety & custom token output
	respCSS, cssBody := getWebPage(t, clientAdminA, srv.URL+"/branding/property.css")
	if respCSS.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 CSS, got %d", respCSS.StatusCode)
	}
	if ct := respCSS.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("expected text/css header, got %q", ct)
	}
	if respCSS.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("expected nosniff on property CSS")
	}
	if !strings.Contains(cssBody, "--property-brand: #1E4D3B;") || strings.Contains(cssBody, "--lovelace-") || strings.Contains(cssBody, "--focus-ring") {
		t.Fatalf("unexpected CSS content:\n%s", cssBody)
	}

	// 6. Verify preview mode is strictly non-destructive
	previewResp, previewHTML := getWebPage(t, clientAdminA, srv.URL+"/property?preview=1&display_name=Draft+Harbour&primary_color=%232E475D&secondary_color=%235B3A29&unit_plural=Cabins")
	if previewResp.StatusCode != http.StatusOK || !strings.Contains(previewHTML, "Unsaved preview mode") || !strings.Contains(previewHTML, "Cabins") {
		t.Fatalf("expected unsaved preview UI on /property, status=%d", previewResp.StatusCode)
	}
	_, previewCSS := getWebPage(t, clientAdminA, srv.URL+"/branding/property.css?preview=1&primary_color=%232E475D&secondary_color=%235B3A29")
	if !strings.Contains(previewCSS, "--property-brand: #2E475D;") {
		t.Fatalf("expected preview CSS token #2E475D, got:\n%s", previewCSS)
	}
	// Saved branding and public API MUST remain untouched at #1E4D3B / Meadowbrook Sanctuary!
	_, pubRespAfterPreview := apiJSONRequest(t, anonClient, http.MethodGet, srv.URL+"/api/public/v1/properties/demo-property", "", nil)
	if !strings.Contains(pubRespAfterPreview, "Meadowbrook Sanctuary") || strings.Contains(pubRespAfterPreview, "Draft Harbour") || strings.Contains(pubRespAfterPreview, "#2E475D") {
		t.Fatalf("preview unexpectedly mutated public API state: %s", pubRespAfterPreview)
	}

	// 7. Verify Admin terminology applies only to admin SSR & LOVELACE login stays immutable
	_, unitsHTML := getWebPage(t, clientAdminA, srv.URL+"/units")
	_, guestsHTML := getWebPage(t, clientAdminA, srv.URL+"/guests")
	_, resHTML := getWebPage(t, clientAdminA, srv.URL+"/reservations")
	if !strings.Contains(unitsHTML, "Physical Pitches") || !strings.Contains(guestsHTML, "Camper Directory") || !strings.Contains(resHTML, "Bookings") {
		t.Fatalf("expected bounded admin terminology overrides across /units, /guests, /reservations")
	}
	// Login screen (signed out client) must remain permanent LOVELACE without property CSS
	_, loginHTML := getWebPage(t, anonClient, srv.URL+"/login")
	if !strings.Contains(loginHTML, "LOVELACE") || strings.Contains(loginHTML, "/branding/property.css") || strings.Contains(loginHTML, "Pitches") {
		t.Fatalf("login page compromised LOVELACE boundary:\n%s", loginHTML)
	}

	// Public API response must NOT contain admin terminology, header_label, or internal keys.
	for _, forbiddenPublicToken := range []string{`"terminology"`, "Pitches", "Camper", "Selected Sanctuary", "logo_storage_key", "admin_branding"} {
		if strings.Contains(pubRespAfterPreview, forbiddenPublicToken) {
			t.Fatalf("public property JSON leaked admin/internal field %q: %s", forbiddenPublicToken, pubRespAfterPreview)
		}
	}

	// 8. Verify SVG / disguised logo rejections
	rSVG, _ := uploadMultipartLogoAPI(t, clientAdminA, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/branding/logo", csrfAdminA, "file", "evil.png", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), "2")
	if rSVG.StatusCode == http.StatusOK {
		t.Fatalf("expected SVG disguised as .png to be rejected")
	}

	// 9. Upload PNG logo -> verify BlobStore, admin logo, public logo, headers, metadata
	validPNG := buildValidTestPNG(t, 64, 48, color.RGBA{R: 30, G: 77, B: 59, A: 255})
	rUp1, up1Body := uploadMultipartLogoAPI(t, clientAdminA, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/branding/logo", csrfAdminA, "file", "crest.png", validPNG, "2")
	if rUp1.StatusCode != http.StatusOK {
		t.Fatalf("expected logo upload 200, got %d: %s", rUp1.StatusCode, up1Body)
	}
	if countFilesInDir(t, blobDir) != 1 {
		t.Fatalf("expected exactly 1 blob file after logo upload, found %d", countFilesInDir(t, blobDir))
	}

	pubLogoResp, err := anonClient.Get(srv.URL + "/api/public/v1/properties/demo-property/logo?v=1")
	if err != nil {
		t.Fatalf("GET public logo: %v", err)
	}
	pubLogoBytes, _ := io.ReadAll(pubLogoResp.Body)
	pubLogoResp.Body.Close()
	if pubLogoResp.StatusCode != http.StatusOK || pubLogoResp.Header.Get("Content-Type") != "image/png" || pubLogoResp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unexpected public logo headers/status=%d ct=%q nosniff=%q", pubLogoResp.StatusCode, pubLogoResp.Header.Get("Content-Type"), pubLogoResp.Header.Get("X-Content-Type-Options"))
	}
	if _, _, err := image.Decode(bytes.NewReader(pubLogoBytes)); err != nil {
		t.Fatalf("served public logo failed PNG decode: %v", err)
	}

	// Stale optimistic lock version must NOT leave an orphan blob in blobDir!
	rStaleUp, _ := uploadMultipartLogoAPI(t, clientAdminA, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/branding/logo", csrfAdminA, "file", "stale.png", validPNG, "1")
	if rStaleUp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 conflict on stale version upload, got %d", rStaleUp.StatusCode)
	}
	if countFilesInDir(t, blobDir) != 1 {
		t.Fatalf("stale upload left orphan blob behind (count=%d)", countFilesInDir(t, blobDir))
	}

	// Replace with JPEG containing GPS/EXIF comment -> strips EXIF, deletes old PNG blob, serves clean JPEG!
	jpgWithEXIF := buildValidTestJPEGWithComment(t, 80, 48, "PRIVATE_CAMERA_SERIAL_999")
	rUp2, up2Body := uploadMultipartLogoAPI(t, clientAdminA, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/branding/logo", csrfAdminA, "file", "crest-new.jpg", jpgWithEXIF, "3")
	if rUp2.StatusCode != http.StatusOK {
		t.Fatalf("expected logo replace 200, got %d: %s", rUp2.StatusCode, up2Body)
	}
	if countFilesInDir(t, blobDir) != 1 {
		t.Fatalf("expected old blob deleted cleanly on replace (count=%d)", countFilesInDir(t, blobDir))
	}
	pubJpgResp, _ := anonClient.Get(srv.URL + "/api/public/v1/properties/demo-property/logo?v=2")
	pubJpgBytes, _ := io.ReadAll(pubJpgResp.Body)
	pubJpgResp.Body.Close()
	if pubJpgResp.StatusCode != http.StatusOK || pubJpgResp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("expected image/jpeg public logo, got %d %q", pubJpgResp.StatusCode, pubJpgResp.Header.Get("Content-Type"))
	}
	if bytes.Contains(pubJpgBytes, []byte("PRIVATE_CAMERA_SERIAL_999")) {
		t.Fatalf("public JPEG logo retained unstripped EXIF comment")
	}

	// Delete logo -> removes blob & returns 404 publicly
	rDel, delBody := apiJSONRequest(t, clientAdminA, http.MethodDelete, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/branding/logo?version=4", csrfAdminA, nil)
	if rDel.StatusCode != http.StatusOK {
		t.Fatalf("expected logo delete 200, got %d: %s", rDel.StatusCode, delBody)
	}
	if countFilesInDir(t, blobDir) != 0 {
		t.Fatalf("expected 0 blob files after delete, got %d", countFilesInDir(t, blobDir))
	}
	pubGoneResp, _ := anonClient.Get(srv.URL + "/api/public/v1/properties/demo-property/logo")
	pubGoneResp.Body.Close()
	if pubGoneResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after logo deletion, got %d", pubGoneResp.StatusCode)
	}

	// Verify SSR form logo upload (/property/branding/logo) & GET /property/logo & SSR remove (/property/branding/logo/delete)
	rWebUp, _ := uploadMultipartLogoAPI(t, clientAdminA, srv.URL+"/property/branding/logo", csrfAdminA, "logo", "webcrest.png", validPNG, "5")
	if rWebUp.StatusCode != http.StatusOK {
		t.Fatalf("expected SSR logo upload redirect->200, got %d", rWebUp.StatusCode)
	}
	adminLogoResp, _ := clientAdminA.Get(srv.URL + "/property/logo?v=3")
	adminLogoBytes, _ := io.ReadAll(adminLogoResp.Body)
	adminLogoResp.Body.Close()
	if adminLogoResp.StatusCode != http.StatusOK || adminLogoResp.Header.Get("Content-Type") != "image/png" || len(adminLogoBytes) == 0 {
		t.Fatalf("expected SSR /property/logo image/png, got status=%d ct=%q", adminLogoResp.StatusCode, adminLogoResp.Header.Get("Content-Type"))
	}
	rWebDel, _ := postWebForm(t, clientAdminA, srv.URL+"/property/branding/logo/delete", url.Values{
		"csrf_token":       {csrfAdminA},
		"branding_version": {"6"},
	})
	if rWebDel.StatusCode != http.StatusOK || countFilesInDir(t, blobDir) != 0 {
		t.Fatalf("expected SSR logo delete->200 and 0 blobs, status=%d count=%d", rWebDel.StatusCode, countFilesInDir(t, blobDir))
	}

	// 10. Verify PWE-2 preferences & PWE-3 dashboard layout remained untouched + sanitized audit logs exist
	_, accountHTML := getWebPage(t, clientAdminA, srv.URL+"/account")
	if !strings.Contains(accountHTML, `data-theme="dark"`) || !strings.Contains(accountHTML, `data-accent="brass"`) {
		t.Fatalf("property branding unexpectedly altered PWE-2 user theme preferences")
	}
	_, dashHTML := getWebPage(t, clientAdminA, srv.URL+"/workspace")
	if !strings.Contains(dashHTML, "w-pwe4-keep") && !strings.Contains(dashHTML, "Revenue") {
		t.Fatalf("property branding unexpectedly altered PWE-3 workspace dashboard:\n%s", dashHTML)
	}

	var auditCount int
	if err := migratorStore.Pool().QueryRow(ctx, `
		SELECT COUNT(*) FROM audit_logs
		WHERE resource_type = 'property_branding'
		  AND action IN ('property.branding_updated', 'property.logo_uploaded', 'property.logo_deleted')
	`).Scan(&auditCount); err != nil {
		t.Fatalf("query property_branding audit logs: %v", err)
	}
	if auditCount < 4 {
		t.Fatalf("expected >= 4 property_branding audit rows (update, 2 uploads, delete), got %d", auditCount)
	}

	// Deactivated user must fail closed even with a previously held actor/session.
	if _, uerr := appSvc.UpdateUser(ctx, globalOwnerActor, service.UpdateUserInput{
		ID: adminA.ID, FullName: adminA.FullName, Role: adminA.Role, IsActive: false, Version: adminA.Version,
	}); uerr != nil {
		t.Fatalf("deactivate adminA: %v", uerr)
	}
	rDeact, _ := apiJSONRequest(t, clientAdminA, http.MethodGet, srv.URL+"/api/v1/properties/"+propA.ID.String()+"/branding", "", nil)
	if rDeact.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected deactivated user rejected (401), got %d", rDeact.StatusCode)
	}
}
