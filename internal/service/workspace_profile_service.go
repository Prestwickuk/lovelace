package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/store/postgres"
)

// WorkspaceProfileState summarizes the resolved and selectable PWE-5 profiles for a user on a property.
type WorkspaceProfileState struct {
	PropertyID       uuid.UUID                        `json:"property_id"`
	ActiveProfile    *domain.WorkspaceProfileSummary  `json:"active_profile"`
	SelectedCode     string                           `json:"selected_profile_code"`
	EligibleProfiles []domain.WorkspaceProfileSummary `json:"eligible_profiles"`
	Warnings         []string                         `json:"warnings,omitempty"`
}

// WorkspaceProfileResetResult captures the refreshed profile and layout after resetting to role defaults.
type WorkspaceProfileResetResult struct {
	Profile WorkspaceProfileState          `json:"profile"`
	Layout  domain.ResolvedDashboardLayout `json:"layout"`
}

func (s *Service) resolveWorkspaceProfileState(
	ctx context.Context,
	db postgres.DBTX,
	user domain.AdminUser,
	propertyID uuid.UUID,
) (WorkspaceProfileState, *domain.WorkspaceProfileDefinition, error) {
	storedCode, err := s.store.GetWorkspaceProfileCode(ctx, db, user.ID, propertyID)
	if err != nil {
		return WorkspaceProfileState{}, nil, err
	}
	permCheck := actorPermChecker(user.Role)
	prof, activeCode, warnings := domain.ResolveProfileForRole(user.Role, storedCode)
	defCode := domain.DefaultProfileCodeForRole(user.Role)

	var eligible []domain.WorkspaceProfileSummary
	for _, ep := range domain.EligibleProfilesForRole(user.Role) {
		eligible = append(eligible, ep.ToSummary(activeCode, defCode, permCheck))
	}

	var activeSummary *domain.WorkspaceProfileSummary
	if prof != nil {
		sum := prof.ToSummary(activeCode, defCode, permCheck)
		activeSummary = &sum
	}

	return WorkspaceProfileState{
		PropertyID:       propertyID,
		ActiveProfile:    activeSummary,
		SelectedCode:     activeCode,
		EligibleProfiles: eligible,
		Warnings:         warnings,
	}, prof, nil
}

// GetWorkspaceProfileState returns targetUserID's live role-filtered PWE-5 profile state for propertyID.
func (s *Service) GetWorkspaceProfileState(
	ctx context.Context,
	actor Actor,
	targetUserID, propertyID uuid.UUID,
) (WorkspaceProfileState, error) {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return WorkspaceProfileState{}, err
	}
	if err := checkUserPropertyAccess(user, propertyID); err != nil {
		return WorkspaceProfileState{}, err
	}
	if _, err := s.store.GetPropertyByID(ctx, nil, propertyID); err != nil {
		return WorkspaceProfileState{}, err
	}
	state, _, err := s.resolveWorkspaceProfileState(ctx, nil, user, propertyID)
	return state, err
}

// SelectWorkspaceProfile persists a role-eligible profile code on (targetUserID, propertyID)
// without modifying PWE-2 preferences, saved views, or custom dashboard layouts.
func (s *Service) SelectWorkspaceProfile(
	ctx context.Context,
	actor Actor,
	targetUserID, propertyID uuid.UUID,
	profileCode string,
) (WorkspaceProfileState, error) {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return WorkspaceProfileState{}, err
	}
	if err := checkUserPropertyAccess(user, propertyID); err != nil {
		return WorkspaceProfileState{}, err
	}
	if _, err := s.store.GetPropertyByID(ctx, nil, propertyID); err != nil {
		return WorkspaceProfileState{}, err
	}

	cleanCode, err := domain.ValidateProfileSelectionForRole(user.Role, profileCode)
	if err != nil {
		return WorkspaceProfileState{}, err
	}
	if err := s.store.SetWorkspaceProfileCode(ctx, nil, user.ID, propertyID, cleanCode); err != nil {
		return WorkspaceProfileState{}, err
	}
	state, _, err := s.resolveWorkspaceProfileState(ctx, nil, user, propertyID)
	return state, err
}

// ResetWorkspaceProfile clears profile selection and custom dashboard layout back to the role default
// while preserving global theme/accessibility settings and saved operational views.
func (s *Service) ResetWorkspaceProfile(
	ctx context.Context,
	actor Actor,
	targetUserID, propertyID uuid.UUID,
	clearNamedOverrides bool,
) (WorkspaceProfileResetResult, error) {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return WorkspaceProfileResetResult{}, err
	}
	if err := checkUserPropertyAccess(user, propertyID); err != nil {
		return WorkspaceProfileResetResult{}, err
	}
	if _, err := s.store.GetPropertyByID(ctx, nil, propertyID); err != nil {
		return WorkspaceProfileResetResult{}, err
	}

	var out WorkspaceProfileResetResult
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if txErr := s.store.ResetWorkspaceToProfileTx(ctx, tx, user.ID, propertyID, clearNamedOverrides); txErr != nil {
			return txErr
		}
		state, prof, txErr := s.resolveWorkspaceProfileState(ctx, tx, user, propertyID)
		if txErr != nil {
			return txErr
		}
		catalog, txErr := s.store.LoadPropertyResourceCatalog(ctx, tx, propertyID)
		if txErr != nil {
			return txErr
		}
		var profileLayout *domain.DashboardLayout
		if prof != nil {
			dl := prof.ToDashboardLayout()
			profileLayout = &dl
		}
		resolved := domain.DecodeAndNormalizeDashboardLayout(1, nil, nil, catalog, actorPermChecker(user.Role), nil, profileLayout)
		if prof != nil {
			resolved.ProfileCode = prof.Code
			resolved.ProfileName = prof.Name
		}
		if len(state.Warnings) > 0 {
			resolved.Warnings = append(resolved.Warnings, state.Warnings...)
		}
		out = WorkspaceProfileResetResult{
			Profile: state,
			Layout:  resolved,
		}
		return nil
	})
	return out, err
}
