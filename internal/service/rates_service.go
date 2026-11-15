package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

func (s *Service) ListRatePlans(ctx context.Context, actor Actor, propertyID uuid.UUID) ([]domain.RatePlan, error) {
	if err := s.requirePerm(actor, auth.PermRatesRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListRatePlans(ctx, propertyID)
}

func (s *Service) CreateRatePlan(ctx context.Context, actor Actor, rp domain.RatePlan) (domain.RatePlan, error) {
	if err := s.requirePerm(actor, auth.PermRatesWrite); err != nil {
		return domain.RatePlan{}, err
	}
	if err := s.requirePropertyScope(actor, rp.PropertyID); err != nil {
		return domain.RatePlan{}, err
	}
	if err := rp.Validate(); err != nil {
		return domain.RatePlan{}, err
	}
	id, err := postgres.NewUUID()
	if err != nil {
		return domain.RatePlan{}, err
	}
	rp.ID = id

	var created domain.RatePlan
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		created, txErr = s.store.CreateRatePlan(ctx, tx, rp)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &created.PropertyID, "rate_plan.created", "rate_plan", created.ID.String(), nil, created)
	})
	return created, err
}

func (s *Service) UpdateRatePlan(ctx context.Context, actor Actor, rp domain.RatePlan) (domain.RatePlan, error) {
	if err := s.requirePerm(actor, auth.PermRatesWrite); err != nil {
		return domain.RatePlan{}, err
	}
	if err := s.requirePropertyScope(actor, rp.PropertyID); err != nil {
		return domain.RatePlan{}, err
	}
	if err := rp.Validate(); err != nil {
		return domain.RatePlan{}, err
	}

	var updated domain.RatePlan
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetRatePlan(ctx, tx, rp.PropertyID, rp.ID)
		if err != nil {
			return err
		}
		updated, err = s.store.UpdateRatePlan(ctx, tx, rp)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &updated.PropertyID, "rate_plan.updated", "rate_plan", updated.ID.String(), before, updated)
	})
	return updated, err
}

func (s *Service) ListRateRules(ctx context.Context, actor Actor, propertyID uuid.UUID, ratePlanID, unitTypeID *uuid.UUID) ([]domain.RateRule, error) {
	if err := s.requirePerm(actor, auth.PermRatesRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListRateRules(ctx, propertyID, ratePlanID, unitTypeID)
}

func (s *Service) CreateRateRule(ctx context.Context, actor Actor, rr domain.RateRule) (domain.RateRule, error) {
	if err := s.requirePerm(actor, auth.PermRatesWrite); err != nil {
		return domain.RateRule{}, err
	}
	if err := s.requirePropertyScope(actor, rr.PropertyID); err != nil {
		return domain.RateRule{}, err
	}
	if err := rr.Validate(); err != nil {
		return domain.RateRule{}, err
	}
	id, err := postgres.NewUUID()
	if err != nil {
		return domain.RateRule{}, err
	}
	rr.ID = id

	var created domain.RateRule
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.store.GetRatePlan(ctx, tx, rr.PropertyID, rr.RatePlanID); err != nil {
			if err == domain.ErrNotFound {
				return domain.NewValidationError(map[string]string{"rate_plan_id": "must belong to this property"})
			}
			return err
		}
		if _, err := s.store.GetUnitType(ctx, tx, rr.PropertyID, rr.UnitTypeID); err != nil {
			if err == domain.ErrNotFound {
				return domain.NewValidationError(map[string]string{"unit_type_id": "must belong to this property"})
			}
			return err
		}
		var txErr error
		created, txErr = s.store.CreateRateRule(ctx, tx, rr)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &created.PropertyID, "rate_rule.created", "rate_rule", created.ID.String(), nil, created)
	})
	return created, err
}

func (s *Service) UpdateRateRule(ctx context.Context, actor Actor, rr domain.RateRule) (domain.RateRule, error) {
	if err := s.requirePerm(actor, auth.PermRatesWrite); err != nil {
		return domain.RateRule{}, err
	}
	if err := s.requirePropertyScope(actor, rr.PropertyID); err != nil {
		return domain.RateRule{}, err
	}
	if err := rr.Validate(); err != nil {
		return domain.RateRule{}, err
	}

	var updated domain.RateRule
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetRateRule(ctx, tx, rr.PropertyID, rr.ID)
		if err != nil {
			return err
		}
		updated, err = s.store.UpdateRateRule(ctx, tx, rr)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &updated.PropertyID, "rate_rule.updated", "rate_rule", updated.ID.String(), before, updated)
	})
	return updated, err
}

func (s *Service) ArchiveRateRule(ctx context.Context, actor Actor, propertyID, id uuid.UUID) error {
	if err := s.requirePerm(actor, auth.PermRatesWrite); err != nil {
		return err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetRateRule(ctx, tx, propertyID, id)
		if err != nil {
			return err
		}
		if err := s.store.ArchiveRateRule(ctx, tx, propertyID, id); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "rate_rule.archived", "rate_rule", id.String(), before, nil)
	})
}

// PreviewRates computes resolved nightly pricing without persisting any booking.
func (s *Service) PreviewRates(
	ctx context.Context,
	actor Actor,
	propertyID, unitTypeID, ratePlanID uuid.UUID,
	startDate, endDate time.Time,
) (domain.RatePreview, error) {
	if err := s.requirePerm(actor, auth.PermRatesRead); err != nil {
		return domain.RatePreview{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.RatePreview{}, err
	}
	prop, err := s.store.GetPropertyByID(ctx, nil, propertyID)
	if err != nil {
		return domain.RatePreview{}, err
	}
	ut, err := s.store.GetUnitType(ctx, nil, propertyID, unitTypeID)
	if err != nil {
		return domain.RatePreview{}, err
	}
	rp, err := s.store.GetRatePlan(ctx, nil, propertyID, ratePlanID)
	if err != nil {
		return domain.RatePreview{}, err
	}
	rules, err := s.store.ListRateRules(ctx, propertyID, &ratePlanID, &unitTypeID)
	if err != nil {
		return domain.RatePreview{}, err
	}
	return domain.CalculateRatePreview(ut, rp, rules, prop.Currency, startDate, endDate)
}
