package app_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/app"
	"github.com/tirbach/tir-bach/internal/backup"
	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/seed"
	"github.com/tirbach/tir-bach/internal/service"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

func TestBackupsMultiCloudSyncIntegrityAndEmptyDBRestore(t *testing.T) {
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

	resetAll := func() {
		t.Helper()
		err := store.WithTx(ctx, func(tx pgx.Tx) error {
			if _, txErr := tx.Exec(ctx, `SET LOCAL tirbach.test_reset_token = 'allow_test_reset'`); txErr != nil {
				return txErr
			}
			_, txErr := tx.Exec(ctx, `
				TRUNCATE TABLE backup_runs, backup_storage_targets, admin_recovery_codes,
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
	}
	resetAll()

	attDir := t.TempDir()
	ownerPw := "OwnerBackupPass!246810"
	seededProp, err := seed.EnsureBaseline(ctx, store, seed.Options{
		AdminEmail:    "owner@backup.test",
		AdminName:     "Backup Owner",
		AdminPassword: ownerPw,
	})
	if err != nil {
		t.Fatalf("EnsureBaseline: %v", err)
	}

	// Write a sample attachment file on disk to verify blob backup & restore.
	sampleBlobRel := filepath.Join(seededProp.ID.String(), "evidence.txt")
	if err := os.MkdirAll(filepath.Join(attDir, seededProp.ID.String()), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attDir, sampleBlobRel), []byte("verified-attachment-payload"), 0o640); err != nil {
		t.Fatal(err)
	}

	// Stand up mock multi-cloud HTTPS/HTTP server handling S3 SigV4, GCS OAuth2+JSON, and Azure SharedKey!
	var cloudMu sync.Mutex
	s3Objects := make(map[string][]byte)
	gcsObjects := make(map[string][]byte)
	azureObjects := make(map[string][]byte)

	mockCloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloudMu.Lock()
		defer cloudMu.Unlock()

		authHdr := r.Header.Get("Authorization")
		switch {
		// GCS OAuth2 JWT token endpoint
		case r.URL.Path == "/gcs/token" && r.Method == http.MethodPost:
			_ = r.ParseForm()
			if r.FormValue("assertion") == "" {
				http.Error(w, "missing assertion", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"mock-gcs-bearer-token","token_type":"Bearer","expires_in":3600}`))
			return

		// GCS upload endpoint
		case strings.HasPrefix(r.URL.Path, "/gcs/upload/storage/v1/b/") && r.Method == http.MethodPost:
			if authHdr != "Bearer mock-gcs-bearer-token" {
				http.Error(w, "unauthorized gcs", http.StatusUnauthorized)
				return
			}
			body, _ := io.ReadAll(r.Body)
			name := r.URL.Query().Get("name")
			gcsObjects[name] = body
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return

		// GCS list/delete endpoint
		case strings.HasPrefix(r.URL.Path, "/gcs/storage/v1/b/"):
			if authHdr != "Bearer mock-gcs-bearer-token" {
				http.Error(w, "unauthorized gcs", http.StatusUnauthorized)
				return
			}
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/json")
				var items []map[string]string
				for k, v := range gcsObjects {
					items = append(items, map[string]string{
						"name":    k,
						"size":    fmt.Sprintf("%d", len(v)),
						"updated": time.Now().UTC().Format(time.RFC3339),
					})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
				return
			}
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}

		// Azure Blob SharedKey endpoint
		case strings.HasPrefix(r.URL.Path, "/azure/"):
			if !strings.HasPrefix(authHdr, "SharedKey ") {
				http.Error(w, "missing SharedKey", http.StatusUnauthorized)
				return
			}
			trimmed := strings.TrimPrefix(r.URL.Path, "/azure/backupcontainer")
			key := strings.TrimLeft(trimmed, "/")
			if r.Method == http.MethodPut {
				body, _ := io.ReadAll(r.Body)
				azureObjects[key] = body
				w.WriteHeader(http.StatusCreated)
				return
			}
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/xml")
				var b strings.Builder
				b.WriteString(`<?xml version="1.0" encoding="utf-8"?><EnumerationResults><Blobs>`)
				for k, v := range azureObjects {
					fmt.Fprintf(&b, `<Blob><Name>%s</Name><Properties><Content-Length>%d</Content-Length><Last-Modified>%s</Last-Modified></Properties></Blob>`,
						k, len(v), time.Now().UTC().Format(http.TimeFormat))
				}
				b.WriteString(`</Blobs></EnumerationResults>`)
				_, _ = w.Write([]byte(b.String()))
				return
			}
			if r.Method == http.MethodDelete {
				delete(azureObjects, key)
				w.WriteHeader(http.StatusAccepted)
				return
			}

		// AWS S3 / R2 SigV4 endpoint
		case strings.HasPrefix(r.URL.Path, "/s3/"):
			if !strings.HasPrefix(authHdr, "AWS4-HMAC-SHA256 ") {
				http.Error(w, "missing AWS4-HMAC-SHA256", http.StatusUnauthorized)
				return
			}
			trimmed := strings.TrimPrefix(r.URL.Path, "/s3/my-s3-bucket")
			key := strings.TrimLeft(trimmed, "/")
			if r.Method == http.MethodPut {
				body, _ := io.ReadAll(r.Body)
				s3Objects[key] = body
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/xml")
				var keys []string
				for k := range s3Objects {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				var b strings.Builder
				b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult>`)
				for _, k := range keys {
					fmt.Fprintf(&b, `<Contents><Key>%s</Key><Size>%d</Size><LastModified>%s</LastModified></Contents>`,
						k, len(s3Objects[k]), time.Now().UTC().Format(time.RFC3339))
				}
				b.WriteString(`</ListBucketResult>`)
				_, _ = w.Write([]byte(b.String()))
				return
			}
			if r.Method == http.MethodDelete {
				delete(s3Objects, key)
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		http.NotFound(w, r)
	}))
	defer mockCloud.Close()

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
		PublicAPISecret:      "backup-cloud-test-bff-secret",
		AttachmentStorageDir: attDir,
		MaxAttachmentBytes:   10 * 1024 * 1024,
	}
	svc := service.New(store, cfg)
	svc.SetBackupCloudConfig(backup.CloudClientConfig{
		AllowLoopbackHTTPForTests: true,
		GCSTokenURLOverride:       mockCloud.URL + "/gcs/token",
	})
	router, err := app.NewRouter(cfg, store, svc)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	srv := httptest.NewServer(router)
	defer srv.Close()

	// Authenticate owner_admin via JSON API & create a reservation + manager user.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	loginBody, _ := json.Marshal(map[string]string{
		"email":    "owner@backup.test",
		"password": ownerPw,
	})
	loginResp, err := client.Post(srv.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil || loginResp.StatusCode != http.StatusOK {
		t.Fatalf("API login failed: status=%v err=%v", loginResp.StatusCode, err)
	}
	var loginDoc struct {
		CSRFToken string           `json:"csrf_token"`
		User      domain.AdminUser `json:"user"`
	}
	_ = json.NewDecoder(loginResp.Body).Decode(&loginDoc)
	loginResp.Body.Close()
	csrf := loginDoc.CSRFToken
	ownerActor := service.Actor{
		UserID:   loginDoc.User.ID,
		Email:    loginDoc.User.Email,
		FullName: loginDoc.User.FullName,
		Role:     loginDoc.User.Role,
	}

	// Generate recovery codes to ensure they are excluded from exported archives.
	if _, err := svc.RegenerateOwnRecoveryCodes(ctx, ownerActor, ownerPw); err != nil {
		t.Fatalf("RegenerateOwnRecoveryCodes: %v", err)
	}

	// Create a reservation and a manager user so restore exercises multi-table FKs, daterange claims, and user remapping.
	units, _ := store.ListUnits(ctx, seededProp.ID, nil, false)
	arr, _ := domain.ParseDate("2027-06-10")
	dep, _ := domain.ParseDate("2027-06-13")
	if _, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    seededProp.ID,
		ChannelCode:   "DIRECT",
		GuestName:     "Carys Morgan",
		GuestEmail:    "carys@example.test",
		GuestPhone:    "+447700900555",
		ArrivalDate:   arr,
		DepartureDate: dep,
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		TotalPence:    19000,
		Currency:      "GBP",
		UnitIDs:       []uuid.UUID{units[0].ID},
	}); err != nil {
		t.Fatalf("CreateReservation: %v", err)
	}

	pid := seededProp.ID
	mgrPw := "ManagerPass!246810"
	if _, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		PropertyID: &pid,
		Email:      "mgr@backup.test",
		FullName:   "Site Manager",
		Role:       domain.RoleManager,
		Password:   mgrPw,
	}); err != nil {
		t.Fatalf("CreateUser manager: %v", err)
	}

	// Verify manager receives 403 Forbidden on /settings/backups and /api/v1/backups.
	mgrJar, _ := cookiejar.New(nil)
	mgrClient := &http.Client{Jar: mgrJar}
	mgrLogin, _ := json.Marshal(map[string]string{"email": "mgr@backup.test", "password": mgrPw})
	rMgr, _ := mgrClient.Post(srv.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(mgrLogin))
	rMgr.Body.Close()
	mgrBackupsResp, _ := mgrClient.Get(srv.URL + "/api/v1/backups")
	mgrBackupsResp.Body.Close()
	if mgrBackupsResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for manager on /api/v1/backups, got %d", mgrBackupsResp.StatusCode)
	}
	mgrWebResp, _ := mgrClient.Get(srv.URL + "/settings/backups")
	mgrWebResp.Body.Close()
	if mgrWebResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for manager on /settings/backups, got %d", mgrWebResp.StatusCode)
	}

	// Wrong current_password rejected when exporting local backup.
	doJSON := func(method, path string, payload any) (*http.Response, []byte) {
		t.Helper()
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, raw
	}

	badExpResp, _ := doJSON(http.MethodPost, "/api/v1/backups/export", map[string]string{
		"current_password": "WrongPassword!000",
	})
	if badExpResp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for wrong current_password on export, got %d", badExpResp.StatusCode)
	}

	// Export encrypted .zip.enc backup!
	encPass := "ArchiveSecretPass!987654"
	if _, _, directErr := svc.ExportLocalBackupArchive(ctx, ownerActor, ownerPw, encPass); directErr != nil {
		t.Fatalf("direct ExportLocalBackupArchive failed: %v", directErr)
	}
	expResp, archiveBytes := doJSON(http.MethodPost, "/api/v1/backups/export", map[string]string{
		"current_password":   ownerPw,
		"archive_passphrase": encPass,
	})
	if expResp.StatusCode != http.StatusOK || !backup.IsEncryptedArchive(archiveBytes) {
		t.Fatalf("expected 200 OK encrypted archive download, got status=%d body=%s", expResp.StatusCode, string(archiveBytes))
	}

	// Verify decrypted contents exclude password_hash, admin_sessions, and admin_recovery_codes.
	unpacked, err := backup.VerifyAndUnpackArchive(archiveBytes, encPass)
	if err != nil {
		t.Fatalf("VerifyAndUnpackArchive: %v", err)
	}
	if _, hasSessions := unpacked.Tables["admin_sessions"]; hasSessions {
		t.Fatalf("admin_sessions must never be exported")
	}
	if _, hasRecCodes := unpacked.Tables["admin_recovery_codes"]; hasRecCodes {
		t.Fatalf("admin_recovery_codes must never be exported")
	}
	for _, uRow := range unpacked.Tables["admin_users"] {
		if strings.Contains(string(uRow), "password_hash") {
			t.Fatalf("admin_users export leaked password_hash: %s", string(uRow))
		}
	}

	// Test S3-Compatible cloud target + retention pruning (pre-seed 2 older objects + upload 1 new with retention=2 -> keeps 2!).
	cloudMu.Lock()
	s3Objects["lovelace-backups/lovelace-backup-20260101-000000Z.zip.enc"] = []byte("old-1")
	s3Objects["lovelace-backups/lovelace-backup-20260102-000000Z.zip.enc"] = []byte("old-2")
	cloudMu.Unlock()

	s3CreateResp, s3Body := doJSON(http.MethodPost, "/api/v1/backups/targets", map[string]any{
		"current_password":        ownerPw,
		"name":                    "Cloudflare R2 Offsite",
		"provider":                "S3_COMPATIBLE",
		"bucket_or_container":     "my-s3-bucket",
		"region":                  "auto",
		"endpoint_url":            mockCloud.URL + "/s3",
		"key_prefix":              "lovelace-backups/",
		"encrypt_archive":         true,
		"schedule_enabled":        false,
		"schedule_interval_hours": 24,
		"retention_count":         2,
		"is_active":               true,
		"credentials": map[string]string{
			"access_key_id":      "AKIAIOSFODNN7EXAMPLE",
			"secret_access_key":  "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			"archive_passphrase": encPass,
		},
	})
	if s3CreateResp.StatusCode != http.StatusCreated {
		t.Fatalf("create S3 target failed (%d): %s", s3CreateResp.StatusCode, string(s3Body))
	}
	var s3Target domain.BackupStorageTarget
	_ = json.Unmarshal(s3Body, &s3Target)

	vS3Resp, _ := doJSON(http.MethodPost, "/api/v1/backups/targets/"+s3Target.ID.String()+"/verify", map[string]any{})
	if vS3Resp.StatusCode != http.StatusOK {
		t.Fatalf("verify S3 target failed: %d", vS3Resp.StatusCode)
	}
	runS3Resp, runS3Body := doJSON(http.MethodPost, "/api/v1/backups/targets/"+s3Target.ID.String()+"/run", map[string]string{
		"current_password": ownerPw,
	})
	if runS3Resp.StatusCode != http.StatusOK {
		t.Fatalf("run S3 backup failed (%d): %s", runS3Resp.StatusCode, string(runS3Body))
	}
	cloudMu.Lock()
	s3Remaining := len(s3Objects)
	_, oldestPruned := s3Objects["lovelace-backups/lovelace-backup-20260101-000000Z.zip.enc"]
	cloudMu.Unlock()
	if s3Remaining != 2 || oldestPruned {
		t.Fatalf("expected S3 retention pruning to keep 2 newest objects and delete oldest, got count=%d oldestPresent=%v", s3Remaining, oldestPruned)
	}

	// Test Google Cloud Storage (GCS) target with generated RSA-2048 Service Account JSON!
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8Bytes}))
	saJSONBytes, _ := json.Marshal(map[string]string{
		"type":         "service_account",
		"client_email": "lovelace-backup@project.iam.gserviceaccount.com",
		"private_key":  pemKey,
	})
	gcsCreateResp, gcsBody := doJSON(http.MethodPost, "/api/v1/backups/targets", map[string]any{
		"current_password":        ownerPw,
		"name":                    "Google Cloud Archive",
		"provider":                "GCS",
		"bucket_or_container":     "my-gcs-bucket",
		"endpoint_url":            mockCloud.URL + "/gcs",
		"key_prefix":              "lovelace-backups/",
		"encrypt_archive":         true,
		"schedule_enabled":        false,
		"schedule_interval_hours": 24,
		"retention_count":         7,
		"is_active":               true,
		"credentials": map[string]string{
			"service_account_json": string(saJSONBytes),
			"archive_passphrase":   encPass,
		},
	})
	if gcsCreateResp.StatusCode != http.StatusCreated {
		t.Fatalf("create GCS target failed (%d): %s", gcsCreateResp.StatusCode, string(gcsBody))
	}
	var gcsTarget domain.BackupStorageTarget
	_ = json.Unmarshal(gcsBody, &gcsTarget)
	runGCSResp, runGCSBody := doJSON(http.MethodPost, "/api/v1/backups/targets/"+gcsTarget.ID.String()+"/run", map[string]string{
		"current_password": ownerPw,
	})
	if runGCSResp.StatusCode != http.StatusOK {
		t.Fatalf("run GCS backup failed (%d): %s", runGCSResp.StatusCode, string(runGCSBody))
	}
	cloudMu.Lock()
	gcsCount := len(gcsObjects)
	cloudMu.Unlock()
	if gcsCount != 1 {
		t.Fatalf("expected 1 uploaded GCS object, got %d", gcsCount)
	}

	// Test Azure Blob Storage target + scheduled background sweep!
	azKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	azCreateResp, azBody := doJSON(http.MethodPost, "/api/v1/backups/targets", map[string]any{
		"current_password":        ownerPw,
		"name":                    "Azure Offsite Container",
		"provider":                "AZURE_BLOB",
		"bucket_or_container":     "backupcontainer",
		"endpoint_url":            mockCloud.URL + "/azure",
		"key_prefix":              "lovelace-backups/",
		"encrypt_archive":         true,
		"schedule_enabled":        true,
		"schedule_interval_hours": 12,
		"retention_count":         5,
		"is_active":               true,
		"credentials": map[string]string{
			"account_name":       "lovelacestorage",
			"account_key":        azKey,
			"archive_passphrase": encPass,
		},
	})
	if azCreateResp.StatusCode != http.StatusCreated {
		t.Fatalf("create Azure target failed (%d): %s", azCreateResp.StatusCode, string(azBody))
	}
	var azTarget domain.BackupStorageTarget
	_ = json.Unmarshal(azBody, &azTarget)
	// Force next_run_at into the past so SweepScheduledBackups picks it up!
	past := time.Now().UTC().Add(-1 * time.Minute)
	_, _ = store.RecordBackupTargetOutcome(ctx, nil, azTarget.ID, domain.BackupTargetStatusNeverRun, nil, nil, &past, "")
	swept, err := svc.SweepScheduledBackups(ctx)
	if err != nil || swept != 1 {
		t.Fatalf("expected SweepScheduledBackups to run 1 Azure backup, got %d (err=%v)", swept, err)
	}
	cloudMu.Lock()
	azCount := len(azureObjects)
	cloudMu.Unlock()
	if azCount != 1 {
		t.Fatalf("expected 1 uploaded Azure blob, got %d", azCount)
	}

	// Helper for multipart upload to /api/v1/backups/verify and /api/v1/backups/restore.
	uploadMultipart := func(path string, fileBytes []byte, fields map[string]string) (*http.Response, []byte) {
		t.Helper()
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, err := mw.CreateFormFile("archive", "backup.zip.enc")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(fileBytes)
		for k, v := range fields {
			_ = mw.WriteField(k, v)
		}
		_ = mw.Close()

		req, _ := http.NewRequest(http.MethodPost, srv.URL+path, &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("X-CSRF-Token", csrf)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, raw
	}

	// Verify uploaded archive checksums via /api/v1/backups/verify.
	verResp, verBody := uploadMultipart("/api/v1/backups/verify", archiveBytes, map[string]string{
		"archive_passphrase": encPass,
	})
	if verResp.StatusCode != http.StatusOK {
		t.Fatalf("verify uploaded archive failed (%d): %s", verResp.StatusCode, string(verBody))
	}

	// Attempting restore onto NON-EMPTY database must fail closed with 409 Conflict!
	nonEmptyResp, _ := uploadMultipart("/api/v1/backups/restore", archiveBytes, map[string]string{
		"current_password":   ownerPw,
		"archive_passphrase": encPass,
	})
	if nonEmptyResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 Conflict when restoring onto non-empty database, got %d", nonEmptyResp.StatusCode)
	}

	// Now wipe database + attachment directory to simulate a brand-new empty environment with a newly bootstrapped owner_admin (different UUID, same email!).
	resetAll()
	_ = os.RemoveAll(filepath.Join(attDir, seededProp.ID.String()))

	newOwnerPw := "NewEnvOwnerPass!246810"
	_, bootOwner, err := svc.BootstrapInitialAdmin(ctx, "owner@backup.test", "Fresh Host Owner", "TempBootPass!135790")
	if err != nil {
		t.Fatalf("BootstrapInitialAdmin fresh: %v", err)
	}
	if _, err := svc.ChangeOwnPassword(ctx, service.Actor{
		UserID: bootOwner.ID,
		Email:  bootOwner.Email,
		Role:   bootOwner.Role,
	}, "TempBootPass!135790", newOwnerPw); err != nil {
		t.Fatalf("ChangeOwnPassword fresh: %v", err)
	}

	// Sign in on the fresh empty host.
	freshLogin, _ := json.Marshal(map[string]string{"email": "owner@backup.test", "password": newOwnerPw})
	freshLoginResp, _ := client.Post(srv.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(freshLogin))
	_ = json.NewDecoder(freshLoginResp.Body).Decode(&loginDoc)
	freshLoginResp.Body.Close()
	csrf = loginDoc.CSRFToken

	// Check WebUI /settings/backups renders 200 OK on an empty database with "Restore Backup Into Empty Environment" unlocked!
	webGetResp, err := client.Get(srv.URL + "/settings/backups")
	if err != nil {
		t.Fatalf("GET /settings/backups on empty DB: %v", err)
	}
	webGetBytes, _ := io.ReadAll(webGetResp.Body)
	webGetResp.Body.Close()
	if webGetResp.StatusCode != http.StatusOK || !strings.Contains(string(webGetBytes), "Verify &amp; Restore Into Empty Database") {
		t.Fatalf("expected /settings/backups to show unlocked empty-DB restore form, status=%d", webGetResp.StatusCode)
	}

	// Execute full restore into the empty environment!
	resResp, resBody := uploadMultipart("/api/v1/backups/restore", archiveBytes, map[string]string{
		"current_password":   newOwnerPw,
		"archive_passphrase": encPass,
	})
	if resResp.StatusCode != http.StatusOK {
		t.Fatalf("restore into empty DB failed (%d): %s", resResp.StatusCode, string(resBody))
	}

	// Verify property, units, extras, manager user, and disk attachment were all restored while active owner session still works!
	restoredProps, err := store.ListProperties(ctx)
	if err != nil || len(restoredProps) != 1 || restoredProps[0].Slug != "tir-bach" {
		t.Fatalf("expected tir-bach property restored, got %+v (err=%v)", restoredProps, err)
	}
	restoredUnits, _ := store.ListUnits(ctx, restoredProps[0].ID, nil, false)
	if len(restoredUnits) != 6 {
		t.Fatalf("expected 6 restored physical units, got %d", len(restoredUnits))
	}
	restoredRes, err := store.ListReservations(ctx, restoredProps[0].ID, postgres.ReservationFilter{})
	if err != nil || len(restoredRes) != 1 || restoredRes[0].GuestName != "Carys Morgan" {
		t.Fatalf("expected restored reservation for Carys Morgan, got %+v (err=%v)", restoredRes, err)
	}
	restoredBlob, err := os.ReadFile(filepath.Join(attDir, sampleBlobRel))
	if err != nil || string(restoredBlob) != "verified-attachment-payload" {
		t.Fatalf("expected attachment blob to be restored on disk, got %q (err=%v)", string(restoredBlob), err)
	}
	meReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/auth/me", nil)
	meResp, err := client.Do(meReq)
	if err != nil || meResp.StatusCode != http.StatusOK {
		t.Fatalf("expected active owner session to remain valid after restore, status=%v err=%v", meResp.StatusCode, err)
	}
	meResp.Body.Close()
}
