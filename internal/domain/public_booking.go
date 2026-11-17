package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/branding"
)

const (
	DepositModeFull       = "FULL"
	DepositModePercentage = "PERCENTAGE"
	DepositModeFixed      = "FIXED"

	HoldStatusActive    = "ACTIVE"
	HoldStatusConfirmed = "CONFIRMED"
	HoldStatusExpired   = "EXPIRED"
	HoldStatusCancelled = "CANCELLED"
	HoldStatusFailed    = "FAILED"

	PaymentStatusManual            = "MANUAL"
	PaymentStatusUnpaid            = "UNPAID"
	PaymentStatusPartiallyPaid     = "PARTIALLY_PAID"
	PaymentStatusPaid              = "PAID"
	PaymentStatusRefunded          = "REFUNDED"
	PaymentStatusPartiallyRefunded = "PARTIALLY_REFUNDED"
	PaymentStatusFailed            = "FAILED"

	AttemptStatusCreated                = "CREATED"
	AttemptStatusRequiresAction         = "REQUIRES_ACTION"
	AttemptStatusSucceeded              = "SUCCEEDED"
	AttemptStatusFailed                 = "FAILED"
	AttemptStatusCancelled              = "CANCELLED"
	AttemptStatusExpired                = "EXPIRED"
	AttemptStatusReconciliationRequired = "RECONCILIATION_REQUIRED"
	AttemptStatusRefunded               = "REFUNDED"
	AttemptStatusPartiallyRefunded      = "PARTIALLY_REFUNDED"
)

type PropertyBookingPolicy struct {
	PropertyID                  uuid.UUID `json:"property_id"`
	HoldDurationMinutes         int       `json:"hold_duration_minutes"`
	DepositMode                 string    `json:"deposit_mode"`
	DepositPercentage           int       `json:"deposit_percentage"`
	DepositFixedPence           int64     `json:"deposit_fixed_pence"`
	BalanceDueDaysBeforeArrival int       `json:"balance_due_days_before_arrival"`
	ExtraAdultNightlyPence      int64     `json:"extra_adult_nightly_pence"`
	ChildNightlyPence           int64     `json:"child_nightly_pence"`
	InfantNightlyPence          int64     `json:"infant_nightly_pence"`
	BookingTermsVersion         string    `json:"booking_terms_version"`
	BookingTermsURL             string    `json:"booking_terms_url"`
	PrivacyPolicyURL            string    `json:"privacy_policy_url"`
	CancellationPolicy          string    `json:"cancellation_policy"`
	QuietHours                  string    `json:"quiet_hours"`
	FirePolicy                  string    `json:"fire_policy"`
	PetPolicy                   string    `json:"pet_policy"`
	PrivacyNotice               string    `json:"privacy_notice"`
	StripeEnabled               bool      `json:"stripe_enabled"`
	GuestPortalEnabled          bool      `json:"guest_portal_enabled"`
	Version                     int       `json:"version"`
	UpdatedAt                   time.Time `json:"updated_at"`
}

func (p *PropertyBookingPolicy) Validate() error {
	errs := map[string]string{}
	if p.HoldDurationMinutes < 5 || p.HoldDurationMinutes > 120 {
		errs["hold_duration_minutes"] = "must be between 5 and 120 minutes"
	}
	p.DepositMode = strings.ToUpper(strings.TrimSpace(p.DepositMode))
	switch p.DepositMode {
	case DepositModeFull, DepositModePercentage, DepositModeFixed:
	default:
		errs["deposit_mode"] = "must be FULL, PERCENTAGE, or FIXED"
	}
	if p.DepositPercentage < 0 || p.DepositPercentage > 100 {
		errs["deposit_percentage"] = "must be between 0 and 100"
	}
	if p.DepositFixedPence < 0 || p.ExtraAdultNightlyPence < 0 || p.ChildNightlyPence < 0 || p.InfantNightlyPence < 0 {
		errs["amounts"] = "pricing and deposit amounts cannot be negative"
	}
	if p.BalanceDueDaysBeforeArrival < 0 {
		errs["balance_due_days_before_arrival"] = "cannot be negative"
	}
	p.BookingTermsVersion = strings.TrimSpace(p.BookingTermsVersion)
	if p.BookingTermsVersion == "" {
		errs["booking_terms_version"] = "is required"
	}
	return NewValidationError(errs)
}

type UnitTypeImage struct {
	URL     string `json:"url"`
	Alt     string `json:"alt"`
	Caption string `json:"caption,omitempty"`
}

// PublicPropertyView exposes strictly safe property and branding metadata to the public web frontend.
type PublicPropertyView struct {
	ID                uuid.UUID                       `json:"id"`
	Slug              string                          `json:"slug"`
	Name              string                          `json:"name"`
	Tagline           string                          `json:"tagline"`
	PublicDescription string                          `json:"public_description"`
	CountryCode       string                          `json:"country_code"`
	Region            string                          `json:"region"`
	AddressLine1      string                          `json:"address_line1,omitempty"`
	AddressLine2      string                          `json:"address_line2,omitempty"`
	Town              string                          `json:"town"`
	Postcode          string                          `json:"postcode"`
	Timezone          string                          `json:"timezone"`
	Currency          string                          `json:"currency"`
	CheckInTime       string                          `json:"check_in_time"`
	CheckOutTime      string                          `json:"check_out_time"`
	VATRegistered     bool                            `json:"vat_registered"`
	ContactEmail      string                          `json:"contact_email"`
	ContactPhone      string                          `json:"contact_phone"`
	WebsiteURL        string                          `json:"website_url,omitempty"`
	CustomDomain      string                          `json:"custom_domain,omitempty"`
	Branding          branding.PublicPropertyBranding `json:"branding"`
	Policy            PropertyBookingPolicy           `json:"policy"`
}

// PublicUnitTypeView exposes customer-facing accommodation details without internal operational notes or unit IDs.
type PublicUnitTypeView struct {
	ID                   uuid.UUID       `json:"id"`
	Code                 string          `json:"code"`
	Name                 string          `json:"name"`
	Category             UnitCategory    `json:"category"`
	Description          string          `json:"description"`
	BaseCapacityAdults   int             `json:"base_capacity_adults"`
	MaxAdults            int             `json:"max_adults"`
	MaxChildren          int             `json:"max_children"`
	MaxInfants           int             `json:"max_infants"`
	MaxGuests            int             `json:"max_guests"`
	PetsAllowed          bool            `json:"pets_allowed"`
	MaxPets              int             `json:"max_pets"`
	BaseNightlyRatePence int64           `json:"base_nightly_rate_pence"`
	TaxRateBPS           int             `json:"tax_rate_bps"`
	MinNights            int             `json:"min_nights"`
	MaxNights            int             `json:"max_nights"`
	Amenities            []string        `json:"amenities"`
	Images               []UnitTypeImage `json:"images"`
}

type PublicExtraView struct {
	ID           uuid.UUID    `json:"id"`
	Code         string       `json:"code"`
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	PricingModel PricingModel `json:"pricing_model"`
	PricePence   int64        `json:"price_pence"`
	TaxRateBPS   int          `json:"tax_rate_bps"`
	MaxQuantity  int          `json:"max_quantity"`
}

type AvailabilitySearchInput struct {
	PropertySlug string
	ArrivalDate  time.Time
	Departure    time.Time
	Adults       int
	Children     int
	Infants      int
	Category     string
}

type AvailabilitySearchItem struct {
	UnitType           PublicUnitTypeView `json:"unit_type"`
	AvailableUnits     int                `json:"available_units"`
	RatePlanID         uuid.UUID          `json:"rate_plan_id"`
	RatePlanName       string             `json:"rate_plan_name"`
	Nights             int                `json:"nights"`
	EffectiveMinStay   int                `json:"effective_min_nights"`
	EffectiveMaxStay   int                `json:"effective_max_nights"`
	MeetsStayRules     bool               `json:"meets_stay_rules"`
	AccommodationPence int64              `json:"accommodation_subtotal_pence"`
	SurchargePence     int64              `json:"occupancy_surcharge_pence"`
	TaxPence           int64              `json:"tax_pence"`
	TotalPence         int64              `json:"total_pence"`
	DepositDuePence    int64              `json:"deposit_due_pence"`
	BalanceDuePence    int64              `json:"balance_due_pence"`
	Currency           string             `json:"currency"`
}

type QuoteExtraSelection struct {
	ExtraID  uuid.UUID `json:"extra_id"`
	Quantity int       `json:"quantity"`
}

type QuoteExtraLine struct {
	ExtraID      uuid.UUID    `json:"extra_id"`
	Code         string       `json:"code"`
	Name         string       `json:"name"`
	PricingModel PricingModel `json:"pricing_model"`
	Quantity     int          `json:"quantity"`
	UnitPence    int64        `json:"unit_pence"`
	TotalPence   int64        `json:"total_pence"`
	TaxPence     int64        `json:"tax_pence"`
}

type TaxLine struct {
	Label        string `json:"label"`
	RateBPS      int    `json:"rate_bps"`
	TaxablePence int64  `json:"taxable_pence"`
	TaxPence     int64  `json:"tax_pence"`
}

type BookingQuote struct {
	ID                         uuid.UUID              `json:"id"`
	PropertyID                 uuid.UUID              `json:"property_id"`
	HoldID                     *uuid.UUID             `json:"hold_id,omitempty"`
	ReservationID              *uuid.UUID             `json:"reservation_id,omitempty"`
	UnitTypeID                 uuid.UUID              `json:"unit_type_id"`
	UnitTypeCode               string                 `json:"unit_type_code,omitempty"`
	UnitTypeName               string                 `json:"unit_type_name,omitempty"`
	RatePlanID                 uuid.UUID              `json:"rate_plan_id"`
	RatePlanName               string                 `json:"rate_plan_name,omitempty"`
	Currency                   string                 `json:"currency"`
	StartDate                  string                 `json:"start_date"`
	EndDate                    string                 `json:"end_date"`
	Nights                     int                    `json:"nights"`
	Adults                     int                    `json:"adults"`
	Children                   int                    `json:"children"`
	Infants                    int                    `json:"infants"`
	NightlyBreakdown           []NightlyRateBreakdown `json:"nightly_breakdown"`
	ExtrasBreakdown            []QuoteExtraLine       `json:"extras_breakdown"`
	TaxBreakdown               []TaxLine              `json:"tax_breakdown"`
	AccommodationSubtotalPence int64                  `json:"accommodation_subtotal_pence"`
	OccupancySurchargePence    int64                  `json:"occupancy_surcharge_pence"`
	ExtrasSubtotalPence        int64                  `json:"extras_subtotal_pence"`
	DiscountPence              int64                  `json:"discount_pence"`
	TaxPence                   int64                  `json:"tax_pence"`
	TotalPence                 int64                  `json:"total_pence"`
	DepositMode                string                 `json:"deposit_mode"`
	DepositDuePence            int64                  `json:"deposit_due_pence"`
	BalanceDuePence            int64                  `json:"balance_due_pence"`
	BalanceDueDate             string                 `json:"balance_due_date,omitempty"`
	PolicyVersion              int                    `json:"policy_version"`
	QuoteHash                  string                 `json:"quote_hash"`
	ExpiresAt                  time.Time              `json:"expires_at"`
	CreatedAt                  time.Time              `json:"created_at"`
}

type BookingHold struct {
	ID                     uuid.UUID  `json:"id"`
	PropertyID             uuid.UUID  `json:"property_id"`
	GuestID                *uuid.UUID `json:"guest_id,omitempty"`
	HoldReference          string     `json:"hold_reference"`
	UnitTypeID             uuid.UUID  `json:"unit_type_id"`
	UnitID                 uuid.UUID  `json:"-"`
	UnitTypeName           string     `json:"unit_type_name,omitempty"`
	RatePlanID             uuid.UUID  `json:"rate_plan_id"`
	ChannelCode            string     `json:"channel_code"`
	ArrivalDate            time.Time  `json:"-"`
	DepartureDate          time.Time  `json:"-"`
	Arrival                string     `json:"arrival_date"`
	Departure              string     `json:"departure_date"`
	Adults                 int        `json:"adults"`
	Children               int        `json:"children"`
	Infants                int        `json:"infants"`
	GuestName              string     `json:"guest_name"`
	GuestEmail             string     `json:"guest_email"`
	GuestPhone             string     `json:"guest_phone"`
	SpecialRequests        string     `json:"special_requests,omitempty"`
	TermsVersion           string     `json:"terms_version"`
	TermsAcceptedAt        *time.Time `json:"terms_accepted_at,omitempty"`
	CustomerSessionHash    string     `json:"-"`
	Status                 string     `json:"status"`
	ExpiresAt              time.Time  `json:"expires_at"`
	ConfirmedReservationID *uuid.UUID `json:"confirmed_reservation_id,omitempty"`
	Version                int        `json:"version"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type PaymentAttempt struct {
	ID                      uuid.UUID  `json:"id"`
	PropertyID              uuid.UUID  `json:"property_id"`
	HoldID                  *uuid.UUID `json:"hold_id,omitempty"`
	ReservationID           *uuid.UUID `json:"reservation_id,omitempty"`
	Provider                string     `json:"provider"`
	Mode                    string     `json:"mode"`
	ProviderSessionID       string     `json:"provider_session_id,omitempty"`
	ProviderPaymentIntentID string     `json:"provider_payment_intent_id,omitempty"`
	PaymentType             string     `json:"payment_type"`
	AmountPence             int64      `json:"amount_pence"`
	Currency                string     `json:"currency"`
	Status                  string     `json:"status"`
	IdempotencyKey          string     `json:"idempotency_key"`
	CheckoutURL             string     `json:"checkout_url,omitempty"`
	FailureReason           string     `json:"failure_reason,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

type PaymentRecord struct {
	ID                      uuid.UUID `json:"id"`
	PropertyID              uuid.UUID `json:"property_id"`
	ReservationID           uuid.UUID `json:"reservation_id"`
	PaymentAttemptID        uuid.UUID `json:"payment_attempt_id"`
	Provider                string    `json:"provider"`
	ProviderPaymentIntentID string    `json:"provider_payment_intent_id,omitempty"`
	ProviderChargeID        string    `json:"provider_charge_id,omitempty"`
	PaymentType             string    `json:"payment_type"`
	AmountPence             int64     `json:"amount_pence"`
	Currency                string    `json:"currency"`
	Status                  string    `json:"status"`
	PaidAt                  time.Time `json:"paid_at"`
	CreatedAt               time.Time `json:"created_at"`
}

type PaymentException struct {
	ID               uuid.UUID  `json:"id"`
	PropertyID       uuid.UUID  `json:"property_id"`
	HoldID           *uuid.UUID `json:"hold_id,omitempty"`
	PaymentAttemptID uuid.UUID  `json:"payment_attempt_id"`
	ReservationID    *uuid.UUID `json:"reservation_id,omitempty"`
	ExceptionType    string     `json:"exception_type"`
	AmountPence      int64      `json:"amount_pence"`
	Currency         string     `json:"currency"`
	Details          string     `json:"details"`
	Status           string     `json:"status"`
	ResolutionNotes  string     `json:"resolution_notes"`
	ResolvedBy       *uuid.UUID `json:"resolved_by,omitempty"`
	ResolvedAt       *time.Time `json:"resolved_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

type PublicConfirmationView struct {
	Status                string          `json:"status"`
	HoldReference         string          `json:"hold_reference,omitempty"`
	ReservationReference  string          `json:"reservation_reference,omitempty"`
	PropertyName          string          `json:"property_name"`
	PropertySlug          string          `json:"property_slug"`
	ContactEmail          string          `json:"contact_email"`
	ContactPhone          string          `json:"contact_phone"`
	CheckInTime           string          `json:"check_in_time"`
	CheckOutTime          string          `json:"check_out_time"`
	CancellationPolicy    string          `json:"cancellation_policy"`
	UnitTypeName          string          `json:"unit_type_name"`
	ArrivalDate           string          `json:"arrival_date"`
	DepartureDate         string          `json:"departure_date"`
	Nights                int             `json:"nights"`
	Adults                int             `json:"adults"`
	Children              int             `json:"children"`
	Infants               int             `json:"infants"`
	GuestName             string          `json:"guest_name"`
	GuestEmail            string          `json:"guest_email"`
	PaymentStatus         string          `json:"payment_status"`
	Quote                 *BookingQuote   `json:"quote,omitempty"`
	Payments              []PaymentRecord `json:"payments,omitempty"`
	CancellationRequested bool            `json:"cancellation_requested"`
	ConfirmationToken     string          `json:"confirmation_token,omitempty"`
	HoldExpiresAt         *time.Time      `json:"hold_expires_at,omitempty"`
}

const (
	OutboxStatusPending         = "PENDING"
	OutboxStatusProcessing      = "PROCESSING"
	OutboxStatusDispatching     = "DISPATCHING"
	OutboxStatusDispatchUnknown = "DISPATCH_UNKNOWN"
	OutboxStatusCancelRequested = "CANCEL_REQUESTED"
	OutboxStatusCancelled       = "CANCELLED"
	OutboxStatusSent            = "SENT"
	OutboxStatusFailed          = "FAILED"
)

type EmailOutboxItem struct {
	ID                    uuid.UUID  `json:"id"`
	PropertyID            uuid.UUID  `json:"property_id"`
	ReservationID         *uuid.UUID `json:"reservation_id,omitempty"`
	GuestID               *uuid.UUID `json:"guest_id,omitempty"`
	CommunicationID       *uuid.UUID `json:"communication_id,omitempty"`
	RecipientEmail        string     `json:"recipient_email"`
	Template              string     `json:"template"`
	Subject               string     `json:"subject"`
	BodyText              string     `json:"body_text"`
	BodyHTML              string     `json:"body_html"`
	Status                string     `json:"status"`
	Attempts              int        `json:"attempts"`
	LastError             string     `json:"last_error,omitempty"`
	CancelReason          string     `json:"cancel_reason,omitempty"`
	CancelledAt           *time.Time `json:"cancelled_at,omitempty"`
	WorkerToken           *uuid.UUID `json:"-"`
	DispatchedAt          *time.Time `json:"dispatched_at,omitempty"`
	DispatchWorkerID      string     `json:"dispatch_worker_id,omitempty"`
	DispatchLeaseExpires  *time.Time `json:"dispatch_lease_expires_at,omitempty"`
	DispatchHeartbeatAt   *time.Time `json:"dispatch_heartbeat_at,omitempty"`
	DispatchFenceEpoch    int64      `json:"dispatch_fence_epoch,omitempty"`
	DispatchFencedAt      *time.Time `json:"dispatch_fenced_at,omitempty"`
	DispatchOutcome       string     `json:"dispatch_outcome,omitempty"`
	ReconciledBy          *uuid.UUID `json:"reconciled_by,omitempty"`
	ReconciledAt          *time.Time `json:"reconciled_at,omitempty"`
	ReconciledDecision    string     `json:"reconciled_decision,omitempty"`
	ReconciledEvidence    string     `json:"reconciled_evidence_type,omitempty"`
	ReconciledEvidenceRef string     `json:"reconciled_evidence_ref,omitempty"`
	ReconciledReasonCode  string     `json:"reconciled_reason_code,omitempty"`
	NextAttemptAt         time.Time  `json:"next_attempt_at"`
	SentAt                *time.Time `json:"sent_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
}

// ComputePublicQuote deterministically calculates authoritative pricing, extras, taxes, deposit, and balance.
func ComputePublicQuote(
	prop Property,
	policy PropertyBookingPolicy,
	ut UnitType,
	plan RatePlan,
	rules []RateRule,
	extrasByID map[uuid.UUID]Extra,
	selectedExtras []QuoteExtraSelection,
	startDate, endDate time.Time,
	adults, children, infants int,
	nowUTC time.Time,
) (BookingQuote, error) {
	errs := map[string]string{}
	if adults < 1 {
		errs["adults"] = "must be at least 1"
	}
	if children < 0 || infants < 0 {
		errs["party"] = "children and infants cannot be negative"
	}
	if !ut.IsActive || ut.ArchivedAt != nil || !ut.IsPublicBookable {
		return BookingQuote{}, NewValidationError(map[string]string{"unit_type_id": "accommodation is not available for public booking"})
	}
	if adults > ut.MaxAdults {
		errs["adults"] = fmt.Sprintf("exceeds maximum adults (%d)", ut.MaxAdults)
	}
	if children > ut.MaxChildren {
		errs["children"] = fmt.Sprintf("exceeds maximum children (%d)", ut.MaxChildren)
	}
	if infants > ut.MaxInfants {
		errs["infants"] = fmt.Sprintf("exceeds maximum infants (%d)", ut.MaxInfants)
	}
	if adults+children+infants > ut.MaxGuests {
		errs["party"] = fmt.Sprintf("total party exceeds maximum occupancy (%d)", ut.MaxGuests)
	}
	if len(errs) > 0 {
		return BookingQuote{}, NewValidationError(errs)
	}

	preview, err := CalculateRatePreview(ut, plan, rules, prop.Currency, startDate, endDate)
	if err != nil {
		return BookingQuote{}, err
	}
	if !preview.MeetsStayRules {
		return BookingQuote{}, NewValidationError(map[string]string{
			"date_range": fmt.Sprintf("stay length (%d nights) must be between %d and %d nights",
				preview.Nights, preview.EffectiveMinNights, preview.EffectiveMaxNights),
		})
	}

	extraAdults := adults - ut.BaseCapacityAdults
	if extraAdults < 0 {
		extraAdults = 0
	}
	surcharge := int64(preview.Nights) * (int64(extraAdults)*policy.ExtraAdultNightlyPence +
		int64(children)*policy.ChildNightlyPence +
		int64(infants)*policy.InfantNightlyPence)

	totalHeadcount := adults + children + infants
	extraLines := make([]QuoteExtraLine, 0, len(selectedExtras))
	var extrasSubtotal int64
	taxByRate := map[int]int64{}

	accommTaxable := preview.TotalPence + surcharge
	if ut.TaxRateBPS > 0 {
		taxByRate[ut.TaxRateBPS] += accommTaxable
	}

	seenExtra := map[uuid.UUID]bool{}
	for _, sel := range selectedExtras {
		if sel.Quantity <= 0 {
			continue
		}
		if seenExtra[sel.ExtraID] {
			return BookingQuote{}, NewValidationError(map[string]string{"extras": "duplicate extra selection"})
		}
		seenExtra[sel.ExtraID] = true

		ex, ok := extrasByID[sel.ExtraID]
		if !ok || !ex.IsActive || ex.ArchivedAt != nil {
			return BookingQuote{}, NewValidationError(map[string]string{"extras": "invalid or inactive extra selected"})
		}
		if sel.Quantity > ex.MaxQuantity {
			return BookingQuote{}, NewValidationError(map[string]string{"extras": fmt.Sprintf("quantity for %s exceeds limit (%d)", ex.Name, ex.MaxQuantity)})
		}
		var lineTotal int64
		switch ex.PricingModel {
		case PricingPerStay:
			lineTotal = ex.PricePence * int64(sel.Quantity)
		case PricingPerNight:
			lineTotal = ex.PricePence * int64(sel.Quantity) * int64(preview.Nights)
		case PricingPerPersonPerStay:
			lineTotal = ex.PricePence * int64(sel.Quantity) * int64(totalHeadcount)
		case PricingPerPersonPerNight:
			lineTotal = ex.PricePence * int64(sel.Quantity) * int64(totalHeadcount) * int64(preview.Nights)
		default:
			lineTotal = ex.PricePence * int64(sel.Quantity)
		}
		lineTax := int64(0)
		if ex.TaxRateBPS > 0 {
			lineTax = (lineTotal*int64(ex.TaxRateBPS) + 5000) / 10000
			taxByRate[ex.TaxRateBPS] += lineTotal
		}
		extrasSubtotal += lineTotal
		extraLines = append(extraLines, QuoteExtraLine{
			ExtraID:      ex.ID,
			Code:         ex.Code,
			Name:         ex.Name,
			PricingModel: ex.PricingModel,
			Quantity:     sel.Quantity,
			UnitPence:    ex.PricePence,
			TotalPence:   lineTotal,
			TaxPence:     lineTax,
		})
	}

	var taxTotal int64
	var taxLines []TaxLine
	for bps, taxable := range taxByRate {
		taxAmt := (taxable*int64(bps) + 5000) / 10000
		taxTotal += taxAmt
		taxLines = append(taxLines, TaxLine{
			Label:        fmt.Sprintf("VAT / Tax (%.2f%%)", float64(bps)/100.0),
			RateBPS:      bps,
			TaxablePence: taxable,
			TaxPence:     taxAmt,
		})
	}

	grandTotal := preview.TotalPence + surcharge + extrasSubtotal + taxTotal
	depositDue, balanceDue := ComputeDeposit(policy, grandTotal)

	var balanceDueDateStr string
	if balanceDue > 0 {
		dueDate := startDate.AddDate(0, 0, -policy.BalanceDueDaysBeforeArrival)
		if dueDate.Before(nowUTC) {
			// Arrival is inside balance window: collect full payment at checkout.
			depositDue = grandTotal
			balanceDue = 0
		} else {
			balanceDueDateStr = FormatDate(dueDate)
		}
	}

	rawHashInput := fmt.Sprintf("%s|%s|%s|%s|%d|%d|%d|%d|%d|%d|%d",
		prop.ID, ut.ID, preview.StartDate, preview.EndDate,
		adults, children, infants, grandTotal, depositDue, policy.Version, len(extraLines),
	)
	sum := sha256.Sum256([]byte(rawHashInput))

	return BookingQuote{
		PropertyID:                 prop.ID,
		UnitTypeID:                 ut.ID,
		UnitTypeCode:               ut.Code,
		UnitTypeName:               ut.Name,
		RatePlanID:                 plan.ID,
		RatePlanName:               plan.Name,
		Currency:                   prop.Currency,
		StartDate:                  preview.StartDate,
		EndDate:                    preview.EndDate,
		Nights:                     preview.Nights,
		Adults:                     adults,
		Children:                   children,
		Infants:                    infants,
		NightlyBreakdown:           preview.Breakdown,
		ExtrasBreakdown:            extraLines,
		TaxBreakdown:               taxLines,
		AccommodationSubtotalPence: preview.TotalPence,
		OccupancySurchargePence:    surcharge,
		ExtrasSubtotalPence:        extrasSubtotal,
		DiscountPence:              0,
		TaxPence:                   taxTotal,
		TotalPence:                 grandTotal,
		DepositMode:                policy.DepositMode,
		DepositDuePence:            depositDue,
		BalanceDuePence:            balanceDue,
		BalanceDueDate:             balanceDueDateStr,
		PolicyVersion:              policy.Version,
		QuoteHash:                  hex.EncodeToString(sum[:]),
		ExpiresAt:                  nowUTC.Add(30 * time.Minute),
	}, nil
}

func ComputeDeposit(policy PropertyBookingPolicy, totalPence int64) (int64, int64) {
	if totalPence <= 0 {
		return 0, 0
	}
	var deposit int64
	switch policy.DepositMode {
	case DepositModeFull:
		deposit = totalPence
	case DepositModeFixed:
		deposit = policy.DepositFixedPence
		if deposit > totalPence || deposit <= 0 {
			deposit = totalPence
		}
	default: // DepositModePercentage
		if policy.DepositPercentage <= 0 || policy.DepositPercentage >= 100 {
			deposit = totalPence
		} else {
			deposit = (totalPence*int64(policy.DepositPercentage) + 50) / 100
			if deposit <= 0 {
				deposit = totalPence
			}
		}
	}
	return deposit, totalPence - deposit
}

func ValidateCustomerInfo(name, email, phone string, acceptedTerms bool) error {
	errs := map[string]string{}
	if strings.TrimSpace(name) == "" {
		errs["guest_name"] = "full name is required"
	}
	email = strings.TrimSpace(email)
	if email == "" {
		errs["guest_email"] = "email address is required"
	} else if _, err := mail.ParseAddress(email); err != nil {
		errs["guest_email"] = "must be a valid email address"
	}
	if len(strings.TrimSpace(phone)) < 6 {
		errs["guest_phone"] = "valid telephone number is required"
	}
	if !acceptedTerms {
		errs["accept_terms"] = "you must accept the booking terms and privacy notice"
	}
	return NewValidationError(errs)
}
