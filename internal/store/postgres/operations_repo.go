package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Prestwickuk/lovelace/internal/domain"
)

// OperationsUnitRow holds a unit with its type metadata and stored housekeeping readiness.
type OperationsUnitRow struct {
	Unit               domain.Unit
	HousekeepingStage  domain.ChangeoverStage
	HousekeepingUpdate time.Time
}

// OperationsClaimRow holds an active inventory claim overlapping the operational date.
type OperationsClaimRow struct {
	UnitID        uuid.UUID
	ClaimType     domain.ClaimType
	ReservationID *uuid.UUID
	ChannelScope  string
	Reason        string
	StartDate     time.Time
	EndDate       time.Time
}

// OperationsStayRow holds a reservation plus unit allocations and hold special requests.
type OperationsStayRow struct {
	Reservation     domain.Reservation
	SpecialRequests string
}

// OperationsPaymentAnomalyRow captures unresolved payment attempts or webhook errors.
type OperationsPaymentAnomalyRow struct {
	ID            uuid.UUID
	Kind          string // PAYMENT_ATTEMPT_FAILED | WEBHOOK_REJECTED | WEBHOOK_FAILED
	Reference     string
	ReservationID *uuid.UUID
	AmountPence   int64
	Currency      string
	Detail        string
	CreatedAt     time.Time
}

// ensureCheckoutChangeoverTasksTx idempotently generates CHANGEOVER_CLEAN housekeeping tasks
// for all units attached to a reservation upon CHECKED_OUT and moves unit housekeeping_status
// to CLEANING_REQUIRED. Never marks a unit READY and never modifies inventory_claims/blocks.
func (s *Store) ensureCheckoutChangeoverTasksTx(
	ctx context.Context,
	tx pgx.Tx,
	res domain.Reservation,
	actorID uuid.UUID,
) ([]domain.HousekeepingTask, error) {
	var tzName, checkInTimeStr string
	if err := tx.QueryRow(ctx, `
		SELECT timezone, check_in_time::text
		FROM properties
		WHERE id = $1
	`, res.PropertyID).Scan(&tzName, &checkInTimeStr); err != nil {
		return nil, MapErr(err)
	}

	loc, err := time.LoadLocation(strings.TrimSpace(tzName))
	if err != nil {
		loc = time.UTC
	}
	nowLocal := time.Now().In(loc)
	scheduledDate := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, time.UTC)
	scheduledStr := domain.FormatDate(scheduledDate)

	var dueAt *time.Time
	if parsedTime, perr := time.Parse("15:04:05", checkInTimeStr); perr == nil {
		dt := time.Date(scheduledDate.Year(), scheduledDate.Month(), scheduledDate.Day(), parsedTime.Hour(), parsedTime.Minute(), 0, 0, loc).UTC()
		dueAt = &dt
	} else if parsedTime, perr := time.Parse("15:04", checkInTimeStr); perr == nil {
		dt := time.Date(scheduledDate.Year(), scheduledDate.Month(), scheduledDate.Day(), parsedTime.Hour(), parsedTime.Minute(), 0, 0, loc).UTC()
		dueAt = &dt
	}

	unitIDs := res.UnitIDs
	if len(unitIDs) == 0 && len(res.Units) > 0 {
		for _, ru := range res.Units {
			unitIDs = append(unitIDs, ru.UnitID)
		}
	}

	var actorPtr *uuid.UUID
	if actorID != uuid.Nil {
		actorPtr = &actorID
	}

	var createdTasks []domain.HousekeepingTask
	for _, uid := range unitIDs {
		var nextArrRef string
		err := tx.QueryRow(ctx, `
			SELECT r.reference
			FROM reservations r
			JOIN reservation_units ru ON ru.reservation_id = r.id
			WHERE r.property_id = $1
			  AND ru.unit_id = $2
			  AND r.id <> $3
			  AND r.status IN ('CONFIRMED', 'PENDING', 'CHECKED_IN')
			  AND lower(r.stay_range) = $4::date
			ORDER BY r.created_at ASC
			LIMIT 1
		`, res.PropertyID, uid, res.ID, scheduledStr).Scan(&nextArrRef)
		priority := domain.TaskPriorityNormal
		notes := fmt.Sprintf("Generated automatically on departure of %s (%s).", res.Reference, res.GuestName)
		if err == nil {
			priority = domain.TaskPriorityUrgent
			notes = fmt.Sprintf("URGENT same-day turnaround: %s departed; next arrival %s expected today.", res.Reference, nextArrRef)
		} else if err != pgx.ErrNoRows {
			return nil, MapErr(err)
		}

		tid, err := NewUUID()
		if err != nil {
			return nil, err
		}
		var insertedID uuid.UUID
		insErr := tx.QueryRow(ctx, `
			INSERT INTO housekeeping_tasks (
				id, property_id, unit_id, reservation_id, task_type, stage, priority,
				scheduled_date, due_at, title, notes, created_by, updated_by
			) VALUES (
				$1, $2, $3, $4, 'CHANGEOVER_CLEAN', 'CLEANING_REQUIRED', $5,
				$6::date, $7, $8, $9, $10, $10
			)
			ON CONFLICT (reservation_id, unit_id, task_type)
			WHERE reservation_id IS NOT NULL AND task_type = 'CHANGEOVER_CLEAN'
			DO NOTHING
			RETURNING id
		`,
			tid, res.PropertyID, uid, res.ID, string(priority),
			scheduledStr, dueAt,
			fmt.Sprintf("Departure changeover (%s)", res.Reference),
			notes, actorPtr,
		).Scan(&insertedID)
		if insErr != nil && insErr != pgx.ErrNoRows {
			return nil, MapErr(insErr)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE units
			SET housekeeping_status = 'CLEANING_REQUIRED',
			    housekeeping_updated_at = NOW(),
			    housekeeping_updated_by = $3,
			    updated_at = NOW()
			WHERE property_id = $1 AND id = $2 AND archived_at IS NULL
		`, res.PropertyID, uid, actorPtr); err != nil {
			return nil, MapErr(err)
		}

		if insertedID != uuid.Nil {
			task, err := s.GetHousekeepingTask(ctx, tx, res.PropertyID, insertedID)
			if err != nil {
				return nil, err
			}
			createdTasks = append(createdTasks, task)
		}
	}
	return createdTasks, nil
}

func (s *Store) GetHousekeepingTask(ctx context.Context, db DBTX, propertyID, taskID uuid.UUID) (domain.HousekeepingTask, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT t.id, t.property_id, t.unit_id, u.code, u.name, ut.name, u.zone, u.status,
		       t.reservation_id, COALESCE(r.reference, ''), COALESCE(r.guest_name, ''),
		       t.task_type, t.stage, t.priority, t.scheduled_date, t.due_at,
		       t.assigned_to, COALESCE(au.full_name, ''),
		       t.title, t.notes, t.started_at, t.started_by, COALESCE(sau.full_name, ''),
		       t.inspected_at, t.inspected_by, COALESCE(iau.full_name, ''), t.completed_at, t.completed_by,
		       t.version, t.created_at, t.updated_at
		FROM housekeeping_tasks t
		JOIN units u ON u.id = t.unit_id
		JOIN unit_types ut ON ut.id = u.unit_type_id
		LEFT JOIN reservations r ON r.id = t.reservation_id
		LEFT JOIN admin_users au ON au.id = t.assigned_to
		LEFT JOIN admin_users sau ON sau.id = t.started_by
		LEFT JOIN admin_users iau ON iau.id = t.inspected_by
		WHERE t.property_id = $1 AND t.id = $2
	`, propertyID, taskID)
	t, err := scanHousekeepingTask(row)
	return t, MapErr(err)
}

func (s *Store) CreateHousekeepingTask(ctx context.Context, tx pgx.Tx, t domain.HousekeepingTask, actorID uuid.UUID) (domain.HousekeepingTask, error) {
	var actorPtr *uuid.UUID
	if actorID != uuid.Nil {
		actorPtr = &actorID
	}
	var dueAt *time.Time
	if t.DueAt != nil {
		dueAt = t.DueAt
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO housekeeping_tasks (
			id, property_id, unit_id, reservation_id, task_type, stage, priority,
			scheduled_date, due_at, assigned_to, title, notes, created_by, updated_by
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8::date, $9, $10, $11, $12, $13, $13
		)
	`,
		t.ID, t.PropertyID, t.UnitID, t.ReservationID, string(t.TaskType), string(t.Stage), string(t.Priority),
		domain.FormatDate(t.ScheduledDate), dueAt, t.AssignedTo, t.Title, t.Notes, actorPtr,
	)
	if err != nil {
		return domain.HousekeepingTask{}, MapErr(err)
	}
	if err := s.syncUnitHousekeepingStatusTx(ctx, tx, t.PropertyID, t.UnitID, actorPtr); err != nil {
		return domain.HousekeepingTask{}, err
	}
	return s.GetHousekeepingTask(ctx, tx, t.PropertyID, t.ID)
}

func (s *Store) TransitionHousekeepingTaskStage(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, taskID uuid.UUID,
	nextStage domain.ChangeoverStage,
	notes string,
	actorID uuid.UUID,
	version int,
) (domain.HousekeepingTask, error) {
	var currentStageStr string
	var unitID uuid.UUID
	var curVersion int
	err := tx.QueryRow(ctx, `
		SELECT stage, unit_id, version
		FROM housekeeping_tasks
		WHERE property_id = $1 AND id = $2
		FOR UPDATE
	`, propertyID, taskID).Scan(&currentStageStr, &unitID, &curVersion)
	if err != nil {
		return domain.HousekeepingTask{}, MapErr(err)
	}
	if curVersion != version {
		return domain.HousekeepingTask{}, domain.ErrConflict
	}
	currentStage := domain.ChangeoverStage(currentStageStr)
	if !currentStage.CanTransitionTo(nextStage) {
		return domain.HousekeepingTask{}, domain.NewValidationError(map[string]string{
			"stage": fmt.Sprintf("transition from %s to %s is not permitted", currentStage, nextStage),
		})
	}

	var actorPtr *uuid.UUID
	if actorID != uuid.Nil {
		actorPtr = &actorID
	}
	trimmedNotes := strings.TrimSpace(notes)

	_, err = tx.Exec(ctx, `
		UPDATE housekeeping_tasks
		SET stage = $3,
		    notes = CASE WHEN $4 <> '' THEN $4 ELSE notes END,
		    started_at = CASE
		        WHEN $3 = 'CLEANING_REQUIRED' THEN NULL
		        WHEN $3 = 'CLEANING_IN_PROGRESS' AND started_at IS NULL THEN NOW()
		        ELSE started_at
		    END,
		    started_by = CASE
		        WHEN $3 = 'CLEANING_REQUIRED' THEN NULL
		        WHEN $3 = 'CLEANING_IN_PROGRESS' AND started_by IS NULL THEN $5
		        ELSE started_by
		    END,
		    inspected_at = CASE WHEN $3 = 'READY' THEN NOW() ELSE NULL END,
		    inspected_by = CASE WHEN $3 = 'READY' THEN $5 ELSE NULL END,
		    completed_at = CASE WHEN $3 = 'READY' THEN NOW() ELSE NULL END,
		    completed_by = CASE WHEN $3 = 'READY' THEN $5 ELSE NULL END,
		    updated_by = $5,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND version = $6
	`, propertyID, taskID, string(nextStage), trimmedNotes, actorPtr, version)
	if err != nil {
		return domain.HousekeepingTask{}, MapErr(err)
	}

	if err := s.syncUnitHousekeepingStatusTx(ctx, tx, propertyID, unitID, actorPtr); err != nil {
		return domain.HousekeepingTask{}, err
	}
	return s.GetHousekeepingTask(ctx, tx, propertyID, taskID)
}

func (s *Store) AssignHousekeepingTask(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, taskID uuid.UUID,
	assigneeID *uuid.UUID,
	actorID uuid.UUID,
	version int,
) (domain.HousekeepingTask, error) {
	var actorPtr *uuid.UUID
	if actorID != uuid.Nil {
		actorPtr = &actorID
	}
	tag, err := tx.Exec(ctx, `
		UPDATE housekeeping_tasks
		SET assigned_to = $3,
		    updated_by = $4,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND version = $5
	`, propertyID, taskID, assigneeID, actorPtr, version)
	if err != nil {
		return domain.HousekeepingTask{}, MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		if _, gerr := s.GetHousekeepingTask(ctx, tx, propertyID, taskID); gerr == nil {
			return domain.HousekeepingTask{}, domain.ErrConflict
		}
		return domain.HousekeepingTask{}, domain.ErrNotFound
	}
	return s.GetHousekeepingTask(ctx, tx, propertyID, taskID)
}

// syncUnitHousekeepingStatusTx updates units.housekeeping_status based purely on open housekeeping
// tasks for that unit. If any non-READY task exists, the unit stays in the most urgent active stage
// (never premature READY). It intentionally never modifies units.status or inventory_claims.
func (s *Store) syncUnitHousekeepingStatusTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, unitID uuid.UUID,
	actorPtr *uuid.UUID,
) error {
	var nextUnitStage string
	err := tx.QueryRow(ctx, `
		SELECT stage
		FROM housekeeping_tasks
		WHERE property_id = $1
		  AND unit_id = $2
		  AND stage <> 'READY'
		ORDER BY
			CASE stage
				WHEN 'CLEANING_REQUIRED' THEN 1
				WHEN 'CLEANING_IN_PROGRESS' THEN 2
				WHEN 'INSPECTION_REQUIRED' THEN 3
				WHEN 'GUEST_DEPARTING' THEN 4
				ELSE 5
			END ASC,
			scheduled_date ASC,
			created_at ASC
		LIMIT 1
	`, propertyID, unitID).Scan(&nextUnitStage)
	if err == pgx.ErrNoRows {
		nextUnitStage = string(domain.StageReady)
	} else if err != nil {
		return MapErr(err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE units
		SET housekeeping_status = $3,
		    housekeeping_updated_at = NOW(),
		    housekeeping_updated_by = $4,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND archived_at IS NULL
	`, propertyID, unitID, nextUnitStage, actorPtr)
	return MapErr(err)
}

func (s *Store) ListHousekeepingTasksForDate(ctx context.Context, propertyID uuid.UUID, opDate time.Time) ([]domain.HousekeepingTask, error) {
	opStr := domain.FormatDate(opDate)
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.property_id, t.unit_id, u.code, u.name, ut.name, u.zone, u.status,
		       t.reservation_id, COALESCE(r.reference, ''), COALESCE(r.guest_name, ''),
		       t.task_type, t.stage, t.priority, t.scheduled_date, t.due_at,
		       t.assigned_to, COALESCE(au.full_name, ''),
		       t.title, t.notes, t.started_at, t.started_by, COALESCE(sau.full_name, ''),
		       t.inspected_at, t.inspected_by, COALESCE(iau.full_name, ''), t.completed_at, t.completed_by,
		       t.version, t.created_at, t.updated_at
		FROM housekeeping_tasks t
		JOIN units u ON u.id = t.unit_id
		JOIN unit_types ut ON ut.id = u.unit_type_id
		LEFT JOIN reservations r ON r.id = t.reservation_id
		LEFT JOIN admin_users au ON au.id = t.assigned_to
		LEFT JOIN admin_users sau ON sau.id = t.started_by
		LEFT JOIN admin_users iau ON iau.id = t.inspected_by
		WHERE t.property_id = $1
		  AND u.archived_at IS NULL
		  AND (
		      t.scheduled_date = $2::date
		      OR (t.stage <> 'READY' AND t.scheduled_date <= $2::date)
		  )
		ORDER BY
			CASE t.priority
				WHEN 'URGENT' THEN 1
				WHEN 'HIGH' THEN 2
				WHEN 'NORMAL' THEN 3
				ELSE 4
			END ASC,
			t.scheduled_date ASC,
			u.sort_order ASC,
			u.code ASC
	`, propertyID, opStr)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.HousekeepingTask
	for rows.Next() {
		t, err := scanHousekeepingTask(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, t)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) ListOperationsUnits(ctx context.Context, propertyID uuid.UUID) ([]OperationsUnitRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, u.property_id, u.unit_type_id, ut.name, u.code, u.name, u.zone,
		       u.status, u.access_notes, u.operational_notes, u.attributes, u.sort_order,
		       u.version, u.archived_at, u.created_at, u.updated_at,
		       u.housekeeping_status, u.housekeeping_updated_at
		FROM units u
		JOIN unit_types ut ON ut.id = u.unit_type_id
		WHERE u.property_id = $1
		  AND u.archived_at IS NULL
		  AND u.status NOT IN ('retired', 'inactive')
		ORDER BY u.sort_order ASC, u.code ASC
	`, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []OperationsUnitRow
	for rows.Next() {
		var u domain.Unit
		var status, hkStage string
		var hkUpdated time.Time
		if err := rows.Scan(
			&u.ID, &u.PropertyID, &u.UnitTypeID, &u.UnitTypeName, &u.Code, &u.Name, &u.Zone,
			&status, &u.AccessNotes, &u.OperationalNotes, &u.Attributes, &u.SortOrder,
			&u.Version, &u.ArchivedAt, &u.CreatedAt, &u.UpdatedAt,
			&hkStage, &hkUpdated,
		); err != nil {
			return nil, MapErr(err)
		}
		u.Status = domain.UnitStatus(status)
		out = append(out, OperationsUnitRow{
			Unit:               u,
			HousekeepingStage:  domain.ChangeoverStage(hkStage),
			HousekeepingUpdate: hkUpdated,
		})
	}
	return out, MapErr(rows.Err())
}

func (s *Store) ListActiveClaimsForDate(ctx context.Context, propertyID uuid.UUID, opDate time.Time) ([]OperationsClaimRow, error) {
	startStr := domain.FormatDate(opDate)
	endStr := domain.FormatDate(opDate.AddDate(0, 0, 1))
	rows, err := s.pool.Query(ctx, `
		SELECT unit_id, claim_type, reservation_id, channel_scope, reason,
		       lower(claim_range) AS start_date, upper(claim_range) AS end_date
		FROM inventory_claims
		WHERE property_id = $1
		  AND status = 'ACTIVE'
		  AND claim_range && daterange($2::date, $3::date, '[)')
		ORDER BY unit_id ASC, created_at ASC
	`, propertyID, startStr, endStr)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []OperationsClaimRow
	for rows.Next() {
		var c OperationsClaimRow
		var ct string
		if err := rows.Scan(&c.UnitID, &ct, &c.ReservationID, &c.ChannelScope, &c.Reason, &c.StartDate, &c.EndDate); err != nil {
			return nil, MapErr(err)
		}
		c.ClaimType = domain.ClaimType(ct)
		out = append(out, c)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) ListOperationsRelevantStays(ctx context.Context, propertyID uuid.UUID, opDate, todayDate time.Time) ([]OperationsStayRow, error) {
	opStr := domain.FormatDate(opDate)
	opNextStr := domain.FormatDate(opDate.AddDate(0, 0, 1))
	todayStr := domain.FormatDate(todayDate)

	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.property_id, r.reference, r.guest_id, r.guest_name, r.guest_email, r.guest_phone,
		       r.channel_code, lower(r.stay_range) AS arrival_date, upper(r.stay_range) AS departure_date,
		       r.adults, r.children, r.infants, r.status,
		       r.payment_status, r.total_pence, r.deposit_pence, r.amount_paid_pence, r.balance_due_pence, r.currency,
		       r.internal_notes, r.cancellation_reason, r.cancelled_at,
		       r.cancellation_requested_at, r.cancellation_request_note,
		       r.created_by, r.updated_by, r.version, r.created_at, r.updated_at,
		       COALESCE(bh.special_requests, '') AS special_requests
		FROM reservations r
		LEFT JOIN booking_holds bh ON bh.id = r.origin_hold_id
		WHERE r.property_id = $1
		  AND (
		      -- Active or departing stays around the selected operational date
		      r.stay_range && daterange($2::date, $3::date, '[)')
		      OR upper(r.stay_range) = $2::date
		      -- Upcoming or open unconfirmed inquiries and provisional pending holds
		      OR (r.status IN ('INQUIRY', 'PENDING') AND lower(r.stay_range) >= ($2::date - 7) AND lower(r.stay_range) <= ($2::date + 14))
		      -- Overdue missed arrivals / late departures relative to real today
		      OR (r.status IN ('CONFIRMED', 'PENDING') AND lower(r.stay_range) < $4::date)
		      OR (r.status = 'CHECKED_IN' AND upper(r.stay_range) < $4::date)
		      -- Unresolved operational attention items (cancellation requests, failed payments, open balances)
		      OR (r.cancellation_requested_at IS NOT NULL AND r.status NOT IN ('CANCELLED', 'NO_SHOW', 'CHECKED_OUT'))
		      OR (r.payment_status = 'FAILED' AND r.status NOT IN ('CANCELLED', 'NO_SHOW'))
		      OR (r.balance_due_pence > 0 AND r.payment_status IN ('UNPAID', 'PARTIALLY_PAID', 'FAILED') AND r.status NOT IN ('CANCELLED', 'NO_SHOW', 'CHECKED_OUT'))
		  )
		ORDER BY lower(r.stay_range) ASC, r.created_at ASC
	`, propertyID, opStr, opNextStr, todayStr)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []OperationsStayRow
	var ids []uuid.UUID
	for rows.Next() {
		var r domain.Reservation
		var status string
		var specialReq string
		if err := rows.Scan(
			&r.ID, &r.PropertyID, &r.Reference, &r.GuestID, &r.GuestName, &r.GuestEmail, &r.GuestPhone,
			&r.ChannelCode, &r.ArrivalDate, &r.DepartureDate,
			&r.Adults, &r.Children, &r.Infants, &status,
			&r.PaymentStatus, &r.TotalPence, &r.DepositPence, &r.AmountPaidPence, &r.BalanceDuePence, &r.Currency,
			&r.InternalNotes, &r.CancellationReason, &r.CancelledAt,
			&r.CancellationRequestedAt, &r.CancellationRequestNote,
			&r.CreatedBy, &r.UpdatedBy, &r.Version, &r.CreatedAt, &r.UpdatedAt,
			&specialReq,
		); err != nil {
			return nil, MapErr(err)
		}
		r.Status = domain.ReservationStatus(status)
		out = append(out, OperationsStayRow{
			Reservation:     r,
			SpecialRequests: strings.TrimSpace(specialReq),
		})
		ids = append(ids, r.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, MapErr(err)
	}
	if len(ids) == 0 {
		return out, nil
	}
	unitMap, err := s.loadReservationUnitsMap(ctx, s.pool, propertyID, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		units := unitMap[out[i].Reservation.ID]
		out[i].Reservation.Units = units
		for _, u := range units {
			out[i].Reservation.UnitIDs = append(out[i].Reservation.UnitIDs, u.UnitID)
		}
	}
	return out, nil
}

func (s *Store) ListOperationsPaymentAnomalies(ctx context.Context, propertyID uuid.UUID) ([]OperationsPaymentAnomalyRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT pa.id, 'PAYMENT_ATTEMPT_FAILED', COALESCE(r.reference, bh.hold_reference, pa.id::text),
		       pa.reservation_id, pa.amount_pence, pa.currency,
		       COALESCE(NULLIF(pa.failure_reason, ''), pa.status), pa.created_at
		FROM payment_attempts pa
		LEFT JOIN reservations r ON r.id = pa.reservation_id
		LEFT JOIN booking_holds bh ON bh.id = pa.hold_id
		WHERE pa.property_id = $1
		  AND pa.status IN ('FAILED', 'RECONCILIATION_REQUIRED')
		  AND (pa.reservation_id IS NULL OR r.payment_status <> 'PAID')
		  AND pa.created_at >= NOW() - INTERVAL '14 days'
		ORDER BY pa.created_at DESC
		LIMIT 20
	`, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []OperationsPaymentAnomalyRow
	for rows.Next() {
		var row OperationsPaymentAnomalyRow
		if err := rows.Scan(
			&row.ID, &row.Kind, &row.Reference, &row.ReservationID,
			&row.AmountPence, &row.Currency, &row.Detail, &row.CreatedAt,
		); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, row)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) ListAssignableStaff(ctx context.Context, propertyID uuid.UUID) ([]domain.OpsStaffMember, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, full_name, role
		FROM admin_users
		WHERE is_active = TRUE
		  AND role IN ('owner_admin', 'manager', 'staff', 'housekeeping_staff')
		  AND (property_id IS NULL OR property_id = $1)
		ORDER BY full_name ASC
	`, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.OpsStaffMember
	for rows.Next() {
		var m domain.OpsStaffMember
		var role string
		if err := rows.Scan(&m.ID, &m.FullName, &role); err != nil {
			return nil, MapErr(err)
		}
		m.Role = domain.Role(role)
		out = append(out, m)
	}
	return out, MapErr(rows.Err())
}

func scanHousekeepingTask(r scannable) (domain.HousekeepingTask, error) {
	var t domain.HousekeepingTask
	var taskType, stage, priority string
	err := r.Scan(
		&t.ID, &t.PropertyID, &t.UnitID, &t.UnitCode, &t.UnitName, &t.UnitTypeName, &t.UnitZone, &t.UnitStatus,
		&t.ReservationID, &t.ReservationReference, &t.GuestName,
		&taskType, &stage, &priority, &t.ScheduledDate, &t.DueAt,
		&t.AssignedTo, &t.AssigneeName,
		&t.Title, &t.Notes, &t.StartedAt, &t.StartedBy, &t.StartedByName,
		&t.InspectedAt, &t.InspectedBy, &t.InspectedByName, &t.CompletedAt, &t.CompletedBy,
		&t.Version, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return domain.HousekeepingTask{}, err
	}
	t.TaskType = domain.HousekeepingTaskType(taskType)
	t.Stage = domain.ChangeoverStage(stage)
	t.Priority = domain.HousekeepingPriority(priority)
	return t, nil
}
