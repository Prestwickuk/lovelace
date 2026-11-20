package service

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Prestwickuk/lovelace/internal/auth"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/store/postgres"
)

func actorPermChecker(role domain.Role) domain.PermCheckFunc {
	return func(permKey string) bool {
		if permKey == "" {
			return true
		}
		return auth.HasPermission(role, auth.Permission(permKey))
	}
}

func (s *Service) verifyPersonalisationUser(ctx context.Context, actor Actor, targetUserID uuid.UUID, allowDuringPasswordRotation bool) (domain.AdminUser, error) {
	if actor.UserID == uuid.Nil {
		return domain.AdminUser{}, domain.ErrUnauthorized
	}
	if targetUserID != actor.UserID {
		return domain.AdminUser{}, domain.ErrForbidden
	}
	user, err := s.store.GetAdminUserByID(ctx, nil, actor.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.AdminUser{}, domain.ErrUnauthorized
		}
		return domain.AdminUser{}, err
	}
	if !user.IsActive {
		return domain.AdminUser{}, domain.ErrUnauthorized
	}
	if !allowDuringPasswordRotation && (actor.MustRotatePassword || user.MustRotatePassword) {
		return domain.AdminUser{}, domain.ErrPasswordRotationRequired
	}
	return user, nil
}

func checkUserPropertyAccess(user domain.AdminUser, propertyID uuid.UUID) error {
	if propertyID == uuid.Nil {
		return domain.NewValidationError(map[string]string{"property_id": "property_id is required"})
	}
	if user.PropertyID != nil && *user.PropertyID != propertyID {
		return domain.ErrForbidden
	}
	return nil
}

// GetUserGlobalPreferences loads targetUserID's global preferences, strictly enforcing self-ownership.
func (s *Service) GetUserGlobalPreferences(ctx context.Context, actor Actor, targetUserID uuid.UUID) (domain.UserPreferences, []string, error) {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return domain.UserPreferences{}, nil, err
	}
	return s.loadNormalizedGlobalPreferences(ctx, user)
}

func (s *Service) loadNormalizedGlobalPreferences(ctx context.Context, user domain.AdminUser) (domain.UserPreferences, []string, error) {
	rawRow, rawJSON, err := s.store.GetUserPreferencesRaw(ctx, nil, user.ID)
	if err != nil {
		if postgres.IsNotFound(err) {
			return domain.DefaultUserPreferences(user.ID), nil, nil
		}
		return domain.UserPreferences{}, nil, err
	}
	norm, warnings := domain.DecodeAndNormalizeUserPreferences(rawRow, rawJSON)
	if norm.LastPropertyID != nil {
		if *norm.LastPropertyID == uuid.Nil || (user.PropertyID != nil && *user.PropertyID != *norm.LastPropertyID) {
			norm.LastPropertyID = nil
			warnings = append(warnings, "discarded unauthorized last_property_id hint")
		} else if _, perr := s.store.GetPropertyByID(ctx, nil, *norm.LastPropertyID); perr != nil {
			norm.LastPropertyID = nil
			warnings = append(warnings, "discarded stale last_property_id hint")
		}
	}
	return norm, warnings, nil
}

// UpdateUserGlobalPreferences validates and persists targetUserID's own global preferences.
func (s *Service) UpdateUserGlobalPreferences(ctx context.Context, actor Actor, targetUserID uuid.UUID, in domain.UserPreferences) (domain.UserPreferences, error) {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return domain.UserPreferences{}, err
	}
	in.UserID = user.ID
	clean, err := domain.ValidateUserPreferencesInput(in, actorPermChecker(user.Role))
	if err != nil {
		return domain.UserPreferences{}, err
	}
	if in.LastPropertyID != nil && *in.LastPropertyID != uuid.Nil {
		if err := checkUserPropertyAccess(user, *in.LastPropertyID); err != nil {
			return domain.UserPreferences{}, err
		}
		if _, err := s.store.GetPropertyByID(ctx, nil, *in.LastPropertyID); err != nil {
			return domain.UserPreferences{}, err
		}
		propID := *in.LastPropertyID
		clean.LastPropertyID = &propID
	}
	return s.store.UpsertUserPreferences(ctx, nil, clean)
}

// ResetUserGlobalPreferences deletes targetUserID's stored global preferences so defaults apply.
func (s *Service) ResetUserGlobalPreferences(ctx context.Context, actor Actor, targetUserID uuid.UUID) error {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return err
	}
	return s.store.DeleteUserPreferences(ctx, nil, user.ID)
}

// GetUserPropertyWorkspace loads targetUserID's (user_id, property_id) workspace preferences and saved views.
func (s *Service) GetUserPropertyWorkspace(ctx context.Context, actor Actor, targetUserID, propertyID uuid.UUID) (domain.UserPropertyWorkspace, []string, error) {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return domain.UserPropertyWorkspace{}, nil, err
	}
	if err := checkUserPropertyAccess(user, propertyID); err != nil {
		return domain.UserPropertyWorkspace{}, nil, err
	}
	if _, err := s.store.GetPropertyByID(ctx, nil, propertyID); err != nil {
		return domain.UserPropertyWorkspace{}, nil, err
	}
	return s.loadNormalizedPropertyWorkspace(ctx, user.ID, propertyID)
}

func (s *Service) loadNormalizedPropertyWorkspace(ctx context.Context, userID, propertyID uuid.UUID) (domain.UserPropertyWorkspace, []string, error) {
	catalog, err := s.store.LoadPropertyResourceCatalog(ctx, nil, propertyID)
	if err != nil {
		return domain.UserPropertyWorkspace{}, nil, err
	}
	ver, rawPrefs, rawViews, createdAt, updatedAt, err := s.store.GetPropertyWorkspaceRaw(ctx, nil, userID, propertyID)
	if err != nil {
		if postgres.IsNotFound(err) {
			return domain.UserPropertyWorkspace{
				UserID:        userID,
				PropertyID:    propertyID,
				SchemaVersion: domain.CurrentWorkspaceSchemaVersion,
			}, nil, nil
		}
		return domain.UserPropertyWorkspace{}, nil, err
	}
	ws, warnings := domain.DecodeAndNormalizePropertyWorkspace(
		userID, propertyID, ver, rawPrefs, rawViews, createdAt, updatedAt, catalog,
	)
	return ws, warnings, nil
}

// UpdateUserPropertyWorkspace validates and saves (user_id, property_id) workspace display defaults,
// preserving existing valid saved operational views.
func (s *Service) UpdateUserPropertyWorkspace(
	ctx context.Context,
	actor Actor,
	targetUserID, propertyID uuid.UUID,
	in domain.PropertyWorkspacePayload,
) (domain.UserPropertyWorkspace, error) {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return domain.UserPropertyWorkspace{}, err
	}
	if err := checkUserPropertyAccess(user, propertyID); err != nil {
		return domain.UserPropertyWorkspace{}, err
	}
	if _, err := s.store.GetPropertyByID(ctx, nil, propertyID); err != nil {
		return domain.UserPropertyWorkspace{}, err
	}

	cleanPayload, err := domain.ValidatePropertyWorkspaceInput(in, actorPermChecker(user.Role))
	if err != nil {
		return domain.UserPropertyWorkspace{}, err
	}

	var saved domain.UserPropertyWorkspace
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		catalog, txErr := s.store.LoadPropertyResourceCatalog(ctx, tx, propertyID)
		if txErr != nil {
			return txErr
		}
		var existingViews []domain.SavedOperationalView
		ver, rawPrefs, rawViews, createdAt, updatedAt, getErr := s.store.GetPropertyWorkspaceRaw(ctx, tx, user.ID, propertyID)
		if getErr != nil && !postgres.IsNotFound(getErr) {
			return getErr
		}
		if getErr == nil {
			existing, _ := domain.DecodeAndNormalizePropertyWorkspace(
				user.ID, propertyID, ver, rawPrefs, rawViews, createdAt, updatedAt, catalog,
			)
			existingViews = existing.SavedViews
		}
		saved, txErr = s.store.UpsertPropertyWorkspace(ctx, tx, domain.UserPropertyWorkspace{
			UserID:        user.ID,
			PropertyID:    propertyID,
			SchemaVersion: domain.CurrentWorkspaceSchemaVersion,
			Payload:       cleanPayload,
			SavedViews:    existingViews,
		}, catalog)
		return txErr
	})
	return saved, err
}

// ResetUserPropertyWorkspace deletes (user_id, property_id) workspace overrides and saved views.
func (s *Service) ResetUserPropertyWorkspace(ctx context.Context, actor Actor, targetUserID, propertyID uuid.UUID) error {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return err
	}
	if err := checkUserPropertyAccess(user, propertyID); err != nil {
		return err
	}
	return s.store.DeletePropertyWorkspace(ctx, nil, user.ID, propertyID)
}

// SaveOperationalView validates screen permission and active-property resource ownership
// before saving a named operational filter preset under (user_id, property_id).
func (s *Service) SaveOperationalView(
	ctx context.Context,
	actor Actor,
	targetUserID, propertyID uuid.UUID,
	in domain.SavedOperationalView,
) (domain.UserPropertyWorkspace, error) {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return domain.UserPropertyWorkspace{}, err
	}
	if err := checkUserPropertyAccess(user, propertyID); err != nil {
		return domain.UserPropertyWorkspace{}, err
	}
	if _, err := s.store.GetPropertyByID(ctx, nil, propertyID); err != nil {
		return domain.UserPropertyWorkspace{}, err
	}

	screen := strings.ToLower(strings.TrimSpace(in.Screen))
	switch screen {
	case domain.SavedViewScreenCalendar, domain.SavedViewScreenReservations:
		if !auth.HasPermission(user.Role, auth.PermReservationsRead) {
			return domain.UserPropertyWorkspace{}, domain.ErrForbidden
		}
	case domain.SavedViewScreenGuests:
		if !auth.HasPermission(user.Role, auth.PermGuestsRead) {
			return domain.UserPropertyWorkspace{}, domain.ErrForbidden
		}
	}

	var saved domain.UserPropertyWorkspace
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		catalog, txErr := s.store.LoadPropertyResourceCatalog(ctx, tx, propertyID)
		if txErr != nil {
			return txErr
		}
		cleanView, vErr := domain.ValidateSavedViewInput(in, catalog)
		if vErr != nil {
			return vErr
		}

		var ws domain.UserPropertyWorkspace
		ver, rawPrefs, rawViews, createdAt, updatedAt, getErr := s.store.GetPropertyWorkspaceRaw(ctx, tx, user.ID, propertyID)
		if getErr != nil && !postgres.IsNotFound(getErr) {
			return getErr
		}
		if getErr == nil {
			ws, _ = domain.DecodeAndNormalizePropertyWorkspace(
				user.ID, propertyID, ver, rawPrefs, rawViews, createdAt, updatedAt, catalog,
			)
		} else {
			ws = domain.UserPropertyWorkspace{
				UserID:        user.ID,
				PropertyID:    propertyID,
				SchemaVersion: domain.CurrentWorkspaceSchemaVersion,
			}
		}

		replaced := false
		for i := range ws.SavedViews {
			if ws.SavedViews[i].ID == cleanView.ID ||
				(strings.EqualFold(ws.SavedViews[i].Name, cleanView.Name) && ws.SavedViews[i].Screen == cleanView.Screen) {
				cleanView.ID = ws.SavedViews[i].ID
				ws.SavedViews[i] = cleanView
				replaced = true
				break
			}
		}
		if !replaced {
			if len(ws.SavedViews) >= domain.MaxSavedViewsPerScope {
				return domain.NewValidationError(map[string]string{
					"saved_views": "maximum saved views per property reached; delete an unused view first",
				})
			}
			ws.SavedViews = append(ws.SavedViews, cleanView)
		}

		saved, txErr = s.store.UpsertPropertyWorkspace(ctx, tx, ws, catalog)
		return txErr
	})
	return saved, err
}

// DeleteOperationalView deletes one user-owned saved view scoped by (user_id, property_id).
func (s *Service) DeleteOperationalView(ctx context.Context, actor Actor, targetUserID, propertyID uuid.UUID, viewID string) error {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return err
	}
	if err := checkUserPropertyAccess(user, propertyID); err != nil {
		return err
	}
	cleanID := strings.TrimSpace(viewID)
	if cleanID == "" {
		return domain.NewValidationError(map[string]string{"view_id": "view_id is required"})
	}

	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		catalog, txErr := s.store.LoadPropertyResourceCatalog(ctx, tx, propertyID)
		if txErr != nil {
			return txErr
		}
		ver, rawPrefs, rawViews, createdAt, updatedAt, getErr := s.store.GetPropertyWorkspaceRaw(ctx, tx, user.ID, propertyID)
		if getErr != nil {
			if postgres.IsNotFound(getErr) {
				return domain.ErrNotFound
			}
			return getErr
		}
		ws, _ := domain.DecodeAndNormalizePropertyWorkspace(
			user.ID, propertyID, ver, rawPrefs, rawViews, createdAt, updatedAt, catalog,
		)
		kept := make([]domain.SavedOperationalView, 0, len(ws.SavedViews))
		found := false
		for _, v := range ws.SavedViews {
			if v.ID == cleanID {
				found = true
				continue
			}
			kept = append(kept, v)
		}
		if !found {
			return domain.ErrNotFound
		}
		ws.SavedViews = kept
		_, txErr = s.store.UpsertPropertyWorkspace(ctx, tx, ws, catalog)
		return txErr
	})
}

// ResolveWorkspace resolves the full PWE-2 cascade for the active authenticated operator:
// Platform Defaults -> (Property/Profile future) -> User Global -> User Active Property,
// revalidating live RBAC permissions, password rotation, and property scope on every call.
func (s *Service) ResolveWorkspace(ctx context.Context, actor Actor, activePropertyID *uuid.UUID) (domain.ResolvedWorkspace, error) {
	if actor.UserID == uuid.Nil {
		return domain.DefaultResolvedWorkspace(), domain.ErrUnauthorized
	}
	user, err := s.store.GetAdminUserByID(ctx, nil, actor.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.DefaultResolvedWorkspace(), domain.ErrUnauthorized
		}
		return domain.DefaultResolvedWorkspace(), err
	}
	if !user.IsActive {
		return domain.DefaultResolvedWorkspace(), domain.ErrUnauthorized
	}
	if actor.MustRotatePassword || user.MustRotatePassword {
		res := domain.DefaultResolvedWorkspace()
		res.DefaultRoute = "/account"
		return res, nil
	}

	globalPrefs, warnings, err := s.loadNormalizedGlobalPreferences(ctx, user)
	if err != nil {
		return domain.DefaultResolvedWorkspace(), err
	}

	var validatedPropID *uuid.UUID
	var propWS *domain.UserPropertyWorkspace
	var storedProfileCode string
	if activePropertyID != nil && *activePropertyID != uuid.Nil {
		if user.PropertyID != nil && *user.PropertyID != *activePropertyID {
			warnings = append(warnings, "discarded active property presentation state due to lost property scope")
		} else {
			if _, perr := s.store.GetPropertyByID(ctx, nil, *activePropertyID); perr == nil {
				pid := *activePropertyID
				validatedPropID = &pid
				ws, propWarnings, werr := s.loadNormalizedPropertyWorkspace(ctx, user.ID, pid)
				if werr == nil {
					propWS = &ws
					warnings = append(warnings, propWarnings...)
				}
				if code, cerr := s.store.GetWorkspaceProfileCode(ctx, nil, user.ID, pid); cerr == nil {
					storedProfileCode = code
				}
			} else {
				warnings = append(warnings, "active property not found; property workspace state discarded")
			}
		}
	}

	return domain.ResolveLayerCascadeWithProfile(
		globalPrefs,
		propWS,
		validatedPropID,
		user.Role,
		storedProfileCode,
		actorPermChecker(user.Role),
		warnings,
	), nil
}
