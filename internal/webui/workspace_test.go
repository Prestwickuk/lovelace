package webui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/branding"
	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
)

func TestWorkspacePageRender_AllWidgetsEditAndAccessibilityCSP(t *testing.T) {
	h, err := New(nil, config.Config{})
	if err != nil {
		t.Fatalf("webui.New: %v", err)
	}

	prop := domain.Property{
		ID:       uuid.New(),
		Slug:     "tir-bach",
		Name:     "Tir Bach Meadows",
		Region:   "Pembrokeshire",
		Currency: "GBP",
	}
	utID := uuid.New()
	ws := domain.DefaultResolvedWorkspace()
	ws.Theme.Mode = branding.ThemeModeDark
	ws.Theme.Contrast = branding.ThemeContrastHigh
	ws.Theme.Motion = branding.ThemeMotionReduced
	ws.Theme.TextScale = branding.ThemeTextScaleXL

	relDate := "2026-10-01"
	widgets := []service.RenderedDashboardWidget{
		{
			InstanceID: "w-arrivals", WidgetID: domain.WidgetOpsArrivalsSummary,
			DisplayName: "Arrivals & Departures Today", Description: "Confirmed arrivals, pending holds, due-outs",
			Category: "Operations", Size: domain.WidgetSizeStandard, Slot: domain.DashboardSlotAuto, Position: 1,
			SupportedSizes: []string{domain.WidgetSizeStandard, domain.WidgetSizeWide},
			Config:         domain.DefaultWidgetConfigMap(domain.WidgetOpsArrivalsSummary),
			ArrivalsSummary: &service.DashboardArrivalsSummaryData{
				OperationalDate: "2026-11-16", ArrivalsTotal: 2, ArrivalsPendingCheckIn: 1,
				DeparturesTotal: 1, DeparturesPendingOut: 1, ShowPendingHolds: true, PendingHoldsCount: 1,
				ShowInHouseCount: true, InHouseStaysCount: 3,
				Rows: []service.DashboardArrivalRow{
					{Kind: "ARRIVAL", ReservationID: uuid.New(), Reference: "RES-101", GuestName: "Carys Evans", Status: "CONFIRMED", UnitCodes: []string{"BT-01"}},
				},
			},
		},
		{
			InstanceID: "w-changeover", WidgetID: domain.WidgetOpsChangeoverBoard,
			DisplayName: "Daily Changeover Queue", Description: "5-stage housekeeping readiness",
			Category: "Operations", Size: domain.WidgetSizeStandard, Slot: domain.DashboardSlotAuto, Position: 2,
			SupportedSizes: []string{domain.WidgetSizeStandard, domain.WidgetSizeWide},
			Config:         domain.DefaultWidgetConfigMap(domain.WidgetOpsChangeoverBoard),
			ChangeoverBoard: &service.DashboardChangeoverBoardData{
				OperationalDate: "2026-11-16", TotalMatching: 1,
				Rows: []service.DashboardChangeoverRow{
					{UnitCode: "BT-01", UnitName: "Bell Tent 1", UnitTypeName: "Bell Tent", Stage: domain.StageCleaningRequired, StageLabel: "Cleaning Required", Overdue: true, SameDayArrival: true},
				},
			},
		},
		{
			InstanceID: "w-alerts", WidgetID: domain.WidgetOpsAlertsAttention,
			DisplayName: "Operational Alerts", Description: "Permitted operational alerts",
			Category: "Operations", Size: domain.WidgetSizeStandard, Slot: domain.DashboardSlotAuto, Position: 3,
			SupportedSizes: []string{domain.WidgetSizeCompact, domain.WidgetSizeStandard},
			Config:         domain.DefaultWidgetConfigMap(domain.WidgetOpsAlertsAttention),
			AlertsAttention: &service.DashboardAlertsAttentionData{
				TotalCount:     1,
				CategoryCounts: []service.DashboardAlertCategoryCount{{Category: "payments", Label: "Payments", Count: 1}},
				Items: []domain.OpsAttentionItem{
					{Severity: domain.SeverityUrgent, Title: "Open payment exception", Detail: "Needs reconciliation", ActionLabel: "Resolve", ActionURL: "/payment-exceptions"},
				},
			},
		},
		{
			InstanceID: "w-maint", WidgetID: domain.WidgetMaintOpenQueue,
			DisplayName: "Open Maintenance Queue", Description: "Urgent/high defects",
			Category: "Maintenance", Size: domain.WidgetSizeStandard, Slot: domain.DashboardSlotAuto, Position: 4,
			SupportedSizes: []string{domain.WidgetSizeStandard, domain.WidgetSizeWide},
			Config:         domain.DefaultWidgetConfigMap(domain.WidgetMaintOpenQueue),
			MaintOpenQueue: &service.DashboardMaintOpenQueueData{
				TotalMatching: 1,
				Requests: []domain.MaintenanceRequest{
					{ID: uuid.New(), Reference: "MNT-2026-0001", Title: "Boiler Check", Priority: domain.MaintPriorityHigh, Status: domain.MaintStatusReported},
				},
			},
		},
		{
			InstanceID: "w-prev", WidgetID: domain.WidgetMaintPreventativeDue,
			DisplayName: "Due Preventative Schedules", Description: "Upcoming recurring tasks",
			Category: "Maintenance", Size: domain.WidgetSizeCompact, Slot: domain.DashboardSlotSide, Position: 5,
			SupportedSizes: []string{domain.WidgetSizeCompact, domain.WidgetSizeStandard},
			Config:         domain.DefaultWidgetConfigMap(domain.WidgetMaintPreventativeDue),
			MaintPreventative: &service.DashboardMaintPreventativeData{
				HorizonDays: 14, TotalMatching: 1,
				Items: []service.DashboardMaintScheduleItem{{ID: uuid.New(), Title: "Gas Safety Inspection", TargetLabel: "Hut 1", NextDueDate: "2026-11-20"}},
			},
		},
		{
			InstanceID: "w-pending", WidgetID: domain.WidgetResPendingInquiries,
			DisplayName: "Provisional Holds & Inquiries", Description: "Unconfirmed bookings",
			Category: "Reservations", Size: domain.WidgetSizeCompact, Slot: domain.DashboardSlotAuto, Position: 6,
			SupportedSizes: []string{domain.WidgetSizeCompact, domain.WidgetSizeStandard},
			Config:         domain.DefaultWidgetConfigMap(domain.WidgetResPendingInquiries),
			PendingInquiries: &service.DashboardPendingInquiriesData{
				TotalMatching: 1, IncludeInquiries: true,
				Items: []service.DashboardPendingHoldItem{{ID: uuid.New(), Reference: "RES-102", GuestName: "Owen Parry", Status: domain.ResStatusPending, ArrivalDate: "2026-11-21", UnitSummary: "HUT-01", AgeLabel: "2h old"}},
			},
		},
		{
			InstanceID: "w-cal", WidgetID: domain.WidgetResMiniCalendar,
			DisplayName: "Availability Horizon", Description: "7 or 14-day overview",
			Category: "Reservations", Size: domain.WidgetSizeWide, Slot: domain.DashboardSlotMain, Position: 7,
			SupportedSizes: []string{domain.WidgetSizeWide},
			Config:         domain.DefaultWidgetConfigMap(domain.WidgetResMiniCalendar),
			MiniCalendar: &service.DashboardMiniCalendarData{
				Days: 7, TotalUnits: 1, CalendarURL: "/calendar?horizon=7d",
				Dates: []time.Time{time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)},
				Units: []service.CalendarUnitRow{
					{Unit: domain.Unit{Code: "BT-01", UnitTypeName: "Bell Tent"}, Cells: []service.CalendarDayCell{{Status: "BOOKED", Reference: "RES-101"}}},
				},
			},
		},
		{
			InstanceID: "w-crm", WidgetID: domain.WidgetCRMOpenEnquiries,
			DisplayName: "Guest Enquiries & Complaints", Description: "Open ENQ-* SLA queue",
			Category: "Guests & CRM", Size: domain.WidgetSizeStandard, Slot: domain.DashboardSlotAuto, Position: 8,
			SupportedSizes: []string{domain.WidgetSizeStandard, domain.WidgetSizeWide},
			Config:         domain.DefaultWidgetConfigMap(domain.WidgetCRMOpenEnquiries),
			OpenEnquiries: &service.DashboardOpenEnquiriesData{
				TotalMatching: 1,
				Items: []service.DashboardEnquiryRow{{
					ID: uuid.New(), Reference: "ENQ-2026-0001", Category: domain.EnqCatComplaint,
					Status: domain.EnqStatusNew, Priority: domain.EnqPriorityHigh, Subject: "Check-in time", GuestLabel: "Megan Hughes",
				}},
			},
		},
		{
			InstanceID: "w-chan", WidgetID: domain.WidgetChannelsSyncHealth,
			DisplayName: "Channel Sync Health", Description: "Provider status and conflicts",
			Category: "Channels", Size: domain.WidgetSizeCompact, Slot: domain.DashboardSlotSide, Position: 9,
			SupportedSizes: []string{domain.WidgetSizeCompact, domain.WidgetSizeStandard},
			Config:         domain.DefaultWidgetConfigMap(domain.WidgetChannelsSyncHealth),
			ChannelsHealth: &service.DashboardChannelsHealthData{
				ShowOKProviders: true, ConfiguredCount: 1, IssueCount: 1,
				Rows: []service.DashboardChannelProviderRow{
					{ProviderCode: "beds24", ProviderName: "Beds24", Status: "CONNECTED", MappingsCount: 1, FailedJobsCount: 1, HasIssue: true},
				},
			},
		},
		{
			InstanceID: "w-kpi", WidgetID: domain.WidgetReportsKPISnapshot,
			DisplayName: "7/30-Day Performance Snapshot", Description: "Occupancy, ADR, RevPAR (per currency)",
			Category: "Reports & BI", Size: domain.WidgetSizeStandard, Slot: domain.DashboardSlotAuto, Position: 10,
			SupportedSizes: []string{domain.WidgetSizeCompact, domain.WidgetSizeStandard},
			Config:         domain.DefaultWidgetConfigMap(domain.WidgetReportsKPISnapshot),
			KPISnapshot: &service.DashboardKPISnapshotData{
				WindowDays: 30, StartDate: "2026-10-18", EndDate: "2026-11-17",
				OccupancyPct: "42.50%", OccupiedUnitNights: 17, SellableUnitNights: 40,
				MultiCurrencyDetected: true, InventoryHistoryIncomplete: true, ReliableFrom: relDate,
				CompletenessWarnings: []string{"multiple currencies present; metrics are separated by currency"},
				ByCurrency: []domain.CurrencyRevenueMetrics{
					{Currency: "GBP", GrossBookedTotalPence: 45000, ADRFormatted: "£150.00", RevPARFormatted: "£45.00"},
					{Currency: "EUR", GrossBookedTotalPence: 20000, ADRFormatted: "EUR 100.00", RevPARFormatted: "EUR 10.00"},
				},
			},
		},
		{
			InstanceID: "w-links", WidgetID: domain.WidgetWorkspaceQuickShortcuts,
			DisplayName: "Personal Quick Links", Description: "Pinned routes and saved views",
			Category: "Workspace", Size: domain.WidgetSizeCompact, Slot: domain.DashboardSlotSide, Position: 11,
			SupportedSizes: []string{domain.WidgetSizeCompact},
			Config:         domain.DefaultWidgetConfigMap(domain.WidgetWorkspaceQuickShortcuts),
			QuickShortcuts: &service.DashboardQuickShortcutsData{
				MaxItems: 8, ShowSavedFilters: true, HasDataWidgets: true,
				Items: []service.DashboardShortcutLink{{Label: "Guest Directory", URL: "/guests", Kind: "route"}},
			},
		},
	}

	pageData := struct {
		baseView
		Dashboard WorkspacePageData
		EditMode  bool
	}{
		baseView: baseView{
			Title:               "My Workspace",
			ActiveNav:           "workspace",
			Brand:               branding.Default(),
			Theme:               ws.Theme,
			Workspace:           ws,
			CSRFToken:           "csrf-token-123",
			Property:            prop,
			CanReadReservations: true,
			CanReadGuests:       true,
			CanReadMaintenance:  true,
			CanReadReports:      true,
			CanReadChannels:     true,
			Actor:               service.Actor{UserID: uuid.New(), FullName: "Carys Evans", Email: "carys@tirbach.wales", Role: domain.RoleManager},
		},
		Dashboard: WorkspacePageData{
			WorkspaceDashboardData: service.WorkspaceDashboardData{
				Property:          prop,
				Layout:            domain.ResolvedDashboardLayout{SchemaVersion: 1, IsDefault: false, Warnings: []string{"test warning"}},
				Widgets:           widgets,
				EligibleCatalogue: domain.DashboardWidgetRegistry(),
				UnitTypes:         []domain.UnitType{{ID: utID, Code: "bell-tent", Name: "Bell Tent"}},
			},
			WidgetCount: len(widgets),
			MaxWidgets:  domain.MaxDashboardWidgets,
		},
		EditMode: true,
	}

	rec := httptest.NewRecorder()
	h.renderPage(rec, "workspace.html", pageData)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK rendering workspace.html, got %d", rec.Code)
	}
	body := rec.Body.String()

	for _, want := range []string{
		`Tir Bach Meadows — Personal Workspace`,
		`href="/workspace" class="active" aria-current="page"`,
		`id="workspace-editor-heading"`,
		`/workspace/widgets/add`,
		`/workspace/widgets/w-arrivals/move`,
		`/workspace/widgets/w-arrivals/update`,
		`/workspace/widgets/w-arrivals/remove`,
		`/workspace/reset`,
		`Partial unit inventory history:`,
		`2026-10-01`,
		`£450.00`,
		`€200.00`,
		`ENQ-2026-0001`,
		`MNT-2026-0001`,
		`RES-101`,
		`BT-01`,
		`<caption class="sr-only">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("workspace SSR output missing %q", want)
		}
	}

	// Ensure strict CSP compliance: zero inline styles/handlers or non-/static scripts inside rendered HTML.
	if regexp.MustCompile(`(?i)\bstyle\s*=|<style\b|\bon[a-z]+\s*=`).MatchString(body) {
		t.Errorf("workspace.html violated CSP by emitting inline style or event handler")
	}
	if strings.Contains(body, "Render Error") {
		t.Errorf("workspace.html triggered template render error fallback")
	}

	// Empty layout graceful state renders cleanly.
	pageData.EditMode = false
	pageData.Dashboard.Widgets = nil
	pageData.Dashboard.WidgetCount = 0
	emptyRec := httptest.NewRecorder()
	h.renderPage(emptyRec, "workspace.html", pageData)
	if emptyRec.Code != http.StatusOK || !strings.Contains(emptyRec.Body.String(), "No Workspace Widgets Active") {
		t.Fatalf("expected empty state render cleanly, got status=%d", emptyRec.Code)
	}
}

func TestParseWidgetConfigFromForm(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/workspace/widgets/w1/update", strings.NewReader(url.Values{
		"window_days":       {"7"},
		"show_prior_period": {"1"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = req.ParseForm()
	cfg, err := parseWidgetConfigFromForm(req, domain.WidgetReportsKPISnapshot)
	if err != nil {
		t.Fatalf("parseWidgetConfigFromForm reports.kpi_snapshot: %v", err)
	}
	if cfg["window_days"] != 7 || cfg["show_prior_period"] != true {
		t.Fatalf("unexpected parsed config: %+v", cfg)
	}

	badReq := httptest.NewRequest(http.MethodPost, "/workspace/widgets/w1/update", strings.NewReader(url.Values{
		"limit": {"abc"},
	}.Encode()))
	badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = badReq.ParseForm()
	if _, err := parseWidgetConfigFromForm(badReq, domain.WidgetMaintOpenQueue); err == nil {
		t.Fatalf("expected ValidationError for non-integer form limit")
	}
}
