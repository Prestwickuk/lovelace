package domain

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var channelCodeRegex = regexp.MustCompile(`^[A-Z0-9_]+$`)

// BookingChannel represents an extensible booking source classification.
type BookingChannel struct {
	ID         uuid.UUID `json:"id"`
	PropertyID uuid.UUID `json:"property_id"`
	Code       string    `json:"code"`
	Name       string    `json:"name"`
	Category   string    `json:"category"` // direct, ota, manual, other
	IsActive   bool      `json:"is_active"`
	SortOrder  int       `json:"sort_order"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (c *BookingChannel) Validate() error {
	errs := map[string]string{}
	c.Code = strings.TrimSpace(strings.ToUpper(c.Code))
	c.Name = strings.TrimSpace(c.Name)
	c.Category = strings.TrimSpace(strings.ToLower(c.Category))

	if !channelCodeRegex.MatchString(c.Code) {
		errs["code"] = "must be uppercase alphanumeric with underscores (e.g. DIRECT, AIRBNB)"
	}
	if c.Name == "" {
		errs["name"] = "is required"
	}
	switch c.Category {
	case "direct", "ota", "manual", "other":
	default:
		errs["category"] = "must be direct, ota, manual, or other"
	}
	return NewValidationError(errs)
}

// Guest represents a guest CRM profile linked to reservations, enquiries, and communications.
type Guest struct {
	ID                     uuid.UUID       `json:"id"`
	PropertyID             uuid.UUID       `json:"property_id"`
	FirstName              string          `json:"first_name"`
	LastName               string          `json:"last_name"`
	FullName               string          `json:"full_name"`
	Email                  string          `json:"email"`
	NormalizedEmail        string          `json:"normalized_email,omitempty"`
	Phone                  string          `json:"phone"`
	NormalizedPhone        string          `json:"normalized_phone,omitempty"`
	PreferredContactMethod string          `json:"preferred_contact_method"`
	PreferredLanguage      string          `json:"preferred_language"`
	Status                 GuestStatus     `json:"status"`
	AddressJSON            json.RawMessage `json:"address_json,omitempty"`
	Preferences            json.RawMessage `json:"preferences,omitempty"`
	Notes                  string          `json:"notes"`
	MergedIntoGuestID      *uuid.UUID      `json:"merged_into_guest_id,omitempty"`
	MergedAt               *time.Time      `json:"merged_at,omitempty"`
	AnonymizedAt           *time.Time      `json:"anonymized_at,omitempty"`
	ErasureRequestedAt     *time.Time      `json:"erasure_requested_at,omitempty"`
	CreatedBy              *uuid.UUID      `json:"created_by,omitempty"`
	UpdatedBy              *uuid.UUID      `json:"updated_by,omitempty"`
	Version                int             `json:"version"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
}

// ReservationStatus defines the Phase 1.1 reservation lifecycle (excluding payment status).
type ReservationStatus string

const (
	ResStatusInquiry    ReservationStatus = "INQUIRY"
	ResStatusPending    ReservationStatus = "PENDING"
	ResStatusConfirmed  ReservationStatus = "CONFIRMED"
	ResStatusCheckedIn  ReservationStatus = "CHECKED_IN"
	ResStatusCheckedOut ReservationStatus = "CHECKED_OUT"
	ResStatusCancelled  ReservationStatus = "CANCELLED"
	ResStatusNoShow     ReservationStatus = "NO_SHOW"
)

func (s ReservationStatus) Valid() bool {
	switch s {
	case ResStatusInquiry, ResStatusPending, ResStatusConfirmed,
		ResStatusCheckedIn, ResStatusCheckedOut, ResStatusCancelled, ResStatusNoShow:
		return true
	default:
		return false
	}
}

// BlocksInventory reports whether reservations in this status hold active inventory claims.
func (s ReservationStatus) BlocksInventory() bool {
	switch s {
	case ResStatusPending, ResStatusConfirmed, ResStatusCheckedIn:
		return true
	default:
		return false
	}
}

// ClaimTypeForStatus maps a blocking reservation status to its central inventory_claims type.
func (s ReservationStatus) ClaimType() ClaimType {
	if s == ResStatusPending {
		return ClaimPendingReservation
	}
	return ClaimReservation
}

var allowedReservationTransitions = map[ReservationStatus][]ReservationStatus{
	ResStatusInquiry:   {ResStatusPending, ResStatusConfirmed, ResStatusCancelled},
	ResStatusPending:   {ResStatusConfirmed, ResStatusCancelled},
	ResStatusConfirmed: {ResStatusCheckedIn, ResStatusCancelled, ResStatusNoShow},
	ResStatusCheckedIn: {ResStatusCheckedOut},
	// Terminal states: CHECKED_OUT, CANCELLED, NO_SHOW
}

// CanTransitionTo validates whether transitioning from s to next is permitted.
func (s ReservationStatus) CanTransitionTo(next ReservationStatus) bool {
	for _, allowed := range allowedReservationTransitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// ReservationUnit represents a physical unit assigned to a reservation
// along with optional per-unit guest allocation breakdown.
type ReservationUnit struct {
	UnitID       uuid.UUID `json:"unit_id"`
	UnitCode     string    `json:"unit_code"`
	UnitName     string    `json:"unit_name"`
	UnitTypeName string    `json:"unit_type_name"`
	Adults       int       `json:"adults"`
	Children     int       `json:"children"`
	Infants      int       `json:"infants"`
}

// Reservation represents an administrative reservation covering one or more units.
type Reservation struct {
	ID                      uuid.UUID         `json:"id"`
	PropertyID              uuid.UUID         `json:"property_id"`
	Reference               string            `json:"reference"`
	GuestID                 uuid.UUID         `json:"guest_id"`
	GuestName               string            `json:"guest_name"`
	GuestEmail              string            `json:"guest_email"`
	GuestPhone              string            `json:"guest_phone"`
	ChannelCode             string            `json:"channel_code"`
	ArrivalDate             time.Time         `json:"arrival_date"`
	DepartureDate           time.Time         `json:"departure_date"` // Exclusive checkout date
	Adults                  int               `json:"adults"`
	Children                int               `json:"children"`
	Infants                 int               `json:"infants"`
	Status                  ReservationStatus `json:"status"`
	PaymentStatus           string            `json:"payment_status,omitempty"`
	TotalPence              int64             `json:"total_pence"`
	DepositPence            int64             `json:"deposit_pence"`
	AmountPaidPence         int64             `json:"amount_paid_pence"`
	BalanceDuePence         int64             `json:"balance_due_pence"`
	Currency                string            `json:"currency,omitempty"`
	OccupantDetails         string            `json:"occupant_details,omitempty"`
	InternalNotes           string            `json:"internal_notes"`
	CancellationReason      string            `json:"cancellation_reason,omitempty"`
	CancelledAt             *time.Time        `json:"cancelled_at,omitempty"`
	CancellationRequestedAt *time.Time        `json:"cancellation_requested_at,omitempty"`
	CancellationRequestNote string            `json:"cancellation_request_note,omitempty"`
	Units                   []ReservationUnit `json:"units"`
	UnitIDs                 []uuid.UUID       `json:"unit_ids,omitempty"`
	CreatedBy               *uuid.UUID        `json:"created_by,omitempty"`
	UpdatedBy               *uuid.UUID        `json:"updated_by,omitempty"`
	Version                 int               `json:"version"`
	CreatedAt               time.Time         `json:"created_at"`
	UpdatedAt               time.Time         `json:"updated_at"`
}

func (r *Reservation) Validate() error {
	errs := map[string]string{}
	r.GuestName = strings.TrimSpace(r.GuestName)
	r.GuestEmail = strings.TrimSpace(strings.ToLower(r.GuestEmail))
	r.GuestPhone = strings.TrimSpace(r.GuestPhone)
	r.ChannelCode = strings.TrimSpace(strings.ToUpper(r.ChannelCode))
	r.InternalNotes = strings.TrimSpace(r.InternalNotes)

	if r.GuestName == "" {
		errs["guest_name"] = "is required"
	}
	if r.GuestEmail != "" {
		if _, err := mail.ParseAddress(r.GuestEmail); err != nil {
			errs["guest_email"] = "must be a valid email address"
		}
	}
	if !channelCodeRegex.MatchString(r.ChannelCode) {
		errs["channel_code"] = "is required"
	}
	if r.ArrivalDate.IsZero() || r.DepartureDate.IsZero() || !r.DepartureDate.After(r.ArrivalDate) {
		errs["date_range"] = "departure_date must be strictly after arrival_date"
	}
	// Normalize unit_ids and optional per-unit allocations cleanly.
	if len(r.Units) > 0 {
		seenAlloc := make(map[uuid.UUID]bool, len(r.Units))
		allocIDs := make([]uuid.UUID, 0, len(r.Units))
		var sumA, sumC, sumI int
		hasBreakdown := false
		for _, ru := range r.Units {
			if ru.UnitID == uuid.Nil {
				errs["units"] = "contains invalid unit id"
				break
			}
			if seenAlloc[ru.UnitID] {
				errs["units"] = "duplicate unit in per-unit allocation"
				break
			}
			if ru.Adults < 0 || ru.Children < 0 || ru.Infants < 0 {
				errs["units"] = "per-unit guest counts cannot be negative"
				break
			}
			seenAlloc[ru.UnitID] = true
			allocIDs = append(allocIDs, ru.UnitID)
			sumA += ru.Adults
			sumC += ru.Children
			sumI += ru.Infants
			if ru.Adults > 0 || ru.Children > 0 || ru.Infants > 0 {
				hasBreakdown = true
			}
		}
		if len(r.UnitIDs) == 0 {
			r.UnitIDs = allocIDs
		} else {
			for _, id := range r.UnitIDs {
				if !seenAlloc[id] {
					errs["units"] = "per-unit allocations must cover the exact same unit_ids"
					break
				}
			}
			if len(r.UnitIDs) != len(allocIDs) {
				errs["units"] = "per-unit allocations must cover the exact same unit_ids"
			}
		}
		if hasBreakdown {
			if r.Adults == 0 && r.Children == 0 && r.Infants == 0 {
				r.Adults = sumA
				r.Children = sumC
				r.Infants = sumI
			} else if sumA != r.Adults || sumC != r.Children || sumI != r.Infants {
				errs["unit_allocations"] = fmt.Sprintf(
					"sum of per-unit guests (%d adults, %d children, %d infants) must match party totals (%d adults, %d children, %d infants)",
					sumA, sumC, sumI, r.Adults, r.Children, r.Infants,
				)
			}
		}
	}

	if r.Adults < 1 {
		errs["adults"] = "must be at least 1"
	}
	if r.Children < 0 {
		errs["children"] = "cannot be negative"
	}
	if r.Infants < 0 {
		errs["infants"] = "cannot be negative"
	}
	if !r.Status.Valid() {
		errs["status"] = "invalid reservation status"
	}
	if len(r.UnitIDs) == 0 {
		errs["unit_ids"] = "at least one accommodation unit must be selected"
	}
	seen := make(map[uuid.UUID]struct{}, len(r.UnitIDs))
	dedup := make([]uuid.UUID, 0, len(r.UnitIDs))
	for _, id := range r.UnitIDs {
		if id == uuid.Nil {
			errs["unit_ids"] = "contains invalid unit id"
			break
		}
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			dedup = append(dedup, id)
		}
	}
	r.UnitIDs = dedup
	return NewValidationError(errs)
}

// ValidateOccupancy checks unit availability status, archived flags, unit-type
// capacity limits (single and combined multi-unit), and optional per-unit allocations.
func (r *Reservation) ValidateOccupancy(units []Unit, unitTypesByID map[uuid.UUID]UnitType) error {
	errs := map[string]string{}
	if len(units) == 0 {
		errs["unit_ids"] = "at least one valid active unit is required"
		return NewValidationError(errs)
	}

	unitByID := make(map[uuid.UUID]Unit, len(units))
	var combinedAdults, combinedChildren, combinedInfants, combinedGuests int

	for _, u := range units {
		unitByID[u.ID] = u
		if u.ArchivedAt != nil || u.Status == UnitStatusInactive || u.Status == UnitStatusRetired {
			errs["unit_ids"] = fmt.Sprintf("unit %s (%s) is %s and cannot accept reservations", u.Code, u.Name, u.Status)
			continue
		}
		ut, ok := unitTypesByID[u.UnitTypeID]
		if !ok {
			errs["unit_ids"] = fmt.Sprintf("unit %s references unknown accommodation type", u.Code)
			continue
		}
		if ut.ArchivedAt != nil || !ut.IsActive {
			errs["unit_ids"] = fmt.Sprintf("accommodation type %s for unit %s is inactive or archived", ut.Name, u.Code)
			continue
		}
		combinedAdults += ut.MaxAdults
		combinedChildren += ut.MaxChildren
		combinedInfants += ut.MaxInfants
		combinedGuests += ut.MaxGuests
	}

	if len(errs) > 0 {
		return NewValidationError(errs)
	}

	if r.Adults > combinedAdults {
		errs["adults"] = fmt.Sprintf("adults (%d) exceed selected unit capacity (%d max)", r.Adults, combinedAdults)
	}
	if r.Children > combinedChildren {
		errs["children"] = fmt.Sprintf("children (%d) exceed selected unit capacity (%d max)", r.Children, combinedChildren)
	}
	if r.Infants > combinedInfants {
		errs["infants"] = fmt.Sprintf("infants (%d) exceed selected unit capacity (%d max)", r.Infants, combinedInfants)
	}
	totalGuests := r.Adults + r.Children + r.Infants
	if totalGuests > combinedGuests {
		errs["occupancy"] = fmt.Sprintf("total party (%d guests) exceeds selected unit max_guests (%d)", totalGuests, combinedGuests)
	}

	// Validate explicit per-unit allocations if supplied on any unit row.
	hasPerUnit := false
	for _, ru := range r.Units {
		if ru.Adults > 0 || ru.Children > 0 || ru.Infants > 0 {
			hasPerUnit = true
			break
		}
	}
	if hasPerUnit {
		for _, ru := range r.Units {
			u := unitByID[ru.UnitID]
			ut := unitTypesByID[u.UnitTypeID]
			ruTotal := ru.Adults + ru.Children + ru.Infants
			if ru.Adults > ut.MaxAdults {
				errs["unit_allocations"] = fmt.Sprintf("unit %s adults (%d) exceed %s limit (%d)", u.Code, ru.Adults, ut.Name, ut.MaxAdults)
				break
			}
			if ru.Children > ut.MaxChildren {
				errs["unit_allocations"] = fmt.Sprintf("unit %s children (%d) exceed %s limit (%d)", u.Code, ru.Children, ut.Name, ut.MaxChildren)
				break
			}
			if ru.Infants > ut.MaxInfants {
				errs["unit_allocations"] = fmt.Sprintf("unit %s infants (%d) exceed %s limit (%d)", u.Code, ru.Infants, ut.Name, ut.MaxInfants)
				break
			}
			if ruTotal > ut.MaxGuests {
				errs["unit_allocations"] = fmt.Sprintf("unit %s total guests (%d) exceed %s max_guests (%d)", u.Code, ruTotal, ut.Name, ut.MaxGuests)
				break
			}
		}
	}

	return NewValidationError(errs)
}
