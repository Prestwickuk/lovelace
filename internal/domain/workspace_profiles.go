package domain

import (
	"fmt"
	"slices"
	"strings"

	"github.com/tirbach/tir-bach/internal/branding"
)

// Stable code-defined PWE-5 Workspace Profile identifiers.
const (
	ProfileOwnerAdmin   = "owner_admin"
	ProfileReception    = "reception"
	ProfileHousekeeping = "housekeeping"
	ProfileMaintenance  = "maintenance"
)

// WorkspaceProfileDefinition defines a curated, presentation-only starting workspace for compatible roles.
type WorkspaceProfileDefinition struct {
	Code                 string
	Name                 string
	Description          string
	EligibleRoles        []Role
	DefaultDensity       string
	DefaultLandingRoute  string
	AllowedLandingRoutes []string
	DefaultShortcuts     []string
	DefaultWidgets       []DashboardWidgetInstance
}

// AllowsRole reports whether live role is permitted to use or select this profile.
func (p WorkspaceProfileDefinition) AllowsRole(role Role) bool {
	return slices.Contains(p.EligibleRoles, role)
}

// ToDashboardLayout returns a schema-versioned defensive copy of this profile's widget layout.
func (p WorkspaceProfileDefinition) ToDashboardLayout() DashboardLayout {
	widgets := make([]DashboardWidgetInstance, len(p.DefaultWidgets))
	for i, w := range p.DefaultWidgets {
		cfgCopy := mapsCloneShallow(w.Config)
		widgets[i] = DashboardWidgetInstance{
			InstanceID: w.InstanceID,
			WidgetID:   w.WidgetID,
			Size:       w.Size,
			Slot:       w.Slot,
			Position:   w.Position,
			Config:     cfgCopy,
		}
	}
	return DashboardLayout{
		SchemaVersion: CurrentDashboardLayoutSchemaVersion,
		Widgets:       widgets,
	}
}

// WorkspaceProfileSummary exposes presentation metadata about a profile for SSR/API clients.
type WorkspaceProfileSummary struct {
	Code                 string             `json:"code"`
	Name                 string             `json:"name"`
	Description          string             `json:"description"`
	DefaultDensity       string             `json:"default_density"`
	DefaultLandingRoute  string             `json:"default_landing_route"`
	AllowedLandingRoutes []string           `json:"allowed_landing_routes"`
	DefaultShortcuts     []ResolvedShortcut `json:"default_shortcuts,omitempty"`
	WidgetCount          int                `json:"widget_count"`
	IsSelected           bool               `json:"is_selected"`
	IsDefaultForRole     bool               `json:"is_default_for_role"`
}

func (p WorkspaceProfileDefinition) ToSummary(selectedCode string, defaultCode string, permCheck PermCheckFunc) WorkspaceProfileSummary {
	var dummyWarnings []string
	shortcuts := resolvePermittedShortcuts(p.DefaultShortcuts, permCheck, &dummyWarnings)
	return WorkspaceProfileSummary{
		Code:                 p.Code,
		Name:                 p.Name,
		Description:          p.Description,
		DefaultDensity:       p.DefaultDensity,
		DefaultLandingRoute:  p.DefaultLandingRoute,
		AllowedLandingRoutes: slices.Clone(p.AllowedLandingRoutes),
		DefaultShortcuts:     shortcuts,
		WidgetCount:          len(p.DefaultWidgets),
		IsSelected:           selectedCode == p.Code,
		IsDefaultForRole:     defaultCode == p.Code,
	}
}

func cloneWorkspaceProfileDefinition(in WorkspaceProfileDefinition) WorkspaceProfileDefinition {
	out := in
	out.EligibleRoles = slices.Clone(in.EligibleRoles)
	out.AllowedLandingRoutes = slices.Clone(in.AllowedLandingRoutes)
	out.DefaultShortcuts = slices.Clone(in.DefaultShortcuts)
	out.DefaultWidgets = in.ToDashboardLayout().Widgets
	return out
}

func buildProfileWidgets(specs []struct {
	id   string
	wID  string
	size string
	slot string
	cfg  map[string]any
}) []DashboardWidgetInstance {
	out := make([]DashboardWidgetInstance, 0, len(specs))
	for i, s := range specs {
		cfg := DefaultWidgetConfigMap(s.wID)
		for k, v := range s.cfg {
			cfg[k] = v
		}
		out = append(out, DashboardWidgetInstance{
			InstanceID: s.id,
			WidgetID:   s.wID,
			Size:       s.size,
			Slot:       s.slot,
			Position:   i + 1,
			Config:     cfg,
		})
	}
	return out
}

var workspaceProfileRegistry = []WorkspaceProfileDefinition{
	{
		Code:                 ProfileOwnerAdmin,
		Name:                 "Owner / Administrator",
		Description:          "Performance KPIs, operational alerts, channel sync health, arrivals summary, and maintenance overview.",
		EligibleRoles:        []Role{RoleOwnerAdmin, RoleManager},
		DefaultDensity:       branding.ThemeDensityComfortable,
		DefaultLandingRoute:  "/",
		AllowedLandingRoutes: []string{"/"},
		DefaultShortcuts:     []string{"/reports", "/settings/integrations/channel-manager", "/property", "/users"},
		DefaultWidgets: buildProfileWidgets([]struct {
			id   string
			wID  string
			size string
			slot string
			cfg  map[string]any
		}{
			{"prof-owner-kpi", WidgetReportsKPISnapshot, WidgetSizeStandard, DashboardSlotMain, map[string]any{"window_days": 30, "show_prior_period": true}},
			{"prof-owner-alerts", WidgetOpsAlertsAttention, WidgetSizeStandard, DashboardSlotSide, nil},
			{"prof-owner-channels", WidgetChannelsSyncHealth, WidgetSizeCompact, DashboardSlotSide, map[string]any{"show_ok_providers": true}},
			{"prof-owner-arrivals", WidgetOpsArrivalsSummary, WidgetSizeStandard, DashboardSlotMain, map[string]any{"show_pending_holds": true, "show_in_house_count": true, "limit": 10}},
			{"prof-owner-maint", WidgetMaintOpenQueue, WidgetSizeStandard, DashboardSlotAuto, map[string]any{"priority_min": "low", "assigned_to_me": false, "limit": 10}},
			{"prof-owner-links", WidgetWorkspaceQuickShortcuts, WidgetSizeCompact, DashboardSlotSide, map[string]any{"max_items": 8, "show_saved_filters": true}},
		}),
	},
	{
		Code:                 ProfileReception,
		Name:                 "Reception",
		Description:          "Compact front-desk arrivals and departures, provisional holds/inquiries, mini calendar, and guest enquiries.",
		EligibleRoles:        []Role{RoleStaff, RoleManager},
		DefaultDensity:       branding.ThemeDensityCompact,
		DefaultLandingRoute:  "/calendar",
		AllowedLandingRoutes: []string{"/", "/calendar"},
		DefaultShortcuts:     []string{"/calendar", "/reservations", "/guests", "/enquiries"},
		DefaultWidgets: buildProfileWidgets([]struct {
			id   string
			wID  string
			size string
			slot string
			cfg  map[string]any
		}{
			{"prof-rec-arrivals", WidgetOpsArrivalsSummary, WidgetSizeStandard, DashboardSlotMain, map[string]any{"show_pending_holds": true, "show_in_house_count": true, "limit": 12}},
			{"prof-rec-pending", WidgetResPendingInquiries, WidgetSizeCompact, DashboardSlotSide, map[string]any{"include_inquiries": true, "limit": 10}},
			{"prof-rec-calendar", WidgetResMiniCalendar, WidgetSizeWide, DashboardSlotMain, map[string]any{"days": 7, "unit_type_id": nil}},
			{"prof-rec-enquiries", WidgetCRMOpenEnquiries, WidgetSizeStandard, DashboardSlotAuto, map[string]any{"priority_filter": "ALL", "only_complaints": false, "limit": 10}},
			{"prof-rec-links", WidgetWorkspaceQuickShortcuts, WidgetSizeCompact, DashboardSlotSide, map[string]any{"max_items": 8, "show_saved_filters": true}},
		}),
	},
	{
		Code:                 ProfileHousekeeping,
		Name:                 "Housekeeping",
		Description:          "Daily changeover board and unit turnaround shortcuts with comfortable spacing.",
		EligibleRoles:        []Role{RoleHousekeepingStaff},
		DefaultDensity:       branding.ThemeDensityComfortable,
		DefaultLandingRoute:  "/",
		AllowedLandingRoutes: []string{"/"},
		DefaultShortcuts:     []string{"/", "/maintenance", "/units"},
		DefaultWidgets: buildProfileWidgets([]struct {
			id   string
			wID  string
			size string
			slot string
			cfg  map[string]any
		}{
			{"prof-hk-changeover", WidgetOpsChangeoverBoard, WidgetSizeWide, DashboardSlotMain, map[string]any{"stage_filter": []string{"ALL"}, "limit": 15}},
			{"prof-hk-alerts", WidgetOpsAlertsAttention, WidgetSizeStandard, DashboardSlotSide, map[string]any{"include_categories": []string{AlertCatHousekeeping, AlertCatMaintenance}}},
			{"prof-hk-links", WidgetWorkspaceQuickShortcuts, WidgetSizeCompact, DashboardSlotSide, map[string]any{"max_items": 6, "show_saved_filters": false}},
		}),
	},
	{
		Code:                 ProfileMaintenance,
		Name:                 "Maintenance",
		Description:          "Open maintenance queue, upcoming preventative schedules, and direct facilities shortcuts.",
		EligibleRoles:        []Role{RoleMaintenanceStaff},
		DefaultDensity:       branding.ThemeDensityComfortable,
		DefaultLandingRoute:  "/maintenance",
		AllowedLandingRoutes: []string{"/maintenance"},
		DefaultShortcuts:     []string{"/maintenance", "/maintenance/schedule", "/maintenance/facilities", "/"},
		DefaultWidgets: buildProfileWidgets([]struct {
			id   string
			wID  string
			size string
			slot string
			cfg  map[string]any
		}{
			{"prof-mnt-queue", WidgetMaintOpenQueue, WidgetSizeWide, DashboardSlotMain, map[string]any{"priority_min": "low", "assigned_to_me": false, "limit": 15}},
			{"prof-mnt-sched", WidgetMaintPreventativeDue, WidgetSizeStandard, DashboardSlotSide, map[string]any{"horizon_days": 14, "limit": 10}},
			{"prof-mnt-alerts", WidgetOpsAlertsAttention, WidgetSizeCompact, DashboardSlotSide, map[string]any{"include_categories": []string{AlertCatMaintenance}}},
			{"prof-mnt-links", WidgetWorkspaceQuickShortcuts, WidgetSizeCompact, DashboardSlotSide, map[string]any{"max_items": 6, "show_saved_filters": false}},
		}),
	},
}

// WorkspaceProfileRegistry returns defensive copies of all code-defined PWE-5 profiles.
func WorkspaceProfileRegistry() []WorkspaceProfileDefinition {
	out := make([]WorkspaceProfileDefinition, len(workspaceProfileRegistry))
	for i, p := range workspaceProfileRegistry {
		out[i] = cloneWorkspaceProfileDefinition(p)
	}
	return out
}

// LookupWorkspaceProfile returns the registered profile for stable code if defined.
func LookupWorkspaceProfile(code string) (WorkspaceProfileDefinition, bool) {
	clean := strings.ToLower(strings.TrimSpace(code))
	for _, p := range workspaceProfileRegistry {
		if p.Code == clean {
			return cloneWorkspaceProfileDefinition(p), true
		}
	}
	return WorkspaceProfileDefinition{}, false
}

// DefaultProfileCodeForRole maps each administrative role to its default profile code (empty if none).
func DefaultProfileCodeForRole(role Role) string {
	switch role {
	case RoleOwnerAdmin, RoleManager:
		return ProfileOwnerAdmin
	case RoleStaff:
		return ProfileReception
	case RoleHousekeepingStaff:
		return ProfileHousekeeping
	case RoleMaintenanceStaff:
		return ProfileMaintenance
	default:
		return ""
	}
}

// EligibleProfilesForRole returns the ordered list of PWE-5 profiles selectable for role.
func EligibleProfilesForRole(role Role) []WorkspaceProfileDefinition {
	defCode := DefaultProfileCodeForRole(role)
	var out []WorkspaceProfileDefinition
	if defCode != "" {
		if def, ok := LookupWorkspaceProfile(defCode); ok {
			out = append(out, def)
		}
	}
	for _, p := range workspaceProfileRegistry {
		if p.Code != defCode && p.AllowsRole(role) {
			out = append(out, cloneWorkspaceProfileDefinition(p))
		}
	}
	return out
}

// ValidateProfileSelectionForRole validates a user-submitted profile selection against registry + live DB role.
// Empty code means "clear back to role-mapped default".
func ValidateProfileSelectionForRole(role Role, rawCode string) (string, error) {
	clean := strings.ToLower(strings.TrimSpace(rawCode))
	if clean == "" {
		return "", nil
	}
	prof, ok := LookupWorkspaceProfile(clean)
	if !ok {
		return "", NewValidationError(map[string]string{
			"profile_code": "unknown or unsupported workspace profile",
		})
	}
	if !prof.AllowsRole(role) {
		return "", ErrForbidden
	}
	return prof.Code, nil
}

// ResolveProfileForRole resolves the effective Layer 3 profile from live DB role + optional stored selection,
// cleanly discarding incompatible or obsolete profile codes without failing SSR or granting access.
func ResolveProfileForRole(role Role, storedCode string) (*WorkspaceProfileDefinition, string, []string) {
	eligible := EligibleProfilesForRole(role)
	cleanStored := strings.ToLower(strings.TrimSpace(storedCode))

	if len(eligible) == 0 {
		if cleanStored != "" {
			return nil, "", []string{
				fmt.Sprintf("saved workspace profile %q is not compatible with your current role; using safe platform defaults", cleanDiagnosticToken(cleanStored)),
			}
		}
		return nil, "", nil
	}

	if cleanStored != "" {
		for i := range eligible {
			if eligible[i].Code == cleanStored {
				chosen := cloneWorkspaceProfileDefinition(eligible[i])
				return &chosen, chosen.Code, nil
			}
		}
		def := cloneWorkspaceProfileDefinition(eligible[0])
		return &def, def.Code, []string{
			fmt.Sprintf("saved workspace profile %q is incompatible with your current role; switched to %s", cleanDiagnosticToken(cleanStored), def.Name),
		}
	}

	def := cloneWorkspaceProfileDefinition(eligible[0])
	return &def, def.Code, nil
}
