package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PWE-3 Dashboard Builder constants and boundaries.
const (
	CurrentDashboardLayoutSchemaVersion = 1
	MaxDashboardWidgets                 = 16

	WidgetSizeCompact  = "compact"
	WidgetSizeStandard = "standard"
	WidgetSizeWide     = "wide"

	DashboardSlotAuto = "auto"
	DashboardSlotMain = "main"
	DashboardSlotSide = "side"
)

// Canonical PWE-3 Widget Identifiers.
const (
	WidgetOpsArrivalsSummary      = "ops.arrivals_summary"
	WidgetOpsChangeoverBoard      = "ops.changeover_board"
	WidgetOpsAlertsAttention      = "ops.alerts_attention"
	WidgetMaintOpenQueue          = "maint.open_queue"
	WidgetMaintPreventativeDue    = "maint.preventative_due"
	WidgetResPendingInquiries     = "res.pending_inquiries"
	WidgetResMiniCalendar         = "res.mini_calendar"
	WidgetCRMOpenEnquiries        = "crm.open_enquiries"
	WidgetChannelsSyncHealth      = "channels.sync_health"
	WidgetReportsKPISnapshot      = "reports.kpi_snapshot"
	WidgetWorkspaceQuickShortcuts = "workspace.quick_shortcuts"
)

// Allowlisted ops.alerts_attention alert categories.
const (
	AlertCatPayments     = "payments"
	AlertCatChannels     = "channels"
	AlertCatComms        = "comms"
	AlertCatMaintenance  = "maintenance"
	AlertCatHousekeeping = "housekeeping"
)

var instanceIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

var allAlertCategories = []string{
	AlertCatPayments,
	AlertCatChannels,
	AlertCatComms,
	AlertCatMaintenance,
	AlertCatHousekeeping,
}

var allChangeoverStageFilters = []string{
	"ALL",
	string(StageGuestDeparting),
	string(StageCleaningRequired),
	string(StageCleaningInProgress),
	string(StageInspectionRequired),
	string(StageReady),
}

// WidgetRegistryEntry describes one declarative PWE-3 dashboard widget.
type WidgetRegistryEntry struct {
	ID                  string   `json:"id"`
	DisplayName         string   `json:"display_name"`
	Description         string   `json:"description"`
	Category            string   `json:"category"`
	SupportedSizes      []string `json:"supported_sizes"`
	DefaultSize         string   `json:"default_size"`
	RequiredPerms       []string `json:"required_perms"`
	RequiresProperty    bool     `json:"requires_property"`
	ConfigSchemaVersion int      `json:"config_schema_version"`
	DefaultPresentation string   `json:"default_presentation"`
}

// SupportsSize reports whether size is supported by this widget registry entry.
func (e WidgetRegistryEntry) SupportsSize(size string) bool {
	return slices.Contains(e.SupportedSizes, size)
}

var widgetRegistry = []WidgetRegistryEntry{
	{
		ID:                  WidgetOpsArrivalsSummary,
		DisplayName:         "Arrivals & Departures Today",
		Description:         "Confirmed arrivals, pending holds, due-outs",
		Category:            "Operations",
		SupportedSizes:      []string{WidgetSizeStandard, WidgetSizeWide},
		DefaultSize:         WidgetSizeStandard,
		RequiredPerms:       []string{"ops:read", "reservations:read"},
		RequiresProperty:    true,
		ConfigSchemaVersion: 1,
		DefaultPresentation: "Table strip grouped by Arrival / Departing with guest status links",
	},
	{
		ID:                  WidgetOpsChangeoverBoard,
		DisplayName:         "Daily Changeover Queue",
		Description:         "5-stage housekeeping readiness & turnaround queue",
		Category:            "Operations",
		SupportedSizes:      []string{WidgetSizeStandard, WidgetSizeWide},
		DefaultSize:         WidgetSizeStandard,
		RequiredPerms:       []string{"ops:read"},
		RequiresProperty:    true,
		ConfigSchemaVersion: 1,
		DefaultPresentation: "Dense rows ordered CLEANING_REQUIRED -> INSPECTION_REQUIRED -> READY",
	},
	{
		ID:                  WidgetOpsAlertsAttention,
		DisplayName:         "Operational Alerts",
		Description:         "Open payment exceptions, failed OTA sync/conflicts, stale outbox",
		Category:            "Operations",
		SupportedSizes:      []string{WidgetSizeCompact, WidgetSizeStandard},
		DefaultSize:         WidgetSizeStandard,
		RequiredPerms:       []string{"ops:read"},
		RequiresProperty:    true,
		ConfigSchemaVersion: 1,
		DefaultPresentation: "Prioritised action list; alerts lacking user permission omitted silently",
	},
	{
		ID:                  WidgetMaintOpenQueue,
		DisplayName:         "Open Maintenance Queue",
		Description:         "Urgent/high defects & pending inspections",
		Category:            "Maintenance",
		SupportedSizes:      []string{WidgetSizeStandard, WidgetSizeWide},
		DefaultSize:         WidgetSizeStandard,
		RequiredPerms:       []string{"maintenance:read"},
		RequiresProperty:    true,
		ConfigSchemaVersion: 1,
		DefaultPresentation: "Tabular queue with MNT-* ID, asset/unit, priority text+badge, due date",
	},
	{
		ID:                  WidgetMaintPreventativeDue,
		DisplayName:         "Due Preventative Schedules",
		Description:         "Upcoming recurring maintenance tasks",
		Category:            "Maintenance",
		SupportedSizes:      []string{WidgetSizeCompact, WidgetSizeStandard},
		DefaultSize:         WidgetSizeCompact,
		RequiredPerms:       []string{"maintenance:read"},
		RequiresProperty:    true,
		ConfigSchemaVersion: 1,
		DefaultPresentation: "Chronological compact checklist",
	},
	{
		ID:                  WidgetResPendingInquiries,
		DisplayName:         "Provisional Holds & Inquiries",
		Description:         "Unconfirmed bookings needing follow-up",
		Category:            "Reservations",
		SupportedSizes:      []string{WidgetSizeCompact, WidgetSizeStandard},
		DefaultSize:         WidgetSizeCompact,
		RequiredPerms:       []string{"reservations:read"},
		RequiresProperty:    true,
		ConfigSchemaVersion: 1,
		DefaultPresentation: "List with expiry/age indicator and reservation link",
	},
	{
		ID:                  WidgetResMiniCalendar,
		DisplayName:         "Availability Horizon",
		Description:         "Compact 7- or 14-day unit availability overview",
		Category:            "Reservations",
		SupportedSizes:      []string{WidgetSizeWide},
		DefaultSize:         WidgetSizeWide,
		RequiredPerms:       []string{"inventory:read", "reservations:read", "blocks:read"},
		RequiresProperty:    true,
		ConfigSchemaVersion: 1,
		DefaultPresentation: "Horizontal compact calendar linking straight to /calendar",
	},
	{
		ID:                  WidgetCRMOpenEnquiries,
		DisplayName:         "Guest Enquiries & Complaints",
		Description:         "Open ENQ-* SLA queue",
		Category:            "Guests & CRM",
		SupportedSizes:      []string{WidgetSizeStandard, WidgetSizeWide},
		DefaultSize:         WidgetSizeStandard,
		RequiredPerms:       []string{"guests:read", "comms:read"},
		RequiresProperty:    true,
		ConfigSchemaVersion: 1,
		DefaultPresentation: "Queue without exposing restricted guest_notes or INTERNAL messages",
	},
	{
		ID:                  WidgetChannelsSyncHealth,
		DisplayName:         "Channel Sync Health",
		Description:         "Provider status, failures, unmapped/overbooked counts",
		Category:            "Channels",
		SupportedSizes:      []string{WidgetSizeCompact, WidgetSizeStandard},
		DefaultSize:         WidgetSizeCompact,
		RequiredPerms:       []string{"channels:read"},
		RequiresProperty:    true,
		ConfigSchemaVersion: 1,
		DefaultPresentation: "Provider status table (Beds24, Smoobu, Lodgify, Channex)",
	},
	{
		ID:                  WidgetReportsKPISnapshot,
		DisplayName:         "7/30-Day Performance Snapshot",
		Description:         "Occupancy, ADR, RevPAR (per currency)",
		Category:            "Reports & BI",
		SupportedSizes:      []string{WidgetSizeCompact, WidgetSizeStandard},
		DefaultSize:         WidgetSizeStandard,
		RequiredPerms:       []string{"reports:read"},
		RequiresProperty:    true,
		ConfigSchemaVersion: 1,
		DefaultPresentation: "Tabular KPI summary noting incomplete history if applicable",
	},
	{
		ID:                  WidgetWorkspaceQuickShortcuts,
		DisplayName:         "Personal Quick Links",
		Description:         "Pinned routes & saved operational views",
		Category:            "Workspace",
		SupportedSizes:      []string{WidgetSizeCompact},
		DefaultSize:         WidgetSizeCompact,
		RequiredPerms:       nil,
		RequiresProperty:    true,
		ConfigSchemaVersion: 1,
		DefaultPresentation: "Keyboard-accessible concise link list",
	},
}

// DashboardWidgetRegistry returns a defensive copy of all registered canonical PWE-3 widgets.
func DashboardWidgetRegistry() []WidgetRegistryEntry {
	out := make([]WidgetRegistryEntry, len(widgetRegistry))
	for i, e := range widgetRegistry {
		e.SupportedSizes = slices.Clone(e.SupportedSizes)
		e.RequiredPerms = slices.Clone(e.RequiredPerms)
		out[i] = e
	}
	return out
}

// LookupDashboardWidget returns a registered widget definition by exact stable ID.
func LookupDashboardWidget(id string) (WidgetRegistryEntry, bool) {
	clean := strings.TrimSpace(id)
	for _, e := range widgetRegistry {
		if e.ID == clean {
			e.SupportedSizes = slices.Clone(e.SupportedSizes)
			e.RequiredPerms = slices.Clone(e.RequiredPerms)
			return e, true
		}
	}
	return WidgetRegistryEntry{}, false
}

// WidgetPermitted reports whether permCheck satisfies every required permission of entry.
func WidgetPermitted(entry WidgetRegistryEntry, permCheck PermCheckFunc) bool {
	if len(entry.RequiredPerms) == 0 {
		return true
	}
	if permCheck == nil {
		return false
	}
	for _, p := range entry.RequiredPerms {
		if !permCheck(p) {
			return false
		}
	}
	return true
}

// EligibleDashboardWidgets returns registry entries accessible to the current permission set.
func EligibleDashboardWidgets(permCheck PermCheckFunc) []WidgetRegistryEntry {
	var out []WidgetRegistryEntry
	for _, e := range DashboardWidgetRegistry() {
		if WidgetPermitted(e, permCheck) {
			out = append(out, e)
		}
	}
	return out
}

// Typed per-widget schema_version=1 configuration structs.

type OpsArrivalsWidgetConfig struct {
	ShowPendingHolds bool `json:"show_pending_holds"`
	ShowInHouseCount bool `json:"show_in_house_count"`
	Limit            int  `json:"limit"`
}

type OpsChangeoverWidgetConfig struct {
	StageFilter []string `json:"stage_filter"`
	Limit       int      `json:"limit"`
}

type OpsAlertsWidgetConfig struct {
	IncludeCategories []string `json:"include_categories"`
}

type MaintOpenQueueWidgetConfig struct {
	PriorityMin  string `json:"priority_min"`
	AssignedToMe bool   `json:"assigned_to_me"`
	Limit        int    `json:"limit"`
}

type MaintPreventativeWidgetConfig struct {
	HorizonDays int `json:"horizon_days"`
	Limit       int `json:"limit"`
}

type ResPendingInquiriesWidgetConfig struct {
	IncludeInquiries bool `json:"include_inquiries"`
	Limit            int  `json:"limit"`
}

type ResMiniCalendarWidgetConfig struct {
	Days       int     `json:"days"`
	UnitTypeID *string `json:"unit_type_id"`
}

type CRMOpenEnquiriesWidgetConfig struct {
	PriorityFilter string `json:"priority_filter"`
	OnlyComplaints bool   `json:"only_complaints"`
	Limit          int    `json:"limit"`
}

type ChannelsSyncHealthWidgetConfig struct {
	ShowOKProviders bool `json:"show_ok_providers"`
}

type ReportsKPISnapshotWidgetConfig struct {
	WindowDays      int  `json:"window_days"`
	ShowPriorPeriod bool `json:"show_prior_period"`
}

type WorkspaceQuickShortcutsWidgetConfig struct {
	MaxItems         int  `json:"max_items"`
	ShowSavedFilters bool `json:"show_saved_filters"`
}

// DefaultWidgetConfigMap returns the safe typed default config for widgetID.
func DefaultWidgetConfigMap(widgetID string) map[string]any {
	switch widgetID {
	case WidgetOpsArrivalsSummary:
		return map[string]any{
			"show_pending_holds":  true,
			"show_in_house_count": true,
			"limit":               10,
		}
	case WidgetOpsChangeoverBoard:
		return map[string]any{
			"stage_filter": []string{"ALL"},
			"limit":        12,
		}
	case WidgetOpsAlertsAttention:
		return map[string]any{
			"include_categories": slices.Clone(allAlertCategories),
		}
	case WidgetMaintOpenQueue:
		return map[string]any{
			"priority_min":   "low",
			"assigned_to_me": false,
			"limit":          10,
		}
	case WidgetMaintPreventativeDue:
		return map[string]any{
			"horizon_days": 14,
			"limit":        8,
		}
	case WidgetResPendingInquiries:
		return map[string]any{
			"include_inquiries": true,
			"limit":             10,
		}
	case WidgetResMiniCalendar:
		return map[string]any{
			"days":         7,
			"unit_type_id": nil,
		}
	case WidgetCRMOpenEnquiries:
		return map[string]any{
			"priority_filter": "ALL",
			"only_complaints": false,
			"limit":           10,
		}
	case WidgetChannelsSyncHealth:
		return map[string]any{
			"show_ok_providers": true,
		}
	case WidgetReportsKPISnapshot:
		return map[string]any{
			"window_days":       30,
			"show_prior_period": true,
		}
	case WidgetWorkspaceQuickShortcuts:
		return map[string]any{
			"max_items":          8,
			"show_saved_filters": true,
		}
	default:
		return map[string]any{}
	}
}

// DashboardWidgetInstance persists a single configured widget in an operator's workspace.
// Zero output data snapshots or executable strings are persisted.
type DashboardWidgetInstance struct {
	InstanceID string         `json:"instance_id"`
	WidgetID   string         `json:"widget_id"`
	Size       string         `json:"size"`
	Slot       string         `json:"slot,omitempty"`
	Position   int            `json:"position"`
	Config     map[string]any `json:"config"`
}

// DashboardLayout represents a schema-versioned ordered dashboard configuration.
type DashboardLayout struct {
	SchemaVersion int                       `json:"schema_version"`
	Widgets       []DashboardWidgetInstance `json:"widgets"`
}

// ResolvedDashboardLayout holds the decoded, revalidated, and permission-filtered layout.
type ResolvedDashboardLayout struct {
	SchemaVersion int                       `json:"schema_version"`
	IsDefault     bool                      `json:"is_default"`
	SourceLayer   string                    `json:"source_layer"` // "platform" | "profile" | "user_property"
	ProfileCode   string                    `json:"profile_code,omitempty"`
	ProfileName   string                    `json:"profile_name,omitempty"`
	StoredWidgets []DashboardWidgetInstance `json:"-"`       // valid persisted items prior to runtime perm filtering
	Widgets       []DashboardWidgetInstance `json:"widgets"` // permission-filtered visible items
	Warnings      []string                  `json:"warnings,omitempty"`
	UpdatedAt     *time.Time                `json:"updated_at,omitempty"`
}

// DefaultDashboardLayout returns LOVELACE's out-of-the-box Layer 1 Platform dashboard layout.
func DefaultDashboardLayout() DashboardLayout {
	defs := []struct {
		instanceID string
		widgetID   string
		size       string
		slot       string
	}{
		{"default-ops-arrivals", WidgetOpsArrivalsSummary, WidgetSizeStandard, DashboardSlotAuto},
		{"default-ops-alerts", WidgetOpsAlertsAttention, WidgetSizeStandard, DashboardSlotAuto},
		{"default-ops-changeover", WidgetOpsChangeoverBoard, WidgetSizeStandard, DashboardSlotAuto},
		{"default-maint-open", WidgetMaintOpenQueue, WidgetSizeStandard, DashboardSlotAuto},
		{"default-crm-enquiries", WidgetCRMOpenEnquiries, WidgetSizeStandard, DashboardSlotAuto},
		{"default-reports-kpi", WidgetReportsKPISnapshot, WidgetSizeStandard, DashboardSlotAuto},
		{"default-res-calendar", WidgetResMiniCalendar, WidgetSizeWide, DashboardSlotMain},
		{"default-res-pending", WidgetResPendingInquiries, WidgetSizeCompact, DashboardSlotSide},
		{"default-channels-health", WidgetChannelsSyncHealth, WidgetSizeCompact, DashboardSlotSide},
		{"default-maint-preventative", WidgetMaintPreventativeDue, WidgetSizeCompact, DashboardSlotSide},
		{"default-workspace-links", WidgetWorkspaceQuickShortcuts, WidgetSizeCompact, DashboardSlotSide},
	}
	widgets := make([]DashboardWidgetInstance, 0, len(defs))
	for i, d := range defs {
		widgets = append(widgets, DashboardWidgetInstance{
			InstanceID: d.instanceID,
			WidgetID:   d.widgetID,
			Size:       d.size,
			Slot:       d.slot,
			Position:   i + 1,
			Config:     DefaultWidgetConfigMap(d.widgetID),
		})
	}
	return DashboardLayout{
		SchemaVersion: CurrentDashboardLayoutSchemaVersion,
		Widgets:       widgets,
	}
}

// IsAllowedDashboardSlot reports whether slot is an allowlisted slot token.
func IsAllowedDashboardSlot(slot string) (string, bool) {
	clean := strings.ToLower(strings.TrimSpace(slot))
	switch clean {
	case "", DashboardSlotAuto:
		return DashboardSlotAuto, true
	case DashboardSlotMain, DashboardSlotSide:
		return clean, true
	default:
		return "", false
	}
}

// ValidateWidgetInstanceInput validates one widget instance strictly for save/edit paths.
func ValidateWidgetInstanceInput(inst DashboardWidgetInstance, catalog PropertyResourceCatalog) (DashboardWidgetInstance, error) {
	errs := make(map[string]string)

	instID := strings.TrimSpace(inst.InstanceID)
	if !instanceIDPattern.MatchString(instID) {
		errs["instance_id"] = "must be 1-64 alphanumeric, underscore, or hyphen characters"
	}

	entry, ok := LookupDashboardWidget(inst.WidgetID)
	if !ok {
		errs["widget_id"] = "unknown or unsupported dashboard widget identifier"
		return DashboardWidgetInstance{}, NewValidationError(errs)
	}

	size := strings.ToLower(strings.TrimSpace(inst.Size))
	if size == "" {
		size = entry.DefaultSize
	} else if !entry.SupportsSize(size) {
		errs["size"] = fmt.Sprintf("size %q is not supported by %s (%s)", inst.Size, entry.ID, strings.Join(entry.SupportedSizes, ", "))
	}

	slot, ok := IsAllowedDashboardSlot(inst.Slot)
	if !ok {
		errs["slot"] = "must be auto, main, or side"
	}

	if inst.Position < 1 || inst.Position > MaxDashboardWidgets {
		errs["position"] = fmt.Sprintf("must be between 1 and %d", MaxDashboardWidgets)
	}

	cleanCfg, cfgErr := validateWidgetConfigStrict(entry.ID, inst.Config, catalog)
	if cfgErr != nil {
		for k, v := range cfgErr {
			errs[k] = v
		}
	}

	if len(errs) > 0 {
		return DashboardWidgetInstance{}, NewValidationError(errs)
	}

	return DashboardWidgetInstance{
		InstanceID: instID,
		WidgetID:   entry.ID,
		Size:       size,
		Slot:       slot,
		Position:   inst.Position,
		Config:     cleanCfg,
	}, nil
}

// ValidateDashboardLayoutInput validates a full dashboard layout save against registry bounds,
// ordering/slot rules, unique instance IDs, and active property resource ownership.
func ValidateDashboardLayoutInput(in DashboardLayout, catalog PropertyResourceCatalog) (DashboardLayout, error) {
	errs := make(map[string]string)

	ver := in.SchemaVersion
	if ver == 0 {
		ver = CurrentDashboardLayoutSchemaVersion
	} else if ver != CurrentDashboardLayoutSchemaVersion {
		errs["schema_version"] = fmt.Sprintf("unsupported dashboard schema_version %d (expected %d)", in.SchemaVersion, CurrentDashboardLayoutSchemaVersion)
	}

	if len(in.Widgets) > MaxDashboardWidgets {
		errs["widgets"] = fmt.Sprintf("at most %d widgets are permitted per workspace layout", MaxDashboardWidgets)
		return DashboardLayout{}, NewValidationError(errs)
	}

	seenInstances := make(map[string]struct{}, len(in.Widgets))
	seenPositions := make(map[int]struct{}, len(in.Widgets))
	cleaned := make([]DashboardWidgetInstance, 0, len(in.Widgets))

	for i, rawInst := range in.Widgets {
		normInst, err := ValidateWidgetInstanceInput(rawInst, catalog)
		if err != nil {
			if vErr, isVal := err.(*ValidationError); isVal {
				for k, v := range vErr.Fields {
					errs[fmt.Sprintf("widgets[%d].%s", i, k)] = v
				}
			} else {
				errs[fmt.Sprintf("widgets[%d]", i)] = err.Error()
			}
			continue
		}
		if _, dup := seenInstances[normInst.InstanceID]; dup {
			errs[fmt.Sprintf("widgets[%d].instance_id", i)] = "duplicate widget instance_id in layout"
			continue
		}
		seenInstances[normInst.InstanceID] = struct{}{}

		if _, dupPos := seenPositions[normInst.Position]; dupPos {
			errs[fmt.Sprintf("widgets[%d].position", i)] = fmt.Sprintf("duplicate ordering position %d", normInst.Position)
			continue
		}
		seenPositions[normInst.Position] = struct{}{}

		cleaned = append(cleaned, normInst)
	}

	if len(errs) > 0 {
		return DashboardLayout{}, NewValidationError(errs)
	}

	sort.SliceStable(cleaned, func(i, j int) bool {
		return cleaned[i].Position < cleaned[j].Position
	})
	for i := range cleaned {
		cleaned[i].Position = i + 1
	}

	return DashboardLayout{
		SchemaVersion: ver,
		Widgets:       cleaned,
	}, nil
}

// DecodeAndNormalizeDashboardLayout safely resolves Layer 1 -> (future Layer 2 Property -> future Layer 3 Profile)
// -> Layer 4 User+Property layout, dropping corrupt/unknown widgets individually and filtering by live permissions.
func DecodeAndNormalizeDashboardLayout(
	schemaVer int,
	rawJSON []byte,
	updatedAt *time.Time,
	catalog PropertyResourceCatalog,
	permCheck PermCheckFunc,
	futurePropertyDefault *DashboardLayout, // Extension hook for PWE-4
	futureProfileDefault *DashboardLayout, // Extension hook for PWE-5
) ResolvedDashboardLayout {
	baseLayout := DefaultDashboardLayout()
	defaultSourceLayer := "platform"
	if futurePropertyDefault != nil {
		baseLayout = *futurePropertyDefault
		defaultSourceLayer = "property"
	}
	if futureProfileDefault != nil {
		baseLayout = *futureProfileDefault
		defaultSourceLayer = "profile"
	}

	trimmed := bytesTrimSpace(rawJSON)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		visible := filterWidgetsByPermission(baseLayout.Widgets, permCheck)
		return ResolvedDashboardLayout{
			SchemaVersion: CurrentDashboardLayoutSchemaVersion,
			IsDefault:     true,
			SourceLayer:   defaultSourceLayer,
			StoredWidgets: baseLayout.Widgets,
			Widgets:       visible,
		}
	}

	var warnings []string
	if schemaVer < 1 || schemaVer > CurrentDashboardLayoutSchemaVersion {
		warnings = append(warnings, fmt.Sprintf("dashboard_schema_version %d upcasted to v%d", schemaVer, CurrentDashboardLayoutSchemaVersion))
	}

	var doc struct {
		SchemaVersion *int              `json:"schema_version"`
		Widgets       []json.RawMessage `json:"widgets"`
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		warnings = append(warnings, "corrupt saved dashboard layout recovered with defaults")
		return ResolvedDashboardLayout{
			SchemaVersion: CurrentDashboardLayoutSchemaVersion,
			IsDefault:     true,
			SourceLayer:   defaultSourceLayer,
			StoredWidgets: baseLayout.Widgets,
			Widgets:       filterWidgetsByPermission(baseLayout.Widgets, permCheck),
			Warnings:      warnings,
		}
	}
	if doc.SchemaVersion != nil && (*doc.SchemaVersion < 1 || *doc.SchemaVersion > CurrentDashboardLayoutSchemaVersion) {
		warnings = append(warnings, fmt.Sprintf("saved layout schema_version %d normalized to v%d", *doc.SchemaVersion, CurrentDashboardLayoutSchemaVersion))
	}

	var stored []DashboardWidgetInstance
	seenInstances := make(map[string]struct{})
	for i, rawItem := range doc.Widgets {
		if len(stored) >= MaxDashboardWidgets {
			warnings = append(warnings, fmt.Sprintf("truncated saved widgets beyond %d item limit", MaxDashboardWidgets))
			break
		}
		var item struct {
			InstanceID string          `json:"instance_id"`
			WidgetID   string          `json:"widget_id"`
			Size       string          `json:"size"`
			Slot       string          `json:"slot"`
			Position   int             `json:"position"`
			Config     json.RawMessage `json:"config"`
		}
		itemDec := json.NewDecoder(bytes.NewReader(rawItem))
		itemDec.DisallowUnknownFields()
		if err := itemDec.Decode(&item); err != nil {
			warnings = append(warnings, fmt.Sprintf("dropped malformed widget entry #%d", i+1))
			continue
		}

		entry, ok := LookupDashboardWidget(item.WidgetID)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("dropped obsolete or unknown widget %q", cleanDiagnosticToken(item.WidgetID)))
			continue
		}

		instID := strings.TrimSpace(item.InstanceID)
		if !instanceIDPattern.MatchString(instID) {
			instID = fmt.Sprintf("recovered-%d", i+1)
			warnings = append(warnings, fmt.Sprintf("repaired invalid instance_id on widget %s", entry.ID))
		}
		if _, dup := seenInstances[instID]; dup {
			warnings = append(warnings, fmt.Sprintf("dropped duplicate widget instance %q", instID))
			continue
		}
		seenInstances[instID] = struct{}{}

		// Deterministic upcast + recovery on size
		size := strings.ToLower(strings.TrimSpace(item.Size))
		if size == "normal" || size == "medium" {
			size = WidgetSizeStandard
			warnings = append(warnings, fmt.Sprintf("upcasted legacy size on %s to standard", entry.ID))
		}
		if !entry.SupportsSize(size) {
			if size != "" {
				warnings = append(warnings, fmt.Sprintf("reset unsupported size on %s to %s", entry.ID, entry.DefaultSize))
			}
			size = entry.DefaultSize
		}

		slot, ok := IsAllowedDashboardSlot(item.Slot)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("reset invalid slot on %s to auto", entry.ID))
			slot = DashboardSlotAuto
		}

		pos := item.Position
		if pos < 1 || pos > MaxDashboardWidgets {
			pos = len(stored) + 1
		}

		cleanCfg, cfgWarnings := normalizeWidgetConfigOnRead(entry.ID, item.Config, catalog)
		warnings = append(warnings, cfgWarnings...)

		stored = append(stored, DashboardWidgetInstance{
			InstanceID: instID,
			WidgetID:   entry.ID,
			Size:       size,
			Slot:       slot,
			Position:   pos,
			Config:     cleanCfg,
		})
	}

	sort.SliceStable(stored, func(i, j int) bool {
		return stored[i].Position < stored[j].Position
	})
	for i := range stored {
		stored[i].Position = i + 1
	}

	return ResolvedDashboardLayout{
		SchemaVersion: CurrentDashboardLayoutSchemaVersion,
		IsDefault:     false,
		SourceLayer:   "user_property",
		StoredWidgets: stored,
		Widgets:       filterWidgetsByPermission(stored, permCheck),
		Warnings:      warnings,
		UpdatedAt:     updatedAt,
	}
}

func filterWidgetsByPermission(items []DashboardWidgetInstance, permCheck PermCheckFunc) []DashboardWidgetInstance {
	out := make([]DashboardWidgetInstance, 0, len(items))
	for _, w := range items {
		entry, ok := LookupDashboardWidget(w.WidgetID)
		if !ok || !WidgetPermitted(entry, permCheck) {
			continue
		}
		wCopy := w
		wCopy.Position = len(out) + 1
		out = append(out, wCopy)
	}
	return out
}

func cleanDiagnosticToken(raw string) string {
	cleaned := cleanPlainTextLabel(raw)
	if len([]rune(cleaned)) > 40 {
		runes := []rune(cleaned)
		return string(runes[:40])
	}
	if cleaned == "" {
		return "(empty)"
	}
	return cleaned
}

func decodeConfigStrictJSON[T any](cfgMap map[string]any, dst *T) error {
	if cfgMap == nil {
		cfgMap = map[string]any{}
	}
	raw, err := json.Marshal(cfgMap)
	if err != nil {
		return fmt.Errorf("config must be a valid JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("contains unknown key or invalid scalar type")
	}
	return nil
}

func validateWidgetConfigStrict(widgetID string, cfgMap map[string]any, catalog PropertyResourceCatalog) (map[string]any, map[string]string) {
	errs := make(map[string]string)
	if cfgMap == nil {
		return DefaultWidgetConfigMap(widgetID), nil
	}

	switch widgetID {
	case WidgetOpsArrivalsSummary:
		var raw struct {
			ShowPendingHolds *bool `json:"show_pending_holds"`
			ShowInHouseCount *bool `json:"show_in_house_count"`
			Limit            *int  `json:"limit"`
		}
		if err := decodeConfigStrictJSON(cfgMap, &raw); err != nil {
			errs["config"] = err.Error()
			return nil, errs
		}
		out := DefaultWidgetConfigMap(widgetID)
		if raw.ShowPendingHolds != nil {
			out["show_pending_holds"] = *raw.ShowPendingHolds
		}
		if raw.ShowInHouseCount != nil {
			out["show_in_house_count"] = *raw.ShowInHouseCount
		}
		if raw.Limit != nil {
			if *raw.Limit < 5 || *raw.Limit > 25 {
				errs["config.limit"] = "must be between 5 and 25"
			} else {
				out["limit"] = *raw.Limit
			}
		}
		if len(errs) > 0 {
			return nil, errs
		}
		return out, nil

	case WidgetOpsChangeoverBoard:
		var raw struct {
			StageFilter []string `json:"stage_filter"`
			Limit       *int     `json:"limit"`
		}
		if err := decodeConfigStrictJSON(cfgMap, &raw); err != nil {
			errs["config"] = err.Error()
			return nil, errs
		}
		out := DefaultWidgetConfigMap(widgetID)
		if raw.StageFilter != nil {
			if len(raw.StageFilter) == 0 || len(raw.StageFilter) > len(allChangeoverStageFilters) {
				errs["config.stage_filter"] = "select at least one valid changeover stage or ALL"
			} else {
				cleanStages := make([]string, 0, len(raw.StageFilter))
				seen := make(map[string]struct{})
				for _, s := range raw.StageFilter {
					norm := strings.ToUpper(strings.TrimSpace(s))
					if !slices.Contains(allChangeoverStageFilters, norm) {
						errs["config.stage_filter"] = fmt.Sprintf("unsupported stage %q", cleanDiagnosticToken(s))
						break
					}
					if _, dup := seen[norm]; !dup {
						seen[norm] = struct{}{}
						cleanStages = append(cleanStages, norm)
					}
				}
				if slices.Contains(cleanStages, "ALL") && len(cleanStages) > 1 {
					cleanStages = []string{"ALL"}
				}
				out["stage_filter"] = cleanStages
			}
		}
		if raw.Limit != nil {
			if *raw.Limit < 5 || *raw.Limit > 30 {
				errs["config.limit"] = "must be between 5 and 30"
			} else {
				out["limit"] = *raw.Limit
			}
		}
		if len(errs) > 0 {
			return nil, errs
		}
		return out, nil

	case WidgetOpsAlertsAttention:
		var raw struct {
			IncludeCategories []string `json:"include_categories"`
		}
		if err := decodeConfigStrictJSON(cfgMap, &raw); err != nil {
			errs["config"] = err.Error()
			return nil, errs
		}
		out := DefaultWidgetConfigMap(widgetID)
		if raw.IncludeCategories != nil {
			if len(raw.IncludeCategories) == 0 || len(raw.IncludeCategories) > len(allAlertCategories) {
				errs["config.include_categories"] = "select at least one valid alert category"
			} else {
				cleanCats := make([]string, 0, len(raw.IncludeCategories))
				seen := make(map[string]struct{})
				for _, c := range raw.IncludeCategories {
					norm := strings.ToLower(strings.TrimSpace(c))
					if !slices.Contains(allAlertCategories, norm) {
						errs["config.include_categories"] = fmt.Sprintf("unsupported alert category %q", cleanDiagnosticToken(c))
						break
					}
					if _, dup := seen[norm]; !dup {
						seen[norm] = struct{}{}
						cleanCats = append(cleanCats, norm)
					}
				}
				out["include_categories"] = cleanCats
			}
		}
		if len(errs) > 0 {
			return nil, errs
		}
		return out, nil

	case WidgetMaintOpenQueue:
		var raw struct {
			PriorityMin  *string `json:"priority_min"`
			AssignedToMe *bool   `json:"assigned_to_me"`
			Limit        *int    `json:"limit"`
		}
		if err := decodeConfigStrictJSON(cfgMap, &raw); err != nil {
			errs["config"] = err.Error()
			return nil, errs
		}
		out := DefaultWidgetConfigMap(widgetID)
		if raw.PriorityMin != nil {
			norm := strings.ToLower(strings.TrimSpace(*raw.PriorityMin))
			if !slices.Contains([]string{"low", "medium", "high", "urgent"}, norm) {
				errs["config.priority_min"] = "must be low, medium, high, or urgent"
			} else {
				out["priority_min"] = norm
			}
		}
		if raw.AssignedToMe != nil {
			out["assigned_to_me"] = *raw.AssignedToMe
		}
		if raw.Limit != nil {
			if *raw.Limit < 5 || *raw.Limit > 20 {
				errs["config.limit"] = "must be between 5 and 20"
			} else {
				out["limit"] = *raw.Limit
			}
		}
		if len(errs) > 0 {
			return nil, errs
		}
		return out, nil

	case WidgetMaintPreventativeDue:
		var raw struct {
			HorizonDays *int `json:"horizon_days"`
			Limit       *int `json:"limit"`
		}
		if err := decodeConfigStrictJSON(cfgMap, &raw); err != nil {
			errs["config"] = err.Error()
			return nil, errs
		}
		out := DefaultWidgetConfigMap(widgetID)
		if raw.HorizonDays != nil {
			if !slices.Contains([]int{7, 14, 30}, *raw.HorizonDays) {
				errs["config.horizon_days"] = "must be 7, 14, or 30"
			} else {
				out["horizon_days"] = *raw.HorizonDays
			}
		}
		if raw.Limit != nil {
			if *raw.Limit < 5 || *raw.Limit > 15 {
				errs["config.limit"] = "must be between 5 and 15"
			} else {
				out["limit"] = *raw.Limit
			}
		}
		if len(errs) > 0 {
			return nil, errs
		}
		return out, nil

	case WidgetResPendingInquiries:
		var raw struct {
			IncludeInquiries *bool `json:"include_inquiries"`
			Limit            *int  `json:"limit"`
		}
		if err := decodeConfigStrictJSON(cfgMap, &raw); err != nil {
			errs["config"] = err.Error()
			return nil, errs
		}
		out := DefaultWidgetConfigMap(widgetID)
		if raw.IncludeInquiries != nil {
			out["include_inquiries"] = *raw.IncludeInquiries
		}
		if raw.Limit != nil {
			if *raw.Limit < 5 || *raw.Limit > 20 {
				errs["config.limit"] = "must be between 5 and 20"
			} else {
				out["limit"] = *raw.Limit
			}
		}
		if len(errs) > 0 {
			return nil, errs
		}
		return out, nil

	case WidgetResMiniCalendar:
		var raw struct {
			Days       *int    `json:"days"`
			UnitTypeID *string `json:"unit_type_id"`
		}
		if err := decodeConfigStrictJSON(cfgMap, &raw); err != nil {
			errs["config"] = err.Error()
			return nil, errs
		}
		out := DefaultWidgetConfigMap(widgetID)
		if raw.Days != nil {
			if *raw.Days != 7 && *raw.Days != 14 {
				errs["config.days"] = "must be 7 or 14"
			} else {
				out["days"] = *raw.Days
			}
		}
		if raw.UnitTypeID != nil && strings.TrimSpace(*raw.UnitTypeID) != "" {
			uid, err := uuid.Parse(strings.TrimSpace(*raw.UnitTypeID))
			if err != nil {
				errs["config.unit_type_id"] = "invalid unit_type_id UUID"
			} else if _, exists := catalog.UnitTypeIDs[uid]; !exists {
				errs["config.unit_type_id"] = "unit type does not belong to active property"
			} else {
				out["unit_type_id"] = uid.String()
			}
		}
		if len(errs) > 0 {
			return nil, errs
		}
		return out, nil

	case WidgetCRMOpenEnquiries:
		var raw struct {
			PriorityFilter *string `json:"priority_filter"`
			OnlyComplaints *bool   `json:"only_complaints"`
			Limit          *int    `json:"limit"`
		}
		if err := decodeConfigStrictJSON(cfgMap, &raw); err != nil {
			errs["config"] = err.Error()
			return nil, errs
		}
		out := DefaultWidgetConfigMap(widgetID)
		if raw.PriorityFilter != nil {
			norm := strings.ToUpper(strings.TrimSpace(*raw.PriorityFilter))
			if !slices.Contains([]string{"ALL", "LOW", "MEDIUM", "HIGH", "URGENT"}, norm) {
				errs["config.priority_filter"] = "must be ALL, LOW, MEDIUM, HIGH, or URGENT"
			} else {
				out["priority_filter"] = norm
			}
		}
		if raw.OnlyComplaints != nil {
			out["only_complaints"] = *raw.OnlyComplaints
		}
		if raw.Limit != nil {
			if *raw.Limit < 5 || *raw.Limit > 20 {
				errs["config.limit"] = "must be between 5 and 20"
			} else {
				out["limit"] = *raw.Limit
			}
		}
		if len(errs) > 0 {
			return nil, errs
		}
		return out, nil

	case WidgetChannelsSyncHealth:
		var raw struct {
			ShowOKProviders *bool `json:"show_ok_providers"`
		}
		if err := decodeConfigStrictJSON(cfgMap, &raw); err != nil {
			errs["config"] = err.Error()
			return nil, errs
		}
		out := DefaultWidgetConfigMap(widgetID)
		if raw.ShowOKProviders != nil {
			out["show_ok_providers"] = *raw.ShowOKProviders
		}
		return out, nil

	case WidgetReportsKPISnapshot:
		var raw struct {
			WindowDays      *int  `json:"window_days"`
			ShowPriorPeriod *bool `json:"show_prior_period"`
		}
		if err := decodeConfigStrictJSON(cfgMap, &raw); err != nil {
			errs["config"] = err.Error()
			return nil, errs
		}
		out := DefaultWidgetConfigMap(widgetID)
		if raw.WindowDays != nil {
			if *raw.WindowDays != 7 && *raw.WindowDays != 30 {
				errs["config.window_days"] = "must be 7 or 30"
			} else {
				out["window_days"] = *raw.WindowDays
			}
		}
		if raw.ShowPriorPeriod != nil {
			out["show_prior_period"] = *raw.ShowPriorPeriod
		}
		if len(errs) > 0 {
			return nil, errs
		}
		return out, nil

	case WidgetWorkspaceQuickShortcuts:
		var raw struct {
			MaxItems         *int  `json:"max_items"`
			ShowSavedFilters *bool `json:"show_saved_filters"`
		}
		if err := decodeConfigStrictJSON(cfgMap, &raw); err != nil {
			errs["config"] = err.Error()
			return nil, errs
		}
		out := DefaultWidgetConfigMap(widgetID)
		if raw.MaxItems != nil {
			if *raw.MaxItems < 4 || *raw.MaxItems > 12 {
				errs["config.max_items"] = "must be between 4 and 12"
			} else {
				out["max_items"] = *raw.MaxItems
			}
		}
		if raw.ShowSavedFilters != nil {
			out["show_saved_filters"] = *raw.ShowSavedFilters
		}
		if len(errs) > 0 {
			return nil, errs
		}
		return out, nil

	default:
		errs["widget_id"] = "unsupported widget identifier"
		return nil, errs
	}
}

func normalizeWidgetConfigOnRead(widgetID string, rawJSON json.RawMessage, catalog PropertyResourceCatalog) (map[string]any, []string) {
	def := DefaultWidgetConfigMap(widgetID)
	trimmed := bytesTrimSpace(rawJSON)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return def, nil
	}

	var rawMap map[string]any
	if err := json.Unmarshal(trimmed, &rawMap); err != nil {
		return def, []string{fmt.Sprintf("reset malformed config on %s", widgetID)}
	}
	var warnings []string

	// Recover invalid fields individually rather than discarding valid sibling settings.
	if clean, errs := validateWidgetConfigStrict(widgetID, rawMap, catalog); len(errs) == 0 {
		return clean, nil
	}

	// Copy valid scalar keys per widget cleanly.
	out := mapsCloneShallow(def)
	for k, v := range rawMap {
		if _, known := def[k]; !known {
			warnings = append(warnings, fmt.Sprintf("removed unsupported config key %q on %s", cleanDiagnosticToken(k), widgetID))
			continue
		}
		single := map[string]any{k: v}
		if validPart, errs := validateWidgetConfigStrict(widgetID, single, catalog); len(errs) == 0 {
			out[k] = validPart[k]
		} else {
			warnings = append(warnings, fmt.Sprintf("reset invalid %s.%s to default", widgetID, k))
		}
	}
	return out, warnings
}

func mapsCloneShallow(src map[string]any) map[string]any {
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// Config extraction helpers used by widget services & SSR templates (avoid repeated type-assert boilerplate).

func ConfigBool(cfg map[string]any, key string, fallback bool) bool {
	if v, ok := cfg[key].(bool); ok {
		return v
	}
	return fallback
}

func ConfigInt(cfg map[string]any, key string, fallback int) int {
	switch v := cfg[key].(type) {
	case int:
		return v
	case float64:
		if float64(int(v)) == v {
			return int(v)
		}
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return parsed
		}
	}
	return fallback
}

func ConfigString(cfg map[string]any, key, fallback string) string {
	if v, ok := cfg[key].(string); ok {
		return v
	}
	return fallback
}

func ConfigStringSlice(cfg map[string]any, key string, fallback []string) []string {
	switch v := cfg[key].(type) {
	case []string:
		return slices.Clone(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, elem := range v {
			if s, ok := elem.(string); ok {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return slices.Clone(fallback)
}
