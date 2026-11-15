package postgres

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/domain"
)

// --- Unit Types ---

func (s *Store) ListUnitTypes(ctx context.Context, propertyID uuid.UUID, includeArchived bool) ([]domain.UnitType, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, property_id, code, name, category, description,
		       base_capacity_adults, max_adults, max_children, max_infants, max_guests,
		       pets_allowed, max_pets, base_nightly_rate_pence, tax_rate_bps,
		       min_nights, max_nights, amenities, metadata, is_active, sort_order,
		       version, archived_at, created_at, updated_at
		FROM unit_types
		WHERE property_id = $1
		  AND ($2::boolean OR archived_at IS NULL)
		ORDER BY sort_order ASC, name ASC
	`, propertyID, includeArchived)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.UnitType
	for rows.Next() {
		ut, err := scanUnitType(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, ut)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetUnitType(ctx context.Context, db DBTX, propertyID, id uuid.UUID) (domain.UnitType, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT id, property_id, code, name, category, description,
		       base_capacity_adults, max_adults, max_children, max_infants, max_guests,
		       pets_allowed, max_pets, base_nightly_rate_pence, tax_rate_bps,
		       min_nights, max_nights, amenities, metadata, is_active, sort_order,
		       version, archived_at, created_at, updated_at
		FROM unit_types
		WHERE property_id = $1 AND id = $2 AND archived_at IS NULL
	`, propertyID, id)
	ut, err := scanUnitType(row)
	return ut, MapErr(err)
}

func (s *Store) CreateUnitType(ctx context.Context, tx pgx.Tx, ut domain.UnitType) (domain.UnitType, error) {
	amenitiesJSON, err := json.Marshal(nonNilStrings(ut.Amenities))
	if err != nil {
		return domain.UnitType{}, err
	}
	meta := defaultJSONObject(ut.Metadata)

	row := tx.QueryRow(ctx, `
		INSERT INTO unit_types (
			id, property_id, code, name, category, description,
			base_capacity_adults, max_adults, max_children, max_infants, max_guests,
			pets_allowed, max_pets, base_nightly_rate_pence, tax_rate_bps,
			min_nights, max_nights, amenities, metadata, is_active, sort_order
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13, $14, $15,
			$16, $17, $18, $19, $20, $21
		)
		RETURNING id, property_id, code, name, category, description,
		          base_capacity_adults, max_adults, max_children, max_infants, max_guests,
		          pets_allowed, max_pets, base_nightly_rate_pence, tax_rate_bps,
		          min_nights, max_nights, amenities, metadata, is_active, sort_order,
		          version, archived_at, created_at, updated_at
	`,
		ut.ID, ut.PropertyID, ut.Code, ut.Name, string(ut.Category), ut.Description,
		ut.BaseCapacityAdults, ut.MaxAdults, ut.MaxChildren, ut.MaxInfants, ut.MaxGuests,
		ut.PetsAllowed, ut.MaxPets, ut.BaseNightlyRatePence, ut.TaxRateBPS,
		ut.MinNights, ut.MaxNights, amenitiesJSON, meta, ut.IsActive, ut.SortOrder,
	)
	out, err := scanUnitType(row)
	return out, MapErr(err)
}

func (s *Store) UpdateUnitType(ctx context.Context, tx pgx.Tx, ut domain.UnitType) (domain.UnitType, error) {
	amenitiesJSON, err := json.Marshal(nonNilStrings(ut.Amenities))
	if err != nil {
		return domain.UnitType{}, err
	}
	meta := defaultJSONObject(ut.Metadata)

	row := tx.QueryRow(ctx, `
		UPDATE unit_types
		SET code = $3,
		    name = $4,
		    category = $5,
		    description = $6,
		    base_capacity_adults = $7,
		    max_adults = $8,
		    max_children = $9,
		    max_infants = $10,
		    max_guests = $11,
		    pets_allowed = $12,
		    max_pets = $13,
		    base_nightly_rate_pence = $14,
		    tax_rate_bps = $15,
		    min_nights = $16,
		    max_nights = $17,
		    amenities = $18,
		    metadata = $19,
		    is_active = $20,
		    sort_order = $21,
		    version = version + 1,
		    updated_at = NOW()
		WHERE id = $1 AND property_id = $2 AND archived_at IS NULL AND version = $22
		RETURNING id, property_id, code, name, category, description,
		          base_capacity_adults, max_adults, max_children, max_infants, max_guests,
		          pets_allowed, max_pets, base_nightly_rate_pence, tax_rate_bps,
		          min_nights, max_nights, amenities, metadata, is_active, sort_order,
		          version, archived_at, created_at, updated_at
	`,
		ut.ID, ut.PropertyID, ut.Code, ut.Name, string(ut.Category), ut.Description,
		ut.BaseCapacityAdults, ut.MaxAdults, ut.MaxChildren, ut.MaxInfants, ut.MaxGuests,
		ut.PetsAllowed, ut.MaxPets, ut.BaseNightlyRatePence, ut.TaxRateBPS,
		ut.MinNights, ut.MaxNights, amenitiesJSON, meta, ut.IsActive, ut.SortOrder, ut.Version,
	)
	out, err := scanUnitType(row)
	if err == pgx.ErrNoRows {
		if _, gerr := s.GetUnitType(ctx, tx, ut.PropertyID, ut.ID); gerr == nil {
			return domain.UnitType{}, domain.ErrConflict
		}
		return domain.UnitType{}, domain.ErrNotFound
	}
	return out, MapErr(err)
}

func (s *Store) ArchiveUnitType(ctx context.Context, tx pgx.Tx, propertyID, id uuid.UUID) error {
	var activeUnits int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM units
		WHERE property_id = $1 AND unit_type_id = $2 AND archived_at IS NULL
	`, propertyID, id).Scan(&activeUnits); err != nil {
		return MapErr(err)
	}
	if activeUnits > 0 {
		return domain.NewValidationError(map[string]string{
			"unit_type_id": "cannot archive unit type with active units; archive or reassign units first",
		})
	}
	tag, err := tx.Exec(ctx, `
		UPDATE unit_types
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

type scannable interface {
	Scan(dest ...any) error
}

func scanUnitType(r scannable) (domain.UnitType, error) {
	var ut domain.UnitType
	var cat string
	var amenitiesRaw []byte
	err := r.Scan(
		&ut.ID, &ut.PropertyID, &ut.Code, &ut.Name, &cat, &ut.Description,
		&ut.BaseCapacityAdults, &ut.MaxAdults, &ut.MaxChildren, &ut.MaxInfants, &ut.MaxGuests,
		&ut.PetsAllowed, &ut.MaxPets, &ut.BaseNightlyRatePence, &ut.TaxRateBPS,
		&ut.MinNights, &ut.MaxNights, &amenitiesRaw, &ut.Metadata, &ut.IsActive, &ut.SortOrder,
		&ut.Version, &ut.ArchivedAt, &ut.CreatedAt, &ut.UpdatedAt,
	)
	if err != nil {
		return domain.UnitType{}, err
	}
	ut.Category = domain.UnitCategory(cat)
	if len(amenitiesRaw) > 0 {
		if err := json.Unmarshal(amenitiesRaw, &ut.Amenities); err != nil {
			ut.Amenities = nil
		}
	}
	return ut, nil
}

// --- Physical Units ---

func (s *Store) ListUnits(ctx context.Context, propertyID uuid.UUID, unitTypeID *uuid.UUID, includeArchived bool) ([]domain.Unit, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, u.property_id, u.unit_type_id, ut.name, u.code, u.name, u.zone,
		       u.status, u.access_notes, u.operational_notes, u.attributes, u.sort_order,
		       u.version, u.archived_at, u.created_at, u.updated_at
		FROM units u
		JOIN unit_types ut ON ut.id = u.unit_type_id
		WHERE u.property_id = $1
		  AND ($2::uuid IS NULL OR u.unit_type_id = $2)
		  AND ($3::boolean OR u.archived_at IS NULL)
		ORDER BY u.sort_order ASC, u.code ASC
	`, propertyID, unitTypeID, includeArchived)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.Unit
	for rows.Next() {
		u, err := scanUnit(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, u)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetUnit(ctx context.Context, db DBTX, propertyID, id uuid.UUID) (domain.Unit, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT u.id, u.property_id, u.unit_type_id, ut.name, u.code, u.name, u.zone,
		       u.status, u.access_notes, u.operational_notes, u.attributes, u.sort_order,
		       u.version, u.archived_at, u.created_at, u.updated_at
		FROM units u
		JOIN unit_types ut ON ut.id = u.unit_type_id
		WHERE u.property_id = $1 AND u.id = $2 AND u.archived_at IS NULL
	`, propertyID, id)
	u, err := scanUnit(row)
	return u, MapErr(err)
}

func (s *Store) CreateUnit(ctx context.Context, tx pgx.Tx, u domain.Unit) (domain.Unit, error) {
	attrs := defaultJSONObject(u.Attributes)
	row := tx.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO units (
				id, property_id, unit_type_id, code, name, zone,
				status, access_notes, operational_notes, attributes, sort_order
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				$7, $8, $9, $10, $11
			)
			RETURNING *
		)
		SELECT i.id, i.property_id, i.unit_type_id, ut.name, i.code, i.name, i.zone,
		       i.status, i.access_notes, i.operational_notes, i.attributes, i.sort_order,
		       i.version, i.archived_at, i.created_at, i.updated_at
		FROM inserted i
		JOIN unit_types ut ON ut.id = i.unit_type_id
	`,
		u.ID, u.PropertyID, u.UnitTypeID, u.Code, u.Name, u.Zone,
		string(u.Status), u.AccessNotes, u.OperationalNotes, attrs, u.SortOrder,
	)
	out, err := scanUnit(row)
	if err != nil {
		return out, MapErr(err)
	}
	if err := s.InheritFutureUnitBlockouts(ctx, tx, out); err != nil {
		return domain.Unit{}, err
	}
	return out, nil
}

func (s *Store) UpdateUnit(ctx context.Context, tx pgx.Tx, u domain.Unit) (domain.Unit, error) {
	attrs := defaultJSONObject(u.Attributes)
	row := tx.QueryRow(ctx, `
		WITH updated AS (
			UPDATE units
			SET unit_type_id = $3,
			    code = $4,
			    name = $5,
			    zone = $6,
			    status = $7,
			    access_notes = $8,
			    operational_notes = $9,
			    attributes = $10,
			    sort_order = $11,
			    version = version + 1,
			    updated_at = NOW()
			WHERE id = $1 AND property_id = $2 AND archived_at IS NULL AND version = $12
			RETURNING *
		)
		SELECT u.id, u.property_id, u.unit_type_id, ut.name, u.code, u.name, u.zone,
		       u.status, u.access_notes, u.operational_notes, u.attributes, u.sort_order,
		       u.version, u.archived_at, u.created_at, u.updated_at
		FROM updated u
		JOIN unit_types ut ON ut.id = u.unit_type_id
	`,
		u.ID, u.PropertyID, u.UnitTypeID, u.Code, u.Name, u.Zone,
		string(u.Status), u.AccessNotes, u.OperationalNotes, attrs, u.SortOrder, u.Version,
	)
	out, err := scanUnit(row)
	if err == pgx.ErrNoRows {
		if _, gerr := s.GetUnit(ctx, tx, u.PropertyID, u.ID); gerr == nil {
			return domain.Unit{}, domain.ErrConflict
		}
		return domain.Unit{}, domain.ErrNotFound
	}
	if err != nil {
		return out, MapErr(err)
	}
	if err := s.InheritFutureUnitBlockouts(ctx, tx, out); err != nil {
		return domain.Unit{}, err
	}
	return out, nil
}

func (s *Store) ArchiveUnit(ctx context.Context, tx pgx.Tx, propertyID, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		UPDATE units
		SET archived_at = NOW(), status = 'retired', updated_at = NOW(), version = version + 1
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

func scanUnit(r scannable) (domain.Unit, error) {
	var u domain.Unit
	var status string
	err := r.Scan(
		&u.ID, &u.PropertyID, &u.UnitTypeID, &u.UnitTypeName, &u.Code, &u.Name, &u.Zone,
		&status, &u.AccessNotes, &u.OperationalNotes, &u.Attributes, &u.SortOrder,
		&u.Version, &u.ArchivedAt, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return domain.Unit{}, err
	}
	u.Status = domain.UnitStatus(status)
	return u, nil
}

// --- Extras ---

func (s *Store) ListExtras(ctx context.Context, propertyID uuid.UUID, includeArchived bool) ([]domain.Extra, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, property_id, code, name, description, pricing_model,
		       price_pence, tax_rate_bps, max_quantity, is_active, sort_order,
		       metadata, version, archived_at, created_at, updated_at
		FROM extras
		WHERE property_id = $1
		  AND ($2::boolean OR archived_at IS NULL)
		ORDER BY sort_order ASC, name ASC
	`, propertyID, includeArchived)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.Extra
	for rows.Next() {
		ex, err := scanExtra(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, ex)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetExtra(ctx context.Context, db DBTX, propertyID, id uuid.UUID) (domain.Extra, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT id, property_id, code, name, description, pricing_model,
		       price_pence, tax_rate_bps, max_quantity, is_active, sort_order,
		       metadata, version, archived_at, created_at, updated_at
		FROM extras
		WHERE property_id = $1 AND id = $2 AND archived_at IS NULL
	`, propertyID, id)
	ex, err := scanExtra(row)
	return ex, MapErr(err)
}

func (s *Store) CreateExtra(ctx context.Context, tx pgx.Tx, e domain.Extra) (domain.Extra, error) {
	meta := defaultJSONObject(e.Metadata)
	row := tx.QueryRow(ctx, `
		INSERT INTO extras (
			id, property_id, code, name, description, pricing_model,
			price_pence, tax_rate_bps, max_quantity, is_active, sort_order, metadata
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12
		)
		RETURNING id, property_id, code, name, description, pricing_model,
		          price_pence, tax_rate_bps, max_quantity, is_active, sort_order,
		          metadata, version, archived_at, created_at, updated_at
	`,
		e.ID, e.PropertyID, e.Code, e.Name, e.Description, string(e.PricingModel),
		e.PricePence, e.TaxRateBPS, e.MaxQuantity, e.IsActive, e.SortOrder, meta,
	)
	out, err := scanExtra(row)
	return out, MapErr(err)
}

func (s *Store) UpdateExtra(ctx context.Context, tx pgx.Tx, e domain.Extra) (domain.Extra, error) {
	meta := defaultJSONObject(e.Metadata)
	row := tx.QueryRow(ctx, `
		UPDATE extras
		SET code = $3,
		    name = $4,
		    description = $5,
		    pricing_model = $6,
		    price_pence = $7,
		    tax_rate_bps = $8,
		    max_quantity = $9,
		    is_active = $10,
		    sort_order = $11,
		    metadata = $12,
		    version = version + 1,
		    updated_at = NOW()
		WHERE id = $1 AND property_id = $2 AND archived_at IS NULL AND version = $13
		RETURNING id, property_id, code, name, description, pricing_model,
		          price_pence, tax_rate_bps, max_quantity, is_active, sort_order,
		          metadata, version, archived_at, created_at, updated_at
	`,
		e.ID, e.PropertyID, e.Code, e.Name, e.Description, string(e.PricingModel),
		e.PricePence, e.TaxRateBPS, e.MaxQuantity, e.IsActive, e.SortOrder, meta, e.Version,
	)
	out, err := scanExtra(row)
	if err == pgx.ErrNoRows {
		if _, gerr := s.GetExtra(ctx, tx, e.PropertyID, e.ID); gerr == nil {
			return domain.Extra{}, domain.ErrConflict
		}
		return domain.Extra{}, domain.ErrNotFound
	}
	return out, MapErr(err)
}

func (s *Store) ArchiveExtra(ctx context.Context, tx pgx.Tx, propertyID, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		UPDATE extras
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

func scanExtra(r scannable) (domain.Extra, error) {
	var e domain.Extra
	var pm string
	err := r.Scan(
		&e.ID, &e.PropertyID, &e.Code, &e.Name, &e.Description, &pm,
		&e.PricePence, &e.TaxRateBPS, &e.MaxQuantity, &e.IsActive, &e.SortOrder,
		&e.Metadata, &e.Version, &e.ArchivedAt, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return domain.Extra{}, err
	}
	e.PricingModel = domain.PricingModel(pm)
	return e, nil
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func defaultJSONObject(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}
