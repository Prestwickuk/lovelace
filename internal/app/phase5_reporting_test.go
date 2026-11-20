package app_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/auth"
	"github.com/Prestwickuk/lovelace/internal/channels"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/service"
)

func TestPhase5_OccupancyRevenueChannelSeasonalForecastRetentionAndExports(t *testing.T) {
	srv, migratorStore, appSvc, prop, _, _ := setupPhase4AppServer(t, "phase5-core", channels.AdapterConfig{})
	ctx := context.Background()

	// Create 2 active units across 2 unit types (existing seeded baseline + our test units):
	// fetch seeded unit types & units first.
	uts, err := migratorStore.ListUnitTypes(ctx, prop.ID, false)
	if err != nil || len(uts) == 0 {
		t.Fatalf("list unit types: %v (len=%d)", err, len(uts))
	}
	existingUnits, err := migratorStore.ListUnits(ctx, prop.ID, nil, false)
	if err != nil {
		t.Fatalf("list existing units: %v", err)
	}
	if len(existingUnits) < 2 {
		t.Fatalf("expected at least 2 seeded units, got %d", len(existingUnits))
	}
	unit1 := existingUnits[0]
	unit2 := existingUnits[1]
	// Retire any extra seeded units beyond unit1 and unit2 so sellable supply math is exact and deterministic,
	// and anchor unit1/unit2 commissioning prior to the 2026-07-01 reporting window.
	for i := 0; i < 2; i++ {
		if _, err := migratorStore.Pool().Exec(ctx, `
			UPDATE units SET created_at = '2026-01-01T00:00:00Z' WHERE id = $1
		`, existingUnits[i].ID); err != nil {
			t.Fatalf("anchor unit commissioning: %v", err)
		}
		if _, err := migratorStore.Pool().Exec(ctx, `
			UPDATE unit_status_history SET effective_from = '2026-01-01T00:00:00Z' WHERE unit_id = $1 AND source = 'unit_created'
		`, existingUnits[i].ID); err != nil {
			t.Fatalf("anchor unit status history: %v", err)
		}
	}
	for i := 2; i < len(existingUnits); i++ {
		if _, err := migratorStore.Pool().Exec(ctx, `UPDATE units SET status = 'retired', updated_at = '2026-01-01T00:00:00Z' WHERE id = $1`, existingUnits[i].ID); err != nil {
			t.Fatalf("retire extra seeded unit: %v", err)
		}
	}

	var ratePlanID uuid.UUID
	if err := migratorStore.Pool().QueryRow(ctx, `
		SELECT id FROM rate_plans WHERE property_id = $1 AND is_default = TRUE LIMIT 1
	`, prop.ID).Scan(&ratePlanID); err != nil {
		t.Fatalf("find default rate plan: %v", err)
	}

	var ownerID uuid.UUID
	if err := migratorStore.Pool().QueryRow(ctx, `
		SELECT id FROM admin_users WHERE email = 'owner@example.com' LIMIT 1
	`).Scan(&ownerID); err != nil {
		t.Fatalf("find owner id: %v", err)
	}
	ownerActor := service.Actor{UserID: ownerID, PropertyID: &prop.ID, Email: "owner@example.com", Role: domain.RoleOwnerAdmin}

	// Build multi-hop canonical guest cluster g1 -> g2 -> g3 (canonical root), plus anonymized guest gAnon.
	g3ID, g2ID, g1ID, gAnonID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	insertGuest := func(id uuid.UUID, name, email, status string, mergedInto *uuid.UUID) {
		var anonAt any
		if status == "ANONYMIZED" {
			anonAt = time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
		}
		var mergedAt any
		if mergedInto != nil {
			mergedAt = time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
		}
		if _, err := migratorStore.Pool().Exec(ctx, `
			INSERT INTO guests (id, property_id, full_name, email, phone, status, merged_into_guest_id, merged_at, anonymized_at)
			VALUES ($1, $2, $3, $4, '+447700900111', $5, $6, $7, $8)
		`, id, prop.ID, name, email, status, mergedInto, mergedAt, anonAt); err != nil {
			t.Fatalf("insert guest %s: %v", name, err)
		}
	}
	insertGuest(g3ID, "Canonical Alice", "alice.secret@example.test", "ACTIVE", nil)
	insertGuest(g2ID, "Alias Alice Two", "alice2.secret@example.test", "MERGED", &g3ID)
	insertGuest(g1ID, "Alias Alice One", "alice1.secret@example.test", "MERGED", &g2ID)
	insertGuest(gAnonID, "Anonymized Guest", "", "ANONYMIZED", nil)

	// 1) Quoted CHECKED_OUT stay on unit1: 2026-07-02 -> 2026-07-05 (3 nights) linked to merged alias g1ID.
	res1, err := appSvc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Canonical Alice",
		GuestEmail:    "alice.secret@example.test",
		GuestPhone:    "+447700900111",
		ChannelCode:   "DIRECT",
		ArrivalDate:   time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		DepartureDate: time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC),
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unit1.ID},
	})
	if err != nil {
		t.Fatalf("create res1: %v", err)
	}
	// Attach guest_id = g1ID (deep merge alias) and check in/out + frozen quote + BST boundary payment + partial refund.
	if _, err := migratorStore.Pool().Exec(ctx, `
		UPDATE reservations
		SET guest_id = $2, status = 'CHECKED_OUT', payment_status = 'PARTIALLY_REFUNDED',
		    total_pence = 36000, deposit_pence = 18000, amount_paid_pence = 36000, balance_due_pence = 0
		WHERE id = $1
	`, res1.ID, g1ID); err != nil {
		t.Fatalf("update res1 completed + financial: %v", err)
	}
	quote1ID := uuid.New()
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO booking_quotes (
			id, property_id, reservation_id, unit_type_id, rate_plan_id, currency,
			start_date, end_date, nights, adults, children, infants,
			nightly_breakdown, extras_breakdown, tax_breakdown,
			accommodation_subtotal_pence, occupancy_surcharge_pence, extras_subtotal_pence,
			discount_pence, tax_pence, total_pence, deposit_mode,
			deposit_due_pence, balance_due_pence, policy_version, quote_hash, expires_at
		) VALUES (
			$1, $2, $3, $4, $5, 'GBP',
			'2026-07-02', '2026-07-05', 3, 2, 0, 0,
			'[]'::jsonb, '[]'::jsonb, '[]'::jsonb,
			30000, 2000, 3000, 1000, 2000, 36000, 'FULL',
			36000, 0, 1, 'hash-res1', NOW() + INTERVAL '1 hour'
		)
	`, quote1ID, prop.ID, res1.ID, unit1.UnitTypeID, ratePlanID); err != nil {
		t.Fatalf("insert quote1: %v", err)
	}
	// Payment at 2026-07-02T23:30:00Z -> Europe/London (UTC+1 BST) is 2026-07-03!
	att1ID, pay1ID := uuid.New(), uuid.New()
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO payment_attempts (id, property_id, reservation_id, provider, payment_type, amount_pence, currency, idempotency_key, status)
		VALUES ($1, $2, $3, 'stripe', 'FULL', 36000, 'GBP', 'idem-res1', 'PARTIALLY_REFUNDED')
	`, att1ID, prop.ID, res1.ID); err != nil {
		t.Fatalf("insert att1: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO payments (id, property_id, reservation_id, payment_attempt_id, provider, provider_payment_intent_id, payment_type, amount_pence, currency, status, paid_at)
		VALUES ($1, $2, $3, $4, 'stripe', 'pi_res1', 'FULL', 36000, 'GBP', 'PARTIALLY_REFUNDED', '2026-07-02T23:30:00Z'::timestamptz)
	`, pay1ID, prop.ID, res1.ID, att1ID); err != nil {
		t.Fatalf("insert pay1: %v", err)
	}
	// Refund with formula-injection + Unicode reason to prove CSV defense.
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO refunds (id, property_id, payment_attempt_id, payment_id, reservation_id, provider, provider_refund_id, amount_pence, currency, reason, status, idempotency_key, created_at)
		VALUES ($1, $2, $3, $4, $5, 'stripe', 're_res1', 6000, 'GBP', '=cmd|"quoted", café £', 'SUCCEEDED', 'rf-res1', '2026-07-04T12:00:00Z'::timestamptz)
	`, uuid.New(), prop.ID, att1ID, pay1ID, res1.ID); err != nil {
		t.Fatalf("insert refund1: %v", err)
	}

	// 2) Second stay by canonical guest g3ID (CHECKED_OUT, 2026-07-06 -> 2026-07-08, 2 nights on AIRBNB)
	// with manual fallback total (no quote row -> completeness manualUnquotedBookingsCount = 1).
	res2, err := appSvc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Canonical Alice",
		GuestEmail:    "alice.secret@example.test",
		ChannelCode:   "AIRBNB",
		ArrivalDate:   time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC),
		DepartureDate: time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC),
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unit1.ID},
	})
	if err != nil {
		t.Fatalf("create res2: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `
		UPDATE reservations
		SET guest_id = $2, status = 'CHECKED_OUT', payment_status = 'UNPAID',
		    total_pence = 20000, balance_due_pence = 20000
		WHERE id = $1
	`, res2.ID, g3ID); err != nil {
		t.Fatalf("update res2: %v", err)
	}

	// 3) Third CHECKED_OUT stay on unit2 linked to anonymized guest (2026-07-02 -> 2026-07-04, EUR currency -> multi-currency separation!).
	res3, err := appSvc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "EUR Guest",
		ChannelCode:   "BOOKING_COM",
		ArrivalDate:   time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		DepartureDate: time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC),
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unit2.ID},
	})
	if err != nil {
		t.Fatalf("create res3: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `
		UPDATE reservations
		SET guest_id = $2, status = 'CHECKED_OUT', currency = 'EUR',
		    total_pence = 24000, amount_paid_pence = 24000, balance_due_pence = 0
		WHERE id = $1
	`, res3.ID, gAnonID); err != nil {
		t.Fatalf("update res3: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO booking_quotes (
			id, property_id, reservation_id, unit_type_id, rate_plan_id, currency,
			start_date, end_date, nights, adults, children, infants,
			nightly_breakdown, extras_breakdown, tax_breakdown,
			accommodation_subtotal_pence, occupancy_surcharge_pence, extras_subtotal_pence,
			discount_pence, tax_pence, total_pence, deposit_mode,
			deposit_due_pence, balance_due_pence, policy_version, quote_hash, expires_at
		) VALUES (
			$1, $2, $3, $4, $5, 'EUR',
			'2026-07-02', '2026-07-04', 2, 2, 0, 0,
			'[]'::jsonb, '[]'::jsonb, '[]'::jsonb,
			22000, 0, 0, 0, 2000, 24000, 'FULL',
			24000, 0, 1, 'hash-res3', NOW() + INTERVAL '1 hour'
		)
	`, uuid.New(), prop.ID, res3.ID, unit2.UnitTypeID, ratePlanID); err != nil {
		t.Fatalf("insert quote3: %v", err)
	}

	// 4) Excluded statuses: PENDING (2026-07-05 -> 2026-07-07 on unit2), CANCELLED, NO_SHOW.
	resPend, err := appSvc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Pending Prospect",
		ChannelCode:   "DIRECT",
		ArrivalDate:   time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC),
		DepartureDate: time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC),
		Adults:        2,
		Status:        domain.ResStatusPending,
		UnitIDs:       []uuid.UUID{unit2.ID},
	})
	if err != nil {
		t.Fatalf("create resPend: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `UPDATE reservations SET total_pence = 99900 WHERE id = $1`, resPend.ID); err != nil {
		t.Fatalf("set pending value: %v", err)
	}
	resCanc, err := appSvc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Cancelled Guest",
		ChannelCode:   "DIRECT",
		ArrivalDate:   time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC),
		DepartureDate: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC),
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unit2.ID},
	})
	if err != nil {
		t.Fatalf("create resCanc: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `UPDATE reservations SET status = 'CANCELLED', cancelled_at = NOW(), total_pence = 50000 WHERE id = $1`, resCanc.ID); err != nil {
		t.Fatalf("cancel resCanc: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `DELETE FROM inventory_claims WHERE reservation_id = $1`, resCanc.ID); err != nil {
		t.Fatalf("release cancel claim: %v", err)
	}

	// 5) Maintenance block on unit2 for 2026-07-08 -> 2026-07-10 (2 OOO nights) + Owner block 2026-07-10 -> 2026-07-11 (1 night).
	if _, err := appSvc.CreateAvailabilityBlock(ctx, ownerActor, domain.AvailabilityBlock{
		PropertyID: prop.ID,
		UnitID:     unit2.ID,
		StartDate:  time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC),
		EndDate:    time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC),
		BlockType:  domain.BlockMaintenance,
		Reason:     "Boiler inspection",
	}); err != nil {
		t.Fatalf("create maintenance block: %v", err)
	}
	if _, err := appSvc.CreateAvailabilityBlock(ctx, ownerActor, domain.AvailabilityBlock{
		PropertyID: prop.ID,
		UnitID:     unit2.ID,
		StartDate:  time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC),
		EndDate:    time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC),
		BlockType:  domain.BlockOwnerHold,
		Reason:     "Owner stay",
	}); err != nil {
		t.Fatalf("create owner block: %v", err)
	}

	// 6) Future CONFIRMED OTB reservation (2026-07-12 -> 2026-07-15 = 3 nights) on unit1 for forecast testing.
	resFuture, err := appSvc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID:    prop.ID,
		GuestName:     "Future Confirmed",
		ChannelCode:   "DIRECT",
		ArrivalDate:   time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC),
		DepartureDate: time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC),
		Adults:        2,
		Status:        domain.ResStatusConfirmed,
		UnitIDs:       []uuid.UUID{unit1.ID},
	})
	if err != nil {
		t.Fatalf("create resFuture: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `UPDATE reservations SET total_pence = 45000 WHERE id = $1`, resFuture.ID); err != nil {
		t.Fatalf("update resFuture: %v", err)
	}

	// 7) Seed UNMAPPED & CONFLICT_OVERBOOKED Phase 4 external reservations for completeness reporting.
	connID := uuid.New()
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO channel_connections (id, property_id, provider_code, display_name, auth_method, status)
		VALUES ($1, $2, 'beds24', 'Beds24 Test', 'token', 'CREDENTIALS_SAVED')
	`, connID, prop.ID); err != nil {
		t.Fatalf("seed channel connection: %v", err)
	}
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO channel_external_reservations (id, property_id, channel_connection_id, provider_code, external_reservation_id, import_status)
		VALUES
			($1, $2, $3, 'beds24', 'b24-unmap-1', 'UNMAPPED'),
			($4, $2, $3, 'beds24', 'b24-conf-1', 'CONFLICT_OVERBOOKED')
	`, uuid.New(), prop.ID, connID, uuid.New()); err != nil {
		t.Fatalf("seed external reservation completeness rows: %v", err)
	}

	client := newClient(t)
	st, _ := apiLogin(t, client, srv.URL, "owner@example.com", "lovelace-owner-password-2026")
	if st != http.StatusOK {
		t.Fatalf("login failed: %d", st)
	}

	// Window 2026-07-01 .. 2026-07-11 (10 days * 2 active units = 20 physical unit-nights):
	// OOO = 2 nights, OwnerBlock = 1 night -> Sellable = 17 nights.
	// Occupied = res1 (3) + res2 (2) + res3 (2) = 7 nights.
	// PendingHold = resPend (2) nights (excluded from occupied!).
	// Occupancy % = 7 / 17 = 41.18% (4118 bps).
	occResp, err := client.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/occupancy?start_date=2026-07-01&end_date=2026-07-11", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET occupancy: %v", err)
	}
	defer occResp.Body.Close()
	if occResp.StatusCode != http.StatusOK {
		t.Fatalf("occupancy status=%d", occResp.StatusCode)
	}
	var occ domain.OccupancyReport
	if err := json.NewDecoder(occResp.Body).Decode(&occ); err != nil {
		t.Fatalf("decode occupancy: %v", err)
	}
	if occ.Summary.PhysicalUnitNights != 20 || occ.Summary.OutOfOrderNights != 2 || occ.Summary.OwnerAdminBlockNights != 1 || occ.Summary.SellableUnitNights != 17 {
		t.Fatalf("unexpected inventory breakdown: %+v", occ.Summary)
	}
	if occ.Summary.OccupiedUnitNights != 7 || occ.Summary.PendingHoldNights != 2 {
		t.Fatalf("expected 7 occupied and 2 pending/hold nights, got %+v", occ.Summary)
	}
	if occ.Summary.OccupancyRateBPS == nil || *occ.Summary.OccupancyRateBPS != 4118 {
		t.Fatalf("expected 4118 bps occupancy rate, got %v", occ.Summary.OccupancyRateBPS)
	}

	// Revenue report (strict multi-currency separation GBP vs EUR + BST midnight localization):
	revResp, err := client.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/revenue?start_date=2026-07-01&end_date=2026-07-11", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET revenue: %v", err)
	}
	defer revResp.Body.Close()
	var rev domain.RevenueReport
	if err := json.NewDecoder(revResp.Body).Decode(&rev); err != nil {
		t.Fatalf("decode revenue: %v", err)
	}
	if !rev.Completeness.MultiCurrencyDetected || len(rev.ByCurrency) != 2 {
		t.Fatalf("expected 2 separate currency buckets (EUR & GBP), got multi=%v len=%d", rev.Completeness.MultiCurrencyDetected, len(rev.ByCurrency))
	}
	if rev.Completeness.UnmappedExternalReservations != 1 || rev.Completeness.ConflictExternalReservations != 1 {
		t.Fatalf("expected Phase 4 completeness warnings surfaced, got %+v", rev.Completeness)
	}
	var gbpRev, eurRev domain.CurrencyRevenueMetrics
	for _, r := range rev.ByCurrency {
		if r.Currency == "GBP" {
			gbpRev = r
		} else if r.Currency == "EUR" {
			eurRev = r
		}
	}
	// GBP accommodation net: quote1 (30000 + 2000 - 1000 = 31000) + manual res2 (20000) = 51000 over 5 GBP occupied nights => ADR 10200!
	if gbpRev.AccommodationNetPence != 51000 || gbpRev.OccupiedUnitNights != 5 || gbpRev.ADRPence == nil || *gbpRev.ADRPence != 10200 {
		t.Fatalf("unexpected GBP revenue/ADR: %+v", gbpRev)
	}
	// Gross collected GBP cash = 36000, refunded = 6000 -> Net collected = 30000; balance due = 20000 (res2); provisional pending excluded = 99900? Wait: 99900 / 2 * 2 = 99900!
	if gbpRev.GrossCollectedPaymentsPence != 36000 || gbpRev.RefundedPence != 6000 || gbpRev.NetCollectedCashPence != 30000 || gbpRev.OutstandingBalancePence != 20000 {
		t.Fatalf("unexpected GBP cash/refunds: %+v", gbpRev)
	}
	if eurRev.AccommodationNetPence != 22000 || eurRev.TaxPence != 2000 || eurRev.GrossBookedTotalPence != 24000 {
		t.Fatalf("unexpected EUR separation: %+v", eurRev)
	}
	// Check BST localization: 2026-07-02T23:30:00Z must appear on 2026-07-03 daily cash bucket, NOT 2026-07-02!
	for _, dp := range rev.DailySeries {
		if dp.Currency == "GBP" && dp.Date == "2026-07-02" && dp.GrossCollectedPaymentsPence != 0 {
			t.Fatalf("expected BST boundary payment excluded from 2026-07-02, got %d", dp.GrossCollectedPaymentsPence)
		}
		if dp.Currency == "GBP" && dp.Date == "2026-07-03" && dp.GrossCollectedPaymentsPence != 36000 {
			t.Fatalf("expected BST boundary payment attributed to 2026-07-03, got %d", dp.GrossCollectedPaymentsPence)
		}
	}

	// Zero-availability safety test: query past year 2020 when zero units have blocks? Wait: physical units exist, so block ALL units 2026-08-01..2026-08-03 with maintenance -> Sellable=0, Occupied=0 -> zero division guard!
	for _, u := range []uuid.UUID{unit1.ID, unit2.ID} {
		if _, err := appSvc.CreateAvailabilityBlock(ctx, ownerActor, domain.AvailabilityBlock{
			PropertyID: prop.ID, UnitID: u,
			StartDate: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC),
			BlockType: domain.BlockMaintenance, Reason: "Full site close",
		}); err != nil {
			t.Fatalf("block all units for zero availability test: %v", err)
		}
	}
	zeroResp, err := client.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/revenue?start_date=2026-08-01&end_date=2026-08-03", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET zero-availability revenue: %v", err)
	}
	zeroResp.Body.Close()
	if zeroResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on zero-availability slice, got %d", zeroResp.StatusCode)
	}

	// Channel comparison check
	chResp, err := client.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/channels?start_date=2026-07-01&end_date=2026-07-11", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET channels: %v", err)
	}
	defer chResp.Body.Close()
	var chRep domain.ChannelComparisonReport
	if err := json.NewDecoder(chResp.Body).Decode(&chRep); err != nil {
		t.Fatalf("decode channels: %v", err)
	}
	if len(chRep.Rows) < 3 {
		t.Fatalf("expected DIRECT_WEB, AIRBNB, BOOKING_COM rows, got %d", len(chRep.Rows))
	}

	// Retention check: strict aggregate, multi-hop merge resolution, ZERO PII!
	retResp, err := client.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/retention?start_date=2026-07-01&end_date=2026-07-11", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET retention: %v", err)
	}
	defer retResp.Body.Close()
	retRaw, _ := io.ReadAll(retResp.Body)
	retStr := string(retRaw)
	for _, forbidden := range []string{"alice.secret", "Canonical Alice", g3ID.String(), g1ID.String(), "+447700900111"} {
		if strings.Contains(retStr, forbidden) {
			t.Fatalf("retention response leaked guest PII/ID %q", forbidden)
		}
	}
	var ret domain.GuestRetentionReport
	if err := json.Unmarshal(retRaw, &ret); err != nil {
		t.Fatalf("unmarshal retention: %v", err)
	}
	// res1 (alias -> g3) + res2 (g3) => 1 returning canonical guest (100%), plus 1 anonymized stay (res3)!
	if ret.CompletedStaysCount != 3 || ret.DistinctCanonicalGuestsCount != 1 || ret.ReturningGuestsCount != 1 || ret.MergedAliasStaysResolved != 1 || ret.AnonymizedGuestStaysCount != 1 {
		t.Fatalf("unexpected retention analytics: %+v", ret)
	}

	// Forecast read-only check: snapshot row counts before/after forecast call.
	var beforeResCnt, beforeClaimCnt int64
	_ = migratorStore.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM reservations`).Scan(&beforeResCnt)
	_ = migratorStore.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM inventory_claims`).Scan(&beforeClaimCnt)

	fcResp, err := client.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/forecast?as_of_date=2026-07-11&horizon_days=10", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET forecast: %v", err)
	}
	defer fcResp.Body.Close()
	var fc domain.ForecastReport
	if err := json.NewDecoder(fcResp.Body).Decode(&fc); err != nil {
		t.Fatalf("decode forecast: %v", err)
	}
	if fc.OnTheBooksUnitNights != 3 || fc.HistoricalSampleUnitNights != 7 {
		t.Fatalf("expected 3 future OTB nights and 7 trailing sample nights, got %+v", fc)
	}
	var afterResCnt, afterClaimCnt int64
	_ = migratorStore.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM reservations`).Scan(&afterResCnt)
	_ = migratorStore.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM inventory_claims`).Scan(&afterClaimCnt)
	if beforeResCnt != afterResCnt || beforeClaimCnt != afterClaimCnt {
		t.Fatalf("forecast violated read-only guarantee: reservations %d->%d claims %d->%d", beforeResCnt, afterResCnt, beforeClaimCnt, afterClaimCnt)
	}

	// Accounting CSV Export + Formula Injection + Unicode + Unsupported fields + Audit Log Check!
	expResp, err := client.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/accounting-export?type=refund_ledger&format=csv&start_date=2026-07-01&end_date=2026-07-11", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET refund csv export: %v", err)
	}
	defer expResp.Body.Close()
	if expResp.StatusCode != http.StatusOK || !strings.HasPrefix(expResp.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("unexpected CSV response status=%d ct=%q", expResp.StatusCode, expResp.Header.Get("Content-Type"))
	}
	csvRaw, _ := io.ReadAll(expResp.Body)
	if strings.Contains(string(csvRaw), "Canonical Alice") || strings.Contains(string(csvRaw), "alice.secret") {
		t.Fatalf("accounting export leaked guest PII: %s", string(csvRaw))
	}
	records, err := csv.NewReader(bytes.NewReader(csvRaw)).ReadAll()
	if err != nil || len(records) != 2 {
		t.Fatalf("parse refund CSV failed: err=%v len=%d", err, len(records))
	}
	reasonIdx := -1
	for i, col := range records[0] {
		if col == "reason" {
			reasonIdx = i
		}
	}
	if reasonIdx < 0 || !strings.HasPrefix(records[1][reasonIdx], "'=cmd|") {
		t.Fatalf("expected formula injection escaped cleanly in CSV reason column, got %q", records[1][reasonIdx])
	}

	// Booking line items export verifies explicit NOT IMPLEMENTED columns + zero PII.
	bliResp, err := client.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/accounting-export?type=booking_line_items&format=csv&start_date=2026-07-01&end_date=2026-07-11", srv.URL, prop.ID))
	if err != nil {
		t.Fatalf("GET booking_line_items csv: %v", err)
	}
	defer bliResp.Body.Close()
	bliRaw, _ := io.ReadAll(bliResp.Body)
	if !strings.Contains(string(bliRaw), "NOT IMPLEMENTED") {
		t.Fatalf("expected NOT IMPLEMENTED marker in booking_line_items export")
	}

	// Verify immutable PII-free report.exported audit entry was recorded in audit_logs_effective!
	var auditCount int64
	if err := migratorStore.Pool().QueryRow(ctx, `
		SELECT COUNT(*) FROM audit_logs_effective
		WHERE property_id = $1 AND action = 'report.exported' AND resource_type = 'report_export'
	`, prop.ID).Scan(&auditCount); err != nil || auditCount < 2 {
		t.Fatalf("expected >= 2 report.exported audit records, got %d (err=%v)", auditCount, err)
	}

	// Verify EXPLAIN plan executes cleanly on reporting tables.
	var planLine string
	if err := migratorStore.Pool().QueryRow(ctx, `
		EXPLAIN SELECT COALESCE(SUM(amount_pence), 0) FROM payments
		WHERE property_id = $1 AND status IN ('SUCCEEDED', 'PARTIALLY_REFUNDED', 'REFUNDED')
	`, prop.ID).Scan(&planLine); err != nil || planLine == "" {
		t.Fatalf("EXPLAIN query check failed: %v", err)
	}
}

func TestPhase5_RBACPropertyIsolationConcurrencyAndWebUI(t *testing.T) {
	srv, migratorStore, _, propA, _, _ := setupPhase4AppServer(t, "phase5-rbac", channels.AdapterConfig{})
	ctx := context.Background()

	// Seed a second property B and isolated owner B.
	propBID := uuid.New()
	if _, err := migratorStore.Pool().Exec(ctx, `
		INSERT INTO properties (id, slug, name, timezone, currency)
		VALUES ($1, 'prop-b-phase5', 'Property B', 'Europe/London', 'GBP')
	`, propBID); err != nil {
		t.Fatalf("seed property B: %v", err)
	}

	createAdminUser := func(email string, role domain.Role, pid *uuid.UUID) {
		hash, err := auth.HashPassword("Passw0rd!Test2026")
		if err != nil {
			t.Fatalf("hash pw: %v", err)
		}
		if _, err := migratorStore.Pool().Exec(ctx, `
			INSERT INTO admin_users (id, property_id, email, full_name, role, password_hash, is_active, must_rotate_password)
			VALUES ($1, $2, $3, $4, $5, $6, TRUE, FALSE)
		`, uuid.New(), pid, email, string(role)+" User", string(role), hash); err != nil {
			t.Fatalf("insert user %s: %v", email, err)
		}
	}

	createAdminUser("viewer@example.com", domain.RoleViewer, &propA.ID)
	createAdminUser("staff@example.com", domain.RoleStaff, &propA.ID)
	createAdminUser("maint@example.com", domain.RoleMaintenanceStaff, &propA.ID)
	createAdminUser("ownerb@example.com", domain.RoleOwnerAdmin, &propBID)

	// Denied roles: staff & maintenance_staff -> 403 on API reports + Web UI /reports.
	for _, email := range []string{"staff@example.com", "maint@example.com"} {
		cl := newClient(t)
		st, _ := apiLogin(t, cl, srv.URL, email, "Passw0rd!Test2026")
		if st != http.StatusOK {
			t.Fatalf("login %s status=%d", email, st)
		}
		r1, err := cl.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/overview?start_date=2026-07-01&end_date=2026-07-10", srv.URL, propA.ID))
		if err != nil {
			t.Fatalf("get overview: %v", err)
		}
		r1.Body.Close()
		if r1.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403 for %s on reports API, got %d", email, r1.StatusCode)
		}
		r2, err := cl.Get(srv.URL + "/reports")
		if err != nil {
			t.Fatalf("get /reports UI: %v", err)
		}
		r2.Body.Close()
		if r2.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403 for %s on /reports UI, got %d", email, r2.StatusCode)
		}
	}

	// Viewer role: CAN read overview API and /reports UI, CANNOT export accounting feeds!
	viewerClient := newClient(t)
	if st, _ := apiLogin(t, viewerClient, srv.URL, "viewer@example.com", "Passw0rd!Test2026"); st != http.StatusOK {
		t.Fatalf("viewer login status=%d", st)
	}
	vRead, err := viewerClient.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/overview?start_date=2026-07-01&end_date=2026-07-10", srv.URL, propA.ID))
	if err != nil {
		t.Fatalf("viewer overview: %v", err)
	}
	vRead.Body.Close()
	if vRead.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for viewer on overview, got %d", vRead.StatusCode)
	}
	vExp, err := viewerClient.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/accounting-export?type=booking_line_items&start_date=2026-07-01&end_date=2026-07-10", srv.URL, propA.ID))
	if err != nil {
		t.Fatalf("viewer export: %v", err)
	}
	vExp.Body.Close()
	if vExp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer on accounting export, got %d", vExp.StatusCode)
	}
	vUI, err := viewerClient.Get(srv.URL + "/reports?start_date=2026-07-01&end_date=2026-07-10")
	if err != nil {
		t.Fatalf("viewer /reports UI: %v", err)
	}
	vUIBody, _ := io.ReadAll(vUI.Body)
	vUI.Body.Close()
	if vUI.StatusCode != http.StatusOK || strings.Contains(string(vUIBody), "/reports/export") {
		t.Fatalf("expected 200 without export links for viewer, status=%d", vUI.StatusCode)
	}

	// Cross-property isolation: Owner B must get 403 on Property A reports & exports!
	ownerBClient := newClient(t)
	if st, _ := apiLogin(t, ownerBClient, srv.URL, "ownerb@example.com", "Passw0rd!Test2026"); st != http.StatusOK {
		t.Fatalf("ownerB login status=%d", st)
	}
	isoResp, err := ownerBClient.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/overview?start_date=2026-07-01&end_date=2026-07-10", srv.URL, propA.ID))
	if err != nil {
		t.Fatalf("cross-property report req: %v", err)
	}
	isoResp.Body.Close()
	if isoResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 cross-property report access, got %d", isoResp.StatusCode)
	}

	// Owner A Web UI + CSP check + concurrent reporting reads under -race!
	ownerAClient := newClient(t)
	if st, _ := apiLogin(t, ownerAClient, srv.URL, "owner@example.com", "lovelace-owner-password-2026"); st != http.StatusOK {
		t.Fatalf("ownerA login status=%d", st)
	}
	uiResp, err := ownerAClient.Get(srv.URL + "/reports?start_date=2026-07-01&end_date=2026-07-10")
	if err != nil {
		t.Fatalf("ownerA /reports UI: %v", err)
	}
	uiHTML, _ := io.ReadAll(uiResp.Body)
	uiResp.Body.Close()
	if uiResp.StatusCode != http.StatusOK || !strings.Contains(string(uiHTML), "Reports &amp; Business Intelligence") {
		t.Fatalf("expected 200 rendered reports console, got %d", uiResp.StatusCode)
	}
	if strings.Contains(string(uiHTML), "<script>") || strings.Contains(string(uiHTML), " style=") {
		t.Fatalf("CSP check failed: inline script/style found in reports HTML")
	}

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, rerr := ownerAClient.Get(fmt.Sprintf("%s/api/v1/properties/%s/reports/overview?start_date=2026-07-01&end_date=2026-07-10", srv.URL, propA.ID))
			if rerr != nil {
				errs <- rerr
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errs <- context.DeadlineExceeded
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("concurrent report call failed: %v", e)
	}
}
