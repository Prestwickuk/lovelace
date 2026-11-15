package domain

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/branding"
)

// Schema versions for persisted PWE-2 JSONB preference payloads.
const (
	CurrentPreferenceSchemaVersion = 1
	CurrentWorkspaceSchemaVersion  = 1

	MaxPinnedShortcuts     = 6
	MaxSavedViewsPerScope  = 12
	MaxSavedViewNameLength = 60
)

// Allowlisted PWE-2 calendar horizons, groupings, sorts, and page sizes.
const (
	HorizonInherit = ""
	HorizonDay     = "day"
	Horizon7Days   = "7d"
	Horizon14Days  = "14d"
	Horizon30Days  = "30d"
	HorizonMonth   = "month"

	GroupingInherit  = ""
	GroupingUnit     = "unit"
	GroupingUnitType = "unit_type"

	SortReservationsDefault  = "stay_asc"
	SortReservationsStayDesc = "stay_desc"
	SortReservationsCreated  = "created_desc"

	SortGuestsDefault    = "name_asc"
	SortGuestsStaysDesc  = "stays_desc"
	SortGuestsRecentStay = "recent_stay_desc"

	DefaultTablePageSize = 25
)

// Allowlisted screens for saved operational filter/view presets.
const (
	SavedViewScreenCalendar     = "calendar"
	SavedViewScreenReservations = "reservations"
	SavedViewScreenGuests       = "guests"
)

// NavSectionOption identifies an allowlisted collapsible sidebar group in layout.html.
type NavSectionOption struct {
	Key   string
	Label string
}

var navSections = []NavSectionOption{
	{Key: "maintenance", Label: "Maintenance & Facilities"},
	{Key: "guests", Label: "Guests & Comms"},
	{Key: "inventory", Label: "Inventory & Holds"},
	{Key: "settings", Label: "Settings"},
}

// NavSections returns a copy of allowlisted collapsible navigation sections.
func NavSections() []NavSectionOption {
	out := make([]NavSectionOption, len(navSections))
	copy(out, navSections)
	return out
}

// IsAllowedNavSection reports whether key is an allowlisted sidebar section.
func IsAllowedNavSection(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	for _, s := range navSections {
		if s.Key == k {
			return true
		}
	}
	return false
}

// AdminRouteOption defines an allowlisted internal administrative destination.
type AdminRouteOption struct {
	Path    string
	Label   string
	PermKey string
	Section string
}

var adminRoutes = []AdminRouteOption{
	{Path: "/", Label: "Operations Centre", PermKey: "ops:read"},
	{Path: "/calendar", Label: "Booking Calendar", PermKey: "reservations:read", Section: "maintenance"},
	{Path: "/reservations", Label: "Reservations", PermKey: "reservations:read", Section: "maintenance"},
	{Path: "/payment-exceptions", Label: "Payment Exceptions", PermKey: "reservations:read", Section: "maintenance"},
	{Path: "/maintenance", Label: "Maintenance", PermKey: "maintenance:read", Section: "maintenance"},
	{Path: "/maintenance/schedule", Label: "Workload & Schedule", PermKey: "maintenance:read", Section: "maintenance"},
	{Path: "/maintenance/facilities", Label: "Facilities & Assets", PermKey: "maintenance:read", Section: "maintenance"},
	{Path: "/guests", Label: "Guest Directory", PermKey: "guests:read", Section: "guests"},
	{Path: "/enquiries", Label: "Enquiries & Requests", PermKey: "comms:read", Section: "guests"},
	{Path: "/guests/merge", Label: "Duplicate Merge Review", PermKey: "guests:merge", Section: "guests"},
	{Path: "/communications/settings", Label: "Templates & Automation", PermKey: "comms:read", Section: "guests"},
	{Path: "/blocks", Label: "Bulk Blockouts", PermKey: "blocks:read", Section: "inventory"},
	{Path: "/releases", Label: "Scheduled Releases", PermKey: "releases:read", Section: "inventory"},
	{Path: "/unit-types", Label: "Unit Types", PermKey: "inventory:read", Section: "inventory"},
	{Path: "/units", Label: "Units & Pitches", PermKey: "inventory:read", Section: "inventory"},
	{Path: "/extras", Label: "Extras & Add-ons", PermKey: "inventory:read", Section: "inventory"},
	{Path: "/rates", Label: "Rates & Preview", PermKey: "rates:read", Section: "inventory"},
	{Path: "/reports", Label: "Reports & BI", PermKey: "reports:read", Section: "inventory"},
	{Path: "/property", Label: "Site Settings", PermKey: "property:read", Section: "settings"},
	{Path: "/settings/integrations/channel-manager", Label: "Channel Manager", PermKey: "channels:read", Section: "settings"},
	{Path: "/users", Label: "Admin Users", PermKey: "users:manage", Section: "settings"},
	{Path: "/audit-logs", Label: "Audit Log", PermKey: "audit:read", Section: "settings"},
	{Path: "/account", Label: "My Account", PermKey: "", Section: "settings"},
}

// AllowedAdminRoutes returns the full allowlisted administrative route catalog.
func AllowedAdminRoutes() []AdminRouteOption {
	out := make([]AdminRouteOption, len(adminRoutes))
	copy(out, adminRoutes)
	return out
}

// LookupAdminRoute finds an allowlisted internal administrative route.
func LookupAdminRoute(path string) (AdminRouteOption, bool) {
	clean := strings.TrimSpace(path)
	for _, r := range adminRoutes {
		if r.Path == clean {
			return r, true
		}
	}
	return AdminRouteOption{}, false
}

// UserPreferencePayload holds bounded JSONB global display/navigation settings.
type UserPreferencePayload struct {
	Shortcuts         []string `json:"shortcuts,omitempty"`
	CollapsedSections []string `json:"collapsed_sections,omitempty"`
	CalendarHorizon   string   `json:"calendar_horizon,omitempty"`
	CalendarGrouping  string   `json:"calendar_grouping,omitempty"`
	TablePageSize     int      `json:"table_page_size,omitempty"`
}

// UserPreferences stores authenticated admin-user global PWE-2 personalisation.
type UserPreferences struct {
	UserID         uuid.UUID             `json:"user_id"`
	SchemaVersion  int                   `json:"schema_version"`
	ThemeMode      string                `json:"theme_mode"`
	Accent         string                `json:"accent"`
	Density        string                `json:"density"`
	TextScale      string                `json:"text_scale"`
	HighContrast   bool                  `json:"high_contrast"`
	ReducedMotion  bool                  `json:"reduced_motion"`
	DefaultRoute   string                `json:"default_route"`
	LastPropertyID *uuid.UUID            `json:"last_property_id,omitempty"`
	Payload        UserPreferencePayload `json:"payload"`
	CreatedAt      time.Time             `json:"created_at"`
	UpdatedAt      time.Time             `json:"updated_at"`
}

// DefaultUserPreferences returns safe platform defaults for global preferences.
func DefaultUserPreferences(userID uuid.UUID) UserPreferences {
	def := branding.DefaultTheme()
	return UserPreferences{
		UserID:        userID,
		SchemaVersion: CurrentPreferenceSchemaVersion,
		ThemeMode:     def.Mode,
		Accent:        def.Accent,
		Density:       def.Density,
		TextScale:     def.TextScale,
		HighContrast:  false,
		ReducedMotion: false,
		DefaultRoute:  "/",
		Payload: UserPreferencePayload{
			CalendarHorizon:  HorizonMonth,
			CalendarGrouping: GroupingUnit,
			TablePageSize:    DefaultTablePageSize,
		},
	}
}

// ToThemeSelection maps user global fields into PWE-1 allowlisted root HTML tokens.
func (p UserPreferences) ToThemeSelection() branding.ThemeSelection {
	contrast := branding.ThemeContrastStandard
	if p.HighContrast {
		contrast = branding.ThemeContrastHigh
	}
	motion := branding.ThemeMotionSystem
	if p.ReducedMotion {
		motion = branding.ThemeMotionReduced
	}
	return branding.ResolveTheme(branding.ThemeSelection{
		Mode:      p.ThemeMode,
		Accent:    p.Accent,
		Contrast:  contrast,
		Motion:    motion,
		Density:   p.Density,
		TextScale: p.TextScale,
	})
}

// SavedOperationalView represents a user-owned, property-scoped saved filter preset.
type SavedOperationalView struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Screen  string            `json:"screen"`
	Filters map[string]string `json:"filters"`
}

// PropertyWorkspacePayload holds bounded JSONB property-scoped presentation defaults.
type PropertyWorkspacePayload struct {
	CalendarHorizon  string   `json:"calendar_horizon,omitempty"`
	CalendarGrouping string   `json:"calendar_grouping,omitempty"`
	TablePageSize    int      `json:"table_page_size,omitempty"`
	ReservationsSort string   `json:"reservations_sort,omitempty"`
	GuestsSort       string   `json:"guests_sort,omitempty"`
	DefaultRoute     string   `json:"default_route,omitempty"`
	Shortcuts        []string `json:"shortcuts,omitempty"`
}

// UserPropertyWorkspace stores (user_id, property_id) workspace preferences and saved views.
type UserPropertyWorkspace struct {
	UserID        uuid.UUID                `json:"user_id"`
	PropertyID    uuid.UUID                `json:"property_id"`
	SchemaVersion int                      `json:"schema_version"`
	Payload       PropertyWorkspacePayload `json:"payload"`
	SavedViews    []SavedOperationalView   `json:"saved_views"`
	CreatedAt     time.Time                `json:"created_at"`
	UpdatedAt     time.Time                `json:"updated_at"`
}

// PropertyResourceCatalog describes valid property-owned IDs/codes for validation.
type PropertyResourceCatalog struct {
	PropertyID   uuid.UUID
	UnitTypeIDs  map[uuid.UUID]struct{}
	ChannelCodes map[string]struct{}
}

// PermCheckFunc checks whether the authenticated actor currently holds permKey.
type PermCheckFunc func(permKey string) bool

// ResolvedShortcut is a permission-filtered navigation shortcut ready for rendering.
type ResolvedShortcut struct {
	Path  string
	Label string
}

// ResolvedWorkspace is the final sanitized PWE-2 layer cascade output for SSR.
type ResolvedWorkspace struct {
	Theme             branding.ThemeSelection
	DefaultRoute      string
	Shortcuts         []ResolvedShortcut
	CollapsedSections map[string]bool
	CalendarHorizon   string
	CalendarGrouping  string
	TablePageSize     int
	ReservationsSort  string
	GuestsSort        string
	SavedViews        []SavedOperationalView
	Global            UserPreferences
	PropertyWorkspace *UserPropertyWorkspace
	ActivePropertyID  *uuid.UUID
	Warnings          []string
}

// DefaultResolvedWorkspace returns safe platform defaults for anonymous/rotation states.
func DefaultResolvedWorkspace() ResolvedWorkspace {
	return ResolvedWorkspace{
		Theme:             branding.DefaultTheme(),
		DefaultRoute:      "/",
		CollapsedSections: map[string]bool{},
		CalendarHorizon:   HorizonMonth,
		CalendarGrouping:  GroupingUnit,
		TablePageSize:     DefaultTablePageSize,
		ReservationsSort:  SortReservationsDefault,
		GuestsSort:        SortGuestsDefault,
	}
}

// IsAllowedHorizon checks allowlisted calendar horizon tokens.
func IsAllowedHorizon(v string, allowEmpty bool) (string, bool) {
	clean := strings.ToLower(strings.TrimSpace(v))
	switch clean {
	case "":
		return "", allowEmpty
	case HorizonDay, Horizon7Days, Horizon14Days, Horizon30Days, HorizonMonth:
		return clean, true
	case "week":
		return Horizon7Days, true
	default:
		return "", false
	}
}

// IsAllowedGrouping checks allowlisted calendar grouping tokens.
func IsAllowedGrouping(v string, allowEmpty bool) (string, bool) {
	clean := strings.ToLower(strings.TrimSpace(v))
	switch clean {
	case "":
		return "", allowEmpty
	case GroupingUnit, GroupingUnitType:
		return clean, true
	default:
		return "", false
	}
}

// IsAllowedPageSize checks allowlisted pagination page sizes.
func IsAllowedPageSize(size int, allowZero bool) bool {
	if size == 0 {
		return allowZero
	}
	switch size {
	case 10, 25, 50, 100:
		return true
	default:
		return false
	}
}

// IsAllowedReservationsSort checks allowlisted reservations sort tokens.
func IsAllowedReservationsSort(v string, allowEmpty bool) (string, bool) {
	clean := strings.ToLower(strings.TrimSpace(v))
	switch clean {
	case "":
		return "", allowEmpty
	case SortReservationsDefault, SortReservationsStayDesc, SortReservationsCreated:
		return clean, true
	default:
		return "", false
	}
}

// IsAllowedGuestsSort checks allowlisted guest directory sort tokens.
func IsAllowedGuestsSort(v string, allowEmpty bool) (string, bool) {
	clean := strings.ToLower(strings.TrimSpace(v))
	switch clean {
	case "":
		return "", allowEmpty
	case SortGuestsDefault, SortGuestsStaysDesc, SortGuestsRecentStay:
		return clean, true
	default:
		return "", false
	}
}

// DecodeAndNormalizeUserPreferences strictly upcasts, parses raw JSONB, and falls back
// field-by-field so corrupt/obsolete values never wipe sibling preferences or lock out SSR.
func DecodeAndNormalizeUserPreferences(
	row UserPreferences,
	rawPayload []byte,
) (UserPreferences, []string) {
	def := DefaultUserPreferences(row.UserID)
	out := def
	out.CreatedAt = row.CreatedAt
	out.UpdatedAt = row.UpdatedAt
	out.LastPropertyID = row.LastPropertyID
	var warnings []string

	if row.SchemaVersion < 1 || row.SchemaVersion > CurrentPreferenceSchemaVersion {
		warnings = append(warnings, fmt.Sprintf("user preference schema_version %d normalized to v%d", row.SchemaVersion, CurrentPreferenceSchemaVersion))
	}
	out.SchemaVersion = CurrentPreferenceSchemaVersion

	if m := strings.ToLower(strings.TrimSpace(row.ThemeMode)); m == branding.ThemeModeSystem || m == branding.ThemeModeLight || m == branding.ThemeModeDark {
		out.ThemeMode = m
	} else if row.ThemeMode != "" {
		warnings = append(warnings, "unrecognised theme_mode reset to system")
	}

	accent := strings.ToLower(strings.TrimSpace(row.Accent))
	validAccent := false
	for _, opt := range branding.CuratedAccents() {
		if accent == opt.Key {
			out.Accent = opt.Key
			validAccent = true
			break
		}
	}
	if !validAccent && row.Accent != "" {
		warnings = append(warnings, "unrecognised accent reset to teal")
	}

	if d := strings.ToLower(strings.TrimSpace(row.Density)); d == branding.ThemeDensityComfortable || d == branding.ThemeDensityCompact {
		out.Density = d
	} else if row.Density != "" {
		warnings = append(warnings, "unrecognised density reset to comfortable")
	}

	switch strings.ToLower(strings.TrimSpace(row.TextScale)) {
	case branding.ThemeTextScaleDefault, "normal":
		out.TextScale = branding.ThemeTextScaleDefault
	case branding.ThemeTextScaleLarge, branding.ThemeTextScaleXL:
		out.TextScale = strings.ToLower(strings.TrimSpace(row.TextScale))
	default:
		if row.TextScale != "" {
			warnings = append(warnings, "unrecognised text_scale reset to default")
		}
	}

	out.HighContrast = row.HighContrast
	out.ReducedMotion = row.ReducedMotion

	if route, ok := LookupAdminRoute(row.DefaultRoute); ok {
		out.DefaultRoute = route.Path
	} else if strings.TrimSpace(row.DefaultRoute) != "" {
		warnings = append(warnings, "invalid default_route reset to /")
	}

	if len(bytesTrimSpace(rawPayload)) > 0 {
		var rawObj map[string]json.RawMessage
		if err := json.Unmarshal(rawPayload, &rawObj); err != nil {
			warnings = append(warnings, "malformed global preferences JSONB recovered with defaults")
			return out, warnings
		}
		if rawVal, exists := rawObj["shortcuts"]; exists {
			var list []string
			if err := json.Unmarshal(rawVal, &list); err != nil {
				warnings = append(warnings, "invalid shortcuts JSONB field ignored")
			} else {
				clean, skipped := sanitizeRouteSlice(list, MaxPinnedShortcuts)
				out.Payload.Shortcuts = clean
				if skipped > 0 {
					warnings = append(warnings, fmt.Sprintf("%d invalid shortcut(s) ignored", skipped))
				}
			}
		}
		if rawVal, exists := rawObj["collapsed_sections"]; exists {
			var list []string
			if err := json.Unmarshal(rawVal, &list); err != nil {
				warnings = append(warnings, "invalid collapsed_sections JSONB field ignored")
			} else {
				clean, skipped := sanitizeSectionSlice(list)
				out.Payload.CollapsedSections = clean
				if skipped > 0 {
					warnings = append(warnings, fmt.Sprintf("%d invalid collapsed section(s) ignored", skipped))
				}
			}
		}
		if rawVal, exists := rawObj["calendar_horizon"]; exists {
			var h string
			if err := json.Unmarshal(rawVal, &h); err != nil {
				warnings = append(warnings, "invalid calendar_horizon JSONB field ignored")
			} else if norm, ok := IsAllowedHorizon(h, true); ok && norm != "" {
				out.Payload.CalendarHorizon = norm
			} else if strings.TrimSpace(h) != "" {
				warnings = append(warnings, "unrecognised calendar_horizon reset to month")
			}
		}
		if rawVal, exists := rawObj["calendar_grouping"]; exists {
			var g string
			if err := json.Unmarshal(rawVal, &g); err != nil {
				warnings = append(warnings, "invalid calendar_grouping JSONB field ignored")
			} else if norm, ok := IsAllowedGrouping(g, true); ok && norm != "" {
				out.Payload.CalendarGrouping = norm
			} else if strings.TrimSpace(g) != "" {
				warnings = append(warnings, "unrecognised calendar_grouping reset to unit")
			}
		}
		if rawVal, exists := rawObj["table_page_size"]; exists {
			var ps int
			if err := json.Unmarshal(rawVal, &ps); err != nil {
				warnings = append(warnings, "invalid table_page_size JSONB field ignored")
			} else if IsAllowedPageSize(ps, false) {
				out.Payload.TablePageSize = ps
			} else if ps != 0 {
				warnings = append(warnings, "unsupported table_page_size reset to 25")
			}
		}
	}

	return out, warnings
}

// DecodeAndNormalizePropertyWorkspace upcasts and safely normalizes persisted
// property-scoped preferences and saved views field-by-field.
func DecodeAndNormalizePropertyWorkspace(
	userID, propertyID uuid.UUID,
	schemaVersion int,
	rawPrefs []byte,
	rawViews []byte,
	createdAt, updatedAt time.Time,
	catalog PropertyResourceCatalog,
) (UserPropertyWorkspace, []string) {
	out := UserPropertyWorkspace{
		UserID:        userID,
		PropertyID:    propertyID,
		SchemaVersion: CurrentWorkspaceSchemaVersion,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}
	var warnings []string

	if schemaVersion < 1 || schemaVersion > CurrentWorkspaceSchemaVersion {
		warnings = append(warnings, fmt.Sprintf("property workspace schema_version %d normalized to v%d", schemaVersion, CurrentWorkspaceSchemaVersion))
	}

	if len(bytesTrimSpace(rawPrefs)) > 0 {
		var rawObj map[string]json.RawMessage
		if err := json.Unmarshal(rawPrefs, &rawObj); err != nil {
			warnings = append(warnings, "malformed property workspace preferences JSONB recovered with defaults")
		} else {
			if rawVal, exists := rawObj["calendar_horizon"]; exists {
				var v string
				if err := json.Unmarshal(rawVal, &v); err != nil {
					warnings = append(warnings, "invalid property calendar_horizon ignored")
				} else if norm, ok := IsAllowedHorizon(v, true); ok {
					out.Payload.CalendarHorizon = norm
				} else {
					warnings = append(warnings, "unsupported property calendar_horizon ignored")
				}
			}
			if rawVal, exists := rawObj["calendar_grouping"]; exists {
				var v string
				if err := json.Unmarshal(rawVal, &v); err != nil {
					warnings = append(warnings, "invalid property calendar_grouping ignored")
				} else if norm, ok := IsAllowedGrouping(v, true); ok {
					out.Payload.CalendarGrouping = norm
				} else {
					warnings = append(warnings, "unsupported property calendar_grouping ignored")
				}
			}
			if rawVal, exists := rawObj["table_page_size"]; exists {
				var ps int
				if err := json.Unmarshal(rawVal, &ps); err != nil {
					warnings = append(warnings, "invalid property table_page_size ignored")
				} else if IsAllowedPageSize(ps, true) {
					out.Payload.TablePageSize = ps
				} else {
					warnings = append(warnings, "unsupported property table_page_size ignored")
				}
			}
			if rawVal, exists := rawObj["reservations_sort"]; exists {
				var v string
				if err := json.Unmarshal(rawVal, &v); err != nil {
					warnings = append(warnings, "invalid property reservations_sort ignored")
				} else if norm, ok := IsAllowedReservationsSort(v, true); ok {
					out.Payload.ReservationsSort = norm
				} else {
					warnings = append(warnings, "unsupported property reservations_sort ignored")
				}
			}
			if rawVal, exists := rawObj["guests_sort"]; exists {
				var v string
				if err := json.Unmarshal(rawVal, &v); err != nil {
					warnings = append(warnings, "invalid property guests_sort ignored")
				} else if norm, ok := IsAllowedGuestsSort(v, true); ok {
					out.Payload.GuestsSort = norm
				} else {
					warnings = append(warnings, "unsupported property guests_sort ignored")
				}
			}
			if rawVal, exists := rawObj["default_route"]; exists {
				var v string
				if err := json.Unmarshal(rawVal, &v); err != nil {
					warnings = append(warnings, "invalid property default_route ignored")
				} else if strings.TrimSpace(v) == "" {
					out.Payload.DefaultRoute = ""
				} else if rt, ok := LookupAdminRoute(v); ok {
					out.Payload.DefaultRoute = rt.Path
				} else {
					warnings = append(warnings, "unsupported property default_route ignored")
				}
			}
			if rawVal, exists := rawObj["shortcuts"]; exists {
				var list []string
				if err := json.Unmarshal(rawVal, &list); err != nil {
					warnings = append(warnings, "invalid property shortcuts ignored")
				} else {
					clean, skipped := sanitizeRouteSlice(list, MaxPinnedShortcuts)
					out.Payload.Shortcuts = clean
					if skipped > 0 {
						warnings = append(warnings, fmt.Sprintf("%d invalid property shortcut(s) ignored", skipped))
					}
				}
			}
		}
	}

	if len(bytesTrimSpace(rawViews)) > 0 {
		var rawArr []json.RawMessage
		if err := json.Unmarshal(rawViews, &rawArr); err != nil {
			warnings = append(warnings, "malformed saved_views JSONB recovered cleanly")
		} else {
			for _, elem := range rawArr {
				if len(out.SavedViews) >= MaxSavedViewsPerScope {
					break
				}
				var candidate SavedOperationalView
				if err := json.Unmarshal(elem, &candidate); err != nil {
					warnings = append(warnings, "skipped malformed saved operational view entry")
					continue
				}
				sanitized, itemWarnings, ok := sanitizeSavedViewOnRead(candidate, catalog)
				warnings = append(warnings, itemWarnings...)
				if ok {
					out.SavedViews = append(out.SavedViews, sanitized)
				}
			}
		}
	}

	return out, warnings
}

// ValidateUserPreferencesInput validates interactive user global preference submissions.
func ValidateUserPreferencesInput(in UserPreferences, permCheck PermCheckFunc) (UserPreferences, error) {
	errs := make(map[string]string)
	out := DefaultUserPreferences(in.UserID)

	mode := strings.ToLower(strings.TrimSpace(in.ThemeMode))
	switch mode {
	case branding.ThemeModeSystem, branding.ThemeModeLight, branding.ThemeModeDark:
		out.ThemeMode = mode
	default:
		errs["theme_mode"] = "must be system, light, or dark"
	}

	accent := strings.ToLower(strings.TrimSpace(in.Accent))
	accentValid := false
	for _, opt := range branding.CuratedAccents() {
		if accent == opt.Key {
			out.Accent = opt.Key
			accentValid = true
			break
		}
	}
	if !accentValid {
		errs["accent"] = "must be one of teal, olive, slate, or brass"
	}

	density := strings.ToLower(strings.TrimSpace(in.Density))
	switch density {
	case branding.ThemeDensityComfortable, branding.ThemeDensityCompact:
		out.Density = density
	default:
		errs["density"] = "must be comfortable or compact"
	}

	scale := strings.ToLower(strings.TrimSpace(in.TextScale))
	switch scale {
	case branding.ThemeTextScaleDefault, branding.ThemeTextScaleLarge, branding.ThemeTextScaleXL:
		out.TextScale = scale
	default:
		errs["text_scale"] = "must be default, large, or xl"
	}

	out.HighContrast = in.HighContrast
	out.ReducedMotion = in.ReducedMotion

	routeOpt, ok := LookupAdminRoute(in.DefaultRoute)
	if !ok {
		errs["default_route"] = "must be an allowlisted administrative console route"
	} else if permCheck != nil && routeOpt.PermKey != "" && !permCheck(routeOpt.PermKey) {
		errs["default_route"] = "cannot select a landing route outside your permitted access"
	} else {
		out.DefaultRoute = routeOpt.Path
	}

	if len(in.Payload.Shortcuts) > MaxPinnedShortcuts {
		errs["shortcuts"] = fmt.Sprintf("at most %d navigation shortcuts are allowed", MaxPinnedShortcuts)
	} else {
		var cleanShortcuts []string
		seen := make(map[string]struct{})
		for _, rawPath := range in.Payload.Shortcuts {
			opt, ok := LookupAdminRoute(rawPath)
			if !ok {
				errs["shortcuts"] = "contains an unsupported navigation shortcut"
				break
			}
			if permCheck != nil && opt.PermKey != "" && !permCheck(opt.PermKey) {
				errs["shortcuts"] = "contains a shortcut outside your current permissions"
				break
			}
			if _, dup := seen[opt.Path]; !dup {
				seen[opt.Path] = struct{}{}
				cleanShortcuts = append(cleanShortcuts, opt.Path)
			}
		}
		out.Payload.Shortcuts = cleanShortcuts
	}

	var cleanSections []string
	seenSec := make(map[string]struct{})
	for _, rawSec := range in.Payload.CollapsedSections {
		sec := strings.ToLower(strings.TrimSpace(rawSec))
		if !IsAllowedNavSection(sec) {
			errs["collapsed_sections"] = "contains an unknown sidebar section"
			break
		}
		if _, dup := seenSec[sec]; !dup {
			seenSec[sec] = struct{}{}
			cleanSections = append(cleanSections, sec)
		}
	}
	out.Payload.CollapsedSections = cleanSections

	if in.Payload.CalendarHorizon == "" {
		out.Payload.CalendarHorizon = HorizonMonth
	} else if norm, ok := IsAllowedHorizon(in.Payload.CalendarHorizon, false); ok {
		out.Payload.CalendarHorizon = norm
	} else {
		errs["calendar_horizon"] = "must be day, 7d, 14d, 30d, or month"
	}

	if in.Payload.CalendarGrouping == "" {
		out.Payload.CalendarGrouping = GroupingUnit
	} else if norm, ok := IsAllowedGrouping(in.Payload.CalendarGrouping, false); ok {
		out.Payload.CalendarGrouping = norm
	} else {
		errs["calendar_grouping"] = "must be unit or unit_type"
	}

	if in.Payload.TablePageSize == 0 {
		out.Payload.TablePageSize = DefaultTablePageSize
	} else if IsAllowedPageSize(in.Payload.TablePageSize, false) {
		out.Payload.TablePageSize = in.Payload.TablePageSize
	} else {
		errs["table_page_size"] = "must be 10, 25, 50, or 100"
	}

	if len(errs) > 0 {
		return UserPreferences{}, NewValidationError(errs)
	}
	return out, nil
}

// ValidatePropertyWorkspaceInput validates interactive property-scoped workspace settings.
func ValidatePropertyWorkspaceInput(in PropertyWorkspacePayload, permCheck PermCheckFunc) (PropertyWorkspacePayload, error) {
	errs := make(map[string]string)
	var out PropertyWorkspacePayload

	if norm, ok := IsAllowedHorizon(in.CalendarHorizon, true); ok {
		out.CalendarHorizon = norm
	} else {
		errs["calendar_horizon"] = "must be empty, day, 7d, 14d, 30d, or month"
	}

	if norm, ok := IsAllowedGrouping(in.CalendarGrouping, true); ok {
		out.CalendarGrouping = norm
	} else {
		errs["calendar_grouping"] = "must be empty, unit, or unit_type"
	}

	if IsAllowedPageSize(in.TablePageSize, true) {
		out.TablePageSize = in.TablePageSize
	} else {
		errs["table_page_size"] = "must be 0, 10, 25, 50, or 100"
	}

	if norm, ok := IsAllowedReservationsSort(in.ReservationsSort, true); ok {
		out.ReservationsSort = norm
	} else {
		errs["reservations_sort"] = "must be stay_asc, stay_desc, or created_desc"
	}

	if norm, ok := IsAllowedGuestsSort(in.GuestsSort, true); ok {
		out.GuestsSort = norm
	} else {
		errs["guests_sort"] = "must be name_asc, stays_desc, or recent_stay_desc"
	}

	if trimmed := strings.TrimSpace(in.DefaultRoute); trimmed != "" {
		opt, ok := LookupAdminRoute(trimmed)
		if !ok {
			errs["default_route"] = "must be an allowlisted administrative console route"
		} else if permCheck != nil && opt.PermKey != "" && !permCheck(opt.PermKey) {
			errs["default_route"] = "cannot select a landing route outside your permitted access"
		} else {
			out.DefaultRoute = opt.Path
		}
	}

	if len(in.Shortcuts) > MaxPinnedShortcuts {
		errs["shortcuts"] = fmt.Sprintf("at most %d navigation shortcuts are allowed", MaxPinnedShortcuts)
	} else if len(in.Shortcuts) > 0 {
		seen := make(map[string]struct{})
		for _, rawPath := range in.Shortcuts {
			opt, ok := LookupAdminRoute(rawPath)
			if !ok {
				errs["shortcuts"] = "contains an unsupported navigation shortcut"
				break
			}
			if permCheck != nil && opt.PermKey != "" && !permCheck(opt.PermKey) {
				errs["shortcuts"] = "contains a shortcut outside your current permissions"
				break
			}
			if _, dup := seen[opt.Path]; !dup {
				seen[opt.Path] = struct{}{}
				out.Shortcuts = append(out.Shortcuts, opt.Path)
			}
		}
	}

	if len(errs) > 0 {
		return PropertyWorkspacePayload{}, NewValidationError(errs)
	}
	return out, nil
}

// ValidateSavedViewInput validates a new or updated saved operational view strictly against
// allowlisted screens, filter keys, and active property-owned resource catalogs.
func ValidateSavedViewInput(in SavedOperationalView, catalog PropertyResourceCatalog) (SavedOperationalView, error) {
	errs := make(map[string]string)

	name := cleanPlainTextLabel(in.Name)
	if name == "" || len([]rune(name)) > MaxSavedViewNameLength {
		errs["name"] = fmt.Sprintf("must be 1–%d printable characters", MaxSavedViewNameLength)
	}

	screen := strings.ToLower(strings.TrimSpace(in.Screen))
	cleanFilters := make(map[string]string)
	switch screen {
	case SavedViewScreenCalendar:
		validateCalendarSavedFilters(in.Filters, catalog, cleanFilters, errs)
	case SavedViewScreenReservations:
		validateReservationsSavedFilters(in.Filters, catalog, cleanFilters, errs)
	case SavedViewScreenGuests:
		validateGuestsSavedFilters(in.Filters, cleanFilters, errs)
	default:
		errs["screen"] = "must be calendar, reservations, or guests"
	}

	if len(errs) > 0 {
		return SavedOperationalView{}, NewValidationError(errs)
	}

	id := strings.TrimSpace(in.ID)
	if id == "" {
		uid, err := uuid.NewV7()
		if err != nil {
			uid = uuid.New()
		}
		id = uid.String()
	}

	return SavedOperationalView{
		ID:      id,
		Name:    name,
		Screen:  screen,
		Filters: cleanFilters,
	}, nil
}

// ResolveLayerCascade merges Platform -> (Property/Profile future) -> User Global -> User Active Property
// and filters landing routes, shortcuts, and saved views by live actor permissions.
func ResolveLayerCascade(
	globalPrefs UserPreferences,
	propWorkspace *UserPropertyWorkspace,
	activePropID *uuid.UUID,
	permCheck PermCheckFunc,
	initialWarnings []string,
) ResolvedWorkspace {
	res := DefaultResolvedWorkspace()
	res.Global = globalPrefs
	res.PropertyWorkspace = propWorkspace
	res.ActivePropertyID = activePropID
	if len(initialWarnings) > 0 {
		res.Warnings = append(res.Warnings, initialWarnings...)
	}

	// Layer 4: User Global Preferences
	res.Theme = globalPrefs.ToThemeSelection()
	if globalPrefs.Payload.CalendarHorizon != "" {
		if norm, ok := IsAllowedHorizon(globalPrefs.Payload.CalendarHorizon, false); ok {
			res.CalendarHorizon = norm
		}
	}
	if globalPrefs.Payload.CalendarGrouping != "" {
		if norm, ok := IsAllowedGrouping(globalPrefs.Payload.CalendarGrouping, false); ok {
			res.CalendarGrouping = norm
		}
	}
	if IsAllowedPageSize(globalPrefs.Payload.TablePageSize, false) {
		res.TablePageSize = globalPrefs.Payload.TablePageSize
	}
	for _, sec := range globalPrefs.Payload.CollapsedSections {
		if IsAllowedNavSection(sec) {
			res.CollapsedSections[sec] = true
		}
	}

	rawDefaultRoute := globalPrefs.DefaultRoute
	rawShortcuts := globalPrefs.Payload.Shortcuts

	// Layer 5: User + Active Authorized Property Workspace Overrides
	if propWorkspace != nil && activePropID != nil && propWorkspace.PropertyID == *activePropID {
		if propWorkspace.Payload.CalendarHorizon != "" {
			if norm, ok := IsAllowedHorizon(propWorkspace.Payload.CalendarHorizon, false); ok {
				res.CalendarHorizon = norm
			}
		}
		if propWorkspace.Payload.CalendarGrouping != "" {
			if norm, ok := IsAllowedGrouping(propWorkspace.Payload.CalendarGrouping, false); ok {
				res.CalendarGrouping = norm
			}
		}
		if IsAllowedPageSize(propWorkspace.Payload.TablePageSize, false) {
			res.TablePageSize = propWorkspace.Payload.TablePageSize
		}
		if propWorkspace.Payload.ReservationsSort != "" {
			if norm, ok := IsAllowedReservationsSort(propWorkspace.Payload.ReservationsSort, false); ok {
				res.ReservationsSort = norm
			}
		}
		if propWorkspace.Payload.GuestsSort != "" {
			if norm, ok := IsAllowedGuestsSort(propWorkspace.Payload.GuestsSort, false); ok {
				res.GuestsSort = norm
			}
		}
		if propWorkspace.Payload.DefaultRoute != "" {
			rawDefaultRoute = propWorkspace.Payload.DefaultRoute
		}
		if len(propWorkspace.Payload.Shortcuts) > 0 {
			rawShortcuts = propWorkspace.Payload.Shortcuts
		}

		for _, view := range propWorkspace.SavedViews {
			if viewPermitted(view.Screen, permCheck) {
				res.SavedViews = append(res.SavedViews, view)
			} else {
				res.Warnings = append(res.Warnings, fmt.Sprintf("saved view %q hidden because screen permission was revoked", view.Name))
			}
		}
	}

	// Render-time permission filtering for landing route and shortcuts.
	res.DefaultRoute = resolvePermittedRoute(rawDefaultRoute, permCheck, &res.Warnings)
	res.Shortcuts = resolvePermittedShortcuts(rawShortcuts, permCheck, &res.Warnings)
	return res
}

func resolvePermittedRoute(candidate string, permCheck PermCheckFunc, warnings *[]string) string {
	if opt, ok := LookupAdminRoute(candidate); ok {
		if permCheck == nil || opt.PermKey == "" || permCheck(opt.PermKey) {
			return opt.Path
		}
		*warnings = append(*warnings, fmt.Sprintf("preferred landing route %s is not permitted for your current role; using fallback", opt.Path))
	}
	for _, fallback := range []string{"/", "/calendar", "/maintenance", "/guests", "/account"} {
		if opt, ok := LookupAdminRoute(fallback); ok {
			if permCheck == nil || opt.PermKey == "" || permCheck(opt.PermKey) {
				return opt.Path
			}
		}
	}
	return "/account"
}

func resolvePermittedShortcuts(paths []string, permCheck PermCheckFunc, warnings *[]string) []ResolvedShortcut {
	var out []ResolvedShortcut
	seen := make(map[string]struct{})
	for _, p := range paths {
		if len(out) >= MaxPinnedShortcuts {
			break
		}
		opt, ok := LookupAdminRoute(p)
		if !ok {
			continue
		}
		if permCheck != nil && opt.PermKey != "" && !permCheck(opt.PermKey) {
			*warnings = append(*warnings, fmt.Sprintf("shortcut %s hidden due to missing permission", opt.Path))
			continue
		}
		if _, dup := seen[opt.Path]; dup {
			continue
		}
		seen[opt.Path] = struct{}{}
		out = append(out, ResolvedShortcut{Path: opt.Path, Label: opt.Label})
	}
	return out
}

func viewPermitted(screen string, permCheck PermCheckFunc) bool {
	if permCheck == nil {
		return true
	}
	switch screen {
	case SavedViewScreenCalendar, SavedViewScreenReservations:
		return permCheck("reservations:read")
	case SavedViewScreenGuests:
		return permCheck("guests:read")
	default:
		return false
	}
}

func sanitizeRouteSlice(list []string, limit int) ([]string, int) {
	var out []string
	seen := make(map[string]struct{})
	skipped := 0
	for _, raw := range list {
		if opt, ok := LookupAdminRoute(raw); ok {
			if _, dup := seen[opt.Path]; !dup {
				if len(out) < limit {
					seen[opt.Path] = struct{}{}
					out = append(out, opt.Path)
				}
			}
		} else if strings.TrimSpace(raw) != "" {
			skipped++
		}
	}
	return out, skipped
}

func sanitizeSectionSlice(list []string) ([]string, int) {
	var out []string
	seen := make(map[string]struct{})
	skipped := 0
	for _, raw := range list {
		sec := strings.ToLower(strings.TrimSpace(raw))
		if IsAllowedNavSection(sec) {
			if _, dup := seen[sec]; !dup {
				seen[sec] = struct{}{}
				out = append(out, sec)
			}
		} else if sec != "" {
			skipped++
		}
	}
	return out, skipped
}

func validateCalendarSavedFilters(in map[string]string, catalog PropertyResourceCatalog, out map[string]string, errs map[string]string) {
	for k, rawVal := range in {
		v := strings.TrimSpace(rawVal)
		if v == "" {
			continue
		}
		switch k {
		case "horizon":
			if norm, ok := IsAllowedHorizon(v, false); ok {
				out[k] = norm
			} else {
				errs["filters.horizon"] = "invalid calendar horizon"
			}
		case "grouping":
			if norm, ok := IsAllowedGrouping(v, false); ok {
				out[k] = norm
			} else {
				errs["filters.grouping"] = "invalid calendar grouping"
			}
		case "unit_type_id":
			uid, err := uuid.Parse(v)
			if err != nil {
				errs["filters.unit_type_id"] = "invalid unit type id"
				continue
			}
			if _, exists := catalog.UnitTypeIDs[uid]; !exists {
				errs["filters.unit_type_id"] = "unit type does not belong to active property"
				continue
			}
			out[k] = uid.String()
		case "channel_code":
			code := strings.ToUpper(v)
			if _, exists := catalog.ChannelCodes[code]; !exists {
				errs["filters.channel_code"] = "channel does not belong to active property"
				continue
			}
			out[k] = code
		case "status":
			st := strings.ToUpper(v)
			if slices.Contains([]string{"AVAILABLE", "BOOKED", "PENDING", "ALLOCATED", "BLOCKED", "UNRELEASED"}, st) {
				out[k] = st
			} else {
				errs["filters.status"] = "invalid calendar highlight status"
			}
		default:
			errs["filters"] = "contains unsupported calendar filter key"
		}
	}
}

func validateReservationsSavedFilters(in map[string]string, catalog PropertyResourceCatalog, out map[string]string, errs map[string]string) {
	for k, rawVal := range in {
		v := strings.TrimSpace(rawVal)
		if v == "" {
			continue
		}
		switch k {
		case "status":
			st := strings.ToUpper(v)
			if slices.Contains([]string{
				string(ResStatusInquiry),
				string(ResStatusPending),
				string(ResStatusConfirmed),
				string(ResStatusCheckedIn),
				string(ResStatusCheckedOut),
				string(ResStatusCancelled),
				string(ResStatusNoShow),
			}, st) {
				out[k] = st
			} else {
				errs["filters.status"] = "invalid reservation status"
			}
		case "channel_code":
			code := strings.ToUpper(v)
			if _, exists := catalog.ChannelCodes[code]; !exists {
				errs["filters.channel_code"] = "channel does not belong to active property"
				continue
			}
			out[k] = code
		case "sort":
			if norm, ok := IsAllowedReservationsSort(v, false); ok {
				out[k] = norm
			} else {
				errs["filters.sort"] = "invalid sort option"
			}
		case "page_size":
			if ps, err := strconv.Atoi(v); err == nil && IsAllowedPageSize(ps, false) {
				out[k] = strconv.Itoa(ps)
			} else {
				errs["filters.page_size"] = "invalid page size"
			}
		default:
			errs["filters"] = "contains unsupported reservation filter key"
		}
	}
}

func validateGuestsSavedFilters(in map[string]string, out map[string]string, errs map[string]string) {
	for k, rawVal := range in {
		v := strings.TrimSpace(rawVal)
		if v == "" {
			continue
		}
		switch k {
		case "segment":
			seg := strings.ToUpper(v)
			if slices.Contains([]string{
				"ALL", "RETURNING", "NEW", "UPCOMING",
				"OUTSTANDING_REQUESTS", "UNRESOLVED_COMPLAINTS", "OPTED_IN",
			}, seg) {
				out[k] = seg
			} else {
				errs["filters.segment"] = "invalid guest segment"
			}
		case "include_all":
			if v == "1" || strings.EqualFold(v, "true") {
				out[k] = "1"
			} else if v == "0" || strings.EqualFold(v, "false") {
				out[k] = "0"
			} else {
				errs["filters.include_all"] = "must be 1 or 0"
			}
		case "sort":
			if norm, ok := IsAllowedGuestsSort(v, false); ok {
				out[k] = norm
			} else {
				errs["filters.sort"] = "invalid guest sort option"
			}
		case "page_size":
			if ps, err := strconv.Atoi(v); err == nil && IsAllowedPageSize(ps, false) {
				out[k] = strconv.Itoa(ps)
			} else {
				errs["filters.page_size"] = "invalid page size"
			}
		default:
			errs["filters"] = "contains unsupported guest filter key"
		}
	}
}

func sanitizeSavedViewOnRead(candidate SavedOperationalView, catalog PropertyResourceCatalog) (SavedOperationalView, []string, bool) {
	var warnings []string
	screen := strings.ToLower(strings.TrimSpace(candidate.Screen))
	if screen != SavedViewScreenCalendar && screen != SavedViewScreenReservations && screen != SavedViewScreenGuests {
		return SavedOperationalView{}, []string{"ignored saved view with unsupported screen"}, false
	}
	name := cleanPlainTextLabel(candidate.Name)
	if name == "" {
		return SavedOperationalView{}, []string{"ignored saved view with empty name"}, false
	}
	if len([]rune(name)) > MaxSavedViewNameLength {
		name = string([]rune(name)[:MaxSavedViewNameLength])
	}
	id := strings.TrimSpace(candidate.ID)
	if id == "" {
		id = name
	}

	cleanFilters := make(map[string]string)
	for k, rawVal := range candidate.Filters {
		v := strings.TrimSpace(rawVal)
		if v == "" {
			continue
		}
		tmpErrs := make(map[string]string)
		tmpIn := map[string]string{k: v}
		switch screen {
		case SavedViewScreenCalendar:
			validateCalendarSavedFilters(tmpIn, catalog, cleanFilters, tmpErrs)
		case SavedViewScreenReservations:
			validateReservationsSavedFilters(tmpIn, catalog, cleanFilters, tmpErrs)
		case SavedViewScreenGuests:
			validateGuestsSavedFilters(tmpIn, cleanFilters, tmpErrs)
		}
		if len(tmpErrs) > 0 {
			warnings = append(warnings, fmt.Sprintf("removed stale or invalid filter %q from saved view %q", k, name))
		}
	}

	return SavedOperationalView{
		ID:      id,
		Name:    name,
		Screen:  screen,
		Filters: cleanFilters,
	}, warnings, true
}

func cleanPlainTextLabel(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
