package domain_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/branding"
	"github.com/tirbach/tir-bach/internal/domain"
)

func allowRolePerms(role domain.Role) domain.PermCheckFunc {
	return func(p string) bool {
		return auth.HasPermission(role, auth.Permission(p))
	}
}

func TestWorkspaceProfileRegistry_DefinitionsValidAndPermitted(t *testing.T) {
	reg := domain.WorkspaceProfileRegistry()
	if len(reg) != 4 {
		t.Fatalf("expected 4 code-defined PWE-5 profiles, got %d", len(reg))
	}

	catalog := domain.PropertyResourceCatalog{PropertyID: uuid.New()}
	seen := make(map[string]struct{})
	for _, prof := range reg {
		if _, dup := seen[prof.Code]; dup {
			t.Fatalf("duplicate profile code %q", prof.Code)
		}
		seen[prof.Code] = struct{}{}

		if prof.Name == "" || prof.Description == "" {
			t.Fatalf("profile %q missing name or description", prof.Code)
		}
		if prof.DefaultDensity != branding.ThemeDensityComfortable && prof.DefaultDensity != branding.ThemeDensityCompact {
			t.Fatalf("profile %q invalid DefaultDensity %q", prof.Code, prof.DefaultDensity)
		}
		if _, ok := domain.LookupAdminRoute(prof.DefaultLandingRoute); !ok {
			t.Fatalf("profile %q DefaultLandingRoute %q not allowlisted", prof.Code, prof.DefaultLandingRoute)
		}
		if len(prof.EligibleRoles) == 0 {
			t.Fatalf("profile %q has no eligible roles", prof.Code)
		}
		if len(prof.DefaultShortcuts) == 0 || len(prof.DefaultShortcuts) > domain.MaxPinnedShortcuts {
			t.Fatalf("profile %q shortcuts out of bounds: %d", prof.Code, len(prof.DefaultShortcuts))
		}
		for _, s := range prof.DefaultShortcuts {
			if _, ok := domain.LookupAdminRoute(s); !ok {
				t.Fatalf("profile %q shortcut %q not allowlisted", prof.Code, s)
			}
		}

		// Every profile starting dashboard layout must validate against the PWE-3 validator cleanly.
		layout := prof.ToDashboardLayout()
		if _, err := domain.ValidateDashboardLayoutInput(layout, catalog); err != nil {
			t.Fatalf("profile %q default layout failed validation: %v", prof.Code, err)
		}
	}

	// Ensure defensive copies are returned by WorkspaceProfileRegistry.
	reg[0].DefaultShortcuts[0] = "/tampered"
	fresh, ok := domain.LookupWorkspaceProfile(domain.ProfileOwnerAdmin)
	if !ok || fresh.DefaultShortcuts[0] == "/tampered" {
		t.Fatalf("WorkspaceProfileRegistry mutated internal slice")
	}
}

func TestWorkspaceProfile_RoleMappingSelectionAndRecovery(t *testing.T) {
	if got := domain.DefaultProfileCodeForRole(domain.RoleOwnerAdmin); got != domain.ProfileOwnerAdmin {
		t.Fatalf("RoleOwnerAdmin default = %q, want %q", got, domain.ProfileOwnerAdmin)
	}
	if got := domain.DefaultProfileCodeForRole(domain.RoleManager); got != domain.ProfileOwnerAdmin {
		t.Fatalf("RoleManager default = %q, want %q", got, domain.ProfileOwnerAdmin)
	}
	if got := domain.DefaultProfileCodeForRole(domain.RoleStaff); got != domain.ProfileReception {
		t.Fatalf("RoleStaff default = %q, want %q", got, domain.ProfileReception)
	}
	if got := domain.DefaultProfileCodeForRole(domain.RoleHousekeepingStaff); got != domain.ProfileHousekeeping {
		t.Fatalf("RoleHousekeepingStaff default = %q, want %q", got, domain.ProfileHousekeeping)
	}
	if got := domain.DefaultProfileCodeForRole(domain.RoleMaintenanceStaff); got != domain.ProfileMaintenance {
		t.Fatalf("RoleMaintenanceStaff default = %q, want %q", got, domain.ProfileMaintenance)
	}
	if got := domain.DefaultProfileCodeForRole(domain.RoleViewer); got != "" {
		t.Fatalf("RoleViewer default = %q, want empty", got)
	}

	mgrEligible := domain.EligibleProfilesForRole(domain.RoleManager)
	if len(mgrEligible) != 2 || mgrEligible[0].Code != domain.ProfileOwnerAdmin || mgrEligible[1].Code != domain.ProfileReception {
		t.Fatalf("expected manager eligible [owner_admin reception], got %+v", mgrEligible)
	}

	// Cross-role escalation must return ErrForbidden.
	for _, r := range []domain.Role{domain.RoleStaff, domain.RoleHousekeepingStaff, domain.RoleMaintenanceStaff, domain.RoleViewer} {
		if _, err := domain.ValidateProfileSelectionForRole(r, domain.ProfileOwnerAdmin); err != domain.ErrForbidden {
			t.Fatalf("role %s selecting owner_admin: got %v, want ErrForbidden", r, err)
		}
	}
	// Unknown profile returns validation error.
	if _, err := domain.ValidateProfileSelectionForRole(domain.RoleManager, "custom_unknown"); err == nil || err == domain.ErrForbidden {
		t.Fatalf("expected validation error for unknown profile, got %v", err)
	}
	// Valid manager switch and clearing succeed.
	if code, err := domain.ValidateProfileSelectionForRole(domain.RoleManager, " RECEPTION "); err != nil || code != domain.ProfileReception {
		t.Fatalf("manager selecting reception failed: code=%q err=%v", code, err)
	}
	if code, err := domain.ValidateProfileSelectionForRole(domain.RoleManager, ""); err != nil || code != "" {
		t.Fatalf("clearing profile selection failed: code=%q err=%v", code, err)
	}

	// Downgraded role with incompatible stored profile recovers cleanly with warning.
	prof, active, warns := domain.ResolveProfileForRole(domain.RoleHousekeepingStaff, domain.ProfileReception)
	if prof == nil || active != domain.ProfileHousekeeping || len(warns) == 0 {
		t.Fatalf("expected housekeeping recovery with warning, got prof=%v active=%q warns=%v", prof, active, warns)
	}
	profViewer, activeViewer, warnsViewer := domain.ResolveProfileForRole(domain.RoleViewer, domain.ProfileOwnerAdmin)
	if profViewer != nil || activeViewer != "" || len(warnsViewer) == 0 {
		t.Fatalf("expected viewer safe nil fallback with warning, got prof=%v active=%q warns=%v", profViewer, activeViewer, warnsViewer)
	}
}

func TestResolveLayerCascadeWithProfile_PrecedenceAndPermissionFiltering(t *testing.T) {
	userID := uuid.New()
	propID := uuid.New()

	// 1. Reception profile applies compact density, /calendar landing route, and front-desk shortcuts
	//    when global preferences do not explicitly override them.
	globalInherit := domain.DefaultUserPreferences(userID)
	globalInherit.ThemeMode = branding.ThemeModeDark
	globalInherit.Accent = branding.ThemeAccentBrass

	wsRec := domain.ResolveLayerCascadeWithProfile(globalInherit, nil, &propID, domain.RoleStaff, "", allowRolePerms(domain.RoleStaff), nil)
	if wsRec.ActiveProfile == nil || wsRec.ActiveProfile.Code != domain.ProfileReception {
		t.Fatalf("expected Reception active profile, got %+v", wsRec.ActiveProfile)
	}
	if wsRec.Theme.Mode != branding.ThemeModeDark || wsRec.Theme.Accent != branding.ThemeAccentBrass {
		t.Fatalf("expected global theme/accent preserved, got %+v", wsRec.Theme)
	}
	if wsRec.Theme.Density != branding.ThemeDensityCompact {
		t.Fatalf("expected Reception compact density, got %q", wsRec.Theme.Density)
	}
	if wsRec.DefaultRoute != "/calendar" {
		t.Fatalf("expected Reception default landing /calendar, got %q", wsRec.DefaultRoute)
	}
	if len(wsRec.Shortcuts) != 4 || wsRec.Shortcuts[0].Path != "/calendar" {
		t.Fatalf("expected Reception default shortcuts, got %+v", wsRec.Shortcuts)
	}

	// 2. Explicit user-global density and landing route override Reception profile.
	globalExplicit := domain.DefaultUserPreferences(userID)
	globalExplicit.Density = branding.ThemeDensityComfortable
	globalExplicit.ExplicitDensity = true
	globalExplicit.InheritDensity = false
	globalExplicit.DefaultRoute = "/guests"
	globalExplicit.ExplicitDefaultRoute = true
	globalExplicit.InheritDefaultRoute = false

	wsExplicit := domain.ResolveLayerCascadeWithProfile(globalExplicit, nil, &propID, domain.RoleStaff, domain.ProfileReception, allowRolePerms(domain.RoleStaff), nil)
	if wsExplicit.Theme.Density != branding.ThemeDensityComfortable || wsExplicit.DefaultRoute != "/guests" {
		t.Fatalf("expected explicit user-global overrides to beat profile, got density=%q route=%q", wsExplicit.Theme.Density, wsExplicit.DefaultRoute)
	}

	// 3. Property workspace default_route and shortcuts beat both Profile and Global.
	propWS := &domain.UserPropertyWorkspace{
		UserID:     userID,
		PropertyID: propID,
		Payload: domain.PropertyWorkspacePayload{
			DefaultRoute: "/enquiries",
			Shortcuts:    []string{"/enquiries", "/reservations"},
		},
	}
	wsProp := domain.ResolveLayerCascadeWithProfile(globalExplicit, propWS, &propID, domain.RoleStaff, domain.ProfileReception, allowRolePerms(domain.RoleStaff), nil)
	if wsProp.DefaultRoute != "/enquiries" || len(wsProp.Shortcuts) != 2 || wsProp.Shortcuts[0].Path != "/enquiries" {
		t.Fatalf("expected property workspace override to win, got route=%q shortcuts=%+v", wsProp.DefaultRoute, wsProp.Shortcuts)
	}

	// 4. Housekeeping profile dashboard widgets exclude ops.arrivals_summary and confidential shortcuts.
	hkProf, _, _ := domain.ResolveProfileForRole(domain.RoleHousekeepingStaff, "")
	hkLayout := hkProf.ToDashboardLayout()
	resolvedHK := domain.DecodeAndNormalizeDashboardLayout(1, nil, nil, domain.PropertyResourceCatalog{PropertyID: propID}, allowRolePerms(domain.RoleHousekeepingStaff), nil, &hkLayout)
	if !resolvedHK.IsDefault || resolvedHK.SourceLayer != "profile" {
		t.Fatalf("expected IsDefault=true and SourceLayer=profile, got default=%v source=%q", resolvedHK.IsDefault, resolvedHK.SourceLayer)
	}
	for _, w := range resolvedHK.Widgets {
		if w.WidgetID == domain.WidgetOpsArrivalsSummary || w.WidgetID == domain.WidgetReportsKPISnapshot {
			t.Fatalf("housekeeping profile must not contain %q", w.WidgetID)
		}
	}
}
