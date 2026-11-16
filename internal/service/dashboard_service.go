package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/channels"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

// Dashboard View Models returned strictly from typed service builders to SSR templates or API JSON.

type WorkspaceDashboardData struct {
	Property          domain.Property                `json:"property"`
	Layout            domain.ResolvedDashboardLayout `json:"layout"`
	Widgets           []RenderedDashboardWidget      `json:"widgets"`
	EligibleCatalogue []domain.WidgetRegistryEntry   `json:"eligible_catalogue"`
	UnitTypes         []domain.UnitType              `json:"unit_types,omitempty"`
	Warnings          []string                       `json:"warnings,omitempty"`
}

type RenderedDashboardWidget struct {
	InstanceID        string                          `json:"instance_id"`
	WidgetID          string                          `json:"widget_id"`
	DisplayName       string                          `json:"display_name"`
	Description       string                          `json:"description"`
	Category          string                          `json:"category"`
	Size              string                          `json:"size"`
	Slot              string                          `json:"slot"`
	Position          int                             `json:"position"`
	SupportedSizes    []string                        `json:"supported_sizes"`
	Config            map[string]any                  `json:"config"`
	ArrivalsSummary   *DashboardArrivalsSummaryData   `json:"arrivals_summary,omitempty"`
	ChangeoverBoard   *DashboardChangeoverBoardData   `json:"changeover_board,omitempty"`
	AlertsAttention   *DashboardAlertsAttentionData   `json:"alerts_attention,omitempty"`
	MaintOpenQueue    *DashboardMaintOpenQueueData    `json:"maint_open_queue,omitempty"`
	MaintPreventative *DashboardMaintPreventativeData `json:"maint_preventative,omitempty"`
	PendingInquiries  *DashboardPendingInquiriesData  `json:"pending_inquiries,omitempty"`
	MiniCalendar      *DashboardMiniCalendarData      `json:"mini_calendar,omitempty"`
	OpenEnquiries     *DashboardOpenEnquiriesData     `json:"open_enquiries,omitempty"`
	ChannelsHealth    *DashboardChannelsHealthData    `json:"channels_health,omitempty"`
	KPISnapshot       *DashboardKPISnapshotData       `json:"kpi_snapshot,omitempty"`
	QuickShortcuts    *DashboardQuickShortcutsData    `json:"quick_shortcuts,omitempty"`
}

type DashboardArrivalRow struct {
	Kind          string // ARRIVAL | DEPARTURE
	ReservationID uuid.UUID
	Reference     string
	GuestName     string
	Status        string
	UnitCodes     []string
	StayDates     string
}

type DashboardArrivalsSummaryData struct {
	OperationalDate        string
	ArrivalsTotal          int
	ArrivalsPendingCheckIn int
	DeparturesTotal        int
	DeparturesPendingOut   int
	ShowPendingHolds       bool
	PendingHoldsCount      int
	ShowInHouseCount       bool
	InHouseStaysCount      int
	Rows                   []DashboardArrivalRow
}

type DashboardChangeoverRow struct {
	UnitCode       string
	UnitName       string
	UnitTypeName   string
	Stage          domain.ChangeoverStage
	StageLabel     string
	Priority       string
	Overdue        bool
	SameDayArrival bool
	NextArrivalRef string // Populated ONLY when user holds reservations:read
}

type DashboardChangeoverBoardData struct {
	OperationalDate string
	TotalMatching   int
	Rows            []DashboardChangeoverRow
}

type DashboardAlertCategoryCount struct {
	Category string
	Label    string
	Count    int
}

type DashboardAlertsAttentionData struct {
	TotalCount     int
	CategoryCounts []DashboardAlertCategoryCount
	Items          []domain.OpsAttentionItem
}

type DashboardMaintOpenQueueData struct {
	TotalMatching int
	Requests      []domain.MaintenanceRequest
}

type DashboardMaintScheduleItem struct {
	ID          uuid.UUID
	Title       string
	TargetLabel string
	NextDueDate string
	IsOverdue   bool
	Priority    domain.MaintenancePriority
}

type DashboardMaintPreventativeData struct {
	HorizonDays   int
	TotalMatching int
	Items         []DashboardMaintScheduleItem
}

type DashboardPendingHoldItem struct {
	ID          uuid.UUID
	Reference   string
	GuestName   string
	Status      domain.ReservationStatus
	ArrivalDate string
	UnitSummary string
	AgeLabel    string
}

type DashboardPendingInquiriesData struct {
	TotalMatching    int
	IncludeInquiries bool
	Items            []DashboardPendingHoldItem
}

type DashboardMiniCalendarData struct {
	Days         int
	UnitTypeID   string
	UnitTypeName string
	Dates        []time.Time
	Units        []CalendarUnitRow
	TotalUnits   int
	Truncated    bool
	CalendarURL  string
}

// DashboardEnquiryRow omits raw Description, ResolutionNotes, email/phone, guest_notes, and internal messages.
type DashboardEnquiryRow struct {
	ID            uuid.UUID
	Reference     string
	Category      domain.EnquiryCategory
	Status        domain.EnquiryStatus
	Priority      domain.EnquiryPriority
	Subject       string
	GuestLabel    string
	FollowUpLabel string
	TargetLabel   string
	Overdue       bool
}

type DashboardOpenEnquiriesData struct {
	TotalMatching int
	Items         []DashboardEnquiryRow
}

type DashboardChannelProviderRow struct {
	ProviderCode      string
	ProviderName      string
	Status            string
	MappingsCount     int
	FailedJobsCount   int
	PendingJobsCount  int
	UnmappedCount     int
	OverbookedCount   int
	FailedImportCount int
	HasIssue          bool
}

type DashboardChannelsHealthData struct {
	ShowOKProviders bool
	ConfiguredCount int
	IssueCount      int
	Rows            []DashboardChannelProviderRow
}

type DashboardKPISnapshotData struct {
	WindowDays                 int
	StartDate                  string
	EndDate                    string
	PriorPeriodShown           bool
	PriorStartDate             string
	PriorEndDate               string
	OccupancyPct               string
	PriorOccupancyPct          string
	OccupiedUnitNights         int64
	SellableUnitNights         int64
	ZeroAvailability           bool
	ByCurrency                 []domain.CurrencyRevenueMetrics
	PriorByCurrency            []domain.CurrencyRevenueMetrics
	InventoryHistoryIncomplete bool
	ReliableFrom               string
	MultiCurrencyDetected      bool
	CompletenessWarnings       []string
}

type DashboardShortcutLink struct {
	Label string
	URL   string
	Kind  string // route | saved_view | action
}

type DashboardQuickShortcutsData struct {
	MaxItems         int
	ShowSavedFilters bool
	HasDataWidgets   bool
	Items            []DashboardShortcutLink
}

// GetDashboardLayout loads targetUserID's resolved PWE-3 dashboard layout for propertyID.
func (s *Service) GetDashboardLayout(ctx context.Context, actor Actor, targetUserID, propertyID uuid.UUID) (domain.ResolvedDashboardLayout, error) {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	if err := checkUserPropertyAccess(user, propertyID); err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	if _, err := s.store.GetPropertyByID(ctx, nil, propertyID); err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	catalog, err := s.store.LoadPropertyResourceCatalog(ctx, nil, propertyID)
	if err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	return s.loadResolvedDashboardLayout(ctx, user, propertyID, catalog)
}

func (s *Service) loadResolvedDashboardLayout(
	ctx context.Context,
	user domain.AdminUser,
	propertyID uuid.UUID,
	catalog domain.PropertyResourceCatalog,
) (domain.ResolvedDashboardLayout, error) {
	permCheck := actorPermChecker(user.Role)
	ver, rawJSON, updatedAt, err := s.store.GetPropertyDashboardLayoutRaw(ctx, nil, user.ID, propertyID)
	if err != nil {
		if postgres.IsNotFound(err) {
			return domain.DecodeAndNormalizeDashboardLayout(1, nil, nil, catalog, permCheck, nil, nil), nil
		}
		return domain.ResolvedDashboardLayout{}, err
	}
	return domain.DecodeAndNormalizeDashboardLayout(ver, rawJSON, updatedAt, catalog, permCheck, nil, nil), nil
}

// SaveDashboardLayout validates and persists targetUserID's (user_id, property_id) dashboard layout
// without altering PWE-2 preferences or saved views.
func (s *Service) SaveDashboardLayout(
	ctx context.Context,
	actor Actor,
	targetUserID, propertyID uuid.UUID,
	in domain.DashboardLayout,
) (domain.ResolvedDashboardLayout, error) {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	if err := checkUserPropertyAccess(user, propertyID); err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	if _, err := s.store.GetPropertyByID(ctx, nil, propertyID); err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}

	permCheck := actorPermChecker(user.Role)
	var out domain.ResolvedDashboardLayout
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		catalog, txErr := s.store.LoadPropertyResourceCatalog(ctx, tx, propertyID)
		if txErr != nil {
			return txErr
		}
		clean, vErr := domain.ValidateDashboardLayoutInput(in, catalog)
		if vErr != nil {
			return vErr
		}
		for _, w := range clean.Widgets {
			entry, _ := domain.LookupDashboardWidget(w.WidgetID)
			if !domain.WidgetPermitted(entry, permCheck) {
				return domain.ErrForbidden
			}
		}
		rawBytes, mErr := json.Marshal(clean)
		if mErr != nil {
			return mErr
		}
		updAt, uErr := s.store.UpsertPropertyDashboardLayout(ctx, tx, user.ID, propertyID, clean.SchemaVersion, rawBytes)
		if uErr != nil {
			return uErr
		}
		out = domain.DecodeAndNormalizeDashboardLayout(clean.SchemaVersion, rawBytes, &updAt, catalog, permCheck, nil, nil)
		return nil
	})
	return out, err
}

// ResetDashboardLayout clears targetUserID's dashboard layout override for propertyID while
// preserving PWE-2 workspace preferences and saved views.
func (s *Service) ResetDashboardLayout(ctx context.Context, actor Actor, targetUserID, propertyID uuid.UUID) (domain.ResolvedDashboardLayout, error) {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	if err := checkUserPropertyAccess(user, propertyID); err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	if _, err := s.store.GetPropertyByID(ctx, nil, propertyID); err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	catalog, err := s.store.LoadPropertyResourceCatalog(ctx, nil, propertyID)
	if err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	if err := s.store.ResetPropertyDashboardLayout(ctx, nil, user.ID, propertyID); err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	return domain.DecodeAndNormalizeDashboardLayout(1, nil, nil, catalog, actorPermChecker(user.Role), nil, nil), nil
}

// AddDashboardWidget appends an eligible widget instance to targetUserID's layout.
func (s *Service) AddDashboardWidget(
	ctx context.Context,
	actor Actor,
	targetUserID, propertyID uuid.UUID,
	widgetID, size, slot string,
	cfg map[string]any,
) (domain.ResolvedDashboardLayout, error) {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	if err := checkUserPropertyAccess(user, propertyID); err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	if _, err := s.store.GetPropertyByID(ctx, nil, propertyID); err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	entry, ok := domain.LookupDashboardWidget(widgetID)
	if !ok {
		return domain.ResolvedDashboardLayout{}, domain.NewValidationError(map[string]string{
			"widget_id": "unknown or unsupported dashboard widget identifier",
		})
	}
	permCheck := actorPermChecker(user.Role)
	if !domain.WidgetPermitted(entry, permCheck) {
		return domain.ResolvedDashboardLayout{}, domain.ErrForbidden
	}

	var out domain.ResolvedDashboardLayout
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		catalog, txErr := s.store.LoadPropertyResourceCatalog(ctx, tx, propertyID)
		if txErr != nil {
			return txErr
		}
		cur, txErr := s.loadMutableDashboardLayoutTx(ctx, tx, user, propertyID, catalog, permCheck)
		if txErr != nil {
			return txErr
		}
		if len(cur.Widgets) >= domain.MaxDashboardWidgets {
			return domain.NewValidationError(map[string]string{
				"widgets": fmt.Sprintf("maximum of %d widgets per workspace reached; remove a widget first", domain.MaxDashboardWidgets),
			})
		}
		uid, uErr := postgres.NewUUID()
		if uErr != nil {
			return uErr
		}
		instID := "w-" + strings.ReplaceAll(uid.String()[:12], "-", "")
		newInst := domain.DashboardWidgetInstance{
			InstanceID: instID,
			WidgetID:   entry.ID,
			Size:       size,
			Slot:       slot,
			Position:   len(cur.Widgets) + 1,
			Config:     cfg,
		}
		cur.Widgets = append(cur.Widgets, newInst)
		clean, vErr := domain.ValidateDashboardLayoutInput(cur, catalog)
		if vErr != nil {
			return vErr
		}
		rawBytes, mErr := json.Marshal(clean)
		if mErr != nil {
			return mErr
		}
		updAt, uErr := s.store.UpsertPropertyDashboardLayout(ctx, tx, user.ID, propertyID, clean.SchemaVersion, rawBytes)
		if uErr != nil {
			return uErr
		}
		out = domain.DecodeAndNormalizeDashboardLayout(clean.SchemaVersion, rawBytes, &updAt, catalog, permCheck, nil, nil)
		return nil
	})
	return out, err
}

// UpdateDashboardWidget updates size, slot, and typed config of an existing instance.
func (s *Service) UpdateDashboardWidget(
	ctx context.Context,
	actor Actor,
	targetUserID, propertyID uuid.UUID,
	instanceID, size, slot string,
	cfg map[string]any,
) (domain.ResolvedDashboardLayout, error) {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	if err := checkUserPropertyAccess(user, propertyID); err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	if _, err := s.store.GetPropertyByID(ctx, nil, propertyID); err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	permCheck := actorPermChecker(user.Role)
	cleanID := strings.TrimSpace(instanceID)
	if cleanID == "" {
		return domain.ResolvedDashboardLayout{}, domain.NewValidationError(map[string]string{"instance_id": "instance_id is required"})
	}

	var out domain.ResolvedDashboardLayout
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		catalog, txErr := s.store.LoadPropertyResourceCatalog(ctx, tx, propertyID)
		if txErr != nil {
			return txErr
		}
		cur, txErr := s.loadMutableDashboardLayoutTx(ctx, tx, user, propertyID, catalog, permCheck)
		if txErr != nil {
			return txErr
		}
		idx := -1
		for i := range cur.Widgets {
			if cur.Widgets[i].InstanceID == cleanID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return domain.ErrNotFound
		}
		entry, ok := domain.LookupDashboardWidget(cur.Widgets[idx].WidgetID)
		if !ok || !domain.WidgetPermitted(entry, permCheck) {
			return domain.ErrForbidden
		}
		if size != "" {
			cur.Widgets[idx].Size = size
		}
		if slot != "" {
			cur.Widgets[idx].Slot = slot
		}
		if cfg != nil {
			cur.Widgets[idx].Config = cfg
		}
		clean, vErr := domain.ValidateDashboardLayoutInput(cur, catalog)
		if vErr != nil {
			return vErr
		}
		rawBytes, mErr := json.Marshal(clean)
		if mErr != nil {
			return mErr
		}
		updAt, uErr := s.store.UpsertPropertyDashboardLayout(ctx, tx, user.ID, propertyID, clean.SchemaVersion, rawBytes)
		if uErr != nil {
			return uErr
		}
		out = domain.DecodeAndNormalizeDashboardLayout(clean.SchemaVersion, rawBytes, &updAt, catalog, permCheck, nil, nil)
		return nil
	})
	return out, err
}

// MoveDashboardWidget reorders one widget instance via direction ("up"|"down") or bounded targetPos (1..N).
func (s *Service) MoveDashboardWidget(
	ctx context.Context,
	actor Actor,
	targetUserID, propertyID uuid.UUID,
	instanceID, direction string,
	targetPos int,
) (domain.ResolvedDashboardLayout, error) {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	if err := checkUserPropertyAccess(user, propertyID); err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	if _, err := s.store.GetPropertyByID(ctx, nil, propertyID); err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	permCheck := actorPermChecker(user.Role)
	cleanID := strings.TrimSpace(instanceID)
	dir := strings.ToLower(strings.TrimSpace(direction))
	if dir != "" && dir != "up" && dir != "down" {
		return domain.ResolvedDashboardLayout{}, domain.NewValidationError(map[string]string{"direction": "must be up or down"})
	}

	var out domain.ResolvedDashboardLayout
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		catalog, txErr := s.store.LoadPropertyResourceCatalog(ctx, tx, propertyID)
		if txErr != nil {
			return txErr
		}
		cur, txErr := s.loadMutableDashboardLayoutTx(ctx, tx, user, propertyID, catalog, permCheck)
		if txErr != nil {
			return txErr
		}
		idx := -1
		for i := range cur.Widgets {
			if cur.Widgets[i].InstanceID == cleanID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return domain.ErrNotFound
		}
		n := len(cur.Widgets)
		dest := idx
		switch dir {
		case "up":
			if idx > 0 {
				dest = idx - 1
			}
		case "down":
			if idx < n-1 {
				dest = idx + 1
			}
		default:
			if targetPos < 1 || targetPos > n {
				return domain.NewValidationError(map[string]string{
					"position": fmt.Sprintf("must be between 1 and %d", n),
				})
			}
			dest = targetPos - 1
		}
		if dest != idx {
			item := cur.Widgets[idx]
			cur.Widgets = append(cur.Widgets[:idx], cur.Widgets[idx+1:]...)
			rear := append([]domain.DashboardWidgetInstance{item}, cur.Widgets[dest:]...)
			cur.Widgets = append(cur.Widgets[:dest], rear...)
		}
		for i := range cur.Widgets {
			cur.Widgets[i].Position = i + 1
		}

		clean, vErr := domain.ValidateDashboardLayoutInput(cur, catalog)
		if vErr != nil {
			return vErr
		}
		rawBytes, mErr := json.Marshal(clean)
		if mErr != nil {
			return mErr
		}
		updAt, uErr := s.store.UpsertPropertyDashboardLayout(ctx, tx, user.ID, propertyID, clean.SchemaVersion, rawBytes)
		if uErr != nil {
			return uErr
		}
		out = domain.DecodeAndNormalizeDashboardLayout(clean.SchemaVersion, rawBytes, &updAt, catalog, permCheck, nil, nil)
		return nil
	})
	return out, err
}

// RemoveDashboardWidget removes one widget instance from targetUserID's property layout.
func (s *Service) RemoveDashboardWidget(
	ctx context.Context,
	actor Actor,
	targetUserID, propertyID uuid.UUID,
	instanceID string,
) (domain.ResolvedDashboardLayout, error) {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	if err := checkUserPropertyAccess(user, propertyID); err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	if _, err := s.store.GetPropertyByID(ctx, nil, propertyID); err != nil {
		return domain.ResolvedDashboardLayout{}, err
	}
	permCheck := actorPermChecker(user.Role)
	cleanID := strings.TrimSpace(instanceID)

	var out domain.ResolvedDashboardLayout
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		catalog, txErr := s.store.LoadPropertyResourceCatalog(ctx, tx, propertyID)
		if txErr != nil {
			return txErr
		}
		cur, txErr := s.loadMutableDashboardLayoutTx(ctx, tx, user, propertyID, catalog, permCheck)
		if txErr != nil {
			return txErr
		}
		kept := make([]domain.DashboardWidgetInstance, 0, len(cur.Widgets))
		found := false
		for _, w := range cur.Widgets {
			if w.InstanceID == cleanID {
				found = true
				continue
			}
			w.Position = len(kept) + 1
			kept = append(kept, w)
		}
		if !found {
			return domain.ErrNotFound
		}
		cur.Widgets = kept
		rawBytes, mErr := json.Marshal(cur)
		if mErr != nil {
			return mErr
		}
		updAt, uErr := s.store.UpsertPropertyDashboardLayout(ctx, tx, user.ID, propertyID, cur.SchemaVersion, rawBytes)
		if uErr != nil {
			return uErr
		}
		out = domain.DecodeAndNormalizeDashboardLayout(cur.SchemaVersion, rawBytes, &updAt, catalog, permCheck, nil, nil)
		return nil
	})
	return out, err
}

func (s *Service) loadMutableDashboardLayoutTx(
	ctx context.Context,
	tx pgx.Tx,
	user domain.AdminUser,
	propertyID uuid.UUID,
	catalog domain.PropertyResourceCatalog,
	permCheck domain.PermCheckFunc,
) (domain.DashboardLayout, error) {
	ver, rawJSON, updatedAt, err := s.store.GetPropertyDashboardLayoutRaw(ctx, tx, user.ID, propertyID)
	if err != nil && !postgres.IsNotFound(err) {
		return domain.DashboardLayout{}, err
	}
	var resolved domain.ResolvedDashboardLayout
	if postgres.IsNotFound(err) || len(rawJSON) == 0 {
		resolved = domain.DecodeAndNormalizeDashboardLayout(1, nil, nil, catalog, permCheck, nil, nil)
		// Materialize ONLY the user's permitted default widgets on first customization so editing preserves visible order cleanly.
		return domain.DashboardLayout{
			SchemaVersion: domain.CurrentDashboardLayoutSchemaVersion,
			Widgets:       slices.Clone(resolved.Widgets),
		}, nil
	}
	resolved = domain.DecodeAndNormalizeDashboardLayout(ver, rawJSON, updatedAt, catalog, permCheck, nil, nil)
	widgets := slices.Clone(resolved.Widgets)
	for i := range widgets {
		widgets[i].Position = i + 1
	}
	return domain.DashboardLayout{
		SchemaVersion: domain.CurrentDashboardLayoutSchemaVersion,
		Widgets:       widgets,
	}, nil
}

// GetWorkspaceDashboard resolves targetUserID's layout for propertyID and populates data
// strictly for visible widgets matching live DB permissions and property scope.
func (s *Service) GetWorkspaceDashboard(
	ctx context.Context,
	actor Actor,
	targetUserID, propertyID uuid.UUID,
	ws domain.ResolvedWorkspace,
) (WorkspaceDashboardData, error) {
	user, err := s.verifyPersonalisationUser(ctx, actor, targetUserID, false)
	if err != nil {
		return WorkspaceDashboardData{}, err
	}
	if err := checkUserPropertyAccess(user, propertyID); err != nil {
		return WorkspaceDashboardData{}, err
	}
	prop, err := s.store.GetPropertyByID(ctx, nil, propertyID)
	if err != nil {
		return WorkspaceDashboardData{}, err
	}
	catalog, err := s.store.LoadPropertyResourceCatalog(ctx, nil, propertyID)
	if err != nil {
		return WorkspaceDashboardData{}, err
	}

	permCheck := actorPermChecker(user.Role)
	resolved, err := s.loadResolvedDashboardLayout(ctx, user, propertyID, catalog)
	if err != nil {
		return WorkspaceDashboardData{}, err
	}

	// Derive liveActor strictly from live DB AdminUser so stale session role never inflates or leaks access.
	liveActor := Actor{
		UserID:     user.ID,
		PropertyID: user.PropertyID,
		Email:      user.Email,
		FullName:   user.FullName,
		Role:       user.Role,
	}

	loc, tzErr := time.LoadLocation(strings.TrimSpace(prop.Timezone))
	if tzErr != nil {
		loc = time.UTC
	}
	nowLocal := time.Now().In(loc)
	todayDate := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, time.UTC)

	eligible := domain.EligibleDashboardWidgets(permCheck)
	var unitTypes []domain.UnitType
	unitTypeByID := make(map[uuid.UUID]domain.UnitType)
	if auth.HasPermission(user.Role, auth.PermInventoryRead) {
		if uts, uErr := s.store.ListUnitTypes(ctx, propertyID, false); uErr == nil {
			unitTypes = uts
			for _, ut := range uts {
				unitTypeByID[ut.ID] = ut
			}
		}
	}

	rendered := make([]RenderedDashboardWidget, 0, len(resolved.Widgets))
	hasDataWidgets := false
	for _, w := range resolved.Widgets {
		entry, ok := domain.LookupDashboardWidget(w.WidgetID)
		if !ok || !domain.WidgetPermitted(entry, permCheck) {
			continue
		}
		rw := RenderedDashboardWidget{
			InstanceID:     w.InstanceID,
			WidgetID:       entry.ID,
			DisplayName:    entry.DisplayName,
			Description:    entry.Description,
			Category:       entry.Category,
			Size:           w.Size,
			Slot:           w.Slot,
			Position:       w.Position,
			SupportedSizes: entry.SupportedSizes,
			Config:         w.Config,
		}
		if entry.ID != domain.WidgetWorkspaceQuickShortcuts {
			hasDataWidgets = true
		}

		switch entry.ID {
		case domain.WidgetOpsArrivalsSummary:
			data, dErr := s.buildWidgetOpsArrivals(ctx, liveActor, prop, todayDate, w.Config)
			if dErr == nil {
				rw.ArrivalsSummary = &data
			}
		case domain.WidgetOpsChangeoverBoard:
			data, dErr := s.buildWidgetOpsChangeover(ctx, liveActor, prop, todayDate, w.Config)
			if dErr == nil {
				rw.ChangeoverBoard = &data
			}
		case domain.WidgetOpsAlertsAttention:
			data, dErr := s.buildWidgetOpsAlerts(ctx, liveActor, prop, todayDate, w.Config)
			if dErr == nil {
				rw.AlertsAttention = &data
			}
		case domain.WidgetMaintOpenQueue:
			data, dErr := s.buildWidgetMaintOpenQueue(ctx, liveActor, propertyID, w.Config)
			if dErr == nil {
				rw.MaintOpenQueue = &data
			}
		case domain.WidgetMaintPreventativeDue:
			data, dErr := s.buildWidgetMaintPreventative(ctx, liveActor, propertyID, todayDate, w.Config)
			if dErr == nil {
				rw.MaintPreventative = &data
			}
		case domain.WidgetResPendingInquiries:
			data, dErr := s.buildWidgetResPendingInquiries(ctx, liveActor, propertyID, w.Config)
			if dErr == nil {
				rw.PendingInquiries = &data
			}
		case domain.WidgetResMiniCalendar:
			data, dErr := s.buildWidgetResMiniCalendar(ctx, liveActor, propertyID, todayDate, w.Config, unitTypeByID)
			if dErr == nil {
				rw.MiniCalendar = &data
			}
		case domain.WidgetCRMOpenEnquiries:
			data, dErr := s.buildWidgetCRMOpenEnquiries(ctx, liveActor, propertyID, todayDate, w.Config)
			if dErr == nil {
				rw.OpenEnquiries = &data
			}
		case domain.WidgetChannelsSyncHealth:
			data, dErr := s.buildWidgetChannelsHealth(ctx, liveActor, propertyID, w.Config)
			if dErr == nil {
				rw.ChannelsHealth = &data
			}
		case domain.WidgetReportsKPISnapshot:
			data, dErr := s.buildWidgetReportsKPI(ctx, liveActor, propertyID, todayDate, w.Config)
			if dErr == nil {
				rw.KPISnapshot = &data
			}
		case domain.WidgetWorkspaceQuickShortcuts:
			data := s.buildWidgetQuickShortcuts(user, ws, w.Config, hasDataWidgets)
			rw.QuickShortcuts = &data
		}

		rendered = append(rendered, rw)
	}

	return WorkspaceDashboardData{
		Property:          prop,
		Layout:            resolved,
		Widgets:           rendered,
		EligibleCatalogue: eligible,
		UnitTypes:         unitTypes,
		Warnings:          resolved.Warnings,
	}, nil
}

func (s *Service) buildWidgetOpsArrivals(
	ctx context.Context,
	actor Actor,
	prop domain.Property,
	todayDate time.Time,
	cfg map[string]any,
) (DashboardArrivalsSummaryData, error) {
	if err := s.requirePerm(actor, auth.PermOpsRead); err != nil {
		return DashboardArrivalsSummaryData{}, err
	}
	if err := s.requirePerm(actor, auth.PermReservationsRead); err != nil {
		return DashboardArrivalsSummaryData{}, err
	}
	if err := s.requirePropertyScope(actor, prop.ID); err != nil {
		return DashboardArrivalsSummaryData{}, err
	}

	stays, err := s.store.ListOperationsRelevantStays(ctx, prop.ID, todayDate, todayDate)
	if err != nil {
		return DashboardArrivalsSummaryData{}, err
	}
	showPending := domain.ConfigBool(cfg, "show_pending_holds", true)
	showInHouse := domain.ConfigBool(cfg, "show_in_house_count", true)
	limit := domain.ConfigInt(cfg, "limit", 10)
	if limit < 5 || limit > 25 {
		limit = 10
	}

	out := DashboardArrivalsSummaryData{
		OperationalDate:  domain.FormatDate(todayDate),
		ShowPendingHolds: showPending,
		ShowInHouseCount: showInHouse,
	}
	var arrRows, depRows []DashboardArrivalRow
	for _, st := range stays {
		r := st.Reservation
		var unitCodes []string
		for _, ru := range r.Units {
			if ru.UnitCode != "" {
				unitCodes = append(unitCodes, ru.UnitCode)
			}
		}
		stayDates := fmt.Sprintf("%s → %s", domain.FormatDate(r.ArrivalDate), domain.FormatDate(r.DepartureDate))

		if r.Status == domain.ResStatusPending && !r.ArrivalDate.Before(todayDate) {
			if showPending {
				out.PendingHoldsCount++
			}
		}
		if r.Status == domain.ResStatusCheckedIn && r.ArrivalDate.Before(todayDate) && r.DepartureDate.After(todayDate) {
			if showInHouse {
				out.InHouseStaysCount++
			}
		}
		if (r.Status == domain.ResStatusConfirmed || r.Status == domain.ResStatusCheckedIn) && r.ArrivalDate.Equal(todayDate) {
			out.ArrivalsTotal++
			if r.Status == domain.ResStatusConfirmed {
				out.ArrivalsPendingCheckIn++
			}
			arrRows = append(arrRows, DashboardArrivalRow{
				Kind:          "ARRIVAL",
				ReservationID: r.ID,
				Reference:     r.Reference,
				GuestName:     r.GuestName,
				Status:        string(r.Status),
				UnitCodes:     unitCodes,
				StayDates:     stayDates,
			})
		}
		if (r.Status == domain.ResStatusCheckedIn || r.Status == domain.ResStatusCheckedOut) && r.DepartureDate.Equal(todayDate) {
			out.DeparturesTotal++
			if r.Status == domain.ResStatusCheckedIn {
				out.DeparturesPendingOut++
			}
			depRows = append(depRows, DashboardArrivalRow{
				Kind:          "DEPARTURE",
				ReservationID: r.ID,
				Reference:     r.Reference,
				GuestName:     r.GuestName,
				Status:        string(r.Status),
				UnitCodes:     unitCodes,
				StayDates:     stayDates,
			})
		}
	}

	combined := append(arrRows, depRows...)
	if len(combined) > limit {
		combined = combined[:limit]
	}
	out.Rows = combined
	return out, nil
}

func (s *Service) buildWidgetOpsChangeover(
	ctx context.Context,
	actor Actor,
	prop domain.Property,
	todayDate time.Time,
	cfg map[string]any,
) (DashboardChangeoverBoardData, error) {
	if err := s.requirePerm(actor, auth.PermOpsRead); err != nil {
		return DashboardChangeoverBoardData{}, err
	}
	if err := s.requirePropertyScope(actor, prop.ID); err != nil {
		return DashboardChangeoverBoardData{}, err
	}

	unitsRows, err := s.store.ListOperationsUnits(ctx, prop.ID)
	if err != nil {
		return DashboardChangeoverBoardData{}, err
	}
	tasks, err := s.store.ListHousekeepingTasksForDate(ctx, prop.ID, todayDate)
	if err != nil {
		return DashboardChangeoverBoardData{}, err
	}

	departingUnits := make(map[uuid.UUID]bool)
	sameDayByUnit := make(map[uuid.UUID]bool)
	arrRefByUnit := make(map[uuid.UUID]string)

	if auth.HasPermission(actor.Role, auth.PermReservationsRead) {
		stays, sErr := s.store.ListOperationsRelevantStays(ctx, prop.ID, todayDate, todayDate)
		if sErr != nil {
			return DashboardChangeoverBoardData{}, sErr
		}
		for _, st := range stays {
			r := st.Reservation
			if (r.Status == domain.ResStatusConfirmed || r.Status == domain.ResStatusCheckedIn || r.Status == domain.ResStatusPending) && r.ArrivalDate.Equal(todayDate) {
				for _, ru := range r.Units {
					sameDayByUnit[ru.UnitID] = true
					if arrRefByUnit[ru.UnitID] == "" {
						arrRefByUnit[ru.UnitID] = r.Reference
					}
				}
			}
			if r.Status == domain.ResStatusCheckedIn && !r.DepartureDate.After(todayDate) {
				for _, ru := range r.Units {
					departingUnits[ru.UnitID] = true
				}
			}
		}
	} else {
		// Without reservations:read, query ONLY unit-level operational booleans (no guest names/booking refs).
		turnarounds, tErr := s.store.ListDashboardUnitTurnaroundFlags(ctx, prop.ID, todayDate, todayDate)
		if tErr != nil {
			return DashboardChangeoverBoardData{}, tErr
		}
		for uid, tr := range turnarounds {
			departingUnits[uid] = tr.DepartingToday
			sameDayByUnit[uid] = tr.SameDayArrival
		}
	}

	taskByUnit := make(map[uuid.UUID]domain.HousekeepingTask)
	for _, t := range tasks {
		if existing, exists := taskByUnit[t.UnitID]; !exists || t.Stage != domain.StageReady && existing.Stage == domain.StageReady {
			taskByUnit[t.UnitID] = t
		}
	}

	stageFilter := domain.ConfigStringSlice(cfg, "stage_filter", []string{"ALL"})
	showAll := len(stageFilter) == 0 || slices.Contains(stageFilter, "ALL")
	limit := domain.ConfigInt(cfg, "limit", 12)
	if limit < 5 || limit > 30 {
		limit = 12
	}

	var rows []DashboardChangeoverRow
	for _, uRow := range unitsRows {
		u := uRow.Unit
		stage := uRow.HousekeepingStage
		priority := string(domain.TaskPriorityNormal)
		overdue := false
		sameDay := sameDayByUnit[u.ID]
		if t, ok := taskByUnit[u.ID]; ok {
			stage = t.Stage
			priority = string(t.Priority)
			overdue = t.Overdue
			if t.SameDayArrival {
				sameDay = true
			}
		} else if departingUnits[u.ID] {
			stage = domain.StageGuestDeparting
		}
		if !showAll && !slices.Contains(stageFilter, string(stage)) {
			continue
		}
		rows = append(rows, DashboardChangeoverRow{
			UnitCode:       u.Code,
			UnitName:       u.Name,
			UnitTypeName:   u.UnitTypeName,
			Stage:          stage,
			StageLabel:     stage.Label(),
			Priority:       priority,
			Overdue:        overdue,
			SameDayArrival: sameDay,
			NextArrivalRef: arrRefByUnit[u.ID],
		})
	}

	stageOrder := func(s domain.ChangeoverStage) int {
		switch s {
		case domain.StageCleaningRequired:
			return 0
		case domain.StageCleaningInProgress:
			return 1
		case domain.StageInspectionRequired:
			return 2
		case domain.StageGuestDeparting:
			return 3
		default:
			return 4
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if stageOrder(rows[i].Stage) != stageOrder(rows[j].Stage) {
			return stageOrder(rows[i].Stage) < stageOrder(rows[j].Stage)
		}
		if rows[i].Overdue != rows[j].Overdue {
			return rows[i].Overdue
		}
		if rows[i].SameDayArrival != rows[j].SameDayArrival {
			return rows[i].SameDayArrival
		}
		return rows[i].UnitCode < rows[j].UnitCode
	})

	totalMatching := len(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}

	return DashboardChangeoverBoardData{
		OperationalDate: domain.FormatDate(todayDate),
		TotalMatching:   totalMatching,
		Rows:            rows,
	}, nil
}

func (s *Service) buildWidgetOpsAlerts(
	ctx context.Context,
	actor Actor,
	prop domain.Property,
	todayDate time.Time,
	cfg map[string]any,
) (DashboardAlertsAttentionData, error) {
	if err := s.requirePerm(actor, auth.PermOpsRead); err != nil {
		return DashboardAlertsAttentionData{}, err
	}
	if err := s.requirePropertyScope(actor, prop.ID); err != nil {
		return DashboardAlertsAttentionData{}, err
	}

	includeCats := domain.ConfigStringSlice(cfg, "include_categories", []string{
		domain.AlertCatPayments, domain.AlertCatChannels, domain.AlertCatComms,
		domain.AlertCatMaintenance, domain.AlertCatHousekeeping,
	})

	var catCounts []DashboardAlertCategoryCount
	var items []domain.OpsAttentionItem

	// 1. Payments category (requires reservations:read; never queried otherwise!)
	if slices.Contains(includeCats, domain.AlertCatPayments) && auth.HasPermission(actor.Role, auth.PermReservationsRead) {
		payEx, err := s.store.ListPaymentExceptions(ctx, prop.ID)
		if err != nil {
			return DashboardAlertsAttentionData{}, err
		}
		payAnom, err := s.store.ListOperationsPaymentAnomalies(ctx, prop.ID)
		if err != nil {
			return DashboardAlertsAttentionData{}, err
		}
		cnt := 0
		for _, pe := range payEx {
			if pe.Status != "OPEN" {
				continue
			}
			cnt++
			targetURL := "/payment-exceptions#ex-" + pe.ID.String()
			if pe.ReservationID != nil {
				targetURL = "/reservations/" + pe.ReservationID.String()
			}
			items = append(items, domain.OpsAttentionItem{
				Severity:    domain.SeverityUrgent,
				Category:    "PAYMENT_EXCEPTION",
				Title:       fmt.Sprintf("Open payment exception (%s)", pe.ExceptionType),
				Detail:      firstNonEmpty(pe.Details, "Customer payment requires manual reconciliation."),
				ActionLabel: "View exception",
				ActionURL:   targetURL,
			})
		}
		for _, a := range payAnom {
			cnt++
			targetURL := "/payment-exceptions"
			if a.ReservationID != nil {
				targetURL = "/reservations/" + a.ReservationID.String()
			}
			items = append(items, domain.OpsAttentionItem{
				Severity:    domain.SeverityUrgent,
				Category:    "FAILED_PAYMENT",
				Title:       fmt.Sprintf("Failed checkout / payment (%s)", a.Reference),
				Detail:      a.Detail,
				ActionLabel: "Review payment",
				ActionURL:   targetURL,
			})
		}
		catCounts = append(catCounts, DashboardAlertCategoryCount{
			Category: domain.AlertCatPayments,
			Label:    "Payments",
			Count:    cnt,
		})
	}

	// 2. Channels category (requires channels:read; never queried otherwise!)
	if slices.Contains(includeCats, domain.AlertCatChannels) && auth.HasPermission(actor.Role, auth.PermChannelsRead) {
		chRows, err := s.store.ListDashboardChannelHealth(ctx, prop.ID)
		if err != nil {
			return DashboardAlertsAttentionData{}, err
		}
		cnt := 0
		for _, ch := range chRows {
			providerName := ch.ProviderCode
			if def, ok := channels.GetProvider(ch.ProviderCode); ok {
				providerName = def.Name
			}
			if ch.Status == string(channels.StatusConnectionError) {
				cnt++
				items = append(items, domain.OpsAttentionItem{
					Severity:    domain.SeverityUrgent,
					Category:    "CHANNEL_CONNECTION_ERROR",
					Title:       fmt.Sprintf("%s connection error", providerName),
					Detail:      firstNonEmpty(ch.LastError, "OTA connection requires re-verification."),
					ActionLabel: "Channel Manager",
					ActionURL:   "/settings/integrations/channel-manager",
				})
			}
			if ch.FailedJobsCount > 0 {
				cnt += ch.FailedJobsCount
				items = append(items, domain.OpsAttentionItem{
					Severity:    domain.SeverityHigh,
					Category:    "CHANNEL_SYNC_FAILED",
					Title:       fmt.Sprintf("%s: %d failed sync job(s)", providerName, ch.FailedJobsCount),
					Detail:      fmt.Sprintf("Latest job status: %s", firstNonEmpty(ch.LastJobStatus, "FAILED")),
					ActionLabel: "Inspect sync",
					ActionURL:   "/settings/integrations/channel-manager",
				})
			}
			if ch.UnmappedCount > 0 || ch.OverbookedCount > 0 || ch.FailedImportCount > 0 {
				issueSub := ch.UnmappedCount + ch.OverbookedCount + ch.FailedImportCount
				cnt += issueSub
				sev := domain.SeverityHigh
				if ch.OverbookedCount > 0 {
					sev = domain.SeverityUrgent
				}
				items = append(items, domain.OpsAttentionItem{
					Severity:    sev,
					Category:    "CHANNEL_IMPORT_CONFLICT",
					Title:       fmt.Sprintf("%s OTA import issues (%d unmapped, %d conflicts)", providerName, ch.UnmappedCount, ch.OverbookedCount),
					Detail:      "Resolve unmapped external listings or overbooked conflicts.",
					ActionLabel: "Open Channel Manager",
					ActionURL:   "/settings/integrations/channel-manager",
				})
			}
		}
		catCounts = append(catCounts, DashboardAlertCategoryCount{
			Category: domain.AlertCatChannels,
			Label:    "OTA Channels",
			Count:    cnt,
		})
	}

	// 3. Comms / Email Outbox category (requires comms:read; never queried otherwise!)
	if slices.Contains(includeCats, domain.AlertCatComms) && auth.HasPermission(actor.Role, auth.PermCommsRead) {
		staleCutoff := s.nowUTC().Add(-15 * time.Minute)
		outTotal, outRows, err := s.store.ListDashboardOutboxAlerts(ctx, prop.ID, staleCutoff, 6)
		if err != nil {
			return DashboardAlertsAttentionData{}, err
		}
		for _, ob := range outRows {
			sev := domain.SeverityHigh
			if ob.Status == domain.OutboxStatusDispatchUnknown {
				sev = domain.SeverityUrgent
			}
			items = append(items, domain.OpsAttentionItem{
				Severity:    sev,
				Category:    "OUTBOX_ANOMALY",
				Title:       fmt.Sprintf("Email outbox %s (%s)", ob.Status, firstNonEmpty(ob.Template, "message")),
				Detail:      firstNonEmpty(ob.LastError, "Queued message requires retry or reconciliation."),
				ActionLabel: "Templates & Queue",
				ActionURL:   "/communications/settings",
			})
		}
		catCounts = append(catCounts, DashboardAlertCategoryCount{
			Category: domain.AlertCatComms,
			Label:    "Email Outbox",
			Count:    outTotal,
		})
	}

	// 4. Maintenance category (requires maintenance:read; never queried otherwise!)
	if slices.Contains(includeCats, domain.AlertCatMaintenance) && auth.HasPermission(actor.Role, auth.PermMaintenanceRead) {
		maintSum, err := s.store.GetOpsMaintenanceSummary(ctx, nil, prop.ID, todayDate, todayDate)
		if err != nil {
			return DashboardAlertsAttentionData{}, err
		}
		cnt := len(maintSum.PriorityJobs) + len(maintSum.AwaitingInspection)
		for _, r := range maintSum.PriorityJobs {
			sev := domain.SeverityHigh
			if r.SafetyImpact || r.Priority == domain.MaintPriorityUrgent {
				sev = domain.SeverityUrgent
			}
			items = append(items, domain.OpsAttentionItem{
				Severity:    sev,
				Category:    "MAINTENANCE_ATTENTION",
				Title:       fmt.Sprintf("Maintenance %s · %s", r.Reference, r.Title),
				Detail:      fmt.Sprintf("%s · %s", r.LocationLabel(), r.Status.Label()),
				ActionLabel: "Open job",
				ActionURL:   "/maintenance/" + r.ID.String(),
			})
		}
		for _, r := range maintSum.AwaitingInspection {
			items = append(items, domain.OpsAttentionItem{
				Severity:    domain.SeverityMedium,
				Category:    "MAINTENANCE_INSPECTION",
				Title:       fmt.Sprintf("Awaiting inspection · %s (%s)", r.Reference, r.Title),
				Detail:      r.LocationLabel(),
				ActionLabel: "Inspect",
				ActionURL:   "/maintenance/" + r.ID.String(),
			})
		}
		catCounts = append(catCounts, DashboardAlertCategoryCount{
			Category: domain.AlertCatMaintenance,
			Label:    "Maintenance",
			Count:    cnt,
		})
	}

	// 5. Housekeeping category (requires ops:read)
	if slices.Contains(includeCats, domain.AlertCatHousekeeping) && auth.HasPermission(actor.Role, auth.PermOpsRead) {
		tasks, err := s.store.ListHousekeepingTasksForDate(ctx, prop.ID, todayDate)
		if err != nil {
			return DashboardAlertsAttentionData{}, err
		}
		cnt := 0
		for _, t := range tasks {
			if t.Stage == domain.StageReady {
				continue
			}
			if t.Overdue || t.Priority == domain.TaskPriorityUrgent {
				cnt++
				sev := domain.SeverityHigh
				if t.Overdue && t.SameDayArrival {
					sev = domain.SeverityUrgent
				}
				items = append(items, domain.OpsAttentionItem{
					Severity:    sev,
					Category:    "HOUSEKEEPING_DUE",
					Title:       fmt.Sprintf("%s · %s (%s)", t.Stage.Label(), t.UnitCode, t.UnitName),
					Detail:      firstNonEmpty(t.Notes, t.Title),
					ActionLabel: "Open changeover",
					ActionURL:   "/?focus=housekeeping#changeover-board",
				})
			}
		}
		catCounts = append(catCounts, DashboardAlertCategoryCount{
			Category: domain.AlertCatHousekeeping,
			Label:    "Housekeeping",
			Count:    cnt,
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		return severityRank(items[i].Severity) < severityRank(items[j].Severity)
	})
	totalCount := 0
	for _, cc := range catCounts {
		totalCount += cc.Count
	}
	if len(items) > 12 {
		items = items[:12]
	}
	return DashboardAlertsAttentionData{
		TotalCount:     totalCount,
		CategoryCounts: catCounts,
		Items:          items,
	}, nil
}

func maintPriorityRank(p string) int {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "urgent":
		return 3
	case "high":
		return 2
	case "medium", "normal":
		return 1
	default:
		return 0
	}
}

func (s *Service) buildWidgetMaintOpenQueue(
	ctx context.Context,
	actor Actor,
	propertyID uuid.UUID,
	cfg map[string]any,
) (DashboardMaintOpenQueueData, error) {
	reqs, err := s.ListMaintenanceRequests(ctx, actor, propertyID, domain.MaintenanceRequestFilter{Limit: 100})
	if err != nil {
		return DashboardMaintOpenQueueData{}, err
	}
	minRank := maintPriorityRank(domain.ConfigString(cfg, "priority_min", "low"))
	assignedToMe := domain.ConfigBool(cfg, "assigned_to_me", false)
	limit := domain.ConfigInt(cfg, "limit", 10)
	if limit < 5 || limit > 20 {
		limit = 10
	}

	filtered := make([]domain.MaintenanceRequest, 0, len(reqs))
	for _, r := range reqs {
		pendingInspection := r.Status == domain.MaintStatusCompleted && r.InspectionRequired && r.LatestInspectionResult != "PASSED"
		if !r.Status.IsOpen() && !pendingInspection {
			continue
		}
		if maintPriorityRank(string(r.Priority)) < minRank {
			continue
		}
		if assignedToMe && (r.AssignedUserID == nil || *r.AssignedUserID != actor.UserID) {
			continue
		}
		filtered = append(filtered, r)
	}
	totalMatching := len(filtered)
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return DashboardMaintOpenQueueData{
		TotalMatching: totalMatching,
		Requests:      filtered,
	}, nil
}

func (s *Service) buildWidgetMaintPreventative(
	ctx context.Context,
	actor Actor,
	propertyID uuid.UUID,
	todayDate time.Time,
	cfg map[string]any,
) (DashboardMaintPreventativeData, error) {
	scheds, err := s.ListMaintenanceSchedules(ctx, actor, propertyID, false)
	if err != nil {
		return DashboardMaintPreventativeData{}, err
	}
	horizonDays := domain.ConfigInt(cfg, "horizon_days", 14)
	if horizonDays != 7 && horizonDays != 14 && horizonDays != 30 {
		horizonDays = 14
	}
	limit := domain.ConfigInt(cfg, "limit", 8)
	if limit < 5 || limit > 15 {
		limit = 8
	}
	cutoff := todayDate.AddDate(0, 0, horizonDays)

	var matching []domain.MaintenanceSchedule
	for _, sc := range scheds {
		if sc.NextDueDate.IsZero() {
			continue
		}
		dueDay := time.Date(sc.NextDueDate.Year(), sc.NextDueDate.Month(), sc.NextDueDate.Day(), 0, 0, 0, 0, time.UTC)
		if !dueDay.After(cutoff) {
			matching = append(matching, sc)
		}
	}
	sort.SliceStable(matching, func(i, j int) bool {
		return matching[i].NextDueDate.Before(matching[j].NextDueDate)
	})
	totalMatching := len(matching)
	if len(matching) > limit {
		matching = matching[:limit]
	}
	items := make([]DashboardMaintScheduleItem, 0, len(matching))
	for _, sc := range matching {
		dueDay := time.Date(sc.NextDueDate.Year(), sc.NextDueDate.Month(), sc.NextDueDate.Day(), 0, 0, 0, 0, time.UTC)
		target := sc.LocationName
		if sc.UnitCode != "" {
			target = sc.UnitCode
		} else if sc.AssetName != "" {
			target = sc.AssetName
		}
		items = append(items, DashboardMaintScheduleItem{
			ID:          sc.ID,
			Title:       sc.Title,
			TargetLabel: firstNonEmpty(target, "Property-wide"),
			NextDueDate: domain.FormatDate(dueDay),
			IsOverdue:   dueDay.Before(todayDate),
			Priority:    sc.Priority,
		})
	}
	return DashboardMaintPreventativeData{
		HorizonDays:   horizonDays,
		TotalMatching: totalMatching,
		Items:         items,
	}, nil
}

func (s *Service) buildWidgetResPendingInquiries(
	ctx context.Context,
	actor Actor,
	propertyID uuid.UUID,
	cfg map[string]any,
) (DashboardPendingInquiriesData, error) {
	if err := s.requirePerm(actor, auth.PermReservationsRead); err != nil {
		return DashboardPendingInquiriesData{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return DashboardPendingInquiriesData{}, err
	}

	includeInquiries := domain.ConfigBool(cfg, "include_inquiries", true)
	limit := domain.ConfigInt(cfg, "limit", 10)
	if limit < 5 || limit > 20 {
		limit = 10
	}

	pending, err := s.ListReservations(ctx, actor, propertyID, postgres.ReservationFilter{Status: string(domain.ResStatusPending)})
	if err != nil {
		return DashboardPendingInquiriesData{}, err
	}
	combined := append([]domain.Reservation{}, pending...)
	if includeInquiries {
		inqs, err := s.ListReservations(ctx, actor, propertyID, postgres.ReservationFilter{Status: string(domain.ResStatusInquiry)})
		if err != nil {
			return DashboardPendingInquiriesData{}, err
		}
		combined = append(combined, inqs...)
	}
	sort.SliceStable(combined, func(i, j int) bool {
		if !combined[i].ArrivalDate.Equal(combined[j].ArrivalDate) {
			return combined[i].ArrivalDate.Before(combined[j].ArrivalDate)
		}
		return combined[i].CreatedAt.Before(combined[j].CreatedAt)
	})
	totalMatching := len(combined)
	if len(combined) > limit {
		combined = combined[:limit]
	}

	now := s.nowUTC()
	items := make([]DashboardPendingHoldItem, 0, len(combined))
	for _, r := range combined {
		var codes []string
		for _, ru := range r.Units {
			if ru.UnitCode != "" {
				codes = append(codes, ru.UnitCode)
			}
		}
		ageHrs := int(now.Sub(r.CreatedAt).Hours())
		ageLabel := "New"
		if ageHrs >= 24 {
			ageLabel = fmt.Sprintf("%dd old", ageHrs/24)
		} else if ageHrs > 0 {
			ageLabel = fmt.Sprintf("%dh old", ageHrs)
		}
		items = append(items, DashboardPendingHoldItem{
			ID:          r.ID,
			Reference:   r.Reference,
			GuestName:   r.GuestName,
			Status:      r.Status,
			ArrivalDate: domain.FormatDate(r.ArrivalDate),
			UnitSummary: firstNonEmpty(strings.Join(codes, ", "), "Unassigned"),
			AgeLabel:    ageLabel,
		})
	}
	return DashboardPendingInquiriesData{
		TotalMatching:    totalMatching,
		IncludeInquiries: includeInquiries,
		Items:            items,
	}, nil
}

func (s *Service) buildWidgetResMiniCalendar(
	ctx context.Context,
	actor Actor,
	propertyID uuid.UUID,
	todayDate time.Time,
	cfg map[string]any,
	unitTypeByID map[uuid.UUID]domain.UnitType,
) (DashboardMiniCalendarData, error) {
	if err := s.requirePerm(actor, auth.PermInventoryRead); err != nil {
		return DashboardMiniCalendarData{}, err
	}
	if err := s.requirePerm(actor, auth.PermReservationsRead); err != nil {
		return DashboardMiniCalendarData{}, err
	}
	if err := s.requirePerm(actor, auth.PermBlocksRead); err != nil {
		return DashboardMiniCalendarData{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return DashboardMiniCalendarData{}, err
	}

	days := domain.ConfigInt(cfg, "days", 7)
	horizon := domain.Horizon7Days
	if days == 14 {
		horizon = domain.Horizon14Days
	} else {
		days = 7
	}

	var utPtr *uuid.UUID
	utIDStr := ""
	utName := ""
	if rawUT := strings.TrimSpace(domain.ConfigString(cfg, "unit_type_id", "")); rawUT != "" {
		if parsed, err := uuid.Parse(rawUT); err == nil {
			if ut, ok := unitTypeByID[parsed]; ok {
				utPtr = &parsed
				utIDStr = parsed.String()
				utName = ut.Name
			}
		}
	}

	cal, err := s.GetCalendarView(ctx, actor, CalendarQuery{
		PropertyID: propertyID,
		Horizon:    horizon,
		Grouping:   domain.GroupingUnitType,
		StartDate:  todayDate,
		UnitTypeID: utPtr,
	})
	if err != nil {
		return DashboardMiniCalendarData{}, err
	}
	totalUnits := len(cal.Units)
	truncated := false
	units := cal.Units
	if len(units) > 10 {
		units = units[:10]
		truncated = true
	}
	calURL := "/calendar?horizon=" + horizon
	if utIDStr != "" {
		calURL += "&unit_type_id=" + utIDStr
	}

	return DashboardMiniCalendarData{
		Days:         days,
		UnitTypeID:   utIDStr,
		UnitTypeName: utName,
		Dates:        cal.Dates,
		Units:        units,
		TotalUnits:   totalUnits,
		Truncated:    truncated,
		CalendarURL:  calURL,
	}, nil
}

func (s *Service) buildWidgetCRMOpenEnquiries(
	ctx context.Context,
	actor Actor,
	propertyID uuid.UUID,
	todayDate time.Time,
	cfg map[string]any,
) (DashboardOpenEnquiriesData, error) {
	if err := s.requirePerm(actor, auth.PermGuestsRead); err != nil {
		return DashboardOpenEnquiriesData{}, err
	}
	if err := s.requirePerm(actor, auth.PermCommsRead); err != nil {
		return DashboardOpenEnquiriesData{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return DashboardOpenEnquiriesData{}, err
	}

	prioFilter := strings.ToUpper(strings.TrimSpace(domain.ConfigString(cfg, "priority_filter", "ALL")))
	if prioFilter == "ALL" {
		prioFilter = ""
	}
	catFilter := ""
	if domain.ConfigBool(cfg, "only_complaints", false) {
		catFilter = string(domain.EnqCatComplaint)
	}
	limit := domain.ConfigInt(cfg, "limit", 10)
	if limit < 5 || limit > 20 {
		limit = 10
	}

	enqs, err := s.ListGuestEnquiries(ctx, actor, propertyID, domain.GuestEnquiryFilter{
		OpenOnly: true,
		Category: catFilter,
		Priority: prioFilter,
		Limit:    50,
	})
	if err != nil {
		return DashboardOpenEnquiriesData{}, err
	}
	totalMatching := len(enqs)
	if len(enqs) > limit {
		enqs = enqs[:limit]
	}

	nowUTC := s.nowUTC()
	items := make([]DashboardEnquiryRow, 0, len(enqs))
	for _, eq := range enqs {
		overdue := false
		followLabel := ""
		if eq.FollowUpAt != nil {
			followLabel = domain.FormatDate(*eq.FollowUpAt)
			if eq.FollowUpAt.Before(todayDate.AddDate(0, 0, 1)) {
				overdue = true
			}
		}
		targetLabel := ""
		if eq.TargetResponseAt != nil {
			targetLabel = eq.TargetResponseAt.UTC().Format("2006-01-02 15:04")
			if eq.TargetResponseAt.Before(nowUTC) {
				overdue = true
			}
		}
		guestLabel := firstNonEmpty(eq.GuestName, eq.RequesterName)
		if guestLabel == "" {
			guestLabel = "Linked guest"
		}
		items = append(items, DashboardEnquiryRow{
			ID:            eq.ID,
			Reference:     eq.Reference,
			Category:      eq.Category,
			Status:        eq.Status,
			Priority:      eq.Priority,
			Subject:       eq.Subject,
			GuestLabel:    guestLabel,
			FollowUpLabel: followLabel,
			TargetLabel:   targetLabel,
			Overdue:       overdue,
		})
	}
	return DashboardOpenEnquiriesData{
		TotalMatching: totalMatching,
		Items:         items,
	}, nil
}

func (s *Service) buildWidgetChannelsHealth(
	ctx context.Context,
	actor Actor,
	propertyID uuid.UUID,
	cfg map[string]any,
) (DashboardChannelsHealthData, error) {
	if err := s.requirePerm(actor, auth.PermChannelsRead); err != nil {
		return DashboardChannelsHealthData{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return DashboardChannelsHealthData{}, err
	}

	showOK := domain.ConfigBool(cfg, "show_ok_providers", true)
	dbRows, err := s.store.ListDashboardChannelHealth(ctx, propertyID)
	if err != nil {
		return DashboardChannelsHealthData{}, err
	}
	byCode := make(map[string]postgres.DashboardChannelHealthRow, len(dbRows))
	for _, r := range dbRows {
		byCode[r.ProviderCode] = r
	}

	var rows []DashboardChannelProviderRow
	configuredCount := 0
	issueCount := 0
	for _, def := range channels.ListProviders() {
		status := string(channels.StatusNotConfigured)
		var mappings, failedJobs, pendingJobs, unmapped, overbooked, failedImp int
		hasIssue := false
		if r, ok := byCode[def.Code]; ok {
			status = r.Status
			mappings = r.MappingsCount
			failedJobs = r.FailedJobsCount
			pendingJobs = r.PendingJobsCount
			unmapped = r.UnmappedCount
			overbooked = r.OverbookedCount
			failedImp = r.FailedImportCount
			if status != string(channels.StatusNotConfigured) && status != string(channels.StatusDisabled) {
				configuredCount++
			}
			hasIssue = status == string(channels.StatusConnectionError) || failedJobs > 0 || unmapped > 0 || overbooked > 0 || failedImp > 0
			if hasIssue {
				issueCount++
			}
		}
		if !showOK && !hasIssue {
			continue
		}
		rows = append(rows, DashboardChannelProviderRow{
			ProviderCode:      def.Code,
			ProviderName:      def.Name,
			Status:            status,
			MappingsCount:     mappings,
			FailedJobsCount:   failedJobs,
			PendingJobsCount:  pendingJobs,
			UnmappedCount:     unmapped,
			OverbookedCount:   overbooked,
			FailedImportCount: failedImp,
			HasIssue:          hasIssue,
		})
	}
	return DashboardChannelsHealthData{
		ShowOKProviders: showOK,
		ConfiguredCount: configuredCount,
		IssueCount:      issueCount,
		Rows:            rows,
	}, nil
}

func (s *Service) buildWidgetReportsKPI(
	ctx context.Context,
	actor Actor,
	propertyID uuid.UUID,
	todayDate time.Time,
	cfg map[string]any,
) (DashboardKPISnapshotData, error) {
	if err := s.requirePerm(actor, auth.PermReportsRead); err != nil {
		return DashboardKPISnapshotData{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return DashboardKPISnapshotData{}, err
	}

	windowDays := domain.ConfigInt(cfg, "window_days", 30)
	if windowDays != 7 && windowDays != 30 {
		windowDays = 30
	}
	showPrior := domain.ConfigBool(cfg, "show_prior_period", true)

	endDt := todayDate.AddDate(0, 0, 1)
	startDt := endDt.AddDate(0, 0, -windowDays)
	q := ReportWindowQuery{
		StartDate: domain.FormatDate(startDt),
		EndDate:   domain.FormatDate(endDt),
	}

	occ, err := s.GetOccupancyReport(ctx, actor, propertyID, q)
	if err != nil {
		return DashboardKPISnapshotData{}, err
	}
	rev, err := s.GetRevenueReport(ctx, actor, propertyID, q)
	if err != nil {
		return DashboardKPISnapshotData{}, err
	}

	// Merge completeness warnings cleanly without duplicates.
	warningSet := make(map[string]struct{})
	var warnings []string
	addWarn := func(list []string) {
		for _, w := range list {
			if _, seen := warningSet[w]; !seen {
				warningSet[w] = struct{}{}
				warnings = append(warnings, w)
			}
		}
	}
	addWarn(occ.Completeness.Warnings)
	addWarn(rev.Completeness.Warnings)

	reliableFrom := ""
	if occ.Completeness.InventoryHistoryReliableFrom != nil {
		reliableFrom = *occ.Completeness.InventoryHistoryReliableFrom
	}
	out := DashboardKPISnapshotData{
		WindowDays:                 windowDays,
		StartDate:                  occ.Period.StartDate,
		EndDate:                    occ.Period.EndDate,
		PriorPeriodShown:           showPrior,
		OccupancyPct:               occ.Summary.OccupancyRatePct,
		OccupiedUnitNights:         occ.Summary.OccupiedUnitNights,
		SellableUnitNights:         occ.Summary.SellableUnitNights,
		ZeroAvailability:           occ.Summary.ZeroAvailability,
		ByCurrency:                 rev.ByCurrency,
		InventoryHistoryIncomplete: occ.Completeness.InventoryHistoryIncomplete,
		ReliableFrom:               reliableFrom,
		MultiCurrencyDetected:      rev.Completeness.MultiCurrencyDetected,
		CompletenessWarnings:       warnings,
	}

	if showPrior {
		priorEnd := startDt
		priorStart := priorEnd.AddDate(0, 0, -windowDays)
		pq := ReportWindowQuery{
			StartDate: domain.FormatDate(priorStart),
			EndDate:   domain.FormatDate(priorEnd),
		}
		if pOcc, pErr := s.GetOccupancyReport(ctx, actor, propertyID, pq); pErr == nil {
			out.PriorStartDate = pOcc.Period.StartDate
			out.PriorEndDate = pOcc.Period.EndDate
			out.PriorOccupancyPct = pOcc.Summary.OccupancyRatePct
		}
		if pRev, rErr := s.GetRevenueReport(ctx, actor, propertyID, pq); rErr == nil {
			out.PriorByCurrency = pRev.ByCurrency
		}
	}

	return out, nil
}

func (s *Service) buildWidgetQuickShortcuts(
	user domain.AdminUser,
	ws domain.ResolvedWorkspace,
	cfg map[string]any,
	hasDataWidgets bool,
) DashboardQuickShortcutsData {
	maxItems := domain.ConfigInt(cfg, "max_items", 8)
	if maxItems < 4 || maxItems > 12 {
		maxItems = 8
	}
	showSaved := domain.ConfigBool(cfg, "show_saved_filters", true)

	seenURL := make(map[string]struct{})
	var items []DashboardShortcutLink
	add := func(label, urlPath, kind string) {
		if len(items) >= maxItems {
			return
		}
		if _, dup := seenURL[urlPath]; dup {
			return
		}
		seenURL[urlPath] = struct{}{}
		items = append(items, DashboardShortcutLink{Label: label, URL: urlPath, Kind: kind})
	}

	for _, sc := range ws.Shortcuts {
		add(sc.Label, sc.Path, "route")
	}
	if showSaved {
		for _, sv := range ws.SavedViews {
			var screenPath string
			switch sv.Screen {
			case domain.SavedViewScreenCalendar:
				screenPath = "/calendar"
			case domain.SavedViewScreenReservations:
				screenPath = "/reservations"
			case domain.SavedViewScreenGuests:
				screenPath = "/guests"
			default:
				continue
			}
			add(sv.Name, screenPath+"?saved_view="+sv.ID, "saved_view")
		}
	}

	// Fill any spare slots with permitted default actions so newly onboarded/restricted users have useful links.
	permCheck := actorPermChecker(user.Role)
	for _, opt := range domain.AllowedAdminRoutes() {
		if len(items) >= maxItems {
			break
		}
		if opt.PermKey == "" || permCheck(opt.PermKey) {
			add(opt.Label, opt.Path, "action")
		}
	}

	return DashboardQuickShortcutsData{
		MaxItems:         maxItems,
		ShowSavedFilters: showSaved,
		HasDataWidgets:   hasDataWidgets,
		Items:            items,
	}
}
