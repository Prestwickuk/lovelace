package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ClaimType identifies what holds an inventory claim on a unit.
type ClaimType string

const (
	ClaimReservation         ClaimType = "RESERVATION"
	ClaimPendingReservation  ClaimType = "PENDING_RESERVATION"
	ClaimExternalReservation ClaimType = "EXTERNAL_RESERVATION"
	ClaimOwnerUse            ClaimType = "OWNER_USE"
	ClaimMaintenance         ClaimType = "MAINTENANCE"
	ClaimSeasonalClosure     ClaimType = "SEASONAL_CLOSURE"
	ClaimWeatherClosure      ClaimType = "WEATHER_CLOSURE"
	ClaimChannelAllocation   ClaimType = "CHANNEL_ALLOCATION"
	ClaimChannelRestriction  ClaimType = "CHANNEL_RESTRICTION"
	ClaimOperationalBuffer   ClaimType = "OPERATIONAL_BUFFER"
	ClaimAdministrativeBlock ClaimType = "ADMINISTRATIVE_BLOCK"
	ClaimHold                ClaimType = "HOLD"
)

func (c ClaimType) ValidCategory() bool {
	return channelCodeRegex.MatchString(string(c))
}

// RequiresSpecificChannel returns true for channel-scoped rules (allocations and stop-sells)
// that must target a concrete channel rather than ALL.
func (c ClaimType) RequiresSpecificChannel() bool {
	return c == ClaimChannelAllocation || c == ClaimChannelRestriction
}

// ValidBlockoutCategory reports whether c is permitted for administrative bulk blockouts/restrictions.
func (c ClaimType) ValidBlockoutCategory() bool {
	switch c {
	case ClaimOwnerUse, ClaimMaintenance, ClaimSeasonalClosure, ClaimWeatherClosure,
		ClaimChannelAllocation, ClaimChannelRestriction, ClaimOperationalBuffer, ClaimAdministrativeBlock, ClaimHold:
		return true
	default:
		return false
	}
}

// BlockoutScope defines the unit selection scope for a blockout operation.
type BlockoutScope string

const (
	BlockScopeUnit     BlockoutScope = "UNIT"
	BlockScopeUnits    BlockoutScope = "UNITS"
	BlockScopeUnitType BlockoutScope = "UNIT_TYPE"
	BlockScopeProperty BlockoutScope = "PROPERTY"
)

func (s BlockoutScope) Valid() bool {
	switch s {
	case BlockScopeUnit, BlockScopeUnits, BlockScopeUnitType, BlockScopeProperty:
		return true
	default:
		return false
	}
}

// ClaimStatus represents the lifecycle state of an inventory claim.
type ClaimStatus string

const (
	ClaimStatusActive    ClaimStatus = "ACTIVE"
	ClaimStatusReleased  ClaimStatus = "RELEASED"
	ClaimStatusCompleted ClaimStatus = "COMPLETED"
	ClaimStatusConsumed  ClaimStatus = "CONSUMED"
)

// BlockoutGroupStatus constants distinguish consumption vs manual release.
const (
	BlockStatusActive            = "ACTIVE"
	BlockStatusPartiallyReleased = "PARTIALLY_RELEASED"
	BlockStatusReleased          = "RELEASED"
	BlockStatusPartiallyConsumed = "PARTIALLY_CONSUMED"
	BlockStatusConsumed          = "CONSUMED"
)

// BlockoutGroup represents an individual or bulk restriction operation.
type BlockoutGroup struct {
	ID                 uuid.UUID     `json:"id"`
	PropertyID         uuid.UUID     `json:"property_id"`
	ScopeType          BlockoutScope `json:"scope_type"`
	TargetUnitTypeID   *uuid.UUID    `json:"target_unit_type_id,omitempty"`
	Category           ClaimType     `json:"category"`
	ChannelScope       string        `json:"channel_scope"`
	StartDate          time.Time     `json:"start_date"`
	EndDate            time.Time     `json:"end_date"` // Exclusive
	Reason             string        `json:"reason"`
	ApplyToFutureUnits bool          `json:"apply_to_future_units"`
	Status             string        `json:"status"` // ACTIVE, PARTIALLY_CONSUMED, CONSUMED, PARTIALLY_RELEASED, RELEASED
	ActiveClaimCount   int           `json:"active_claim_count"`
	ConsumedClaimCount int           `json:"consumed_claim_count"`
	ReleasedClaimCount int           `json:"released_claim_count"`
	TotalClaimCount    int           `json:"total_claim_count"`
	CreatedBy          *uuid.UUID    `json:"created_by,omitempty"`
	ReleasedBy         *uuid.UUID    `json:"released_by,omitempty"`
	ReleasedAt         *time.Time    `json:"released_at,omitempty"`
	Version            int           `json:"version"`
	CreatedAt          time.Time     `json:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at"`
}

// InventoryClaim represents one unit-level allocation inside the central inventory_claims table.
type InventoryClaim struct {
	ID                      uuid.UUID         `json:"id"`
	PropertyID              uuid.UUID         `json:"property_id"`
	UnitID                  uuid.UUID         `json:"unit_id"`
	UnitCode                string            `json:"unit_code,omitempty"`
	UnitName                string            `json:"unit_name,omitempty"`
	ClaimType               ClaimType         `json:"claim_type"`
	ReservationID           *uuid.UUID        `json:"reservation_id,omitempty"`
	ReservationRef          string            `json:"reservation_reference,omitempty"`
	GuestName               string            `json:"guest_name,omitempty"`
	ReservationStat         ReservationStatus `json:"reservation_status,omitempty"`
	BlockoutGroupID         *uuid.UUID        `json:"blockout_group_id,omitempty"`
	ChannelScope            string            `json:"channel_scope"`
	StartDate               time.Time         `json:"start_date"`
	EndDate                 time.Time         `json:"end_date"` // Exclusive
	Status                  ClaimStatus       `json:"status"`
	ConsumedByReservationID *uuid.UUID        `json:"consumed_by_reservation_id,omitempty"`
	Reason                  string            `json:"reason"`
	CreatedBy               *uuid.UUID        `json:"created_by,omitempty"`
	ReleasedBy              *uuid.UUID        `json:"released_by,omitempty"`
	ReleasedAt              *time.Time        `json:"released_at,omitempty"`
	CreatedAt               time.Time         `json:"created_at"`
	UpdatedAt               time.Time         `json:"updated_at"`
}

// BulkBlockoutInput describes a bulk blockout request or preview.
type BulkBlockoutInput struct {
	PropertyID         uuid.UUID     `json:"property_id"`
	ScopeType          BlockoutScope `json:"scope_type"`
	UnitIDs            []uuid.UUID   `json:"unit_ids"`
	UnitTypeID         *uuid.UUID    `json:"unit_type_id,omitempty"`
	Category           ClaimType     `json:"category"`
	ChannelScope       string        `json:"channel_scope"`
	StartDate          time.Time     `json:"start_date"`
	EndDate            time.Time     `json:"end_date"`
	Reason             string        `json:"reason"`
	ApplyToFutureUnits bool          `json:"apply_to_future_units"`
}

func (in *BulkBlockoutInput) Validate() error {
	errs := map[string]string{}
	in.Category = ClaimType(strings.TrimSpace(strings.ToUpper(string(in.Category))))
	in.ChannelScope = strings.TrimSpace(strings.ToUpper(in.ChannelScope))
	if in.ChannelScope == "" {
		in.ChannelScope = "ALL"
	}
	in.Reason = strings.TrimSpace(in.Reason)

	if !in.ScopeType.Valid() {
		errs["scope_type"] = "must be UNIT, UNITS, UNIT_TYPE, or PROPERTY"
	}
	if !in.Category.ValidBlockoutCategory() {
		errs["category"] = "must be one of OWNER_USE, MAINTENANCE, SEASONAL_CLOSURE, WEATHER_CLOSURE, CHANNEL_ALLOCATION, CHANNEL_RESTRICTION, OPERATIONAL_BUFFER, ADMINISTRATIVE_BLOCK, HOLD"
	}
	if !channelCodeRegex.MatchString(in.ChannelScope) {
		errs["channel_scope"] = "must be ALL or valid uppercase channel code"
	} else if in.Category.RequiresSpecificChannel() && in.ChannelScope == "ALL" {
		errs["channel_scope"] = fmt.Sprintf("%s requires a specific booking channel (cannot be ALL)", in.Category)
	} else if !in.Category.RequiresSpecificChannel() && in.ChannelScope != "ALL" {
		errs["channel_scope"] = "physical closures and holds must use ALL channels; use CHANNEL_ALLOCATION or CHANNEL_RESTRICTION for channel-specific rules"
	}
	if in.StartDate.IsZero() || in.EndDate.IsZero() || !in.EndDate.After(in.StartDate) {
		errs["date_range"] = "end_date must be strictly after start_date"
	}
	switch in.ScopeType {
	case BlockScopeUnit:
		if len(in.UnitIDs) != 1 {
			errs["unit_ids"] = "must specify exactly 1 unit for UNIT scope"
		}
	case BlockScopeUnits:
		if len(in.UnitIDs) == 0 {
			errs["unit_ids"] = "must select at least 1 unit for UNITS scope"
		}
	case BlockScopeUnitType:
		if in.UnitTypeID == nil || *in.UnitTypeID == uuid.Nil {
			errs["unit_type_id"] = "is required for UNIT_TYPE scope"
		}
	}
	return NewValidationError(errs)
}

// BlockoutConflict summarizes an existing active claim colliding with a proposed blockout.
type BlockoutConflict struct {
	UnitID        uuid.UUID  `json:"unit_id"`
	UnitCode      string     `json:"unit_code"`
	UnitName      string     `json:"unit_name"`
	ClaimID       uuid.UUID  `json:"claim_id"`
	ClaimType     ClaimType  `json:"claim_type"`
	ReservationID *uuid.UUID `json:"reservation_id,omitempty"`
	Reference     string     `json:"reference,omitempty"`
	GuestName     string     `json:"guest_name,omitempty"`
	StartDate     time.Time  `json:"start_date"`
	EndDate       time.Time  `json:"end_date"`
	Reason        string     `json:"reason"`
}

// BlockoutPreview shows affected units and any blocking conflicts before confirmation.
type BlockoutPreview struct {
	AffectedUnits []Unit             `json:"affected_units"`
	Conflicts     []BlockoutConflict `json:"conflicts"`
	CanCommit     bool               `json:"can_commit"`
}

// ReleaseScope defines the target inventory of a scheduled availability release rule.
type ReleaseScope string

const (
	ReleaseScopeUnit     ReleaseScope = "UNIT"
	ReleaseScopeUnitType ReleaseScope = "UNIT_TYPE"
	ReleaseScopeProperty ReleaseScope = "PROPERTY"
)

func (s ReleaseScope) Valid() bool {
	switch s {
	case ReleaseScopeUnit, ReleaseScopeUnitType, ReleaseScopeProperty:
		return true
	default:
		return false
	}
}

// AvailabilityReleaseRule holds back [StartDate, EndDate) until ReleaseAt for ChannelCode.
type AvailabilityReleaseRule struct {
	ID                 uuid.UUID    `json:"id"`
	PropertyID         uuid.UUID    `json:"property_id"`
	ScopeType          ReleaseScope `json:"scope_type"`
	UnitID             *uuid.UUID   `json:"unit_id,omitempty"`
	UnitCode           string       `json:"unit_code,omitempty"`
	UnitName           string       `json:"unit_name,omitempty"`
	UnitTypeID         *uuid.UUID   `json:"unit_type_id,omitempty"`
	UnitTypeName       string       `json:"unit_type_name,omitempty"`
	StartDate          time.Time    `json:"start_date"`
	EndDate            time.Time    `json:"end_date"` // Exclusive
	ChannelCode        string       `json:"channel_code"`
	ReleaseAt          time.Time    `json:"release_at"` // UTC in DB; formatted in property timezone for display
	ReleasedManuallyAt *time.Time   `json:"released_manually_at,omitempty"`
	ReleasedManuallyBy *uuid.UUID   `json:"released_manually_by,omitempty"`
	IsActive           bool         `json:"is_active"`
	Notes              string       `json:"notes"`
	CreatedBy          *uuid.UUID   `json:"created_by,omitempty"`
	UpdatedBy          *uuid.UUID   `json:"updated_by,omitempty"`
	CancelledAt        *time.Time   `json:"cancelled_at,omitempty"`
	CancelledBy        *uuid.UUID   `json:"cancelled_by,omitempty"`
	Version            int          `json:"version"`
	CreatedAt          time.Time    `json:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at"`
}

func (r *AvailabilityReleaseRule) Validate() error {
	errs := map[string]string{}
	r.ChannelCode = strings.TrimSpace(strings.ToUpper(r.ChannelCode))
	r.Notes = strings.TrimSpace(r.Notes)

	if !r.ScopeType.Valid() {
		errs["scope_type"] = "must be UNIT, UNIT_TYPE, or PROPERTY"
	}
	if r.ScopeType == ReleaseScopeUnit && (r.UnitID == nil || *r.UnitID == uuid.Nil) {
		errs["unit_id"] = "is required for UNIT scope"
	}
	if r.ScopeType == ReleaseScopeUnitType && (r.UnitTypeID == nil || *r.UnitTypeID == uuid.Nil) {
		errs["unit_type_id"] = "is required for UNIT_TYPE scope"
	}
	if !channelCodeRegex.MatchString(r.ChannelCode) {
		errs["channel_code"] = "must be ALL or a valid uppercase channel code"
	}
	if r.StartDate.IsZero() || r.EndDate.IsZero() || !r.EndDate.After(r.StartDate) {
		errs["date_range"] = "end_date must be strictly after start_date"
	}
	if r.ReleaseAt.IsZero() {
		errs["release_at"] = "is required"
	}
	return NewValidationError(errs)
}

// IsReleasedAt evaluates whether this rule has opened at evaluationTime.
func (r AvailabilityReleaseRule) IsReleasedAt(evaluationTime time.Time) bool {
	if !r.IsActive || r.CancelledAt != nil {
		return true
	}
	if r.ReleasedManuallyAt != nil {
		return true
	}
	return !evaluationTime.Before(r.ReleaseAt)
}

// ReleaseStatusLabel returns EFFECTIVE, RELEASED_MANUALLY, CANCELLED, INACTIVE, or SCHEDULED.
func (r AvailabilityReleaseRule) ReleaseStatusLabel(now time.Time) string {
	if r.CancelledAt != nil {
		return "CANCELLED"
	}
	if !r.IsActive {
		return "INACTIVE"
	}
	if r.ReleasedManuallyAt != nil {
		return "RELEASED_MANUALLY"
	}
	if !now.Before(r.ReleaseAt) {
		return "RELEASED"
	}
	return "SCHEDULED"
}

// EvaluateReleaseForStay checks if any active release rule prevents sale of unit over [startDate, endDate)
// on channelCode at evaluationTime. Returns the first blocking rule if unreleased.
func EvaluateReleaseForStay(
	rules []AvailabilityReleaseRule,
	unit Unit,
	channelCode string,
	startDate, endDate time.Time,
	evaluationTime time.Time,
) (allowed bool, blockingRule *AvailabilityReleaseRule, reason string) {
	ch := strings.ToUpper(strings.TrimSpace(channelCode))

	for cur := startDate; cur.Before(endDate); cur = cur.AddDate(0, 0, 1) {
		var channelSpecific []AvailabilityReleaseRule
		var allChannel []AvailabilityReleaseRule

		for _, r := range rules {
			if !r.IsActive || r.CancelledAt != nil {
				continue
			}
			if cur.Before(r.StartDate) || !cur.Before(r.EndDate) {
				continue
			}
			if !ruleMatchesUnit(r, unit) {
				continue
			}
			if r.ChannelCode == ch {
				channelSpecific = append(channelSpecific, r)
			} else if r.ChannelCode == "ALL" {
				allChannel = append(allChannel, r)
			}
		}

		// Channel-specific release policies take precedence over generic ALL policies.
		governing := channelSpecific
		if len(governing) == 0 {
			governing = allChannel
		}
		for i := range governing {
			r := governing[i]
			if !r.IsReleasedAt(evaluationTime) {
				copyRule := r
				return false, &copyRule, fmt.Sprintf(
					"Dates held until %s for channel %s",
					r.ReleaseAt.UTC().Format(time.RFC3339), r.ChannelCode,
				)
			}
		}
	}

	return true, nil, ""
}

func ruleMatchesUnit(r AvailabilityReleaseRule, u Unit) bool {
	switch r.ScopeType {
	case ReleaseScopeProperty:
		return r.PropertyID == u.PropertyID
	case ReleaseScopeUnitType:
		return r.UnitTypeID != nil && *r.UnitTypeID == u.UnitTypeID
	case ReleaseScopeUnit:
		return r.UnitID != nil && *r.UnitID == u.ID
	default:
		return false
	}
}
