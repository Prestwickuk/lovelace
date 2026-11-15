package postgres

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/domain"
)

// ResolveBlockoutTargetUnits resolves the current active/eligible physical units matching scope.
func (s *Store) ResolveBlockoutTargetUnits(ctx context.Context, db DBTX, in domain.BulkBlockoutInput) ([]domain.Unit, error) {
	if db == nil {
		db = s.pool
	}
	var rows pgx.Rows
	var err error
	switch in.ScopeType {
	case domain.BlockScopeProperty:
		rows, err = db.Query(ctx, `
			SELECT u.id, u.property_id, u.unit_type_id, ut.name, u.code, u.name, u.zone,
			       u.status, u.access_notes, u.operational_notes, u.attributes, u.sort_order,
			       u.version, u.archived_at, u.created_at, u.updated_at
			FROM units u
			JOIN unit_types ut ON ut.id = u.unit_type_id
			WHERE u.property_id = $1 AND u.archived_at IS NULL AND u.status NOT IN ('inactive', 'retired')
			ORDER BY u.sort_order ASC, u.code ASC
		`, in.PropertyID)
	case domain.BlockScopeUnitType:
		rows, err = db.Query(ctx, `
			SELECT u.id, u.property_id, u.unit_type_id, ut.name, u.code, u.name, u.zone,
			       u.status, u.access_notes, u.operational_notes, u.attributes, u.sort_order,
			       u.version, u.archived_at, u.created_at, u.updated_at
			FROM units u
			JOIN unit_types ut ON ut.id = u.unit_type_id
			WHERE u.property_id = $1 AND u.unit_type_id = $2 AND u.archived_at IS NULL AND u.status NOT IN ('inactive', 'retired')
			ORDER BY u.sort_order ASC, u.code ASC
		`, in.PropertyID, *in.UnitTypeID)
	default:
		rows, err = db.Query(ctx, `
			SELECT u.id, u.property_id, u.unit_type_id, ut.name, u.code, u.name, u.zone,
			       u.status, u.access_notes, u.operational_notes, u.attributes, u.sort_order,
			       u.version, u.archived_at, u.created_at, u.updated_at
			FROM units u
			JOIN unit_types ut ON ut.id = u.unit_type_id
			WHERE u.property_id = $1 AND u.id = ANY($2::uuid[]) AND u.archived_at IS NULL AND u.status NOT IN ('inactive', 'retired')
			ORDER BY u.sort_order ASC, u.code ASC
		`, in.PropertyID, in.UnitIDs)
	}
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.Unit
	for rows.Next() {
		u, err := scanUnit(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, u)
	}
	return out, MapErr(rows.Err())
}

// FindClaimConflicts finds active inventory claims overlapping [startDate, endDate) on unitIDs
// assuming a general physical closure or unrestricted check.
func (s *Store) FindClaimConflicts(
	ctx context.Context,
	db DBTX,
	propertyID uuid.UUID,
	unitIDs []uuid.UUID,
	startDate, endDate time.Time,
	excludeBlockoutGroupID *uuid.UUID,
	excludeReservationID *uuid.UUID,
) ([]domain.BlockoutConflict, error) {
	return s.FindClaimConflictsForChannel(
		ctx, db, propertyID, unitIDs, startDate, endDate,
		domain.ClaimAdministrativeBlock, "ALL", excludeBlockoutGroupID, excludeReservationID,
	)
}

// FindClaimConflictsForChannel evaluates channel-aware active claim conflicts:
//   - CHANNEL_RESTRICTION conflicts only with another CHANNEL_RESTRICTION on the same channel.
//   - CHANNEL_ALLOCATION conflicts with non-restriction active claims (including other channels' allocations).
//   - Physical closures (OWNER_USE, MAINTENANCE, etc.) conflict with non-restriction active claims.
//   - RESERVATION / PENDING_RESERVATION conflict with non-restriction active claims (except a matching
//     CHANNEL_ALLOCATION for the same channel, which is atomically consumed) plus any CHANNEL_RESTRICTION
//     blocking that reservation's channel.
func (s *Store) FindClaimConflictsForChannel(
	ctx context.Context,
	db DBTX,
	propertyID uuid.UUID,
	unitIDs []uuid.UUID,
	startDate, endDate time.Time,
	incomingType domain.ClaimType,
	incomingChannel string,
	excludeBlockoutGroupID *uuid.UUID,
	excludeReservationID *uuid.UUID,
) ([]domain.BlockoutConflict, error) {
	if db == nil {
		db = s.pool
	}
	if len(unitIDs) == 0 {
		return nil, nil
	}
	ch := strings.ToUpper(strings.TrimSpace(incomingChannel))
	if ch == "" {
		ch = "ALL"
	}
	rows, err := db.Query(ctx, `
		SELECT ic.unit_id, u.code, u.name, ic.id, ic.claim_type,
		       ic.reservation_id, COALESCE(r.reference, ''), COALESCE(r.guest_name, ''),
		       lower(ic.claim_range), upper(ic.claim_range), ic.reason
		FROM inventory_claims ic
		JOIN units u ON u.id = ic.unit_id
		LEFT JOIN reservations r ON r.id = ic.reservation_id
		WHERE ic.property_id = $1
		  AND ic.unit_id = ANY($2::uuid[])
		  AND ic.status = 'ACTIVE'
		  AND ic.claim_range && daterange($3::date, $4::date, '[)')
		  AND ($5::uuid IS NULL OR ic.blockout_group_id IS DISTINCT FROM $5)
		  AND ($6::uuid IS NULL OR ic.reservation_id IS DISTINCT FROM $6)
		  AND (
			  CASE $7::text
			  WHEN 'CHANNEL_RESTRICTION' THEN
				  ic.claim_type = 'CHANNEL_RESTRICTION' AND ic.channel_scope = $8
			  WHEN 'RESERVATION' THEN
				  (ic.claim_type = 'CHANNEL_RESTRICTION' AND ic.channel_scope = $8)
				  OR (ic.claim_type <> 'CHANNEL_RESTRICTION'
				      AND NOT (ic.claim_type = 'CHANNEL_ALLOCATION' AND ic.channel_scope = $8))
			  WHEN 'PENDING_RESERVATION' THEN
				  (ic.claim_type = 'CHANNEL_RESTRICTION' AND ic.channel_scope = $8)
				  OR (ic.claim_type <> 'CHANNEL_RESTRICTION'
				      AND NOT (ic.claim_type = 'CHANNEL_ALLOCATION' AND ic.channel_scope = $8))
			  WHEN 'CHANNEL_ALLOCATION' THEN
				  ic.claim_type <> 'CHANNEL_RESTRICTION'
				  OR (ic.claim_type = 'CHANNEL_RESTRICTION' AND ic.channel_scope = $8)
			  ELSE
				  ic.claim_type <> 'CHANNEL_RESTRICTION'
			  END
		  )
		ORDER BY u.code ASC, lower(ic.claim_range) ASC
	`, propertyID, unitIDs, domain.FormatDate(startDate), domain.FormatDate(endDate), excludeBlockoutGroupID, excludeReservationID, string(incomingType), ch)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.BlockoutConflict
	for rows.Next() {
		var c domain.BlockoutConflict
		var ct string
		if err := rows.Scan(
			&c.UnitID, &c.UnitCode, &c.UnitName, &c.ClaimID, &ct,
			&c.ReservationID, &c.Reference, &c.GuestName,
			&c.StartDate, &c.EndDate, &c.Reason,
		); err != nil {
			return nil, MapErr(err)
		}
		c.ClaimType = domain.ClaimType(ct)
		out = append(out, c)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) ListBlockoutGroups(ctx context.Context, propertyID uuid.UUID, includeReleased bool) ([]domain.BlockoutGroup, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT bg.id, bg.property_id, bg.scope_type, bg.target_unit_type_id,
		       bg.category, bg.channel_scope, lower(bg.block_range), upper(bg.block_range),
		       bg.reason, bg.apply_to_future_units, bg.status,
		       COUNT(ic.id) FILTER (WHERE ic.status = 'ACTIVE') AS active_count,
		       (SELECT COUNT(*) FROM allocation_consumptions ac WHERE ac.blockout_group_id = bg.id AND ac.status = 'CONSUMED') AS consumed_count,
		       COUNT(ic.id) FILTER (WHERE ic.status = 'RELEASED') AS released_count,
		       COUNT(ic.id) AS total_count,
		       bg.created_by, bg.released_by, bg.released_at, bg.version, bg.created_at, bg.updated_at
		FROM blockout_groups bg
		LEFT JOIN inventory_claims ic ON ic.blockout_group_id = bg.id
		WHERE bg.property_id = $1
		  AND ($2::boolean OR bg.status <> 'RELEASED')
		GROUP BY bg.id
		ORDER BY (bg.status = 'RELEASED') ASC, lower(bg.block_range) ASC, bg.created_at DESC
	`, propertyID, includeReleased)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.BlockoutGroup
	for rows.Next() {
		g, err := scanBlockoutGroup(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, g)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetBlockoutGroup(ctx context.Context, db DBTX, propertyID, id uuid.UUID) (domain.BlockoutGroup, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT bg.id, bg.property_id, bg.scope_type, bg.target_unit_type_id,
		       bg.category, bg.channel_scope, lower(bg.block_range), upper(bg.block_range),
		       bg.reason, bg.apply_to_future_units, bg.status,
		       COUNT(ic.id) FILTER (WHERE ic.status = 'ACTIVE') AS active_count,
		       (SELECT COUNT(*) FROM allocation_consumptions ac WHERE ac.blockout_group_id = bg.id AND ac.status = 'CONSUMED') AS consumed_count,
		       COUNT(ic.id) FILTER (WHERE ic.status = 'RELEASED') AS released_count,
		       COUNT(ic.id) AS total_count,
		       bg.created_by, bg.released_by, bg.released_at, bg.version, bg.created_at, bg.updated_at
		FROM blockout_groups bg
		LEFT JOIN inventory_claims ic ON ic.blockout_group_id = bg.id
		WHERE bg.property_id = $1 AND bg.id = $2
		GROUP BY bg.id
	`, propertyID, id)
	g, err := scanBlockoutGroup(row)
	return g, MapErr(err)
}

func (s *Store) ListClaimsByBlockoutGroup(ctx context.Context, db DBTX, propertyID, groupID uuid.UUID) ([]domain.InventoryClaim, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT ic.id, ic.property_id, ic.unit_id, u.code, u.name,
		       ic.claim_type, ic.reservation_id,
		       COALESCE(cr.reference, ''), COALESCE(cr.guest_name, ''), COALESCE(cr.status, ''),
		       ic.blockout_group_id, ic.channel_scope,
		       lower(ic.claim_range), upper(ic.claim_range),
		       ic.status, ic.consumed_by_reservation_id, ic.reason, ic.created_by, ic.released_by, ic.released_at,
		       ic.created_at, ic.updated_at
		FROM inventory_claims ic
		JOIN units u ON u.id = ic.unit_id
		LEFT JOIN reservations cr ON cr.id = ic.consumed_by_reservation_id
		WHERE ic.property_id = $1 AND ic.blockout_group_id = $2
		ORDER BY u.code ASC, lower(ic.claim_range) ASC
	`, propertyID, groupID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.InventoryClaim
	for rows.Next() {
		c, err := scanInventoryClaim(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, c)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) CreateBulkBlockout(
	ctx context.Context,
	tx pgx.Tx,
	g domain.BlockoutGroup,
	unitIDs []uuid.UUID,
) (domain.BlockoutGroup, []domain.InventoryClaim, error) {
	_, err := tx.Exec(ctx, `
		INSERT INTO blockout_groups (
			id, property_id, scope_type, target_unit_type_id, category, channel_scope,
			block_range, reason, apply_to_future_units, status, created_by
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			daterange($7::date, $8::date, '[)'), $9, $10, 'ACTIVE', $11
		)
	`,
		g.ID, g.PropertyID, string(g.ScopeType), g.TargetUnitTypeID, string(g.Category), g.ChannelScope,
		domain.FormatDate(g.StartDate), domain.FormatDate(g.EndDate), g.Reason, g.ApplyToFutureUnits, g.CreatedBy,
	)
	if err != nil {
		return domain.BlockoutGroup{}, nil, MapErr(err)
	}

	for _, uid := range unitIDs {
		cid, err := NewUUID()
		if err != nil {
			return domain.BlockoutGroup{}, nil, err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO inventory_claims (
				id, property_id, unit_id, claim_type, blockout_group_id, channel_scope,
				claim_range, status, reason, created_by
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				daterange($7::date, $8::date, '[)'), 'ACTIVE', $9, $10
			)
		`,
			cid, g.PropertyID, uid, string(g.Category), g.ID, g.ChannelScope,
			domain.FormatDate(g.StartDate), domain.FormatDate(g.EndDate), g.Reason, g.CreatedBy,
		)
		if err != nil {
			return domain.BlockoutGroup{}, nil, MapErr(err)
		}
	}

	createdGroup, err := s.GetBlockoutGroup(ctx, tx, g.PropertyID, g.ID)
	if err != nil {
		return domain.BlockoutGroup{}, nil, err
	}
	claims, err := s.ListClaimsByBlockoutGroup(ctx, tx, g.PropertyID, g.ID)
	return createdGroup, claims, err
}

// ReleaseBlockoutClaims releases either all active claims in a group (if unitIDs empty)
// or specific selected unitIDs from the group.
func (s *Store) ReleaseBlockoutClaims(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, groupID, releasedBy uuid.UUID,
	unitIDs []uuid.UUID,
) (domain.BlockoutGroup, error) {
	if len(unitIDs) == 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE inventory_claims
			SET status = 'RELEASED', released_at = NOW(), released_by = $3, updated_at = NOW()
			WHERE property_id = $1 AND blockout_group_id = $2 AND status = 'ACTIVE'
		`, propertyID, groupID, releasedBy); err != nil {
			return domain.BlockoutGroup{}, MapErr(err)
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE inventory_claims
			SET status = 'RELEASED', released_at = NOW(), released_by = $4, updated_at = NOW()
			WHERE property_id = $1 AND blockout_group_id = $2 AND unit_id = ANY($3::uuid[]) AND status = 'ACTIVE'
		`, propertyID, groupID, unitIDs, releasedBy); err != nil {
			return domain.BlockoutGroup{}, MapErr(err)
		}
	}
	if err := s.recomputeBlockoutGroupStatus(ctx, tx, groupID, releasedBy); err != nil {
		return domain.BlockoutGroup{}, err
	}
	return s.GetBlockoutGroup(ctx, tx, propertyID, groupID)
}

type dateRange struct {
	start time.Time
	end   time.Time
}

// subtractDateRanges computes [boundStart, boundEnd) \ union(holes), returning disjoint sorted segments.
func subtractDateRanges(boundStart, boundEnd time.Time, holes []dateRange) []dateRange {
	if !boundEnd.After(boundStart) {
		return nil
	}
	var clipped []dateRange
	for _, h := range holes {
		s := h.start
		if s.Before(boundStart) {
			s = boundStart
		}
		e := h.end
		if e.After(boundEnd) {
			e = boundEnd
		}
		if e.After(s) {
			clipped = append(clipped, dateRange{start: s, end: e})
		}
	}
	if len(clipped) == 0 {
		return []dateRange{{start: boundStart, end: boundEnd}}
	}
	sort.Slice(clipped, func(i, j int) bool {
		if !clipped[i].start.Equal(clipped[j].start) {
			return clipped[i].start.Before(clipped[j].start)
		}
		return clipped[i].end.Before(clipped[j].end)
	})
	var merged []dateRange
	for _, h := range clipped {
		if len(merged) == 0 || h.start.After(merged[len(merged)-1].end) {
			merged = append(merged, h)
		} else if h.end.After(merged[len(merged)-1].end) {
			merged[len(merged)-1].end = h.end
		}
	}

	var out []dateRange
	cursor := boundStart
	for _, h := range merged {
		if cursor.Before(h.start) {
			out = append(out, dateRange{start: cursor, end: h.start})
		}
		if h.end.After(cursor) {
			cursor = h.end
		}
	}
	if cursor.Before(boundEnd) {
		out = append(out, dateRange{start: cursor, end: boundEnd})
	}
	return out
}

// UpdateBlockoutGroupDates safely extends or shortens a blockout/allocation group using three
// explicit inventory categories (CONSUMED, RELEASED, ACTIVE). Existing consumed reservations and
// manually released intervals are never reactivated automatically; only active/newly extended dates
// outside consumed and released ranges become ACTIVE.
func (s *Store) UpdateBlockoutGroupDates(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, groupID uuid.UUID,
	newStart, newEnd time.Time,
	version int,
) (domain.BlockoutGroup, error) {
	var category, channelScope, curStatus, reason string
	var createdBy *uuid.UUID
	var curVersion int
	err := tx.QueryRow(ctx, `
		SELECT category, channel_scope, status, reason, created_by, version
		FROM blockout_groups
		WHERE property_id = $1 AND id = $2
		FOR UPDATE
	`, propertyID, groupID).Scan(&category, &channelScope, &curStatus, &reason, &createdBy, &curVersion)
	if err == pgx.ErrNoRows {
		return domain.BlockoutGroup{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.BlockoutGroup{}, MapErr(err)
	}
	if curVersion != version {
		return domain.BlockoutGroup{}, domain.ErrConflict
	}

	// 1. Lock and inspect all active CONSUMED slices for this group.
	consumedByUnit := make(map[uuid.UUID][]dateRange)
	targetUnitSet := make(map[uuid.UUID]struct{})
	cRows, err := tx.Query(ctx, `
		SELECT unit_id, lower(consumed_range), upper(consumed_range)
		FROM allocation_consumptions
		WHERE property_id = $1 AND blockout_group_id = $2 AND status = 'CONSUMED'
		FOR UPDATE
	`, propertyID, groupID)
	if err != nil {
		return domain.BlockoutGroup{}, MapErr(err)
	}
	for cRows.Next() {
		var uid uuid.UUID
		var cs, ce time.Time
		if err := cRows.Scan(&uid, &cs, &ce); err != nil {
			cRows.Close()
			return domain.BlockoutGroup{}, MapErr(err)
		}
		if cs.Before(newStart) || ce.After(newEnd) {
			cRows.Close()
			return domain.BlockoutGroup{}, domain.NewValidationError(map[string]string{
				"date_range": fmt.Sprintf(
					"new dates (%s to %s) must cover existing consumed reservation (%s to %s)",
					domain.FormatDate(newStart), domain.FormatDate(newEnd),
					domain.FormatDate(cs), domain.FormatDate(ce),
				),
			})
		}
		consumedByUnit[uid] = append(consumedByUnit[uid], dateRange{start: cs, end: ce})
		targetUnitSet[uid] = struct{}{}
	}
	if err := cRows.Err(); err != nil {
		cRows.Close()
		return domain.BlockoutGroup{}, MapErr(err)
	}
	cRows.Close()

	// 2. Lock all manually RELEASED claim intervals so they are strictly preserved and never silently reinstated.
	releasedByUnit := make(map[uuid.UUID][]dateRange)
	rRows, err := tx.Query(ctx, `
		SELECT unit_id, lower(claim_range), upper(claim_range)
		FROM inventory_claims
		WHERE property_id = $1 AND blockout_group_id = $2 AND status = 'RELEASED'
		FOR UPDATE
	`, propertyID, groupID)
	if err != nil {
		return domain.BlockoutGroup{}, MapErr(err)
	}
	for rRows.Next() {
		var uid uuid.UUID
		var rs, re time.Time
		if err := rRows.Scan(&uid, &rs, &re); err != nil {
			rRows.Close()
			return domain.BlockoutGroup{}, MapErr(err)
		}
		releasedByUnit[uid] = append(releasedByUnit[uid], dateRange{start: rs, end: re})
		targetUnitSet[uid] = struct{}{}
	}
	if err := rRows.Err(); err != nil {
		rRows.Close()
		return domain.BlockoutGroup{}, MapErr(err)
	}
	rRows.Close()

	// 3. Lock current ACTIVE claims in this group.
	aRows, err := tx.Query(ctx, `
		SELECT unit_id
		FROM inventory_claims
		WHERE property_id = $1 AND blockout_group_id = $2 AND status = 'ACTIVE'
		FOR UPDATE
	`, propertyID, groupID)
	if err != nil {
		return domain.BlockoutGroup{}, MapErr(err)
	}
	for aRows.Next() {
		var uid uuid.UUID
		if err := aRows.Scan(&uid); err != nil {
			aRows.Close()
			return domain.BlockoutGroup{}, MapErr(err)
		}
		targetUnitSet[uid] = struct{}{}
	}
	if err := aRows.Err(); err != nil {
		aRows.Close()
		return domain.BlockoutGroup{}, MapErr(err)
	}
	aRows.Close()

	if len(targetUnitSet) == 0 {
		return domain.BlockoutGroup{}, domain.ErrConflict
	}

	targetUnits := make([]uuid.UUID, 0, len(targetUnitSet))
	for uid := range targetUnitSet {
		targetUnits = append(targetUnits, uid)
	}
	sort.Slice(targetUnits, func(i, j int) bool {
		return targetUnits[i].String() < targetUnits[j].String()
	})

	// Precompute revised active segments across units using [newStart, newEnd) \ (CONSUMED ∪ RELEASED).
	segmentsByUnit := make(map[uuid.UUID][]dateRange, len(targetUnits))
	totalNewActiveSegments := 0
	for _, uid := range targetUnits {
		holes := make([]dateRange, 0, len(consumedByUnit[uid])+len(releasedByUnit[uid]))
		holes = append(holes, consumedByUnit[uid]...)
		holes = append(holes, releasedByUnit[uid]...)
		segs := subtractDateRanges(newStart, newEnd, holes)
		segmentsByUnit[uid] = segs
		totalNewActiveSegments += len(segs)
	}

	// Refuse empty shortening/no-op edits on an already 100% RELEASED group with zero consumptions.
	if curStatus == domain.BlockStatusReleased && totalNewActiveSegments == 0 && len(consumedByUnit) == 0 {
		return domain.BlockoutGroup{}, domain.ErrConflict
	}

	for _, uid := range targetUnits {
		// Delete only current ACTIVE segments for this unit; CONSUMED and RELEASED rows remain immutable.
		if _, err := tx.Exec(ctx, `
			DELETE FROM inventory_claims
			WHERE property_id = $1 AND blockout_group_id = $2 AND unit_id = $3 AND status = 'ACTIVE'
		`, propertyID, groupID, uid); err != nil {
			return domain.BlockoutGroup{}, MapErr(err)
		}

		for _, seg := range segmentsByUnit[uid] {
			cid, err := NewUUID()
			if err != nil {
				return domain.BlockoutGroup{}, err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO inventory_claims (
					id, property_id, unit_id, claim_type, blockout_group_id, channel_scope,
					claim_range, status, reason, created_by
				) VALUES (
					$1, $2, $3, $4, $5, $6,
					daterange($7::date, $8::date, '[)'), 'ACTIVE', $9, $10
				)
			`, cid, propertyID, uid, category, groupID, channelScope,
				domain.FormatDate(seg.start), domain.FormatDate(seg.end), reason, createdBy,
			); err != nil {
				return domain.BlockoutGroup{}, MapErr(err)
			}
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE blockout_groups
		SET block_range = daterange($3::date, $4::date, '[)'),
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2
	`, propertyID, groupID, domain.FormatDate(newStart), domain.FormatDate(newEnd)); err != nil {
		return domain.BlockoutGroup{}, MapErr(err)
	}

	if err := s.recomputeBlockoutGroupStatus(ctx, tx, groupID, uuid.Nil); err != nil {
		return domain.BlockoutGroup{}, err
	}
	return s.GetBlockoutGroup(ctx, tx, propertyID, groupID)
}

// ReinstateBlockoutClaims explicitly reactivates manually RELEASED claims back to ACTIVE status
// after checking for competing claims/reservations (enforced both pre-check and via GiST constraint).
func (s *Store) ReinstateBlockoutClaims(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, groupID uuid.UUID,
	claimIDs []uuid.UUID,
	unitIDs []uuid.UUID,
) (domain.BlockoutGroup, []uuid.UUID, error) {
	var category, channelScope string
	err := tx.QueryRow(ctx, `
		SELECT category, channel_scope
		FROM blockout_groups
		WHERE property_id = $1 AND id = $2
		FOR UPDATE
	`, propertyID, groupID).Scan(&category, &channelScope)
	if err == pgx.ErrNoRows {
		return domain.BlockoutGroup{}, nil, domain.ErrNotFound
	}
	if err != nil {
		return domain.BlockoutGroup{}, nil, MapErr(err)
	}

	rows, err := tx.Query(ctx, `
		SELECT ic.id, ic.unit_id, u.code, lower(ic.claim_range), upper(ic.claim_range)
		FROM inventory_claims ic
		JOIN units u ON u.id = ic.unit_id
		WHERE ic.property_id = $1
		  AND ic.blockout_group_id = $2
		  AND ic.status = 'RELEASED'
		  AND (COALESCE(cardinality($3::uuid[]), 0) = 0 OR ic.id = ANY($3::uuid[]))
		  AND (COALESCE(cardinality($4::uuid[]), 0) = 0 OR ic.unit_id = ANY($4::uuid[]))
		ORDER BY u.code ASC, lower(ic.claim_range) ASC
		FOR UPDATE OF ic
	`, propertyID, groupID, claimIDs, unitIDs)
	if err != nil {
		return domain.BlockoutGroup{}, nil, MapErr(err)
	}

	type relClaim struct {
		id       uuid.UUID
		unitID   uuid.UUID
		unitCode string
		start    time.Time
		end      time.Time
	}
	var toReinstate []relClaim
	for rows.Next() {
		var rc relClaim
		if err := rows.Scan(&rc.id, &rc.unitID, &rc.unitCode, &rc.start, &rc.end); err != nil {
			rows.Close()
			return domain.BlockoutGroup{}, nil, MapErr(err)
		}
		toReinstate = append(toReinstate, rc)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.BlockoutGroup{}, nil, MapErr(err)
	}
	rows.Close()

	if len(toReinstate) == 0 {
		return domain.BlockoutGroup{}, nil, domain.ErrConflict
	}

	reinstatedIDs := make([]uuid.UUID, 0, len(toReinstate))
	for _, rc := range toReinstate {
		conflicts, err := s.FindClaimConflictsForChannel(
			ctx, tx, propertyID, []uuid.UUID{rc.unitID},
			rc.start, rc.end, domain.ClaimType(category), channelScope, nil, nil,
		)
		if err != nil {
			return domain.BlockoutGroup{}, nil, err
		}
		if len(conflicts) > 0 {
			return domain.BlockoutGroup{}, nil, fmt.Errorf(
				"%w: cannot reinstate released dates on %s (%s to %s) due to active %s",
				domain.ErrOverlap, rc.unitCode, domain.FormatDate(rc.start), domain.FormatDate(rc.end), conflicts[0].ClaimType,
			)
		}
		reinstatedIDs = append(reinstatedIDs, rc.id)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE inventory_claims
		SET status = 'ACTIVE',
		    released_at = NULL,
		    released_by = NULL,
		    updated_at = NOW()
		WHERE property_id = $1 AND blockout_group_id = $2 AND id = ANY($3::uuid[]) AND status = 'RELEASED'
	`, propertyID, groupID, reinstatedIDs); err != nil {
		return domain.BlockoutGroup{}, nil, MapErr(err)
	}

	if err := s.recomputeBlockoutGroupStatus(ctx, tx, groupID, uuid.Nil); err != nil {
		return domain.BlockoutGroup{}, nil, err
	}
	grp, err := s.GetBlockoutGroup(ctx, tx, propertyID, groupID)
	return grp, reinstatedIDs, err
}

func (s *Store) recomputeBlockoutGroupStatus(ctx context.Context, tx pgx.Tx, groupID, actorID uuid.UUID) error {
	var activeCount, consumedCount, releasedCount int
	if err := tx.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM inventory_claims WHERE blockout_group_id = $1 AND status = 'ACTIVE'),
			(SELECT COUNT(*) FROM allocation_consumptions WHERE blockout_group_id = $1 AND status = 'CONSUMED'),
			(SELECT COUNT(*) FROM inventory_claims WHERE blockout_group_id = $1 AND status = 'RELEASED')
	`, groupID).Scan(&activeCount, &consumedCount, &releasedCount); err != nil {
		return MapErr(err)
	}

	var newStatus string
	switch {
	case activeCount > 0 && releasedCount > 0:
		newStatus = domain.BlockStatusPartiallyReleased
	case activeCount > 0 && consumedCount > 0:
		newStatus = domain.BlockStatusPartiallyConsumed
	case activeCount > 0:
		newStatus = domain.BlockStatusActive
	case consumedCount > 0 && releasedCount == 0:
		newStatus = domain.BlockStatusConsumed
	case consumedCount > 0 && releasedCount > 0:
		newStatus = domain.BlockStatusPartiallyReleased
	default:
		newStatus = domain.BlockStatusReleased
	}

	var releasedByPtr *uuid.UUID
	if actorID != uuid.Nil {
		releasedByPtr = &actorID
	}

	_, err := tx.Exec(ctx, `
		UPDATE blockout_groups
		SET status = $2,
		    released_at = CASE
		        WHEN $2 IN ('RELEASED', 'PARTIALLY_RELEASED') THEN COALESCE(released_at, NOW())
		        ELSE NULL
		    END,
		    released_by = CASE
		        WHEN $2 IN ('RELEASED', 'PARTIALLY_RELEASED') THEN COALESCE(released_by, $3::uuid)
		        ELSE NULL
		    END,
		    version = version + 1,
		    updated_at = NOW()
		WHERE id = $1
	`, groupID, newStatus, releasedByPtr)
	return MapErr(err)
}

// InheritFutureUnitBlockouts creates inventory_claims for a newly added or reassigned unit
// from any active PROPERTY or matching UNIT_TYPE blockout_groups with apply_to_future_units = TRUE.
func (s *Store) InheritFutureUnitBlockouts(ctx context.Context, tx pgx.Tx, u domain.Unit) error {
	if u.Status == domain.UnitStatusRetired {
		return nil
	}
	rows, err := tx.Query(ctx, `
		SELECT id, category, channel_scope, lower(block_range), upper(block_range), reason, created_by
		FROM blockout_groups
		WHERE property_id = $1
		  AND status IN ('ACTIVE', 'PARTIALLY_RELEASED', 'PARTIALLY_CONSUMED')
		  AND apply_to_future_units = TRUE
		  AND (
			  scope_type = 'PROPERTY' OR
			  (scope_type = 'UNIT_TYPE' AND target_unit_type_id = $2)
		  )
	`, u.PropertyID, u.UnitTypeID)
	if err != nil {
		return MapErr(err)
	}
	type pendingClaim struct {
		groupID      uuid.UUID
		category     string
		channelScope string
		start        time.Time
		end          time.Time
		reason       string
		createdBy    *uuid.UUID
	}
	var pending []pendingClaim
	for rows.Next() {
		var p pendingClaim
		if err := rows.Scan(&p.groupID, &p.category, &p.channelScope, &p.start, &p.end, &p.reason, &p.createdBy); err != nil {
			rows.Close()
			return MapErr(err)
		}
		pending = append(pending, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return MapErr(err)
	}
	rows.Close()

	for _, p := range pending {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM inventory_claims
				WHERE blockout_group_id = $1 AND unit_id = $2 AND status = 'ACTIVE'
			)
		`, p.groupID, u.ID).Scan(&exists); err != nil {
			return MapErr(err)
		}
		if exists {
			continue
		}
		cid, err := NewUUID()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO inventory_claims (
				id, property_id, unit_id, claim_type, blockout_group_id, channel_scope,
				claim_range, status, reason, created_by
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				daterange($7::date, $8::date, '[)'), 'ACTIVE', $9, $10
			)
		`,
			cid, u.PropertyID, u.ID, p.category, p.groupID, p.channelScope,
			domain.FormatDate(p.start), domain.FormatDate(p.end), p.reason, p.createdBy,
		); err != nil {
			return MapErr(err)
		}
	}
	return nil
}

// ListActiveClaimsInRange returns all active inventory claims overlapping [startDate, endDate).
func (s *Store) ListActiveClaimsInRange(ctx context.Context, propertyID uuid.UUID, startDate, endDate time.Time) ([]domain.InventoryClaim, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ic.id, ic.property_id, ic.unit_id, u.code, u.name,
		       ic.claim_type, ic.reservation_id, COALESCE(r.reference, ''),
		       COALESCE(r.guest_name, ''), COALESCE(r.status, ''),
		       ic.blockout_group_id, ic.channel_scope,
		       lower(ic.claim_range), upper(ic.claim_range),
		       ic.status, ic.consumed_by_reservation_id, ic.reason, ic.created_by, ic.released_by, ic.released_at,
		       ic.created_at, ic.updated_at
		FROM inventory_claims ic
		JOIN units u ON u.id = ic.unit_id
		LEFT JOIN reservations r ON r.id = ic.reservation_id
		WHERE ic.property_id = $1
		  AND ic.status = 'ACTIVE'
		  AND ic.claim_range && daterange($2::date, $3::date, '[)')
		ORDER BY lower(ic.claim_range) ASC, u.code ASC
	`, propertyID, domain.FormatDate(startDate), domain.FormatDate(endDate))
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.InventoryClaim
	for rows.Next() {
		c, err := scanInventoryClaim(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, c)
	}
	return out, MapErr(rows.Err())
}

func scanBlockoutGroup(r scannable) (domain.BlockoutGroup, error) {
	var g domain.BlockoutGroup
	var scope, cat string
	err := r.Scan(
		&g.ID, &g.PropertyID, &scope, &g.TargetUnitTypeID,
		&cat, &g.ChannelScope, &g.StartDate, &g.EndDate,
		&g.Reason, &g.ApplyToFutureUnits, &g.Status,
		&g.ActiveClaimCount, &g.ConsumedClaimCount, &g.ReleasedClaimCount, &g.TotalClaimCount,
		&g.CreatedBy, &g.ReleasedBy, &g.ReleasedAt, &g.Version, &g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		return domain.BlockoutGroup{}, err
	}
	g.ScopeType = domain.BlockoutScope(scope)
	g.Category = domain.ClaimType(cat)
	return g, nil
}

func scanInventoryClaim(r scannable) (domain.InventoryClaim, error) {
	var c domain.InventoryClaim
	var ct, st, resStat string
	err := r.Scan(
		&c.ID, &c.PropertyID, &c.UnitID, &c.UnitCode, &c.UnitName,
		&ct, &c.ReservationID, &c.ReservationRef, &c.GuestName, &resStat,
		&c.BlockoutGroupID, &c.ChannelScope,
		&c.StartDate, &c.EndDate,
		&st, &c.ConsumedByReservationID, &c.Reason, &c.CreatedBy, &c.ReleasedBy, &c.ReleasedAt,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return domain.InventoryClaim{}, err
	}
	c.ClaimType = domain.ClaimType(ct)
	c.Status = domain.ClaimStatus(st)
	c.ReservationStat = domain.ReservationStatus(resStat)
	return c, nil
}
