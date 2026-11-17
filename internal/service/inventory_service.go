package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

// --- Properties ---

func (s *Service) ListProperties(ctx context.Context, actor Actor) ([]domain.Property, error) {
	if err := s.requirePerm(actor, auth.PermPropertyRead); err != nil {
		return nil, err
	}
	props, err := s.store.ListProperties(ctx)
	if err != nil {
		return nil, err
	}
	scopePropID := s.effectiveActorPropertyScope(ctx, nil, actor)
	if scopePropID == nil {
		return props, nil
	}
	var filtered []domain.Property
	for _, p := range props {
		if p.ID == *scopePropID {
			filtered = append(filtered, p)
		}
	}
	return filtered, nil
}

func (s *Service) GetProperty(ctx context.Context, actor Actor, id uuid.UUID) (domain.Property, error) {
	if err := s.requirePerm(actor, auth.PermPropertyRead); err != nil {
		return domain.Property{}, err
	}
	if err := s.requirePropertyScope(actor, id); err != nil {
		return domain.Property{}, err
	}
	return s.store.GetPropertyByID(ctx, nil, id)
}

func (s *Service) UpdateProperty(ctx context.Context, actor Actor, p domain.Property) (domain.Property, error) {
	if err := s.requirePerm(actor, auth.PermPropertyWrite); err != nil {
		return domain.Property{}, err
	}
	if err := s.requirePropertyScope(actor, p.ID); err != nil {
		return domain.Property{}, err
	}
	if err := p.Validate(); err != nil {
		return domain.Property{}, err
	}
	var updated domain.Property
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetPropertyByID(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		p.Slug = before.Slug // Slug is immutable after creation
		updated, err = s.store.UpdateProperty(ctx, tx, p)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &updated.ID, "property.updated", "property", updated.ID.String(), before, updated)
	})
	return updated, err
}

// --- Unit Types ---

func (s *Service) ListUnitTypes(ctx context.Context, actor Actor, propertyID uuid.UUID) ([]domain.UnitType, error) {
	if err := s.requirePerm(actor, auth.PermInventoryRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListUnitTypes(ctx, propertyID, false)
}

func (s *Service) GetUnitType(ctx context.Context, actor Actor, propertyID, id uuid.UUID) (domain.UnitType, error) {
	if err := s.requirePerm(actor, auth.PermInventoryRead); err != nil {
		return domain.UnitType{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.UnitType{}, err
	}
	return s.store.GetUnitType(ctx, nil, propertyID, id)
}

func (s *Service) CreateUnitType(ctx context.Context, actor Actor, ut domain.UnitType) (domain.UnitType, error) {
	if err := s.requirePerm(actor, auth.PermInventoryWrite); err != nil {
		return domain.UnitType{}, err
	}
	if err := s.requirePropertyScope(actor, ut.PropertyID); err != nil {
		return domain.UnitType{}, err
	}
	if err := ut.Validate(); err != nil {
		return domain.UnitType{}, err
	}
	id, err := postgres.NewUUID()
	if err != nil {
		return domain.UnitType{}, err
	}
	ut.ID = id

	var created domain.UnitType
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		created, txErr = s.store.CreateUnitType(ctx, tx, ut)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &created.PropertyID, "unit_type.created", "unit_type", created.ID.String(), nil, created)
	})
	return created, err
}

func (s *Service) UpdateUnitType(ctx context.Context, actor Actor, ut domain.UnitType) (domain.UnitType, error) {
	if err := s.requirePerm(actor, auth.PermInventoryWrite); err != nil {
		return domain.UnitType{}, err
	}
	if err := s.requirePropertyScope(actor, ut.PropertyID); err != nil {
		return domain.UnitType{}, err
	}
	if err := ut.Validate(); err != nil {
		return domain.UnitType{}, err
	}
	var updated domain.UnitType
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetUnitType(ctx, tx, ut.PropertyID, ut.ID)
		if err != nil {
			return err
		}
		updated, err = s.store.UpdateUnitType(ctx, tx, ut)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &updated.PropertyID, "unit_type.updated", "unit_type", updated.ID.String(), before, updated)
	})
	return updated, err
}

func (s *Service) ArchiveUnitType(ctx context.Context, actor Actor, propertyID, id uuid.UUID) error {
	if err := s.requirePerm(actor, auth.PermInventoryWrite); err != nil {
		return err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetUnitType(ctx, tx, propertyID, id)
		if err != nil {
			return err
		}
		if err := s.store.ArchiveUnitType(ctx, tx, propertyID, id); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "unit_type.archived", "unit_type", id.String(), before, nil)
	})
}

// --- Physical Units ---

func (s *Service) ListUnits(ctx context.Context, actor Actor, propertyID uuid.UUID, unitTypeID *uuid.UUID) ([]domain.Unit, error) {
	if err := s.requirePerm(actor, auth.PermInventoryRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListUnits(ctx, propertyID, unitTypeID, false)
}

func (s *Service) GetUnit(ctx context.Context, actor Actor, propertyID, id uuid.UUID) (domain.Unit, error) {
	if err := s.requirePerm(actor, auth.PermInventoryRead); err != nil {
		return domain.Unit{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.Unit{}, err
	}
	return s.store.GetUnit(ctx, nil, propertyID, id)
}

func (s *Service) CreateUnit(ctx context.Context, actor Actor, u domain.Unit) (domain.Unit, error) {
	if err := s.requirePerm(actor, auth.PermInventoryWrite); err != nil {
		return domain.Unit{}, err
	}
	if err := s.requirePropertyScope(actor, u.PropertyID); err != nil {
		return domain.Unit{}, err
	}
	if u.Status == "" {
		u.Status = domain.UnitStatusActive
	}
	if err := u.Validate(); err != nil {
		return domain.Unit{}, err
	}
	id, err := postgres.NewUUID()
	if err != nil {
		return domain.Unit{}, err
	}
	u.ID = id

	var created domain.Unit
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		// Ensure referenced unit_type belongs to the same property and is not archived.
		if _, err := s.store.GetUnitType(ctx, tx, u.PropertyID, u.UnitTypeID); err != nil {
			if err == domain.ErrNotFound {
				return domain.NewValidationError(map[string]string{"unit_type_id": "must belong to this property"})
			}
			return err
		}
		var txErr error
		created, txErr = s.store.CreateUnit(ctx, tx, u)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &created.PropertyID, "unit.created", "unit", created.ID.String(), nil, created)
	})
	return created, err
}

func (s *Service) UpdateUnit(ctx context.Context, actor Actor, u domain.Unit) (domain.Unit, error) {
	if err := s.requirePerm(actor, auth.PermInventoryWrite); err != nil {
		return domain.Unit{}, err
	}
	if err := s.requirePropertyScope(actor, u.PropertyID); err != nil {
		return domain.Unit{}, err
	}
	if err := u.Validate(); err != nil {
		return domain.Unit{}, err
	}
	var updated domain.Unit
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetUnit(ctx, tx, u.PropertyID, u.ID)
		if err != nil {
			return err
		}
		if _, err := s.store.GetUnitType(ctx, tx, u.PropertyID, u.UnitTypeID); err != nil {
			if err == domain.ErrNotFound {
				return domain.NewValidationError(map[string]string{"unit_type_id": "must belong to this property"})
			}
			return err
		}
		updated, err = s.store.UpdateUnit(ctx, tx, u)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &updated.PropertyID, "unit.updated", "unit", updated.ID.String(), before, updated)
	})
	return updated, err
}

func (s *Service) ArchiveUnit(ctx context.Context, actor Actor, propertyID, id uuid.UUID) error {
	if err := s.requirePerm(actor, auth.PermInventoryWrite); err != nil {
		return err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetUnit(ctx, tx, propertyID, id)
		if err != nil {
			return err
		}
		if err := s.store.ArchiveUnit(ctx, tx, propertyID, id); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "unit.archived", "unit", id.String(), before, nil)
	})
}

// --- Extras ---

func (s *Service) ListExtras(ctx context.Context, actor Actor, propertyID uuid.UUID) ([]domain.Extra, error) {
	if err := s.requirePerm(actor, auth.PermInventoryRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListExtras(ctx, propertyID, false)
}

func (s *Service) GetExtra(ctx context.Context, actor Actor, propertyID, id uuid.UUID) (domain.Extra, error) {
	if err := s.requirePerm(actor, auth.PermInventoryRead); err != nil {
		return domain.Extra{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.Extra{}, err
	}
	return s.store.GetExtra(ctx, nil, propertyID, id)
}

func (s *Service) CreateExtra(ctx context.Context, actor Actor, e domain.Extra) (domain.Extra, error) {
	if err := s.requirePerm(actor, auth.PermInventoryWrite); err != nil {
		return domain.Extra{}, err
	}
	if err := s.requirePropertyScope(actor, e.PropertyID); err != nil {
		return domain.Extra{}, err
	}
	if err := e.Validate(); err != nil {
		return domain.Extra{}, err
	}
	id, err := postgres.NewUUID()
	if err != nil {
		return domain.Extra{}, err
	}
	e.ID = id

	var created domain.Extra
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		created, txErr = s.store.CreateExtra(ctx, tx, e)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &created.PropertyID, "extra.created", "extra", created.ID.String(), nil, created)
	})
	return created, err
}

func (s *Service) UpdateExtra(ctx context.Context, actor Actor, e domain.Extra) (domain.Extra, error) {
	if err := s.requirePerm(actor, auth.PermInventoryWrite); err != nil {
		return domain.Extra{}, err
	}
	if err := s.requirePropertyScope(actor, e.PropertyID); err != nil {
		return domain.Extra{}, err
	}
	if err := e.Validate(); err != nil {
		return domain.Extra{}, err
	}
	var updated domain.Extra
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetExtra(ctx, tx, e.PropertyID, e.ID)
		if err != nil {
			return err
		}
		updated, err = s.store.UpdateExtra(ctx, tx, e)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &updated.PropertyID, "extra.updated", "extra", updated.ID.String(), before, updated)
	})
	return updated, err
}

func (s *Service) ArchiveExtra(ctx context.Context, actor Actor, propertyID, id uuid.UUID) error {
	if err := s.requirePerm(actor, auth.PermInventoryWrite); err != nil {
		return err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetExtra(ctx, tx, propertyID, id)
		if err != nil {
			return err
		}
		if err := s.store.ArchiveExtra(ctx, tx, propertyID, id); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "extra.archived", "extra", id.String(), before, nil)
	})
}
