package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/channels"
	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/secrets"
	"github.com/tirbach/tir-bach/internal/service"
)

func TestVerifySafeTestDatabase(t *testing.T) {
	if err := verifySafeTestDatabase("postgres://tir_bach_test_runner:p@127.0.0.1:5432/tir_bach_test?sslmode=disable", "tir_bach_test", "tir_bach_test_runner", false); err != nil {
		t.Fatalf("expected valid test db & role to pass, got: %v", err)
	}
	for _, tc := range []struct {
		url   string
		db    string
		user  string
		super bool
	}{
		{"postgres://u:p@127.0.0.1:5432/tir_bach", "tir_bach", "tir_bach_test_runner", false},
		{"postgres://u:p@127.0.0.1:5432/postgres", "postgres", "tir_bach_test_runner", false},
		{"postgres://u:p@127.0.0.1:5432/tir_bach_prod_test", "tir_bach_prod_test", "tir_bach_test_runner", false},
		{"postgres://u:p@127.0.0.1:5432/tir_bach_test", "tir_bach", "tir_bach_test_runner", false},
		{"postgres://u:p@127.0.0.1:5432/tir_bach_test", "tir_bach_test", "tir_bach", true},
		{"postgres://u:p@127.0.0.1:5432/tir_bach_test", "tir_bach_test", "postgres", false},
		{"postgres://u:p@127.0.0.1:5432/tir_bach_test", "tir_bach_test", "prod_admin", false},
		{"postgres://u:p@prod.db.internal:5432/tir_bach_test", "tir_bach_test", "tir_bach_test_runner", false},
	} {
		if err := verifySafeTestDatabase(tc.url, tc.db, tc.user, tc.super); err == nil {
			t.Fatalf("expected non-test db/role/host (%+v) to fail closed", tc)
		}
	}
}

func TestConfigAndSecretsUnit(t *testing.T) {
	// Secrets AES-256-GCM roundtrip and masking
	box, err := secrets.NewBox(testEncryptionKey)
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	sealed, err := box.SealMap(map[string]string{
		"api_key":       "b24_super_secret_token_987654",
		"refresh_token": "rt_short",
	})
	if err != nil {
		t.Fatalf("SealMap: %v", err)
	}
	if bytes.Contains(sealed, []byte("b24_super_secret_token_987654")) {
		t.Fatal("ciphertext must never contain plaintext secret")
	}
	opened, err := box.OpenMap(sealed)
	if err != nil || opened["api_key"] != "b24_super_secret_token_987654" {
		t.Fatalf("OpenMap mismatch: %v %+v", err, opened)
	}
	if hint := secrets.MaskSecret("b24_super_secret_token_987654"); hint != "••••7654" {
		t.Fatalf("unexpected secret mask: %q", hint)
	}

	// Production config fails closed if dev defaults or insecure flags are set.
	t.Setenv("TIRBACH_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://tir_bach:strong_prod_pw_9988@db:5432/tir_bach?sslmode=require")
	t.Setenv("TIRBACH_COOKIE_SECURE", "false")
	t.Setenv("TIRBACH_AUTO_SEED", "false")
	t.Setenv("TIRBACH_ENCRYPTION_KEY", "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected production load with TIRBACH_COOKIE_SECURE=false to fail closed")
	}

	t.Setenv("TIRBACH_COOKIE_SECURE", "true")
	t.Setenv("TIRBACH_AUTO_SEED", "true")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected production load with TIRBACH_AUTO_SEED=true to fail closed")
	}

	t.Setenv("TIRBACH_AUTO_SEED", "false")
	t.Setenv("TIRBACH_ENCRYPTION_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected production load with known dev encryption key to fail closed")
	}

	t.Setenv("TIRBACH_ENCRYPTION_KEY", "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90")
	if _, err := config.Load(); err != nil {
		t.Fatalf("expected valid hardened production config to load, got: %v", err)
	}
}

func TestDomainPhase1_1Rules(t *testing.T) {
	if !domain.ResStatusConfirmed.CanTransitionTo(domain.ResStatusCheckedIn) {
		t.Fatal("CONFIRMED -> CHECKED_IN must be valid")
	}
	if domain.ResStatusCancelled.CanTransitionTo(domain.ResStatusConfirmed) {
		t.Fatal("CANCELLED -> CONFIRMED must be rejected")
	}
	if domain.ResStatusInquiry.BlocksInventory() {
		t.Fatal("INQUIRY must not block inventory")
	}
	if !domain.ResStatusConfirmed.BlocksInventory() || !domain.ResStatusPending.BlocksInventory() {
		t.Fatal("CONFIRMED and PENDING must block inventory")
	}

	// EvaluateReleaseForStay channel precedence
	propID := uuid.New()
	unit := domain.Unit{ID: uuid.New(), PropertyID: propID, UnitTypeID: uuid.New(), Code: "U1"}
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	rules := []domain.AvailabilityReleaseRule{
		{
			ID: uuid.New(), PropertyID: propID, ScopeType: domain.ReleaseScopeProperty,
			StartDate: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			ChannelCode: "AIRBNB", ReleaseAt: now.Add(24 * time.Hour), IsActive: true,
		},
	}
	if ok, _, _ := domain.EvaluateReleaseForStay(rules, unit, "AIRBNB", time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC), now); ok {
		t.Fatal("expected AIRBNB to be unreleased before ReleaseAt")
	}
	if ok, _, _ := domain.EvaluateReleaseForStay(rules, unit, "DIRECT", time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC), now); !ok {
		t.Fatal("expected DIRECT to be allowed when only AIRBNB is held")
	}
}

func TestPhase1_1EndToEnd(t *testing.T) {
	srv, store, prop := setupTestServer(t)
	ownerClient := newClient(t)

	code, csrf := apiLogin(t, ownerClient, srv.URL, "owner@tirbach.wales", "tirbach-owner-password-2026")
	if code != http.StatusOK || csrf == "" {
		t.Fatalf("owner login failed: %d", code)
	}

	// Setup unit types and units for tests: Glamping (2 units) + Meadow (2 units)
	utGlamp := mustCreateUnitType(t, ownerClient, srv.URL, prop.ID, csrf, "pod", "Timber Pod", "cabin", 12000)
	utMeadow := mustCreateUnitType(t, ownerClient, srv.URL, prop.ID, csrf, "meadow", "Meadow Pitch", "tent_pitch", 4500)

	uPod1 := mustCreateUnit(t, ownerClient, srv.URL, prop.ID, csrf, utGlamp.ID, "POD-01", "Oak Pod")
	uPod2 := mustCreateUnit(t, ownerClient, srv.URL, prop.ID, csrf, utGlamp.ID, "POD-02", "Ash Pod")
	uMead1 := mustCreateUnit(t, ownerClient, srv.URL, prop.ID, csrf, utMeadow.ID, "MP-01", "Meadow 1")
	uMead2 := mustCreateUnit(t, ownerClient, srv.URL, prop.ID, csrf, utMeadow.ID, "MP-02", "Meadow 2")

	// Scenario 1: Manual multi-unit reservation with unique reference
	resCreate := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Bryn Morgan", "guest_email": "bryn@example.wales", "guest_phone": "+44 7700 900111",
		"channel_code": "PHONE", "arrival_date": "2026-09-10", "departure_date": "2026-09-14",
		"adults": 4, "children": 2, "infants": 1, "status": "CONFIRMED",
		"internal_notes": "Family group booking two pods",
		"unit_ids":       []uuid.UUID{uPod1.ID, uPod2.ID},
	})
	if resCreate.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resCreate.Body)
		t.Fatalf("expected 201 creating multi-unit reservation, got %d: %s", resCreate.StatusCode, string(b))
	}
	var res1 domain.Reservation
	_ = json.NewDecoder(resCreate.Body).Decode(&res1)
	resCreate.Body.Close()
	if !strings.HasPrefix(res1.Reference, "TB-") || len(res1.Units) != 2 {
		t.Fatalf("unexpected reservation payload: %+v", res1)
	}

	// Scenario 2: Overlapping reservation fails -> cancel releases claim -> rebook succeeds
	overlapRes := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Elen Rees", "channel_code": "DIRECT",
		"arrival_date": "2026-09-12", "departure_date": "2026-09-15",
		"adults": 2, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uPod1.ID},
	})
	overlapRes.Body.Close()
	if overlapRes.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for overlapping reservation, got %d", overlapRes.StatusCode)
	}

	// Cancel res1 -> inventory claims released cleanly
	cancelResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations/"+res1.ID.String()+"/transition", csrf, map[string]any{
		"status": "CANCELLED", "reason": "Guest changed travel plans", "version": res1.Version,
	})
	if cancelResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(cancelResp.Body)
		t.Fatalf("expected 200 cancelling reservation, got %d: %s", cancelResp.StatusCode, string(b))
	}
	cancelResp.Body.Close()

	rebookResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Elen Rees", "channel_code": "DIRECT",
		"arrival_date": "2026-09-12", "departure_date": "2026-09-15",
		"adults": 2, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uPod1.ID},
	})
	rebookResp.Body.Close()
	if rebookResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 rebooking after cancellation, got %d", rebookResp.StatusCode)
	}

	// Scenario 3: Reservation blocked by existing maintenance block in unified inventory_claims
	blkResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts", csrf, map[string]any{
		"scope_type": "UNIT", "unit_ids": []uuid.UUID{uPod2.ID}, "category": "OWNER_USE",
		"channel_scope": "ALL", "start_date": "2026-10-01", "end_date": "2026-10-05", "reason": "Owner stay",
	})
	if blkResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating owner blockout, got %d", blkResp.StatusCode)
	}
	blkResp.Body.Close()

	blockedRes := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Blocked Guest", "channel_code": "WALK_IN",
		"arrival_date": "2026-10-02", "departure_date": "2026-10-04",
		"adults": 2, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uPod2.ID},
	})
	blockedRes.Body.Close()
	if blockedRes.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 when booking across OWNER_USE claim, got %d", blockedRes.StatusCode)
	}

	// Scenario 4 & 5: Whole-property & unit-type bulk blockout + future-unit inheritance + partial release
	utBlkResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts", csrf, map[string]any{
		"scope_type": "UNIT_TYPE", "unit_type_id": utMeadow.ID, "category": "SEASONAL_CLOSURE",
		"channel_scope": "ALL", "start_date": "2026-11-01", "end_date": "2026-11-10",
		"reason": "Meadow drainage winter closure", "apply_to_future_units": true,
	})
	if utBlkResp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(utBlkResp.Body)
		t.Fatalf("expected 201 unit_type bulk blockout, got %d: %s", utBlkResp.StatusCode, string(b))
	}
	var blkPayload struct {
		Group  domain.BlockoutGroup    `json:"group"`
		Claims []domain.InventoryClaim `json:"claims"`
	}
	_ = json.NewDecoder(utBlkResp.Body).Decode(&blkPayload)
	utBlkResp.Body.Close()
	if len(blkPayload.Claims) != 2 {
		t.Fatalf("expected 2 claims for meadow unit type, got %d", len(blkPayload.Claims))
	}

	// Creating a new unit inside utMeadow inherits the active seasonal closure automatically!
	uMead3 := mustCreateUnit(t, ownerClient, srv.URL, prop.ID, csrf, utMeadow.ID, "MP-03", "Meadow 3")
	chkInherit := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Future Unit Test", "channel_code": "DIRECT",
		"arrival_date": "2026-11-03", "departure_date": "2026-11-06",
		"adults": 2, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uMead3.ID},
	})
	chkInherit.Body.Close()
	if chkInherit.StatusCode != http.StatusConflict {
		t.Fatalf("expected future unit MP-03 to inherit seasonal closure (409), got %d", chkInherit.StatusCode)
	}

	// Partial release of MP-01 only -> group becomes PARTIALLY_RELEASED, MP-01 bookable, MP-02 still blocked
	partRelResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts/"+blkPayload.Group.ID.String()+"/release", csrf, map[string]any{
		"unit_ids": []uuid.UUID{uMead1.ID},
	})
	if partRelResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 partial release, got %d", partRelResp.StatusCode)
	}
	var partPayload struct {
		Group domain.BlockoutGroup `json:"group"`
	}
	_ = json.NewDecoder(partRelResp.Body).Decode(&partPayload)
	partRelResp.Body.Close()
	if partPayload.Group.Status != "PARTIALLY_RELEASED" {
		t.Fatalf("expected PARTIALLY_RELEASED, got %s", partPayload.Group.Status)
	}
	okBookReleased := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Winter Camper", "channel_code": "DIRECT",
		"arrival_date": "2026-11-02", "departure_date": "2026-11-04",
		"adults": 2, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uMead1.ID},
	})
	okBookReleased.Body.Close()
	if okBookReleased.StatusCode != http.StatusCreated {
		t.Fatalf("expected MP-01 to be bookable after partial release, got %d", okBookReleased.StatusCode)
	}

	// Scenario 8: Transaction rollback on bulk blockout failure (unit_ids includes MP-02 free vs POD-02 occupied)
	failPreviewResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts/preview", csrf, map[string]any{
		"scope_type": "UNITS", "unit_ids": []uuid.UUID{uPod1.ID, uPod2.ID}, "category": "MAINTENANCE",
		"channel_scope": "ALL", "start_date": "2026-10-02", "end_date": "2026-10-04", "reason": "Should detect POD-02 conflict",
	})
	if failPreviewResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 preview, got %d", failPreviewResp.StatusCode)
	}
	var prev domain.BlockoutPreview
	_ = json.NewDecoder(failPreviewResp.Body).Decode(&prev)
	failPreviewResp.Body.Close()
	if prev.CanCommit || len(prev.Conflicts) != 1 || prev.Conflicts[0].UnitID != uPod2.ID {
		t.Fatalf("expected 1 conflict on POD-02, got %+v", prev)
	}

	beforeGroups, _ := store.ListBlockoutGroups(context.Background(), prop.ID, true)
	failBulkResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts", csrf, map[string]any{
		"scope_type": "UNITS", "unit_ids": []uuid.UUID{uPod1.ID, uPod2.ID}, "category": "MAINTENANCE",
		"channel_scope": "ALL", "start_date": "2026-10-02", "end_date": "2026-10-04", "reason": "Must roll back atomically",
	})
	failBulkResp.Body.Close()
	if failBulkResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 bulk blockout rollback, got %d", failBulkResp.StatusCode)
	}
	afterGroups, _ := store.ListBlockoutGroups(context.Background(), prop.ID, true)
	if len(afterGroups) != len(beforeGroups) {
		t.Fatalf("bulk rollback failed: group count changed from %d to %d", len(beforeGroups), len(afterGroups))
	}

	// Scenario 6: Channel-specific scheduled release holds AIRBNB, allows DIRECT, auto-opens on timestamp
	relTime := time.Now().UTC().Add(1400 * time.Millisecond)
	relRuleResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/releases", csrf, map[string]any{
		"scope_type": "UNIT", "unit_id": uPod1.ID, "start_date": "2026-12-01", "end_date": "2026-12-10",
		"channel_code": "AIRBNB", "release_at": relTime.Format(time.RFC3339Nano), "notes": "Hold for direct guests briefly",
	})
	if relRuleResp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(relRuleResp.Body)
		t.Fatalf("expected 201 creating release rule, got %d: %s", relRuleResp.StatusCode, string(b))
	}
	relRuleResp.Body.Close()

	// Before ReleaseAt: AIRBNB blocked (409), DIRECT allowed (201)
	airbnbBlocked := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Airbnb Early", "channel_code": "AIRBNB",
		"arrival_date": "2026-12-05", "departure_date": "2026-12-08",
		"adults": 2, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uPod1.ID},
	})
	airbnbBlocked.Body.Close()
	if airbnbBlocked.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for AIRBNB before release timestamp, got %d", airbnbBlocked.StatusCode)
	}
	directOK := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Direct Early", "channel_code": "DIRECT",
		"arrival_date": "2026-12-01", "departure_date": "2026-12-03",
		"adults": 2, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uPod1.ID},
	})
	directOK.Body.Close()
	if directOK.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 for DIRECT during AIRBNB-only release hold, got %d", directOK.StatusCode)
	}

	// Wait for ReleaseAt to elapse -> AIRBNB automatically permitted with zero cron intervention!
	time.Sleep(1600 * time.Millisecond)
	airbnbNowOK := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Airbnb After Release", "channel_code": "AIRBNB",
		"arrival_date": "2026-12-05", "departure_date": "2026-12-08",
		"adults": 2, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uPod1.ID},
	})
	airbnbNowOK.Body.Close()
	if airbnbNowOK.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 for AIRBNB once release_at elapsed, got %d", airbnbNowOK.StatusCode)
	}

	// Also test manual immediate release rule + whole-property bulk blockout across all active units.
	manRelCreate := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/releases", csrf, map[string]any{
		"scope_type": "PROPERTY", "start_date": "2027-06-01", "end_date": "2027-06-15",
		"channel_code": "ALL", "release_at": time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339), "notes": "Future summer wave",
	})
	if manRelCreate.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating manual-release test rule, got %d", manRelCreate.StatusCode)
	}
	var manRule domain.AvailabilityReleaseRule
	_ = json.NewDecoder(manRelCreate.Body).Decode(&manRule)
	manRelCreate.Body.Close()

	manRelNow := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/releases/"+manRule.ID.String()+"/release-now", csrf, map[string]any{})
	if manRelNow.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 triggering manual immediate release, got %d", manRelNow.StatusCode)
	}
	manRelNow.Body.Close()
	// Idempotent second call succeeds cleanly
	manRelAgain := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/releases/"+manRule.ID.String()+"/release-now", csrf, map[string]any{})
	manRelAgain.Body.Close()
	if manRelAgain.StatusCode != http.StatusOK {
		t.Fatalf("expected idempotent 200 on repeated manual release, got %d", manRelAgain.StatusCode)
	}

	propBlk := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts", csrf, map[string]any{
		"scope_type": "PROPERTY", "category": "WEATHER_CLOSURE",
		"channel_scope": "ALL", "start_date": "2027-07-01", "end_date": "2027-07-05", "reason": "Full site inspection",
	})
	if propBlk.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 whole-property blockout, got %d", propBlk.StatusCode)
	}
	var propBlkRes struct {
		Group  domain.BlockoutGroup    `json:"group"`
		Claims []domain.InventoryClaim `json:"claims"`
	}
	_ = json.NewDecoder(propBlk.Body).Decode(&propBlkRes)
	propBlk.Body.Close()
	if propBlkRes.Group.ScopeType != domain.BlockScopeProperty || len(propBlkRes.Claims) < 5 {
		t.Fatalf("expected PROPERTY blockout covering all 5 active units, got %d claims", len(propBlkRes.Claims))
	}

	// Scenario 7: Concurrency double-book test (10 goroutines race on MP-01 for identical dates)
	var successCount atomic.Int32
	var conflictCount atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
				"guest_name": "Racer Guest", "channel_code": "DIRECT",
				"arrival_date": "2027-01-10", "departure_date": "2027-01-13",
				"adults": 2, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uMead1.ID},
			})
			r.Body.Close()
			if r.StatusCode == http.StatusCreated {
				successCount.Add(1)
			} else if r.StatusCode == http.StatusConflict {
				conflictCount.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if successCount.Load() != 1 || conflictCount.Load() != 9 {
		t.Fatalf("concurrency test expected 1 winner & 9 conflicts, got success=%d conflict=%d", successCount.Load(), conflictCount.Load())
	}

	// Scenario 9: Channel Manager Configuration Framework + encrypted secrets + never CONNECTED
	badConnected := doJSON(t, ownerClient, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/beds24", csrf, map[string]any{
		"display_name": "Tir Bach Beds24",
		"status":       "CONNECTED",
		"inputs":       map[string]string{"property_id": "12345", "refresh_token": "secret_b24_key_9988"},
	})
	badConnected.Body.Close()
	if badConnected.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 rejecting CONNECTED without live adapter, got %d", badConnected.StatusCode)
	}

	saveCh := doJSON(t, ownerClient, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/beds24", csrf, map[string]any{
		"display_name": "Tir Bach Beds24",
		"inputs":       map[string]string{"property_id": "12345", "refresh_token": "secret_b24_key_9988"},
	})
	if saveCh.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(saveCh.Body)
		t.Fatalf("expected 200 saving channel config, got %d: %s", saveCh.StatusCode, string(b))
	}
	rawJSON, _ := io.ReadAll(saveCh.Body)
	saveCh.Body.Close()
	if bytes.Contains(rawJSON, []byte("secret_b24_key_9988")) {
		t.Fatal("API response must never expose raw secret refresh_token")
	}
	var savedConn channels.ChannelConnection
	_ = json.Unmarshal(rawJSON, &savedConn)
	if savedConn.Status != channels.StatusCredentialsSaved || savedConn.HasLiveAdapter || savedConn.CredentialHints["refresh_token"] != "••••9988" {
		t.Fatalf("unexpected channel connection payload: %+v", savedConn)
	}

	// Verify ciphertext stored in DB does not leak plaintext credential, and Channex uses user-api-key header auth
	rec, err := store.GetChannelConnection(context.Background(), nil, prop.ID, "beds24")
	if err != nil || bytes.Contains(rec.EncryptedCredentials, []byte("secret_b24_key_9988")) {
		t.Fatalf("channel credentials in DB invalid or unencrypted: err=%v", err)
	}
	saveChx := doJSON(t, ownerClient, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/channex", csrf, map[string]any{
		"display_name": "Tir Bach Channex",
		"inputs":       map[string]string{"property_id": "chx-prop-1", "environment": "staging", "user_api_key": "chx_user_api_key_4321"},
	})
	if saveChx.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(saveChx.Body)
		t.Fatalf("expected 200 saving channex config, got %d: %s", saveChx.StatusCode, string(b))
	}
	var chxConn channels.ChannelConnection
	_ = json.NewDecoder(saveChx.Body).Decode(&chxConn)
	saveChx.Body.Close()
	if chxConn.AuthMethod != channels.AuthAPIKey || chxConn.Status != channels.StatusCredentialsSaved || chxConn.CredentialHints["user_api_key"] != "••••4321" {
		t.Fatalf("unexpected channex auth/status/hint: %+v", chxConn)
	}

	// Calendar JSON query reflects real DB inventory claims & unit counts
	calResp, err := ownerClient.Get(srv.URL + "/api/v1/properties/" + prop.ID.String() + "/calendar?view=month&start_date=2026-09-01")
	if err != nil {
		t.Fatalf("calendar GET: %v", err)
	}
	var calData service.CalendarViewData
	_ = json.NewDecoder(calResp.Body).Decode(&calData)
	calResp.Body.Close()
	if calResp.StatusCode != http.StatusOK || len(calData.Units) < 5 || calData.Summary.BookedNights == 0 {
		t.Fatalf("unexpected calendar response: status=%d summary=%+v units=%d", calResp.StatusCode, calData.Summary, len(calData.Units))
	}

	// Scenario 10: must_rotate_password enforcement + session invalidation across sessions
	createTemp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/users", csrf, map[string]any{
		"property_id": prop.ID, "email": "temp@tirbach.wales", "full_name": "Temp Manager",
		"role": "manager", "password": "temporary-password-2026", "must_rotate_password": true,
	})
	if createTemp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating temp manager, got %d", createTemp.StatusCode)
	}
	createTemp.Body.Close()

	// Two separate clients log in with temp password
	tempClient1 := newClient(t)
	c1, csrf1 := apiLogin(t, tempClient1, srv.URL, "temp@tirbach.wales", "temporary-password-2026")
	tempClient2 := newClient(t)
	c2, _ := apiLogin(t, tempClient2, srv.URL, "temp@tirbach.wales", "temporary-password-2026")
	if c1 != http.StatusOK || c2 != http.StatusOK {
		t.Fatalf("temp manager login failed: %d / %d", c1, c2)
	}

	// Blocked on normal API endpoint (403 password_rotation_required)
	blockedAPI, _ := tempClient1.Get(srv.URL + "/api/v1/properties")
	blockedAPI.Body.Close()
	if blockedAPI.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 password_rotation_required on API, got %d", blockedAPI.StatusCode)
	}
	// Web UI redirects to /account?rotate=1
	webTemp := newClient(t)
	f := url.Values{}
	f.Set("email", "temp@tirbach.wales")
	f.Set("password", "temporary-password-2026")
	_, _ = webTemp.PostForm(srv.URL+"/login", f)
	webRedir, _ := webTemp.Get(srv.URL + "/calendar")
	webRedir.Body.Close()
	if webRedir.StatusCode != http.StatusSeeOther || !strings.Contains(webRedir.Header.Get("Location"), "/account?rotate=1") {
		t.Fatalf("expected redirect to /account?rotate=1, got %d %q", webRedir.StatusCode, webRedir.Header.Get("Location"))
	}

	// Changing password rotates client1 session cleanly and revokes client2 & webTemp sessions
	chgResp := doJSON(t, tempClient1, http.MethodPost, srv.URL+"/api/v1/auth/change-password", csrf1, map[string]any{
		"current_password": "temporary-password-2026",
		"new_password":     "rotated-manager-password-2026!",
	})
	if chgResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(chgResp.Body)
		t.Fatalf("expected 200 changing password, got %d: %s", chgResp.StatusCode, string(b))
	}
	chgResp.Body.Close()

	// client1 now accesses /api/v1/properties with 200!
	okAfterRotate, _ := tempClient1.Get(srv.URL + "/api/v1/properties")
	okAfterRotate.Body.Close()
	if okAfterRotate.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after password rotation on current client, got %d", okAfterRotate.StatusCode)
	}
	// client2 prior session MUST be revoked -> 401!
	revokedOther, _ := tempClient2.Get(srv.URL + "/api/v1/properties")
	revokedOther.Body.Close()
	if revokedOther.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on prior session after password rotation, got %d", revokedOther.StatusCode)
	}

	// Scenario 11: Append-only audit_logs trigger rejects UPDATE, DELETE, and TRUNCATE at PostgreSQL level
	if _, err := store.Pool().Exec(context.Background(), `DELETE FROM audit_logs`); err == nil {
		t.Fatal("expected PostgreSQL append-only trigger to reject DELETE on audit_logs")
	}
	if _, err := store.Pool().Exec(context.Background(), `UPDATE audit_logs SET action = 'tampered'`); err == nil {
		t.Fatal("expected PostgreSQL append-only trigger to reject UPDATE on audit_logs")
	}
	if _, err := store.Pool().Exec(context.Background(), `TRUNCATE TABLE audit_logs`); err == nil {
		t.Fatal("expected PostgreSQL append-only trigger to reject TRUNCATE on audit_logs")
	}
	_ = uMead2
}

func mustCreateUnitType(t *testing.T, client *http.Client, baseURL string, propID uuid.UUID, csrf, code, name, cat string, ratePence int64) domain.UnitType {
	t.Helper()
	resp := doJSON(t, client, http.MethodPost, baseURL+"/api/v1/properties/"+propID.String()+"/unit-types", csrf, map[string]any{
		"code": code, "name": name, "category": cat,
		"base_capacity_adults": 2, "max_adults": 4, "max_children": 2, "max_infants": 1, "max_guests": 6,
		"base_nightly_rate_pence": ratePence, "min_nights": 1, "max_nights": 28,
	})
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create unit type %s failed (%d): %s", code, resp.StatusCode, string(b))
	}
	defer resp.Body.Close()
	var ut domain.UnitType
	_ = json.NewDecoder(resp.Body).Decode(&ut)
	return ut
}

func mustCreateUnit(t *testing.T, client *http.Client, baseURL string, propID uuid.UUID, csrf string, utID uuid.UUID, code, name string) domain.Unit {
	t.Helper()
	resp := doJSON(t, client, http.MethodPost, baseURL+"/api/v1/properties/"+propID.String()+"/units", csrf, map[string]any{
		"unit_type_id": utID, "code": code, "name": name, "status": "active",
	})
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create unit %s failed (%d): %s", code, resp.StatusCode, string(b))
	}
	defer resp.Body.Close()
	var u domain.Unit
	_ = json.NewDecoder(resp.Body).Decode(&u)
	return u
}
