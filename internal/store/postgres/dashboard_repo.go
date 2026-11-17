package postgres

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/domain"
)

// DashboardOutboxAlertRow captures strictly non-PII email_outbox exception metadata.
type DashboardOutboxAlertRow struct {
	ID              uuid.UUID
	Template        string
	Status          string
	DispatchOutcome string
	LastError       string
	NextAttemptAt   time.Time
	UpdatedAt       time.Time
}

// DashboardChannelHealthRow aggregates per-provider connection, sync job, and import anomaly counts.
type DashboardChannelHealthRow struct {
	ProviderCode      string
	DisplayName       string
	Status            string
	LastError         string
	LastJobStatus     string
	LastJobAt         *time.Time
	FailedJobsCount   int
	PendingJobsCount  int
	UnmappedCount     int
	OverbookedCount   int
	FailedImportCount int
	MappingsCount     int
	UpdatedAt         *time.Time
}

// DashboardUnitTurnaroundRow gives unit-level departure/same-day turnaround flags without guest PII.
type DashboardUnitTurnaroundRow struct {
	UnitID         uuid.UUID
	DepartingToday bool
	SameDayArrival bool
}

// GetPropertyDashboardLayoutRaw reads raw PWE-3 dashboard layout JSON without touching PWE-2 columns.
func (s *Store) GetPropertyDashboardLayoutRaw(
	ctx context.Context,
	db DBTX,
	userID, propertyID uuid.UUID,
) (schemaVersion int, rawLayout []byte, updatedAt *time.Time, err error) {
	if db == nil {
		db = s.pool
	}
	var upd time.Time
	query := `
		SELECT dashboard_schema_version, dashboard_layout, updated_at
		FROM admin_user_property_workspaces
		WHERE user_id = $1 AND property_id = $2
	`
	if _, isTx := db.(pgx.Tx); isTx {
		query += " FOR UPDATE"
	}
	err = db.QueryRow(ctx, query, userID, propertyID).Scan(&schemaVersion, &rawLayout, &upd)
	if err != nil {
		return 0, nil, nil, MapErr(err)
	}
	if len(rawLayout) > 0 {
		updatedAt = &upd
	}
	return schemaVersion, rawLayout, updatedAt, nil
}

// UpsertPropertyDashboardLayout saves PWE-3 dashboard_layout while preserving PWE-2 preferences and saved_views.
func (s *Store) UpsertPropertyDashboardLayout(
	ctx context.Context,
	db DBTX,
	userID, propertyID uuid.UUID,
	schemaVersion int,
	layoutJSON []byte,
) (time.Time, error) {
	if db == nil {
		db = s.pool
	}
	if schemaVersion < 1 {
		schemaVersion = domain.CurrentDashboardLayoutSchemaVersion
	}
	var updatedAt time.Time
	err := db.QueryRow(ctx, `
		INSERT INTO admin_user_property_workspaces (
			user_id, property_id, dashboard_schema_version, dashboard_layout, updated_at
		) VALUES ($1, $2, $3, $4::jsonb, NOW())
		ON CONFLICT (user_id, property_id) DO UPDATE SET
			dashboard_schema_version = EXCLUDED.dashboard_schema_version,
			dashboard_layout         = EXCLUDED.dashboard_layout,
			updated_at               = NOW()
		RETURNING updated_at
	`, userID, propertyID, schemaVersion, layoutJSON).Scan(&updatedAt)
	if err != nil {
		return time.Time{}, MapErr(err)
	}
	return updatedAt, nil
}

// ResetPropertyDashboardLayout clears dashboard_layout back to NULL while preserving PWE-2 settings.
func (s *Store) ResetPropertyDashboardLayout(
	ctx context.Context,
	db DBTX,
	userID, propertyID uuid.UUID,
) error {
	if db == nil {
		db = s.pool
	}
	_, err := db.Exec(ctx, `
		UPDATE admin_user_property_workspaces
		SET dashboard_schema_version = $3,
		    dashboard_layout = NULL,
		    updated_at = NOW()
		WHERE user_id = $1 AND property_id = $2
	`, userID, propertyID, domain.CurrentDashboardLayoutSchemaVersion)
	return MapErr(err)
}

// ListDashboardOutboxAlerts loads bounded non-PII outbox anomalies (FAILED, DISPATCH_UNKNOWN, or stale PENDING).
func (s *Store) ListDashboardOutboxAlerts(
	ctx context.Context,
	propertyID uuid.UUID,
	staleBefore time.Time,
	limit int,
) (totalCount int, rows []DashboardOutboxAlertRow, err error) {
	if limit <= 0 {
		limit = 10
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM email_outbox
		WHERE property_id = $1
		  AND (
		      status IN ('FAILED', 'DISPATCH_UNKNOWN')
		      OR (status IN ('PENDING', 'DISPATCHING') AND next_attempt_at <= $2)
		  )
	`, propertyID, staleBefore).Scan(&totalCount); err != nil {
		return 0, nil, MapErr(err)
	}
	if totalCount == 0 {
		return 0, nil, nil
	}

	qRows, err := s.pool.Query(ctx, `
		SELECT id, template, status, dispatch_outcome, last_error, next_attempt_at, updated_at
		FROM email_outbox
		WHERE property_id = $1
		  AND (
		      status IN ('FAILED', 'DISPATCH_UNKNOWN')
		      OR (status IN ('PENDING', 'DISPATCHING') AND next_attempt_at <= $2)
		  )
		ORDER BY
		  CASE status WHEN 'DISPATCH_UNKNOWN' THEN 0 WHEN 'FAILED' THEN 1 ELSE 2 END,
		  updated_at DESC
		LIMIT $3
	`, propertyID, staleBefore, limit)
	if err != nil {
		return 0, nil, MapErr(err)
	}
	defer qRows.Close()

	for qRows.Next() {
		var r DashboardOutboxAlertRow
		if err := qRows.Scan(&r.ID, &r.Template, &r.Status, &r.DispatchOutcome, &r.LastError, &r.NextAttemptAt, &r.UpdatedAt); err != nil {
			return 0, nil, MapErr(err)
		}
		rows = append(rows, r)
	}
	if err := qRows.Err(); err != nil {
		return 0, nil, MapErr(err)
	}
	return totalCount, rows, nil
}

// ListDashboardChannelHealth loads per-provider connection and sync summary without secrets or N+1 queries.
func (s *Store) ListDashboardChannelHealth(ctx context.Context, propertyID uuid.UUID) ([]DashboardChannelHealthRow, error) {
	qRows, err := s.pool.Query(ctx, `
		WITH job_agg AS (
			SELECT
				provider_code,
				COUNT(*) FILTER (WHERE status IN ('FAILED_RETRYABLE', 'FAILED_TERMINAL'))::int AS failed_cnt,
				COUNT(*) FILTER (WHERE status IN ('PENDING', 'RUNNING'))::int AS pending_cnt
			FROM channel_sync_jobs
			WHERE property_id = $1
			GROUP BY provider_code
		),
		latest_job AS (
			SELECT DISTINCT ON (provider_code)
				provider_code,
				status AS last_status,
				updated_at AS last_at
			FROM channel_sync_jobs
			WHERE property_id = $1
			ORDER BY provider_code, updated_at DESC
		),
		ext_agg AS (
			SELECT
				provider_code,
				COUNT(*) FILTER (WHERE import_status = 'UNMAPPED')::int AS unmapped_cnt,
				COUNT(*) FILTER (WHERE import_status = 'CONFLICT_OVERBOOKED')::int AS overbooked_cnt,
				COUNT(*) FILTER (WHERE import_status = 'FAILED')::int AS failed_import_cnt
			FROM channel_external_reservations
			WHERE property_id = $1
			GROUP BY provider_code
		),
		map_agg AS (
			SELECT provider_code, COUNT(*) FILTER (WHERE is_active)::int AS mappings_cnt
			FROM channel_listing_mappings
			WHERE property_id = $1
			GROUP BY provider_code
		)
		SELECT
			cc.provider_code,
			cc.display_name,
			cc.status,
			cc.last_error,
			COALESCE(lj.last_status, ''),
			lj.last_at,
			COALESCE(ja.failed_cnt, 0),
			COALESCE(ja.pending_cnt, 0),
			COALESCE(ea.unmapped_cnt, 0),
			COALESCE(ea.overbooked_cnt, 0),
			COALESCE(ea.failed_import_cnt, 0),
			COALESCE(ma.mappings_cnt, 0),
			cc.updated_at
		FROM channel_connections cc
		LEFT JOIN job_agg ja ON ja.provider_code = cc.provider_code
		LEFT JOIN latest_job lj ON lj.provider_code = cc.provider_code
		LEFT JOIN ext_agg ea ON ea.provider_code = cc.provider_code
		LEFT JOIN map_agg ma ON ma.provider_code = cc.provider_code
		WHERE cc.property_id = $1
		ORDER BY cc.provider_code ASC
	`, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer qRows.Close()

	var out []DashboardChannelHealthRow
	for qRows.Next() {
		var r DashboardChannelHealthRow
		var lastAt, updAt *time.Time
		if err := qRows.Scan(
			&r.ProviderCode,
			&r.DisplayName,
			&r.Status,
			&r.LastError,
			&r.LastJobStatus,
			&lastAt,
			&r.FailedJobsCount,
			&r.PendingJobsCount,
			&r.UnmappedCount,
			&r.OverbookedCount,
			&r.FailedImportCount,
			&r.MappingsCount,
			&updAt,
		); err != nil {
			return nil, MapErr(err)
		}
		r.LastJobAt = lastAt
		r.UpdatedAt = updAt
		r.ProviderCode = strings.ToLower(strings.TrimSpace(r.ProviderCode))
		out = append(out, r)
	}
	if err := qRows.Err(); err != nil {
		return nil, MapErr(err)
	}
	return out, nil
}

// ListDashboardUnitTurnaroundFlags computes purely operational unit departure/turnaround booleans
// for housekeeping boards when the caller holds ops:read without reservations:read.
func (s *Store) ListDashboardUnitTurnaroundFlags(
	ctx context.Context,
	propertyID uuid.UUID,
	selectedDate, todayDate time.Time,
) (map[uuid.UUID]DashboardUnitTurnaroundRow, error) {
	selStr := domain.FormatDate(selectedDate)
	todayStr := domain.FormatDate(todayDate)
	qRows, err := s.pool.Query(ctx, `
		SELECT
			ru.unit_id,
			BOOL_OR(
				r.status = 'CHECKED_IN'
				AND (
					upper(r.stay_range) = $2::date
					OR ($2::date = $3::date AND upper(r.stay_range) < $3::date)
				)
			) AS departing_today,
			BOOL_OR(
				r.status IN ('CONFIRMED', 'CHECKED_IN', 'PENDING')
				AND lower(r.stay_range) = $2::date
			) AS same_day_arrival
		FROM reservation_units ru
		JOIN reservations r ON r.id = ru.reservation_id
		WHERE r.property_id = $1
		  AND r.status IN ('CONFIRMED', 'CHECKED_IN', 'PENDING')
		  AND (
			  lower(r.stay_range) = $2::date
			  OR upper(r.stay_range) = $2::date
			  OR ($2::date = $3::date AND r.status = 'CHECKED_IN' AND upper(r.stay_range) < $3::date)
		  )
		GROUP BY ru.unit_id
	`, propertyID, selStr, todayStr)
	if err != nil {
		if err == pgx.ErrNoRows {
			return map[uuid.UUID]DashboardUnitTurnaroundRow{}, nil
		}
		return nil, MapErr(err)
	}
	defer qRows.Close()

	out := make(map[uuid.UUID]DashboardUnitTurnaroundRow)
	for qRows.Next() {
		var r DashboardUnitTurnaroundRow
		if err := qRows.Scan(&r.UnitID, &r.DepartingToday, &r.SameDayArrival); err != nil {
			return nil, MapErr(err)
		}
		out[r.UnitID] = r
	}
	if err := qRows.Err(); err != nil {
		return nil, MapErr(err)
	}
	return out, nil
}
