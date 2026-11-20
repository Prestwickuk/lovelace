package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Prestwickuk/lovelace/internal/branding"
	"github.com/Prestwickuk/lovelace/internal/domain"
)

func scanPropertyBrandingRecord(row pgx.Row) (domain.PropertyBrandingRecord, error) {
	var (
		rec             domain.PropertyBrandingRecord
		hasRow          bool
		rawPublic       []byte
		rawAdmin        []byte
		rawLegacy       []byte
		schemaVersion   sql.NullInt32
		logoStorageKey  sql.NullString
		logoContentType sql.NullString
		logoSizeBytes   sql.NullInt64
		logoSHA256      sql.NullString
		logoWidth       sql.NullInt32
		logoHeight      sql.NullInt32
		logoVersion     sql.NullInt32
		version         sql.NullInt32
		updatedBy       *uuid.UUID
		pbsCreatedAt    sql.NullTime
		pbsUpdatedAt    sql.NullTime
	)

	err := row.Scan(
		&rec.PropertyID,
		&rec.PropertySlug,
		&rec.PropertyName,
		&rec.PropertyTagline,
		&rec.PropertyRegion,
		&rawLegacy,
		&hasRow,
		&schemaVersion,
		&rawPublic,
		&rawAdmin,
		&logoStorageKey,
		&logoContentType,
		&logoSizeBytes,
		&logoSHA256,
		&logoWidth,
		&logoHeight,
		&logoVersion,
		&version,
		&updatedBy,
		&pbsCreatedAt,
		&pbsUpdatedAt,
	)
	if err != nil {
		return domain.PropertyBrandingRecord{}, MapErr(err)
	}

	rec.LegacyBranding = rawLegacy
	if schemaVersion.Valid && schemaVersion.Int32 > 0 {
		rec.SchemaVersion = int(schemaVersion.Int32)
	} else {
		rec.SchemaVersion = branding.PropertyBrandingSchemaVersion
	}
	if version.Valid && version.Int32 > 0 {
		rec.Version = int(version.Int32)
	} else {
		rec.Version = 1
	}

	var pubInput []byte
	var admInput []byte
	if hasRow {
		pubInput = rawPublic
		admInput = rawAdmin
	}
	pub, adm := domain.DecodeStoredBranding(pubInput, admInput, rawLegacy)
	rec.DisplayName = pub.DisplayName
	rec.Tagline = pub.Tagline
	rec.RegionLabel = pub.RegionLabel
	rec.PrimaryColor = pub.PrimaryColor
	rec.SecondaryColor = pub.SecondaryColor
	rec.LogoAlt = pub.LogoAlt
	rec.HeaderLabel = adm.HeaderLabel
	rec.Terminology = adm.Terminology

	rec.LogoStorageKey = logoStorageKey.String
	rec.LogoContentType = logoContentType.String
	rec.LogoSizeBytes = logoSizeBytes.Int64
	rec.LogoSHA256 = logoSHA256.String
	rec.LogoWidth = int(logoWidth.Int32)
	rec.LogoHeight = int(logoHeight.Int32)
	rec.LogoVersion = int(logoVersion.Int32)
	rec.UpdatedBy = updatedBy
	if pbsCreatedAt.Valid {
		rec.CreatedAt = pbsCreatedAt.Time
	}
	if pbsUpdatedAt.Valid {
		rec.UpdatedAt = pbsUpdatedAt.Time
	}

	return rec, nil
}

const propertyBrandingSelectSQL = `
	SELECT p.id, p.slug, p.name, p.tagline, p.region, p.branding_config,
	       (pbs.property_id IS NOT NULL) AS has_row,
	       pbs.schema_version, pbs.public_branding, pbs.admin_branding,
	       pbs.logo_storage_key, pbs.logo_content_type, pbs.logo_size_bytes,
	       pbs.logo_sha256, pbs.logo_width, pbs.logo_height, pbs.logo_version,
	       pbs.version, pbs.updated_by, pbs.created_at, pbs.updated_at
	FROM properties p
	LEFT JOIN property_branding_settings pbs ON pbs.property_id = p.id
`

// GetPropertyBrandingByPropertyID returns the full PWE-4 branding record for an internal admin property.
func (s *Store) GetPropertyBrandingByPropertyID(ctx context.Context, db DBTX, propertyID uuid.UUID) (domain.PropertyBrandingRecord, error) {
	if db == nil {
		db = s.pool
	}
	return scanPropertyBrandingRecord(db.QueryRow(ctx, propertyBrandingSelectSQL+`
		WHERE p.id = $1
	`, propertyID))
}

// GetPropertyBrandingBySlug returns the active property branding record by slug.
func (s *Store) GetPropertyBrandingBySlug(ctx context.Context, db DBTX, slug string) (domain.PropertyBrandingRecord, error) {
	if db == nil {
		db = s.pool
	}
	return scanPropertyBrandingRecord(db.QueryRow(ctx, propertyBrandingSelectSQL+`
		WHERE p.slug = $1 AND p.public_enabled = TRUE
	`, strings.TrimSpace(strings.ToLower(slug))))
}

// UpsertPropertyBrandingSettings creates or updates PWE-4 public/admin branding settings with optimistic locking.
func (s *Store) UpsertPropertyBrandingSettings(
	ctx context.Context,
	db DBTX,
	propertyID uuid.UUID,
	expectedVersion int,
	publicJSON json.RawMessage,
	adminJSON json.RawMessage,
	legacyProjection json.RawMessage,
	updatedBy uuid.UUID,
) (domain.PropertyBrandingRecord, error) {
	if db == nil {
		db = s.pool
	}

	var currentVersion int
	err := db.QueryRow(ctx, `
		SELECT version
		FROM property_branding_settings
		WHERE property_id = $1
		FOR UPDATE
	`, propertyID).Scan(&currentVersion)

	if err != nil {
		mapped := MapErr(err)
		if !errors.Is(mapped, domain.ErrNotFound) {
			return domain.PropertyBrandingRecord{}, mapped
		}
		if expectedVersion > 1 {
			return domain.PropertyBrandingRecord{}, domain.ErrConflict
		}
		if _, err := db.Exec(ctx, `
			INSERT INTO property_branding_settings (
				property_id, schema_version, public_branding, admin_branding,
				version, updated_by, created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, 2, $5, NOW(), NOW()
			)
		`, propertyID, branding.PropertyBrandingSchemaVersion, []byte(publicJSON), []byte(adminJSON), updatedBy); err != nil {
			return domain.PropertyBrandingRecord{}, MapErr(err)
		}
	} else {
		if expectedVersion > 0 && currentVersion != expectedVersion {
			return domain.PropertyBrandingRecord{}, domain.ErrConflict
		}
		res, err := db.Exec(ctx, `
			UPDATE property_branding_settings
			SET schema_version = $2,
			    public_branding = $3,
			    admin_branding = $4,
			    version = version + 1,
			    updated_by = $5,
			    updated_at = NOW()
			WHERE property_id = $1 AND version = $6
		`, propertyID, branding.PropertyBrandingSchemaVersion, []byte(publicJSON), []byte(adminJSON), updatedBy, currentVersion)
		if err != nil {
			return domain.PropertyBrandingRecord{}, MapErr(err)
		}
		if res.RowsAffected() == 0 {
			return domain.PropertyBrandingRecord{}, domain.ErrConflict
		}
	}

	// Keep properties.branding_config projected to public-safe summary fields only (never admin terminology or storage keys).
	if len(legacyProjection) > 0 {
		if _, err := db.Exec(ctx, `
			UPDATE properties
			SET branding_config = $2,
			    updated_at = NOW()
			WHERE id = $1
		`, propertyID, []byte(legacyProjection)); err != nil {
			return domain.PropertyBrandingRecord{}, MapErr(err)
		}
	}

	return s.GetPropertyBrandingByPropertyID(ctx, db, propertyID)
}

// SetPropertyLogoMetadata stores new validated logo blob metadata with optimistic locking and returns any superseded storageKey.
func (s *Store) SetPropertyLogoMetadata(
	ctx context.Context,
	db DBTX,
	propertyID uuid.UUID,
	expectedVersion int,
	storageKey, contentType string,
	sizeBytes int64,
	sha256Hex string,
	width, height int,
	updatedBy uuid.UUID,
) (string, domain.PropertyBrandingRecord, error) {
	if db == nil {
		db = s.pool
	}

	var currentVersion int
	var oldKey sql.NullString
	err := db.QueryRow(ctx, `
		SELECT version, logo_storage_key
		FROM property_branding_settings
		WHERE property_id = $1
		FOR UPDATE
	`, propertyID).Scan(&currentVersion, &oldKey)

	if err != nil {
		mapped := MapErr(err)
		if !errors.Is(mapped, domain.ErrNotFound) {
			return "", domain.PropertyBrandingRecord{}, mapped
		}
		if expectedVersion > 1 {
			return "", domain.PropertyBrandingRecord{}, domain.ErrConflict
		}
		if _, err := db.Exec(ctx, `
			INSERT INTO property_branding_settings (
				property_id, schema_version, public_branding, admin_branding,
				logo_storage_key, logo_content_type, logo_size_bytes, logo_sha256,
				logo_width, logo_height, logo_version, version, updated_by, created_at, updated_at
			) VALUES (
				$1, $2, '{}'::jsonb, '{}'::jsonb,
				$3, $4, $5, $6,
				$7, $8, 1, 2, $9, NOW(), NOW()
			)
		`, propertyID, branding.PropertyBrandingSchemaVersion,
			storageKey, contentType, sizeBytes, sha256Hex,
			width, height, updatedBy); err != nil {
			return "", domain.PropertyBrandingRecord{}, MapErr(err)
		}
	} else {
		if expectedVersion > 0 && currentVersion != expectedVersion {
			return "", domain.PropertyBrandingRecord{}, domain.ErrConflict
		}
		res, err := db.Exec(ctx, `
			UPDATE property_branding_settings
			SET logo_storage_key = $2,
			    logo_content_type = $3,
			    logo_size_bytes = $4,
			    logo_sha256 = $5,
			    logo_width = $6,
			    logo_height = $7,
			    logo_version = logo_version + 1,
			    version = version + 1,
			    updated_by = $8,
			    updated_at = NOW()
			WHERE property_id = $1 AND version = $9
		`, propertyID, storageKey, contentType, sizeBytes, sha256Hex, width, height, updatedBy, currentVersion)
		if err != nil {
			return "", domain.PropertyBrandingRecord{}, MapErr(err)
		}
		if res.RowsAffected() == 0 {
			return "", domain.PropertyBrandingRecord{}, domain.ErrConflict
		}
	}

	rec, err := s.GetPropertyBrandingByPropertyID(ctx, db, propertyID)
	return oldKey.String, rec, err
}

// ClearPropertyLogoMetadata removes the property's active logo reference and returns any old storageKey for post-commit cleanup.
func (s *Store) ClearPropertyLogoMetadata(
	ctx context.Context,
	db DBTX,
	propertyID uuid.UUID,
	expectedVersion int,
	updatedBy uuid.UUID,
) (string, domain.PropertyBrandingRecord, error) {
	if db == nil {
		db = s.pool
	}

	var currentVersion int
	var oldKey sql.NullString
	err := db.QueryRow(ctx, `
		SELECT version, logo_storage_key
		FROM property_branding_settings
		WHERE property_id = $1
		FOR UPDATE
	`, propertyID).Scan(&currentVersion, &oldKey)
	if err != nil {
		mapped := MapErr(err)
		if errors.Is(mapped, domain.ErrNotFound) {
			rec, gerr := s.GetPropertyBrandingByPropertyID(ctx, db, propertyID)
			return "", rec, gerr
		}
		return "", domain.PropertyBrandingRecord{}, mapped
	}
	if expectedVersion > 0 && currentVersion != expectedVersion {
		return "", domain.PropertyBrandingRecord{}, domain.ErrConflict
	}

	res, err := db.Exec(ctx, `
		UPDATE property_branding_settings
		SET logo_storage_key = NULL,
		    logo_content_type = NULL,
		    logo_size_bytes = 0,
		    logo_sha256 = NULL,
		    logo_width = 0,
		    logo_height = 0,
		    logo_version = logo_version + 1,
		    version = version + 1,
		    updated_by = $2,
		    updated_at = NOW()
		WHERE property_id = $1 AND version = $3
	`, propertyID, updatedBy, currentVersion)
	if err != nil {
		return "", domain.PropertyBrandingRecord{}, MapErr(err)
	}
	if res.RowsAffected() == 0 {
		return "", domain.PropertyBrandingRecord{}, domain.ErrConflict
	}

	rec, err := s.GetPropertyBrandingByPropertyID(ctx, db, propertyID)
	return oldKey.String, rec, err
}
