package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/domain"
)

// --- Booking Channels ---

func (s *Store) ListBookingChannels(ctx context.Context, db DBTX, propertyID uuid.UUID, activeOnly bool) ([]domain.BookingChannel, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT id, property_id, code, name, category, is_active, sort_order, created_at, updated_at
		FROM booking_channels
		WHERE property_id = $1
		  AND ($2::boolean = FALSE OR is_active = TRUE)
		ORDER BY sort_order ASC, name ASC
	`, propertyID, activeOnly)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.BookingChannel
	for rows.Next() {
		var c domain.BookingChannel
		if err := rows.Scan(
			&c.ID, &c.PropertyID, &c.Code, &c.Name, &c.Category,
			&c.IsActive, &c.SortOrder, &c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, c)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) CreateBookingChannel(ctx context.Context, tx pgx.Tx, c domain.BookingChannel) (domain.BookingChannel, error) {
	var out domain.BookingChannel
	err := tx.QueryRow(ctx, `
		INSERT INTO booking_channels (id, property_id, code, name, category, is_active, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, property_id, code, name, category, is_active, sort_order, created_at, updated_at
	`, c.ID, c.PropertyID, c.Code, c.Name, c.Category, c.IsActive, c.SortOrder).Scan(
		&out.ID, &out.PropertyID, &out.Code, &out.Name, &out.Category,
		&out.IsActive, &out.SortOrder, &out.CreatedAt, &out.UpdatedAt,
	)
	return out, MapErr(err)
}

// --- Guests ---

func (s *Store) FindOrCreateGuest(ctx context.Context, tx pgx.Tx, propertyID uuid.UUID, fullName, email, phone string) (domain.Guest, error) {
	normEmail := domain.NormalizeEmail(email)
	normPhone := domain.NormalizePhone(phone)
	firstName, lastName, cleanName := domain.SplitFullName("", "", fullName)
	phone = strings.TrimSpace(phone)

	if normEmail != "" {
		var g domain.Guest
		var status string
		err := tx.QueryRow(ctx, `
			SELECT id, property_id, first_name, last_name, full_name, email, normalized_email,
			       phone, normalized_phone, preferred_contact_method, preferred_language, status,
			       address_json, preferences, notes, merged_into_guest_id, merged_at, anonymized_at,
			       version, created_at, updated_at
			FROM guests
			WHERE property_id = $1
			  AND status NOT IN ('MERGED', 'ANONYMIZED')
			  AND (normalized_email = $2 OR LOWER(email) = $2)
			ORDER BY created_at ASC
			LIMIT 1
		`, propertyID, normEmail).Scan(
			&g.ID, &g.PropertyID, &g.FirstName, &g.LastName, &g.FullName, &g.Email, &g.NormalizedEmail,
			&g.Phone, &g.NormalizedPhone, &g.PreferredContactMethod, &g.PreferredLanguage, &status,
			&g.AddressJSON, &g.Preferences, &g.Notes, &g.MergedIntoGuestID, &g.MergedAt, &g.AnonymizedAt,
			&g.Version, &g.CreatedAt, &g.UpdatedAt,
		)
		if err == nil {
			g.Status = domain.GuestStatus(status)
			return g, nil
		}
		if err != pgx.ErrNoRows {
			return domain.Guest{}, MapErr(err)
		}
	}

	gid, err := NewUUID()
	if err != nil {
		return domain.Guest{}, err
	}
	var out domain.Guest
	var status string
	err = tx.QueryRow(ctx, `
		INSERT INTO guests (
			id, property_id, first_name, last_name, full_name, email, normalized_email, phone, normalized_phone
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, property_id, first_name, last_name, full_name, email, normalized_email,
		          phone, normalized_phone, preferred_contact_method, preferred_language, status,
		          address_json, preferences, notes, merged_into_guest_id, merged_at, anonymized_at,
		          version, created_at, updated_at
	`, gid, propertyID, firstName, lastName, cleanName, normEmail, normEmail, phone, normPhone).Scan(
		&out.ID, &out.PropertyID, &out.FirstName, &out.LastName, &out.FullName, &out.Email, &out.NormalizedEmail,
		&out.Phone, &out.NormalizedPhone, &out.PreferredContactMethod, &out.PreferredLanguage, &status,
		&out.AddressJSON, &out.Preferences, &out.Notes, &out.MergedIntoGuestID, &out.MergedAt, &out.AnonymizedAt,
		&out.Version, &out.CreatedAt, &out.UpdatedAt,
	)
	if err == nil {
		out.Status = domain.GuestStatus(status)
	}
	return out, MapErr(err)
}

// --- Reservations ---

func generateReservationReference(year int) string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return fmt.Sprintf("TB-%d-%s", year, strings.ToUpper(hex.EncodeToString(b)))
}

type ReservationFilter struct {
	Status      string
	ChannelCode string
	StartDate   *time.Time
	EndDate     *time.Time
}

func (s *Store) ListReservations(ctx context.Context, propertyID uuid.UUID, f ReservationFilter) ([]domain.Reservation, error) {
	var startArg, endArg any
	if f.StartDate != nil && f.EndDate != nil {
		startArg = domain.FormatDate(*f.StartDate)
		endArg = domain.FormatDate(*f.EndDate)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, property_id, reference, guest_id, guest_name, guest_email, guest_phone,
		       channel_code, lower(stay_range), upper(stay_range),
		       adults, children, infants, status,
		       payment_status, total_pence, deposit_pence, amount_paid_pence, balance_due_pence, currency,
		       occupant_details, internal_notes, cancellation_reason, cancelled_at,
		       cancellation_requested_at, cancellation_request_note,
		       created_by, updated_by, version, created_at, updated_at
		FROM reservations
		WHERE property_id = $1
		  AND ($2::text = '' OR status = $2)
		  AND ($3::text = '' OR channel_code = $3)
		  AND ($4::date IS NULL OR $5::date IS NULL OR stay_range && daterange($4::date, $5::date, '[)'))
		ORDER BY lower(stay_range) ASC, created_at DESC
	`, propertyID, strings.ToUpper(strings.TrimSpace(f.Status)), strings.ToUpper(strings.TrimSpace(f.ChannelCode)), startArg, endArg)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.Reservation
	var ids []uuid.UUID
	for rows.Next() {
		r, err := scanReservation(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, r)
		ids = append(ids, r.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, MapErr(err)
	}
	if len(ids) > 0 {
		unitMap, err := s.loadReservationUnitsMap(ctx, s.pool, propertyID, ids)
		if err != nil {
			return nil, err
		}
		for i := range out {
			out[i].Units = unitMap[out[i].ID]
			for _, u := range out[i].Units {
				out[i].UnitIDs = append(out[i].UnitIDs, u.UnitID)
			}
		}
	}
	return out, nil
}

func (s *Store) GetReservation(ctx context.Context, db DBTX, propertyID, id uuid.UUID) (domain.Reservation, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT id, property_id, reference, guest_id, guest_name, guest_email, guest_phone,
		       channel_code, lower(stay_range), upper(stay_range),
		       adults, children, infants, status,
		       payment_status, total_pence, deposit_pence, amount_paid_pence, balance_due_pence, currency,
		       occupant_details, internal_notes, cancellation_reason, cancelled_at,
		       cancellation_requested_at, cancellation_request_note,
		       created_by, updated_by, version, created_at, updated_at
		FROM reservations
		WHERE property_id = $1 AND id = $2
	`, propertyID, id)
	r, err := scanReservation(row)
	if err != nil {
		return domain.Reservation{}, MapErr(err)
	}
	unitMap, err := s.loadReservationUnitsMap(ctx, db, propertyID, []uuid.UUID{r.ID})
	if err != nil {
		return domain.Reservation{}, err
	}
	r.Units = unitMap[r.ID]
	for _, u := range r.Units {
		r.UnitIDs = append(r.UnitIDs, u.UnitID)
	}
	return r, nil
}

func (s *Store) CreateReservation(ctx context.Context, tx pgx.Tx, r domain.Reservation) (domain.Reservation, error) {
	if r.Reference == "" {
		r.Reference = generateReservationReference(r.ArrivalDate.Year())
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO reservations (
			id, property_id, reference, guest_id, guest_name, guest_email, guest_phone,
			channel_code, stay_range, adults, children, infants, status,
			occupant_details, internal_notes, created_by, updated_by
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, daterange($9::date, $10::date, '[)'), $11, $12, $13, $14,
			$15, $16, $17, $18
		)
	`,
		r.ID, r.PropertyID, r.Reference, r.GuestID, r.GuestName, r.GuestEmail, r.GuestPhone,
		r.ChannelCode, domain.FormatDate(r.ArrivalDate), domain.FormatDate(r.DepartureDate),
		r.Adults, r.Children, r.Infants, string(r.Status),
		r.OccupantDetails, r.InternalNotes, r.CreatedBy, r.UpdatedBy,
	)
	if err != nil {
		return domain.Reservation{}, MapErr(err)
	}

	if err := s.syncReservationUnitsAndClaims(ctx, tx, r); err != nil {
		return domain.Reservation{}, err
	}
	return s.GetReservation(ctx, tx, r.PropertyID, r.ID)
}

func (s *Store) UpdateReservation(ctx context.Context, tx pgx.Tx, r domain.Reservation) (domain.Reservation, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE reservations
		SET guest_name = $3,
		    guest_email = $4,
		    guest_phone = $5,
		    channel_code = $6,
		    stay_range = daterange($7::date, $8::date, '[)'),
		    adults = $9,
		    children = $10,
		    infants = $11,
		    occupant_details = $12,
		    internal_notes = $13,
		    updated_by = $14,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND version = $15
	`,
		r.PropertyID, r.ID, r.GuestName, r.GuestEmail, r.GuestPhone,
		r.ChannelCode, domain.FormatDate(r.ArrivalDate), domain.FormatDate(r.DepartureDate),
		r.Adults, r.Children, r.Infants, r.OccupantDetails, r.InternalNotes, r.UpdatedBy, r.Version,
	)
	if err != nil {
		return domain.Reservation{}, MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		if _, gerr := s.GetReservation(ctx, tx, r.PropertyID, r.ID); gerr == nil {
			return domain.Reservation{}, domain.ErrConflict
		}
		return domain.Reservation{}, domain.ErrNotFound
	}

	if err := s.syncReservationUnitsAndClaims(ctx, tx, r); err != nil {
		return domain.Reservation{}, err
	}
	return s.GetReservation(ctx, tx, r.PropertyID, r.ID)
}

func (s *Store) TransitionReservationStatus(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, id uuid.UUID,
	nextStatus domain.ReservationStatus,
	reason string,
	actorID uuid.UUID,
	version int,
) (domain.Reservation, error) {
	updated, _, err := s.TransitionReservationStatusWithTasks(ctx, tx, propertyID, id, nextStatus, reason, actorID, version)
	return updated, err
}

func (s *Store) TransitionReservationStatusWithTasks(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, id uuid.UUID,
	nextStatus domain.ReservationStatus,
	reason string,
	actorID uuid.UUID,
	version int,
) (domain.Reservation, []domain.HousekeepingTask, error) {
	var cancelledAt any
	if nextStatus == domain.ResStatusCancelled {
		now := time.Now().UTC()
		cancelledAt = now
	}
	var updatedBy *uuid.UUID
	if actorID != uuid.Nil {
		updatedBy = &actorID
	}
	tag, err := tx.Exec(ctx, `
		UPDATE reservations
		SET status = $3,
		    cancellation_reason = CASE WHEN $4::text <> '' THEN $4 ELSE cancellation_reason END,
		    cancelled_at = COALESCE($5::timestamptz, cancelled_at),
		    updated_by = $6,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND version = $7
	`, propertyID, id, string(nextStatus), strings.TrimSpace(reason), cancelledAt, updatedBy, version)
	if err != nil {
		return domain.Reservation{}, nil, MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		if _, gerr := s.GetReservation(ctx, tx, propertyID, id); gerr == nil {
			return domain.Reservation{}, nil, domain.ErrConflict
		}
		return domain.Reservation{}, nil, domain.ErrNotFound
	}

	updated, err := s.GetReservation(ctx, tx, propertyID, id)
	if err != nil {
		return domain.Reservation{}, nil, err
	}

	var generatedTasks []domain.HousekeepingTask
	if nextStatus.BlocksInventory() {
		if err := s.syncReservationUnitsAndClaims(ctx, tx, updated); err != nil {
			return domain.Reservation{}, nil, err
		}
	} else {
		claimStatus := string(domain.ClaimStatusReleased)
		if nextStatus == domain.ResStatusCheckedOut {
			claimStatus = string(domain.ClaimStatusCompleted)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE inventory_claims
			SET status = $3, released_at = NOW(), released_by = $4, updated_at = NOW()
			WHERE property_id = $1 AND reservation_id = $2 AND status = 'ACTIVE'
		`, propertyID, id, claimStatus, updatedBy); err != nil {
			return domain.Reservation{}, nil, MapErr(err)
		}
		if nextStatus != domain.ResStatusCheckedOut {
			if err := s.restoreConsumedAllocationsForReservation(ctx, tx, propertyID, id, actorID); err != nil {
				return domain.Reservation{}, nil, err
			}
		} else {
			generatedTasks, err = s.ensureCheckoutChangeoverTasksTx(ctx, tx, updated, actorID)
			if err != nil {
				return domain.Reservation{}, nil, err
			}
		}
	}

	finalRes, err := s.GetReservation(ctx, tx, propertyID, id)
	return finalRes, generatedTasks, err
}

func (s *Store) syncReservationUnitsAndClaims(ctx context.Context, tx pgx.Tx, r domain.Reservation) error {
	if _, err := tx.Exec(ctx, `
		DELETE FROM reservation_units WHERE property_id = $1 AND reservation_id = $2
	`, r.PropertyID, r.ID); err != nil {
		return MapErr(err)
	}

	unitAlloc := make(map[uuid.UUID]domain.ReservationUnit, len(r.Units))
	for _, ru := range r.Units {
		unitAlloc[ru.UnitID] = ru
	}
	for _, uid := range r.UnitIDs {
		ruid, err := NewUUID()
		if err != nil {
			return err
		}
		var a, c, inf int
		if ru, ok := unitAlloc[uid]; ok {
			a, c, inf = ru.Adults, ru.Children, ru.Infants
		} else if len(r.UnitIDs) == 1 {
			a, c, inf = r.Adults, r.Children, r.Infants
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO reservation_units (id, property_id, reservation_id, unit_id, adults, children, infants)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, ruid, r.PropertyID, r.ID, uid, a, c, inf); err != nil {
			return MapErr(err)
		}
	}

	// Remove prior active reservation claims and restore any channel allocations previously consumed
	// by this reservation before re-claiming the updated dates, units, or channel atomically.
	if _, err := tx.Exec(ctx, `
		DELETE FROM inventory_claims WHERE property_id = $1 AND reservation_id = $2 AND status = 'ACTIVE'
	`, r.PropertyID, r.ID); err != nil {
		return MapErr(err)
	}

	var actorID uuid.UUID
	if r.UpdatedBy != nil {
		actorID = *r.UpdatedBy
	} else if r.CreatedBy != nil {
		actorID = *r.CreatedBy
	}
	if err := s.restoreConsumedAllocationsForReservation(ctx, tx, r.PropertyID, r.ID, actorID); err != nil {
		return err
	}

	if !r.Status.BlocksInventory() {
		return nil
	}

	startStr := domain.FormatDate(r.ArrivalDate)
	endStr := domain.FormatDate(r.DepartureDate)
	touchedGroups := make(map[uuid.UUID]struct{})

	for _, uid := range r.UnitIDs {
		// 1. Reject if a channel stop-sell restriction actively blocks r.ChannelCode on these dates.
		var restrictionReason string
		err := tx.QueryRow(ctx, `
			SELECT reason
			FROM inventory_claims
			WHERE property_id = $1
			  AND unit_id = $2
			  AND status = 'ACTIVE'
			  AND claim_type = 'CHANNEL_RESTRICTION'
			  AND channel_scope = $3
			  AND claim_range && daterange($4::date, $5::date, '[)')
			LIMIT 1
		`, r.PropertyID, uid, r.ChannelCode, startStr, endStr).Scan(&restrictionReason)
		if err == nil {
			return fmt.Errorf("%w: channel %s stop-sell restriction active (%s)", domain.ErrOverlap, r.ChannelCode, restrictionReason)
		} else if err != pgx.ErrNoRows {
			return MapErr(err)
		}

		// 2. Lock and inspect any active non-restriction overlapping claims.
		type overlappingClaim struct {
			id              uuid.UUID
			claimType       string
			channelScope    string
			blockoutGroupID *uuid.UUID
			startDate       time.Time
			endDate         time.Time
			reason          string
			createdBy       *uuid.UUID
		}
		rows, err := tx.Query(ctx, `
			SELECT id, claim_type, channel_scope, blockout_group_id,
			       lower(claim_range), upper(claim_range), reason, created_by
			FROM inventory_claims
			WHERE property_id = $1
			  AND unit_id = $2
			  AND status = 'ACTIVE'
			  AND claim_type <> 'CHANNEL_RESTRICTION'
			  AND claim_range && daterange($3::date, $4::date, '[)')
			ORDER BY lower(claim_range) ASC
			FOR UPDATE
		`, r.PropertyID, uid, startStr, endStr)
		if err != nil {
			return MapErr(err)
		}
		var overlaps []overlappingClaim
		for rows.Next() {
			var oc overlappingClaim
			if err := rows.Scan(&oc.id, &oc.claimType, &oc.channelScope, &oc.blockoutGroupID, &oc.startDate, &oc.endDate, &oc.reason, &oc.createdBy); err != nil {
				rows.Close()
				return MapErr(err)
			}
			overlaps = append(overlaps, oc)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return MapErr(err)
		}
		rows.Close()

		for _, oc := range overlaps {
			if oc.claimType != string(domain.ClaimChannelAllocation) || oc.channelScope != r.ChannelCode {
				return fmt.Errorf("%w: unit is already claimed by %s (%s to %s)", domain.ErrOverlap, oc.claimType, domain.FormatDate(oc.startDate), domain.FormatDate(oc.endDate))
			}

			// Atomically consume matching CHANNEL_ALLOCATION and split leftover prefix/suffix segments.
			// Never record released_at/released_by on consumed allocations (consumption is not manual release).
			if _, err := tx.Exec(ctx, `
				UPDATE inventory_claims
				SET status = 'CONSUMED',
				    consumed_by_reservation_id = $2,
				    released_at = NULL,
				    released_by = NULL,
				    updated_at = NOW()
				WHERE id = $1 AND status = 'ACTIVE'
			`, oc.id, r.ID); err != nil {
				return MapErr(err)
			}
			if oc.blockoutGroupID != nil {
				consumedStart := oc.startDate
				if r.ArrivalDate.After(consumedStart) {
					consumedStart = r.ArrivalDate
				}
				consumedEnd := oc.endDate
				if r.DepartureDate.Before(consumedEnd) {
					consumedEnd = r.DepartureDate
				}
				if consumedEnd.After(consumedStart) {
					acID, err := NewUUID()
					if err != nil {
						return err
					}
					if _, err := tx.Exec(ctx, `
						INSERT INTO allocation_consumptions (
							id, property_id, blockout_group_id, source_claim_id, reservation_id, unit_id,
							channel_scope, consumed_range, status
						) VALUES (
							$1, $2, $3, $4, $5, $6,
							$7, daterange($8::date, $9::date, '[)'), 'CONSUMED'
						)
					`, acID, r.PropertyID, *oc.blockoutGroupID, oc.id, r.ID, uid,
						oc.channelScope, domain.FormatDate(consumedStart), domain.FormatDate(consumedEnd),
					); err != nil {
						return MapErr(err)
					}
				}
				touchedGroups[*oc.blockoutGroupID] = struct{}{}
			}
			if oc.startDate.Before(r.ArrivalDate) {
				remID, err := NewUUID()
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO inventory_claims (
						id, property_id, unit_id, claim_type, blockout_group_id, channel_scope,
						claim_range, status, reason, created_by
					) VALUES (
						$1, $2, $3, 'CHANNEL_ALLOCATION', $4, $5,
						daterange($6::date, $7::date, '[)'), 'ACTIVE', $8, $9
					)
				`, remID, r.PropertyID, uid, oc.blockoutGroupID, oc.channelScope,
					domain.FormatDate(oc.startDate), startStr, oc.reason, oc.createdBy,
				); err != nil {
					return MapErr(err)
				}
			}
			if r.DepartureDate.Before(oc.endDate) {
				remID, err := NewUUID()
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO inventory_claims (
						id, property_id, unit_id, claim_type, blockout_group_id, channel_scope,
						claim_range, status, reason, created_by
					) VALUES (
						$1, $2, $3, 'CHANNEL_ALLOCATION', $4, $5,
						daterange($6::date, $7::date, '[)'), 'ACTIVE', $8, $9
					)
				`, remID, r.PropertyID, uid, oc.blockoutGroupID, oc.channelScope,
					endStr, domain.FormatDate(oc.endDate), oc.reason, oc.createdBy,
				); err != nil {
					return MapErr(err)
				}
			}
		}
	}

	for grpID := range touchedGroups {
		if err := s.recomputeBlockoutGroupStatus(ctx, tx, grpID, uuid.Nil); err != nil {
			return err
		}
	}

	claimType := r.Status.ClaimType()
	for _, uid := range r.UnitIDs {
		cid, err := NewUUID()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO inventory_claims (
				id, property_id, unit_id, claim_type, reservation_id, channel_scope,
				claim_range, status, reason, created_by
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				daterange($7::date, $8::date, '[)'), 'ACTIVE', $9, $10
			)
		`,
			cid, r.PropertyID, uid, string(claimType), r.ID, r.ChannelCode,
			startStr, endStr,
			fmt.Sprintf("Reservation %s (%s)", r.Reference, r.GuestName), r.UpdatedBy,
		); err != nil {
			return MapErr(err)
		}
	}
	return nil
}

// restoreConsumedAllocationsForReservation returns previously consumed CHANNEL_ALLOCATION slices
// back to their originating blockout_group_id unless that allocation/unit remainder was already manually released.
func (s *Store) restoreConsumedAllocationsForReservation(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, reservationID, actorID uuid.UUID,
) error {
	type consumptionRow struct {
		id              uuid.UUID
		blockoutGroupID uuid.UUID
		unitID          uuid.UUID
		channelScope    string
		startDate       time.Time
		endDate         time.Time
	}

	rows, err := tx.Query(ctx, `
		SELECT id, blockout_group_id, unit_id, channel_scope, lower(consumed_range), upper(consumed_range)
		FROM allocation_consumptions
		WHERE property_id = $1 AND reservation_id = $2 AND status = 'CONSUMED'
		ORDER BY unit_id ASC, lower(consumed_range) ASC
		FOR UPDATE
	`, propertyID, reservationID)
	if err != nil {
		return MapErr(err)
	}
	var pending []consumptionRow
	for rows.Next() {
		var row consumptionRow
		if err := rows.Scan(&row.id, &row.blockoutGroupID, &row.unitID, &row.channelScope, &row.startDate, &row.endDate); err != nil {
			rows.Close()
			return MapErr(err)
		}
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return MapErr(err)
	}
	rows.Close()

	if len(pending) == 0 {
		return nil
	}

	var actorPtr *uuid.UUID
	if actorID != uuid.Nil {
		actorPtr = &actorID
	}
	touchedGroups := make(map[uuid.UUID]struct{})

	for _, c := range pending {
		var grpStatus, grpChannel, grpReason string
		var grpStart, grpEnd time.Time
		var grpCreatedBy *uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT status, channel_scope, lower(block_range), upper(block_range), reason, created_by
			FROM blockout_groups
			WHERE property_id = $1 AND id = $2
			FOR UPDATE
		`, propertyID, c.blockoutGroupID).Scan(&grpStatus, &grpChannel, &grpStart, &grpEnd, &grpReason, &grpCreatedBy)
		if err == pgx.ErrNoRows {
			if _, uerr := tx.Exec(ctx, `
				UPDATE allocation_consumptions
				SET status = 'DETACHED', restored_at = NOW(), restored_by = $2, updated_at = NOW()
				WHERE id = $1
			`, c.id, actorPtr); uerr != nil {
				return MapErr(uerr)
			}
			continue
		} else if err != nil {
			return MapErr(err)
		}

		var activeInGroup, releasedOnUnit int
		if err := tx.QueryRow(ctx, `
			SELECT
				COUNT(*) FILTER (WHERE status = 'ACTIVE'),
				COUNT(*) FILTER (WHERE status = 'RELEASED' AND unit_id = $3)
			FROM inventory_claims
			WHERE property_id = $1 AND blockout_group_id = $2
		`, propertyID, c.blockoutGroupID, c.unitID).Scan(&activeInGroup, &releasedOnUnit); err != nil {
			return MapErr(err)
		}

		manuallyReleased := grpStatus == domain.BlockStatusReleased ||
			releasedOnUnit > 0 ||
			(grpStatus == domain.BlockStatusPartiallyReleased && activeInGroup == 0)

		restoreStart := c.startDate
		if grpStart.After(restoreStart) {
			restoreStart = grpStart
		}
		restoreEnd := c.endDate
		if grpEnd.Before(restoreEnd) {
			restoreEnd = grpEnd
		}

		if manuallyReleased || !restoreEnd.After(restoreStart) {
			if _, err := tx.Exec(ctx, `
				UPDATE allocation_consumptions
				SET status = 'DETACHED', restored_at = NOW(), restored_by = $2, updated_at = NOW()
				WHERE id = $1
			`, c.id, actorPtr); err != nil {
				return MapErr(err)
			}
			continue
		}

		// Coalesce restored range with any adjacent/overlapping active claims belonging to the same group & unit.
		adjRows, err := tx.Query(ctx, `
			SELECT id, lower(claim_range), upper(claim_range)
			FROM inventory_claims
			WHERE property_id = $1
			  AND blockout_group_id = $2
			  AND unit_id = $3
			  AND status = 'ACTIVE'
			  AND lower(claim_range) <= $5::date
			  AND upper(claim_range) >= $4::date
			FOR UPDATE
		`, propertyID, c.blockoutGroupID, c.unitID, domain.FormatDate(restoreStart), domain.FormatDate(restoreEnd))
		if err != nil {
			return MapErr(err)
		}
		mergedStart := restoreStart
		mergedEnd := restoreEnd
		var adjIDs []uuid.UUID
		for adjRows.Next() {
			var aid uuid.UUID
			var as, ae time.Time
			if err := adjRows.Scan(&aid, &as, &ae); err != nil {
				adjRows.Close()
				return MapErr(err)
			}
			adjIDs = append(adjIDs, aid)
			if as.Before(mergedStart) {
				mergedStart = as
			}
			if ae.After(mergedEnd) {
				mergedEnd = ae
			}
		}
		if err := adjRows.Err(); err != nil {
			adjRows.Close()
			return MapErr(err)
		}
		adjRows.Close()

		if len(adjIDs) > 0 {
			if _, err := tx.Exec(ctx, `
				DELETE FROM inventory_claims WHERE id = ANY($1::uuid[])
			`, adjIDs); err != nil {
				return MapErr(err)
			}
		}

		newClaimID, err := NewUUID()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO inventory_claims (
				id, property_id, unit_id, claim_type, blockout_group_id, channel_scope,
				claim_range, status, reason, created_by
			) VALUES (
				$1, $2, $3, 'CHANNEL_ALLOCATION', $4, $5,
				daterange($6::date, $7::date, '[)'), 'ACTIVE', $8, $9
			)
		`, newClaimID, propertyID, c.unitID, c.blockoutGroupID, grpChannel,
			domain.FormatDate(mergedStart), domain.FormatDate(mergedEnd), grpReason, grpCreatedBy,
		); err != nil {
			return MapErr(err)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE allocation_consumptions
			SET status = 'RESTORED', restored_at = NOW(), restored_by = $2, updated_at = NOW()
			WHERE id = $1
		`, c.id, actorPtr); err != nil {
			return MapErr(err)
		}
		touchedGroups[c.blockoutGroupID] = struct{}{}
	}

	for grpID := range touchedGroups {
		if err := s.recomputeBlockoutGroupStatus(ctx, tx, grpID, uuid.Nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) loadReservationUnitsMap(ctx context.Context, db DBTX, propertyID uuid.UUID, resIDs []uuid.UUID) (map[uuid.UUID][]domain.ReservationUnit, error) {
	rows, err := db.Query(ctx, `
		SELECT ru.reservation_id, ru.unit_id, u.code, u.name, ut.name, ru.adults, ru.children, ru.infants
		FROM reservation_units ru
		JOIN units u ON u.id = ru.unit_id
		JOIN unit_types ut ON ut.id = u.unit_type_id
		WHERE ru.property_id = $1 AND ru.reservation_id = ANY($2::uuid[])
		ORDER BY u.sort_order ASC, u.code ASC
	`, propertyID, resIDs)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	out := make(map[uuid.UUID][]domain.ReservationUnit, len(resIDs))
	for rows.Next() {
		var resID uuid.UUID
		var ru domain.ReservationUnit
		if err := rows.Scan(&resID, &ru.UnitID, &ru.UnitCode, &ru.UnitName, &ru.UnitTypeName, &ru.Adults, &ru.Children, &ru.Infants); err != nil {
			return nil, MapErr(err)
		}
		out[resID] = append(out[resID], ru)
	}
	return out, MapErr(rows.Err())
}

func scanReservation(r scannable) (domain.Reservation, error) {
	var res domain.Reservation
	var status string
	err := r.Scan(
		&res.ID, &res.PropertyID, &res.Reference, &res.GuestID, &res.GuestName, &res.GuestEmail, &res.GuestPhone,
		&res.ChannelCode, &res.ArrivalDate, &res.DepartureDate,
		&res.Adults, &res.Children, &res.Infants, &status,
		&res.PaymentStatus, &res.TotalPence, &res.DepositPence, &res.AmountPaidPence, &res.BalanceDuePence, &res.Currency,
		&res.OccupantDetails, &res.InternalNotes, &res.CancellationReason, &res.CancelledAt,
		&res.CancellationRequestedAt, &res.CancellationRequestNote,
		&res.CreatedBy, &res.UpdatedBy, &res.Version, &res.CreatedAt, &res.UpdatedAt,
	)
	if err != nil {
		return domain.Reservation{}, err
	}
	res.Status = domain.ReservationStatus(status)
	return res, nil
}
