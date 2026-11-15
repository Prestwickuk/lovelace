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

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/channels"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
)

func TestPhase1_5HardeningChannelAllocationsAndRebrand(t *testing.T) {
	srv, store, prop := setupTestServer(t)
	ownerClient := newClient(t)
	code, csrf := apiLogin(t, ownerClient, srv.URL, "owner@tirbach.wales", "tirbach-owner-password-2026")
	if code != http.StatusOK {
		t.Fatalf("owner login failed: %d", code)
	}

	// Setup unit types and units with distinct occupancy limits & statuses.
	utPod := mustCreateUnitTypeCustom(t, ownerClient, srv.URL, prop.ID, csrf, "pod-occ", "Timber Pod", "cabin", 2, 1, 1, 3, 11000)
	utLodge := mustCreateUnitTypeCustom(t, ownerClient, srv.URL, prop.ID, csrf, "lodge-occ", "Heritage Lodge", "glamping", 4, 2, 1, 6, 18000)
	uPodA := mustCreateUnit(t, ownerClient, srv.URL, prop.ID, csrf, utPod.ID, "POD-A", "Pod A")
	uPodB := mustCreateUnit(t, ownerClient, srv.URL, prop.ID, csrf, utPod.ID, "POD-B", "Pod B")
	uLodge1 := mustCreateUnit(t, ownerClient, srv.URL, prop.ID, csrf, utLodge.ID, "LDG-1", "Lodge 1")

	// 1. Create an inactive unit and archived unit for status validation checks.
	respInact := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/units", csrf, map[string]any{
		"unit_type_id": utPod.ID, "code": "POD-INACT", "name": "Inactive Pod", "status": "inactive",
	})
	if respInact.StatusCode != http.StatusCreated {
		t.Fatalf("create inactive unit failed: %d", respInact.StatusCode)
	}
	var uInactive domain.Unit
	_ = json.NewDecoder(respInact.Body).Decode(&uInactive)
	respInact.Body.Close()

	// -------------------------------------------------------------------------
	// A. OCCUPANCY & UNIT STATUS VALIDATION
	// -------------------------------------------------------------------------
	// A1. Excess adults (> max_adults=2) rejected with 422
	rExcessAdults := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Too Many Adults", "channel_code": "DIRECT",
		"arrival_date": "2026-07-01", "departure_date": "2026-07-03",
		"adults": 3, "children": 0, "infants": 0, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uPodA.ID},
	})
	rExcessAdults.Body.Close()
	if rExcessAdults.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for excess adults, got %d", rExcessAdults.StatusCode)
	}

	// A2. Excess combined max_guests (adults=2, children=1, infants=1 => total=4 > max_guests=3) rejected with 422
	rExcessCombined := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Too Many Total Guests", "channel_code": "DIRECT",
		"arrival_date": "2026-07-01", "departure_date": "2026-07-03",
		"adults": 2, "children": 1, "infants": 1, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uPodA.ID},
	})
	rExcessCombined.Body.Close()
	if rExcessCombined.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for excess combined max_guests, got %d", rExcessCombined.StatusCode)
	}

	// A3. Multi-unit aggregate capacity succeeds (POD-A + POD-B combined max_adults=4, max_children=2, max_guests=6)
	rMultiAgg := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Family Duo Group", "channel_code": "DIRECT",
		"arrival_date": "2026-07-05", "departure_date": "2026-07-08",
		"adults": 4, "children": 2, "infants": 0, "status": "CONFIRMED",
		"unit_ids": []uuid.UUID{uPodA.ID, uPodB.ID},
	})
	if rMultiAgg.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(rMultiAgg.Body)
		t.Fatalf("expected 201 multi-unit aggregate occupancy, got %d: %s", rMultiAgg.StatusCode, string(b))
	}
	rMultiAgg.Body.Close()

	// A4. Explicit per-unit allocation rejects exceeding single unit limit & sum mismatch, accepts valid split & persists
	rBadPerUnit := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Invalid Split", "channel_code": "DIRECT",
		"arrival_date": "2026-07-10", "departure_date": "2026-07-12",
		"adults": 4, "children": 0, "infants": 0, "status": "CONFIRMED",
		"units": []map[string]any{
			{"unit_id": uPodA.ID, "adults": 3, "children": 0, "infants": 0}, // exceeds Pod max_adults=2
			{"unit_id": uLodge1.ID, "adults": 1, "children": 0, "infants": 0},
		},
	})
	rBadPerUnit.Body.Close()
	if rBadPerUnit.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 when single unit allocation exceeds unit type limit, got %d", rBadPerUnit.StatusCode)
	}

	rSumMismatch := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Sum Mismatch", "channel_code": "DIRECT",
		"arrival_date": "2026-07-10", "departure_date": "2026-07-12",
		"adults": 4, "children": 0, "infants": 0, "status": "CONFIRMED",
		"units": []map[string]any{
			{"unit_id": uPodA.ID, "adults": 2, "children": 0, "infants": 0},
			{"unit_id": uLodge1.ID, "adults": 1, "children": 0, "infants": 0}, // sums to 3 != 4
		},
	})
	rSumMismatch.Body.Close()
	if rSumMismatch.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on per-unit allocation sum mismatch, got %d", rSumMismatch.StatusCode)
	}

	rGoodPerUnit := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Valid Per-Unit Split", "channel_code": "DIRECT",
		"arrival_date": "2026-07-10", "departure_date": "2026-07-12",
		"adults": 4, "children": 1, "infants": 1, "status": "CONFIRMED",
		"units": []map[string]any{
			{"unit_id": uPodA.ID, "adults": 2, "children": 0, "infants": 1},
			{"unit_id": uLodge1.ID, "adults": 2, "children": 1, "infants": 0},
		},
	})
	if rGoodPerUnit.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(rGoodPerUnit.Body)
		t.Fatalf("expected 201 valid per-unit split, got %d: %s", rGoodPerUnit.StatusCode, string(b))
	}
	var resSplit domain.Reservation
	_ = json.NewDecoder(rGoodPerUnit.Body).Decode(&resSplit)
	rGoodPerUnit.Body.Close()
	if len(resSplit.Units) != 2 || (resSplit.Units[0].Adults+resSplit.Units[1].Adults) != 4 {
		t.Fatalf("expected persisted per-unit allocations on response, got %+v", resSplit.Units)
	}

	// A5. Inactive unit rejected for reservations, blocks, and unit-scoped releases (422)
	rInactRes := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Inactive Unit Guest", "channel_code": "DIRECT",
		"arrival_date": "2026-07-15", "departure_date": "2026-07-17",
		"adults": 1, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uInactive.ID},
	})
	rInactRes.Body.Close()
	if rInactRes.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 booking inactive unit, got %d", rInactRes.StatusCode)
	}
	rInactBlk := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts", csrf, map[string]any{
		"scope_type": "UNIT", "unit_ids": []uuid.UUID{uInactive.ID}, "category": "MAINTENANCE",
		"channel_scope": "ALL", "start_date": "2026-07-15", "end_date": "2026-07-17", "reason": "Blocked inactive",
	})
	rInactBlk.Body.Close()
	if rInactBlk.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 creating blockout on inactive unit, got %d", rInactBlk.StatusCode)
	}
	rInactRel := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/releases", csrf, map[string]any{
		"scope_type": "UNIT", "unit_id": uInactive.ID, "start_date": "2026-07-15", "end_date": "2026-07-17",
		"channel_code": "AIRBNB", "release_at": "2026-07-14T12:00:00Z", "notes": "Inactive release test",
	})
	rInactRel.Body.Close()
	if rInactRel.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 creating release rule on inactive unit, got %d", rInactRel.StatusCode)
	}

	// -------------------------------------------------------------------------
	// B. CHANNEL ALLOCATIONS, ATOMIC SEGMENT CONSUMPTION & CONCURRENCY
	// -------------------------------------------------------------------------
	// B1. Scope validation rules for categories
	badAllocAll := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts", csrf, map[string]any{
		"scope_type": "UNIT", "unit_ids": []uuid.UUID{uPodA.ID}, "category": "CHANNEL_ALLOCATION",
		"channel_scope": "ALL", "start_date": "2026-08-10", "end_date": "2026-08-20", "reason": "Must specify channel",
	})
	badAllocAll.Body.Close()
	if badAllocAll.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for CHANNEL_ALLOCATION with channel_scope=ALL, got %d", badAllocAll.StatusCode)
	}

	badOwnerCh := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts", csrf, map[string]any{
		"scope_type": "UNIT", "unit_ids": []uuid.UUID{uPodA.ID}, "category": "OWNER_USE",
		"channel_scope": "AIRBNB", "start_date": "2026-08-10", "end_date": "2026-08-20", "reason": "Owner stay is physical",
	})
	badOwnerCh.Body.Close()
	if badOwnerCh.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for OWNER_USE with channel_scope!=ALL, got %d", badOwnerCh.StatusCode)
	}

	// B2. Create AIRBNB CHANNEL_ALLOCATION [2026-08-10 .. 2026-08-20) on POD-A
	allocResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts", csrf, map[string]any{
		"scope_type": "UNIT", "unit_ids": []uuid.UUID{uPodA.ID}, "category": "CHANNEL_ALLOCATION",
		"channel_scope": "AIRBNB", "start_date": "2026-08-10", "end_date": "2026-08-20", "reason": "Summer Airbnb allotment",
	})
	if allocResp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(allocResp.Body)
		t.Fatalf("expected 201 creating AIRBNB allocation, got %d: %s", allocResp.StatusCode, string(b))
	}
	var allocOut struct {
		Group  domain.BlockoutGroup    `json:"group"`
		Claims []domain.InventoryClaim `json:"claims"`
	}
	_ = json.NewDecoder(allocResp.Body).Decode(&allocOut)
	allocResp.Body.Close()

	// Non-matching channels (DIRECT & BOOKING_COM) must be rejected (409)
	for _, ch := range []string{"DIRECT", "BOOKING_COM"} {
		blk := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
			"guest_name": "Wrong Channel", "channel_code": ch,
			"arrival_date": "2026-08-12", "departure_date": "2026-08-15",
			"adults": 2, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uPodA.ID},
		})
		blk.Body.Close()
		if blk.StatusCode != http.StatusConflict {
			t.Fatalf("expected 409 for %s during AIRBNB allocation, got %d", ch, blk.StatusCode)
		}
	}

	// B3. Matching AIRBNB reservation [2026-08-12 .. 2026-08-15) consumes [10..20) and preserves [10..12) & [15..20)!
	airbnbBook := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Airbnb Matched Guest", "channel_code": "AIRBNB",
		"arrival_date": "2026-08-12", "departure_date": "2026-08-15",
		"adults": 2, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uPodA.ID},
	})
	if airbnbBook.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(airbnbBook.Body)
		t.Fatalf("expected 201 booking within matching AIRBNB allocation, got %d: %s", airbnbBook.StatusCode, string(b))
	}
	var bookedRes domain.Reservation
	_ = json.NewDecoder(airbnbBook.Body).Decode(&bookedRes)
	airbnbBook.Body.Close()

	// Inspect DB claims for allocation group: original -> CONSUMED with consumed_by_reservation_id; 2 split ACTIVE claims remain
	claims, err := store.ListClaimsByBlockoutGroup(context.Background(), nil, prop.ID, allocOut.Group.ID)
	if err != nil {
		t.Fatalf("ListClaimsByBlockoutGroup: %v", err)
	}
	var consumedCount, activeCount int
	for _, c := range claims {
		switch c.Status {
		case domain.ClaimStatusConsumed:
			consumedCount++
			if c.ConsumedByReservationID == nil || *c.ConsumedByReservationID != bookedRes.ID {
				t.Fatalf("expected consumed_by_reservation_id=%s, got %v", bookedRes.ID, c.ConsumedByReservationID)
			}
		case domain.ClaimStatusActive:
			activeCount++
		}
	}
	if consumedCount != 1 || activeCount != 2 {
		t.Fatalf("expected 1 CONSUMED and 2 ACTIVE split segments, got consumed=%d active=%d", consumedCount, activeCount)
	}

	// Prefix [2026-08-10..2026-08-12) is STILL allocated strictly to AIRBNB so DIRECT cannot take it
	directPre := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Direct Steal Prefix", "channel_code": "DIRECT",
		"arrival_date": "2026-08-10", "departure_date": "2026-08-12",
		"adults": 2, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uPodA.ID},
	})
	directPre.Body.Close()
	if directPre.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for DIRECT on remaining prefix of AIRBNB allocation, got %d", directPre.StatusCode)
	}

	// Releasing remaining unused allocation returns [10..12) and [15..20) to general pool so DIRECT books [15..18)!
	relAlloc := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts/"+allocOut.Group.ID.String()+"/release", csrf, map[string]any{})
	if relAlloc.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(relAlloc.Body)
		t.Fatalf("expected 200 releasing remaining allocation, got %d: %s", relAlloc.StatusCode, string(b))
	}
	relAlloc.Body.Close()

	directSuffix := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Direct Suffix After Release", "channel_code": "DIRECT",
		"arrival_date": "2026-08-15", "departure_date": "2026-08-18",
		"adults": 2, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uPodA.ID},
	})
	if directSuffix.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(directSuffix.Body)
		t.Fatalf("expected 201 for DIRECT after releasing unused allocation suffix, got %d: %s", directSuffix.StatusCode, string(b))
	}
	directSuffix.Body.Close()

	// B4. Concurrent consumption race test on POD-B: 5 goroutines on AIRBNB racing [2026-09-02..2026-09-05) inside [2026-09-01..2026-09-10)
	allocRace := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts", csrf, map[string]any{
		"scope_type": "UNIT", "unit_ids": []uuid.UUID{uPodB.ID}, "category": "CHANNEL_ALLOCATION",
		"channel_scope": "AIRBNB", "start_date": "2026-09-01", "end_date": "2026-09-10", "reason": "Race allotment",
	})
	if allocRace.StatusCode != http.StatusCreated {
		t.Fatalf("create race allocation failed: %d", allocRace.StatusCode)
	}
	allocRace.Body.Close()

	var okCnt, conflictCnt int32
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
				"guest_name": "Racing Airbnb", "channel_code": "AIRBNB",
				"arrival_date": "2026-09-02", "departure_date": "2026-09-05",
				"adults": 2, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uPodB.ID},
			})
			defer r.Body.Close()
			if r.StatusCode == http.StatusCreated {
				atomic.AddInt32(&okCnt, 1)
			} else if r.StatusCode == http.StatusConflict {
				atomic.AddInt32(&conflictCnt, 1)
			}
		}()
	}
	wg.Wait()
	if okCnt != 1 || conflictCnt != 4 {
		t.Fatalf("concurrent allocation race expected 1 created / 4 conflict, got ok=%d conflict=%d", okCnt, conflictCnt)
	}

	// -------------------------------------------------------------------------
	// C. CHANNEL RESTRICTIONS (STOP-SELL) & CALENDAR CHANNEL AWARENESS
	// -------------------------------------------------------------------------
	// Stop-sell AIRBNB and BOOKING_COM simultaneously on Lodge 1 [2026-10-01..2026-10-05)
	for _, ch := range []string{"AIRBNB", "BOOKING_COM"} {
		rest := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts", csrf, map[string]any{
			"scope_type": "UNIT", "unit_ids": []uuid.UUID{uLodge1.ID}, "category": "CHANNEL_RESTRICTION",
			"channel_scope": ch, "start_date": "2026-10-01", "end_date": "2026-10-05", "reason": "Stop sell " + ch,
		})
		if rest.StatusCode != http.StatusCreated {
			b, _ := io.ReadAll(rest.Body)
			t.Fatalf("expected 201 creating CHANNEL_RESTRICTION for %s, got %d: %s", ch, rest.StatusCode, string(b))
		}
		rest.Body.Close()
	}

	// AIRBNB booking on Lodge 1 blocked (409), while DIRECT remains bookable (201)
	airbnbStop := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Stopped Airbnb", "channel_code": "AIRBNB",
		"arrival_date": "2026-10-02", "departure_date": "2026-10-04",
		"adults": 2, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uLodge1.ID},
	})
	airbnbStop.Body.Close()
	if airbnbStop.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for AIRBNB under CHANNEL_RESTRICTION, got %d", airbnbStop.StatusCode)
	}

	// Calendar query for AIRBNB shows Lodge 1 BLOCKED on 2026-10-02, whereas DIRECT shows AVAILABLE
	calAirResp, err := ownerClient.Get(srv.URL + "/api/v1/properties/" + prop.ID.String() + "/calendar?start_date=2026-10-01&end_date=2026-10-04&channel_code=AIRBNB")
	if err != nil {
		t.Fatalf("calendar AIRBNB GET: %v", err)
	}
	var calAir service.CalendarViewData
	_ = json.NewDecoder(calAirResp.Body).Decode(&calAir)
	calAirResp.Body.Close()

	calDirResp, err := ownerClient.Get(srv.URL + "/api/v1/properties/" + prop.ID.String() + "/calendar?start_date=2026-10-01&end_date=2026-10-04&channel_code=DIRECT")
	if err != nil {
		t.Fatalf("calendar DIRECT GET: %v", err)
	}
	var calDir service.CalendarViewData
	_ = json.NewDecoder(calDirResp.Body).Decode(&calDir)
	calDirResp.Body.Close()

	findUnitCell := func(view service.CalendarViewData, unitID uuid.UUID) string {
		for _, row := range view.Units {
			if row.Unit.ID == unitID && len(row.Cells) > 0 {
				return row.Cells[0].Status
			}
		}
		return ""
	}
	if st := findUnitCell(calAir, uLodge1.ID); st != "BLOCKED" {
		t.Fatalf("expected Lodge 1 calendar status BLOCKED for AIRBNB, got %q", st)
	}
	if st := findUnitCell(calDir, uLodge1.ID); st != "AVAILABLE" {
		t.Fatalf("expected Lodge 1 calendar status AVAILABLE for DIRECT, got %q", st)
	}

	// Now book DIRECT on Lodge 1 while OTA stop-sells remain active -> succeeds (201)
	directOK := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Direct Under StopSell", "channel_code": "DIRECT",
		"arrival_date": "2026-10-02", "departure_date": "2026-10-04",
		"adults": 2, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uLodge1.ID},
	})
	directOK.Body.Close()
	if directOK.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 for DIRECT during OTA CHANNEL_RESTRICTION, got %d", directOK.StatusCode)
	}

	// -------------------------------------------------------------------------
	// D. CHANNEL MANAGER AUTH MODELS (SMOOBU HMAC-SHA256 & CHANNEX USER-API-KEY)
	// -------------------------------------------------------------------------
	cmListResp, err := ownerClient.Get(srv.URL + "/api/v1/properties/" + prop.ID.String() + "/integrations/channel-manager")
	if err != nil {
		t.Fatalf("list channel manager: %v", err)
	}
	var cmList struct {
		Items []service.ChannelProviderView `json:"items"`
	}
	_ = json.NewDecoder(cmListResp.Body).Decode(&cmList)
	cmListResp.Body.Close()

	byCode := map[string]channels.ProviderDefinition{}
	for _, p := range cmList.Items {
		byCode[p.Provider.Code] = p.Provider
	}
	if smoobu, ok := byCode["smoobu"]; !ok || smoobu.AuthMethod != channels.AuthHMAC || smoobu.SignatureSpec == nil {
		t.Fatalf("expected Smoobu to use hmac_sha256 with SignatureSpec, got %+v", byCode["smoobu"])
	}
	if chx, ok := byCode["channex"]; !ok || chx.AuthMethod != channels.AuthAPIKey || !strings.Contains(chx.AuthSummary, "user-api-key") {
		t.Fatalf("expected Channex to use api_key via user-api-key header, got %+v", byCode["channex"])
	}

	// Save Smoobu HMAC config and verify secret masking & non-CONNECTED safety invariant
	saveSmoobu := doJSON(t, ownerClient, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/smoobu", csrf, map[string]any{
		"display_name": "Smoobu Tir Bach",
		"inputs": map[string]string{
			"api_key":    "smoobu_pub_key_1234",
			"api_secret": "smoobu_hmac_secret_9988",
		},
	})
	if saveSmoobu.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(saveSmoobu.Body)
		t.Fatalf("expected 200 saving Smoobu HMAC creds, got %d: %s", saveSmoobu.StatusCode, string(b))
	}
	var savedConn channels.ChannelConnection
	_ = json.NewDecoder(saveSmoobu.Body).Decode(&savedConn)
	saveSmoobu.Body.Close()
	if savedConn.AuthMethod != channels.AuthHMAC || savedConn.HasLiveAdapter || savedConn.Status == channels.StatusConnected {
		t.Fatalf("unexpected saved Smoobu connection state: %+v", savedConn)
	}
	if savedConn.CredentialHints["api_secret"] == "smoobu_hmac_secret_9988" {
		t.Fatal("plaintext api_secret leaked in credential hint")
	}

	// Attempting to mark Smoobu CONNECTED without live adapter fails closed (422)
	connFail := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/smoobu/status", csrf, map[string]any{
		"status": "CONNECTED",
	})
	connFail.Body.Close()
	if connFail.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 promoting unimplemented channel adapter to CONNECTED, got %d", connFail.StatusCode)
	}

	// -------------------------------------------------------------------------
	// E. LOVELACE PLATFORM BRANDING, ASSETS & BRANDED ERROR PAGES
	// -------------------------------------------------------------------------
	// E1. Login page & static logo assets
	loginPage, err := newClient(t).Get(srv.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	var loginBuf bytes.Buffer
	_, _ = loginBuf.ReadFrom(loginPage.Body)
	loginPage.Body.Close()
	if loginPage.StatusCode != http.StatusOK || !strings.Contains(loginBuf.String(), "LOVELACE Booking System") || !strings.Contains(loginBuf.String(), "/static/brand/lovelace-logo-full.png") {
		t.Fatalf("login page missing LOVELACE branding/logo (status=%d)", loginPage.StatusCode)
	}

	for _, asset := range []string{
		"/static/brand/lovelace-favicon.png",
		"/static/brand/lovelace-logo-compact.png",
		"/static/brand/lovelace-logo-emblem.png",
		"/static/brand/lovelace-logo-full.png",
		"/static/app.css",
	} {
		ar, err := ownerClient.Get(srv.URL + asset)
		if err != nil {
			t.Fatalf("GET %s: %v", asset, err)
		}
		var ab bytes.Buffer
		_, _ = ab.ReadFrom(ar.Body)
		ar.Body.Close()
		if ar.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d", asset, ar.StatusCode)
		}
		if asset == "/static/app.css" {
			css := strings.ToLower(ab.String())
			if !strings.Contains(css, "prefers-color-scheme: dark") || !strings.Contains(css, "#2f4f4a") {
				t.Fatalf("app.css missing LOVELACE palette / dark mode tokens")
			}
		}
	}

	// E2. Create readonly_staff user and verify Web UI renders LOVELACE-branded error.html on 403 (/users)
	mkStaff := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/users", csrf, map[string]any{
		"property_id": prop.ID, "email": "rostaff@tirbach.wales", "full_name": "Front Desk Staff",
		"role": "staff", "password": "ro-staff-password-2026",
	})
	if mkStaff.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(mkStaff.Body)
		t.Fatalf("expected 201 creating readonly_staff, got %d: %s", mkStaff.StatusCode, string(b))
	}
	mkStaff.Body.Close()

	roClient := newClient(t)
	roForm := url.Values{}
	roForm.Set("email", "rostaff@tirbach.wales")
	roForm.Set("password", "ro-staff-password-2026")
	roLogin, err := roClient.PostForm(srv.URL+"/login", roForm)
	if err != nil {
		t.Fatalf("staff web login: %v", err)
	}
	roLogin.Body.Close()

	err403Resp, err := roClient.Get(srv.URL + "/users")
	if err != nil {
		t.Fatalf("GET /users as readonly staff: %v", err)
	}
	var err403Buf bytes.Buffer
	_, _ = err403Buf.ReadFrom(err403Resp.Body)
	err403Resp.Body.Close()
	if err403Resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for readonly /users, got %d", err403Resp.StatusCode)
	}
	if !strings.Contains(err403Buf.String(), "LOVELACE") || !strings.Contains(err403Buf.String(), "HTTP 403") {
		t.Fatalf("expected LOVELACE-branded error.html on 403, got: %s", err403Buf.String())
	}
}

func mustCreateUnitTypeCustom(t *testing.T, client *http.Client, baseURL string, propID uuid.UUID, csrf, code, name, cat string, maxAdults, maxChildren, maxInfants, maxGuests int, ratePence int64) domain.UnitType {
	t.Helper()
	resp := doJSON(t, client, http.MethodPost, baseURL+"/api/v1/properties/"+propID.String()+"/unit-types", csrf, map[string]any{
		"code": code, "name": name, "category": cat,
		"base_capacity_adults": 2, "max_adults": maxAdults, "max_children": maxChildren,
		"max_infants": maxInfants, "max_guests": maxGuests,
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
