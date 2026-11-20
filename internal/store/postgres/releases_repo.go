package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Prestwickuk/lovelace/internal/domain"
)

func (s *Store) ListReleaseRules(ctx context.Context, db DBTX, propertyID uuid.UUID, includeCancelled bool) ([]domain.AvailabilityReleaseRule, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT r.id, r.property_id, r.scope_type, r.unit_id, COALESCE(u.code, ''), COALESCE(u.name, ''),
		       r.unit_type_id, COALESCE(ut.name, ''),
		       lower(r.stay_range), upper(r.stay_range),
		       r.channel_code, r.release_at, r.released_manually_at, r.released_manually_by,
		       r.is_active, r.notes, r.created_by, r.updated_by, r.cancelled_at, r.cancelled_by,
		       r.version, r.created_at, r.updated_at
		FROM availability_release_rules r
		LEFT JOIN units u ON u.id = r.unit_id
		LEFT JOIN unit_types ut ON ut.id = r.unit_type_id
		WHERE r.property_id = $1
		  AND ($2::boolean OR r.cancelled_at IS NULL)
		ORDER BY (r.cancelled_at IS NOT NULL) ASC, lower(r.stay_range) ASC, r.release_at ASC
	`, propertyID, includeCancelled)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.AvailabilityReleaseRule
	for rows.Next() {
		rule, err := scanReleaseRule(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, rule)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) ListActiveReleaseRulesInRange(ctx context.Context, db DBTX, propertyID uuid.UUID, startDate, endDate time.Time) ([]domain.AvailabilityReleaseRule, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT r.id, r.property_id, r.scope_type, r.unit_id, COALESCE(u.code, ''), COALESCE(u.name, ''),
		       r.unit_type_id, COALESCE(ut.name, ''),
		       lower(r.stay_range), upper(r.stay_range),
		       r.channel_code, r.release_at, r.released_manually_at, r.released_manually_by,
		       r.is_active, r.notes, r.created_by, r.updated_by, r.cancelled_at, r.cancelled_by,
		       r.version, r.created_at, r.updated_at
		FROM availability_release_rules r
		LEFT JOIN units u ON u.id = r.unit_id
		LEFT JOIN unit_types ut ON ut.id = r.unit_type_id
		WHERE r.property_id = $1
		  AND r.is_active = TRUE
		  AND r.cancelled_at IS NULL
		  AND r.stay_range && daterange($2::date, $3::date, '[)')
		ORDER BY r.release_at ASC
	`, propertyID, domain.FormatDate(startDate), domain.FormatDate(endDate))
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.AvailabilityReleaseRule
	for rows.Next() {
		rule, err := scanReleaseRule(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, rule)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetReleaseRule(ctx context.Context, db DBTX, propertyID, id uuid.UUID) (domain.AvailabilityReleaseRule, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT r.id, r.property_id, r.scope_type, r.unit_id, COALESCE(u.code, ''), COALESCE(u.name, ''),
		       r.unit_type_id, COALESCE(ut.name, ''),
		       lower(r.stay_range), upper(r.stay_range),
		       r.channel_code, r.release_at, r.released_manually_at, r.released_manually_by,
		       r.is_active, r.notes, r.created_by, r.updated_by, r.cancelled_at, r.cancelled_by,
		       r.version, r.created_at, r.updated_at
		FROM availability_release_rules r
		LEFT JOIN units u ON u.id = r.unit_id
		LEFT JOIN unit_types ut ON ut.id = r.unit_type_id
		WHERE r.property_id = $1 AND r.id = $2
	`, propertyID, id)
	rule, err := scanReleaseRule(row)
	return rule, MapErr(err)
}

func (s *Store) CreateReleaseRule(ctx context.Context, tx pgx.Tx, r domain.AvailabilityReleaseRule) (domain.AvailabilityReleaseRule, error) {
	_, err := tx.Exec(ctx, `
		INSERT INTO availability_release_rules (
			id, property_id, scope_type, unit_id, unit_type_id,
			stay_range, channel_code, release_at, is_active, notes,
			created_by, updated_by
		) VALUES (
			$1, $2, $3, $4, $5,
			daterange($6::date, $7::date, '[)'), $8, $9, $10, $11,
			$12, $13
		)
	`,
		r.ID, r.PropertyID, string(r.ScopeType), r.UnitID, r.UnitTypeID,
		domain.FormatDate(r.StartDate), domain.FormatDate(r.EndDate), r.ChannelCode, r.ReleaseAt.UTC(), r.IsActive, r.Notes,
		r.CreatedBy, r.UpdatedBy,
	)
	if err != nil {
		return domain.AvailabilityReleaseRule{}, MapErr(err)
	}
	return s.GetReleaseRule(ctx, tx, r.PropertyID, r.ID)
}

func (s *Store) UpdateReleaseRule(ctx context.Context, tx pgx.Tx, r domain.AvailabilityReleaseRule) (domain.AvailabilityReleaseRule, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE availability_release_rules
		SET scope_type = $3,
		    unit_id = $4,
		    unit_type_id = $5,
		    stay_range = daterange($6::date, $7::date, '[)'),
		    channel_code = $8,
		    release_at = $9,
		    is_active = $10,
		    notes = $11,
		    updated_by = $12,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND cancelled_at IS NULL AND version = $13
	`,
		r.PropertyID, r.ID, string(r.ScopeType), r.UnitID, r.UnitTypeID,
		domain.FormatDate(r.StartDate), domain.FormatDate(r.EndDate), r.ChannelCode, r.ReleaseAt.UTC(),
		r.IsActive, r.Notes, r.UpdatedBy, r.Version,
	)
	if err != nil {
		return domain.AvailabilityReleaseRule{}, MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		if _, gerr := s.GetReleaseRule(ctx, tx, r.PropertyID, r.ID); gerr == nil {
			return domain.AvailabilityReleaseRule{}, domain.ErrConflict
		}
		return domain.AvailabilityReleaseRule{}, domain.ErrNotFound
	}
	return s.GetReleaseRule(ctx, tx, r.PropertyID, r.ID)
}

func (s *Store) ManualReleaseRule(ctx context.Context, tx pgx.Tx, propertyID, id, actorID uuid.UUID) (domain.AvailabilityReleaseRule, bool, error) {
	existing, err := s.GetReleaseRule(ctx, tx, propertyID, id)
	if err != nil {
		return domain.AvailabilityReleaseRule{}, false, err
	}
	if existing.CancelledAt != nil {
		return domain.AvailabilityReleaseRule{}, false, domain.NewValidationError(map[string]string{
			"release_rule": "cannot release a cancelled rule",
		})
	}
	if existing.ReleasedManuallyAt != nil {
		return existing, false, nil // Idempotent: already released manually
	}
	if _, err := tx.Exec(ctx, `
		UPDATE availability_release_rules
		SET released_manually_at = NOW(),
		    released_manually_by = $3,
		    updated_by = $3,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND released_manually_at IS NULL AND cancelled_at IS NULL
	`, propertyID, id, actorID); err != nil {
		return domain.AvailabilityReleaseRule{}, false, MapErr(err)
	}
	updated, err := s.GetReleaseRule(ctx, tx, propertyID, id)
	return updated, true, err
}

func (s *Store) CancelReleaseRule(ctx context.Context, tx pgx.Tx, propertyID, id, actorID uuid.UUID) (domain.AvailabilityReleaseRule, error) {
	existing, err := s.GetReleaseRule(ctx, tx, propertyID, id)
	if err != nil {
		return domain.AvailabilityReleaseRule{}, err
	}
	if existing.CancelledAt != nil {
		return existing, nil // Idempotent
	}
	if _, err := tx.Exec(ctx, `
		UPDATE availability_release_rules
		SET is_active = FALSE,
		    cancelled_at = NOW(),
		    cancelled_by = $3,
		    updated_by = $3,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND cancelled_at IS NULL
	`, propertyID, id, actorID); err != nil {
		return domain.AvailabilityReleaseRule{}, MapErr(err)
	}
	return s.GetReleaseRule(ctx, tx, propertyID, id)
}

func scanReleaseRule(r scannable) (domain.AvailabilityReleaseRule, error) {
	var rule domain.AvailabilityReleaseRule
	var scope string
	err := r.Scan(
		&rule.ID, &rule.PropertyID, &scope, &rule.UnitID, &rule.UnitCode, &rule.UnitName,
		&rule.UnitTypeID, &rule.UnitTypeName,
		&rule.StartDate, &rule.EndDate,
		&rule.ChannelCode, &rule.ReleaseAt, &rule.ReleasedManuallyAt, &rule.ReleasedManuallyBy,
		&rule.IsActive, &rule.Notes, &rule.CreatedBy, &rule.UpdatedBy, &rule.CancelledAt, &rule.CancelledBy,
		&rule.Version, &rule.CreatedAt, &rule.UpdatedAt,
	)
	if err != nil {
		return domain.AvailabilityReleaseRule{}, err
	}
	rule.ScopeType = domain.ReleaseScope(scope)
	return rule, nil
}
