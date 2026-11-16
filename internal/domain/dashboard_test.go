package domain

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDashboardWidgetRegistry_CompletenessAndDefaults(t *testing.T) {
	reg := DashboardWidgetRegistry()
	expectedIDs := []string{
		WidgetOpsArrivalsSummary,
		WidgetOpsChangeoverBoard,
		WidgetOpsAlertsAttention,
		WidgetMaintOpenQueue,
		WidgetMaintPreventativeDue,
		WidgetResPendingInquiries,
		WidgetResMiniCalendar,
		WidgetCRMOpenEnquiries,
		WidgetChannelsSyncHealth,
		WidgetReportsKPISnapshot,
		WidgetWorkspaceQuickShortcuts,
	}
	if len(reg) != len(expectedIDs) {
		t.Fatalf("expected %d registered widgets, got %d", len(expectedIDs), len(reg))
	}

	seen := make(map[string]struct{}, len(reg))
	catalog := PropertyResourceCatalog{PropertyID: uuid.New()}
	for _, entry := range reg {
		if _, dup := seen[entry.ID]; dup {
			t.Fatalf("duplicate registry widget ID %q", entry.ID)
		}
		seen[entry.ID] = struct{}{}
		if !slices.Contains(expectedIDs, entry.ID) {
			t.Fatalf("unexpected widget ID %q", entry.ID)
		}
		if len(entry.SupportedSizes) == 0 || !entry.SupportsSize(entry.DefaultSize) {
			t.Fatalf("widget %q has invalid size metadata: supported=%v default=%q", entry.ID, entry.SupportedSizes, entry.DefaultSize)
		}
		inst := DashboardWidgetInstance{
			InstanceID: "test-" + strings.ReplaceAll(entry.ID, ".", "-"),
			WidgetID:   entry.ID,
			Size:       entry.DefaultSize,
			Slot:       DashboardSlotAuto,
			Position:   1,
			Config:     DefaultWidgetConfigMap(entry.ID),
		}
		if _, err := ValidateWidgetInstanceInput(inst, catalog); err != nil {
			t.Fatalf("default config for widget %q failed validation: %v", entry.ID, err)
		}
	}

	// Default layout must validate cleanly.
	def := DefaultDashboardLayout()
	validated, err := ValidateDashboardLayoutInput(def, catalog)
	if err != nil {
		t.Fatalf("default dashboard layout failed validation: %v", err)
	}
	if len(validated.Widgets) != len(expectedIDs) {
		t.Fatalf("expected default layout to include all %d widgets, got %d", len(expectedIDs), len(validated.Widgets))
	}
}

func TestValidateDashboardLayoutInput_RejectsInvalidAndXSSPayloads(t *testing.T) {
	utPropA := uuid.New()
	utPropB := uuid.New()
	catalog := PropertyResourceCatalog{
		PropertyID:   uuid.New(),
		UnitTypeIDs:  map[uuid.UUID]struct{}{utPropA: {}},
		ChannelCodes: map[string]struct{}{"DIRECT": {}},
	}

	cases := []struct {
		name   string
		layout DashboardLayout
	}{
		{
			name: "unknown widget id",
			layout: DashboardLayout{
				SchemaVersion: 1,
				Widgets: []DashboardWidgetInstance{{
					InstanceID: "w1", WidgetID: "evil.exec_widget", Size: WidgetSizeStandard, Position: 1,
				}},
			},
		},
		{
			name: "html in instance id",
			layout: DashboardLayout{
				SchemaVersion: 1,
				Widgets: []DashboardWidgetInstance{{
					InstanceID: "<script>alert(1)</script>", WidgetID: WidgetOpsChangeoverBoard, Size: WidgetSizeStandard, Position: 1,
				}},
			},
		},
		{
			name: "duplicate instance ids",
			layout: DashboardLayout{
				SchemaVersion: 1,
				Widgets: []DashboardWidgetInstance{
					{InstanceID: "dup-1", WidgetID: WidgetOpsChangeoverBoard, Size: WidgetSizeStandard, Position: 1},
					{InstanceID: "dup-1", WidgetID: WidgetMaintOpenQueue, Size: WidgetSizeStandard, Position: 2},
				},
			},
		},
		{
			name: "unsupported size on mini calendar",
			layout: DashboardLayout{
				SchemaVersion: 1,
				Widgets: []DashboardWidgetInstance{{
					InstanceID: "cal-1", WidgetID: WidgetResMiniCalendar, Size: WidgetSizeCompact, Position: 1,
				}},
			},
		},
		{
			name: "unsupported size on quick shortcuts",
			layout: DashboardLayout{
				SchemaVersion: 1,
				Widgets: []DashboardWidgetInstance{{
					InstanceID: "qs-1", WidgetID: WidgetWorkspaceQuickShortcuts, Size: WidgetSizeWide, Position: 1,
				}},
			},
		},
		{
			name: "invalid slot",
			layout: DashboardLayout{
				SchemaVersion: 1,
				Widgets: []DashboardWidgetInstance{{
					InstanceID: "w1", WidgetID: WidgetMaintOpenQueue, Size: WidgetSizeStandard, Slot: "javascript:bad", Position: 1,
				}},
			},
		},
		{
			name: "duplicate explicit positions",
			layout: DashboardLayout{
				SchemaVersion: 1,
				Widgets: []DashboardWidgetInstance{
					{InstanceID: "w1", WidgetID: WidgetMaintOpenQueue, Size: WidgetSizeStandard, Position: 1},
					{InstanceID: "w2", WidgetID: WidgetOpsChangeoverBoard, Size: WidgetSizeStandard, Position: 1},
				},
			},
		},
		{
			name: "out of range position",
			layout: DashboardLayout{
				SchemaVersion: 1,
				Widgets: []DashboardWidgetInstance{{
					InstanceID: "w1", WidgetID: WidgetMaintOpenQueue, Size: WidgetSizeStandard, Position: MaxDashboardWidgets + 1,
				}},
			},
		},
		{
			name: "unknown config key",
			layout: DashboardLayout{
				SchemaVersion: 1,
				Widgets: []DashboardWidgetInstance{{
					InstanceID: "w1", WidgetID: WidgetReportsKPISnapshot, Size: WidgetSizeStandard, Position: 1,
					Config: map[string]any{"window_days": 30, "custom_html": "<b>xss</b>"},
				}},
			},
		},
		{
			name: "out of bounds limit",
			layout: DashboardLayout{
				SchemaVersion: 1,
				Widgets: []DashboardWidgetInstance{{
					InstanceID: "w1", WidgetID: WidgetOpsArrivalsSummary, Size: WidgetSizeStandard, Position: 1,
					Config: map[string]any{"limit": 100},
				}},
			},
		},
		{
			name: "cross property unit_type_id",
			layout: DashboardLayout{
				SchemaVersion: 1,
				Widgets: []DashboardWidgetInstance{{
					InstanceID: "w1", WidgetID: WidgetResMiniCalendar, Size: WidgetSizeWide, Position: 1,
					Config: map[string]any{"days": 7, "unit_type_id": utPropB.String()},
				}},
			},
		},
		{
			name: "empty alert categories",
			layout: DashboardLayout{
				SchemaVersion: 1,
				Widgets: []DashboardWidgetInstance{{
					InstanceID: "w1", WidgetID: WidgetOpsAlertsAttention, Size: WidgetSizeStandard, Position: 1,
					Config: map[string]any{"include_categories": []string{}},
				}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateDashboardLayoutInput(tc.layout, catalog); err == nil {
				t.Fatalf("expected validation error for case %q, got nil", tc.name)
			}
		})
	}

	// Oversized layout (> MaxDashboardWidgets)
	oversized := make([]DashboardWidgetInstance, 0, MaxDashboardWidgets+1)
	for i := 1; i <= MaxDashboardWidgets+1; i++ {
		oversized = append(oversized, DashboardWidgetInstance{
			InstanceID: uuid.New().String(),
			WidgetID:   WidgetWorkspaceQuickShortcuts,
			Size:       WidgetSizeCompact,
			Position:   i,
		})
	}
	if _, err := ValidateDashboardLayoutInput(DashboardLayout{SchemaVersion: 1, Widgets: oversized}, catalog); err == nil {
		t.Fatalf("expected error for layout exceeding MaxDashboardWidgets")
	}

	// Valid same-property unit_type_id passes cleanly.
	validCal := DashboardLayout{
		SchemaVersion: 1,
		Widgets: []DashboardWidgetInstance{{
			InstanceID: "cal-ok",
			WidgetID:   WidgetResMiniCalendar,
			Size:       WidgetSizeWide,
			Position:   5,
			Config:     map[string]any{"days": 14, "unit_type_id": utPropA.String()},
		}},
	}
	got, err := ValidateDashboardLayoutInput(validCal, catalog)
	if err != nil || len(got.Widgets) != 1 || got.Widgets[0].Position != 1 {
		t.Fatalf("expected normalized position 1 for single valid calendar widget, got %+v err=%v", got, err)
	}
}

func TestDecodeAndNormalizeDashboardLayout_RecoveryAndPermFiltering(t *testing.T) {
	utPropA := uuid.New()
	utForeign := uuid.New()
	catalog := PropertyResourceCatalog{
		PropertyID:  uuid.New(),
		UnitTypeIDs: map[uuid.UUID]struct{}{utPropA: {}},
	}
	allowAll := func(string) bool { return true }
	housekeepingOnly := func(perm string) bool {
		return perm == "ops:read" || perm == "maintenance:read"
	}

	// 1. Corrupt top-level JSON degrades safely to filtered platform default.
	resCorrupt := DecodeAndNormalizeDashboardLayout(1, []byte("{not-json"), nil, catalog, housekeepingOnly, nil, nil)
	if !resCorrupt.IsDefault || len(resCorrupt.Warnings) == 0 {
		t.Fatalf("expected fallback to default layout with diagnostic warnings on corrupt JSON, got %+v", resCorrupt)
	}
	for _, w := range resCorrupt.Widgets {
		if w.WidgetID == WidgetReportsKPISnapshot || w.WidgetID == WidgetChannelsSyncHealth || w.WidgetID == WidgetCRMOpenEnquiries {
			t.Fatalf("restricted role unexpectedly received %s", w.WidgetID)
		}
	}

	// 2. Partial item recovery: unknown widget dropped, legacy size upcasted, foreign unit_type_id cleared, valid siblings kept!
	partialJSON := []byte(`{
		"schema_version": 99,
		"widgets": [
			{"instance_id": "bad-widget", "widget_id": "legacy.removed_widget", "size": "compact", "position": 1},
			{"instance_id": "cal-1", "widget_id": "res.mini_calendar", "size": "normal", "slot": "weird", "position": 2, "config": {"days": 14, "unit_type_id": "` + utForeign.String() + `", "injected": "<script>"}},
			{"instance_id": "maint-1", "widget_id": "maint.open_queue", "size": "wide", "slot": "main", "position": 3, "config": {"priority_min": "high", "limit": 8}}
		]
	}`)
	recovered := DecodeAndNormalizeDashboardLayout(99, partialJSON, nil, catalog, allowAll, nil, nil)
	if recovered.IsDefault {
		t.Fatalf("expected custom recovered layout rather than full reset to platform default")
	}
	if len(recovered.Widgets) != 2 {
		t.Fatalf("expected 2 valid surviving widgets, got %d (%+v)", len(recovered.Widgets), recovered.Widgets)
	}
	cal := recovered.Widgets[0]
	if cal.WidgetID != WidgetResMiniCalendar || cal.Size != WidgetSizeWide || cal.Config["unit_type_id"] != nil {
		t.Fatalf("expected cross-property unit_type_id cleared & unsupported size recovered on res.mini_calendar: %+v", cal)
	}
	if cal.Config["days"] != 14 {
		t.Fatalf("expected valid sibling field days=14 preserved during config recovery, got %#v", cal.Config["days"])
	}
	maint := recovered.Widgets[1]
	if maint.Config["priority_min"] != "high" || ConfigInt(maint.Config, "limit", 0) != 8 {
		t.Fatalf("expected valid sibling widget preserved untouched: %+v", maint)
	}
	if len(recovered.Warnings) == 0 {
		t.Fatalf("expected sanitized diagnostic warnings on recovered layout")
	}
}
