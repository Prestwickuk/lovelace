package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/domain"
)

// GetUserPreferencesRaw loads the raw persisted global preference row for userID.
func (s *Store) GetUserPreferencesRaw(ctx context.Context, db DBTX, userID uuid.UUID) (domain.UserPreferences, []byte, error) {
	if db == nil {
		db = s.pool
	}
	var out domain.UserPreferences
	var rawPayload []byte
	err := db.QueryRow(ctx, `
		SELECT user_id, schema_version, theme_mode, accent, density, text_scale,
		       high_contrast, reduced_motion, default_route, last_property_id,
		       preferences, created_at, updated_at
		FROM admin_user_preferences
		WHERE user_id = $1
	`, userID).Scan(
		&out.UserID,
		&out.SchemaVersion,
		&out.ThemeMode,
		&out.Accent,
		&out.Density,
		&out.TextScale,
		&out.HighContrast,
		&out.ReducedMotion,
		&out.DefaultRoute,
		&out.LastPropertyID,
		&rawPayload,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		return domain.UserPreferences{}, nil, MapErr(err)
	}
	return out, rawPayload, nil
}

// UpsertUserPreferences creates or updates a user's validated global preference row.
func (s *Store) UpsertUserPreferences(ctx context.Context, db DBTX, p domain.UserPreferences) (domain.UserPreferences, error) {
	if db == nil {
		db = s.pool
	}
	payloadBytes, err := json.Marshal(p.Payload)
	if err != nil {
		return domain.UserPreferences{}, err
	}
	schemaVer := p.SchemaVersion
	if schemaVer < 1 {
		schemaVer = domain.CurrentPreferenceSchemaVersion
	}

	var out domain.UserPreferences
	var rawPayload []byte
	err = db.QueryRow(ctx, `
		INSERT INTO admin_user_preferences (
			user_id, schema_version, theme_mode, accent, density, text_scale,
			high_contrast, reduced_motion, default_route, last_property_id,
			preferences, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			schema_version   = EXCLUDED.schema_version,
			theme_mode       = EXCLUDED.theme_mode,
			accent           = EXCLUDED.accent,
			density          = EXCLUDED.density,
			text_scale       = EXCLUDED.text_scale,
			high_contrast    = EXCLUDED.high_contrast,
			reduced_motion   = EXCLUDED.reduced_motion,
			default_route    = EXCLUDED.default_route,
			last_property_id = EXCLUDED.last_property_id,
			preferences      = EXCLUDED.preferences,
			updated_at       = NOW()
		RETURNING user_id, schema_version, theme_mode, accent, density, text_scale,
		          high_contrast, reduced_motion, default_route, last_property_id,
		          preferences, created_at, updated_at
	`, p.UserID, schemaVer, p.ThemeMode, p.Accent, p.Density, p.TextScale,
		p.HighContrast, p.ReducedMotion, p.DefaultRoute, p.LastPropertyID, payloadBytes,
	).Scan(
		&out.UserID,
		&out.SchemaVersion,
		&out.ThemeMode,
		&out.Accent,
		&out.Density,
		&out.TextScale,
		&out.HighContrast,
		&out.ReducedMotion,
		&out.DefaultRoute,
		&out.LastPropertyID,
		&rawPayload,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		return domain.UserPreferences{}, MapErr(err)
	}
	norm, _ := domain.DecodeAndNormalizeUserPreferences(out, rawPayload)
	return norm, nil
}

// DeleteUserPreferences resets global preferences for userID by removing the row.
func (s *Store) DeleteUserPreferences(ctx context.Context, db DBTX, userID uuid.UUID) error {
	if db == nil {
		db = s.pool
	}
	_, err := db.Exec(ctx, `DELETE FROM admin_user_preferences WHERE user_id = $1`, userID)
	return MapErr(err)
}

// GetPropertyWorkspaceRaw loads raw persisted (user_id, property_id) workspace data.
func (s *Store) GetPropertyWorkspaceRaw(
	ctx context.Context,
	db DBTX,
	userID, propertyID uuid.UUID,
) (schemaVersion int, rawPrefs []byte, rawViews []byte, createdAt time.Time, updatedAt time.Time, err error) {
	if db == nil {
		db = s.pool
	}
	err = db.QueryRow(ctx, `
		SELECT schema_version, preferences, saved_views, created_at, updated_at
		FROM admin_user_property_workspaces
		WHERE user_id = $1 AND property_id = $2
	`, userID, propertyID).Scan(&schemaVersion, &rawPrefs, &rawViews, &createdAt, &updatedAt)
	if err != nil {
		return 0, nil, nil, time.Time{}, time.Time{}, MapErr(err)
	}
	return schemaVersion, rawPrefs, rawViews, createdAt, updatedAt, nil
}

// UpsertPropertyWorkspace creates or updates a (user_id, property_id) workspace row.
func (s *Store) UpsertPropertyWorkspace(
	ctx context.Context,
	db DBTX,
	w domain.UserPropertyWorkspace,
	catalog domain.PropertyResourceCatalog,
) (domain.UserPropertyWorkspace, error) {
	if db == nil {
		db = s.pool
	}
	prefsBytes, err := json.Marshal(w.Payload)
	if err != nil {
		return domain.UserPropertyWorkspace{}, err
	}
	views := w.SavedViews
	if views == nil {
		views = []domain.SavedOperationalView{}
	}
	viewsBytes, err := json.Marshal(views)
	if err != nil {
		return domain.UserPropertyWorkspace{}, err
	}
	schemaVer := w.SchemaVersion
	if schemaVer < 1 {
		schemaVer = domain.CurrentWorkspaceSchemaVersion
	}

	var gotVer int
	var gotPrefs, gotViews []byte
	var createdAt, updatedAt time.Time
	err = db.QueryRow(ctx, `
		INSERT INTO admin_user_property_workspaces (
			user_id, property_id, schema_version, preferences, saved_views, updated_at
		) VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, NOW())
		ON CONFLICT (user_id, property_id) DO UPDATE SET
			schema_version = EXCLUDED.schema_version,
			preferences    = EXCLUDED.preferences,
			saved_views    = EXCLUDED.saved_views,
			updated_at     = NOW()
		RETURNING schema_version, preferences, saved_views, created_at, updated_at
	`, w.UserID, w.PropertyID, schemaVer, prefsBytes, viewsBytes).Scan(
		&gotVer, &gotPrefs, &gotViews, &createdAt, &updatedAt,
	)
	if err != nil {
		return domain.UserPropertyWorkspace{}, MapErr(err)
	}
	norm, _ := domain.DecodeAndNormalizePropertyWorkspace(
		w.UserID, w.PropertyID, gotVer, gotPrefs, gotViews, createdAt, updatedAt, catalog,
	)
	return norm, nil
}

// DeletePropertyWorkspace removes (user_id, property_id) workspace settings and saved views.
func (s *Store) DeletePropertyWorkspace(ctx context.Context, db DBTX, userID, propertyID uuid.UUID) error {
	if db == nil {
		db = s.pool
	}
	_, err := db.Exec(ctx, `
		DELETE FROM admin_user_property_workspaces
		WHERE user_id = $1 AND property_id = $2
	`, userID, propertyID)
	return MapErr(err)
}

// LoadPropertyResourceCatalog loads active unit type IDs and booking channel codes
// strictly owned by propertyID for save-time and read-time personalisation checks.
func (s *Store) LoadPropertyResourceCatalog(ctx context.Context, db DBTX, propertyID uuid.UUID) (domain.PropertyResourceCatalog, error) {
	if db == nil {
		db = s.pool
	}
	cat := domain.PropertyResourceCatalog{
		PropertyID:   propertyID,
		UnitTypeIDs:  make(map[uuid.UUID]struct{}),
		ChannelCodes: make(map[string]struct{}),
	}

	utRows, err := db.Query(ctx, `
		SELECT id FROM unit_types
		WHERE property_id = $1 AND archived_at IS NULL
	`, propertyID)
	if err != nil {
		return cat, MapErr(err)
	}
	defer utRows.Close()
	for utRows.Next() {
		var id uuid.UUID
		if err := utRows.Scan(&id); err != nil {
			return cat, MapErr(err)
		}
		cat.UnitTypeIDs[id] = struct{}{}
	}
	if err := utRows.Err(); err != nil {
		return cat, MapErr(err)
	}

	chRows, err := db.Query(ctx, `
		SELECT UPPER(code) FROM booking_channels
		WHERE property_id = $1 AND is_active = TRUE
	`, propertyID)
	if err != nil {
		return cat, MapErr(err)
	}
	defer chRows.Close()
	for chRows.Next() {
		var code string
		if err := chRows.Scan(&code); err != nil {
			return cat, MapErr(err)
		}
		if code = strings.TrimSpace(code); code != "" {
			cat.ChannelCodes[code] = struct{}{}
		}
	}
	if err := chRows.Err(); err != nil {
		return cat, MapErr(err)
	}
	return cat, nil
}

// IsNotFound reports whether err represents a missing database record.
func IsNotFound(err error) bool {
	return errors.Is(err, domain.ErrNotFound) || errors.Is(err, pgx.ErrNoRows)
}
