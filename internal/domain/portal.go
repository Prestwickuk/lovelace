package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

type PortalSessionScope string

const (
	PortalScopeBooking PortalSessionScope = "BOOKING"
	PortalScopeAccount PortalSessionScope = "ACCOUNT"
)

type PortalChallengePurpose string

const (
	PortalChallengeLogin       PortalChallengePurpose = "LOGIN"
	PortalChallengeVerifyEmail PortalChallengePurpose = "VERIFY_EMAIL"
	PortalChallengeStepUp      PortalChallengePurpose = "STEP_UP"
)

type PortalIdentityStatus string

const (
	PortalIdentityActive    PortalIdentityStatus = "ACTIVE"
	PortalIdentitySuspended PortalIdentityStatus = "SUSPENDED"
)

// Portal session, challenge, and validation bounds (Owner-Confirmed D8 & Section 6.1).
const (
	PortalChallengeTTL            = 15 * time.Minute
	PortalChallengeMaxAttempts    = 5
	PortalTierAIdleTTL            = 2 * time.Hour
	PortalTierAAbsoluteTTL        = 12 * time.Hour
	PortalTierBIdleTTL            = 60 * time.Minute
	PortalTierBAbsoluteTTL        = 7 * 24 * time.Hour
	PortalStepUpFreshnessWindow   = 15 * time.Minute
	PortalMaxOpenChangeRequests   = 3
	PortalMaxETARunes             = 32
	PortalMaxArrivalNotesRunes    = 500
	PortalMaxChangeNotesRunes     = 1000
	PortalMaxEnquiryMessageRunes  = 2000
	PortalMaxEnquirySubjectRunes  = 160
	PortalMaxPreferencesCount     = 20
	PortalMaxPreferenceKeyRunes   = 64
	PortalMaxPreferenceValueRunes = 200
	PortalSARSubjectPrefix        = "[Privacy Rights — SAR]"
	PortalErasureSubjectPrefix    = "[Privacy Rights — Erasure]"
)

var (
	// Accepts 24-hour HH:MM or bounded arrival window HH:MM-HH:MM (<= 32 chars).
	portalETARegex   = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d(\s*-\s*([01]\d|2[0-3]):[0-5]\d)?$`)
	portalPrefKeyRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	portalOutboxTpls = map[string]bool{
		"portal_verification":        true,
		"portal_step_up":             true,
		"portal_email_change_verify": true,
	}
)

// IsPortalSecurityChallengeTemplate reports whether an email_outbox template carries a
// single-use portal security credential whose body must never enter guest_communications
// and must be scrubbed from email_outbox upon dispatch or expiry.
func IsPortalSecurityChallengeTemplate(templateCode string) bool {
	return portalOutboxTpls[strings.ToLower(strings.TrimSpace(templateCode))]
}

// IsPrivacyRightsEnquiry reports whether a GuestEnquiry is a tracked SAR or Erasure rights
// request whose details and status must not be surfaced in portal enquiry endpoints while
// Section 7.2 identity-assurance and fulfilment gates remain open.
func IsPrivacyRightsEnquiry(e GuestEnquiry) bool {
	subj := strings.TrimSpace(e.Subject)
	return strings.HasPrefix(subj, PortalSARSubjectPrefix) ||
		strings.HasPrefix(subj, PortalErasureSubjectPrefix) ||
		strings.HasPrefix(subj, "[GDPR Art.")
}

// GuestPortalIdentity represents the DB-persisted per-property verified portal identity.
type GuestPortalIdentity struct {
	ID                      uuid.UUID
	PropertyID              uuid.UUID
	CanonicalGuestID        uuid.UUID
	VerifiedEmail           string
	NormalizedVerifiedEmail string
	PendingEmail            string
	PendingNormalizedEmail  string
	PendingEmailExpiresAt   *time.Time
	EmailVerifiedAt         time.Time
	LastLoginAt             *time.Time
	Status                  PortalIdentityStatus
	Version                 int
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

// GuestPortalChallenge represents a single-use hashed OTP / magic-link challenge.
// Stores zero plaintext secrets and zero IP/user-agent hashes (Section 7.2).
type GuestPortalChallenge struct {
	ID               uuid.UUID
	PropertyID       uuid.UUID
	PortalIdentityID *uuid.UUID
	GuestID          *uuid.UUID
	ReservationID    *uuid.UUID
	NormalizedEmail  string
	Purpose          PortalChallengePurpose
	OTPHash          string
	TokenHash        string
	BrowserNonceHash string
	BookingReference string
	FailedAttempts   int
	MaxAttempts      int
	ExpiresAt        time.Time
	ConsumedAt       *time.Time
	InvalidatedAt    *time.Time
	CreatedAt        time.Time
}

// GuestPortalSession represents an active server-side public portal session.
// Stores zero IP/user-agent hashes (Section 7.2).
type GuestPortalSession struct {
	ID                  uuid.UUID
	PropertyID          uuid.UUID
	PortalIdentityID    *uuid.UUID
	ScopedReservationID *uuid.UUID
	ScopeType           PortalSessionScope
	SessionTokenHash    string
	CSRFSecretHash      string
	VerifiedAt          time.Time
	IdleExpiresAt       time.Time
	AbsoluteExpiresAt   time.Time
	RevokedAt           *time.Time
	LastSeenAt          time.Time
	CreatedAt           time.Time
}

// --- Strictly Public-Safe Portal DTOs (Zero Admin/Internal CRM Fields) ---

type PortalSessionGuestSummary struct {
	FullName              string `json:"full_name"`
	VerifiedEmail         string `json:"verified_email"`
	PendingEmail          string `json:"pending_email,omitempty"`
	PreferredLanguage     string `json:"preferred_language"`
	IsDoNotContact        bool   `json:"is_do_not_contact"`
	MarketingConsentState string `json:"marketing_consent_state"`
}

type PortalSessionView struct {
	Authenticated          bool                       `json:"authenticated"`
	PropertySlug           string                     `json:"property_slug"`
	PropertyName           string                     `json:"property_name"`
	ScopeType              PortalSessionScope         `json:"scope_type"`
	ScopedBookingReference string                     `json:"scoped_booking_reference,omitempty"`
	CSRFToken              string                     `json:"csrf_token"`
	VerifiedAt             time.Time                  `json:"verified_at"`
	StepUpFresh            bool                       `json:"step_up_fresh"`
	IdleExpiresAt          time.Time                  `json:"idle_expires_at"`
	AbsoluteExpiresAt      time.Time                  `json:"absolute_expires_at"`
	Guest                  *PortalSessionGuestSummary `json:"guest,omitempty"`
}

type PortalBookingUnitView struct {
	UnitName     string `json:"unit_name"`
	UnitTypeName string `json:"unit_type_name"`
	Adults       int    `json:"adults"`
	Children     int    `json:"children"`
	Infants      int    `json:"infants"`
}

type PortalBookingSummary struct {
	Reference             string                  `json:"reference"`
	Status                ReservationStatus       `json:"status"`
	ChannelCode           string                  `json:"channel_code"`
	IsOTAManaged          bool                    `json:"is_ota_managed"`
	BillingNotice         string                  `json:"billing_notice,omitempty"`
	ArrivalDate           string                  `json:"arrival_date"`
	DepartureDate         string                  `json:"departure_date"`
	Nights                int                     `json:"nights"`
	CheckInTime           string                  `json:"check_in_time"`
	CheckOutTime          string                  `json:"check_out_time"`
	Adults                int                     `json:"adults"`
	Children              int                     `json:"children"`
	Infants               int                     `json:"infants"`
	Units                 []PortalBookingUnitView `json:"units"`
	PaymentStatus         string                  `json:"payment_status,omitempty"`
	TotalPence            int64                   `json:"total_pence,omitempty"`
	DepositPence          int64                   `json:"deposit_pence,omitempty"`
	AmountPaidPence       int64                   `json:"amount_paid_pence,omitempty"`
	BalanceDuePence       int64                   `json:"balance_due_pence,omitempty"`
	Currency              string                  `json:"currency,omitempty"`
	EstimatedArrivalTime  string                  `json:"estimated_arrival_time,omitempty"`
	GuestArrivalNotes     string                  `json:"guest_arrival_notes,omitempty"`
	CancellationRequested bool                    `json:"cancellation_requested"`
	CanUpdateArrival      bool                    `json:"can_update_arrival"`
	CanRequestChange      bool                    `json:"can_request_change"`
	CanRequestCancel      bool                    `json:"can_request_cancel"`
}

type PortalPaymentSummaryView struct {
	AmountPence int64      `json:"amount_pence"`
	Currency    string     `json:"currency"`
	Status      string     `json:"status"`
	PaidAt      *time.Time `json:"paid_at,omitempty"`
}

type PortalQuoteLineItemView struct {
	Description string `json:"description"`
	Quantity    int    `json:"quantity"`
	UnitPence   int64  `json:"unit_pence"`
	TotalPence  int64  `json:"total_pence"`
}

type PortalQuoteSummaryView struct {
	Currency      string                    `json:"currency"`
	Nights        int                       `json:"nights"`
	SubtotalPence int64                     `json:"subtotal_pence"`
	ExtrasPence   int64                     `json:"extras_pence"`
	TaxPence      int64                     `json:"tax_pence"`
	TotalPence    int64                     `json:"total_pence"`
	DepositPence  int64                     `json:"deposit_pence"`
	BalancePence  int64                     `json:"balance_pence"`
	LineItems     []PortalQuoteLineItemView `json:"line_items,omitempty"`
}

type PortalBookingDetail struct {
	PortalBookingSummary
	PropertyName       string                     `json:"property_name"`
	PropertySlug       string                     `json:"property_slug"`
	ContactEmail       string                     `json:"contact_email"`
	ContactPhone       string                     `json:"contact_phone"`
	CancellationPolicy string                     `json:"cancellation_policy"`
	QuietHours         string                     `json:"quiet_hours,omitempty"`
	FirePolicy         string                     `json:"fire_policy,omitempty"`
	PetPolicy          string                     `json:"pet_policy,omitempty"`
	Quote              *PortalQuoteSummaryView    `json:"quote,omitempty"`
	Payments           []PortalPaymentSummaryView `json:"payments,omitempty"`
}

type PortalGuestPreferenceView struct {
	Category        GuestPreferenceCategory `json:"category"`
	PreferenceKey   string                  `json:"preference_key"`
	PreferenceValue string                  `json:"preference_value"`
}

type PortalConsentEntryView struct {
	ConsentType string        `json:"consent_type"`
	Status      ConsentStatus `json:"status"`
	Method      string        `json:"method"`
	OccurredAt  time.Time     `json:"occurred_at"`
}

type PortalProfileView struct {
	FirstName              string                      `json:"first_name"`
	LastName               string                      `json:"last_name"`
	FullName               string                      `json:"full_name"`
	VerifiedEmail          string                      `json:"verified_email"`
	PendingEmail           string                      `json:"pending_email,omitempty"`
	PendingEmailExpiresAt  *time.Time                  `json:"pending_email_expires_at,omitempty"`
	Phone                  string                      `json:"phone"`
	PreferredContactMethod string                      `json:"preferred_contact_method"`
	PreferredLanguage      string                      `json:"preferred_language"`
	IsDoNotContact         bool                        `json:"is_do_not_contact"`
	MarketingConsentState  ConsentStatus               `json:"marketing_consent_state"`
	ConsentHistory         []PortalConsentEntryView    `json:"consent_history"`
	Preferences            []PortalGuestPreferenceView `json:"preferences"`
}

type PortalEnquiryMessageView struct {
	ID         uuid.UUID     `json:"id"`
	Direction  CommDirection `json:"direction"`
	SenderRole string        `json:"sender_role"` // "GUEST" or "PROPERTY"
	SenderName string        `json:"sender_name"`
	Body       string        `json:"body"`
	CreatedAt  time.Time     `json:"created_at"`
}

type PortalEnquiryView struct {
	ID                   uuid.UUID                  `json:"id"`
	Reference            string                     `json:"reference"`
	ReservationReference string                     `json:"reservation_reference,omitempty"`
	Category             EnquiryCategory            `json:"category"`
	Status               EnquiryStatus              `json:"status"`
	Subject              string                     `json:"subject"`
	Description          string                     `json:"description"`
	CreatedAt            time.Time                  `json:"created_at"`
	UpdatedAt            time.Time                  `json:"updated_at"`
	Messages             []PortalEnquiryMessageView `json:"messages,omitempty"`
}

// PortalPrivacyRequestReceipt is the uniform Section 7.2 response returned when an
// authenticated guest submits a tracked SAR or Erasure request. It intentionally omits
// request IDs, internal status, or prior request state.
type PortalPrivacyRequestReceipt struct {
	Accepted bool   `json:"accepted"`
	Message  string `json:"message"`
}

// IsOTAProxyEmail detects synthetic/relay OTA proxy email addresses that do not represent
// a guest's personal email inbox (Owner-Confirmed D4).
func IsOTAProxyEmail(email string) bool {
	norm := NormalizeEmail(email)
	if norm == "" {
		return true
	}
	at := strings.LastIndexByte(norm, '@')
	if at <= 0 || at == len(norm)-1 {
		return true
	}
	host := norm[at+1:]
	proxySuffixes := []string{
		"guest.booking.com",
		"airbnb.com",
		"guest.airbnb.com",
		"expediapartnercentral.com",
		"m.expediapartnercentral.com",
		"vrbo.com",
		"homeaway.com",
		"pitchup.com",
		"hipcamp.com",
		"campsites.co.uk",
		"smoobu.com",
		"beds24.com",
		"lodgify.com",
		"channex.io",
		".invalid",
		".local",
	}
	for _, suf := range proxySuffixes {
		if strings.HasPrefix(suf, ".") {
			if strings.HasSuffix(host, suf) {
				return true
			}
			continue
		}
		if host == suf || strings.HasSuffix(host, "."+suf) {
			return true
		}
	}
	return false
}

// ValidatePortalArrivalDetails validates optional HH:MM (or HH:MM-HH:MM) estimated arrival time
// and single-paragraph guest arrival notes (<= 500 chars).
func ValidatePortalArrivalDetails(eta, notes string) (cleanETA, cleanNotes string, err error) {
	errs := map[string]string{}
	cleanETA = strings.TrimSpace(eta)
	if utf8.RuneCountInString(cleanETA) > PortalMaxETARunes || (cleanETA != "" && !portalETARegex.MatchString(cleanETA)) {
		errs["estimated_arrival_time"] = "must be empty or 24-hour HH:MM (e.g. 16:30 or 16:00-17:00)"
	}
	cleanNotes = strings.TrimSpace(notes)
	if utf8.RuneCountInString(cleanNotes) > PortalMaxArrivalNotesRunes {
		errs["guest_arrival_notes"] = "must be 500 characters or fewer"
	}
	if strings.Contains(cleanNotes, "\n\n") || strings.Contains(cleanNotes, "\r\n\r\n") {
		errs["guest_arrival_notes"] = "must be a single paragraph"
	}
	for _, r := range cleanNotes {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			errs["guest_arrival_notes"] = "contains unsupported control characters"
			break
		}
	}
	cleanNotes = strings.Join(strings.Fields(cleanNotes), " ")
	if len(errs) > 0 {
		return "", "", NewValidationError(errs)
	}
	return cleanETA, cleanNotes, nil
}

// ValidatePortalProfileUpdate validates self-service contact & language updates (D10).
func ValidatePortalProfileUpdate(phone, contactMethod, preferredLang string) (cleanPhone, cleanMethod, cleanLang string, err error) {
	errs := map[string]string{}
	cleanPhone = strings.TrimSpace(phone)
	if utf8.RuneCountInString(cleanPhone) > 32 {
		errs["phone"] = "must be 32 characters or fewer"
	}
	for _, r := range cleanPhone {
		if unicode.IsControl(r) {
			errs["phone"] = "contains invalid characters"
			break
		}
	}
	cleanMethod = strings.ToUpper(strings.TrimSpace(contactMethod))
	if cleanMethod == "" {
		cleanMethod = string(ContactMethodEmail)
	}
	switch ContactMethod(cleanMethod) {
	case ContactMethodEmail, ContactMethodPhone, ContactMethodSMS, ContactMethodPost:
	default:
		errs["preferred_contact_method"] = "must be EMAIL, PHONE, SMS, or POST"
	}

	cleanLang = strings.ToLower(strings.TrimSpace(preferredLang))
	if cleanLang == "" {
		cleanLang = "en"
	}
	switch cleanLang {
	case "en", "cy":
	default:
		errs["preferred_language"] = "must be en or cy"
	}
	if len(errs) > 0 {
		return "", "", "", NewValidationError(errs)
	}
	return cleanPhone, cleanMethod, cleanLang, nil
}

// ValidatePortalPreferences validates guest-safe structured preferences (ARRIVAL, ACCESSIBILITY,
// COMMUNICATION, PET; <= 20 items; <= 200 chars; zero internal notes).
func ValidatePortalPreferences(items []PortalGuestPreferenceView) ([]PortalGuestPreferenceView, error) {
	if len(items) > PortalMaxPreferencesCount {
		return nil, NewValidationError(map[string]string{
			"preferences": "at most 20 structured preferences are permitted",
		})
	}
	seen := make(map[string]bool, len(items))
	out := make([]PortalGuestPreferenceView, 0, len(items))
	for _, item := range items {
		cat := GuestPreferenceCategory(strings.ToUpper(strings.TrimSpace(string(item.Category))))
		switch cat {
		case PrefCatArrival, PrefCatAccessibility, PrefCatCommunication, PrefCatPet:
		default:
			return nil, NewValidationError(map[string]string{
				"preferences": "category must be ARRIVAL, ACCESSIBILITY, COMMUNICATION, or PET",
			})
		}
		key := strings.ToLower(strings.TrimSpace(item.PreferenceKey))
		if !portalPrefKeyRe.MatchString(key) || utf8.RuneCountInString(key) > PortalMaxPreferenceKeyRunes {
			return nil, NewValidationError(map[string]string{
				"preferences": "preference_key must be lowercase alphanumeric/underscore/hyphen (1..64 chars)",
			})
		}
		val := strings.TrimSpace(item.PreferenceValue)
		if val == "" || utf8.RuneCountInString(val) > PortalMaxPreferenceValueRunes {
			return nil, NewValidationError(map[string]string{
				"preferences": "preference_value must be 1..200 characters",
			})
		}
		for _, r := range val {
			if unicode.IsControl(r) {
				return nil, NewValidationError(map[string]string{
					"preferences": "preference_value contains invalid control characters",
				})
			}
		}
		dedup := string(cat) + ":" + key
		if seen[dedup] {
			return nil, NewValidationError(map[string]string{
				"preferences": "duplicate preference category and key",
			})
		}
		seen[dedup] = true
		out = append(out, PortalGuestPreferenceView{
			Category:        cat,
			PreferenceKey:   key,
			PreferenceValue: val,
		})
	}
	return out, nil
}

// ValidatePortalMessageText validates bounded customer enquiry subjects/bodies.
func ValidatePortalMessageText(field, text string, maxRunes int, required bool) (string, error) {
	clean := strings.TrimSpace(text)
	if required && clean == "" {
		return "", NewValidationError(map[string]string{field: "is required"})
	}
	if utf8.RuneCountInString(clean) > maxRunes {
		return "", NewValidationError(map[string]string{field: "exceeds maximum allowed length"})
	}
	for _, r := range clean {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return "", NewValidationError(map[string]string{field: "contains invalid control characters"})
		}
	}
	return clean, nil
}
