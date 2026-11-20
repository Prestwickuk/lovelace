package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Prestwickuk/lovelace/internal/domain"
)

func (s *Store) dbConn(db DBTX) DBTX {
	if db == nil {
		return s.pool
	}
	return db
}

// validateMaintenanceTargetsTx verifies that any non-nil category, location, unit, and asset
// referenced by a maintenance operation belong to propertyID. When inheritAssetLinks is true
// and an asset is supplied, missing location/unit values inherit from the validated asset.
func (s *Store) validateMaintenanceTargetsTx(
	ctx context.Context,
	db DBTX,
	propertyID uuid.UUID,
	categoryID *uuid.UUID,
	locationID *uuid.UUID,
	unitID *uuid.UUID,
	assetID *uuid.UUID,
	inheritAssetLinks bool,
) (categoryName string, resolvedLoc *uuid.UUID, resolvedUnit *uuid.UUID, err error) {
	q := s.dbConn(db)
	resolvedLoc = locationID
	resolvedUnit = unitID

	if categoryID != nil {
		if err := q.QueryRow(ctx, `
			SELECT name
			FROM maintenance_categories
			WHERE property_id = $1 AND id = $2
		`, propertyID, *categoryID).Scan(&categoryName); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", nil, nil, fmt.Errorf("%w: maintenance category does not belong to this property", domain.ErrValidation)
			}
			return "", nil, nil, MapErr(err)
		}
	}
	if locationID != nil {
		var exists bool
		if err := q.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM maintenance_locations
				WHERE property_id = $1 AND id = $2
			)
		`, propertyID, *locationID).Scan(&exists); err != nil {
			return "", nil, nil, MapErr(err)
		}
		if !exists {
			return "", nil, nil, fmt.Errorf("%w: maintenance location does not belong to this property", domain.ErrValidation)
		}
	}
	if unitID != nil {
		var exists bool
		if err := q.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM units
				WHERE property_id = $1 AND id = $2
			)
		`, propertyID, *unitID).Scan(&exists); err != nil {
			return "", nil, nil, MapErr(err)
		}
		if !exists {
			return "", nil, nil, fmt.Errorf("%w: accommodation unit does not belong to this property", domain.ErrValidation)
		}
	}
	if assetID != nil {
		var assetLoc, assetUnit *uuid.UUID
		if err := q.QueryRow(ctx, `
			SELECT location_id, unit_id
			FROM maintenance_assets
			WHERE property_id = $1 AND id = $2
		`, propertyID, *assetID).Scan(&assetLoc, &assetUnit); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", nil, nil, fmt.Errorf("%w: maintenance asset does not belong to this property", domain.ErrValidation)
			}
			return "", nil, nil, MapErr(err)
		}
		if inheritAssetLinks {
			if resolvedLoc == nil && assetLoc != nil {
				resolvedLoc = assetLoc
			}
			if resolvedUnit == nil && assetUnit != nil {
				resolvedUnit = assetUnit
			}
		}
	}
	return categoryName, resolvedLoc, resolvedUnit, nil
}

// validateMaintenanceAssigneesTx verifies that any assigned staff member is active and
// authorised for propertyID (property_id IS NULL OR property_id = propertyID), and that any
// assigned contractor belongs to propertyID and is active.
func (s *Store) validateMaintenanceAssigneesTx(
	ctx context.Context,
	db DBTX,
	propertyID uuid.UUID,
	userID *uuid.UUID,
	contractorID *uuid.UUID,
) error {
	q := s.dbConn(db)
	if userID != nil {
		var exists bool
		if err := q.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM admin_users
				WHERE id = $1 AND is_active = TRUE AND (property_id IS NULL OR property_id = $2)
			)
		`, *userID, propertyID).Scan(&exists); err != nil {
			return MapErr(err)
		}
		if !exists {
			return fmt.Errorf("%w: assigned staff user is inactive or does not belong to this property", domain.ErrValidation)
		}
	}
	if contractorID != nil {
		var active bool
		if err := q.QueryRow(ctx, `
			SELECT is_active
			FROM maintenance_contractors
			WHERE property_id = $1 AND id = $2
		`, propertyID, *contractorID).Scan(&active); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: assigned contractor does not belong to this property", domain.ErrValidation)
			}
			return MapErr(err)
		}
		if !active {
			return fmt.Errorf("%w: assigned contractor is inactive", domain.ErrValidation)
		}
	}
	return nil
}

// ValidateWorkEntryBelongsToRequest verifies a work entry belongs to the specified property and request.
func (s *Store) ValidateWorkEntryBelongsToRequest(
	ctx context.Context,
	db DBTX,
	propertyID, requestID, workEntryID uuid.UUID,
) error {
	q := s.dbConn(db)
	var exists bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM maintenance_work_entries
			WHERE property_id = $1 AND request_id = $2 AND id = $3
		)
	`, propertyID, requestID, workEntryID).Scan(&exists); err != nil {
		return MapErr(err)
	}
	if !exists {
		return fmt.Errorf("%w: work entry does not belong to this request and property", domain.ErrValidation)
	}
	return nil
}

type defaultMaintCat struct {
	Code        string
	Name        string
	Description string
	SortOrder   int
}

var defaultMaintenanceCategories = []defaultMaintCat{
	{"PLUMBING", "Plumbing", "Pipes, taps, leaks, cisterns, and hot water plumbing", 10},
	{"ELECTRICAL", "Electrical", "Circuits, hook-up bollards, sockets, breakers, and power distribution", 20},
	{"HEATING", "Heating", "Boilers, stoves, heaters, and hot water generation", 30},
	{"WATER_SUPPLY", "Water supply", "Standpipes, supply pressure, pumps, and water storage", 40},
	{"DRAINAGE", "Drainage", "Drains, soakaways, gullies, and waste water systems", 50},
	{"ACCOMMODATION", "Accommodation", "Glamping pods, huts, tent pitches, doors, windows, and structures", 60},
	{"FURNITURE_FITTINGS", "Furniture and fittings", "Beds, seating, fixtures, shelving, and hardware", 70},
	{"CLEANING_HYGIENE", "Cleaning and hygiene", "Deep cleans, damp remediation, sanitisation, and bins", 80},
	{"SHOWER_FACILITIES", "Shower facilities", "Communal shower cubicles, mixers, cubicles, and drainage", 90},
	{"TOILET_FACILITIES", "Toilet facilities", "WC blocks, flushes, partitions, sinks, and ventilation", 100},
	{"COMMUNAL_BUILDINGS", "Communal buildings", "Farmhouse, reception, laundry, recreation barns, and roofs", 110},
	{"KITCHEN_FACILITIES", "Kitchen facilities", "Communal kitchen appliances, sinks, counters, and ventilation", 120},
	{"GROUNDS_LANDSCAPING", "Grounds and landscaping", "Grass cutting, trees, hedges, pitches, and drainage ditches", 130},
	{"FENCING_GATES", "Fencing and gates", "Boundary fences, gates, latches, hinges, and stock proofing", 140},
	{"PATHS_ACCESS", "Paths and access", "Footpaths, tracks, car parks, gravel surfaces, and signage", 150},
	{"LIGHTING", "Lighting", "Site bollards, path lights, floodlights, and emergency lighting", 160},
	{"NETWORK_COMMS", "Network and communications", "Wi-Fi access points, routers, telecoms, and office IT", 170},
	{"EQUIPMENT", "Equipment", "Site tools, mowers, generators, and shared equipment", 180},
	{"FIRE_SAFETY", "Fire and safety systems", "Fire extinguishers, alarms, CO detectors, and first-aid stations", 190},
	{"PEST_CONTROL", "Pest control", "Rodent, wasp, and wildlife proofing or callouts", 200},
	{"OTHER", "Other", "General property maintenance not covered elsewhere", 210},
}

type defaultMaintLoc struct {
	Code         string
	Name         string
	LocationType string
	Description  string
	SortOrder    int
}

var defaultMaintenanceLocations = []defaultMaintLoc{
	{"MAIN_FARMHOUSE", "Main farmhouse", "BUILDING", "Main farmhouse building and utilities", 10},
	{"RECEPTION", "Reception", "BUILDING", "Guest check-in and welcome area", 20},
	{"SHOWER_BLOCK", "Communal shower block", "FACILITY", "Shared guest shower block", 30},
	{"TOILETS", "Toilets", "FACILITY", "Shared WC and handwashing block", 40},
	{"COMMUNAL_KITCHEN", "Kitchen", "FACILITY", "Shared guest kitchen and washing-up area", 50},
	{"COVERED_REC", "Covered recreation area", "FACILITY", "Covered outdoor seating and shelter area", 60},
	{"LAUNDRY", "Laundry facilities", "FACILITY", "Washer/dryer room and guest utility space", 70},
	{"PLANT_ROOM", "Plant room", "INFRASTRUCTURE", "Boilers, pumps, manifolds, and controls", 80},
	{"ELECTRICAL_DIST", "Electrical distribution area", "INFRASTRUCTURE", "Main distribution boards and hook-up feeds", 90},
	{"WATER_INFRA", "Water infrastructure", "INFRASTRUCTURE", "Borehole, mains stopcock, filtration, and tanks", 100},
	{"GROUNDS", "Grounds", "GROUNDS", "Site lawns, hedges, trees, and shared open spaces", 110},
	{"CAR_PARK", "Car park", "ACCESS", "Guest parking bays and EV/turning areas", 120},
	{"ACCESS_ROADS", "Access roads", "ACCESS", "Site tracks, entrance lane, and pedestrian paths", 130},
	{"STORAGE", "Storage facilities", "BUILDING", "Maintenance shed, linen store, and equipment storage", 140},
}

// EnsureDefaultMaintenanceData seeds the built-in maintenance categories and standard
// campsite locations idempotently for a property.
func (s *Store) EnsureDefaultMaintenanceData(ctx context.Context, db DBTX, propertyID uuid.UUID) error {
	q := s.dbConn(db)
	for _, c := range defaultMaintenanceCategories {
		id, err := NewUUID()
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO maintenance_categories (id, property_id, code, name, description, is_active, sort_order)
			VALUES ($1, $2, $3, $4, $5, TRUE, $6)
			ON CONFLICT (property_id, code) DO NOTHING
		`, id, propertyID, c.Code, c.Name, c.Description, c.SortOrder); err != nil {
			return MapErr(err)
		}
	}
	for _, l := range defaultMaintenanceLocations {
		id, err := NewUUID()
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO maintenance_locations (id, property_id, code, name, location_type, description, is_active, sort_order)
			VALUES ($1, $2, $3, $4, $5, $6, TRUE, $7)
			ON CONFLICT (property_id, code) DO NOTHING
		`, id, propertyID, l.Code, l.Name, l.LocationType, l.Description, l.SortOrder); err != nil {
			return MapErr(err)
		}
	}
	return nil
}

func (s *Store) ListMaintenanceAssignableStaff(ctx context.Context, propertyID uuid.UUID) ([]domain.OpsStaffMember, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, full_name, role
		FROM admin_users
		WHERE is_active = TRUE
		  AND role IN ('owner_admin', 'manager', 'staff', 'maintenance_staff')
		  AND (property_id IS NULL OR property_id = $1)
		ORDER BY full_name ASC
	`, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.OpsStaffMember
	for rows.Next() {
		var m domain.OpsStaffMember
		var role string
		if err := rows.Scan(&m.ID, &m.FullName, &role); err != nil {
			return nil, MapErr(err)
		}
		m.Role = domain.Role(role)
		out = append(out, m)
	}
	return out, MapErr(rows.Err())
}

// ---------------- Categories ----------------

func (s *Store) ListMaintenanceCategories(ctx context.Context, db DBTX, propertyID uuid.UUID, includeInactive bool) ([]domain.MaintenanceCategory, error) {
	q := s.dbConn(db)
	query := `
		SELECT id, property_id, code, name, description, is_active, sort_order, created_at, updated_at
		FROM maintenance_categories
		WHERE property_id = $1
	`
	if !includeInactive {
		query += ` AND is_active = TRUE`
	}
	query += ` ORDER BY sort_order ASC, name ASC`
	rows, err := q.Query(ctx, query, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.MaintenanceCategory
	for rows.Next() {
		c, err := scanMaintenanceCategory(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, c)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetMaintenanceCategory(ctx context.Context, db DBTX, propertyID, id uuid.UUID) (domain.MaintenanceCategory, error) {
	q := s.dbConn(db)
	row := q.QueryRow(ctx, `
		SELECT id, property_id, code, name, description, is_active, sort_order, created_at, updated_at
		FROM maintenance_categories
		WHERE property_id = $1 AND id = $2
	`, propertyID, id)
	c, err := scanMaintenanceCategory(row)
	return c, MapErr(err)
}

func (s *Store) CreateMaintenanceCategory(ctx context.Context, tx pgx.Tx, c domain.MaintenanceCategory) (domain.MaintenanceCategory, error) {
	id, err := NewUUID()
	if err != nil {
		return domain.MaintenanceCategory{}, err
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO maintenance_categories (id, property_id, code, name, description, is_active, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, property_id, code, name, description, is_active, sort_order, created_at, updated_at
	`, id, c.PropertyID, c.Code, c.Name, c.Description, c.IsActive, c.SortOrder)
	out, err := scanMaintenanceCategory(row)
	return out, MapErr(err)
}

func (s *Store) UpdateMaintenanceCategory(ctx context.Context, tx pgx.Tx, c domain.MaintenanceCategory) (domain.MaintenanceCategory, error) {
	row := tx.QueryRow(ctx, `
		UPDATE maintenance_categories
		SET code = $3, name = $4, description = $5, is_active = $6, sort_order = $7, updated_at = NOW()
		WHERE property_id = $1 AND id = $2
		RETURNING id, property_id, code, name, description, is_active, sort_order, created_at, updated_at
	`, c.PropertyID, c.ID, c.Code, c.Name, c.Description, c.IsActive, c.SortOrder)
	out, err := scanMaintenanceCategory(row)
	return out, MapErr(err)
}

func scanMaintenanceCategory(r scannable) (domain.MaintenanceCategory, error) {
	var c domain.MaintenanceCategory
	err := r.Scan(&c.ID, &c.PropertyID, &c.Code, &c.Name, &c.Description, &c.IsActive, &c.SortOrder, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// ---------------- Locations ----------------

const selectLocationColumns = `
	ml.id, ml.property_id, ml.code, ml.name, ml.location_type, ml.description,
	ml.unit_id, COALESCE(u.code, ''), COALESCE(u.name, ''),
	ml.is_active, ml.sort_order, ml.created_at, ml.updated_at
`

func (s *Store) ListMaintenanceLocations(ctx context.Context, db DBTX, propertyID uuid.UUID, includeInactive bool) ([]domain.MaintenanceLocation, error) {
	q := s.dbConn(db)
	query := `SELECT ` + selectLocationColumns + `
		FROM maintenance_locations ml
		LEFT JOIN units u ON u.id = ml.unit_id
		WHERE ml.property_id = $1`
	if !includeInactive {
		query += ` AND ml.is_active = TRUE`
	}
	query += ` ORDER BY ml.sort_order ASC, ml.name ASC`
	rows, err := q.Query(ctx, query, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.MaintenanceLocation
	for rows.Next() {
		l, err := scanMaintenanceLocation(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, l)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetMaintenanceLocation(ctx context.Context, db DBTX, propertyID, id uuid.UUID) (domain.MaintenanceLocation, error) {
	q := s.dbConn(db)
	row := q.QueryRow(ctx, `SELECT `+selectLocationColumns+`
		FROM maintenance_locations ml
		LEFT JOIN units u ON u.id = ml.unit_id
		WHERE ml.property_id = $1 AND ml.id = $2`, propertyID, id)
	l, err := scanMaintenanceLocation(row)
	return l, MapErr(err)
}

func (s *Store) CreateMaintenanceLocation(ctx context.Context, tx pgx.Tx, l domain.MaintenanceLocation) (domain.MaintenanceLocation, error) {
	if _, _, _, err := s.validateMaintenanceTargetsTx(ctx, tx, l.PropertyID, nil, nil, l.UnitID, nil, false); err != nil {
		return domain.MaintenanceLocation{}, err
	}
	id, err := NewUUID()
	if err != nil {
		return domain.MaintenanceLocation{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO maintenance_locations (id, property_id, code, name, location_type, description, unit_id, is_active, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, id, l.PropertyID, l.Code, l.Name, l.LocationType, l.Description, l.UnitID, l.IsActive, l.SortOrder); err != nil {
		return domain.MaintenanceLocation{}, MapErr(err)
	}
	return s.GetMaintenanceLocation(ctx, tx, l.PropertyID, id)
}

func (s *Store) UpdateMaintenanceLocation(ctx context.Context, tx pgx.Tx, l domain.MaintenanceLocation) (domain.MaintenanceLocation, error) {
	if _, _, _, err := s.validateMaintenanceTargetsTx(ctx, tx, l.PropertyID, nil, nil, l.UnitID, nil, false); err != nil {
		return domain.MaintenanceLocation{}, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE maintenance_locations
		SET code = $3, name = $4, location_type = $5, description = $6, unit_id = $7, is_active = $8, sort_order = $9, updated_at = NOW()
		WHERE property_id = $1 AND id = $2
	`, l.PropertyID, l.ID, l.Code, l.Name, l.LocationType, l.Description, l.UnitID, l.IsActive, l.SortOrder)
	if err != nil {
		return domain.MaintenanceLocation{}, MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.MaintenanceLocation{}, domain.ErrNotFound
	}
	return s.GetMaintenanceLocation(ctx, tx, l.PropertyID, l.ID)
}

func scanMaintenanceLocation(r scannable) (domain.MaintenanceLocation, error) {
	var l domain.MaintenanceLocation
	err := r.Scan(
		&l.ID, &l.PropertyID, &l.Code, &l.Name, &l.LocationType, &l.Description,
		&l.UnitID, &l.UnitCode, &l.UnitName,
		&l.IsActive, &l.SortOrder, &l.CreatedAt, &l.UpdatedAt,
	)
	return l, err
}

// ---------------- Assets ----------------

const selectAssetColumns = `
	a.id, a.property_id, a.asset_code, a.name, a.description,
	a.location_id, COALESCE(ml.name, ''),
	a.unit_id, COALESCE(u.code, ''), COALESCE(u.name, ''),
	a.asset_category, a.manufacturer, a.model, a.serial_number,
	a.installed_on, a.warranty_expires_on, a.warranty_notes, a.status, a.notes,
	a.version, a.created_at, a.updated_at
`

func (s *Store) ListMaintenanceAssets(ctx context.Context, db DBTX, propertyID uuid.UUID, includeRetired bool) ([]domain.MaintenanceAsset, error) {
	q := s.dbConn(db)
	query := `SELECT ` + selectAssetColumns + `
		FROM maintenance_assets a
		LEFT JOIN maintenance_locations ml ON ml.id = a.location_id
		LEFT JOIN units u ON u.id = a.unit_id
		WHERE a.property_id = $1`
	if !includeRetired {
		query += ` AND a.status = 'ACTIVE'`
	}
	query += ` ORDER BY a.name ASC, a.asset_code ASC`
	rows, err := q.Query(ctx, query, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.MaintenanceAsset
	for rows.Next() {
		a, err := scanMaintenanceAsset(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, a)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetMaintenanceAsset(ctx context.Context, db DBTX, propertyID, id uuid.UUID) (domain.MaintenanceAsset, error) {
	q := s.dbConn(db)
	row := q.QueryRow(ctx, `SELECT `+selectAssetColumns+`
		FROM maintenance_assets a
		LEFT JOIN maintenance_locations ml ON ml.id = a.location_id
		LEFT JOIN units u ON u.id = a.unit_id
		WHERE a.property_id = $1 AND a.id = $2`, propertyID, id)
	a, err := scanMaintenanceAsset(row)
	return a, MapErr(err)
}

func (s *Store) CreateMaintenanceAsset(ctx context.Context, tx pgx.Tx, a domain.MaintenanceAsset, actorID uuid.UUID) (domain.MaintenanceAsset, error) {
	if _, _, _, err := s.validateMaintenanceTargetsTx(ctx, tx, a.PropertyID, nil, a.LocationID, a.UnitID, nil, false); err != nil {
		return domain.MaintenanceAsset{}, err
	}
	id, err := NewUUID()
	if err != nil {
		return domain.MaintenanceAsset{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO maintenance_assets (
			id, property_id, asset_code, name, description, location_id, unit_id,
			asset_category, manufacturer, model, serial_number,
			installed_on, warranty_expires_on, warranty_notes, status, notes,
			created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17)
	`, id, a.PropertyID, a.AssetCode, a.Name, a.Description, a.LocationID, a.UnitID,
		a.AssetCategory, a.Manufacturer, a.Model, a.SerialNumber,
		a.InstalledOn, a.WarrantyExpiresOn, a.WarrantyNotes, string(a.Status), a.Notes, actorID); err != nil {
		return domain.MaintenanceAsset{}, MapErr(err)
	}
	return s.GetMaintenanceAsset(ctx, tx, a.PropertyID, id)
}

func (s *Store) UpdateMaintenanceAsset(ctx context.Context, tx pgx.Tx, a domain.MaintenanceAsset, actorID uuid.UUID) (domain.MaintenanceAsset, error) {
	if _, _, _, err := s.validateMaintenanceTargetsTx(ctx, tx, a.PropertyID, nil, a.LocationID, a.UnitID, nil, false); err != nil {
		return domain.MaintenanceAsset{}, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE maintenance_assets
		SET asset_code = $3, name = $4, description = $5, location_id = $6, unit_id = $7,
		    asset_category = $8, manufacturer = $9, model = $10, serial_number = $11,
		    installed_on = $12, warranty_expires_on = $13, warranty_notes = $14,
		    status = $15, notes = $16, updated_by = $17, version = version + 1, updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND version = $18
	`, a.PropertyID, a.ID, a.AssetCode, a.Name, a.Description, a.LocationID, a.UnitID,
		a.AssetCategory, a.Manufacturer, a.Model, a.SerialNumber,
		a.InstalledOn, a.WarrantyExpiresOn, a.WarrantyNotes, string(a.Status), a.Notes, actorID, a.Version)
	if err != nil {
		return domain.MaintenanceAsset{}, MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.MaintenanceAsset{}, domain.ErrConflict
	}
	return s.GetMaintenanceAsset(ctx, tx, a.PropertyID, a.ID)
}

func scanMaintenanceAsset(r scannable) (domain.MaintenanceAsset, error) {
	var a domain.MaintenanceAsset
	var status string
	err := r.Scan(
		&a.ID, &a.PropertyID, &a.AssetCode, &a.Name, &a.Description,
		&a.LocationID, &a.LocationName,
		&a.UnitID, &a.UnitCode, &a.UnitName,
		&a.AssetCategory, &a.Manufacturer, &a.Model, &a.SerialNumber,
		&a.InstalledOn, &a.WarrantyExpiresOn, &a.WarrantyNotes, &status, &a.Notes,
		&a.Version, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return domain.MaintenanceAsset{}, err
	}
	a.Status = domain.MaintenanceAssetStatus(status)
	return a, nil
}

// ---------------- Contractors ----------------

func (s *Store) ListMaintenanceContractors(ctx context.Context, db DBTX, propertyID uuid.UUID, includeInactive bool) ([]domain.MaintenanceContractor, error) {
	q := s.dbConn(db)
	query := `
		SELECT id, property_id, name, company, trade, email, phone, notes, is_active, created_at, updated_at
		FROM maintenance_contractors
		WHERE property_id = $1
	`
	if !includeInactive {
		query += ` AND is_active = TRUE`
	}
	query += ` ORDER BY name ASC, company ASC`
	rows, err := q.Query(ctx, query, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.MaintenanceContractor
	for rows.Next() {
		c, err := scanMaintenanceContractor(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, c)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetMaintenanceContractor(ctx context.Context, db DBTX, propertyID, id uuid.UUID) (domain.MaintenanceContractor, error) {
	q := s.dbConn(db)
	row := q.QueryRow(ctx, `
		SELECT id, property_id, name, company, trade, email, phone, notes, is_active, created_at, updated_at
		FROM maintenance_contractors
		WHERE property_id = $1 AND id = $2
	`, propertyID, id)
	c, err := scanMaintenanceContractor(row)
	return c, MapErr(err)
}

func (s *Store) CreateMaintenanceContractor(ctx context.Context, tx pgx.Tx, c domain.MaintenanceContractor) (domain.MaintenanceContractor, error) {
	id, err := NewUUID()
	if err != nil {
		return domain.MaintenanceContractor{}, err
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO maintenance_contractors (id, property_id, name, company, trade, email, phone, notes, is_active)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, property_id, name, company, trade, email, phone, notes, is_active, created_at, updated_at
	`, id, c.PropertyID, c.Name, c.Company, c.Trade, c.Email, c.Phone, c.Notes, c.IsActive)
	out, err := scanMaintenanceContractor(row)
	return out, MapErr(err)
}

func (s *Store) UpdateMaintenanceContractor(ctx context.Context, tx pgx.Tx, c domain.MaintenanceContractor) (domain.MaintenanceContractor, error) {
	row := tx.QueryRow(ctx, `
		UPDATE maintenance_contractors
		SET name = $3, company = $4, trade = $5, email = $6, phone = $7, notes = $8, is_active = $9, updated_at = NOW()
		WHERE property_id = $1 AND id = $2
		RETURNING id, property_id, name, company, trade, email, phone, notes, is_active, created_at, updated_at
	`, c.PropertyID, c.ID, c.Name, c.Company, c.Trade, c.Email, c.Phone, c.Notes, c.IsActive)
	out, err := scanMaintenanceContractor(row)
	return out, MapErr(err)
}

func scanMaintenanceContractor(r scannable) (domain.MaintenanceContractor, error) {
	var c domain.MaintenanceContractor
	err := r.Scan(&c.ID, &c.PropertyID, &c.Name, &c.Company, &c.Trade, &c.Email, &c.Phone, &c.Notes, &c.IsActive, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// ---------------- Requests ----------------

const selectMaintRequestColumns = `
	mr.id, mr.property_id, mr.reference, mr.title, mr.description,
	mr.category_id, COALESCE(NULLIF(mr.category_name_snapshot, ''), mc.name, ''), COALESCE(mc.code, ''),
	mr.priority, mr.status, mr.work_type,
	mr.location_id, COALESCE(ml.name, ''),
	mr.unit_id, COALESCE(u.code, ''), COALESCE(u.name, ''),
	mr.asset_id, COALESCE(a.asset_code, ''), COALESCE(a.name, ''),
	mr.schedule_id, mr.scheduled_occurrence_date,
	mr.reported_by, COALESCE(rep.full_name, ''),
	mr.assigned_user_id, COALESCE(asg.full_name, ''),
	mr.assigned_contractor_id, COALESCE(NULLIF(trim(concat_ws(' — ', ctr.name, NULLIF(ctr.company, ''))), ''), ''),
	mr.reported_at, mr.target_date, mr.scheduled_for, mr.started_at, mr.completed_at, mr.closed_at,
	mr.guest_impact, mr.safety_impact, mr.availability_impact, mr.inspection_required, mr.internal_notes,
	(mr.target_date IS NOT NULL AND mr.target_date < $2 AND mr.status NOT IN ('COMPLETED', 'CANCELLED', 'CLOSED')) AS overdue,
	EXISTS (
		SELECT 1
		FROM maintenance_restriction_links mrl
		JOIN blockout_groups bg ON bg.id = mrl.blockout_group_id
		WHERE mrl.request_id = mr.id
		  AND mrl.released_at IS NULL
		  AND bg.status IN ('ACTIVE', 'PARTIALLY_RELEASED')
	) AS has_active_restriction,
	COALESCE((
		SELECT mi.result
		FROM maintenance_inspections mi
		WHERE mi.request_id = mr.id
		  AND mr.completed_at IS NOT NULL
		  AND mi.inspected_at > mr.completed_at
		ORDER BY mi.inspected_at DESC
		LIMIT 1
	), '') AS latest_inspection_result,
	mr.version, mr.created_at, mr.updated_at
`

const fromMaintRequestJoins = `
	FROM maintenance_requests mr
	LEFT JOIN maintenance_categories mc ON mc.id = mr.category_id
	LEFT JOIN maintenance_locations ml ON ml.id = mr.location_id
	LEFT JOIN units u ON u.id = mr.unit_id
	LEFT JOIN maintenance_assets a ON a.id = mr.asset_id
	LEFT JOIN admin_users rep ON rep.id = mr.reported_by
	LEFT JOIN admin_users asg ON asg.id = mr.assigned_user_id
	LEFT JOIN maintenance_contractors ctr ON ctr.id = mr.assigned_contractor_id
`

func (s *Store) nextMaintenanceReferenceTx(ctx context.Context, tx pgx.Tx, year int) (string, error) {
	var seq int64
	if err := tx.QueryRow(ctx, `SELECT nextval('maintenance_ref_seq')`).Scan(&seq); err != nil {
		return "", MapErr(err)
	}
	return fmt.Sprintf("MNT-%04d-%04d", year, seq), nil
}

func (s *Store) CreateMaintenanceRequest(ctx context.Context, tx pgx.Tx, req domain.MaintenanceRequest, actorID uuid.UUID, refTime time.Time) (domain.MaintenanceRequest, error) {
	catName, resolvedLoc, resolvedUnit, err := s.validateMaintenanceTargetsTx(
		ctx, tx, req.PropertyID, &req.CategoryID, req.LocationID, req.UnitID, req.AssetID, true,
	)
	if err != nil {
		return domain.MaintenanceRequest{}, err
	}
	req.LocationID = resolvedLoc
	req.UnitID = resolvedUnit

	if err := s.validateMaintenanceAssigneesTx(ctx, tx, req.PropertyID, req.AssignedUserID, req.AssignedContractorID); err != nil {
		return domain.MaintenanceRequest{}, err
	}

	id, err := NewUUID()
	if err != nil {
		return domain.MaintenanceRequest{}, err
	}
	ref, err := s.nextMaintenanceReferenceTx(ctx, tx, refTime.UTC().Year())
	if err != nil {
		return domain.MaintenanceRequest{}, err
	}
	if req.ReportedAt.IsZero() {
		req.ReportedAt = refTime.UTC()
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO maintenance_requests (
			id, property_id, reference, title, description,
			category_id, category_name_snapshot, priority, status, work_type,
			location_id, unit_id, asset_id, schedule_id, scheduled_occurrence_date,
			reported_by, assigned_user_id, assigned_contractor_id,
			reported_at, target_date, scheduled_for,
			guest_impact, safety_impact, availability_impact, inspection_required, internal_notes,
			created_by, updated_by
		) VALUES (
			$1,$2,$3,$4,$5,
			$6,$7,$8,$9,$10,
			$11,$12,$13,$14,$15,
			$16,$17,$18,
			$19,$20,$21,
			$22,$23,$24,$25,$26,
			$27,$27
		)
	`, id, req.PropertyID, ref, req.Title, req.Description,
		req.CategoryID, catName, string(req.Priority), string(req.Status), string(req.WorkType),
		req.LocationID, req.UnitID, req.AssetID, req.ScheduleID, req.ScheduledOccurrenceDate,
		req.ReportedBy, req.AssignedUserID, req.AssignedContractorID,
		req.ReportedAt, req.TargetDate, req.ScheduledFor,
		req.GuestImpact, req.SafetyImpact, string(req.AvailabilityImpact), req.InspectionRequired, req.InternalNotes,
		actorID); err != nil {
		return domain.MaintenanceRequest{}, MapErr(err)
	}

	if err := s.appendStatusHistoryTx(ctx, tx, req.PropertyID, id, "", string(req.Status), "Initial report", &actorID); err != nil {
		return domain.MaintenanceRequest{}, err
	}
	if req.AssignedUserID != nil || req.AssignedContractorID != nil {
		if err := s.appendAssignmentTx(ctx, tx, req.PropertyID, id, req.AssignedUserID, req.AssignedContractorID, &actorID, req.TargetDate, "Assigned on creation"); err != nil {
			return domain.MaintenanceRequest{}, err
		}
	}

	today := time.Date(refTime.UTC().Year(), refTime.UTC().Month(), refTime.UTC().Day(), 0, 0, 0, 0, time.UTC)
	return s.GetMaintenanceRequest(ctx, tx, req.PropertyID, id, today)
}

func (s *Store) GetMaintenanceRequest(ctx context.Context, db DBTX, propertyID, requestID uuid.UUID, today time.Time) (domain.MaintenanceRequest, error) {
	q := s.dbConn(db)
	row := q.QueryRow(ctx, `SELECT `+selectMaintRequestColumns+fromMaintRequestJoins+`
		WHERE mr.property_id = $1 AND mr.id = $3`, propertyID, today, requestID)
	req, err := scanMaintenanceRequest(row)
	return req, MapErr(err)
}

func (s *Store) ListMaintenanceRequests(ctx context.Context, db DBTX, propertyID uuid.UUID, filter domain.MaintenanceRequestFilter, today time.Time) ([]domain.MaintenanceRequest, error) {
	q := s.dbConn(db)
	args := []any{propertyID, today}
	where := []string{"mr.property_id = $1"}

	addArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	switch strings.ToUpper(strings.TrimSpace(filter.Status)) {
	case "", "ALL":
	case "OPEN":
		where = append(where, "mr.status NOT IN ('COMPLETED', 'CANCELLED', 'CLOSED')")
	case "AWAITING_INSPECTION":
		where = append(where, `mr.status = 'COMPLETED' AND mr.inspection_required = TRUE AND (
			mr.completed_at IS NULL OR COALESCE((
				SELECT mi.result
				FROM maintenance_inspections mi
				WHERE mi.request_id = mr.id
				  AND mi.inspected_at > mr.completed_at
				ORDER BY mi.inspected_at DESC
				LIMIT 1
			), '') <> 'PASSED'
		)`)
	default:
		p := addArg(strings.ToUpper(strings.TrimSpace(filter.Status)))
		where = append(where, "mr.status = "+p)
	}
	if filter.Priority != "" && strings.ToUpper(filter.Priority) != "ALL" {
		p := addArg(strings.ToUpper(strings.TrimSpace(filter.Priority)))
		where = append(where, "mr.priority = "+p)
	}
	if filter.CategoryID != nil && *filter.CategoryID != uuid.Nil {
		p := addArg(*filter.CategoryID)
		where = append(where, "mr.category_id = "+p)
	}
	if filter.LocationID != nil && *filter.LocationID != uuid.Nil {
		p := addArg(*filter.LocationID)
		where = append(where, "mr.location_id = "+p)
	}
	if filter.UnitID != nil && *filter.UnitID != uuid.Nil {
		p := addArg(*filter.UnitID)
		where = append(where, "mr.unit_id = "+p)
	}
	if filter.AssetID != nil && *filter.AssetID != uuid.Nil {
		p := addArg(*filter.AssetID)
		where = append(where, "mr.asset_id = "+p)
	}
	if filter.AssignedTo != nil && *filter.AssignedTo != uuid.Nil {
		p := addArg(*filter.AssignedTo)
		where = append(where, "mr.assigned_user_id = "+p)
	}
	if filter.WorkType != "" && strings.ToUpper(filter.WorkType) != "ALL" {
		p := addArg(strings.ToUpper(strings.TrimSpace(filter.WorkType)))
		where = append(where, "mr.work_type = "+p)
	}
	if filter.OverdueOnly {
		where = append(where, "mr.target_date IS NOT NULL AND mr.target_date < $2 AND mr.status NOT IN ('COMPLETED', 'CANCELLED', 'CLOSED')")
	}
	if filter.Restricted {
		where = append(where, `EXISTS (
			SELECT 1 FROM maintenance_restriction_links mrl
			JOIN blockout_groups bg ON bg.id = mrl.blockout_group_id
			WHERE mrl.request_id = mr.id AND mrl.released_at IS NULL AND bg.status IN ('ACTIVE', 'PARTIALLY_RELEASED')
		)`)
	}
	if filter.DateFrom != nil {
		p := addArg(*filter.DateFrom)
		where = append(where, "COALESCE(mr.scheduled_for, mr.target_date, mr.reported_at::date) >= "+p)
	}
	if filter.DateTo != nil {
		p := addArg(*filter.DateTo)
		where = append(where, "COALESCE(mr.scheduled_for, mr.target_date, mr.reported_at::date) <= "+p)
	}
	if qStr := strings.TrimSpace(filter.Search); qStr != "" {
		p := addArg("%" + escapeLikeTerm(qStr) + "%")
		where = append(where, fmt.Sprintf(`(
			mr.reference ILIKE %s ESCAPE '\' OR mr.title ILIKE %s ESCAPE '\' OR mr.description ILIKE %s ESCAPE '\'
			OR COALESCE(ml.name, '') ILIKE %s ESCAPE '\' OR COALESCE(u.code, '') ILIKE %s ESCAPE '\' OR COALESCE(u.name, '') ILIKE %s ESCAPE '\'
			OR COALESCE(a.name, '') ILIKE %s ESCAPE '\' OR COALESCE(a.asset_code, '') ILIKE %s ESCAPE '\'
		)`, p, p, p, p, p, p, p, p))
	}

	limit := filter.Limit
	if limit <= 0 || limit > 250 {
		limit = 150
	}

	query := `SELECT ` + selectMaintRequestColumns + fromMaintRequestJoins +
		` WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY
			CASE mr.status
				WHEN 'COMPLETED' THEN 2
				WHEN 'CANCELLED' THEN 3
				WHEN 'CLOSED' THEN 4
				ELSE 1
			END ASC,
			CASE mr.priority
				WHEN 'URGENT' THEN 1
				WHEN 'HIGH' THEN 2
				WHEN 'NORMAL' THEN 3
				ELSE 4
			END ASC,
			mr.target_date ASC NULLS LAST,
			mr.created_at DESC
		LIMIT ` + fmt.Sprintf("%d", limit)

	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.MaintenanceRequest
	for rows.Next() {
		req, err := scanMaintenanceRequest(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, req)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) UpdateMaintenanceRequest(ctx context.Context, tx pgx.Tx, req domain.MaintenanceRequest, actorID uuid.UUID, today time.Time) (domain.MaintenanceRequest, error) {
	catName, _, _, err := s.validateMaintenanceTargetsTx(
		ctx, tx, req.PropertyID, &req.CategoryID, req.LocationID, req.UnitID, req.AssetID, false,
	)
	if err != nil {
		return domain.MaintenanceRequest{}, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE maintenance_requests
		SET title = $3, description = $4, category_id = $5, category_name_snapshot = $6,
		    priority = $7, location_id = $8, unit_id = $9, asset_id = $10,
		    target_date = $11, scheduled_for = $12,
		    guest_impact = $13, safety_impact = $14, availability_impact = $15,
		    inspection_required = $16, internal_notes = $17,
		    updated_by = $18, version = version + 1, updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND version = $19
	`, req.PropertyID, req.ID, req.Title, req.Description, req.CategoryID, catName,
		string(req.Priority), req.LocationID, req.UnitID, req.AssetID,
		req.TargetDate, req.ScheduledFor,
		req.GuestImpact, req.SafetyImpact, string(req.AvailabilityImpact),
		req.InspectionRequired, req.InternalNotes, actorID, req.Version)
	if err != nil {
		return domain.MaintenanceRequest{}, MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.MaintenanceRequest{}, domain.ErrConflict
	}
	return s.GetMaintenanceRequest(ctx, tx, req.PropertyID, req.ID, today)
}

func (s *Store) AssignMaintenanceRequest(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, requestID uuid.UUID,
	userID, contractorID *uuid.UUID,
	assignedBy uuid.UUID,
	dueDate *time.Time,
	notes string,
	version int,
	today time.Time,
) (domain.MaintenanceRequest, error) {
	if err := s.validateMaintenanceAssigneesTx(ctx, tx, propertyID, userID, contractorID); err != nil {
		return domain.MaintenanceRequest{}, err
	}

	tag, err := tx.Exec(ctx, `
		UPDATE maintenance_requests
		SET assigned_user_id = $3,
		    assigned_contractor_id = $4,
		    target_date = COALESCE($5, target_date),
		    updated_by = $6,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND version = $7
	`, propertyID, requestID, userID, contractorID, dueDate, assignedBy, version)
	if err != nil {
		return domain.MaintenanceRequest{}, MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.MaintenanceRequest{}, domain.ErrConflict
	}

	if err := s.appendAssignmentTx(ctx, tx, propertyID, requestID, userID, contractorID, &assignedBy, dueDate, strings.TrimSpace(notes)); err != nil {
		return domain.MaintenanceRequest{}, err
	}
	return s.GetMaintenanceRequest(ctx, tx, propertyID, requestID, today)
}

func (s *Store) TransitionMaintenanceStatus(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, requestID uuid.UUID,
	toStatus domain.MaintenanceStatus,
	actorID uuid.UUID,
	reason string,
	scheduledFor *time.Time,
	version int,
	today time.Time,
) (domain.MaintenanceRequest, error) {
	cur, err := s.GetMaintenanceRequest(ctx, tx, propertyID, requestID, today)
	if err != nil {
		return domain.MaintenanceRequest{}, err
	}
	if cur.Version != version {
		return domain.MaintenanceRequest{}, domain.ErrConflict
	}
	if !cur.Status.CanTransitionTo(toStatus) {
		return domain.MaintenanceRequest{}, fmt.Errorf("%w: cannot transition maintenance request from %s to %s", domain.ErrValidation, cur.Status, toStatus)
	}
	if toStatus == domain.MaintStatusClosed && cur.InspectionRequired {
		if cur.CompletedAt == nil || cur.LatestInspectionResult != string(domain.MaintInspectionPassed) {
			return domain.MaintenanceRequest{}, domain.NewValidationError(map[string]string{
				"inspection": "inspection must be recorded as PASSED after the current repair completion before closing this maintenance request",
			})
		}
		var currentCyclePassed bool
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE((
				SELECT mi.result = 'PASSED'
				FROM maintenance_inspections mi
				WHERE mi.property_id = $1
				  AND mi.request_id = $2
				  AND mi.inspected_at > $3
				ORDER BY mi.inspected_at DESC
				LIMIT 1
			), FALSE)
		`, propertyID, requestID, *cur.CompletedAt).Scan(&currentCyclePassed); err != nil {
			return domain.MaintenanceRequest{}, MapErr(err)
		}
		if !currentCyclePassed {
			return domain.MaintenanceRequest{}, domain.NewValidationError(map[string]string{
				"inspection": "a new PASSED inspection recorded after the latest completion is required before closing this maintenance request",
			})
		}
	}

	startedExpr := "started_at"
	completedExpr := "completed_at"
	closedExpr := "closed_at"
	if toStatus.IsOpen() {
		if toStatus == domain.MaintStatusInProgress {
			startedExpr = "COALESCE(started_at, clock_timestamp())"
		}
		completedExpr = "NULL"
		closedExpr = "NULL"
	} else {
		switch toStatus {
		case domain.MaintStatusCompleted:
			completedExpr = "GREATEST(clock_timestamp(), COALESCE(completed_at, '-infinity'::timestamptz) + interval '1 microsecond', COALESCE((SELECT MAX(inspected_at) FROM maintenance_inspections WHERE property_id = $1 AND request_id = $2), '-infinity'::timestamptz) + interval '1 microsecond')"
			closedExpr = "NULL"
		case domain.MaintStatusClosed, domain.MaintStatusCancelled:
			closedExpr = "GREATEST(clock_timestamp(), COALESCE(completed_at, '-infinity'::timestamptz) + interval '1 microsecond')"
		}
	}

	query := fmt.Sprintf(`
		UPDATE maintenance_requests
		SET status = $3,
		    scheduled_for = COALESCE($4, scheduled_for),
		    started_at = %s,
		    completed_at = %s,
		    closed_at = %s,
		    updated_by = $5,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND version = $6
	`, startedExpr, completedExpr, closedExpr)

	tag, err := tx.Exec(ctx, query, propertyID, requestID, string(toStatus), scheduledFor, actorID, version)
	if err != nil {
		return domain.MaintenanceRequest{}, MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.MaintenanceRequest{}, domain.ErrConflict
	}

	if err := s.appendStatusHistoryTx(ctx, tx, propertyID, requestID, string(cur.Status), string(toStatus), strings.TrimSpace(reason), &actorID); err != nil {
		return domain.MaintenanceRequest{}, err
	}
	return s.GetMaintenanceRequest(ctx, tx, propertyID, requestID, today)
}

func (s *Store) AddMaintenanceWorkEntry(ctx context.Context, tx pgx.Tx, entry domain.MaintenanceWorkEntry) (domain.MaintenanceWorkEntry, error) {
	id, err := NewUUID()
	if err != nil {
		return domain.MaintenanceWorkEntry{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO maintenance_work_entries (id, property_id, request_id, entry_type, body, parts_used, author_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, id, entry.PropertyID, entry.RequestID, string(entry.EntryType), entry.Body, entry.PartsUsed, entry.AuthorID); err != nil {
		return domain.MaintenanceWorkEntry{}, MapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE maintenance_requests SET updated_at = NOW(), version = version + 1
		WHERE property_id = $1 AND id = $2
	`, entry.PropertyID, entry.RequestID); err != nil {
		return domain.MaintenanceWorkEntry{}, MapErr(err)
	}

	var out domain.MaintenanceWorkEntry
	var entryType string
	err = tx.QueryRow(ctx, `
		SELECT we.id, we.property_id, we.request_id, we.entry_type, we.body, we.parts_used,
		       we.author_id, COALESCE(au.full_name, ''), we.created_at
		FROM maintenance_work_entries we
		LEFT JOIN admin_users au ON au.id = we.author_id
		WHERE we.id = $1
	`, id).Scan(&out.ID, &out.PropertyID, &out.RequestID, &entryType, &out.Body, &out.PartsUsed, &out.AuthorID, &out.AuthorName, &out.CreatedAt)
	if err != nil {
		return domain.MaintenanceWorkEntry{}, MapErr(err)
	}
	out.EntryType = domain.MaintenanceWorkEntryType(entryType)
	return out, nil
}

func (s *Store) RecordMaintenanceInspection(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, requestID uuid.UUID,
	inspectorID uuid.UUID,
	result domain.MaintenanceInspectionResult,
	notes string,
	closeIfPassed bool,
	version int,
	today time.Time,
) (domain.MaintenanceRequest, domain.MaintenanceInspection, error) {
	cur, err := s.GetMaintenanceRequest(ctx, tx, propertyID, requestID, today)
	if err != nil {
		return domain.MaintenanceRequest{}, domain.MaintenanceInspection{}, err
	}
	if cur.Version != version {
		return domain.MaintenanceRequest{}, domain.MaintenanceInspection{}, domain.ErrConflict
	}
	if cur.Status != domain.MaintStatusCompleted {
		return domain.MaintenanceRequest{}, domain.MaintenanceInspection{}, fmt.Errorf("%w: inspections can only be recorded once a maintenance request is COMPLETED", domain.ErrValidation)
	}

	inspID, err := NewUUID()
	if err != nil {
		return domain.MaintenanceRequest{}, domain.MaintenanceInspection{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO maintenance_inspections (id, property_id, request_id, inspector_id, result, notes, inspected_at)
		VALUES (
			$1, $2, $3, $4, $5, $6,
			GREATEST(
				clock_timestamp(),
				COALESCE((SELECT completed_at FROM maintenance_requests WHERE property_id = $2 AND id = $3), '-infinity'::timestamptz) + interval '1 microsecond',
				COALESCE((SELECT MAX(inspected_at) FROM maintenance_inspections WHERE property_id = $2 AND request_id = $3), '-infinity'::timestamptz) + interval '1 microsecond'
			)
		)
	`, inspID, propertyID, requestID, inspectorID, string(result), strings.TrimSpace(notes)); err != nil {
		return domain.MaintenanceRequest{}, domain.MaintenanceInspection{}, MapErr(err)
	}

	if result == domain.MaintInspectionFailed {
		tag, err := tx.Exec(ctx, `
			UPDATE maintenance_requests
			SET status = 'IN_PROGRESS', completed_at = NULL, closed_at = NULL, updated_by = $3, version = version + 1, updated_at = NOW()
			WHERE property_id = $1 AND id = $2 AND version = $4
		`, propertyID, requestID, inspectorID, version)
		if err != nil {
			return domain.MaintenanceRequest{}, domain.MaintenanceInspection{}, MapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return domain.MaintenanceRequest{}, domain.MaintenanceInspection{}, domain.ErrConflict
		}
		reason := "Inspection failed"
		if strings.TrimSpace(notes) != "" {
			reason += ": " + strings.TrimSpace(notes)
		}
		if err := s.appendStatusHistoryTx(ctx, tx, propertyID, requestID, string(domain.MaintStatusCompleted), string(domain.MaintStatusInProgress), reason, &inspectorID); err != nil {
			return domain.MaintenanceRequest{}, domain.MaintenanceInspection{}, err
		}
	} else {
		newStatus := domain.MaintStatusCompleted
		closedExpr := "closed_at"
		if closeIfPassed {
			newStatus = domain.MaintStatusClosed
			closedExpr = "GREATEST(clock_timestamp(), COALESCE(completed_at, '-infinity'::timestamptz) + interval '1 microsecond')"
		}
		tag, err := tx.Exec(ctx, fmt.Sprintf(`
			UPDATE maintenance_requests
			SET status = $3, closed_at = %s, updated_by = $4, version = version + 1, updated_at = NOW()
			WHERE property_id = $1 AND id = $2 AND version = $5
		`, closedExpr), propertyID, requestID, string(newStatus), inspectorID, version)
		if err != nil {
			return domain.MaintenanceRequest{}, domain.MaintenanceInspection{}, MapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return domain.MaintenanceRequest{}, domain.MaintenanceInspection{}, domain.ErrConflict
		}
		if closeIfPassed {
			if err := s.appendStatusHistoryTx(ctx, tx, propertyID, requestID, string(domain.MaintStatusCompleted), string(domain.MaintStatusClosed), "Inspection passed and closed", &inspectorID); err != nil {
				return domain.MaintenanceRequest{}, domain.MaintenanceInspection{}, err
			}
		}
	}

	var insp domain.MaintenanceInspection
	var resStr string
	if err := tx.QueryRow(ctx, `
		SELECT mi.id, mi.property_id, mi.request_id, mi.inspector_id, COALESCE(au.full_name, ''), mi.result, mi.notes, mi.inspected_at
		FROM maintenance_inspections mi
		LEFT JOIN admin_users au ON au.id = mi.inspector_id
		WHERE mi.id = $1
	`, inspID).Scan(&insp.ID, &insp.PropertyID, &insp.RequestID, &insp.InspectorID, &insp.InspectorName, &resStr, &insp.Notes, &insp.InspectedAt); err != nil {
		return domain.MaintenanceRequest{}, domain.MaintenanceInspection{}, MapErr(err)
	}
	insp.Result = domain.MaintenanceInspectionResult(resStr)

	updatedReq, err := s.GetMaintenanceRequest(ctx, tx, propertyID, requestID, today)
	if err != nil {
		return domain.MaintenanceRequest{}, domain.MaintenanceInspection{}, err
	}
	return updatedReq, insp, nil
}

func (s *Store) GetMaintenanceRequestDetail(ctx context.Context, db DBTX, propertyID, requestID uuid.UUID, today time.Time) (domain.MaintenanceRequestDetail, error) {
	q := s.dbConn(db)
	req, err := s.GetMaintenanceRequest(ctx, q, propertyID, requestID, today)
	if err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	detail := domain.MaintenanceRequestDetail{Request: req}

	// Assignments
	aRows, err := q.Query(ctx, `
		SELECT ma.id, ma.property_id, ma.request_id, ma.assigned_user_id, COALESCE(u.full_name, ''),
		       ma.assigned_contractor_id, COALESCE(NULLIF(trim(concat_ws(' — ', c.name, NULLIF(c.company, ''))), ''), ''),
		       ma.assigned_by, COALESCE(ab.full_name, ''), ma.due_date, ma.notes, ma.created_at
		FROM maintenance_assignments ma
		LEFT JOIN admin_users u ON u.id = ma.assigned_user_id
		LEFT JOIN maintenance_contractors c ON c.id = ma.assigned_contractor_id
		LEFT JOIN admin_users ab ON ab.id = ma.assigned_by
		WHERE ma.property_id = $1 AND ma.request_id = $2
		ORDER BY ma.created_at DESC
	`, propertyID, requestID)
	if err != nil {
		return domain.MaintenanceRequestDetail{}, MapErr(err)
	}
	defer aRows.Close()
	for aRows.Next() {
		var item domain.MaintenanceAssignment
		if err := aRows.Scan(
			&item.ID, &item.PropertyID, &item.RequestID, &item.AssignedUserID, &item.AssignedUserName,
			&item.AssignedContractorID, &item.AssignedContractorName,
			&item.AssignedBy, &item.AssignedByName, &item.DueDate, &item.Notes, &item.CreatedAt,
		); err != nil {
			return domain.MaintenanceRequestDetail{}, MapErr(err)
		}
		detail.Assignments = append(detail.Assignments, item)
	}
	if err := aRows.Err(); err != nil {
		return domain.MaintenanceRequestDetail{}, MapErr(err)
	}

	// Work entries
	wRows, err := q.Query(ctx, `
		SELECT we.id, we.property_id, we.request_id, we.entry_type, we.body, we.parts_used,
		       we.author_id, COALESCE(au.full_name, ''), we.created_at
		FROM maintenance_work_entries we
		LEFT JOIN admin_users au ON au.id = we.author_id
		WHERE we.property_id = $1 AND we.request_id = $2
		ORDER BY we.created_at ASC
	`, propertyID, requestID)
	if err != nil {
		return domain.MaintenanceRequestDetail{}, MapErr(err)
	}
	defer wRows.Close()
	for wRows.Next() {
		var item domain.MaintenanceWorkEntry
		var et string
		if err := wRows.Scan(&item.ID, &item.PropertyID, &item.RequestID, &et, &item.Body, &item.PartsUsed, &item.AuthorID, &item.AuthorName, &item.CreatedAt); err != nil {
			return domain.MaintenanceRequestDetail{}, MapErr(err)
		}
		item.EntryType = domain.MaintenanceWorkEntryType(et)
		detail.WorkEntries = append(detail.WorkEntries, item)
	}
	if err := wRows.Err(); err != nil {
		return domain.MaintenanceRequestDetail{}, MapErr(err)
	}

	// Inspections
	iRows, err := q.Query(ctx, `
		SELECT mi.id, mi.property_id, mi.request_id, mi.inspector_id, COALESCE(au.full_name, ''),
		       mi.result, mi.notes, mi.inspected_at
		FROM maintenance_inspections mi
		LEFT JOIN admin_users au ON au.id = mi.inspector_id
		WHERE mi.property_id = $1 AND mi.request_id = $2
		ORDER BY mi.inspected_at DESC
	`, propertyID, requestID)
	if err != nil {
		return domain.MaintenanceRequestDetail{}, MapErr(err)
	}
	defer iRows.Close()
	for iRows.Next() {
		var item domain.MaintenanceInspection
		var res string
		if err := iRows.Scan(&item.ID, &item.PropertyID, &item.RequestID, &item.InspectorID, &item.InspectorName, &res, &item.Notes, &item.InspectedAt); err != nil {
			return domain.MaintenanceRequestDetail{}, MapErr(err)
		}
		item.Result = domain.MaintenanceInspectionResult(res)
		detail.Inspections = append(detail.Inspections, item)
	}
	if err := iRows.Err(); err != nil {
		return domain.MaintenanceRequestDetail{}, MapErr(err)
	}

	// Status history
	hRows, err := q.Query(ctx, `
		SELECT sh.id, sh.property_id, sh.request_id, sh.from_status, sh.to_status, sh.reason,
		       sh.changed_by, COALESCE(au.full_name, ''), sh.created_at
		FROM maintenance_status_history sh
		LEFT JOIN admin_users au ON au.id = sh.changed_by
		WHERE sh.property_id = $1 AND sh.request_id = $2
		ORDER BY sh.created_at ASC
	`, propertyID, requestID)
	if err != nil {
		return domain.MaintenanceRequestDetail{}, MapErr(err)
	}
	defer hRows.Close()
	for hRows.Next() {
		var item domain.MaintenanceStatusHistory
		if err := hRows.Scan(&item.ID, &item.PropertyID, &item.RequestID, &item.FromStatus, &item.ToStatus, &item.Reason, &item.ChangedBy, &item.ChangedByName, &item.CreatedAt); err != nil {
			return domain.MaintenanceRequestDetail{}, MapErr(err)
		}
		detail.StatusHistory = append(detail.StatusHistory, item)
	}
	if err := hRows.Err(); err != nil {
		return domain.MaintenanceRequestDetail{}, MapErr(err)
	}

	// Attachments
	atRows, err := q.Query(ctx, `
		SELECT ma.id, ma.property_id, ma.request_id, ma.work_entry_id, ma.storage_key,
		       ma.original_filename, ma.content_type, ma.size_bytes, ma.sha256, ma.caption,
		       ma.uploaded_by, COALESCE(au.full_name, ''), ma.created_at
		FROM maintenance_attachments ma
		LEFT JOIN admin_users au ON au.id = ma.uploaded_by
		WHERE ma.property_id = $1 AND ma.request_id = $2
		ORDER BY ma.created_at DESC
	`, propertyID, requestID)
	if err != nil {
		return domain.MaintenanceRequestDetail{}, MapErr(err)
	}
	defer atRows.Close()
	for atRows.Next() {
		att, err := scanMaintenanceAttachment(atRows)
		if err != nil {
			return domain.MaintenanceRequestDetail{}, MapErr(err)
		}
		detail.Attachments = append(detail.Attachments, att)
	}
	if err := atRows.Err(); err != nil {
		return domain.MaintenanceRequestDetail{}, MapErr(err)
	}

	// Restriction links
	links, err := s.ListMaintenanceRestrictionLinks(ctx, q, propertyID, requestID)
	if err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	detail.RestrictionLinks = links
	return detail, nil
}

func (s *Store) appendStatusHistoryTx(ctx context.Context, tx pgx.Tx, propertyID, requestID uuid.UUID, fromStatus, toStatus, reason string, changedBy *uuid.UUID) error {
	id, err := NewUUID()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO maintenance_status_history (id, property_id, request_id, from_status, to_status, reason, changed_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, id, propertyID, requestID, fromStatus, toStatus, reason, changedBy)
	return MapErr(err)
}

func (s *Store) appendAssignmentTx(ctx context.Context, tx pgx.Tx, propertyID, requestID uuid.UUID, userID, contractorID, assignedBy *uuid.UUID, dueDate *time.Time, notes string) error {
	id, err := NewUUID()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO maintenance_assignments (id, property_id, request_id, assigned_user_id, assigned_contractor_id, assigned_by, due_date, notes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, id, propertyID, requestID, userID, contractorID, assignedBy, dueDate, notes)
	return MapErr(err)
}

func scanMaintenanceRequest(r scannable) (domain.MaintenanceRequest, error) {
	var m domain.MaintenanceRequest
	var priority, status, workType, impact string
	err := r.Scan(
		&m.ID, &m.PropertyID, &m.Reference, &m.Title, &m.Description,
		&m.CategoryID, &m.CategoryName, &m.CategoryCode,
		&priority, &status, &workType,
		&m.LocationID, &m.LocationName,
		&m.UnitID, &m.UnitCode, &m.UnitName,
		&m.AssetID, &m.AssetCode, &m.AssetName,
		&m.ScheduleID, &m.ScheduledOccurrenceDate,
		&m.ReportedBy, &m.ReporterName,
		&m.AssignedUserID, &m.AssignedUserName,
		&m.AssignedContractorID, &m.AssignedContractorName,
		&m.ReportedAt, &m.TargetDate, &m.ScheduledFor, &m.StartedAt, &m.CompletedAt, &m.ClosedAt,
		&m.GuestImpact, &m.SafetyImpact, &impact, &m.InspectionRequired, &m.InternalNotes,
		&m.Overdue, &m.HasActiveRestriction, &m.LatestInspectionResult,
		&m.Version, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return domain.MaintenanceRequest{}, err
	}
	m.Priority = domain.MaintenancePriority(priority)
	m.Status = domain.MaintenanceStatus(status)
	m.WorkType = domain.MaintenanceWorkType(workType)
	m.AvailabilityImpact = domain.MaintenanceAvailabilityImpact(impact)
	return m, nil
}

// ---------------- Attachments ----------------

func (s *Store) CreateMaintenanceAttachment(ctx context.Context, tx pgx.Tx, att domain.MaintenanceAttachment) (domain.MaintenanceAttachment, error) {
	if att.WorkEntryID != nil {
		if err := s.ValidateWorkEntryBelongsToRequest(ctx, tx, att.PropertyID, att.RequestID, *att.WorkEntryID); err != nil {
			return domain.MaintenanceAttachment{}, err
		}
	}
	id, err := NewUUID()
	if err != nil {
		return domain.MaintenanceAttachment{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO maintenance_attachments (
			id, property_id, request_id, work_entry_id, storage_key,
			original_filename, content_type, size_bytes, sha256, caption, uploaded_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	`, id, att.PropertyID, att.RequestID, att.WorkEntryID, att.StorageKey,
		att.OriginalFilename, att.ContentType, att.SizeBytes, att.SHA256, att.Caption, att.UploadedBy); err != nil {
		return domain.MaintenanceAttachment{}, MapErr(err)
	}
	return s.GetMaintenanceAttachment(ctx, tx, att.PropertyID, id)
}

func (s *Store) GetMaintenanceAttachment(ctx context.Context, db DBTX, propertyID, attachmentID uuid.UUID) (domain.MaintenanceAttachment, error) {
	q := s.dbConn(db)
	row := q.QueryRow(ctx, `
		SELECT ma.id, ma.property_id, ma.request_id, ma.work_entry_id, ma.storage_key,
		       ma.original_filename, ma.content_type, ma.size_bytes, ma.sha256, ma.caption,
		       ma.uploaded_by, COALESCE(au.full_name, ''), ma.created_at
		FROM maintenance_attachments ma
		LEFT JOIN admin_users au ON au.id = ma.uploaded_by
		WHERE ma.property_id = $1 AND ma.id = $2
	`, propertyID, attachmentID)
	att, err := scanMaintenanceAttachment(row)
	return att, MapErr(err)
}

func scanMaintenanceAttachment(r scannable) (domain.MaintenanceAttachment, error) {
	var a domain.MaintenanceAttachment
	err := r.Scan(
		&a.ID, &a.PropertyID, &a.RequestID, &a.WorkEntryID, &a.StorageKey,
		&a.OriginalFilename, &a.ContentType, &a.SizeBytes, &a.SHA256, &a.Caption,
		&a.UploadedBy, &a.UploaderName, &a.CreatedAt,
	)
	return a, err
}

// ---------------- Inventory Restriction Links ----------------

func (s *Store) ListMaintenanceRestrictionLinks(ctx context.Context, db DBTX, propertyID, requestID uuid.UUID) ([]domain.MaintenanceRestrictionLink, error) {
	q := s.dbConn(db)
	rows, err := q.Query(ctx, `
		SELECT mrl.id, mrl.property_id, mrl.request_id, mrl.unit_id,
		       COALESCE(u.code, ''), COALESCE(u.name, ''),
		       mrl.blockout_group_id, COALESCE(bg.status, ''),
		       lower(bg.block_range), upper(bg.block_range), COALESCE(bg.reason, ''), bg.version,
		       mrl.created_by, COALESCE(cb.full_name, ''),
		       mrl.released_by, COALESCE(rb.full_name, ''), mrl.released_at,
		       mrl.notes, mrl.created_at
		FROM maintenance_restriction_links mrl
		JOIN units u ON u.id = mrl.unit_id
		JOIN blockout_groups bg ON bg.id = mrl.blockout_group_id
		LEFT JOIN admin_users cb ON cb.id = mrl.created_by
		LEFT JOIN admin_users rb ON rb.id = mrl.released_by
		WHERE mrl.property_id = $1 AND mrl.request_id = $2
		ORDER BY mrl.created_at DESC
	`, propertyID, requestID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.MaintenanceRestrictionLink
	for rows.Next() {
		link, err := scanMaintenanceRestrictionLink(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, link)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetMaintenanceRestrictionLink(ctx context.Context, db DBTX, propertyID, linkID uuid.UUID) (domain.MaintenanceRestrictionLink, error) {
	q := s.dbConn(db)
	row := q.QueryRow(ctx, `
		SELECT mrl.id, mrl.property_id, mrl.request_id, mrl.unit_id,
		       COALESCE(u.code, ''), COALESCE(u.name, ''),
		       mrl.blockout_group_id, COALESCE(bg.status, ''),
		       lower(bg.block_range), upper(bg.block_range), COALESCE(bg.reason, ''), bg.version,
		       mrl.created_by, COALESCE(cb.full_name, ''),
		       mrl.released_by, COALESCE(rb.full_name, ''), mrl.released_at,
		       mrl.notes, mrl.created_at
		FROM maintenance_restriction_links mrl
		JOIN units u ON u.id = mrl.unit_id
		JOIN blockout_groups bg ON bg.id = mrl.blockout_group_id
		LEFT JOIN admin_users cb ON cb.id = mrl.created_by
		LEFT JOIN admin_users rb ON rb.id = mrl.released_by
		WHERE mrl.property_id = $1 AND mrl.id = $2
	`, propertyID, linkID)
	link, err := scanMaintenanceRestrictionLink(row)
	return link, MapErr(err)
}

func (s *Store) CreateMaintenanceRestrictionLink(ctx context.Context, tx pgx.Tx, propertyID, requestID, unitID, blockoutGroupID, createdBy uuid.UUID, notes string) (domain.MaintenanceRestrictionLink, error) {
	id, err := NewUUID()
	if err != nil {
		return domain.MaintenanceRestrictionLink{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO maintenance_restriction_links (id, property_id, request_id, unit_id, blockout_group_id, created_by, notes)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, id, propertyID, requestID, unitID, blockoutGroupID, createdBy, strings.TrimSpace(notes)); err != nil {
		return domain.MaintenanceRestrictionLink{}, MapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE maintenance_requests
		SET availability_impact = 'UNAVAILABLE', updated_by = $3, version = version + 1, updated_at = NOW()
		WHERE property_id = $1 AND id = $2
	`, propertyID, requestID, createdBy); err != nil {
		return domain.MaintenanceRestrictionLink{}, MapErr(err)
	}
	return s.GetMaintenanceRestrictionLink(ctx, tx, propertyID, id)
}

func (s *Store) MarkMaintenanceRestrictionReleased(ctx context.Context, tx pgx.Tx, propertyID, linkID, releasedBy uuid.UUID) (domain.MaintenanceRestrictionLink, error) {
	var requestID uuid.UUID
	err := tx.QueryRow(ctx, `
		UPDATE maintenance_restriction_links
		SET released_by = $3, released_at = NOW()
		WHERE property_id = $1 AND id = $2 AND released_at IS NULL
		RETURNING request_id
	`, propertyID, linkID, releasedBy).Scan(&requestID)
	if err != nil {
		return domain.MaintenanceRestrictionLink{}, MapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE maintenance_requests mr
		SET availability_impact = CASE
				WHEN EXISTS (
					SELECT 1
					FROM maintenance_restriction_links mrl
					JOIN blockout_groups bg ON bg.id = mrl.blockout_group_id
					WHERE mrl.request_id = mr.id
					  AND mrl.released_at IS NULL
					  AND bg.status IN ('ACTIVE', 'PARTIALLY_RELEASED')
				) THEN 'UNAVAILABLE'
				ELSE 'NONE'
			END,
			updated_by = $3,
			version = version + 1,
			updated_at = NOW()
		WHERE mr.property_id = $1 AND mr.id = $2
	`, propertyID, requestID, releasedBy); err != nil {
		return domain.MaintenanceRestrictionLink{}, MapErr(err)
	}
	return s.GetMaintenanceRestrictionLink(ctx, tx, propertyID, linkID)
}

// Unused helper removed cleanly below.

func scanMaintenanceRestrictionLink(r scannable) (domain.MaintenanceRestrictionLink, error) {
	var l domain.MaintenanceRestrictionLink
	err := r.Scan(
		&l.ID, &l.PropertyID, &l.RequestID, &l.UnitID,
		&l.UnitCode, &l.UnitName,
		&l.BlockoutGroupID, &l.GroupStatus,
		&l.StartDate, &l.EndDate, &l.Reason, &l.GroupVersion,
		&l.CreatedBy, &l.CreatedByName,
		&l.ReleasedBy, &l.ReleasedByName, &l.ReleasedAt,
		&l.Notes, &l.CreatedAt,
	)
	return l, err
}

// ---------------- Preventative Schedules ----------------

const selectScheduleColumns = `
	ms.id, ms.property_id, ms.title, ms.description,
	ms.category_id, COALESCE(mc.name, ''),
	ms.location_id, COALESCE(ml.name, ''),
	ms.unit_id, COALESCE(u.code, ''),
	ms.asset_id, COALESCE(a.asset_code, ''), COALESCE(a.name, ''),
	ms.priority, ms.frequency, ms.next_due_date, COALESCE(ms.anchor_day, EXTRACT(DAY FROM ms.next_due_date)::int), ms.last_generated_for_date,
	ms.assigned_user_id, COALESCE(au.full_name, ''),
	ms.assigned_contractor_id, COALESCE(NULLIF(trim(concat_ws(' — ', ctr.name, NULLIF(ctr.company, ''))), ''), ''),
	ms.inspection_required, ms.notes, ms.is_active, ms.version, ms.created_at, ms.updated_at
`

func (s *Store) ListMaintenanceSchedules(ctx context.Context, db DBTX, propertyID uuid.UUID, includeInactive bool) ([]domain.MaintenanceSchedule, error) {
	q := s.dbConn(db)
	query := `SELECT ` + selectScheduleColumns + `
		FROM maintenance_schedules ms
		LEFT JOIN maintenance_categories mc ON mc.id = ms.category_id
		LEFT JOIN maintenance_locations ml ON ml.id = ms.location_id
		LEFT JOIN units u ON u.id = ms.unit_id
		LEFT JOIN maintenance_assets a ON a.id = ms.asset_id
		LEFT JOIN admin_users au ON au.id = ms.assigned_user_id
		LEFT JOIN maintenance_contractors ctr ON ctr.id = ms.assigned_contractor_id
		WHERE ms.property_id = $1`
	if !includeInactive {
		query += ` AND ms.is_active = TRUE`
	}
	query += ` ORDER BY ms.next_due_date ASC, ms.title ASC`
	rows, err := q.Query(ctx, query, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.MaintenanceSchedule
	for rows.Next() {
		item, err := scanMaintenanceSchedule(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, item)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetMaintenanceSchedule(ctx context.Context, db DBTX, propertyID, id uuid.UUID) (domain.MaintenanceSchedule, error) {
	q := s.dbConn(db)
	row := q.QueryRow(ctx, `SELECT `+selectScheduleColumns+`
		FROM maintenance_schedules ms
		LEFT JOIN maintenance_categories mc ON mc.id = ms.category_id
		LEFT JOIN maintenance_locations ml ON ml.id = ms.location_id
		LEFT JOIN units u ON u.id = ms.unit_id
		LEFT JOIN maintenance_assets a ON a.id = ms.asset_id
		LEFT JOIN admin_users au ON au.id = ms.assigned_user_id
		LEFT JOIN maintenance_contractors ctr ON ctr.id = ms.assigned_contractor_id
		WHERE ms.property_id = $1 AND ms.id = $2`, propertyID, id)
	out, err := scanMaintenanceSchedule(row)
	return out, MapErr(err)
}

func (s *Store) CreateMaintenanceSchedule(ctx context.Context, tx pgx.Tx, sc domain.MaintenanceSchedule, actorID uuid.UUID) (domain.MaintenanceSchedule, error) {
	if _, _, _, err := s.validateMaintenanceTargetsTx(
		ctx, tx, sc.PropertyID, &sc.CategoryID, sc.LocationID, sc.UnitID, sc.AssetID, false,
	); err != nil {
		return domain.MaintenanceSchedule{}, err
	}
	if err := s.validateMaintenanceAssigneesTx(ctx, tx, sc.PropertyID, sc.AssignedUserID, sc.AssignedContractorID); err != nil {
		return domain.MaintenanceSchedule{}, err
	}
	id, err := NewUUID()
	if err != nil {
		return domain.MaintenanceSchedule{}, err
	}
	anchorDay := sc.AnchorDay
	if anchorDay < 1 || anchorDay > 31 {
		anchorDay = sc.NextDueDate.Day()
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO maintenance_schedules (
			id, property_id, title, description, category_id, location_id, unit_id, asset_id,
			priority, frequency, next_due_date, anchor_day, assigned_user_id, assigned_contractor_id,
			inspection_required, notes, is_active, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$18)
	`, id, sc.PropertyID, sc.Title, sc.Description, sc.CategoryID, sc.LocationID, sc.UnitID, sc.AssetID,
		string(sc.Priority), string(sc.Frequency), sc.NextDueDate, anchorDay, sc.AssignedUserID, sc.AssignedContractorID,
		sc.InspectionRequired, sc.Notes, sc.IsActive, actorID); err != nil {
		return domain.MaintenanceSchedule{}, MapErr(err)
	}
	return s.GetMaintenanceSchedule(ctx, tx, sc.PropertyID, id)
}

func (s *Store) UpdateMaintenanceSchedule(ctx context.Context, tx pgx.Tx, sc domain.MaintenanceSchedule, actorID uuid.UUID) (domain.MaintenanceSchedule, error) {
	if _, _, _, err := s.validateMaintenanceTargetsTx(
		ctx, tx, sc.PropertyID, &sc.CategoryID, sc.LocationID, sc.UnitID, sc.AssetID, false,
	); err != nil {
		return domain.MaintenanceSchedule{}, err
	}
	if err := s.validateMaintenanceAssigneesTx(ctx, tx, sc.PropertyID, sc.AssignedUserID, sc.AssignedContractorID); err != nil {
		return domain.MaintenanceSchedule{}, err
	}
	anchorDay := sc.AnchorDay
	if anchorDay < 1 || anchorDay > 31 {
		anchorDay = sc.NextDueDate.Day()
	}
	tag, err := tx.Exec(ctx, `
		UPDATE maintenance_schedules
		SET title = $3, description = $4, category_id = $5, location_id = $6, unit_id = $7, asset_id = $8,
		    priority = $9, frequency = $10, next_due_date = $11, anchor_day = $12, assigned_user_id = $13, assigned_contractor_id = $14,
		    inspection_required = $15, notes = $16, is_active = $17, updated_by = $18, version = version + 1, updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND version = $19
	`, sc.PropertyID, sc.ID, sc.Title, sc.Description, sc.CategoryID, sc.LocationID, sc.UnitID, sc.AssetID,
		string(sc.Priority), string(sc.Frequency), sc.NextDueDate, anchorDay, sc.AssignedUserID, sc.AssignedContractorID,
		sc.InspectionRequired, sc.Notes, sc.IsActive, actorID, sc.Version)
	if err != nil {
		return domain.MaintenanceSchedule{}, MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.MaintenanceSchedule{}, domain.ErrConflict
	}
	return s.GetMaintenanceSchedule(ctx, tx, sc.PropertyID, sc.ID)
}

// AcquireDueSchedulesAndGenerateRequests idempotently creates preventative maintenance
// work orders for due schedules on or before asOfDate without creating duplicates on repeated runs.
// When a schedule is substantially overdue across multiple missed intervals, the overdue backlog
// is consolidated into at most one work order aligned on the latest due date <= asOfDate, while
// next_due_date advances strictly beyond asOfDate so subsequent sweeps do not emit bursts.
func (s *Store) AcquireDueSchedulesAndGenerateRequests(
	ctx context.Context,
	tx pgx.Tx,
	propertyID uuid.UUID,
	asOfDate time.Time,
	actorID *uuid.UUID,
) ([]domain.MaintenanceRequest, error) {
	dueDay := time.Date(asOfDate.UTC().Year(), asOfDate.UTC().Month(), asOfDate.UTC().Day(), 0, 0, 0, 0, time.UTC)
	rows, err := tx.Query(ctx, `SELECT `+selectScheduleColumns+`
		FROM maintenance_schedules ms
		LEFT JOIN maintenance_categories mc ON mc.id = ms.category_id
		LEFT JOIN maintenance_locations ml ON ml.id = ms.location_id
		LEFT JOIN units u ON u.id = ms.unit_id
		LEFT JOIN maintenance_assets a ON a.id = ms.asset_id
		LEFT JOIN admin_users au ON au.id = ms.assigned_user_id
		LEFT JOIN maintenance_contractors ctr ON ctr.id = ms.assigned_contractor_id
		WHERE ms.property_id = $1 AND ms.is_active = TRUE AND ms.next_due_date <= $2
		ORDER BY ms.next_due_date ASC
		FOR UPDATE OF ms SKIP LOCKED`, propertyID, dueDay)
	if err != nil {
		return nil, MapErr(err)
	}

	var due []domain.MaintenanceSchedule
	for rows.Next() {
		item, err := scanMaintenanceSchedule(rows)
		if err != nil {
			rows.Close()
			return nil, MapErr(err)
		}
		due = append(due, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, MapErr(err)
	}
	rows.Close()

	var created []domain.MaintenanceRequest
	for _, sc := range due {
		occDate, nextFuture, skippedIntervals, isDue := sc.Frequency.ResolveDueCatchUp(sc.NextDueDate, dueDay, sc.AnchorDay)
		if !isDue {
			continue
		}

		assignedUserID := sc.AssignedUserID
		assignedContractorID := sc.AssignedContractorID
		var clearedNotes []string
		if assignedUserID != nil {
			if err := s.validateMaintenanceAssigneesTx(ctx, tx, sc.PropertyID, assignedUserID, nil); err != nil {
				assignedUserID = nil
				clearedNotes = append(clearedNotes, fmt.Sprintf("Scheduled staff assignee cleared (%v)", err))
			}
		}
		if assignedContractorID != nil {
			if err := s.validateMaintenanceAssigneesTx(ctx, tx, sc.PropertyID, nil, assignedContractorID); err != nil {
				assignedContractorID = nil
				clearedNotes = append(clearedNotes, fmt.Sprintf("Scheduled contractor assignee cleared (%v)", err))
			}
		}

		initialStatus := domain.MaintStatusScheduled
		if assignedUserID == nil && assignedContractorID == nil {
			initialStatus = domain.MaintStatusReported
		}

		historyReason := "Generated from preventative schedule"
		internalNotes := strings.TrimSpace(sc.Notes)
		if len(clearedNotes) > 0 {
			clearanceSummary := strings.Join(clearedNotes, "; ")
			historyReason = historyReason + " — " + clearanceSummary
			if internalNotes != "" {
				internalNotes += "\n"
			}
			internalNotes += "[Schedule Assignment Warning] " + clearanceSummary
		}
		if skippedIntervals > 0 {
			catchUpSummary := fmt.Sprintf(
				"Consolidated overdue catch-up from %s (%d missed interval(s) skipped to %s)",
				sc.NextDueDate.UTC().Format("2006-01-02"),
				skippedIntervals,
				occDate.Format("2006-01-02"),
			)
			historyReason = historyReason + " — " + catchUpSummary
			if internalNotes != "" {
				internalNotes += "\n"
			}
			internalNotes += "[Overdue Catch-Up] " + catchUpSummary
		}

		reqID, err := NewUUID()
		if err != nil {
			return nil, err
		}
		ref, err := s.nextMaintenanceReferenceTx(ctx, tx, dueDay.Year())
		if err != nil {
			return nil, err
		}
		var insertedID uuid.UUID
		err = tx.QueryRow(ctx, `
			INSERT INTO maintenance_requests (
				id, property_id, reference, title, description,
				category_id, category_name_snapshot, priority, status, work_type,
				location_id, unit_id, asset_id, schedule_id, scheduled_occurrence_date,
				reported_by, assigned_user_id, assigned_contractor_id,
				reported_at, target_date, scheduled_for,
				guest_impact, safety_impact, availability_impact, inspection_required, internal_notes,
				created_by, updated_by
			) VALUES (
				$1,$2,$3,$4,$5,
				$6,$7,$8,$9,'PREVENTATIVE',
				$10,$11,$12,$13,$14,
				$15,$16,$17,
				NOW(),$14,$14,
				FALSE,FALSE,'NONE',$18,$19,
				$15,$15
			)
			ON CONFLICT (schedule_id, scheduled_occurrence_date)
			WHERE schedule_id IS NOT NULL AND scheduled_occurrence_date IS NOT NULL
			DO NOTHING
			RETURNING id
		`, reqID, sc.PropertyID, ref, sc.Title, sc.Description,
			sc.CategoryID, sc.CategoryName, string(sc.Priority), string(initialStatus),
			sc.LocationID, sc.UnitID, sc.AssetID, sc.ID, occDate,
			actorID, assignedUserID, assignedContractorID,
			sc.InspectionRequired, internalNotes).Scan(&insertedID)
		if err == nil && insertedID != uuid.Nil {
			if err := s.appendStatusHistoryTx(ctx, tx, sc.PropertyID, insertedID, "", string(initialStatus), historyReason, actorID); err != nil {
				return nil, err
			}
			if assignedUserID != nil || assignedContractorID != nil {
				if err := s.appendAssignmentTx(ctx, tx, sc.PropertyID, insertedID, assignedUserID, assignedContractorID, actorID, &occDate, "Default schedule assignment"); err != nil {
					return nil, err
				}
			}
			req, err := s.GetMaintenanceRequest(ctx, tx, sc.PropertyID, insertedID, dueDay)
			if err != nil {
				return nil, err
			}
			created = append(created, req)
		} else if err != nil && err != pgx.ErrNoRows {
			return nil, MapErr(err)
		}

		if nextFuture != nil {
			if _, err := tx.Exec(ctx, `
				UPDATE maintenance_schedules
				SET next_due_date = $3, last_generated_for_date = $4, version = version + 1, updated_at = NOW()
				WHERE id = $1 AND property_id = $2
			`, sc.ID, sc.PropertyID, *nextFuture, occDate); err != nil {
				return nil, MapErr(err)
			}
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE maintenance_schedules
				SET is_active = FALSE, last_generated_for_date = $3, version = version + 1, updated_at = NOW()
				WHERE id = $1 AND property_id = $2
			`, sc.ID, sc.PropertyID, occDate); err != nil {
				return nil, MapErr(err)
			}
		}
	}
	return created, nil
}

func scanMaintenanceSchedule(r scannable) (domain.MaintenanceSchedule, error) {
	var sc domain.MaintenanceSchedule
	var priority, freq string
	err := r.Scan(
		&sc.ID, &sc.PropertyID, &sc.Title, &sc.Description,
		&sc.CategoryID, &sc.CategoryName,
		&sc.LocationID, &sc.LocationName,
		&sc.UnitID, &sc.UnitCode,
		&sc.AssetID, &sc.AssetCode, &sc.AssetName,
		&priority, &freq, &sc.NextDueDate, &sc.AnchorDay, &sc.LastGeneratedForDate,
		&sc.AssignedUserID, &sc.AssignedUserName,
		&sc.AssignedContractorID, &sc.AssignedContractorName,
		&sc.InspectionRequired, &sc.Notes, &sc.IsActive, &sc.Version, &sc.CreatedAt, &sc.UpdatedAt,
	)
	if err != nil {
		return domain.MaintenanceSchedule{}, err
	}
	sc.Priority = domain.MaintenancePriority(priority)
	sc.Frequency = domain.MaintenanceFrequency(freq)
	return sc, nil
}

// ---------------- Operations Centre Integration Queries ----------------

func (s *Store) GetOpsMaintenanceSummary(ctx context.Context, db DBTX, propertyID uuid.UUID, opDate, todayDate time.Time) (domain.OpsMaintenanceSummary, error) {
	q := s.dbConn(db)
	var sum domain.OpsMaintenanceSummary

	if err := q.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status NOT IN ('COMPLETED', 'CANCELLED', 'CLOSED')) AS open_count,
			COUNT(*) FILTER (WHERE status NOT IN ('COMPLETED', 'CANCELLED', 'CLOSED') AND priority = 'URGENT') AS urgent_count,
			COUNT(*) FILTER (WHERE status NOT IN ('COMPLETED', 'CANCELLED', 'CLOSED') AND priority = 'HIGH') AS high_count,
			COUNT(*) FILTER (WHERE status NOT IN ('COMPLETED', 'CANCELLED', 'CLOSED') AND target_date IS NOT NULL AND target_date < $2) AS overdue_count,
			COUNT(*) FILTER (WHERE status NOT IN ('COMPLETED', 'CANCELLED', 'CLOSED') AND (scheduled_for = $3 OR target_date = $3)) AS scheduled_today_count,
			COUNT(*) FILTER (WHERE EXISTS (
				SELECT 1 FROM maintenance_restriction_links mrl
				JOIN blockout_groups bg ON bg.id = mrl.blockout_group_id
				WHERE mrl.request_id = maintenance_requests.id
				  AND mrl.released_at IS NULL
				  AND bg.status IN ('ACTIVE', 'PARTIALLY_RELEASED')
			)) AS restricting_units_count,
			COUNT(*) FILTER (WHERE status = 'COMPLETED' AND inspection_required = TRUE AND (
				completed_at IS NULL OR COALESCE((
					SELECT mi.result
					FROM maintenance_inspections mi
					WHERE mi.request_id = maintenance_requests.id
					  AND mi.inspected_at > maintenance_requests.completed_at
					ORDER BY mi.inspected_at DESC
					LIMIT 1
				), '') <> 'PASSED'
			)) AS awaiting_inspection_count,
			COUNT(*) FILTER (WHERE status IN ('COMPLETED', 'CLOSED') AND COALESCE(closed_at, completed_at, updated_at)::date >= ($3::date - INTERVAL '2 days')) AS recently_completed_count
		FROM maintenance_requests
		WHERE property_id = $1
	`, propertyID, todayDate, opDate).Scan(
		&sum.OpenCount,
		&sum.UrgentCount,
		&sum.HighCount,
		&sum.OverdueCount,
		&sum.ScheduledTodayCount,
		&sum.RestrictingUnitsCount,
		&sum.AwaitingInspectionCount,
		&sum.RecentlyCompletedCount,
	); err != nil {
		return domain.OpsMaintenanceSummary{}, MapErr(err)
	}

	openReqs, err := s.ListMaintenanceRequests(ctx, q, propertyID, domain.MaintenanceRequestFilter{
		Status: "OPEN",
		Limit:  50,
	}, todayDate)
	if err != nil {
		return domain.OpsMaintenanceSummary{}, err
	}
	for _, r := range openReqs {
		if r.Priority == domain.MaintPriorityUrgent || r.Priority == domain.MaintPriorityHigh || r.SafetyImpact || r.Overdue || r.HasActiveRestriction {
			sum.PriorityJobs = append(sum.PriorityJobs, r)
		}
		if (r.ScheduledFor != nil && r.ScheduledFor.Format("2006-01-02") == opDate.Format("2006-01-02")) ||
			(r.TargetDate != nil && r.TargetDate.Format("2006-01-02") == opDate.Format("2006-01-02")) {
			sum.ScheduledToday = append(sum.ScheduledToday, r)
		}
	}

	awaiting, err := s.ListMaintenanceRequests(ctx, q, propertyID, domain.MaintenanceRequestFilter{
		Status: "AWAITING_INSPECTION",
		Limit:  20,
	}, todayDate)
	if err != nil {
		return domain.OpsMaintenanceSummary{}, err
	}
	sum.AwaitingInspection = awaiting

	recentRows, err := q.Query(ctx, `SELECT `+selectMaintRequestColumns+fromMaintRequestJoins+`
		WHERE mr.property_id = $1
		  AND mr.status IN ('COMPLETED', 'CLOSED')
		ORDER BY COALESCE(mr.closed_at, mr.completed_at, mr.updated_at) DESC
		LIMIT 10`, propertyID, todayDate)
	if err != nil {
		return domain.OpsMaintenanceSummary{}, MapErr(err)
	}
	defer recentRows.Close()
	for recentRows.Next() {
		r, err := scanMaintenanceRequest(recentRows)
		if err != nil {
			return domain.OpsMaintenanceSummary{}, MapErr(err)
		}
		sum.RecentlyCompleted = append(sum.RecentlyCompleted, r)
	}
	if err := recentRows.Err(); err != nil {
		return domain.OpsMaintenanceSummary{}, MapErr(err)
	}
	return sum, nil
}

// MaintenanceAffectedBooking captures open unit-linked maintenance intersecting upcoming stays.
type MaintenanceAffectedBooking struct {
	RequestID     uuid.UUID
	Reference     string
	Title         string
	UnitID        uuid.UUID
	UnitCode      string
	ReservationID uuid.UUID
	BookingRef    string
	GuestName     string
	CheckInDate   time.Time
}

func (s *Store) ListMaintenanceAffectedBookings(ctx context.Context, db DBTX, propertyID uuid.UUID, fromDate, toDate time.Time) ([]MaintenanceAffectedBooking, error) {
	q := s.dbConn(db)
	rows, err := q.Query(ctx, `
		SELECT DISTINCT mr.id, mr.reference, mr.title, u.id, u.code, r.id, r.reference, r.guest_name, lower(r.stay_range) AS check_in_date
		FROM maintenance_requests mr
		JOIN units u ON u.id = mr.unit_id
		JOIN reservation_units ru ON ru.unit_id = mr.unit_id
		JOIN reservations r ON r.id = ru.reservation_id
		WHERE mr.property_id = $1
		  AND mr.status NOT IN ('COMPLETED', 'CANCELLED', 'CLOSED')
		  AND (mr.priority IN ('URGENT', 'HIGH') OR mr.safety_impact = TRUE OR mr.guest_impact = TRUE OR mr.availability_impact <> 'NONE')
		  AND r.status IN ('PENDING', 'CONFIRMED', 'CHECKED_IN')
		  AND lower(r.stay_range) <= $3
		  AND upper(r.stay_range) >= $2
		ORDER BY lower(r.stay_range) ASC, mr.reference ASC
		LIMIT 20
	`, propertyID, domain.FormatDate(fromDate), domain.FormatDate(toDate))
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []MaintenanceAffectedBooking
	for rows.Next() {
		var row MaintenanceAffectedBooking
		if err := rows.Scan(
			&row.RequestID, &row.Reference, &row.Title,
			&row.UnitID, &row.UnitCode,
			&row.ReservationID, &row.BookingRef, &row.GuestName, &row.CheckInDate,
		); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, row)
	}
	return out, MapErr(rows.Err())
}
