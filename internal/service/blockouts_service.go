package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Prestwickuk/lovelace/internal/auth"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/store/postgres"
)

func (s *Service) PreviewBulkBlockout(ctx context.Context, actor Actor, in domain.BulkBlockoutInput) (domain.BlockoutPreview, error) {
	if err := s.requirePerm(actor, auth.PermBlocksWrite); err != nil {
		return domain.BlockoutPreview{}, err
	}
	if err := s.requirePropertyScope(actor, in.PropertyID); err != nil {
		return domain.BlockoutPreview{}, err
	}
	if err := in.Validate(); err != nil {
		return domain.BlockoutPreview{}, err
	}

	if in.Category.RequiresSpecificChannel() {
		if err := s.ensureActiveChannel(ctx, nil, in.PropertyID, in.ChannelScope); err != nil {
			return domain.BlockoutPreview{}, err
		}
	}

	units, err := s.store.ResolveBlockoutTargetUnits(ctx, nil, in)
	if err != nil {
		return domain.BlockoutPreview{}, err
	}
	if err := validateBlockoutResolvedUnits(in, units); err != nil {
		return domain.BlockoutPreview{}, err
	}
	unitIDs := make([]uuid.UUID, 0, len(units))
	for _, u := range units {
		unitIDs = append(unitIDs, u.ID)
	}
	conflicts, err := s.store.FindClaimConflictsForChannel(ctx, nil, in.PropertyID, unitIDs, in.StartDate, in.EndDate, in.Category, in.ChannelScope, nil, nil)
	if err != nil {
		return domain.BlockoutPreview{}, err
	}
	return domain.BlockoutPreview{
		AffectedUnits: units,
		Conflicts:     conflicts,
		CanCommit:     len(conflicts) == 0,
	}, nil
}

func (s *Service) CreateBulkBlockout(ctx context.Context, actor Actor, in domain.BulkBlockoutInput) (domain.BlockoutGroup, []domain.InventoryClaim, error) {
	if err := s.requirePerm(actor, auth.PermBlocksWrite); err != nil {
		return domain.BlockoutGroup{}, nil, err
	}
	if err := s.requirePropertyScope(actor, in.PropertyID); err != nil {
		return domain.BlockoutGroup{}, nil, err
	}
	if err := in.Validate(); err != nil {
		return domain.BlockoutGroup{}, nil, err
	}

	groupID, err := postgres.NewUUID()
	if err != nil {
		return domain.BlockoutGroup{}, nil, err
	}
	uid := actor.UserID

	var created domain.BlockoutGroup
	var claims []domain.InventoryClaim
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.store.GetPropertyByID(ctx, tx, in.PropertyID); err != nil {
			return err
		}
		if in.Category.RequiresSpecificChannel() {
			if err := s.ensureActiveChannel(ctx, tx, in.PropertyID, in.ChannelScope); err != nil {
				return err
			}
		}
		if in.ScopeType == domain.BlockScopeUnitType && in.UnitTypeID != nil {
			if _, err := s.store.GetUnitType(ctx, tx, in.PropertyID, *in.UnitTypeID); err != nil {
				return domain.NewValidationError(map[string]string{"unit_type_id": "invalid unit type for property"})
			}
		}

		units, err := s.store.ResolveBlockoutTargetUnits(ctx, tx, in)
		if err != nil {
			return err
		}
		if err := validateBlockoutResolvedUnits(in, units); err != nil {
			return err
		}

		unitIDs := make([]uuid.UUID, 0, len(units))
		for _, u := range units {
			unitIDs = append(unitIDs, u.ID)
		}

		conflicts, err := s.store.FindClaimConflictsForChannel(ctx, tx, in.PropertyID, unitIDs, in.StartDate, in.EndDate, in.Category, in.ChannelScope, nil, nil)
		if err != nil {
			return err
		}
		if len(conflicts) > 0 {
			first := conflicts[0]
			detail := fmt.Sprintf("unit %s already has active %s (%s to %s)", first.UnitCode, first.ClaimType, domain.FormatDate(first.StartDate), domain.FormatDate(first.EndDate))
			if first.Reference != "" {
				detail = fmt.Sprintf("unit %s is booked by reservation %s (%s)", first.UnitCode, first.Reference, first.GuestName)
			}
			return fmt.Errorf("%w: %s", domain.ErrOverlap, detail)
		}

		grp := domain.BlockoutGroup{
			ID:                 groupID,
			PropertyID:         in.PropertyID,
			ScopeType:          in.ScopeType,
			TargetUnitTypeID:   in.UnitTypeID,
			Category:           in.Category,
			ChannelScope:       in.ChannelScope,
			StartDate:          in.StartDate,
			EndDate:            in.EndDate,
			Reason:             in.Reason,
			ApplyToFutureUnits: in.ApplyToFutureUnits,
			CreatedBy:          &uid,
		}
		created, claims, err = s.store.CreateBulkBlockout(ctx, tx, grp, unitIDs)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &created.PropertyID, "blockout_group.created", "blockout_group", created.ID.String(), nil, map[string]any{
			"group":      created,
			"unit_count": len(unitIDs),
		})
	})
	return created, claims, err
}

func (s *Service) ListBlockoutGroups(ctx context.Context, actor Actor, propertyID uuid.UUID, includeReleased bool) ([]domain.BlockoutGroup, error) {
	if err := s.requirePerm(actor, auth.PermBlocksRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListBlockoutGroups(ctx, propertyID, includeReleased)
}

func (s *Service) GetBlockoutGroup(ctx context.Context, actor Actor, propertyID, groupID uuid.UUID) (domain.BlockoutGroup, []domain.InventoryClaim, error) {
	if err := s.requirePerm(actor, auth.PermBlocksRead); err != nil {
		return domain.BlockoutGroup{}, nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.BlockoutGroup{}, nil, err
	}
	grp, err := s.store.GetBlockoutGroup(ctx, nil, propertyID, groupID)
	if err != nil {
		return domain.BlockoutGroup{}, nil, err
	}
	claims, err := s.store.ListClaimsByBlockoutGroup(ctx, nil, propertyID, groupID)
	if err != nil {
		return domain.BlockoutGroup{}, nil, err
	}
	return grp, claims, nil
}

func (s *Service) ReleaseBlockout(ctx context.Context, actor Actor, propertyID, groupID uuid.UUID, unitIDs []uuid.UUID) (domain.BlockoutGroup, []domain.InventoryClaim, error) {
	if err := s.requirePerm(actor, auth.PermBlocksWrite); err != nil {
		return domain.BlockoutGroup{}, nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.BlockoutGroup{}, nil, err
	}

	var updated domain.BlockoutGroup
	var claims []domain.InventoryClaim
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetBlockoutGroup(ctx, tx, propertyID, groupID)
		if err != nil {
			return err
		}
		updated, err = s.store.ReleaseBlockoutClaims(ctx, tx, propertyID, groupID, actor.UserID, unitIDs)
		if err != nil {
			return err
		}
		claims, err = s.store.ListClaimsByBlockoutGroup(ctx, tx, propertyID, groupID)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "blockout_group.released", "blockout_group", groupID.String(), before, map[string]any{
			"group":             updated,
			"released_unit_ids": unitIDs,
		})
	})
	return updated, claims, err
}

func (s *Service) UpdateBlockoutDates(ctx context.Context, actor Actor, propertyID, groupID uuid.UUID, startDate, endDate time.Time, version int) (domain.BlockoutGroup, []domain.InventoryClaim, error) {
	if err := s.requirePerm(actor, auth.PermBlocksWrite); err != nil {
		return domain.BlockoutGroup{}, nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.BlockoutGroup{}, nil, err
	}
	if startDate.IsZero() || endDate.IsZero() || !endDate.After(startDate) {
		return domain.BlockoutGroup{}, nil, domain.NewValidationError(map[string]string{
			"date_range": "end_date must be strictly after start_date",
		})
	}

	var updated domain.BlockoutGroup
	var claims []domain.InventoryClaim
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetBlockoutGroup(ctx, tx, propertyID, groupID)
		if err != nil {
			return err
		}
		updated, err = s.store.UpdateBlockoutGroupDates(ctx, tx, propertyID, groupID, startDate, endDate, version)
		if err != nil {
			return err
		}
		claims, err = s.store.ListClaimsByBlockoutGroup(ctx, tx, propertyID, groupID)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "blockout_group.dates_updated", "blockout_group", groupID.String(), before, updated)
	})
	return updated, claims, err
}

func (s *Service) ReinstateBlockout(ctx context.Context, actor Actor, propertyID, groupID uuid.UUID, claimIDs, unitIDs []uuid.UUID) (domain.BlockoutGroup, []domain.InventoryClaim, error) {
	if err := s.requirePerm(actor, auth.PermBlocksWrite); err != nil {
		return domain.BlockoutGroup{}, nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.BlockoutGroup{}, nil, err
	}

	var updated domain.BlockoutGroup
	var claims []domain.InventoryClaim
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetBlockoutGroup(ctx, tx, propertyID, groupID)
		if err != nil {
			return err
		}
		var reinstatedIDs []uuid.UUID
		updated, reinstatedIDs, err = s.store.ReinstateBlockoutClaims(ctx, tx, propertyID, groupID, claimIDs, unitIDs)
		if err != nil {
			return err
		}
		claims, err = s.store.ListClaimsByBlockoutGroup(ctx, tx, propertyID, groupID)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "blockout_group.reinstated", "blockout_group", groupID.String(), before, map[string]any{
			"group":                updated,
			"reinstated_claim_ids": reinstatedIDs,
			"unit_ids":             unitIDs,
		})
	})
	return updated, claims, err
}

func validateBlockoutResolvedUnits(in domain.BulkBlockoutInput, units []domain.Unit) error {
	if len(units) == 0 {
		return domain.NewValidationError(map[string]string{
			"unit_ids": "no active units matched the selected scope",
		})
	}
	if in.ScopeType == domain.BlockScopeUnit || in.ScopeType == domain.BlockScopeUnits {
		found := make(map[uuid.UUID]struct{}, len(units))
		for _, u := range units {
			found[u.ID] = struct{}{}
		}
		for _, id := range in.UnitIDs {
			if _, ok := found[id]; !ok {
				return domain.NewValidationError(map[string]string{
					"unit_ids": "one or more selected units do not exist, are inactive/retired, or are archived",
				})
			}
		}
	}
	return nil
}
