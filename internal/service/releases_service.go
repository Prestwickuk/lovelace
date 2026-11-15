package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

func (s *Service) ListReleaseRules(ctx context.Context, actor Actor, propertyID uuid.UUID, includeCancelled bool) ([]domain.AvailabilityReleaseRule, error) {
	if err := s.requirePerm(actor, auth.PermReleasesRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListReleaseRules(ctx, nil, propertyID, includeCancelled)
}

func (s *Service) GetReleaseRule(ctx context.Context, actor Actor, propertyID, ruleID uuid.UUID) (domain.AvailabilityReleaseRule, error) {
	if err := s.requirePerm(actor, auth.PermReleasesRead); err != nil {
		return domain.AvailabilityReleaseRule{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.AvailabilityReleaseRule{}, err
	}
	return s.store.GetReleaseRule(ctx, nil, propertyID, ruleID)
}

func (s *Service) CreateReleaseRule(ctx context.Context, actor Actor, r domain.AvailabilityReleaseRule) (domain.AvailabilityReleaseRule, error) {
	if err := s.requirePerm(actor, auth.PermReleasesWrite); err != nil {
		return domain.AvailabilityReleaseRule{}, err
	}
	if err := s.requirePropertyScope(actor, r.PropertyID); err != nil {
		return domain.AvailabilityReleaseRule{}, err
	}
	if err := r.Validate(); err != nil {
		return domain.AvailabilityReleaseRule{}, err
	}

	id, err := postgres.NewUUID()
	if err != nil {
		return domain.AvailabilityReleaseRule{}, err
	}
	r.ID = id
	r.IsActive = true
	uid := actor.UserID
	r.CreatedBy = &uid
	r.UpdatedBy = &uid

	var created domain.AvailabilityReleaseRule
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.validateReleaseRuleTargets(ctx, tx, r); err != nil {
			return err
		}
		var txErr error
		created, txErr = s.store.CreateReleaseRule(ctx, tx, r)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &created.PropertyID, "release_rule.created", "availability_release_rule", created.ID.String(), nil, created)
	})
	return created, err
}

func (s *Service) UpdateReleaseRule(ctx context.Context, actor Actor, r domain.AvailabilityReleaseRule) (domain.AvailabilityReleaseRule, error) {
	if err := s.requirePerm(actor, auth.PermReleasesWrite); err != nil {
		return domain.AvailabilityReleaseRule{}, err
	}
	if err := s.requirePropertyScope(actor, r.PropertyID); err != nil {
		return domain.AvailabilityReleaseRule{}, err
	}
	if err := r.Validate(); err != nil {
		return domain.AvailabilityReleaseRule{}, err
	}
	uid := actor.UserID
	r.UpdatedBy = &uid

	var updated domain.AvailabilityReleaseRule
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetReleaseRule(ctx, tx, r.PropertyID, r.ID)
		if err != nil {
			return err
		}
		if before.CancelledAt != nil {
			return domain.NewValidationError(map[string]string{
				"rule": "cancelled release rule cannot be modified",
			})
		}
		if err := s.validateReleaseRuleTargets(ctx, tx, r); err != nil {
			return err
		}
		updated, err = s.store.UpdateReleaseRule(ctx, tx, r)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &updated.PropertyID, "release_rule.updated", "availability_release_rule", updated.ID.String(), before, updated)
	})
	return updated, err
}

func (s *Service) ManualReleaseRule(ctx context.Context, actor Actor, propertyID, ruleID uuid.UUID) (domain.AvailabilityReleaseRule, error) {
	if err := s.requirePerm(actor, auth.PermReleasesWrite); err != nil {
		return domain.AvailabilityReleaseRule{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.AvailabilityReleaseRule{}, err
	}

	var updated domain.AvailabilityReleaseRule
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetReleaseRule(ctx, tx, propertyID, ruleID)
		if err != nil {
			return err
		}
		if before.CancelledAt != nil {
			return domain.NewValidationError(map[string]string{
				"rule": "cancelled release rule cannot be manually released",
			})
		}
		var changed bool
		updated, changed, err = s.store.ManualReleaseRule(ctx, tx, propertyID, ruleID, actor.UserID)
		if err != nil {
			return err
		}
		if changed {
			return s.recordAudit(ctx, tx, actor, &propertyID, "release_rule.released_manually", "availability_release_rule", ruleID.String(), before, updated)
		}
		return nil
	})
	return updated, err
}

func (s *Service) CancelReleaseRule(ctx context.Context, actor Actor, propertyID, ruleID uuid.UUID) (domain.AvailabilityReleaseRule, error) {
	if err := s.requirePerm(actor, auth.PermReleasesWrite); err != nil {
		return domain.AvailabilityReleaseRule{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.AvailabilityReleaseRule{}, err
	}

	var updated domain.AvailabilityReleaseRule
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetReleaseRule(ctx, tx, propertyID, ruleID)
		if err != nil {
			return err
		}
		updated, err = s.store.CancelReleaseRule(ctx, tx, propertyID, ruleID, actor.UserID)
		if err != nil {
			return err
		}
		if before.CancelledAt == nil {
			return s.recordAudit(ctx, tx, actor, &propertyID, "release_rule.cancelled", "availability_release_rule", ruleID.String(), before, updated)
		}
		return nil
	})
	return updated, err
}

type UnitAvailabilityCheck struct {
	Available       bool                            `json:"available"`
	BlockingClaims  []domain.BlockoutConflict       `json:"blocking_claims,omitempty"`
	BlockingRelease *domain.AvailabilityReleaseRule `json:"blocking_release,omitempty"`
	Reason          string                          `json:"reason,omitempty"`
}

func (s *Service) CheckUnitAvailability(
	ctx context.Context,
	actor Actor,
	propertyID, unitID uuid.UUID,
	channelCode string,
	startDate, endDate time.Time,
) (UnitAvailabilityCheck, error) {
	if err := s.requirePerm(actor, auth.PermReservationsRead); err != nil {
		return UnitAvailabilityCheck{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return UnitAvailabilityCheck{}, err
	}
	unit, err := s.store.GetUnit(ctx, nil, propertyID, unitID)
	if err != nil {
		return UnitAvailabilityCheck{}, err
	}
	if unit.ArchivedAt != nil || unit.Status == domain.UnitStatusInactive || unit.Status == domain.UnitStatusRetired {
		return UnitAvailabilityCheck{
			Available: false,
			Reason:    fmt.Sprintf("Unit %s is %s and not bookable", unit.Code, unit.Status),
		}, nil
	}
	if channelCode == "" {
		channelCode = "ALL"
	}
	conflicts, err := s.store.FindClaimConflictsForChannel(ctx, nil, propertyID, []uuid.UUID{unitID}, startDate, endDate, domain.ClaimReservation, channelCode, nil, nil)
	if err != nil {
		return UnitAvailabilityCheck{}, err
	}
	if len(conflicts) > 0 {
		first := conflicts[0]
		reason := fmt.Sprintf("Occupied by %s (%s to %s)", first.ClaimType, domain.FormatDate(first.StartDate), domain.FormatDate(first.EndDate))
		if first.Reference != "" {
			reason = fmt.Sprintf("Booked by %s (%s)", first.Reference, first.GuestName)
		} else if first.Reason != "" {
			reason = fmt.Sprintf("%s: %s", first.ClaimType, first.Reason)
		}
		return UnitAvailabilityCheck{
			Available:      false,
			BlockingClaims: conflicts,
			Reason:         reason,
		}, nil
	}
	rules, err := s.store.ListActiveReleaseRulesInRange(ctx, nil, propertyID, startDate, endDate)
	if err != nil {
		return UnitAvailabilityCheck{}, err
	}
	allowed, blockingRule, reason := domain.EvaluateReleaseForStay(rules, unit, channelCode, startDate, endDate, s.nowUTC())
	if !allowed {
		return UnitAvailabilityCheck{
			Available:       false,
			BlockingRelease: blockingRule,
			Reason:          reason,
		}, nil
	}
	return UnitAvailabilityCheck{
		Available: true,
		Reason:    "Unit is available and released for booking",
	}, nil
}

func (s *Service) validateReleaseRuleTargets(ctx context.Context, tx pgx.Tx, r domain.AvailabilityReleaseRule) error {
	if _, err := s.store.GetPropertyByID(ctx, tx, r.PropertyID); err != nil {
		return err
	}
	switch r.ScopeType {
	case domain.ReleaseScopeUnit:
		unit, err := s.store.GetUnit(ctx, tx, r.PropertyID, *r.UnitID)
		if err != nil {
			if err == domain.ErrNotFound {
				return domain.NewValidationError(map[string]string{"unit_id": "invalid unit for this property"})
			}
			return err
		}
		if unit.ArchivedAt != nil || unit.Status == domain.UnitStatusRetired || unit.Status == domain.UnitStatusInactive {
			return domain.NewValidationError(map[string]string{"unit_id": "cannot create release rule for an inactive, retired, or archived unit"})
		}
	case domain.ReleaseScopeUnitType:
		if _, err := s.store.GetUnitType(ctx, tx, r.PropertyID, *r.UnitTypeID); err != nil {
			if err == domain.ErrNotFound {
				return domain.NewValidationError(map[string]string{"unit_type_id": "invalid unit type for this property"})
			}
			return err
		}
	}
	if r.ChannelCode != "ALL" {
		if err := s.ensureActiveChannel(ctx, tx, r.PropertyID, r.ChannelCode); err != nil {
			return err
		}
	}
	return nil
}
