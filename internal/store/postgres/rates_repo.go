package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/domain"
)

// --- Rate Plans ---

func (s *Store) ListRatePlans(ctx context.Context, propertyID uuid.UUID) ([]domain.RatePlan, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, property_id, code, name, description, is_default, is_active, priority,
		       version, archived_at, created_at, updated_at
		FROM rate_plans
		WHERE property_id = $1 AND archived_at IS NULL
		ORDER BY is_default DESC, priority DESC, name ASC
	`, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.RatePlan
	for rows.Next() {
		rp, err := scanRatePlan(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, rp)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetRatePlan(ctx context.Context, db DBTX, propertyID, id uuid.UUID) (domain.RatePlan, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT id, property_id, code, name, description, is_default, is_active, priority,
		       version, archived_at, created_at, updated_at
		FROM rate_plans
		WHERE property_id = $1 AND id = $2 AND archived_at IS NULL
	`, propertyID, id)
	rp, err := scanRatePlan(row)
	return rp, MapErr(err)
}

func (s *Store) CreateRatePlan(ctx context.Context, tx pgx.Tx, rp domain.RatePlan) (domain.RatePlan, error) {
	if rp.IsDefault {
		if _, err := tx.Exec(ctx, `
			UPDATE rate_plans SET is_default = FALSE, updated_at = NOW(), version = version + 1
			WHERE property_id = $1 AND is_default = TRUE AND archived_at IS NULL
		`, rp.PropertyID); err != nil {
			return domain.RatePlan{}, MapErr(err)
		}
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO rate_plans (id, property_id, code, name, description, is_default, is_active, priority)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, property_id, code, name, description, is_default, is_active, priority,
		          version, archived_at, created_at, updated_at
	`, rp.ID, rp.PropertyID, rp.Code, rp.Name, rp.Description, rp.IsDefault, rp.IsActive, rp.Priority)
	out, err := scanRatePlan(row)
	return out, MapErr(err)
}

func (s *Store) UpdateRatePlan(ctx context.Context, tx pgx.Tx, rp domain.RatePlan) (domain.RatePlan, error) {
	if rp.IsDefault {
		if _, err := tx.Exec(ctx, `
			UPDATE rate_plans SET is_default = FALSE, updated_at = NOW(), version = version + 1
			WHERE property_id = $1 AND id <> $2 AND is_default = TRUE AND archived_at IS NULL
		`, rp.PropertyID, rp.ID); err != nil {
			return domain.RatePlan{}, MapErr(err)
		}
	}
	row := tx.QueryRow(ctx, `
		UPDATE rate_plans
		SET code = $3, name = $4, description = $5, is_default = $6, is_active = $7, priority = $8,
		    version = version + 1, updated_at = NOW()
		WHERE id = $1 AND property_id = $2 AND archived_at IS NULL AND version = $9
		RETURNING id, property_id, code, name, description, is_default, is_active, priority,
		          version, archived_at, created_at, updated_at
	`, rp.ID, rp.PropertyID, rp.Code, rp.Name, rp.Description, rp.IsDefault, rp.IsActive, rp.Priority, rp.Version)
	out, err := scanRatePlan(row)
	if err == pgx.ErrNoRows {
		if _, gerr := s.GetRatePlan(ctx, tx, rp.PropertyID, rp.ID); gerr == nil {
			return domain.RatePlan{}, domain.ErrConflict
		}
		return domain.RatePlan{}, domain.ErrNotFound
	}
	return out, MapErr(err)
}

func scanRatePlan(r scannable) (domain.RatePlan, error) {
	var rp domain.RatePlan
	err := r.Scan(
		&rp.ID, &rp.PropertyID, &rp.Code, &rp.Name, &rp.Description,
		&rp.IsDefault, &rp.IsActive, &rp.Priority,
		&rp.Version, &rp.ArchivedAt, &rp.CreatedAt, &rp.UpdatedAt,
	)
	return rp, err
}

// --- Rate Rules ---

func (s *Store) ListRateRules(ctx context.Context, propertyID uuid.UUID, ratePlanID, unitTypeID *uuid.UUID) ([]domain.RateRule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT rr.id, rr.property_id, rr.rate_plan_id, rp.name, rr.unit_type_id, ut.name,
		       rr.name, lower(rr.date_range), upper(rr.date_range), rr.days_of_week,
		       rr.nightly_rate_pence, rr.min_nights_override, rr.priority, rr.is_active,
		       rr.version, rr.archived_at, rr.created_at, rr.updated_at
		FROM rate_rules rr
		JOIN rate_plans rp ON rp.id = rr.rate_plan_id
		JOIN unit_types ut ON ut.id = rr.unit_type_id
		WHERE rr.property_id = $1
		  AND rr.archived_at IS NULL
		  AND ($2::uuid IS NULL OR rr.rate_plan_id = $2)
		  AND ($3::uuid IS NULL OR rr.unit_type_id = $3)
		ORDER BY rr.priority DESC, lower(rr.date_range) ASC, rr.name ASC
	`, propertyID, ratePlanID, unitTypeID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.RateRule
	for rows.Next() {
		rr, err := scanRateRule(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, rr)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetRateRule(ctx context.Context, db DBTX, propertyID, id uuid.UUID) (domain.RateRule, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT rr.id, rr.property_id, rr.rate_plan_id, rp.name, rr.unit_type_id, ut.name,
		       rr.name, lower(rr.date_range), upper(rr.date_range), rr.days_of_week,
		       rr.nightly_rate_pence, rr.min_nights_override, rr.priority, rr.is_active,
		       rr.version, rr.archived_at, rr.created_at, rr.updated_at
		FROM rate_rules rr
		JOIN rate_plans rp ON rp.id = rr.rate_plan_id
		JOIN unit_types ut ON ut.id = rr.unit_type_id
		WHERE rr.property_id = $1 AND rr.id = $2 AND rr.archived_at IS NULL
	`, propertyID, id)
	rr, err := scanRateRule(row)
	return rr, MapErr(err)
}

func (s *Store) CreateRateRule(ctx context.Context, tx pgx.Tx, rr domain.RateRule) (domain.RateRule, error) {
	row := tx.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO rate_rules (
				id, property_id, rate_plan_id, unit_type_id, name, date_range,
				days_of_week, nightly_rate_pence, min_nights_override, priority, is_active
			) VALUES (
				$1, $2, $3, $4, $5, daterange($6::date, $7::date, '[)'),
				$8, $9, $10, $11, $12
			)
			RETURNING *
		)
		SELECT i.id, i.property_id, i.rate_plan_id, rp.name, i.unit_type_id, ut.name,
		       i.name, lower(i.date_range), upper(i.date_range), i.days_of_week,
		       i.nightly_rate_pence, i.min_nights_override, i.priority, i.is_active,
		       i.version, i.archived_at, i.created_at, i.updated_at
		FROM inserted i
		JOIN rate_plans rp ON rp.id = i.rate_plan_id
		JOIN unit_types ut ON ut.id = i.unit_type_id
	`,
		rr.ID, rr.PropertyID, rr.RatePlanID, rr.UnitTypeID, rr.Name,
		domain.FormatDate(rr.StartDate), domain.FormatDate(rr.EndDate),
		int16(rr.DaysOfWeek), rr.NightlyRatePence, rr.MinNightsOverride, rr.Priority, rr.IsActive,
	)
	out, err := scanRateRule(row)
	return out, MapErr(err)
}

func (s *Store) UpdateRateRule(ctx context.Context, tx pgx.Tx, rr domain.RateRule) (domain.RateRule, error) {
	row := tx.QueryRow(ctx, `
		WITH updated AS (
			UPDATE rate_rules
			SET rate_plan_id = $3,
			    unit_type_id = $4,
			    name = $5,
			    date_range = daterange($6::date, $7::date, '[)'),
			    days_of_week = $8,
			    nightly_rate_pence = $9,
			    min_nights_override = $10,
			    priority = $11,
			    is_active = $12,
			    version = version + 1,
			    updated_at = NOW()
			WHERE id = $1 AND property_id = $2 AND archived_at IS NULL AND version = $13
			RETURNING *
		)
		SELECT u.id, u.property_id, u.rate_plan_id, rp.name, u.unit_type_id, ut.name,
		       u.name, lower(u.date_range), upper(u.date_range), u.days_of_week,
		       u.nightly_rate_pence, u.min_nights_override, u.priority, u.is_active,
		       u.version, u.archived_at, u.created_at, u.updated_at
		FROM updated u
		JOIN rate_plans rp ON rp.id = u.rate_plan_id
		JOIN unit_types ut ON ut.id = u.unit_type_id
	`,
		rr.ID, rr.PropertyID, rr.RatePlanID, rr.UnitTypeID, rr.Name,
		domain.FormatDate(rr.StartDate), domain.FormatDate(rr.EndDate),
		int16(rr.DaysOfWeek), rr.NightlyRatePence, rr.MinNightsOverride, rr.Priority, rr.IsActive, rr.Version,
	)
	out, err := scanRateRule(row)
	if err == pgx.ErrNoRows {
		if _, gerr := s.GetRateRule(ctx, tx, rr.PropertyID, rr.ID); gerr == nil {
			return domain.RateRule{}, domain.ErrConflict
		}
		return domain.RateRule{}, domain.ErrNotFound
	}
	return out, MapErr(err)
}

func (s *Store) ArchiveRateRule(ctx context.Context, tx pgx.Tx, propertyID, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		UPDATE rate_rules
		SET archived_at = NOW(), is_active = FALSE, updated_at = NOW(), version = version + 1
		WHERE property_id = $1 AND id = $2 AND archived_at IS NULL
	`, propertyID, id)
	if err != nil {
		return MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func scanRateRule(r scannable) (domain.RateRule, error) {
	var rr domain.RateRule
	var dow int16
	err := r.Scan(
		&rr.ID, &rr.PropertyID, &rr.RatePlanID, &rr.RatePlanName, &rr.UnitTypeID, &rr.UnitTypeName,
		&rr.Name, &rr.StartDate, &rr.EndDate, &dow,
		&rr.NightlyRatePence, &rr.MinNightsOverride, &rr.Priority, &rr.IsActive,
		&rr.Version, &rr.ArchivedAt, &rr.CreatedAt, &rr.UpdatedAt,
	)
	if err != nil {
		return domain.RateRule{}, err
	}
	rr.DaysOfWeek = domain.DaysOfWeekMask(dow)
	return rr, nil
}
