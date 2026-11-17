package app_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/channels"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
)

func TestPhase5_RefundWebhookPersistenceAndLegacyUndatedExclusion(t *testing.T) {
	srv, migratorStore, appSvc, prop, _, _, clk := setupPhase2TestServer(t, "")
	ctx := context.Background()
	adminClient := newClient(t)
	if st, _ := apiLogin(t, adminClient, srv.URL, "owner@tirbach.wales", "tirbach-owner-password-2026"); st != http.StatusOK {
		t.Fatalf("admin login failed: %d", st)
	}
	pubClient := newClient(t)

	units, err := migratorStore.ListUnits(ctx, prop.ID, nil, false)
	if err != nil || len(units) == 0 {
		t.Fatalf("list units: %v", err)
	}
	var ownerID uuid.UUID
	if err := migratorStore.Pool().QueryRow(ctx, `SELECT id FROM admin_users WHERE email = 'owner@tirbach.wales' LIMIT 1`).Scan(&ownerID); err != nil {
		t.Fatalf("lookup owner: %v", err)
	}
	ownerActor := service.Actor{UserID: ownerID, PropertyID: &prop.ID, Email: "owner@tirbach.wales", Role: domain.RoleOwnerAdmin}

	// Reservation 1: paid at 2026-07-02T23:30:00Z (= 2026-07-03 BST), refunded in two partial Stripe webhooks on 2026-07-10T23:30:00Z (= 2026-07-11 BST) and 2026-07-15T12:00:00Z.
	res1, err := appSvc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Refund Timing Guest",
		ChannelCode:   "DIRECT",
		ArrivalDate:   time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		DepartureDate: time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC),
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{units[0].ID},
	})
	if err != nil {
		t.Fatalf("create res1: %v", err)
	}
	att1ID, pay1ID := uuid.New(), uuid.New()
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO payment_attempts (id, property_id, reservation_id, provider, provider_payment_intent_id, payment_type, amount_pence, currency, idempotency_key, status)
		VALUES ($1, $2, $3, 'stripe', 'pi_ref_time_1', 'FULL', 12000, 'GBP', 'idem-ref-time-1', 'SUCCEEDED')
	`, att1ID, prop.ID, res1.ID); err != nil {
		t.Fatalf("insert att1: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO payments (id, property_id, reservation_id, payment_attempt_id, provider, provider_payment_intent_id, payment_type, amount_pence, currency, status, paid_at)
		VALUES ($1, $2, $3, $4, 'stripe', 'pi_ref_time_1', 'FULL', 12000, 'GBP', 'SUCCEEDED', '2026-07-02T23:30:00Z'::timestamptz)
	`, pay1ID, prop.ID, res1.ID, att1ID); err != nil {
		t.Fatalf("insert pay1: %v", err)
	}

	whSecret := "whsec_test_lovelace_phase2_secret"
	rf1Time := time.Date(2026, 7, 10, 23, 30, 0, 0, time.UTC).Unix() // 2026-07-11 00:30 BST!
	evt1 := map[string]any{
		"id":       "evt_rf_partial_1",
		"type":     "charge.refunded",
		"created":  rf1Time,
		"livemode": false,
		"data": map[string]any{
			"object": map[string]any{
				"id":              "ch_ref_time_1",
				"payment_intent":  "pi_ref_time_1",
				"amount":          12000,
				"amount_refunded": 5000,
				"currency":        "gbp",
				"refunds": map[string]any{
					"data": []map[string]any{
						{
							"id":       "re_part_1",
							"amount":   5000,
							"currency": "gbp",
							"status":   "succeeded",
							"reason":   "requested_by_customer",
							"created":  rf1Time,
						},
					},
				},
			},
		},
	}
	if st, _ := signAndSendStripeWebhook(t, pubClient, srv.URL, whSecret, clk.Now(), evt1); st != http.StatusOK {
		t.Fatalf("webhook evt1 failed: %d", st)
	}

	rf2Time := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC).Unix()
	evt2 := map[string]any{
		"id":       "evt_rf_partial_2",
		"type":     "charge.refunded",
		"created":  rf2Time,
		"livemode": false,
		"data": map[string]any{
			"object": map[string]any{
				"id":              "ch_ref_time_1",
				"payment_intent":  "pi_ref_time_1",
				"amount":          12000,
				"amount_refunded": 12000,
				"currency":        "gbp",
				"refunds": map[string]any{
					"data": []map[string]any{
						{
							"id":       "re_part_1",
							"amount":   5000,
							"currency": "gbp",
							"status":   "succeeded",
							"reason":   "requested_by_customer",
							"created":  rf1Time,
						},
						{
							"id":       "re_part_2",
							"amount":   7000,
							"currency": "gbp",
							"status":   "succeeded",
							"reason":   "duplicate",
							"created":  rf2Time,
						},
					},
				},
			},
		},
	}
	if st, _ := signAndSendStripeWebhook(t, pubClient, srv.URL, whSecret, clk.Now(), evt2); st != http.StatusOK {
		t.Fatalf("webhook evt2 failed: %d", st)
	}
	// Out-of-order replay with new event ID containing older partial snapshot must stay idempotent and NOT lose REFUNDED status or duplicate re_part_1.
	evtReplay := map[string]any{
		"id":       "evt_rf_replay_ooo",
		"type":     "charge.refunded",
		"created":  rf1Time,
		"livemode": false,
		"data":     evt1["data"],
	}
	if st, _ := signAndSendStripeWebhook(t, pubClient, srv.URL, whSecret, clk.Now(), evtReplay); st != http.StatusOK {
		t.Fatalf("webhook replay failed: %d", st)
	}

	var refundRowCount int
	var refundTotal int64
	if err := migratorStore.Pool().QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(SUM(amount_pence), 0)::bigint FROM refunds WHERE payment_attempt_id = $1
	`, att1ID).Scan(&refundRowCount, &refundTotal); err != nil {
		t.Fatalf("query refunds: %v", err)
	}
	if refundRowCount != 2 || refundTotal != 12000 {
		t.Fatalf("expected 2 idempotent refund rows summing 12000, got count=%d sum=%d", refundRowCount, refundTotal)
	}
	var finalPayStatus string
	if err := migratorStore.Pool().QueryRow(ctx, `SELECT status FROM payments WHERE id = $1`, pay1ID).Scan(&finalPayStatus); err != nil || finalPayStatus != "REFUNDED" {
		t.Fatalf("expected final payment status REFUNDED after replay, got %q (err=%v)", finalPayStatus, err)
	}

	// Legacy payment marked REFUNDED without any persisted refunds row (must NOT backdate onto payments.paid_at!)
	resLegacy, err := appSvc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Legacy Refund Guest",
		ChannelCode:   "DIRECT",
		ArrivalDate:   time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC),
		DepartureDate: time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC),
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{units[0].ID},
	})
	if err != nil {
		t.Fatalf("create resLegacy: %v", err)
	}
	attLegacyID, payLegacyID := uuid.New(), uuid.New()
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO payment_attempts (id, property_id, reservation_id, provider, provider_payment_intent_id, payment_type, amount_pence, currency, idempotency_key, status)
		VALUES ($1, $2, $3, 'stripe', 'pi_legacy_nodate', 'FULL', 8000, 'GBP', 'idem-legacy-nodate', 'REFUNDED')
	`, attLegacyID, prop.ID, resLegacy.ID); err != nil {
		t.Fatalf("insert attLegacy: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO payments (id, property_id, reservation_id, payment_attempt_id, provider, provider_payment_intent_id, payment_type, amount_pence, currency, status, paid_at)
		VALUES ($1, $2, $3, $4, 'stripe', 'pi_legacy_nodate', 'FULL', 8000, 'GBP', 'REFUNDED', '2026-07-04T10:00:00Z'::timestamptz)
	`, payLegacyID, prop.ID, resLegacy.ID, attLegacyID); err != nil {
		t.Fatalf("insert payLegacy: %v", err)
	}

	// Revenue report for 2026-07-01..2026-07-11 MUST show GrossCollectedPaymentsPence=20000 and RefundedPence=0
	// (neither re_part_1 on 2026-07-11 BST nor legacy REFUNDED payment without refunds row may be counted in 07-01..07-11!),
	// AND must flag UnresolvedLegacyRefundPaymentsCount=1!
	revPreResp, err := adminClient.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/revenue?start_date=2026-07-01&end_date=2026-07-11", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET revenue pre: %v", err)
	}
	defer revPreResp.Body.Close()
	var revPre domain.RevenueReport
	if err := json.NewDecoder(revPreResp.Body).Decode(&revPre); err != nil {
		t.Fatalf("decode revPre: %v", err)
	}
	if revPre.Completeness.UnresolvedLegacyRefundPaymentsCount != 1 {
		t.Fatalf("expected UnresolvedLegacyRefundPaymentsCount=1, got %+v", revPre.Completeness)
	}
	for _, cur := range revPre.ByCurrency {
		if cur.Currency == "GBP" && (cur.GrossCollectedPaymentsPence != 20000 || cur.RefundedPence != 0) {
			t.Fatalf("expected 0 refunds attributed to 2026-07-01..2026-07-11 (no paid_at backdating!), got %+v", cur)
		}
	}

	// Revenue report & refund_ledger CSV for 2026-07-11..2026-07-16 MUST capture both re_part_1 (2026-07-11) and re_part_2 (2026-07-15) = 12000!
	revPostResp, err := adminClient.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/revenue?start_date=2026-07-11&end_date=2026-07-16", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET revenue post: %v", err)
	}
	defer revPostResp.Body.Close()
	var revPost domain.RevenueReport
	_ = json.NewDecoder(revPostResp.Body).Decode(&revPost)
	if len(revPost.ByCurrency) != 1 || revPost.ByCurrency[0].RefundedPence != 12000 {
		t.Fatalf("expected 12000 refunded on actual occurred_at dates 07-11..07-16, got %+v", revPost.ByCurrency)
	}

	csvResp, err := adminClient.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/accounting-export?type=refund_ledger&format=csv&start_date=2026-07-11&end_date=2026-07-16", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET refund_ledger csv: %v", err)
	}
	defer csvResp.Body.Close()
	csvBytes, _ := io.ReadAll(csvResp.Body)
	recs, err := csv.NewReader(bytes.NewReader(csvBytes)).ReadAll()
	if err != nil || len(recs) != 3 {
		t.Fatalf("expected 2 persisted refund rows (+header=3) in refund_ledger CSV, got len=%d err=%v raw=%s", len(recs), err, string(csvBytes))
	}
	if recs[1][5] != "2026-07-11" || recs[2][5] != "2026-07-15" {
		t.Fatalf("expected BST refund local dates 2026-07-11 and 2026-07-15, got %q and %q", recs[1][5], recs[2][5])
	}

	jsonExpResp, err := adminClient.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/accounting-export?type=refund_ledger&format=json&start_date=2026-07-01&end_date=2026-07-11", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET refund_ledger json: %v", err)
	}
	defer jsonExpResp.Body.Close()
	var jsonExp domain.AccountingExportResult
	_ = json.NewDecoder(jsonExpResp.Body).Decode(&jsonExp)
	if jsonExp.RowCount != 0 || jsonExp.UnresolvedLegacyRefundPaymentsCount != 1 {
		t.Fatalf("expected 0 synthetic fallback refund rows in 07-01..07-11 and unresolved count=1, got %+v", jsonExp)
	}
}

func TestPhase5_DateAwareOccupancySupplyHistoryAndPreTrackingBoundary(t *testing.T) {
	srv, migratorStore, appSvc, prop, _, _ := setupPhase4AppServer(t, "phase5-inv-hist", channels.AdapterConfig{})
	ctx := context.Background()

	client := newClient(t)
	if st, _ := apiLogin(t, client, srv.URL, "owner@tirbach.wales", "tirbach-owner-password-2026"); st != http.StatusOK {
		t.Fatalf("login failed: %d", st)
	}
	var ownerID uuid.UUID
	if err := migratorStore.Pool().QueryRow(ctx, `SELECT id FROM admin_users WHERE email = 'owner@tirbach.wales' LIMIT 1`).Scan(&ownerID); err != nil {
		t.Fatalf("lookup owner: %v", err)
	}
	ownerActor := service.Actor{UserID: ownerID, PropertyID: &prop.ID, Email: "owner@tirbach.wales", Role: domain.RoleOwnerAdmin}

	uts, err := migratorStore.ListUnitTypes(ctx, prop.ID, false)
	if err != nil || len(uts) == 0 {
		t.Fatalf("list unit types: %v", err)
	}
	utID := uts[0].ID

	// Retire all seeded baseline units prior to 2026-07-01 so we have a clean, isolated inventory timeline starting 2026-07-01.
	seeded, _ := migratorStore.ListUnits(ctx, prop.ID, nil, false)
	for _, u := range seeded {
		if _, err := migratorStore.Pool().Exec(ctx, `UPDATE units SET status = 'retired', updated_at = '2026-06-01T00:00:00Z' WHERE id = $1`, u.ID); err != nil {
			t.Fatalf("retire seeded unit: %v", err)
		}
		if _, err := migratorStore.Pool().Exec(ctx, `UPDATE unit_status_history SET effective_from = '2026-06-01T00:00:00Z' WHERE unit_id = $1`, u.ID); err != nil {
			t.Fatalf("anchor seeded unit history: %v", err)
		}
	}

	// Unit A: commissioned 2026-07-01, active throughout 2026-07-01..2026-07-11 (10 nights).
	unitA, err := appSvc.CreateUnit(ctx, ownerActor, domain.Unit{
		PropertyID: prop.ID, UnitTypeID: utID, Code: "HIST-A", Name: "History Unit A", Status: domain.UnitStatusActive,
	})
	if err != nil {
		t.Fatalf("create unitA: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `UPDATE units SET created_at = '2026-07-01T00:00:00Z' WHERE id = $1`, unitA.ID); err != nil {
		t.Fatalf("set unitA created_at: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `UPDATE unit_status_history SET effective_from = '2026-07-01T00:00:00Z' WHERE unit_id = $1`, unitA.ID); err != nil {
		t.Fatalf("set unitA history: %v", err)
	}

	// Unit B: added mid-period on 2026-07-05, active on 07-05..07-06 (2 nights), moved to 'maintenance' on 07-07 (1 physical + OOO night),
	// moved to 'inactive' on 07-08 (0 physical), then archived/retired on 07-10 (0 physical).
	unitB, err := appSvc.CreateUnit(ctx, ownerActor, domain.Unit{
		PropertyID: prop.ID, UnitTypeID: utID, Code: "HIST-B", Name: "History Unit B", Status: domain.UnitStatusActive,
	})
	if err != nil {
		t.Fatalf("create unitB: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `UPDATE units SET created_at = '2026-07-05T00:00:00Z' WHERE id = $1`, unitB.ID); err != nil {
		t.Fatalf("set unitB created_at: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `UPDATE unit_status_history SET effective_from = '2026-07-05T00:00:00Z' WHERE unit_id = $1 AND source = 'unit_created'`, unitB.ID); err != nil {
		t.Fatalf("set unitB initial history: %v", err)
	}

	// Put a confirmed 1-night stay on Unit B on 2026-07-06 (BEFORE archival!) so archiving later must preserve that historical occupied & physical night.
	if _, err := appSvc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Historical Unit B Guest",
		ChannelCode:   "DIRECT",
		ArrivalDate:   time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC),
		DepartureDate: time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC),
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unitB.ID},
	}); err != nil {
		t.Fatalf("create booking on unitB: %v", err)
	}

	// Real app service UpdateUnit -> maintenance, then inactive, then ArchiveUnit!
	unitB.Status = domain.UnitStatusMaintenance
	unitB, err = appSvc.UpdateUnit(ctx, ownerActor, unitB)
	if err != nil {
		t.Fatalf("update unitB maintenance: %v", err)
	}
	unitB.Status = domain.UnitStatusInactive
	unitB, err = appSvc.UpdateUnit(ctx, ownerActor, unitB)
	if err != nil {
		t.Fatalf("update unitB inactive: %v", err)
	}
	if err := appSvc.ArchiveUnit(ctx, ownerActor, prop.ID, unitB.ID); err != nil {
		t.Fatalf("archive unitB: %v", err)
	}
	// Place timestamps of the trigger-recorded transitions on 07-07, 07-08, 07-10.
	for _, stmt := range []string{
		`UPDATE unit_status_history SET effective_from = '2026-07-07T09:00:00Z' WHERE unit_id = $1 AND status = 'maintenance'`,
		`UPDATE unit_status_history SET effective_from = '2026-07-08T09:00:00Z' WHERE unit_id = $1 AND status = 'inactive'`,
		`UPDATE unit_status_history SET effective_from = '2026-07-10T09:00:00Z' WHERE unit_id = $1 AND is_archived = TRUE`,
	} {
		if _, err := migratorStore.Pool().Exec(ctx, stmt, unitB.ID); err != nil {
			t.Fatalf("timestamp unitB lifecycle event (%s): %v", stmt, err)
		}
	}

	// Query 2026-07-01..2026-07-11 (10 days):
	// Unit A: 10 active nights => 10 physical, 10 sellable.
	// Unit B: 07-01..07-04 (not created: 0), 07-05..07-06 (active: 2 phys, 2 sellable), 07-07 (maintenance: 1 phys, 1 OOO, 0 sellable), 07-08..07-09 (inactive: 0 phys), 07-10 (archived: 0 phys).
	// Total: Physical = 13, OOO = 1, Sellable = 12, Occupied = 1!
	occResp, err := client.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/occupancy?start_date=2026-07-01&end_date=2026-07-11", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET occupancy: %v", err)
	}
	defer occResp.Body.Close()
	var occ domain.OccupancyReport
	if err := json.NewDecoder(occResp.Body).Decode(&occ); err != nil {
		t.Fatalf("decode occ: %v", err)
	}
	if occ.Summary.PhysicalUnitNights != 13 || occ.Summary.OutOfOrderNights != 1 || occ.Summary.SellableUnitNights != 12 || occ.Summary.OccupiedUnitNights != 1 {
		t.Fatalf("unexpected date-aware occupancy summary (expected phys=13 ooo=1 sell=12 occ=1): %+v", occ.Summary)
	}
	if len(occ.ByUnitType) != 1 || occ.ByUnitType[0].PhysicalUnitNights != 13 || occ.ByUnitType[0].SellableUnitNights != 12 {
		t.Fatalf("expected ByUnitType to share date-aware supply rules, got %+v", occ.ByUnitType)
	}
	if occ.Completeness.InventoryHistoryIncomplete {
		t.Fatalf("expected InventoryHistoryIncomplete=false when all units have tracked history, got %+v", occ.Completeness)
	}

	// Revenue RevPAR denominator must equal date-aware SellableUnitNights = 12!
	revResp, err := client.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/revenue?start_date=2026-07-01&end_date=2026-07-11", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET revenue: %v", err)
	}
	defer revResp.Body.Close()
	var rev domain.RevenueReport
	_ = json.NewDecoder(revResp.Body).Decode(&rev)
	if len(rev.ByCurrency) != 1 || rev.ByCurrency[0].SellableUnitNights != 12 {
		t.Fatalf("expected RevPAR SellableUnitNights denominator = 12, got %+v", rev.ByCurrency)
	}

	// Record an explicit pre-tracking inventory gap at 2026-07-05 and verify querying before 2026-07-05 surfaces incomplete history warning,
	// while querying >= 2026-07-05 shows complete coverage!
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO inventory_history_gaps (id, property_id, unit_id, reliable_from, reason)
		VALUES ($1, $2, $3, '2026-07-05T00:00:00Z', 'pre_0021_unrecorded_unit_transitions')
	`, uuid.New(), prop.ID, unitA.ID); err != nil {
		t.Fatalf("insert inventory history gap: %v", err)
	}
	occGapResp, err := client.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/occupancy?start_date=2026-07-01&end_date=2026-07-11", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET occ gap: %v", err)
	}
	defer occGapResp.Body.Close()
	var occGap domain.OccupancyReport
	_ = json.NewDecoder(occGapResp.Body).Decode(&occGap)
	if !occGap.Completeness.InventoryHistoryIncomplete || occGap.Completeness.InventoryHistoryReliableFrom == nil || *occGap.Completeness.InventoryHistoryReliableFrom != "2026-07-05" {
		t.Fatalf("expected InventoryHistoryIncomplete=true with reliable_from=2026-07-05, got %+v", occGap.Completeness)
	}
	occCleanResp, err := client.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/occupancy?start_date=2026-07-05&end_date=2026-07-11", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET occ clean: %v", err)
	}
	defer occCleanResp.Body.Close()
	var occClean domain.OccupancyReport
	_ = json.NewDecoder(occCleanResp.Body).Decode(&occClean)
	if occClean.Completeness.InventoryHistoryIncomplete {
		t.Fatalf("expected InventoryHistoryIncomplete=false when window starts on reliable_from, got %+v", occClean.Completeness)
	}
}

func TestPhase5_MultiCurrencyForecastNoDoubleCount(t *testing.T) {
	srv, migratorStore, appSvc, prop, _, _ := setupPhase4AppServer(t, "phase5-mc-fc", channels.AdapterConfig{})
	ctx := context.Background()
	client := newClient(t)
	if st, _ := apiLogin(t, client, srv.URL, "owner@tirbach.wales", "tirbach-owner-password-2026"); st != http.StatusOK {
		t.Fatalf("login failed: %d", st)
	}
	var ownerID uuid.UUID
	_ = migratorStore.Pool().QueryRow(ctx, `SELECT id FROM admin_users WHERE email = 'owner@tirbach.wales' LIMIT 1`).Scan(&ownerID)
	ownerActor := service.Actor{UserID: ownerID, PropertyID: &prop.ID, Email: "owner@tirbach.wales", Role: domain.RoleOwnerAdmin}

	units, _ := migratorStore.ListUnits(ctx, prop.ID, nil, false)
	for i := 0; i < 2; i++ {
		_, _ = migratorStore.Pool().Exec(ctx, `UPDATE units SET created_at = '2026-01-01T00:00:00Z' WHERE id = $1`, units[i].ID)
		_, _ = migratorStore.Pool().Exec(ctx, `UPDATE unit_status_history SET effective_from = '2026-01-01T00:00:00Z' WHERE unit_id = $1 AND source = 'unit_created'`, units[i].ID)
	}
	for i := 2; i < len(units); i++ {
		_, _ = migratorStore.Pool().Exec(ctx, `UPDATE units SET status = 'retired', updated_at = '2026-01-01T00:00:00Z' WHERE id = $1`, units[i].ID)
	}

	// Trailing 90d before 2026-07-11: 60 GBP nights (resGBP) + 30 EUR nights (resEUR) => 50% trailing occupancy, 2:1 GBP:EUR mix.
	rGBP, err := appSvc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID: prop.ID, GuestName: "GBP Sample", ChannelCode: "DIRECT",
		ArrivalDate: time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC), DepartureDate: time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC),
		Adults: 2, Status: domain.ResStatusConfirmed, UnitIDs: []uuid.UUID{units[0].ID},
	})
	if err != nil {
		t.Fatalf("create rGBP: %v", err)
	}
	_, _ = migratorStore.Pool().Exec(ctx, `UPDATE reservations SET total_pence = 600000 WHERE id = $1`, rGBP.ID)

	rEUR, err := appSvc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID: prop.ID, GuestName: "EUR Sample", ChannelCode: "BOOKING_COM",
		ArrivalDate: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), DepartureDate: time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC),
		Adults: 2, Status: domain.ResStatusConfirmed, UnitIDs: []uuid.UUID{units[1].ID},
	})
	if err != nil {
		t.Fatalf("create rEUR: %v", err)
	}
	_, _ = migratorStore.Pool().Exec(ctx, `UPDATE reservations SET currency = 'EUR', total_pence = 300000 WHERE id = $1`, rEUR.ID)

	// Query forecast starting 2026-07-11 for 10 days (20 sellable nights, 0 OTB -> ProjectedAdditionalNights > 0).
	fcResp, err := client.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/forecast?as_of_date=2026-07-11&horizon_days=10", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET multi-currency forecast: %v", err)
	}
	defer fcResp.Body.Close()
	var fc domain.ForecastReport
	if err := json.NewDecoder(fcResp.Body).Decode(&fc); err != nil {
		t.Fatalf("decode forecast: %v", err)
	}
	if fc.ProjectedAdditionalNights <= 0 || len(fc.ByCurrency) != 2 {
		t.Fatalf("expected positive projected nights across 2 currencies, got nights=%d len=%d", fc.ProjectedAdditionalNights, len(fc.ByCurrency))
	}

	var sumAlloc int64
	eurAlloc := int64(-1)
	gbpAlloc := int64(-1)
	for _, cp := range fc.ByCurrency {
		sumAlloc += cp.AllocatedAdditionalNights
		if cp.Currency == "EUR" {
			eurAlloc = cp.AllocatedAdditionalNights
		} else if cp.Currency == "GBP" {
			gbpAlloc = cp.AllocatedAdditionalNights
		}
		if cp.TrailingBenchmarkADRPence != nil {
			expectedIncr := cp.AllocatedAdditionalNights * (*cp.TrailingBenchmarkADRPence)
			if cp.ProjectedIncrementalPence != expectedIncr {
				t.Fatalf("currency %s projected_incremental_pence=%d != allocated(%d)*adr(%d)", cp.Currency, cp.ProjectedIncrementalPence, cp.AllocatedAdditionalNights, *cp.TrailingBenchmarkADRPence)
			}
		}
	}
	if sumAlloc != fc.ProjectedAdditionalNights {
		t.Fatalf("forecast duplicated/lost incremental nights across currencies: sum(allocated)=%d vs projected_additional_nights=%d", sumAlloc, fc.ProjectedAdditionalNights)
	}
	if gbpAlloc <= eurAlloc {
		t.Fatalf("expected GBP (60 nights) allocated strictly more nights than EUR (30 nights), got gbp=%d eur=%d", gbpAlloc, eurAlloc)
	}

	// Filtering by currency=EUR must return ONLY EUR's proportional share, NOT 100% of property nights!
	fcEURResp, err := client.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/forecast?as_of_date=2026-07-11&horizon_days=10&currency=EUR", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET filtered forecast: %v", err)
	}
	defer fcEURResp.Body.Close()
	var fcEUR domain.ForecastReport
	_ = json.NewDecoder(fcEURResp.Body).Decode(&fcEUR)
	if len(fcEUR.ByCurrency) != 1 || fcEUR.ByCurrency[0].AllocatedAdditionalNights != eurAlloc {
		t.Fatalf("filtered currency=EUR must preserve proportional share (%d), got %+v", eurAlloc, fcEUR.ByCurrency)
	}
}

func TestPhase4_DisconnectWebhookCleanupFailureDoesNotMaskOrBlockLocalWipe(t *testing.T) {
	var failDelete atomic.Bool
	var deleteCalls atomic.Int32
	smSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/me"):
			_, _ = w.Write([]byte(`{"id":999,"email":"sm@test"}`))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/users/webhooks/"):
			deleteCalls.Add(1)
			if failDelete.Load() {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer smSrv.Close()

	srv, migratorStore, _, prop, _, _ := setupPhase4AppServer(t, "phase4-disc-fix", channels.AdapterConfig{
		AllowHTTPForTests: true,
		MaxRetries:        0,
		ProviderBaseURLs:  map[string]string{"smoobu": smSrv.URL},
	})
	ctx := context.Background()
	client := newClient(t)
	st, csrf := apiLogin(t, client, srv.URL, "owner@tirbach.wales", "tirbach-owner-password-2026")
	if st != http.StatusOK {
		t.Fatalf("login failed: %d", st)
	}

	// 1. Save Smoobu credentials + webhook_id, then disconnect while webhook deletion fails with 500.
	rSave := doJSON(t, client, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/smoobu", csrf, map[string]any{
		"inputs": map[string]string{
			"api_key":        "sm_key_secret_1",
			"api_secret":     "sm_hmac_secret_2",
			"webhook_secret": "sm_wh_secret_9",
		},
	})
	rSave.Body.Close()
	if _, err := migratorStore.Pool().Exec(ctx, `
		UPDATE channel_connections
		SET status = 'CONNECTED', public_config = '{"webhook_id":"wh-fail-1"}'::jsonb
		WHERE property_id = $1 AND provider_code = 'smoobu'
	`, prop.ID); err != nil {
		t.Fatalf("set webhook_id: %v", err)
	}

	failDelete.Store(true)
	rDiscFail := doJSON(t, client, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/smoobu/disconnect", csrf, nil)
	if rDiscFail.StatusCode != http.StatusOK {
		t.Fatalf("disconnect failed unexpectedly: %d", rDiscFail.StatusCode)
	}
	var failOut service.DisconnectChannelResult
	_ = json.NewDecoder(rDiscFail.Body).Decode(&failOut)
	rDiscFail.Body.Close()
	if deleteCalls.Load() < 1 || !failOut.WebhookDeleteAttempted || failOut.WebhookDeleted || failOut.WebhookDeleteStatus != channels.CapabilityBlocked || failOut.WebhookErrorCode != "HTTP_500" || failOut.CapabilityStatus != channels.CapabilityBlocked {
		t.Fatalf("expected webhook delete failure surfaced as BLOCKED/HTTP_500, got %+v", failOut)
	}
	if failOut.TokenRevocationStatus != channels.CapabilityNotImplemented || failOut.TokenRevocationErrorCode != "NO_REMOTE_REVOKE" {
		t.Fatalf("expected separated token revocation status NOT_IMPLEMENTED, got %+v", failOut)
	}
	if failOut.Connection.HasCredentials || failOut.Connection.Status != channels.StatusDisabled {
		t.Fatalf("expected local encrypted credentials wiped and connection disabled despite remote webhook failure, got %+v", failOut.Connection)
	}
	if failOut.Connection.PublicConfig["webhook_id"] != "wh-fail-1" {
		t.Fatalf("expected failed webhook_id retained in public_config so orphaned remote webhook remains visible, got %+v", failOut.Connection.PublicConfig)
	}

	// 2. Reconfigure with valid webhook_id, disconnect when webhook delete succeeds -> webhook_id cleared, token revoke still NOT_IMPLEMENTED, repeat disconnect is idempotent.
	rSave2 := doJSON(t, client, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/smoobu", csrf, map[string]any{
		"inputs": map[string]string{
			"api_key":        "sm_key_secret_1",
			"api_secret":     "sm_hmac_secret_2",
			"webhook_secret": "sm_wh_secret_9",
		},
	})
	rSave2.Body.Close()
	_, _ = migratorStore.Pool().Exec(ctx, `
		UPDATE channel_connections
		SET status = 'CONNECTED', public_config = '{"webhook_id":"wh-ok-2"}'::jsonb
		WHERE property_id = $1 AND provider_code = 'smoobu'
	`, prop.ID)

	failDelete.Store(false)
	rDiscOK := doJSON(t, client, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/smoobu/disconnect", csrf, nil)
	var okOut service.DisconnectChannelResult
	_ = json.NewDecoder(rDiscOK.Body).Decode(&okOut)
	rDiscOK.Body.Close()
	if !okOut.WebhookDeleted || okOut.WebhookDeleteStatus != channels.CapabilitySupported || okOut.TokenRevocationStatus != channels.CapabilityNotImplemented || okOut.RemoteRevoked || okOut.Connection.HasCredentials {
		t.Fatalf("expected webhook deleted + token revoke NOT_IMPLEMENTED + local wipe, got %+v", okOut)
	}
	if _, stillSet := okOut.Connection.PublicConfig["webhook_id"]; stillSet {
		t.Fatalf("expected cleared webhook_id after successful remote delete, got %+v", okOut.Connection.PublicConfig)
	}

	// Repeated disconnect must be idempotent without calling remote delete again.
	callsBefore := deleteCalls.Load()
	rDiscRepeat := doJSON(t, client, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/smoobu/disconnect", csrf, nil)
	var repeatOut service.DisconnectChannelResult
	_ = json.NewDecoder(rDiscRepeat.Body).Decode(&repeatOut)
	rDiscRepeat.Body.Close()
	if deleteCalls.Load() != callsBefore || repeatOut.WebhookDeleteAttempted || repeatOut.WebhookDeleteStatus != channels.CapabilityNotApplicable {
		t.Fatalf("expected idempotent repeated disconnect with no webhook delete attempt, got calls=%d out=%+v", deleteCalls.Load()-callsBefore, repeatOut)
	}
}
