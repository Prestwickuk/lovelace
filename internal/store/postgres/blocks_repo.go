package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/domain"
)

func legacyBlockTypeToCategory(bt domain.BlockType) domain.ClaimType {
	switch bt {
	case domain.BlockOwnerHold:
		return domain.ClaimOwnerUse
	case domain.BlockMaintenance:
		return domain.ClaimMaintenance
	case domain.BlockSeasonalClosure:
		return domain.ClaimSeasonalClosure
	case domain.BlockWeatherClosure:
		return domain.ClaimWeatherClosure
	default:
		return domain.ClaimAdministrativeBlock
	}
}

func categoryToLegacyBlockType(ct string) domain.BlockType {
	switch domain.ClaimType(ct) {
	case domain.ClaimOwnerUse:
		return domain.BlockOwnerHold
	case domain.ClaimMaintenance:
		return domain.BlockMaintenance
	case domain.ClaimSeasonalClosure:
		return domain.BlockSeasonalClosure
	case domain.ClaimWeatherClosure:
		return domain.BlockWeatherClosure
	default:
		return domain.BlockType(ct)
	}
}

func (s *Store) ListAvailabilityBlocks(ctx context.Context, propertyID uuid.UUID, unitID *uuid.UUID, includeReleased bool) ([]domain.AvailabilityBlock, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ic.id, ic.property_id, ic.unit_id, u.code, u.name,
		       lower(ic.claim_range), upper(ic.claim_range),
		       ic.claim_type, ic.reason, ic.created_by, ic.released_at, ic.released_by,
		       ic.created_at, ic.updated_at
		FROM inventory_claims ic
		JOIN units u ON u.id = ic.unit_id
		WHERE ic.property_id = $1
		  AND ic.blockout_group_id IS NOT NULL
		  AND ($2::uuid IS NULL OR ic.unit_id = $2)
		  AND ($3::boolean OR ic.status = 'ACTIVE')
		ORDER BY (ic.status <> 'ACTIVE') ASC, lower(ic.claim_range) ASC, u.code ASC
	`, propertyID, unitID, includeReleased)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.AvailabilityBlock
	for rows.Next() {
		b, err := scanBlock(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, b)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetAvailabilityBlock(ctx context.Context, db DBTX, propertyID, id uuid.UUID) (domain.AvailabilityBlock, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT ic.id, ic.property_id, ic.unit_id, u.code, u.name,
		       lower(ic.claim_range), upper(ic.claim_range),
		       ic.claim_type, ic.reason, ic.created_by, ic.released_at, ic.released_by,
		       ic.created_at, ic.updated_at
		FROM inventory_claims ic
		JOIN units u ON u.id = ic.unit_id
		WHERE ic.property_id = $1 AND ic.id = $2 AND ic.blockout_group_id IS NOT NULL
	`, propertyID, id)
	b, err := scanBlock(row)
	return b, MapErr(err)
}

func (s *Store) CreateAvailabilityBlock(ctx context.Context, tx pgx.Tx, b domain.AvailabilityBlock) (domain.AvailabilityBlock, error) {
	cat := legacyBlockTypeToCategory(b.BlockType)
	groupID := b.ID // 1:1 group for single legacy blockout
	if _, err := tx.Exec(ctx, `
		INSERT INTO blockout_groups (
			id, property_id, scope_type, category, channel_scope,
			block_range, reason, apply_to_future_units, status, created_by
		) VALUES (
			$1, $2, 'UNIT', $3, 'ALL',
			daterange($4::date, $5::date, '[)'), $6, FALSE, 'ACTIVE', $7
		)
	`, groupID, b.PropertyID, string(cat), domain.FormatDate(b.StartDate), domain.FormatDate(b.EndDate), b.Reason, b.CreatedBy); err != nil {
		return domain.AvailabilityBlock{}, MapErr(err)
	}

	row := tx.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO inventory_claims (
				id, property_id, unit_id, claim_type, blockout_group_id, channel_scope,
				claim_range, status, reason, created_by
			) VALUES (
				$1, $2, $3, $4, $5, 'ALL',
				daterange($6::date, $7::date, '[)'), 'ACTIVE', $8, $9
			)
			RETURNING *
		)
		SELECT i.id, i.property_id, i.unit_id, u.code, u.name,
		       lower(i.claim_range), upper(i.claim_range),
		       i.claim_type, i.reason, i.created_by, i.released_at, i.released_by,
		       i.created_at, i.updated_at
		FROM inserted i
		JOIN units u ON u.id = i.unit_id
	`,
		b.ID, b.PropertyID, b.UnitID, string(cat), groupID,
		domain.FormatDate(b.StartDate), domain.FormatDate(b.EndDate),
		b.Reason, b.CreatedBy,
	)
	out, err := scanBlock(row)
	return out, MapErr(err)
}

func (s *Store) ReleaseAvailabilityBlock(ctx context.Context, tx pgx.Tx, propertyID, id, releasedBy uuid.UUID) (domain.AvailabilityBlock, error) {
	var groupID uuid.UUID
	row := tx.QueryRow(ctx, `
		WITH updated AS (
			UPDATE inventory_claims
			SET status = 'RELEASED', released_at = NOW(), released_by = $3, updated_at = NOW()
			WHERE property_id = $1 AND id = $2 AND blockout_group_id IS NOT NULL AND status = 'ACTIVE'
			RETURNING *
		)
		SELECT u2.id, u2.property_id, u2.unit_id, un.code, un.name,
		       lower(u2.claim_range), upper(u2.claim_range),
		       u2.claim_type, u2.reason, u2.created_by, u2.released_at, u2.released_by,
		       u2.created_at, u2.updated_at, u2.blockout_group_id
		FROM updated u2
		JOIN units un ON un.id = u2.unit_id
	`, propertyID, id, releasedBy)

	var b domain.AvailabilityBlock
	var ct string
	err := row.Scan(
		&b.ID, &b.PropertyID, &b.UnitID, &b.UnitCode, &b.UnitName,
		&b.StartDate, &b.EndDate,
		&ct, &b.Reason, &b.CreatedBy, &b.ReleasedAt, &b.ReleasedBy,
		&b.CreatedAt, &b.UpdatedAt, &groupID,
	)
	if err == pgx.ErrNoRows {
		if _, gerr := s.GetAvailabilityBlock(ctx, tx, propertyID, id); gerr == nil {
			return domain.AvailabilityBlock{}, domain.ErrConflict
		}
		return domain.AvailabilityBlock{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.AvailabilityBlock{}, MapErr(err)
	}
	b.BlockType = categoryToLegacyBlockType(ct)

	if err := s.recomputeBlockoutGroupStatus(ctx, tx, groupID, releasedBy); err != nil {
		return domain.AvailabilityBlock{}, err
	}
	return b, nil
}

func scanBlock(r scannable) (domain.AvailabilityBlock, error) {
	var b domain.AvailabilityBlock
	var ct string
	err := r.Scan(
		&b.ID, &b.PropertyID, &b.UnitID, &b.UnitCode, &b.UnitName,
		&b.StartDate, &b.EndDate,
		&ct, &b.Reason, &b.CreatedBy, &b.ReleasedAt, &b.ReleasedBy,
		&b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		return domain.AvailabilityBlock{}, err
	}
	b.BlockType = categoryToLegacyBlockType(ct)
	return b, nil
}
