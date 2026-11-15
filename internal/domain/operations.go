package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ChangeoverStage represents the 5-stage daily operational housekeeping workflow.
// Stage completion is independent of inventory availability and maintenance restrictions.
type ChangeoverStage string

const (
	StageGuestDeparting     ChangeoverStage = "GUEST_DEPARTING"
	StageCleaningRequired   ChangeoverStage = "CLEANING_REQUIRED"
	StageCleaningInProgress ChangeoverStage = "CLEANING_IN_PROGRESS"
	StageInspectionRequired ChangeoverStage = "INSPECTION_REQUIRED"
	StageReady              ChangeoverStage = "READY"
)

func (s ChangeoverStage) Valid() bool {
	switch s {
	case StageGuestDeparting, StageCleaningRequired, StageCleaningInProgress, StageInspectionRequired, StageReady:
		return true
	default:
		return false
	}
}

// Label returns a human-readable operational label for the changeover stage.
func (s ChangeoverStage) Label() string {
	switch s {
	case StageGuestDeparting:
		return "Guest departing"
	case StageCleaningRequired:
		return "Cleaning required"
	case StageCleaningInProgress:
		return "Cleaning in progress"
	case StageInspectionRequired:
		return "Inspection required"
	case StageReady:
		return "Ready for guests"
	default:
		return string(s)
	}
}

var allowedChangeoverTransitions = map[ChangeoverStage][]ChangeoverStage{
	StageGuestDeparting:     {StageCleaningRequired},
	StageCleaningRequired:   {StageCleaningInProgress},
	StageCleaningInProgress: {StageInspectionRequired, StageCleaningRequired},
	StageInspectionRequired: {StageReady, StageCleaningRequired},
	StageReady:              {StageCleaningRequired},
}

// CanTransitionTo validates whether moving a changeover task from s to next is permitted.
func (s ChangeoverStage) CanTransitionTo(next ChangeoverStage) bool {
	for _, allowed := range allowedChangeoverTransitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// AllowedNextStages returns valid outgoing stages for the current stage.
func (s ChangeoverStage) AllowedNextStages() []ChangeoverStage {
	out := make([]ChangeoverStage, len(allowedChangeoverTransitions[s]))
	copy(out, allowedChangeoverTransitions[s])
	return out
}

// HousekeepingPriority defines task urgency on the Operations Centre board.
type HousekeepingPriority string

const (
	TaskPriorityLow    HousekeepingPriority = "LOW"
	TaskPriorityNormal HousekeepingPriority = "NORMAL"
	TaskPriorityHigh   HousekeepingPriority = "HIGH"
	TaskPriorityUrgent HousekeepingPriority = "URGENT"
)

func (p HousekeepingPriority) Valid() bool {
	switch p {
	case TaskPriorityLow, TaskPriorityNormal, TaskPriorityHigh, TaskPriorityUrgent:
		return true
	default:
		return false
	}
}

// HousekeepingTaskType classifies operational cleaning and inspection tasks.
type HousekeepingTaskType string

const (
	TaskTypeChangeoverClean HousekeepingTaskType = "CHANGEOVER_CLEAN"
	TaskTypeInspection      HousekeepingTaskType = "INSPECTION"
	TaskTypeDeepClean       HousekeepingTaskType = "DEEP_CLEAN"
	TaskTypeMidStayRefresh  HousekeepingTaskType = "MID_STAY_REFRESH"
	TaskTypeGeneral         HousekeepingTaskType = "GENERAL"
)

func (t HousekeepingTaskType) Valid() bool {
	switch t {
	case TaskTypeChangeoverClean, TaskTypeInspection, TaskTypeDeepClean, TaskTypeMidStayRefresh, TaskTypeGeneral:
		return true
	default:
		return false
	}
}

// HousekeepingTask tracks unit cleaning, changeover, and inspection execution.
type HousekeepingTask struct {
	ID                   uuid.UUID            `json:"id"`
	PropertyID           uuid.UUID            `json:"property_id"`
	UnitID               uuid.UUID            `json:"unit_id"`
	UnitCode             string               `json:"unit_code"`
	UnitName             string               `json:"unit_name"`
	UnitTypeName         string               `json:"unit_type_name"`
	UnitZone             string               `json:"unit_zone"`
	UnitStatus           string               `json:"unit_status"`
	ReservationID        *uuid.UUID           `json:"reservation_id,omitempty"`
	ReservationReference string               `json:"reservation_reference,omitempty"`
	GuestName            string               `json:"guest_name,omitempty"`
	TaskType             HousekeepingTaskType `json:"task_type"`
	Stage                ChangeoverStage      `json:"stage"`
	Priority             HousekeepingPriority `json:"priority"`
	ScheduledDate        time.Time            `json:"scheduled_date"`
	DueAt                *time.Time           `json:"due_at,omitempty"`
	AssignedTo           *uuid.UUID           `json:"assigned_to,omitempty"`
	AssigneeName         string               `json:"assignee_name,omitempty"`
	Title                string               `json:"title"`
	Notes                string               `json:"notes"`
	SameDayArrival       bool                 `json:"same_day_arrival"`
	NextArrivalGuest     string               `json:"next_arrival_guest,omitempty"`
	NextArrivalRef       string               `json:"next_arrival_ref,omitempty"`
	HasActiveBlock       bool                 `json:"has_active_block"`
	BlockSummary         string               `json:"block_summary,omitempty"`
	Overdue              bool                 `json:"overdue"`
	StartedAt            *time.Time           `json:"started_at,omitempty"`
	StartedBy            *uuid.UUID           `json:"started_by,omitempty"`
	StartedByName        string               `json:"started_by_name,omitempty"`
	InspectedAt          *time.Time           `json:"inspected_at,omitempty"`
	InspectedBy          *uuid.UUID           `json:"inspected_by,omitempty"`
	InspectedByName      string               `json:"inspected_by_name,omitempty"`
	CompletedAt          *time.Time           `json:"completed_at,omitempty"`
	CompletedBy          *uuid.UUID           `json:"completed_by,omitempty"`
	Version              int                  `json:"version"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
}

func (t *HousekeepingTask) Validate() error {
	errs := map[string]string{}
	if t.PropertyID == uuid.Nil {
		errs["property_id"] = "is required"
	}
	if t.UnitID == uuid.Nil {
		errs["unit_id"] = "is required"
	}
	if t.TaskType == "" {
		t.TaskType = TaskTypeChangeoverClean
	} else {
		t.TaskType = HousekeepingTaskType(strings.ToUpper(strings.TrimSpace(string(t.TaskType))))
	}
	if !t.TaskType.Valid() {
		errs["task_type"] = "must be CHANGEOVER_CLEAN, INSPECTION, DEEP_CLEAN, MID_STAY_REFRESH, or GENERAL"
	}
	if t.Stage == "" {
		t.Stage = StageCleaningRequired
	} else {
		t.Stage = ChangeoverStage(strings.ToUpper(strings.TrimSpace(string(t.Stage))))
	}
	if !t.Stage.Valid() || t.Stage == StageGuestDeparting || t.Stage == StageReady {
		if !t.Stage.Valid() {
			errs["stage"] = "invalid changeover stage"
		}
	}
	if t.Priority == "" {
		t.Priority = TaskPriorityNormal
	} else {
		t.Priority = HousekeepingPriority(strings.ToUpper(strings.TrimSpace(string(t.Priority))))
	}
	if !t.Priority.Valid() {
		errs["priority"] = "must be LOW, NORMAL, HIGH, or URGENT"
	}
	if t.ScheduledDate.IsZero() {
		errs["scheduled_date"] = "is required"
	}
	t.Title = strings.TrimSpace(t.Title)
	t.Notes = strings.TrimSpace(t.Notes)
	if t.Title == "" {
		t.Title = fmt.Sprintf("%s changeover", strings.ReplaceAll(strings.ToLower(string(t.TaskType)), "_", " "))
	}
	return NewValidationError(errs)
}

// OpsFocus selects a role-appropriate operational emphasis without hiding data access.
type OpsFocus string

const (
	OpsFocusAll          OpsFocus = "all"
	OpsFocusFrontDesk    OpsFocus = "front_desk"
	OpsFocusHousekeeping OpsFocus = "housekeeping"
	OpsFocusAttention    OpsFocus = "attention"
)

func ParseOpsFocus(raw string) OpsFocus {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(OpsFocusFrontDesk):
		return OpsFocusFrontDesk
	case string(OpsFocusHousekeeping):
		return OpsFocusHousekeeping
	case string(OpsFocusAttention):
		return OpsFocusAttention
	default:
		return OpsFocusAll
	}
}

// OpsStayItem represents an arrival, departure, or in-house stay on the Operations Centre.
type OpsStayItem struct {
	ReservationID           uuid.UUID         `json:"reservation_id"`
	Reference               string            `json:"reference"`
	GuestID                 uuid.UUID         `json:"guest_id"`
	GuestName               string            `json:"guest_name"`
	GuestEmail              string            `json:"guest_email"`
	GuestPhone              string            `json:"guest_phone"`
	ChannelCode             string            `json:"channel_code"`
	ArrivalDate             time.Time         `json:"arrival_date"`
	DepartureDate           time.Time         `json:"departure_date"`
	Nights                  int               `json:"nights"`
	Adults                  int               `json:"adults"`
	Children                int               `json:"children"`
	Infants                 int               `json:"infants"`
	Status                  ReservationStatus `json:"status"`
	PaymentStatus           string            `json:"payment_status"`
	TotalPence              int64             `json:"total_pence"`
	AmountPaidPence         int64             `json:"amount_paid_pence"`
	BalanceDuePence         int64             `json:"balance_due_pence"`
	Currency                string            `json:"currency"`
	UnitLabels              []string          `json:"unit_labels"`
	UnitIDs                 []uuid.UUID       `json:"unit_ids"`
	UnitReadinessSummary    string            `json:"unit_readiness_summary"`
	HasUnreadyUnit          bool              `json:"has_unready_unit"`
	HasBlockedUnit          bool              `json:"has_blocked_unit"`
	IsBackToBack            bool              `json:"is_back_to_back"`
	SpecialRequests         string            `json:"special_requests,omitempty"`
	InternalNotes           string            `json:"internal_notes,omitempty"`
	CancellationRequestedAt *time.Time        `json:"cancellation_requested_at,omitempty"`
	CancellationRequestNote string            `json:"cancellation_request_note,omitempty"`
	RequiresAttention       bool              `json:"requires_attention"`
	AttentionReason         string            `json:"attention_reason,omitempty"`
	Version                 int               `json:"version"`
}

// OpsChangeoverDepartingCard represents Stage 1 (Guest departing) on the Daily Changeover Board.
type OpsChangeoverDepartingCard struct {
	ReservationID        uuid.UUID `json:"reservation_id"`
	ReservationReference string    `json:"reservation_reference"`
	GuestName            string    `json:"guest_name"`
	DepartureDate        time.Time `json:"departure_date"`
	ExpectedCheckOutTime string    `json:"expected_checkout_time"`
	UnitID               uuid.UUID `json:"unit_id"`
	UnitCode             string    `json:"unit_code"`
	UnitName             string    `json:"unit_name"`
	UnitTypeName         string    `json:"unit_type_name"`
	SameDayArrival       bool      `json:"same_day_arrival"`
	NextArrivalGuest     string    `json:"next_arrival_guest,omitempty"`
	NextArrivalRef       string    `json:"next_arrival_ref,omitempty"`
	BalanceDuePence      int64     `json:"balance_due_pence"`
	Currency             string    `json:"currency"`
	LateDeparture        bool      `json:"late_departure"`
	ReservationVersion   int       `json:"reservation_version"`
}

// OpsReadyUnitCard represents Stage 5 (Ready for guests) on the Daily Changeover Board.
type OpsReadyUnitCard struct {
	UnitID               uuid.UUID  `json:"unit_id"`
	UnitCode             string     `json:"unit_code"`
	UnitName             string     `json:"unit_name"`
	UnitTypeName         string     `json:"unit_type_name"`
	UnitStatus           string     `json:"unit_status"`
	TaskID               *uuid.UUID `json:"task_id,omitempty"`
	ReservationReference string     `json:"reservation_reference,omitempty"`
	InspectedAt          *time.Time `json:"inspected_at,omitempty"`
	InspectedByName      string     `json:"inspected_by_name,omitempty"`
	CompletedAt          *time.Time `json:"completed_at,omitempty"`
	SameDayArrival       bool       `json:"same_day_arrival"`
	NextArrivalGuest     string     `json:"next_arrival_guest,omitempty"`
	HasActiveBlock       bool       `json:"has_active_block"`
	BlockSummary         string     `json:"block_summary,omitempty"`
}

// OpsChangeoverBoard organizes units and tasks across the 5-stage operational workflow.
type OpsChangeoverBoard struct {
	GuestDepartingCount     int                          `json:"guest_departing_count"`
	CleaningRequiredCount   int                          `json:"cleaning_required_count"`
	CleaningInProgressCount int                          `json:"cleaning_in_progress_count"`
	InspectionRequiredCount int                          `json:"inspection_required_count"`
	ReadyForGuestsCount     int                          `json:"ready_for_guests_count"`
	OverdueCount            int                          `json:"overdue_count"`
	GuestDeparting          []OpsChangeoverDepartingCard `json:"guest_departing"`
	CleaningRequired        []HousekeepingTask           `json:"cleaning_required"`
	CleaningInProgress      []HousekeepingTask           `json:"cleaning_in_progress"`
	InspectionRequired      []HousekeepingTask           `json:"inspection_required"`
	ReadyForGuests          []OpsReadyUnitCard           `json:"ready_for_guests"`
}

// OpsUnitTypeOccupancy breaks down today's physical occupancy and readiness by accommodation type.
type OpsUnitTypeOccupancy struct {
	UnitTypeID      uuid.UUID `json:"unit_type_id"`
	UnitTypeCode    string    `json:"unit_type_code"`
	UnitTypeName    string    `json:"unit_type_name"`
	Category        string    `json:"category"`
	TotalUnits      int       `json:"total_units"`
	OccupiedUnits   int       `json:"occupied_units"`
	BlockedUnits    int       `json:"blocked_units"`
	NeedingCleaning int       `json:"needing_cleaning"`
	AvailableUnits  int       `json:"available_units"`
}

// OpsUnitSnapshot gives the live per-unit state linking stay occupancy, housekeeping readiness,
// and independent maintenance/availability restrictions.
type OpsUnitSnapshot struct {
	UnitID              uuid.UUID       `json:"unit_id"`
	UnitCode            string          `json:"unit_code"`
	UnitName            string          `json:"unit_name"`
	UnitTypeName        string          `json:"unit_type_name"`
	Zone                string          `json:"zone"`
	UnitStatus          string          `json:"unit_status"`
	HousekeepingStage   ChangeoverStage `json:"housekeeping_stage"`
	OccupancyState      string          `json:"occupancy_state"` // CHECKED_IN, ARRIVING, RESERVED, BLOCKED, AVAILABLE
	StayReference       string          `json:"stay_reference,omitempty"`
	StayReservationID   *uuid.UUID      `json:"stay_reservation_id,omitempty"`
	StayGuestName       string          `json:"stay_guest_name,omitempty"`
	HasMaintenanceBlock bool            `json:"has_maintenance_block"`
	BlockReason         string          `json:"block_reason,omitempty"`
	BlockCategory       string          `json:"block_category,omitempty"`
}

// OpsOccupancySummary captures daily physical campsite capacity and unit breakdown.
type OpsOccupancySummary struct {
	TotalUnits      int                    `json:"total_units"`
	OccupiedUnits   int                    `json:"occupied_units"`
	CheckedInUnits  int                    `json:"checked_in_units"`
	ArrivalsToday   int                    `json:"arrivals_today"`
	DeparturesToday int                    `json:"departures_today"`
	BlockedUnits    int                    `json:"blocked_units"`
	AvailableUnits  int                    `json:"available_units"`
	ReadyUnits      int                    `json:"ready_units"`
	UnreadyUnits    int                    `json:"unready_units"`
	ByUnitType      []OpsUnitTypeOccupancy `json:"by_unit_type"`
	Units           []OpsUnitSnapshot      `json:"units"`
}

// OpsAttentionSeverity prioritizes immediate operational alerts.
type OpsAttentionSeverity string

const (
	SeverityUrgent OpsAttentionSeverity = "URGENT"
	SeverityHigh   OpsAttentionSeverity = "HIGH"
	SeverityMedium OpsAttentionSeverity = "MEDIUM"
)

// OpsAttentionItem highlights an immediate actionable operational issue.
type OpsAttentionItem struct {
	Severity      OpsAttentionSeverity `json:"severity"`
	Category      string               `json:"category"`
	Title         string               `json:"title"`
	Detail        string               `json:"detail"`
	ActionLabel   string               `json:"action_label"`
	ActionURL     string               `json:"action_url"`
	ReservationID *uuid.UUID           `json:"reservation_id,omitempty"`
	GuestID       *uuid.UUID           `json:"guest_id,omitempty"`
	EnquiryID     *uuid.UUID           `json:"enquiry_id,omitempty"`
	UnitID        *uuid.UUID           `json:"unit_id,omitempty"`
	TaskID        *uuid.UUID           `json:"task_id,omitempty"`
	AmountPence   int64                `json:"amount_pence,omitempty"`
	Currency      string               `json:"currency,omitempty"`
}

// OpsStaffMember represents an assignable active staff user on the Operations Centre.
type OpsStaffMember struct {
	ID       uuid.UUID `json:"id"`
	FullName string    `json:"full_name"`
	Role     Role      `json:"role"`
}

// OpsDailySummary answers "what is happening today" at a glance.
type OpsDailySummary struct {
	InHouseStays           int `json:"in_house_stays"`
	ArrivalsTotal          int `json:"arrivals_total"`
	ArrivalsPendingCheckIn int `json:"arrivals_pending_check_in"`
	ArrivalsCheckedIn      int `json:"arrivals_checked_in"`
	PendingArrivalsCount   int `json:"pending_arrivals_count"`
	InquiriesCount         int `json:"inquiries_count"`
	DeparturesTotal        int `json:"departures_total"`
	DeparturesPendingOut   int `json:"departures_pending_out"`
	DeparturesCheckedOut   int `json:"departures_checked_out"`
	GuestsNeedingAttention int `json:"guests_needing_attention"`
	TasksOpenCount         int `json:"tasks_open_count"`
	TasksOverdueCount      int `json:"tasks_overdue_count"`
	AttentionCount         int `json:"attention_count"`
	UrgentAttentionCount   int `json:"urgent_attention_count"`
}

// OperationsCentreData is the complete real-time Operations Centre payload for a given operational date.
type OperationsCentreData struct {
	Property        Property              `json:"property"`
	OperationalDate time.Time             `json:"operational_date"`
	TodayDate       time.Time             `json:"today_date"`
	IsToday         bool                  `json:"is_today"`
	LocalTimeLabel  string                `json:"local_time_label"`
	Focus           OpsFocus              `json:"focus"`
	Summary         OpsDailySummary       `json:"summary"`
	Arrivals        []OpsStayItem         `json:"arrivals"`
	PendingArrivals []OpsStayItem         `json:"pending_arrivals"`
	Inquiries       []OpsStayItem         `json:"inquiries"`
	Departures      []OpsStayItem         `json:"departures"`
	InHouse         []OpsStayItem         `json:"in_house"`
	AttentionGuests []OpsStayItem         `json:"attention_guests"`
	AttentionItems  []OpsAttentionItem    `json:"attention_items"`
	ChangeoverBoard OpsChangeoverBoard    `json:"changeover_board"`
	Occupancy       OpsOccupancySummary   `json:"occupancy"`
	Maintenance     OpsMaintenanceSummary `json:"maintenance"`
	GuestCRM        OpsGuestCRMSummary    `json:"guest_crm"`
	StaffMembers    []OpsStaffMember      `json:"staff_members"`
}

// OpsGuestCRMSummary summarizes guest enquiries, follow-ups, complaints, and comms needing attention.
type OpsGuestCRMSummary struct {
	Visible                  bool `json:"visible"`
	NewEnquiriesCount        int  `json:"new_enquiries_count"`
	UnassignedEnquiriesCount int  `json:"unassigned_enquiries_count"`
	OverdueFollowUpsCount    int  `json:"overdue_follow_ups_count"`
	UnresolvedComplaints     int  `json:"unresolved_complaints"`
	UpcomingArrivalRequests  int  `json:"upcoming_arrival_requests"`
	FailedEmailsCount        int  `json:"failed_emails_count"`
	ScheduledAutoEmailsCount int  `json:"scheduled_auto_emails_count"`
}
