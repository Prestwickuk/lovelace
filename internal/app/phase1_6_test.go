package app_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/channels"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
)

func TestPhase1_6DatabasePrivilegeIsolationAndReadOnlyVerify(t *testing.T) {
	_, store, _ := setupTestServer(t)
	ctx := context.Background()

	priv, err := store.InspectRolePrivileges(ctx)
	if err != nil {
		t.Fatalf("InspectRolePrivileges: %v", err)
	}
	if priv.IsSuperuser || priv.CanCreateDB || priv.CanCreateRole {
		t.Fatalf("expected non-superuser test role without CREATEDB/CREATEROLE, got %+v", priv)
	}

	// Re-running Migrate succeeds cleanly on read-only schema_migrations checksum verification.
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("expected Migrate verification pass when up to date, got %v", err)
	}

	// Verify role cannot escalate privileges to create roles.
	if _, err := store.Pool().Exec(ctx, `CREATE ROLE illegal_escalation`); err == nil {
		t.Fatal("expected insufficient privilege error creating role, succeeded unexpectedly")
	}
}

func TestPhase1_6InventoryLifecycleDateEditsReconciliationAndSmoobu(t *testing.T) {
	srv, store, prop := setupTestServer(t)
	ownerClient := newClient(t)
	code, csrf := apiLogin(t, ownerClient, srv.URL, "owner@tirbach.wales", "tirbach-owner-password-2026")
	if code != http.StatusOK {
		t.Fatalf("owner login failed: %d", code)
	}

	utPod := mustCreateUnitTypeCustom(t, ownerClient, srv.URL, prop.ID, csrf, "pod-p16", "Timber Pod P16", "cabin", 2, 1, 1, 4, 12000)
	uPodA := mustCreateUnit(t, ownerClient, srv.URL, prop.ID, csrf, utPod.ID, "P16-A", "Pod A")
	uPodB := mustCreateUnit(t, ownerClient, srv.URL, prop.ID, csrf, utPod.ID, "P16-B", "Pod B")
	uPodC := mustCreateUnit(t, ownerClient, srv.URL, prop.ID, csrf, utPod.ID, "P16-C", "Pod C")

	createAlloc := func(unitIDs []uuid.UUID, start, end, channel, reason string) (domain.BlockoutGroup, []domain.InventoryClaim) {
		t.Helper()
		resp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts", csrf, map[string]any{
			"scope_type": "UNITS", "unit_ids": unitIDs, "category": "CHANNEL_ALLOCATION",
			"channel_scope": channel, "start_date": start, "end_date": end, "reason": reason,
		})
		if resp.StatusCode != http.StatusCreated {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("create alloc %s..%s failed (%d): %s", start, end, resp.StatusCode, string(b))
		}
		var out struct {
			Group  domain.BlockoutGroup    `json:"group"`
			Claims []domain.InventoryClaim `json:"claims"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		return out.Group, out.Claims
	}

	updateDates := func(groupID uuid.UUID, start, end string, version int) (*http.Response, domain.BlockoutGroup) {
		t.Helper()
		resp := doJSON(t, ownerClient, http.MethodPatch, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts/"+groupID.String()+"/dates", csrf, map[string]any{
			"start_date": start, "end_date": end, "version": version,
		})
		if resp.StatusCode != http.StatusOK {
			return resp, domain.BlockoutGroup{}
		}
		var out struct {
			Group domain.BlockoutGroup `json:"group"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		return nil, out.Group
	}

	book := func(guest, channel, start, end string, unitIDs []uuid.UUID) domain.Reservation {
		t.Helper()
		resp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
			"guest_name": guest, "channel_code": channel,
			"arrival_date": start, "departure_date": end,
			"adults": 2, "status": "CONFIRMED", "unit_ids": unitIDs,
		})
		if resp.StatusCode != http.StatusCreated {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("create booking %s (%s %s..%s) failed (%d): %s", guest, channel, start, end, resp.StatusCode, string(b))
		}
		var out domain.Reservation
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		return out
	}

	activeSegmentsForUnit := func(groupID, unitID uuid.UUID) []string {
		t.Helper()
		claims, err := store.ListClaimsByBlockoutGroup(context.Background(), nil, prop.ID, groupID)
		if err != nil {
			t.Fatalf("ListClaimsByBlockoutGroup: %v", err)
		}
		var segs []string
		for _, c := range claims {
			if c.UnitID == unitID && c.Status == domain.ClaimStatusActive {
				segs = append(segs, domain.FormatDate(c.StartDate)+".."+domain.FormatDate(c.EndDate))
			}
		}
		sort.Strings(segs)
		return segs
	}

	assertEqualSegs := func(got, want []string, label string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: expected segments %v, got %v", label, want, got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: expected segments %v, got %v", label, want, got)
			}
		}
	}

	// =========================================================================
	// SECTION 2 & 3: SAFE UpdateBlockoutGroupDates & LIFECYCLE STATES
	// =========================================================================
	gEdit, _ := createAlloc([]uuid.UUID{uPodA.ID, uPodB.ID}, "2026-06-10", "2026-06-20", "AIRBNB", "June Airbnb Block")
	if gEdit.Status != domain.BlockStatusActive || gEdit.ReleasedAt != nil || gEdit.ReleasedBy != nil {
		t.Fatalf("expected fresh allocation ACTIVE without release timestamp, got %+v", gEdit)
	}

	// 2.1 Extending untouched group
	errResp, gEdit := updateDates(gEdit.ID, "2026-06-08", "2026-06-22", gEdit.Version)
	if errResp != nil {
		b, _ := io.ReadAll(errResp.Body)
		errResp.Body.Close()
		t.Fatalf("extend untouched failed: %d %s", errResp.StatusCode, string(b))
	}
	assertEqualSegs(activeSegmentsForUnit(gEdit.ID, uPodA.ID), []string{"2026-06-08..2026-06-22"}, "extend untouched A")

	// 2.2 Shortening untouched group back to [06-10..06-20)
	errResp, gEdit = updateDates(gEdit.ID, "2026-06-10", "2026-06-20", gEdit.Version)
	if errResp != nil {
		t.Fatalf("shorten untouched failed: %d", errResp.StatusCode)
	}
	assertEqualSegs(activeSegmentsForUnit(gEdit.ID, uPodA.ID), []string{"2026-06-10..2026-06-20"}, "shorten untouched A")

	// 2.3 Consume single range [06-12..06-14) on P16-A -> status PARTIALLY_CONSUMED, released_at/by MUST be nil!
	res1 := book("Airbnb Guest 1", "AIRBNB", "2026-06-12", "2026-06-14", []uuid.UUID{uPodA.ID})
	_ = res1
	gAfterR1, err := store.GetBlockoutGroup(context.Background(), nil, prop.ID, gEdit.ID)
	if err != nil {
		t.Fatalf("GetBlockoutGroup: %v", err)
	}
	if gAfterR1.Status != domain.BlockStatusPartiallyConsumed {
		t.Fatalf("expected PARTIALLY_CONSUMED, got %s", gAfterR1.Status)
	}
	if gAfterR1.ReleasedAt != nil || gAfterR1.ReleasedBy != nil {
		t.Fatalf("consumed allocation must NOT set released_at/released_by, got released_at=%v released_by=%v", gAfterR1.ReleasedAt, gAfterR1.ReleasedBy)
	}
	assertEqualSegs(activeSegmentsForUnit(gEdit.ID, uPodA.ID), []string{"2026-06-10..2026-06-12", "2026-06-14..2026-06-20"}, "post-R1 splits")

	// Verify consumed claim in DB does NOT carry released_at/by either
	claimsR1, _ := store.ListClaimsByBlockoutGroup(context.Background(), nil, prop.ID, gEdit.ID)
	for _, c := range claimsR1 {
		if c.Status == domain.ClaimStatusConsumed && (c.ReleasedAt != nil || c.ReleasedBy != nil) {
			t.Fatalf("CONSUMED claim mistakenly recorded released_at=%v released_by=%v", c.ReleasedAt, c.ReleasedBy)
		}
	}

	// Extend partially consumed group [06-08..06-22) -> recalculates residuals to [06-08..06-12) and [06-14..06-22)
	errResp, gAfterExt := updateDates(gEdit.ID, "2026-06-08", "2026-06-22", gAfterR1.Version)
	if errResp != nil {
		b, _ := io.ReadAll(errResp.Body)
		errResp.Body.Close()
		t.Fatalf("extend after single consumption failed (%d): %s", errResp.StatusCode, string(b))
	}
	assertEqualSegs(activeSegmentsForUnit(gEdit.ID, uPodA.ID), []string{"2026-06-08..2026-06-12", "2026-06-14..2026-06-22"}, "extend partial A")
	assertEqualSegs(activeSegmentsForUnit(gEdit.ID, uPodB.ID), []string{"2026-06-08..2026-06-22"}, "extend partial untouched B")

	// Shorten partially consumed group [06-10..06-19) -> residuals [06-10..06-12) and [06-14..06-19)
	errResp, gAfterShort := updateDates(gEdit.ID, "2026-06-10", "2026-06-19", gAfterExt.Version)
	if errResp != nil {
		b, _ := io.ReadAll(errResp.Body)
		errResp.Body.Close()
		t.Fatalf("shorten after single consumption failed (%d): %s", errResp.StatusCode, string(b))
	}
	if gAfterShort.Version <= gAfterExt.Version {
		t.Fatalf("expected blockout_group version to increment after date edit (%d -> %d)", gAfterExt.Version, gAfterShort.Version)
	}
	assertEqualSegs(activeSegmentsForUnit(gEdit.ID, uPodA.ID), []string{"2026-06-10..2026-06-12", "2026-06-14..2026-06-19"}, "shorten partial A")

	// 2.4 Consume second range [06-15..06-17) on P16-A -> now two holes in P16-A!
	_ = book("Airbnb Guest 2", "AIRBNB", "2026-06-15", "2026-06-17", []uuid.UUID{uPodA.ID})
	gAfterR2, _ := store.GetBlockoutGroup(context.Background(), nil, prop.ID, gEdit.ID)
	assertEqualSegs(activeSegmentsForUnit(gEdit.ID, uPodA.ID), []string{"2026-06-10..2026-06-12", "2026-06-14..2026-06-15", "2026-06-17..2026-06-19"}, "post-R2 3 segments")

	// Extend multi-consumed group to [06-09..06-21)
	errResp, gMultiExt := updateDates(gEdit.ID, "2026-06-09", "2026-06-21", gAfterR2.Version)
	if errResp != nil {
		b, _ := io.ReadAll(errResp.Body)
		errResp.Body.Close()
		t.Fatalf("extend multi-consumed failed (%d): %s", errResp.StatusCode, string(b))
	}
	assertEqualSegs(activeSegmentsForUnit(gEdit.ID, uPodA.ID), []string{"2026-06-09..2026-06-12", "2026-06-14..2026-06-15", "2026-06-17..2026-06-21"}, "extend multi-consumed")

	// 2.5 Reject shortening across a consumed reservation ([06-13..06-21) cuts across R1 [06-12..06-14)) -> 422
	badShorten, _ := updateDates(gEdit.ID, "2026-06-13", "2026-06-21", gMultiExt.Version)
	if badShorten == nil {
		t.Fatal("expected 422 when shortening across consumed reservation R1, succeeded")
	}
	badShorten.Body.Close()
	if badShorten.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 shortening across consumed reservation, got %d", badShorten.StatusCode)
	}
	// Verify untouched after rejected edit
	assertEqualSegs(activeSegmentsForUnit(gEdit.ID, uPodA.ID), []string{"2026-06-09..2026-06-12", "2026-06-14..2026-06-15", "2026-06-17..2026-06-21"}, "unchanged after 422")

	// 2.6 Transaction rollback on overlap conflict: put a DIRECT booking on P16-B [06-22..06-24),
	// then try extending gEdit to [06-09..06-25) -> 409 Conflict, and P16-A remains [06-09..06-21)!
	_ = book("Direct Blocker", "DIRECT", "2026-06-22", "2026-06-24", []uuid.UUID{uPodB.ID})
	conflictExt, _ := updateDates(gEdit.ID, "2026-06-09", "2026-06-25", gMultiExt.Version)
	if conflictExt == nil {
		t.Fatal("expected 409 conflict extending over DIRECT booking on P16-B")
	}
	conflictExt.Body.Close()
	if conflictExt.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 on overlap extension, got %d", conflictExt.StatusCode)
	}
	assertEqualSegs(activeSegmentsForUnit(gEdit.ID, uPodA.ID), []string{"2026-06-09..2026-06-12", "2026-06-14..2026-06-15", "2026-06-17..2026-06-21"}, "rolled back after 409")

	// Full consumption lifecycle state (CONSUMED without released_at/by)
	gFull, _ := createAlloc([]uuid.UUID{uPodC.ID}, "2026-07-01", "2026-07-05", "AIRBNB", "Full Consumption Group")
	rFull := book("Full Consumed Guest", "AIRBNB", "2026-07-01", "2026-07-05", []uuid.UUID{uPodC.ID})
	gFullState, err := store.GetBlockoutGroup(context.Background(), nil, prop.ID, gFull.ID)
	if err != nil {
		t.Fatalf("GetBlockoutGroup gFull: %v", err)
	}
	if gFullState.Status != domain.BlockStatusConsumed || gFullState.ActiveClaimCount != 0 || gFullState.ConsumedClaimCount != 1 {
		t.Fatalf("expected CONSUMED with active=0 consumed=1, got %+v", gFullState)
	}
	if gFullState.ReleasedAt != nil || gFullState.ReleasedBy != nil {
		t.Fatalf("CONSUMED group must not set released_at/by, got %v / %v", gFullState.ReleasedAt, gFullState.ReleasedBy)
	}

	// Web UI /blocks?group_id=... shows CONSUMED & clickable link to /reservations/<id>
	blocksPage, err := ownerClient.Get(srv.URL + "/blocks?group_id=" + gFull.ID.String())
	if err != nil {
		t.Fatalf("GET /blocks?group_id: %v", err)
	}
	bodyBytes, _ := io.ReadAll(blocksPage.Body)
	blocksPage.Body.Close()
	bodyHTML := string(bodyBytes)
	if !strings.Contains(bodyHTML, "CONSUMED") || !strings.Contains(bodyHTML, "/reservations/"+rFull.ID.String()) {
		t.Fatalf("expected /blocks detail to render CONSUMED badge and reservation link /reservations/%s", rFull.ID)
	}

	// =========================================================================
	// SECTION 4: CANCELLATION & AMENDMENT RECONCILIATION
	// =========================================================================
	// 4.1 Cancel rFull (CANCELLED) -> restores [2026-07-01..2026-07-05) back to gFull!
	cancelResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations/"+rFull.ID.String()+"/transition", csrf, map[string]any{
		"status": "CANCELLED", "reason": "Guest cancelled", "version": rFull.Version,
	})
	if cancelResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(cancelResp.Body)
		cancelResp.Body.Close()
		t.Fatalf("cancel rFull failed (%d): %s", cancelResp.StatusCode, string(b))
	}
	cancelResp.Body.Close()

	gFullRestored, _ := store.GetBlockoutGroup(context.Background(), nil, prop.ID, gFull.ID)
	if gFullRestored.Status != domain.BlockStatusActive || gFullRestored.ActiveClaimCount != 1 {
		t.Fatalf("expected gFull restored to ACTIVE (1 active claim), got %+v", gFullRestored)
	}
	assertEqualSegs(activeSegmentsForUnit(gFull.ID, uPodC.ID), []string{"2026-07-01..2026-07-05"}, "restored full allocation")

	// DIRECT still blocked on restored AIRBNB allocation; AIRBNB can rebook!
	dirBlocked := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
		"guest_name": "Direct Attempt", "channel_code": "DIRECT",
		"arrival_date": "2026-07-01", "departure_date": "2026-07-04",
		"adults": 2, "status": "CONFIRMED", "unit_ids": []uuid.UUID{uPodC.ID},
	})
	dirBlocked.Body.Close()
	if dirBlocked.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for DIRECT on restored AIRBNB allocation, got %d", dirBlocked.StatusCode)
	}

	// 4.2 NO_SHOW coalesces restored range cleanly with adjacent segments
	gNoShow, _ := createAlloc([]uuid.UUID{uPodC.ID}, "2026-08-01", "2026-08-10", "AIRBNB", "August Airbnb")
	rNS := book("NoShow Guest", "AIRBNB", "2026-08-03", "2026-08-06", []uuid.UUID{uPodC.ID})
	assertEqualSegs(activeSegmentsForUnit(gNoShow.ID, uPodC.ID), []string{"2026-08-01..2026-08-03", "2026-08-06..2026-08-10"}, "split before NO_SHOW")
	nsResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations/"+rNS.ID.String()+"/transition", csrf, map[string]any{
		"status": "NO_SHOW", "reason": "Did not arrive", "version": rNS.Version,
	})
	if nsResp.StatusCode != http.StatusOK {
		t.Fatalf("NO_SHOW failed: %d", nsResp.StatusCode)
	}
	nsResp.Body.Close()
	// Coalesced back into [08-01..08-10)!
	assertEqualSegs(activeSegmentsForUnit(gNoShow.ID, uPodC.ID), []string{"2026-08-01..2026-08-10"}, "coalesced after NO_SHOW")

	// 4.3 CHECKED_OUT reservation NEVER reopens consumed allocation nights!
	gCheckedOut, _ := createAlloc([]uuid.UUID{uPodC.ID}, "2026-08-15", "2026-08-22", "AIRBNB", "Late Aug Airbnb")
	rOut := book("Stayed Guest", "AIRBNB", "2026-08-17", "2026-08-20", []uuid.UUID{uPodC.ID})
	ciResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations/"+rOut.ID.String()+"/transition", csrf, map[string]any{
		"status": "CHECKED_IN", "version": rOut.Version,
	})
	if ciResp.StatusCode != http.StatusOK {
		t.Fatalf("CHECKED_IN failed: %d", ciResp.StatusCode)
	}
	var rCI domain.Reservation
	_ = json.NewDecoder(ciResp.Body).Decode(&rCI)
	ciResp.Body.Close()

	coResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations/"+rCI.ID.String()+"/transition", csrf, map[string]any{
		"status": "CHECKED_OUT", "version": rCI.Version,
	})
	if coResp.StatusCode != http.StatusOK {
		t.Fatalf("CHECKED_OUT failed: %d", coResp.StatusCode)
	}
	coResp.Body.Close()
	assertEqualSegs(activeSegmentsForUnit(gCheckedOut.ID, uPodC.ID), []string{"2026-08-15..2026-08-17", "2026-08-20..2026-08-22"}, "CHECKED_OUT must not reopen past stay")

	// 4.4 Date & Unit amendment reconciles consumed segments back to originating blockout_group_id
	gMove, _ := createAlloc([]uuid.UUID{uPodC.ID}, "2026-09-01", "2026-09-10", "AIRBNB", "Sept Airbnb")
	rMove := book("Amend Dates Guest", "AIRBNB", "2026-09-02", "2026-09-05", []uuid.UUID{uPodC.ID})

	// Amend dates from [09-02..09-05) -> [09-05..09-08): [09-01..09-05) restored+coalesced, [09-08..09-10) remains!
	updDateResp := doJSON(t, ownerClient, http.MethodPatch, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations/"+rMove.ID.String(), csrf, map[string]any{
		"guest_name": "Amend Dates Guest", "channel_code": "AIRBNB",
		"arrival_date": "2026-09-05", "departure_date": "2026-09-08",
		"adults": 2, "unit_ids": []uuid.UUID{uPodC.ID}, "version": rMove.Version,
	})
	if updDateResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(updDateResp.Body)
		updDateResp.Body.Close()
		t.Fatalf("amend dates failed (%d): %s", updDateResp.StatusCode, string(b))
	}
	var rMovedDates domain.Reservation
	_ = json.NewDecoder(updDateResp.Body).Decode(&rMovedDates)
	updDateResp.Body.Close()
	assertEqualSegs(activeSegmentsForUnit(gMove.ID, uPodC.ID), []string{"2026-09-01..2026-09-05", "2026-09-08..2026-09-10"}, "dates amended reconciliation")

	// Swap unit from P16-C to P16-B (general pool on [09-05..09-08)): P16-C full [09-01..09-10) restores & coalesces!
	updUnitResp := doJSON(t, ownerClient, http.MethodPatch, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations/"+rMovedDates.ID.String(), csrf, map[string]any{
		"guest_name": "Amend Dates Guest", "channel_code": "AIRBNB",
		"arrival_date": "2026-09-05", "departure_date": "2026-09-08",
		"adults": 2, "unit_ids": []uuid.UUID{uPodB.ID}, "version": rMovedDates.Version,
	})
	if updUnitResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(updUnitResp.Body)
		updUnitResp.Body.Close()
		t.Fatalf("amend unit failed (%d): %s", updUnitResp.StatusCode, string(b))
	}
	updUnitResp.Body.Close()
	assertEqualSegs(activeSegmentsForUnit(gMove.ID, uPodC.ID), []string{"2026-09-01..2026-09-10"}, "restored P16-C after unit swap")

	// Channel change on active allocation must be rejected unless allocation is released
	gChg, _ := createAlloc([]uuid.UUID{uPodC.ID}, "2026-09-20", "2026-09-25", "AIRBNB", "Sept Late Airbnb")
	rChg := book("Channel Change Guest", "AIRBNB", "2026-09-21", "2026-09-24", []uuid.UUID{uPodC.ID})
	chgBlock := doJSON(t, ownerClient, http.MethodPatch, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations/"+rChg.ID.String(), csrf, map[string]any{
		"guest_name": "Channel Change Guest", "channel_code": "DIRECT",
		"arrival_date": "2026-09-21", "departure_date": "2026-09-24",
		"adults": 2, "unit_ids": []uuid.UUID{uPodC.ID}, "version": rChg.Version,
	})
	chgBlock.Body.Close()
	if chgBlock.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 changing AIRBNB->DIRECT on active AIRBNB allocation unit, got %d", chgBlock.StatusCode)
	}
	relChg := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts/"+gChg.ID.String()+"/release", csrf, map[string]any{})
	relChg.Body.Close()
	chgOK := doJSON(t, ownerClient, http.MethodPatch, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations/"+rChg.ID.String(), csrf, map[string]any{
		"guest_name": "Channel Change Guest", "channel_code": "DIRECT",
		"arrival_date": "2026-09-21", "departure_date": "2026-09-24",
		"adults": 2, "unit_ids": []uuid.UUID{uPodC.ID}, "version": rChg.Version,
	})
	if chgOK.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(chgOK.Body)
		chgOK.Body.Close()
		t.Fatalf("expected 200 changing AIRBNB->DIRECT after manual allocation release, got %d: %s", chgOK.StatusCode, string(b))
	}
	chgOK.Body.Close()

	// 4.5 Admin explicitly releases remainder -> cancelling consumed reservation MUST NOT recreate released allocation!
	gManRel, _ := createAlloc([]uuid.UUID{uPodC.ID}, "2026-10-01", "2026-10-10", "AIRBNB", "Manual Release Remainder")
	rManRel := book("Rel Guest", "AIRBNB", "2026-10-03", "2026-10-06", []uuid.UUID{uPodC.ID})
	relRemResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts/"+gManRel.ID.String()+"/release", csrf, map[string]any{})
	if relRemResp.StatusCode != http.StatusOK {
		t.Fatalf("release remainder failed: %d", relRemResp.StatusCode)
	}
	var relOut struct {
		Group domain.BlockoutGroup `json:"group"`
	}
	_ = json.NewDecoder(relRemResp.Body).Decode(&relOut)
	relRemResp.Body.Close()
	if relOut.Group.Status != domain.BlockStatusPartiallyReleased || relOut.Group.ReleasedAt == nil {
		t.Fatalf("expected PARTIALLY_RELEASED with ReleasedAt set, got %+v", relOut.Group)
	}

	// Cancel rManRel -> [10-03..10-06) goes to general pool (NOT re-locked to AIRBNB), so DIRECT can book [10-03..10-06)!
	canRelResp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations/"+rManRel.ID.String()+"/transition", csrf, map[string]any{
		"status": "CANCELLED", "version": rManRel.Version,
	})
	if canRelResp.StatusCode != http.StatusOK {
		t.Fatalf("cancel rManRel failed: %d", canRelResp.StatusCode)
	}
	canRelResp.Body.Close()
	_ = book("Direct After Manual Release Cancel", "DIRECT", "2026-10-03", "2026-10-06", []uuid.UUID{uPodC.ID})

	// =========================================================================
	// SECTION 5: SMOOBU PROVIDER REFINEMENT
	// =========================================================================
	cmListResp, err := ownerClient.Get(srv.URL + "/api/v1/properties/" + prop.ID.String() + "/integrations/channel-manager")
	if err != nil {
		t.Fatalf("GET channel-manager: %v", err)
	}
	var cmList struct {
		Items []service.ChannelProviderView `json:"items"`
	}
	_ = json.NewDecoder(cmListResp.Body).Decode(&cmList)
	cmListResp.Body.Close()

	var smoobuDef *channels.ProviderDefinition
	for i := range cmList.Items {
		if cmList.Items[i].Provider.Code == "smoobu" {
			p := cmList.Items[i].Provider
			smoobuDef = &p
			break
		}
	}
	if smoobuDef == nil {
		t.Fatal("smoobu provider missing from registry")
	}
	if smoobuDef.AuthMethod != channels.AuthHMAC || smoobuDef.SignatureSpec == nil {
		t.Fatalf("expected Smoobu hmac_sha256 + SignatureSpec, got %+v", smoobuDef)
	}
	if smoobuDef.SignatureSpec.SignatureEncoding != "base64" || smoobuDef.SignatureSpec.AccountDiscoveryEndpoint != "GET /api/me" {
		t.Fatalf("unexpected Smoobu SignatureSpec: %+v", smoobuDef.SignatureSpec)
	}
	for _, f := range smoobuDef.Fields {
		if f.Name == "user_id" {
			t.Fatal("Smoobu must not require manual user_id input")
		}
	}
	if len(smoobuDef.Fields) != 2 {
		t.Fatalf("expected exactly 2 Smoobu fields (api_key, api_secret), got %d", len(smoobuDef.Fields))
	}

	// Reject stale user_id field cleanly (422)
	rejectUserID := doJSON(t, ownerClient, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/smoobu", csrf, map[string]any{
		"display_name": "Smoobu",
		"inputs": map[string]string{
			"user_id": "123", "api_key": "k", "api_secret": "s",
		},
	})
	rejectUserID.Body.Close()
	if rejectUserID.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 rejecting unsupported user_id for Smoobu, got %d", rejectUserID.StatusCode)
	}
}

func TestPhase1_6_1PreservesReleasedInventoryAndExplicitReinstate(t *testing.T) {
	srv, store, prop := setupTestServer(t)
	ownerClient := newClient(t)
	code, csrf := apiLogin(t, ownerClient, srv.URL, "owner@tirbach.wales", "tirbach-owner-password-2026")
	if code != http.StatusOK {
		t.Fatalf("owner login failed: %d", code)
	}

	ut := mustCreateUnitTypeCustom(t, ownerClient, srv.URL, prop.ID, csrf, "cabin-161", "Lovelace Cabin", "cabin", 2, 1, 1, 4, 15000)
	uA := mustCreateUnit(t, ownerClient, srv.URL, prop.ID, csrf, ut.ID, "C161-A", "Cabin A")
	uB := mustCreateUnit(t, ownerClient, srv.URL, prop.ID, csrf, ut.ID, "C161-B", "Cabin B")
	uC := mustCreateUnit(t, ownerClient, srv.URL, prop.ID, csrf, ut.ID, "C161-C", "Cabin C")

	createAlloc := func(unitIDs []uuid.UUID, start, end, channel, reason string) domain.BlockoutGroup {
		t.Helper()
		resp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts", csrf, map[string]any{
			"scope_type": "UNITS", "unit_ids": unitIDs, "category": "CHANNEL_ALLOCATION",
			"channel_scope": channel, "start_date": start, "end_date": end, "reason": reason,
		})
		if resp.StatusCode != http.StatusCreated {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("create alloc %s..%s failed (%d): %s", start, end, resp.StatusCode, string(b))
		}
		var out struct {
			Group domain.BlockoutGroup `json:"group"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		return out.Group
	}

	releaseUnits := func(groupID uuid.UUID, unitIDs []uuid.UUID) domain.BlockoutGroup {
		t.Helper()
		resp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts/"+groupID.String()+"/release", csrf, map[string]any{
			"unit_ids": unitIDs,
		})
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("release failed (%d): %s", resp.StatusCode, string(b))
		}
		var out struct {
			Group domain.BlockoutGroup `json:"group"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		return out.Group
	}

	updateDates := func(groupID uuid.UUID, start, end string, version int) (*http.Response, domain.BlockoutGroup) {
		t.Helper()
		resp := doJSON(t, ownerClient, http.MethodPatch, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts/"+groupID.String()+"/dates", csrf, map[string]any{
			"start_date": start, "end_date": end, "version": version,
		})
		if resp.StatusCode != http.StatusOK {
			return resp, domain.BlockoutGroup{}
		}
		var out struct {
			Group domain.BlockoutGroup `json:"group"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		return nil, out.Group
	}

	bookExpect := func(guest, channel, start, end string, unitIDs []uuid.UUID, wantStatus int) domain.Reservation {
		t.Helper()
		resp := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/reservations", csrf, map[string]any{
			"guest_name": guest, "channel_code": channel,
			"arrival_date": start, "departure_date": end,
			"adults": 2, "status": "CONFIRMED", "unit_ids": unitIDs,
		})
		if resp.StatusCode != wantStatus {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("booking %s (%s %s..%s): want %d got %d: %s", guest, channel, start, end, wantStatus, resp.StatusCode, string(b))
		}
		var out domain.Reservation
		if wantStatus == http.StatusCreated {
			_ = json.NewDecoder(resp.Body).Decode(&out)
		}
		resp.Body.Close()
		return out
	}

	claimsSummary := func(groupID, unitID uuid.UUID) []domain.InventoryClaim {
		t.Helper()
		all, err := store.ListClaimsByBlockoutGroup(context.Background(), nil, prop.ID, groupID)
		if err != nil {
			t.Fatalf("ListClaimsByBlockoutGroup: %v", err)
		}
		var filtered []domain.InventoryClaim
		for _, c := range all {
			if c.UnitID == unitID {
				filtered = append(filtered, c)
			}
		}
		sort.Slice(filtered, func(i, j int) bool {
			if filtered[i].StartDate.Equal(filtered[j].StartDate) {
				return filtered[i].Status < filtered[j].Status
			}
			return filtered[i].StartDate.Before(filtered[j].StartDate)
		})
		return filtered
	}

	// =========================================================================
	// 1. MANDATORY SCENARIO: Aug 1-10 Airbnb -> Aug 3-5 Res -> Release Remaining -> Amend Aug 1-12
	// =========================================================================
	gAug := createAlloc([]uuid.UUID{uA.ID}, "2026-08-01", "2026-08-10", "AIRBNB", "August Airbnb")
	_ = bookExpect("Aug Airbnb Guest", "AIRBNB", "2026-08-03", "2026-08-05", []uuid.UUID{uA.ID}, http.StatusCreated)
	gAug = releaseUnits(gAug.ID, nil)

	preAmendClaims := claimsSummary(gAug.ID, uA.ID)
	if len(preAmendClaims) != 3 {
		t.Fatalf("expected 3 pre-amend claims (original CONSUMED + [01..03) RELEASED + [05..10) RELEASED), got %d: %+v", len(preAmendClaims), preAmendClaims)
	}
	consumedOrigBefore := preAmendClaims[0]
	rel1Before := preAmendClaims[1]
	rel2Before := preAmendClaims[2]
	if consumedOrigBefore.Status != domain.ClaimStatusConsumed ||
		domain.FormatDate(consumedOrigBefore.StartDate) != "2026-08-01" ||
		domain.FormatDate(consumedOrigBefore.EndDate) != "2026-08-10" {
		t.Fatalf("expected consumed parent claim 2026-08-01..2026-08-10 CONSUMED, got %+v", consumedOrigBefore)
	}
	if rel1Before.Status != domain.ClaimStatusReleased || rel1Before.ReleasedAt == nil || rel1Before.ReleasedBy == nil ||
		domain.FormatDate(rel1Before.StartDate) != "2026-08-01" || domain.FormatDate(rel1Before.EndDate) != "2026-08-03" {
		t.Fatalf("expected [08-01..08-03) RELEASED with provenance, got %+v", rel1Before)
	}
	if rel2Before.Status != domain.ClaimStatusReleased || rel2Before.ReleasedAt == nil || rel2Before.ReleasedBy == nil ||
		domain.FormatDate(rel2Before.StartDate) != "2026-08-05" || domain.FormatDate(rel2Before.EndDate) != "2026-08-10" {
		t.Fatalf("expected [08-05..08-10) RELEASED with provenance, got %+v", rel2Before)
	}

	// Verify exact consumed slice [2026-08-03..2026-08-05) recorded in allocation_consumptions
	var consRange string
	if err := store.Pool().QueryRow(context.Background(), `
		SELECT lower(consumed_range)::text || '..' || upper(consumed_range)::text
		FROM allocation_consumptions
		WHERE property_id = $1 AND blockout_group_id = $2 AND unit_id = $3 AND status = 'CONSUMED'
	`, prop.ID, gAug.ID, uA.ID).Scan(&consRange); err != nil {
		t.Fatalf("query allocation_consumptions: %v", err)
	}
	if consRange != "2026-08-03..2026-08-05" {
		t.Fatalf("expected consumed slice 2026-08-03..2026-08-05, got %s", consRange)
	}

	// Amend dates from Aug 1-10 to Aug 1-12
	errResp, gAug := updateDates(gAug.ID, "2026-08-01", "2026-08-12", gAug.Version)
	if errResp != nil {
		b, _ := io.ReadAll(errResp.Body)
		errResp.Body.Close()
		t.Fatalf("amend Aug 1-12 failed: %d %s", errResp.StatusCode, string(b))
	}

	postAmendClaims := claimsSummary(gAug.ID, uA.ID)
	if len(postAmendClaims) != 4 {
		t.Fatalf("expected 4 post-amend claims, got %d: %+v", len(postAmendClaims), postAmendClaims)
	}
	wantPost := []struct {
		seg    string
		status domain.ClaimStatus
	}{
		{"2026-08-01..2026-08-10", domain.ClaimStatusConsumed},
		{"2026-08-01..2026-08-03", domain.ClaimStatusReleased},
		{"2026-08-05..2026-08-10", domain.ClaimStatusReleased},
		{"2026-08-10..2026-08-12", domain.ClaimStatusActive},
	}
	for i, w := range wantPost {
		gotSeg := domain.FormatDate(postAmendClaims[i].StartDate) + ".." + domain.FormatDate(postAmendClaims[i].EndDate)
		if gotSeg != w.seg || postAmendClaims[i].Status != w.status {
			t.Fatalf("claim[%d]: expected %s (%s), got %s (%s)", i, w.seg, w.status, gotSeg, postAmendClaims[i].Status)
		}
	}
	// Exact original claim IDs and release provenance MUST be preserved
	if postAmendClaims[0].ID != consumedOrigBefore.ID {
		t.Fatalf("consumed parent claim ID changed! before=%s after=%s", consumedOrigBefore.ID, postAmendClaims[0].ID)
	}
	if postAmendClaims[1].ID != rel1Before.ID || !postAmendClaims[1].ReleasedAt.Equal(*rel1Before.ReleasedAt) || *postAmendClaims[1].ReleasedBy != *rel1Before.ReleasedBy {
		t.Fatalf("released segment [08-01..08-03) provenance mutated! before=%+v after=%+v", rel1Before, postAmendClaims[1])
	}
	if postAmendClaims[2].ID != rel2Before.ID || !postAmendClaims[2].ReleasedAt.Equal(*rel2Before.ReleasedAt) || *postAmendClaims[2].ReleasedBy != *rel2Before.ReleasedBy {
		t.Fatalf("released segment [08-05..08-10) provenance mutated! before=%+v after=%+v", rel2Before, postAmendClaims[2])
	}
	if gAug.Status != domain.BlockStatusPartiallyReleased {
		t.Fatalf("expected group status PARTIALLY_RELEASED (has RELEASED 08-01..08-03 & 08-05..08-10 + ACTIVE 08-10..08-12), got %s", gAug.Status)
	}

	// Released Aug 5-8 MUST be bookable by DIRECT; active Aug 10-12 MUST remain blocked from DIRECT
	_ = bookExpect("Direct On Released", "DIRECT", "2026-08-05", "2026-08-08", []uuid.UUID{uA.ID}, http.StatusCreated)
	_ = bookExpect("Direct On Active Ext", "DIRECT", "2026-08-10", "2026-08-12", []uuid.UUID{uA.ID}, http.StatusConflict)

	// Web UI blocks detail shows Reinstate Claim for RELEASED segments
	webResp, err := ownerClient.Get(srv.URL + "/blocks?group_id=" + gAug.ID.String())
	if err != nil {
		t.Fatalf("GET /blocks: %v", err)
	}
	webBody, _ := io.ReadAll(webResp.Body)
	webResp.Body.Close()
	if webResp.StatusCode != http.StatusOK || !strings.Contains(string(webBody), "Reinstate Claim") {
		t.Fatalf("expected /blocks detail view to render 'Reinstate Claim' action for RELEASED claims")
	}

	// =========================================================================
	// 2. EXPLICIT REINSTATE: blocked on conflict, succeeds on clear segment
	// =========================================================================
	reinstateConflict := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts/"+gAug.ID.String()+"/reinstate", csrf, map[string]any{
		"claim_ids": []uuid.UUID{rel2Before.ID},
	})
	reinstateConflict.Body.Close()
	if reinstateConflict.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 reinstating segment now occupied by DIRECT booking, got %d", reinstateConflict.StatusCode)
	}

	reinstateOK := doJSON(t, ownerClient, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/blockouts/"+gAug.ID.String()+"/reinstate", csrf, map[string]any{
		"claim_ids": []uuid.UUID{rel1Before.ID},
	})
	if reinstateOK.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(reinstateOK.Body)
		reinstateOK.Body.Close()
		t.Fatalf("expected 200 reinstating clear [08-01..08-03), got %d: %s", reinstateOK.StatusCode, string(b))
	}
	reinstateOK.Body.Close()

	afterReinstate := claimsSummary(gAug.ID, uA.ID)
	var foundReinstated *domain.InventoryClaim
	for i := range afterReinstate {
		if afterReinstate[i].ID == rel1Before.ID {
			foundReinstated = &afterReinstate[i]
			break
		}
	}
	if foundReinstated == nil || foundReinstated.Status != domain.ClaimStatusActive || foundReinstated.ReleasedAt != nil || foundReinstated.ReleasedBy != nil {
		t.Fatalf("expected reinstated [08-01..08-03) ACTIVE with cleared release metadata, got %+v", foundReinstated)
	}
	// DIRECT now blocked on reinstated [08-01..08-03)
	_ = bookExpect("Direct On Reinstated", "DIRECT", "2026-08-01", "2026-08-03", []uuid.UUID{uA.ID}, http.StatusConflict)

	// =========================================================================
	// 3. SELECTED-UNIT RELEASE + AMEND DATES (EXTEND & SHORTEN)
	// =========================================================================
	gSel := createAlloc([]uuid.UUID{uB.ID, uC.ID}, "2026-09-01", "2026-09-10", "BOOKING_COM", "Sept Selection")
	gSel = releaseUnits(gSel.ID, []uuid.UUID{uB.ID})

	errResp, gSel = updateDates(gSel.ID, "2026-09-01", "2026-09-12", gSel.Version)
	if errResp != nil {
		b, _ := io.ReadAll(errResp.Body)
		errResp.Body.Close()
		t.Fatalf("extend selected-unit group failed: %d %s", errResp.StatusCode, string(b))
	}
	claimsBExt := claimsSummary(gSel.ID, uB.ID)
	if len(claimsBExt) != 2 || claimsBExt[0].Status != domain.ClaimStatusReleased || claimsBExt[1].Status != domain.ClaimStatusActive ||
		domain.FormatDate(claimsBExt[1].StartDate) != "2026-09-10" || domain.FormatDate(claimsBExt[1].EndDate) != "2026-09-12" {
		t.Fatalf("unexpected uB claims after extend: %+v", claimsBExt)
	}
	claimsCExt := claimsSummary(gSel.ID, uC.ID)
	if len(claimsCExt) != 1 || claimsCExt[0].Status != domain.ClaimStatusActive ||
		domain.FormatDate(claimsCExt[0].StartDate) != "2026-09-01" || domain.FormatDate(claimsCExt[0].EndDate) != "2026-09-12" {
		t.Fatalf("unexpected uC claims after extend: %+v", claimsCExt)
	}

	// Shorten to Sept 1-8: uB keeps historical RELEASED [09-01..09-10) and drops ACTIVE [09-10..09-12); uC shortens to [09-01..09-08)
	errResp, gSel = updateDates(gSel.ID, "2026-09-01", "2026-09-08", gSel.Version)
	if errResp != nil {
		b, _ := io.ReadAll(errResp.Body)
		errResp.Body.Close()
		t.Fatalf("shorten selected-unit group failed: %d %s", errResp.StatusCode, string(b))
	}
	claimsBShort := claimsSummary(gSel.ID, uB.ID)
	if len(claimsBShort) != 1 || claimsBShort[0].Status != domain.ClaimStatusReleased {
		t.Fatalf("expected uB to have only RELEASED [09-01..09-10) after shortening, got %+v", claimsBShort)
	}
	claimsCShort := claimsSummary(gSel.ID, uC.ID)
	if len(claimsCShort) != 1 || claimsCShort[0].Status != domain.ClaimStatusActive || domain.FormatDate(claimsCShort[0].EndDate) != "2026-09-08" {
		t.Fatalf("expected uC ACTIVE [09-01..09-08), got %+v", claimsCShort)
	}

	// =========================================================================
	// 4. ENTIRE GROUP RELEASE WITHOUT CONSUMPTIONS & ROLLBACK SAFETY
	// =========================================================================
	gFullRel := createAlloc([]uuid.UUID{uB.ID}, "2026-11-01", "2026-11-10", "AIRBNB", "Nov Entire Release")
	gFullRel = releaseUnits(gFullRel.ID, nil)

	// Shortening within already-released range has no active or consumed segments -> rejected cleanly (409)
	errResp, _ = updateDates(gFullRel.ID, "2026-11-01", "2026-11-08", gFullRel.Version)
	if errResp == nil || errResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 shortening fully released group with no net-new active dates")
	}
	errResp.Body.Close()

	// Extending [11-01..11-10) released group to [11-01..11-12) activates ONLY [11-10..11-12)
	errResp, gFullRel = updateDates(gFullRel.ID, "2026-11-01", "2026-11-12", gFullRel.Version)
	if errResp != nil {
		b, _ := io.ReadAll(errResp.Body)
		errResp.Body.Close()
		t.Fatalf("expected extending released group to net-new dates [11-10..11-12) to succeed, got %d: %s", errResp.StatusCode, string(b))
	}
	novClaims := claimsSummary(gFullRel.ID, uB.ID)
	if len(novClaims) != 2 || novClaims[0].Status != domain.ClaimStatusReleased ||
		novClaims[1].Status != domain.ClaimStatusActive || domain.FormatDate(novClaims[1].StartDate) != "2026-11-10" {
		t.Fatalf("unexpected Nov claims after extend: %+v", novClaims)
	}

	// =========================================================================
	// 5. MULTIPLE CONSUMPTIONS + ROLLBACK ON CONFLICTING EXTENSION
	// =========================================================================
	gMulti := createAlloc([]uuid.UUID{uC.ID}, "2026-10-01", "2026-10-15", "AIRBNB", "Oct Multi")
	_ = bookExpect("Oct Guest 1", "AIRBNB", "2026-10-03", "2026-10-05", []uuid.UUID{uC.ID}, http.StatusCreated)
	_ = bookExpect("Oct Guest 2", "AIRBNB", "2026-10-08", "2026-10-10", []uuid.UUID{uC.ID}, http.StatusCreated)
	_ = bookExpect("Conflicting Direct", "DIRECT", "2026-10-16", "2026-10-18", []uuid.UUID{uC.ID}, http.StatusCreated)

	octBefore, err := store.GetBlockoutGroup(context.Background(), nil, prop.ID, gMulti.ID)
	if err != nil {
		t.Fatalf("GetBlockoutGroup: %v", err)
	}

	// Trying to extend Oct Multi across conflicting DIRECT booking [10-16..10-18) MUST roll back cleanly
	errResp, _ = updateDates(gMulti.ID, "2026-10-01", "2026-10-18", octBefore.Version)
	if errResp == nil || errResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 rolling back conflicting extension")
	}
	errResp.Body.Close()

	octAfterRollback, err := store.GetBlockoutGroup(context.Background(), nil, prop.ID, gMulti.ID)
	if err != nil {
		t.Fatalf("GetBlockoutGroup: %v", err)
	}
	if domain.FormatDate(octAfterRollback.EndDate) != "2026-10-15" || octAfterRollback.Version != octBefore.Version {
		t.Fatalf("expected rolled-back group dates/version unchanged, got end=%s ver=%d (want %d)", domain.FormatDate(octAfterRollback.EndDate), octAfterRollback.Version, octBefore.Version)
	}

	// =========================================================================
	// 6. AUDIT HISTORY VERIFICATION
	// =========================================================================
	auditResp, err := ownerClient.Get(srv.URL + "/api/v1/audit-logs?property_id=" + prop.ID.String() + "&resource_type=blockout_group&limit=50")
	if err != nil {
		t.Fatalf("GET audit-logs: %v", err)
	}
	var auditOut struct {
		Items []domain.AuditLog `json:"items"`
	}
	_ = json.NewDecoder(auditResp.Body).Decode(&auditOut)
	auditResp.Body.Close()
	actions := map[string]bool{}
	for _, a := range auditOut.Items {
		actions[a.Action] = true
	}
	for _, wantAction := range []string{"blockout_group.created", "blockout_group.released", "blockout_group.dates_updated", "blockout_group.reinstated"} {
		if !actions[wantAction] {
			t.Fatalf("expected audit action %q in blockout_group audit history, got actions=%v", wantAction, actions)
		}
	}
}
