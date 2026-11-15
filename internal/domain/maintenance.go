package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type MaintenancePriority string

const (
	MaintPriorityLow    MaintenancePriority = "LOW"
	MaintPriorityNormal MaintenancePriority = "NORMAL"
	MaintPriorityHigh   MaintenancePriority = "HIGH"
	MaintPriorityUrgent MaintenancePriority = "URGENT"
)

func (p MaintenancePriority) Valid() bool {
	switch p {
	case MaintPriorityLow, MaintPriorityNormal, MaintPriorityHigh, MaintPriorityUrgent:
		return true
	default:
		return false
	}
}

func (p MaintenancePriority) Description() string {
	switch p {
	case MaintPriorityLow:
		return "Cosmetic damage or non-urgent improvement"
	case MaintPriorityNormal:
		return "Ordinary repair which should be scheduled"
	case MaintPriorityHigh:
		return "Issue affecting guest comfort or operational capacity"
	case MaintPriorityUrgent:
		return "Immediate safety issue, major utility failure, or serious guest impact"
	default:
		return string(p)
	}
}

type MaintenanceStatus string

const (
	MaintStatusReported      MaintenanceStatus = "REPORTED"
	MaintStatusTriaged       MaintenanceStatus = "TRIAGED"
	MaintStatusScheduled     MaintenanceStatus = "SCHEDULED"
	MaintStatusInProgress    MaintenanceStatus = "IN_PROGRESS"
	MaintStatusAwaitingParts MaintenanceStatus = "AWAITING_PARTS"
	MaintStatusOnHold        MaintenanceStatus = "ON_HOLD"
	MaintStatusCompleted     MaintenanceStatus = "COMPLETED"
	MaintStatusCancelled     MaintenanceStatus = "CANCELLED"
	MaintStatusClosed        MaintenanceStatus = "CLOSED"
)

func (s MaintenanceStatus) Valid() bool {
	switch s {
	case MaintStatusReported, MaintStatusTriaged, MaintStatusScheduled,
		MaintStatusInProgress, MaintStatusAwaitingParts, MaintStatusOnHold,
		MaintStatusCompleted, MaintStatusCancelled, MaintStatusClosed:
		return true
	default:
		return false
	}
}

func (s MaintenanceStatus) IsOpen() bool {
	switch s {
	case MaintStatusCompleted, MaintStatusCancelled, MaintStatusClosed:
		return false
	default:
		return true
	}
}

func (s MaintenanceStatus) Label() string {
	switch s {
	case MaintStatusReported:
		return "Reported"
	case MaintStatusTriaged:
		return "Triaged"
	case MaintStatusScheduled:
		return "Scheduled"
	case MaintStatusInProgress:
		return "In Progress"
	case MaintStatusAwaitingParts:
		return "Awaiting Parts"
	case MaintStatusOnHold:
		return "On Hold"
	case MaintStatusCompleted:
		return "Completed"
	case MaintStatusCancelled:
		return "Cancelled"
	case MaintStatusClosed:
		return "Closed"
	default:
		return string(s)
	}
}

var allowedMaintenanceTransitions = map[MaintenanceStatus][]MaintenanceStatus{
	MaintStatusReported:      {MaintStatusTriaged, MaintStatusScheduled, MaintStatusInProgress, MaintStatusOnHold, MaintStatusCancelled},
	MaintStatusTriaged:       {MaintStatusScheduled, MaintStatusInProgress, MaintStatusAwaitingParts, MaintStatusOnHold, MaintStatusCancelled},
	MaintStatusScheduled:     {MaintStatusInProgress, MaintStatusAwaitingParts, MaintStatusOnHold, MaintStatusCancelled},
	MaintStatusInProgress:    {MaintStatusAwaitingParts, MaintStatusOnHold, MaintStatusCompleted, MaintStatusCancelled},
	MaintStatusAwaitingParts: {MaintStatusInProgress, MaintStatusScheduled, MaintStatusOnHold, MaintStatusCancelled},
	MaintStatusOnHold:        {MaintStatusTriaged, MaintStatusScheduled, MaintStatusInProgress, MaintStatusCancelled},
	MaintStatusCompleted:     {MaintStatusClosed, MaintStatusInProgress, MaintStatusTriaged},
	MaintStatusClosed:        {MaintStatusTriaged, MaintStatusInProgress},
	MaintStatusCancelled:     {MaintStatusReported, MaintStatusTriaged},
}

func (s MaintenanceStatus) CanTransitionTo(next MaintenanceStatus) bool {
	for _, candidate := range allowedMaintenanceTransitions[s] {
		if candidate == next {
			return true
		}
	}
	return false
}

func (s MaintenanceStatus) AllowedNextStatuses() []MaintenanceStatus {
	src := allowedMaintenanceTransitions[s]
	out := make([]MaintenanceStatus, len(src))
	copy(out, src)
	return out
}

type MaintenanceWorkType string

const (
	MaintWorkReactive     MaintenanceWorkType = "REACTIVE"
	MaintWorkPreventative MaintenanceWorkType = "PREVENTATIVE"
)

func (w MaintenanceWorkType) Valid() bool {
	return w == MaintWorkReactive || w == MaintWorkPreventative
}

type MaintenanceAvailabilityImpact string

const (
	MaintImpactNone        MaintenanceAvailabilityImpact = "NONE"
	MaintImpactPartial     MaintenanceAvailabilityImpact = "PARTIAL"
	MaintImpactUnavailable MaintenanceAvailabilityImpact = "UNAVAILABLE"
)

func (i MaintenanceAvailabilityImpact) Valid() bool {
	return i == MaintImpactNone || i == MaintImpactPartial || i == MaintImpactUnavailable
}

type MaintenanceWorkEntryType string

const (
	WorkEntryStarted        MaintenanceWorkEntryType = "WORK_STARTED"
	WorkEntryDiagnosis      MaintenanceWorkEntryType = "DIAGNOSIS"
	WorkEntryPerformed      MaintenanceWorkEntryType = "WORK_PERFORMED"
	WorkEntryPartsUsed      MaintenanceWorkEntryType = "PARTS_USED"
	WorkEntryAdditionalWork MaintenanceWorkEntryType = "ADDITIONAL_WORK"
	WorkEntryDelayReason    MaintenanceWorkEntryType = "DELAY_REASON"
	WorkEntryContractorNote MaintenanceWorkEntryType = "CONTRACTOR_NOTE"
	WorkEntryCompletionNote MaintenanceWorkEntryType = "COMPLETION_NOTE"
	WorkEntryGeneralNote    MaintenanceWorkEntryType = "GENERAL_NOTE"
	WorkEntryReopened       MaintenanceWorkEntryType = "REOPENED"
)

func (t MaintenanceWorkEntryType) Valid() bool {
	switch t {
	case WorkEntryStarted, WorkEntryDiagnosis, WorkEntryPerformed, WorkEntryPartsUsed,
		WorkEntryAdditionalWork, WorkEntryDelayReason, WorkEntryContractorNote,
		WorkEntryCompletionNote, WorkEntryGeneralNote, WorkEntryReopened:
		return true
	default:
		return false
	}
}

type MaintenanceInspectionResult string

const (
	MaintInspectionPassed MaintenanceInspectionResult = "PASSED"
	MaintInspectionFailed MaintenanceInspectionResult = "FAILED"
)

func (r MaintenanceInspectionResult) Valid() bool {
	return r == MaintInspectionPassed || r == MaintInspectionFailed
}

type MaintenanceFrequency string

const (
	MaintFreqOnce       MaintenanceFrequency = "ONCE"
	MaintFreqWeekly     MaintenanceFrequency = "WEEKLY"
	MaintFreqMonthly    MaintenanceFrequency = "MONTHLY"
	MaintFreqQuarterly  MaintenanceFrequency = "QUARTERLY"
	MaintFreqSixMonthly MaintenanceFrequency = "SIX_MONTHLY"
	MaintFreqAnnually   MaintenanceFrequency = "ANNUALLY"
)

func (f MaintenanceFrequency) Valid() bool {
	switch f {
	case MaintFreqOnce, MaintFreqWeekly, MaintFreqMonthly, MaintFreqQuarterly, MaintFreqSixMonthly, MaintFreqAnnually:
		return true
	default:
		return false
	}
}

// AdvanceDueDate calculates the next scheduled date for a preventative rule using
// calendar-aware stepping and clamping to the target month's final day.
// Returns (nextDate, true) for recurring frequencies or (zero, false) for ONCE.
func (f MaintenanceFrequency) AdvanceDueDate(from time.Time) (time.Time, bool) {
	return f.AdvanceDueDateWithAnchor(from, from.Day())
}

// AdvanceDueDateWithAnchor calculates the next scheduled date for a preventative rule
// while preserving anchorDay (1..31) across shorter months when possible.
func (f MaintenanceFrequency) AdvanceDueDateWithAnchor(from time.Time, anchorDay int) (time.Time, bool) {
	base := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	if anchorDay < 1 || anchorDay > 31 {
		anchorDay = base.Day()
	}
	switch f {
	case MaintFreqWeekly:
		return base.AddDate(0, 0, 7), true
	case MaintFreqMonthly:
		return addMonthsClamped(base, 1, anchorDay), true
	case MaintFreqQuarterly:
		return addMonthsClamped(base, 3, anchorDay), true
	case MaintFreqSixMonthly:
		return addMonthsClamped(base, 6, anchorDay), true
	case MaintFreqAnnually:
		return addMonthsClamped(base, 12, anchorDay), true
	default:
		return time.Time{}, false
	}
}

// ResolveDueCatchUp consolidates any overdue recurring occurrences through asOf into
// a single aligned catch-up occurrence date and computes the next future due date (> asOf).
// Historical requests are never mutated; skippedIntervals reports how many older due
// intervals were collapsed into this catch-up cycle.
func (f MaintenanceFrequency) ResolveDueCatchUp(nextDue, asOf time.Time, anchorDay int) (occDate time.Time, nextFuture *time.Time, skippedIntervals int, due bool) {
	cur := time.Date(nextDue.Year(), nextDue.Month(), nextDue.Day(), 0, 0, 0, 0, time.UTC)
	asOfDate := time.Date(asOf.Year(), asOf.Month(), asOf.Day(), 0, 0, 0, 0, time.UTC)
	if cur.IsZero() || cur.After(asOfDate) {
		return time.Time{}, nil, 0, false
	}
	if f == MaintFreqOnce {
		return cur, nil, 0, true
	}
	if anchorDay < 1 || anchorDay > 31 {
		anchorDay = cur.Day()
	}
	const maxCatchUpSteps = 100000
	for i := 0; i < maxCatchUpSteps; i++ {
		next, ok := f.AdvanceDueDateWithAnchor(cur, anchorDay)
		if !ok || !next.After(cur) {
			return cur, nil, skippedIntervals, true
		}
		if next.After(asOfDate) {
			n := next
			return cur, &n, skippedIntervals, true
		}
		cur = next
		skippedIntervals++
	}
	n, ok := f.AdvanceDueDateWithAnchor(cur, anchorDay)
	if !ok {
		return cur, nil, skippedIntervals, true
	}
	return cur, &n, skippedIntervals, true
}

func addMonthsClamped(base time.Time, months int, anchorDay int) time.Time {
	totalMonths := int(base.Month()) - 1 + months
	targetYear := base.Year() + totalMonths/12
	targetMonthIdx := totalMonths % 12
	if targetMonthIdx < 0 {
		targetMonthIdx += 12
		targetYear--
	}
	targetMonth := time.Month(targetMonthIdx + 1)
	maxDay := time.Date(targetYear, targetMonth+1, 0, 0, 0, 0, 0, time.UTC).Day()
	day := anchorDay
	if day > maxDay {
		day = maxDay
	}
	return time.Date(targetYear, targetMonth, day, 0, 0, 0, 0, time.UTC)
}

type MaintenanceAssetStatus string

const (
	AssetStatusActive  MaintenanceAssetStatus = "ACTIVE"
	AssetStatusRetired MaintenanceAssetStatus = "RETIRED"
)

func (s MaintenanceAssetStatus) Valid() bool {
	return s == AssetStatusActive || s == AssetStatusRetired
}

type MaintenanceCategory struct {
	ID          uuid.UUID `json:"id"`
	PropertyID  uuid.UUID `json:"property_id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	IsActive    bool      `json:"is_active"`
	SortOrder   int       `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (c *MaintenanceCategory) Validate() error {
	errs := map[string]string{}
	c.Code = strings.ToUpper(strings.TrimSpace(c.Code))
	c.Name = strings.TrimSpace(c.Name)
	c.Description = strings.TrimSpace(c.Description)
	if c.PropertyID == uuid.Nil {
		errs["property_id"] = "is required"
	}
	if !codeRegex.MatchString(strings.ReplaceAll(c.Code, "_", "-")) {
		errs["code"] = "must use letters, numbers, underscores, or hyphens"
	}
	if c.Name == "" {
		errs["name"] = "is required"
	}
	if len(errs) > 0 {
		return NewValidationError(errs)
	}
	return nil
}

type MaintenanceLocation struct {
	ID           uuid.UUID  `json:"id"`
	PropertyID   uuid.UUID  `json:"property_id"`
	Code         string     `json:"code"`
	Name         string     `json:"name"`
	LocationType string     `json:"location_type"`
	Description  string     `json:"description"`
	UnitID       *uuid.UUID `json:"unit_id,omitempty"`
	UnitCode     string     `json:"unit_code,omitempty"`
	UnitName     string     `json:"unit_name,omitempty"`
	IsActive     bool       `json:"is_active"`
	SortOrder    int        `json:"sort_order"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func (l *MaintenanceLocation) Validate() error {
	errs := map[string]string{}
	l.Code = strings.ToUpper(strings.TrimSpace(l.Code))
	l.Name = strings.TrimSpace(l.Name)
	l.LocationType = strings.TrimSpace(l.LocationType)
	l.Description = strings.TrimSpace(l.Description)
	if l.PropertyID == uuid.Nil {
		errs["property_id"] = "is required"
	}
	if !codeRegex.MatchString(strings.ReplaceAll(l.Code, "_", "-")) {
		errs["code"] = "must use letters, numbers, underscores, or hyphens"
	}
	if l.Name == "" {
		errs["name"] = "is required"
	}
	if l.LocationType == "" {
		l.LocationType = "OTHER"
	}
	if len(errs) > 0 {
		return NewValidationError(errs)
	}
	return nil
}

type MaintenanceAsset struct {
	ID                uuid.UUID              `json:"id"`
	PropertyID        uuid.UUID              `json:"property_id"`
	AssetCode         string                 `json:"asset_code"`
	Name              string                 `json:"name"`
	Description       string                 `json:"description"`
	LocationID        *uuid.UUID             `json:"location_id,omitempty"`
	LocationName      string                 `json:"location_name,omitempty"`
	UnitID            *uuid.UUID             `json:"unit_id,omitempty"`
	UnitCode          string                 `json:"unit_code,omitempty"`
	UnitName          string                 `json:"unit_name,omitempty"`
	AssetCategory     string                 `json:"asset_category"`
	Manufacturer      string                 `json:"manufacturer"`
	Model             string                 `json:"model"`
	SerialNumber      string                 `json:"serial_number"`
	InstalledOn       *time.Time             `json:"installed_on,omitempty"`
	WarrantyExpiresOn *time.Time             `json:"warranty_expires_on,omitempty"`
	WarrantyNotes     string                 `json:"warranty_notes"`
	Status            MaintenanceAssetStatus `json:"status"`
	Notes             string                 `json:"notes"`
	Version           int                    `json:"version"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

func (a *MaintenanceAsset) Validate() error {
	errs := map[string]string{}
	a.AssetCode = strings.ToUpper(strings.TrimSpace(a.AssetCode))
	a.Name = strings.TrimSpace(a.Name)
	a.AssetCategory = strings.TrimSpace(a.AssetCategory)
	a.Description = strings.TrimSpace(a.Description)
	a.Manufacturer = strings.TrimSpace(a.Manufacturer)
	a.Model = strings.TrimSpace(a.Model)
	a.SerialNumber = strings.TrimSpace(a.SerialNumber)
	a.WarrantyNotes = strings.TrimSpace(a.WarrantyNotes)
	a.Notes = strings.TrimSpace(a.Notes)
	if a.PropertyID == uuid.Nil {
		errs["property_id"] = "is required"
	}
	if !codeRegex.MatchString(strings.ReplaceAll(a.AssetCode, "_", "-")) {
		errs["asset_code"] = "must use letters, numbers, underscores, or hyphens"
	}
	if a.Name == "" {
		errs["name"] = "is required"
	}
	if a.AssetCategory == "" {
		errs["asset_category"] = "is required"
	}
	if a.Status == "" {
		a.Status = AssetStatusActive
	} else if !a.Status.Valid() {
		errs["status"] = "invalid asset status"
	}
	if len(errs) > 0 {
		return NewValidationError(errs)
	}
	return nil
}

type MaintenanceContractor struct {
	ID         uuid.UUID `json:"id"`
	PropertyID uuid.UUID `json:"property_id"`
	Name       string    `json:"name"`
	Company    string    `json:"company"`
	Trade      string    `json:"trade"`
	Email      string    `json:"email"`
	Phone      string    `json:"phone"`
	Notes      string    `json:"notes"`
	IsActive   bool      `json:"is_active"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (c *MaintenanceContractor) Validate() error {
	errs := map[string]string{}
	c.Name = strings.TrimSpace(c.Name)
	c.Company = strings.TrimSpace(c.Company)
	c.Trade = strings.TrimSpace(c.Trade)
	c.Email = strings.TrimSpace(c.Email)
	c.Phone = strings.TrimSpace(c.Phone)
	c.Notes = strings.TrimSpace(c.Notes)
	if c.PropertyID == uuid.Nil {
		errs["property_id"] = "is required"
	}
	if c.Name == "" {
		errs["name"] = "is required"
	}
	if len(errs) > 0 {
		return NewValidationError(errs)
	}
	return nil
}

type MaintenanceRequest struct {
	ID                      uuid.UUID                     `json:"id"`
	PropertyID              uuid.UUID                     `json:"property_id"`
	Reference               string                        `json:"reference"`
	Title                   string                        `json:"title"`
	Description             string                        `json:"description"`
	CategoryID              uuid.UUID                     `json:"category_id"`
	CategoryName            string                        `json:"category_name"`
	CategoryCode            string                        `json:"category_code,omitempty"`
	Priority                MaintenancePriority           `json:"priority"`
	Status                  MaintenanceStatus             `json:"status"`
	WorkType                MaintenanceWorkType           `json:"work_type"`
	LocationID              *uuid.UUID                    `json:"location_id,omitempty"`
	LocationName            string                        `json:"location_name,omitempty"`
	UnitID                  *uuid.UUID                    `json:"unit_id,omitempty"`
	UnitCode                string                        `json:"unit_code,omitempty"`
	UnitName                string                        `json:"unit_name,omitempty"`
	AssetID                 *uuid.UUID                    `json:"asset_id,omitempty"`
	AssetCode               string                        `json:"asset_code,omitempty"`
	AssetName               string                        `json:"asset_name,omitempty"`
	ScheduleID              *uuid.UUID                    `json:"schedule_id,omitempty"`
	ScheduledOccurrenceDate *time.Time                    `json:"scheduled_occurrence_date,omitempty"`
	ReportedBy              *uuid.UUID                    `json:"reported_by,omitempty"`
	ReporterName            string                        `json:"reporter_name,omitempty"`
	AssignedUserID          *uuid.UUID                    `json:"assigned_user_id,omitempty"`
	AssignedUserName        string                        `json:"assigned_user_name,omitempty"`
	AssignedContractorID    *uuid.UUID                    `json:"assigned_contractor_id,omitempty"`
	AssignedContractorName  string                        `json:"assigned_contractor_name,omitempty"`
	ReportedAt              time.Time                     `json:"reported_at"`
	TargetDate              *time.Time                    `json:"target_date,omitempty"`
	ScheduledFor            *time.Time                    `json:"scheduled_for,omitempty"`
	StartedAt               *time.Time                    `json:"started_at,omitempty"`
	CompletedAt             *time.Time                    `json:"completed_at,omitempty"`
	ClosedAt                *time.Time                    `json:"closed_at,omitempty"`
	GuestImpact             bool                          `json:"guest_impact"`
	SafetyImpact            bool                          `json:"safety_impact"`
	AvailabilityImpact      MaintenanceAvailabilityImpact `json:"availability_impact"`
	InspectionRequired      bool                          `json:"inspection_required"`
	InternalNotes           string                        `json:"internal_notes,omitempty"`
	Overdue                 bool                          `json:"overdue"`
	HasActiveRestriction    bool                          `json:"has_active_restriction"`
	LatestInspectionResult  string                        `json:"latest_inspection_result,omitempty"`
	Version                 int                           `json:"version"`
	CreatedAt               time.Time                     `json:"created_at"`
	UpdatedAt               time.Time                     `json:"updated_at"`
}

func (r *MaintenanceRequest) Validate() error {
	errs := map[string]string{}
	r.Title = strings.TrimSpace(r.Title)
	r.Description = strings.TrimSpace(r.Description)
	r.InternalNotes = strings.TrimSpace(r.InternalNotes)
	if r.PropertyID == uuid.Nil {
		errs["property_id"] = "is required"
	}
	if r.Title == "" {
		errs["title"] = "is required"
	}
	if r.CategoryID == uuid.Nil {
		errs["category_id"] = "is required"
	}
	if r.Priority == "" {
		r.Priority = MaintPriorityNormal
	} else if !r.Priority.Valid() {
		errs["priority"] = "must be LOW, NORMAL, HIGH, or URGENT"
	}
	if r.Status == "" {
		r.Status = MaintStatusReported
	} else if !r.Status.Valid() {
		errs["status"] = "invalid maintenance status"
	}
	if r.WorkType == "" {
		r.WorkType = MaintWorkReactive
	} else if !r.WorkType.Valid() {
		errs["work_type"] = "must be REACTIVE or PREVENTATIVE"
	}
	if r.AvailabilityImpact == "" {
		r.AvailabilityImpact = MaintImpactNone
	} else if !r.AvailabilityImpact.Valid() {
		errs["availability_impact"] = "must be NONE, PARTIAL, or UNAVAILABLE"
	}
	if r.AssignedUserID != nil && *r.AssignedUserID == uuid.Nil {
		r.AssignedUserID = nil
	}
	if r.AssignedContractorID != nil && *r.AssignedContractorID == uuid.Nil {
		r.AssignedContractorID = nil
	}
	if r.LocationID != nil && *r.LocationID == uuid.Nil {
		r.LocationID = nil
	}
	if r.UnitID != nil && *r.UnitID == uuid.Nil {
		r.UnitID = nil
	}
	if r.AssetID != nil && *r.AssetID == uuid.Nil {
		r.AssetID = nil
	}
	if r.LocationID == nil && r.UnitID == nil && r.AssetID == nil {
		errs["location"] = "select a location, accommodation unit, or asset"
	}
	if len(errs) > 0 {
		return NewValidationError(errs)
	}
	return nil
}

func (r MaintenanceRequest) LocationLabel() string {
	parts := make([]string, 0, 3)
	if r.UnitCode != "" {
		parts = append(parts, fmt.Sprintf("%s (%s)", r.UnitCode, r.UnitName))
	}
	if r.LocationName != "" {
		parts = append(parts, r.LocationName)
	}
	if r.AssetName != "" {
		parts = append(parts, fmt.Sprintf("[%s] %s", r.AssetCode, r.AssetName))
	}
	if len(parts) == 0 {
		return "Property-wide"
	}
	return strings.Join(parts, " · ")
}

func (r MaintenanceRequest) AssigneeLabel() string {
	switch {
	case r.AssignedUserName != "" && r.AssignedContractorName != "":
		return r.AssignedUserName + " / " + r.AssignedContractorName
	case r.AssignedUserName != "":
		return r.AssignedUserName
	case r.AssignedContractorName != "":
		return "Contractor: " + r.AssignedContractorName
	default:
		return "Unassigned"
	}
}

type MaintenanceAssignment struct {
	ID                     uuid.UUID  `json:"id"`
	PropertyID             uuid.UUID  `json:"property_id"`
	RequestID              uuid.UUID  `json:"request_id"`
	AssignedUserID         *uuid.UUID `json:"assigned_user_id,omitempty"`
	AssignedUserName       string     `json:"assigned_user_name,omitempty"`
	AssignedContractorID   *uuid.UUID `json:"assigned_contractor_id,omitempty"`
	AssignedContractorName string     `json:"assigned_contractor_name,omitempty"`
	AssignedBy             *uuid.UUID `json:"assigned_by,omitempty"`
	AssignedByName         string     `json:"assigned_by_name,omitempty"`
	DueDate                *time.Time `json:"due_date,omitempty"`
	Notes                  string     `json:"notes"`
	CreatedAt              time.Time  `json:"created_at"`
}

type MaintenanceWorkEntry struct {
	ID         uuid.UUID                `json:"id"`
	PropertyID uuid.UUID                `json:"property_id"`
	RequestID  uuid.UUID                `json:"request_id"`
	EntryType  MaintenanceWorkEntryType `json:"entry_type"`
	Body       string                   `json:"body"`
	PartsUsed  string                   `json:"parts_used,omitempty"`
	AuthorID   *uuid.UUID               `json:"author_id,omitempty"`
	AuthorName string                   `json:"author_name,omitempty"`
	CreatedAt  time.Time                `json:"created_at"`
}

type MaintenanceInspection struct {
	ID            uuid.UUID                   `json:"id"`
	PropertyID    uuid.UUID                   `json:"property_id"`
	RequestID     uuid.UUID                   `json:"request_id"`
	InspectorID   *uuid.UUID                  `json:"inspector_id,omitempty"`
	InspectorName string                      `json:"inspector_name,omitempty"`
	Result        MaintenanceInspectionResult `json:"result"`
	Notes         string                      `json:"notes"`
	InspectedAt   time.Time                   `json:"inspected_at"`
}

type MaintenanceStatusHistory struct {
	ID            uuid.UUID  `json:"id"`
	PropertyID    uuid.UUID  `json:"property_id"`
	RequestID     uuid.UUID  `json:"request_id"`
	FromStatus    string     `json:"from_status"`
	ToStatus      string     `json:"to_status"`
	Reason        string     `json:"reason"`
	ChangedBy     *uuid.UUID `json:"changed_by,omitempty"`
	ChangedByName string     `json:"changed_by_name,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

type MaintenanceAttachment struct {
	ID               uuid.UUID  `json:"id"`
	PropertyID       uuid.UUID  `json:"property_id"`
	RequestID        uuid.UUID  `json:"request_id"`
	WorkEntryID      *uuid.UUID `json:"work_entry_id,omitempty"`
	StorageKey       string     `json:"-"`
	OriginalFilename string     `json:"original_filename"`
	ContentType      string     `json:"content_type"`
	SizeBytes        int64      `json:"size_bytes"`
	SHA256           string     `json:"sha256"`
	Caption          string     `json:"caption"`
	UploadedBy       *uuid.UUID `json:"uploaded_by,omitempty"`
	UploaderName     string     `json:"uploader_name,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

type MaintenanceRestrictionLink struct {
	ID              uuid.UUID  `json:"id"`
	PropertyID      uuid.UUID  `json:"property_id"`
	RequestID       uuid.UUID  `json:"request_id"`
	UnitID          uuid.UUID  `json:"unit_id"`
	UnitCode        string     `json:"unit_code"`
	UnitName        string     `json:"unit_name"`
	BlockoutGroupID uuid.UUID  `json:"blockout_group_id"`
	GroupStatus     string     `json:"group_status"`
	StartDate       time.Time  `json:"start_date"`
	EndDate         time.Time  `json:"end_date"`
	Reason          string     `json:"reason"`
	GroupVersion    int        `json:"group_version"`
	CreatedBy       *uuid.UUID `json:"created_by,omitempty"`
	CreatedByName   string     `json:"created_by_name,omitempty"`
	ReleasedBy      *uuid.UUID `json:"released_by,omitempty"`
	ReleasedByName  string     `json:"released_by_name,omitempty"`
	ReleasedAt      *time.Time `json:"released_at,omitempty"`
	Notes           string     `json:"notes"`
	CreatedAt       time.Time  `json:"created_at"`
}

type MaintenanceSchedule struct {
	ID                     uuid.UUID            `json:"id"`
	PropertyID             uuid.UUID            `json:"property_id"`
	Title                  string               `json:"title"`
	Description            string               `json:"description"`
	CategoryID             uuid.UUID            `json:"category_id"`
	CategoryName           string               `json:"category_name,omitempty"`
	LocationID             *uuid.UUID           `json:"location_id,omitempty"`
	LocationName           string               `json:"location_name,omitempty"`
	UnitID                 *uuid.UUID           `json:"unit_id,omitempty"`
	UnitCode               string               `json:"unit_code,omitempty"`
	AssetID                *uuid.UUID           `json:"asset_id,omitempty"`
	AssetCode              string               `json:"asset_code,omitempty"`
	AssetName              string               `json:"asset_name,omitempty"`
	Priority               MaintenancePriority  `json:"priority"`
	Frequency              MaintenanceFrequency `json:"frequency"`
	NextDueDate            time.Time            `json:"next_due_date"`
	AnchorDay              int                  `json:"anchor_day,omitempty"`
	LastGeneratedForDate   *time.Time           `json:"last_generated_for_date,omitempty"`
	AssignedUserID         *uuid.UUID           `json:"assigned_user_id,omitempty"`
	AssignedUserName       string               `json:"assigned_user_name,omitempty"`
	AssignedContractorID   *uuid.UUID           `json:"assigned_contractor_id,omitempty"`
	AssignedContractorName string               `json:"assigned_contractor_name,omitempty"`
	InspectionRequired     bool                 `json:"inspection_required"`
	Notes                  string               `json:"notes"`
	IsActive               bool                 `json:"is_active"`
	Version                int                  `json:"version"`
	CreatedAt              time.Time            `json:"created_at"`
	UpdatedAt              time.Time            `json:"updated_at"`
}

func (s *MaintenanceSchedule) Validate() error {
	errs := map[string]string{}
	s.Title = strings.TrimSpace(s.Title)
	s.Description = strings.TrimSpace(s.Description)
	s.Notes = strings.TrimSpace(s.Notes)
	if s.PropertyID == uuid.Nil {
		errs["property_id"] = "is required"
	}
	if s.Title == "" {
		errs["title"] = "is required"
	}
	if s.CategoryID == uuid.Nil {
		errs["category_id"] = "is required"
	}
	if s.Priority == "" {
		s.Priority = MaintPriorityNormal
	} else if !s.Priority.Valid() {
		errs["priority"] = "must be LOW, NORMAL, HIGH, or URGENT"
	}
	if s.Frequency == "" {
		s.Frequency = MaintFreqMonthly
	} else if !s.Frequency.Valid() {
		errs["frequency"] = "invalid preventative frequency"
	}
	if s.NextDueDate.IsZero() {
		errs["next_due_date"] = "is required"
	}
	if s.LocationID == nil && s.UnitID == nil && s.AssetID == nil {
		errs["location"] = "select a location, accommodation unit, or asset"
	}
	if len(errs) > 0 {
		return NewValidationError(errs)
	}
	return nil
}

type MaintenanceRequestFilter struct {
	Status      string
	Priority    string
	CategoryID  *uuid.UUID
	LocationID  *uuid.UUID
	UnitID      *uuid.UUID
	AssetID     *uuid.UUID
	AssignedTo  *uuid.UUID
	WorkType    string
	OverdueOnly bool
	Restricted  bool
	DateFrom    *time.Time
	DateTo      *time.Time
	Search      string
	Limit       int
}

type MaintenanceRequestDetail struct {
	Request          MaintenanceRequest           `json:"request"`
	Assignments      []MaintenanceAssignment      `json:"assignments"`
	WorkEntries      []MaintenanceWorkEntry       `json:"work_entries"`
	Inspections      []MaintenanceInspection      `json:"inspections"`
	StatusHistory    []MaintenanceStatusHistory   `json:"status_history"`
	Attachments      []MaintenanceAttachment      `json:"attachments"`
	RestrictionLinks []MaintenanceRestrictionLink `json:"restriction_links"`
}

// OpsMaintenanceSummary feeds the Operations Centre and /api/v1/properties/{id}/operations/maintenance-summary.
type OpsMaintenanceSummary struct {
	OpenCount               int                  `json:"open_count"`
	UrgentCount             int                  `json:"urgent_count"`
	HighCount               int                  `json:"high_count"`
	OverdueCount            int                  `json:"overdue_count"`
	ScheduledTodayCount     int                  `json:"scheduled_today_count"`
	RestrictingUnitsCount   int                  `json:"restricting_units_count"`
	AwaitingInspectionCount int                  `json:"awaiting_inspection_count"`
	RecentlyCompletedCount  int                  `json:"recently_completed_count"`
	PriorityJobs            []MaintenanceRequest `json:"priority_jobs"`
	ScheduledToday          []MaintenanceRequest `json:"scheduled_today"`
	AwaitingInspection      []MaintenanceRequest `json:"awaiting_inspection"`
	RecentlyCompleted       []MaintenanceRequest `json:"recently_completed"`
}
