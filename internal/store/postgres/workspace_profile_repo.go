package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GetWorkspaceProfileCode loads the raw persisted selected_profile_code for (userID, propertyID).
// Returns "" when no row exists or no profile has been explicitly selected.
func (s *Store) GetWorkspaceProfileCode(ctx context.Context, db DBTX, userID, propertyID uuid.UUID) (string, error) {
	if db == nil {
		db = s.pool
	}
	var code string
	err := db.QueryRow(ctx, `
		SELECT COALESCE(selected_profile_code, '')
		FROM admin_user_property_workspaces
		WHERE user_id = $1 AND property_id = $2
	`, userID, propertyID).Scan(&code)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", MapErr(err)
	}
	return code, nil
}

// SetWorkspaceProfileCode stores (or clears with "") selected_profile_code on (userID, propertyID)
// without modifying property preferences, saved views, or saved dashboard layout.
func (s *Store) SetWorkspaceProfileCode(ctx context.Context, db DBTX, userID, propertyID uuid.UUID, profileCode string) error {
	if db == nil {
		db = s.pool
	}
	var nullableCode any
	if profileCode != "" {
		nullableCode = profileCode
	}
	_, err := db.Exec(ctx, `
		INSERT INTO admin_user_property_workspaces (user_id, property_id, selected_profile_code, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id, property_id) DO UPDATE SET
			selected_profile_code = EXCLUDED.selected_profile_code,
			updated_at            = NOW()
	`, userID, propertyID, nullableCode)
	return MapErr(err)
}

// ResetWorkspaceToProfileTx resets PWE-5 profile selection and PWE-3 saved dashboard layout back to
// role defaults while preserving saved views and global theme/accessibility settings.
// When clearNamedOverrides is true, it also resets explicit density, default_route, and shortcuts overrides.
func (s *Store) ResetWorkspaceToProfileTx(ctx context.Context, tx pgx.Tx, userID, propertyID uuid.UUID, clearNamedOverrides bool) error {
	if tx == nil {
		return s.WithTx(ctx, func(t pgx.Tx) error {
			return s.ResetWorkspaceToProfileTx(ctx, t, userID, propertyID, clearNamedOverrides)
		})
	}

	if clearNamedOverrides {
		if _, err := tx.Exec(ctx, `
			UPDATE admin_user_property_workspaces
			SET selected_profile_code   = NULL,
			    dashboard_layout        = NULL,
			    dashboard_schema_version = 1,
			    preferences             = COALESCE(preferences, '{}'::jsonb) - 'default_route' - 'shortcuts',
			    updated_at              = NOW()
			WHERE user_id = $1 AND property_id = $2
		`, userID, propertyID); err != nil {
			return MapErr(err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE admin_user_preferences
			SET explicit_density       = FALSE,
			    explicit_default_route = FALSE,
			    density                = 'comfortable',
			    default_route          = '/',
			    preferences            = COALESCE(preferences, '{}'::jsonb) - 'shortcuts',
			    updated_at             = NOW()
			WHERE user_id = $1
		`, userID); err != nil {
			return MapErr(err)
		}
		return nil
	}

	_, err := tx.Exec(ctx, `
		UPDATE admin_user_property_workspaces
		SET selected_profile_code    = NULL,
		    dashboard_layout         = NULL,
		    dashboard_schema_version = 1,
		    updated_at               = NOW()
		WHERE user_id = $1 AND property_id = $2
	`, userID, propertyID)
	return MapErr(err)
}
