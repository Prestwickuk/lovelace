package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Prestwickuk/lovelace/internal/auth"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/store/postgres"
)

func (s *Service) ListAvailabilityBlocks(ctx context.Context, actor Actor, propertyID uuid.UUID, unitID *uuid.UUID, includeReleased bool) ([]domain.AvailabilityBlock, error) {
	if err := s.requirePerm(actor, auth.PermBlocksRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListAvailabilityBlocks(ctx, propertyID, unitID, includeReleased)
}

func (s *Service) CreateAvailabilityBlock(ctx context.Context, actor Actor, b domain.AvailabilityBlock) (domain.AvailabilityBlock, error) {
	if err := s.requirePerm(actor, auth.PermBlocksWrite); err != nil {
		return domain.AvailabilityBlock{}, err
	}
	if err := s.requirePropertyScope(actor, b.PropertyID); err != nil {
		return domain.AvailabilityBlock{}, err
	}
	if err := b.Validate(); err != nil {
		return domain.AvailabilityBlock{}, err
	}
	id, err := postgres.NewUUID()
	if err != nil {
		return domain.AvailabilityBlock{}, err
	}
	b.ID = id
	createdBy := actor.UserID
	b.CreatedBy = &createdBy

	var created domain.AvailabilityBlock
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		unit, err := s.store.GetUnit(ctx, tx, b.PropertyID, b.UnitID)
		if err != nil {
			if err == domain.ErrNotFound {
				return domain.NewValidationError(map[string]string{"unit_id": "must belong to this property"})
			}
			return err
		}
		if unit.ArchivedAt != nil || unit.Status == domain.UnitStatusRetired || unit.Status == domain.UnitStatusInactive {
			return domain.NewValidationError(map[string]string{"unit_id": "cannot block an inactive, retired, or archived unit"})
		}
		created, err = s.store.CreateAvailabilityBlock(ctx, tx, b)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &created.PropertyID, "availability_block.created", "availability_block", created.ID.String(), nil, created)
	})
	return created, err
}

func (s *Service) ReleaseAvailabilityBlock(ctx context.Context, actor Actor, propertyID, blockID uuid.UUID) (domain.AvailabilityBlock, error) {
	if err := s.requirePerm(actor, auth.PermBlocksWrite); err != nil {
		return domain.AvailabilityBlock{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.AvailabilityBlock{}, err
	}
	var released domain.AvailabilityBlock
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetAvailabilityBlock(ctx, tx, propertyID, blockID)
		if err != nil {
			return err
		}
		released, err = s.store.ReleaseAvailabilityBlock(ctx, tx, propertyID, blockID, actor.UserID)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "availability_block.released", "availability_block", blockID.String(), before, released)
	})
	return released, err
}
