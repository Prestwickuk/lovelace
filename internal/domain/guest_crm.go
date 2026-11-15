package domain

import (
	"encoding/json"
	"fmt"
	"html"
	"net/mail"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

type GuestStatus string

const (
	GuestStatusActive       GuestStatus = "ACTIVE"
	GuestStatusVIP          GuestStatus = "VIP"
	GuestStatusDoNotContact GuestStatus = "DO_NOT_CONTACT"
	GuestStatusMerged       GuestStatus = "MERGED"
	GuestStatusAnonymized   GuestStatus = "ANONYMIZED"
)

type ContactMethod string

const (
	ContactMethodEmail ContactMethod = "EMAIL"
	ContactMethodPhone ContactMethod = "PHONE"
	ContactMethodSMS   ContactMethod = "SMS"
	ContactMethodPost  ContactMethod = "POST"
)

type GuestPreferenceCategory string

const (
	PrefCatAccommodation GuestPreferenceCategory = "ACCOMMODATION"
	PrefCatPet           GuestPreferenceCategory = "PET"
	PrefCatArrival       GuestPreferenceCategory = "ARRIVAL"
	PrefCatEquipment     GuestPreferenceCategory = "EQUIPMENT"
	PrefCatCommunication GuestPreferenceCategory = "COMMUNICATION"
	PrefCatAccessibility GuestPreferenceCategory = "ACCESSIBILITY"
	PrefCatOther         GuestPreferenceCategory = "OTHER"
)

type GuestNoteCategory string

const (
	NoteCatGeneral         GuestNoteCategory = "GENERAL"
	NoteCatBooking         GuestNoteCategory = "BOOKING"
	NoteCatPreference      GuestNoteCategory = "PREFERENCE"
	NoteCatCustomerService GuestNoteCategory = "CUSTOMER_SERVICE"
	NoteCatOperational     GuestNoteCategory = "OPERATIONAL"
)

type EnquiryCategory string

const (
	EnqCatGeneral          EnquiryCategory = "GENERAL"
	EnqCatBooking          EnquiryCategory = "BOOKING"
	EnqCatAvailability     EnquiryCategory = "AVAILABILITY"
	EnqCatAccommodation    EnquiryCategory = "ACCOMMODATION"
	EnqCatFacilities       EnquiryCategory = "FACILITIES"
	EnqCatArrivalDeparture EnquiryCategory = "ARRIVAL_DEPARTURE"
	EnqCatSpecialRequest   EnquiryCategory = "SPECIAL_REQUEST"
	EnqCatGroupBooking     EnquiryCategory = "GROUP_BOOKING"
	EnqCatCancellation     EnquiryCategory = "CANCELLATION"
	EnqCatComplaint        EnquiryCategory = "COMPLAINT"
	EnqCatLostProperty     EnquiryCategory = "LOST_PROPERTY"
	EnqCatOther            EnquiryCategory = "OTHER"
)

type EnquiryStatus string

const (
	EnqStatusNew                EnquiryStatus = "NEW"
	EnqStatusTriaged            EnquiryStatus = "TRIAGED"
	EnqStatusInProgress         EnquiryStatus = "IN_PROGRESS"
	EnqStatusWaitingForGuest    EnquiryStatus = "WAITING_FOR_GUEST"
	EnqStatusWaitingForProperty EnquiryStatus = "WAITING_FOR_PROPERTY"
	EnqStatusResolved           EnquiryStatus = "RESOLVED"
	EnqStatusClosed             EnquiryStatus = "CLOSED"
)

type EnquiryPriority string

const (
	EnqPriorityLow    EnquiryPriority = "LOW"
	EnqPriorityNormal EnquiryPriority = "NORMAL"
	EnqPriorityHigh   EnquiryPriority = "HIGH"
	EnqPriorityUrgent EnquiryPriority = "URGENT"
)

type EnquirySource string

const (
	EnqSourcePhone    EnquirySource = "PHONE"
	EnqSourceInPerson EnquirySource = "IN_PERSON"
	EnqSourceEmail    EnquirySource = "EMAIL"
	EnqSourceWeb      EnquirySource = "WEB"
	EnqSourceAdmin    EnquirySource = "ADMIN"
)

type InteractionType string

const (
	InteractionEmailOutbound      InteractionType = "EMAIL_OUTBOUND"
	InteractionEmailInbound       InteractionType = "EMAIL_INBOUND"
	InteractionPhoneCall          InteractionType = "PHONE_CALL"
	InteractionInPerson           InteractionType = "IN_PERSON"
	InteractionWebsiteEnquiry     InteractionType = "WEBSITE_ENQUIRY"
	InteractionBookingNotice      InteractionType = "BOOKING_NOTIFICATION"
	InteractionSystemNotification InteractionType = "SYSTEM_NOTIFICATION"
	InteractionComplaint          InteractionType = "COMPLAINT"
)

type CommDirection string

const (
	CommDirInbound  CommDirection = "INBOUND"
	CommDirOutbound CommDirection = "OUTBOUND"
	CommDirInternal CommDirection = "INTERNAL"
)

type DeliveryStatus string

const (
	DeliveryDraft     DeliveryStatus = "DRAFT"
	DeliveryQueued    DeliveryStatus = "QUEUED"
	DeliverySending   DeliveryStatus = "SENDING"
	DeliveryAccepted  DeliveryStatus = "ACCEPTED"
	DeliveryDelivered DeliveryStatus = "DELIVERED"
	DeliveryBounced   DeliveryStatus = "BOUNCED"
	DeliveryFailed    DeliveryStatus = "FAILED"
	DeliveryCancelled DeliveryStatus = "CANCELLED"
	DeliveryLogged    DeliveryStatus = "LOGGED"
)

type AutomationTrigger string

const (
	TriggerBookingConfirmation   AutomationTrigger = "BOOKING_CONFIRMATION"
	TriggerPreArrival            AutomationTrigger = "PRE_ARRIVAL"
	TriggerArrivalInfo           AutomationTrigger = "ARRIVAL_INFO"
	TriggerDepartureInstructions AutomationTrigger = "DEPARTURE_INSTRUCTIONS"
	TriggerPostStayThankYou      AutomationTrigger = "POST_STAY_THANK_YOU"
	TriggerPaymentReminder       AutomationTrigger = "PAYMENT_REMINDER"
)

type ConsentStatus string

const (
	ConsentNotSubscribed ConsentStatus = "NOT_SUBSCRIBED"
	ConsentGranted       ConsentStatus = "GRANTED"
	ConsentWithdrawn     ConsentStatus = "WITHDRAWN"
)

// ValidateOperationalGuestStatus ensures generic create/update paths only accept
// operational statuses; MERGED and ANONYMIZED are restricted to dedicated workflows.
func ValidateOperationalGuestStatus(status GuestStatus) error {
	switch status {
	case GuestStatusActive, GuestStatusVIP, GuestStatusDoNotContact:
		return nil
	default:
		return NewValidationError(map[string]string{
			"status": "status must be ACTIVE, VIP, or DO_NOT_CONTACT; MERGED and ANONYMIZED require dedicated workflows",
		})
	}
}

// GuestDirectoryFilter configures searchable, segmentable guest CRM directory listings.
type GuestDirectoryFilter struct {
	Query      string
	Segment    string // ALL, NEW, RETURNING, UPCOMING, OUTSTANDING_REQUESTS, UNRESOLVED_COMPLAINTS, OPTED_IN
	Status     string
	IncludeAll bool // include MERGED/ANONYMIZED when explicitly requested
	Limit      int
	Offset     int
	NoLimit    bool
}

// GuestListItem represents a guest row in the CRM directory with stay & service metrics.
type GuestListItem struct {
	Guest
	TotalBookings         int        `json:"total_bookings"`
	CompletedStays        int        `json:"completed_stays"`
	UpcomingStays         int        `json:"upcoming_stays"`
	CancelledBookings     int        `json:"cancelled_bookings"`
	IsReturningGuest      bool       `json:"is_returning_guest"`
	LastStayDate          *time.Time `json:"last_stay_date,omitempty"`
	NextArrivalDate       *time.Time `json:"next_arrival_date,omitempty"`
	OpenEnquiriesCount    int        `json:"open_enquiries_count"`
	OpenComplaintsCount   int        `json:"open_complaints_count"`
	TotalRevenuePence     int64      `json:"total_revenue_pence,omitempty"`
	MarketingConsentState string     `json:"marketing_consent_state"`
}

// GuestDuplicateCandidate identifies a probable duplicate guest record and why it matched.
type GuestDuplicateCandidate struct {
	GuestA       GuestListItem `json:"guest_a"`
	GuestB       GuestListItem `json:"guest_b"`
	Score        int           `json:"score"`
	MatchReasons []string      `json:"match_reasons"`
}

// DuplicateScanResult reports detected duplicates together with explicit scan completeness metadata.
type DuplicateScanResult struct {
	Candidates        []GuestDuplicateCandidate `json:"candidates"`
	ScannedGuests     int                       `json:"scanned_guests"`
	TotalActiveGuests int                       `json:"total_active_guests"`
	IsComplete        bool                      `json:"is_complete"`
}

// GuestPreference stores an operational preference reusable across stays.
type GuestPreference struct {
	ID              uuid.UUID               `json:"id"`
	PropertyID      uuid.UUID               `json:"property_id"`
	GuestID         uuid.UUID               `json:"guest_id"`
	Category        GuestPreferenceCategory `json:"category"`
	PreferenceKey   string                  `json:"preference_key"`
	PreferenceValue string                  `json:"preference_value"`
	Notes           string                  `json:"notes"`
	CreatedBy       *uuid.UUID              `json:"created_by,omitempty"`
	UpdatedBy       *uuid.UUID              `json:"updated_by,omitempty"`
	CreatedAt       time.Time               `json:"created_at"`
	UpdatedAt       time.Time               `json:"updated_at"`
}

// GuestNote is an internal-only staff CRM note never exposed publicly or in emails.
type GuestNote struct {
	ID            uuid.UUID         `json:"id"`
	PropertyID    uuid.UUID         `json:"property_id"`
	GuestID       uuid.UUID         `json:"guest_id"`
	ReservationID *uuid.UUID        `json:"reservation_id,omitempty"`
	Category      GuestNoteCategory `json:"category"`
	Content       string            `json:"content"`
	IsPinned      bool              `json:"is_pinned"`
	AuthorName    string            `json:"author_name,omitempty"`
	CreatedBy     *uuid.UUID        `json:"created_by,omitempty"`
	UpdatedBy     *uuid.UUID        `json:"updated_by,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// GuestEnquiry represents an enquiry, special request, complaint, or lost-property ticket.
type GuestEnquiry struct {
	ID                   uuid.UUID       `json:"id"`
	PropertyID           uuid.UUID       `json:"property_id"`
	Reference            string          `json:"reference"`
	GuestID              *uuid.UUID      `json:"guest_id,omitempty"`
	GuestName            string          `json:"guest_name,omitempty"`
	ReservationID        *uuid.UUID      `json:"reservation_id,omitempty"`
	ReservationReference string          `json:"reservation_reference,omitempty"`
	MaintenanceRequestID *uuid.UUID      `json:"maintenance_request_id,omitempty"`
	MaintenanceReference string          `json:"maintenance_reference,omitempty"`
	Category             EnquiryCategory `json:"category"`
	Status               EnquiryStatus   `json:"status"`
	Priority             EnquiryPriority `json:"priority"`
	Source               EnquirySource   `json:"source"`
	Subject              string          `json:"subject"`
	Description          string          `json:"description"`
	RequesterName        string          `json:"requester_name"`
	RequesterEmail       string          `json:"requester_email"`
	RequesterPhone       string          `json:"requester_phone"`
	AssignedUserID       *uuid.UUID      `json:"assigned_user_id,omitempty"`
	AssignedUserName     string          `json:"assigned_user_name,omitempty"`
	TargetResponseAt     *time.Time      `json:"target_response_at,omitempty"`
	FollowUpAt           *time.Time      `json:"follow_up_at,omitempty"`
	ResolvedAt           *time.Time      `json:"resolved_at,omitempty"`
	ResolvedBy           *uuid.UUID      `json:"resolved_by,omitempty"`
	ResolutionNotes      string          `json:"resolution_notes"`
	ClosedAt             *time.Time      `json:"closed_at,omitempty"`
	ClosedBy             *uuid.UUID      `json:"closed_by,omitempty"`
	Version              int             `json:"version"`
	CreatedBy            *uuid.UUID      `json:"created_by,omitempty"`
	UpdatedBy            *uuid.UUID      `json:"updated_by,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

// GuestEnquiryFilter filters property enquiries.
type GuestEnquiryFilter struct {
	Query      string
	Status     string
	Category   string
	Priority   string
	AssignedTo *uuid.UUID
	GuestID    *uuid.UUID
	OpenOnly   bool
	Overdue    bool
	Limit      int
	NoLimit    bool
}

// GuestEnquiryMessage records inbound, outbound, or internal enquiry correspondence.
type GuestEnquiryMessage struct {
	ID          uuid.UUID     `json:"id"`
	PropertyID  uuid.UUID     `json:"property_id"`
	EnquiryID   uuid.UUID     `json:"enquiry_id"`
	Direction   CommDirection `json:"direction"`
	SenderName  string        `json:"sender_name"`
	SenderEmail string        `json:"sender_email"`
	Body        string        `json:"body"`
	IsInternal  bool          `json:"is_internal"`
	StaffUserID *uuid.UUID    `json:"staff_user_id,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
}

// GuestCommunication records an item on a guest's unified interaction timeline.
type GuestCommunication struct {
	ID                   uuid.UUID       `json:"id"`
	PropertyID           uuid.UUID       `json:"property_id"`
	GuestID              *uuid.UUID      `json:"guest_id,omitempty"`
	GuestName            string          `json:"guest_name,omitempty"`
	ReservationID        *uuid.UUID      `json:"reservation_id,omitempty"`
	ReservationReference string          `json:"reservation_reference,omitempty"`
	EnquiryID            *uuid.UUID      `json:"enquiry_id,omitempty"`
	InteractionType      InteractionType `json:"interaction_type"`
	Direction            CommDirection   `json:"direction"`
	Subject              string          `json:"subject"`
	BodyText             string          `json:"body_text"`
	BodyHTML             string          `json:"body_html,omitempty"`
	Summary              string          `json:"summary"`
	RecipientEmail       string          `json:"recipient_email"`
	SenderEmail          string          `json:"sender_email"`
	TemplateID           *uuid.UUID      `json:"template_id,omitempty"`
	TemplateVersionID    *uuid.UUID      `json:"template_version_id,omitempty"`
	RuleID               *uuid.UUID      `json:"rule_id,omitempty"`
	TriggerEvent         string          `json:"trigger_event,omitempty"`
	EmailOutboxID        *uuid.UUID      `json:"email_outbox_id,omitempty"`
	DeliveryStatus       DeliveryStatus  `json:"delivery_status"`
	ProviderMessageID    string          `json:"provider_message_id,omitempty"`
	FollowUpRequired     bool            `json:"follow_up_required"`
	FollowUpAssignedTo   *uuid.UUID      `json:"follow_up_assigned_to,omitempty"`
	FollowUpAt           *time.Time      `json:"follow_up_at,omitempty"`
	FollowUpCompletedAt  *time.Time      `json:"follow_up_completed_at,omitempty"`
	StaffUserID          *uuid.UUID      `json:"staff_user_id,omitempty"`
	StaffUserName        string          `json:"staff_user_name,omitempty"`
	OccurredAt           time.Time       `json:"occurred_at"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

// CommunicationDeliveryEvent logs SMTP/outbox/webhook delivery state transitions.
type CommunicationDeliveryEvent struct {
	ID              uuid.UUID       `json:"id"`
	PropertyID      uuid.UUID       `json:"property_id"`
	CommunicationID uuid.UUID       `json:"communication_id"`
	EmailOutboxID   *uuid.UUID      `json:"email_outbox_id,omitempty"`
	EventType       string          `json:"event_type"`
	Provider        string          `json:"provider"`
	ProviderEventID string          `json:"provider_event_id,omitempty"`
	Detail          string          `json:"detail"`
	Payload         json.RawMessage `json:"payload,omitempty"`
	OccurredAt      time.Time       `json:"occurred_at"`
}

// CommunicationTemplate defines an editable property-scoped message template.
type CommunicationTemplate struct {
	ID               uuid.UUID  `json:"id"`
	PropertyID       uuid.UUID  `json:"property_id"`
	Code             string     `json:"code"`
	Name             string     `json:"name"`
	Category         string     `json:"category"`
	Locale           string     `json:"locale"`
	SubjectTemplate  string     `json:"subject_template"`
	BodyTextTemplate string     `json:"body_text_template"`
	BodyHTMLTemplate string     `json:"body_html_template"`
	IsActive         bool       `json:"is_active"`
	Version          int        `json:"version"`
	CreatedBy        *uuid.UUID `json:"created_by,omitempty"`
	UpdatedBy        *uuid.UUID `json:"updated_by,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// CommunicationTemplateVersion stores an immutable snapshot of a template version.
type CommunicationTemplateVersion struct {
	ID               uuid.UUID  `json:"id"`
	PropertyID       uuid.UUID  `json:"property_id"`
	TemplateID       uuid.UUID  `json:"template_id"`
	Version          int        `json:"version"`
	SubjectTemplate  string     `json:"subject_template"`
	BodyTextTemplate string     `json:"body_text_template"`
	BodyHTMLTemplate string     `json:"body_html_template"`
	EditedBy         *uuid.UUID `json:"edited_by,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// CommunicationAutomationRule defines when an automated booking message fires.
type CommunicationAutomationRule struct {
	ID                 uuid.UUID         `json:"id"`
	PropertyID         uuid.UUID         `json:"property_id"`
	Name               string            `json:"name"`
	TriggerEvent       AutomationTrigger `json:"trigger_event"`
	OffsetHours        int               `json:"offset_hours"`
	SendHourLocal      *int              `json:"send_hour_local,omitempty"`
	TemplateID         uuid.UUID         `json:"template_id"`
	TemplateName       string            `json:"template_name,omitempty"`
	ApplicableStatuses []string          `json:"applicable_statuses"`
	IsActive           bool              `json:"is_active"`
	CreatedBy          *uuid.UUID        `json:"created_by,omitempty"`
	UpdatedBy          *uuid.UUID        `json:"updated_by,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

// CommunicationJob represents a concrete scheduled/processed automated send for a reservation.
type CommunicationJob struct {
	ID              uuid.UUID  `json:"id"`
	PropertyID      uuid.UUID  `json:"property_id"`
	RuleID          uuid.UUID  `json:"rule_id"`
	RuleName        string     `json:"rule_name,omitempty"`
	ReservationID   uuid.UUID  `json:"reservation_id"`
	ReservationRef  string     `json:"reservation_reference,omitempty"`
	GuestID         *uuid.UUID `json:"guest_id,omitempty"`
	GuestName       string     `json:"guest_name,omitempty"`
	TriggerEvent    string     `json:"trigger_event"`
	ScheduledForUTC time.Time  `json:"scheduled_for_utc"`
	StayFingerprint string     `json:"stay_fingerprint"`
	Status          string     `json:"status"`
	CommunicationID *uuid.UUID `json:"communication_id,omitempty"`
	EmailOutboxID   *uuid.UUID `json:"email_outbox_id,omitempty"`
	SkipReason      string     `json:"skip_reason,omitempty"`
	ProcessedAt     *time.Time `json:"processed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// GuestConsentRecord represents a timestamped marketing consent or withdrawal event.
type GuestConsentRecord struct {
	ID             uuid.UUID     `json:"id"`
	PropertyID     uuid.UUID     `json:"property_id"`
	GuestID        uuid.UUID     `json:"guest_id"`
	ConsentType    string        `json:"consent_type"`
	Status         ConsentStatus `json:"status"`
	Method         string        `json:"method"`
	Source         string        `json:"source"`
	PolicyVersion  string        `json:"policy_version"`
	Notes          string        `json:"notes"`
	IsNoteRedacted bool          `json:"is_note_redacted,omitempty"`
	NoteRedactedAt *time.Time    `json:"note_redacted_at,omitempty"`
	RecordedBy     *uuid.UUID    `json:"recorded_by,omitempty"`
	OccurredAt     time.Time     `json:"occurred_at"`
	CreatedAt      time.Time     `json:"created_at"`
}

// PropertyRetentionPolicy defines retention windows and optional anonymisation policy.
type PropertyRetentionPolicy struct {
	PropertyID                 uuid.UUID  `json:"property_id"`
	EnquiryRetentionDays       int        `json:"enquiry_retention_days"`
	InactiveGuestRetentionDays int        `json:"inactive_guest_retention_days"`
	CommsRetentionDays         int        `json:"comms_retention_days"`
	AutoAnonymizeEnabled       bool       `json:"auto_anonymize_enabled"`
	ExecutionMode              string     `json:"execution_mode,omitempty"`
	UpdatedBy                  *uuid.UUID `json:"updated_by,omitempty"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

// GuestMergeEvent captures the immutable audit record of a guest merge.
type GuestMergeEvent struct {
	ID            uuid.UUID       `json:"id"`
	PropertyID    uuid.UUID       `json:"property_id"`
	SourceGuestID uuid.UUID       `json:"source_guest_id"`
	TargetGuestID uuid.UUID       `json:"target_guest_id"`
	SourceSnap    json.RawMessage `json:"source_snapshot"`
	MovedCounts   json.RawMessage `json:"moved_counts"`
	Notes         string          `json:"notes"`
	MergedBy      *uuid.UUID      `json:"merged_by,omitempty"`
	MergedAt      time.Time       `json:"merged_at"`
}

// GuestDetailBundle is the full CRM view for a guest (permission-filtered in service layer).
type GuestDetailBundle struct {
	Guest                 GuestListItem        `json:"guest"`
	Reservations          []Reservation        `json:"reservations"`
	Preferences           []GuestPreference    `json:"preferences"`
	Notes                 []GuestNote          `json:"notes"`
	Enquiries             []GuestEnquiry       `json:"enquiries"`
	Communications        []GuestCommunication `json:"communications"`
	ConsentHistory        []GuestConsentRecord `json:"consent_history"`
	CurrentMarketingState ConsentStatus        `json:"current_marketing_state"`
	MergeHistory          []GuestMergeEvent    `json:"merge_history,omitempty"`
	ShowFinancialTotals   bool                 `json:"show_financial_totals"`
	ShowInternalNotes     bool                 `json:"show_internal_notes"`
	ShowCommunications    bool                 `json:"show_communications"`
	ShowEnquiries         bool                 `json:"show_enquiries"`
	ShowConsentHistory    bool                 `json:"show_consent_history"`
	OutboxFailures        []FailedEmailSummary `json:"outbox_failures,omitempty"`
}

// FailedEmailSummary summarizes a failed or bounced outbox email for staff triage.
type FailedEmailSummary struct {
	OutboxID        uuid.UUID  `json:"outbox_id"`
	CommunicationID *uuid.UUID `json:"communication_id,omitempty"`
	GuestID         *uuid.UUID `json:"guest_id,omitempty"`
	ReservationID   *uuid.UUID `json:"reservation_id,omitempty"`
	Template        string     `json:"template"`
	Recipient       string     `json:"recipient"`
	Subject         string     `json:"subject"`
	Status          string     `json:"status"`
	Attempts        int        `json:"attempts"`
	LastError       string     `json:"last_error"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// GuestDuplicateDismissal represents a reviewed non-duplicate guest pair record.
type GuestDuplicateDismissal struct {
	PropertyID     uuid.UUID  `json:"property_id"`
	GuestAID       uuid.UUID  `json:"guest_a_id"`
	GuestBID       uuid.UUID  `json:"guest_b_id"`
	Reason         string     `json:"reason"`
	ReasonCategory string     `json:"reason_category"`
	DismissedBy    *uuid.UUID `json:"dismissed_by,omitempty"`
	DismissedAt    time.Time  `json:"dismissed_at"`
}

// GuestDataExport is the structured GDPR / subject-access JSON bundle.
type GuestDataExport struct {
	ExportedAt          time.Time                    `json:"exported_at"`
	ExportScopeNotice   string                       `json:"export_scope_notice"`
	PropertyID          uuid.UUID                    `json:"property_id"`
	Guest               GuestListItem                `json:"guest"`
	Reservations        []Reservation                `json:"reservations"`
	BookingHolds        []BookingHold                `json:"booking_holds,omitempty"`
	Preferences         []GuestPreference            `json:"preferences"`
	Notes               []GuestNote                  `json:"notes,omitempty"`
	Enquiries           []GuestEnquiry               `json:"enquiries"`
	EnquiryMessages     []GuestEnquiryMessage        `json:"enquiry_messages,omitempty"`
	Communications      []GuestCommunication         `json:"communications"`
	DeliveryEvents      []CommunicationDeliveryEvent `json:"delivery_events,omitempty"`
	ConsentHistory      []GuestConsentRecord         `json:"consent_history"`
	MergeHistory        []GuestMergeEvent            `json:"merge_history,omitempty"`
	DuplicateDismissals []GuestDuplicateDismissal    `json:"duplicate_dismissals,omitempty"`
}

var (
	placeholderRegex = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)
	phoneKeepRegex   = regexp.MustCompile(`[^0-9+]`)
)

// AllowedTemplatePlaceholders defines the strict allowlist of variables available in message templates.
var AllowedTemplatePlaceholders = map[string]string{
	"guest_first_name":      "Guest first name",
	"guest_last_name":       "Guest last name",
	"guest_full_name":       "Guest full name",
	"property_name":         "Property name",
	"property_email":        "Property contact email",
	"property_phone":        "Property contact phone",
	"reservation_reference": "Reservation reference (e.g. TB-2026-0001)",
	"arrival_date":          "Check-in date (YYYY-MM-DD)",
	"departure_date":        "Check-out date (YYYY-MM-DD)",
	"check_in_time":         "Standard check-in time",
	"check_out_time":        "Standard check-out time",
	"unit_names":            "Assigned pitch or unit names",
	"adults":                "Number of adults",
	"children":              "Number of children",
	"infants":               "Number of infants",
	"total_amount":          "Formatted total amount",
	"balance_due":           "Formatted remaining balance due",
	"enquiry_reference":     "Enquiry reference (e.g. ENQ-2026-0001)",
	"enquiry_subject":       "Enquiry subject",
}

// NormalizeEmail trims whitespace and lowercases an email address for lookup consistency.
func NormalizeEmail(email string) string {
	return strings.TrimSpace(strings.ToLower(email))
}

// NormalizePhone removes spaces, hyphens, parentheses, and non-dial characters.
func NormalizePhone(phone string) string {
	cleaned := phoneKeepRegex.ReplaceAllString(strings.TrimSpace(phone), "")
	if strings.HasPrefix(cleaned, "00") && len(cleaned) > 2 {
		cleaned = "+" + cleaned[2:]
	}
	return cleaned
}

// SplitFullName derives first/last names from a full name while preserving provided split parts.
func SplitFullName(first, last, full string) (string, string, string) {
	first = strings.TrimSpace(first)
	last = strings.TrimSpace(last)
	full = strings.TrimSpace(full)
	if full == "" {
		full = strings.TrimSpace(strings.TrimSpace(first) + " " + strings.TrimSpace(last))
	}
	if first == "" && full != "" {
		parts := strings.Fields(full)
		if len(parts) > 0 {
			first = parts[0]
		}
		if len(parts) > 1 && last == "" {
			last = strings.Join(parts[1:], " ")
		}
	}
	if full != "" && last == "" {
		if idx := strings.IndexFunc(full, unicode.IsSpace); idx > 0 {
			last = strings.TrimSpace(full[idx+1:])
		}
	}
	return first, last, full
}

var (
	nonPIIEmailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	nonPIIPhonePattern = regexp.MustCompile(`(?:\+\d{1,3}[\s.\-]?\d[\d\s.\-]{6,}|\b0\d{2,4}[\s.\-]?\d{3,4}[\s.\-]?\d{3,6}\b|\(\d{3,5}\)\s*\d{3,})`)
	reasonCodePattern  = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_\-]{2,63}$`)
)

// ContainsPII reports whether val contains an email address or phone number pattern.
func ContainsPII(val string) bool {
	return nonPIIEmailPattern.MatchString(val) || nonPIIPhonePattern.MatchString(val)
}

// ValidateNonPIIText enforces bounded single-line text without embedded email addresses
// or phone numbers for audit redactions, consent redaction reasons, and reconciliation notes/refs.
func ValidateNonPIIText(field, val string, minLen, maxLen int) (string, error) {
	if strings.ContainsAny(val, "\r\n\x00") {
		return "", NewValidationError(map[string]string{field: "must be single-line without control characters"})
	}
	clean := strings.TrimSpace(val)
	runeCount := len([]rune(clean))
	if runeCount < minLen || runeCount > maxLen {
		return "", NewValidationError(map[string]string{
			field: fmt.Sprintf("must be between %d and %d characters", minLen, maxLen),
		})
	}
	if ContainsPII(clean) {
		return "", NewValidationError(map[string]string{
			field: "must not contain personal email addresses or phone numbers",
		})
	}
	return clean, nil
}

// ValidateReasonCode normalizes and validates an uppercase machine reason code (e.g. SMTP_PRE_DATA_RESET).
func ValidateReasonCode(field, code string) (string, error) {
	clean := strings.ToUpper(strings.TrimSpace(code))
	if !reasonCodePattern.MatchString(clean) {
		return "", NewValidationError(map[string]string{
			field: "must be 3..64 uppercase ASCII letters, digits, underscores, or hyphens",
		})
	}
	return clean, nil
}

// SanitizeEmailHeaderValue strips CR/LF and control bytes to prevent SMTP header injection.
func SanitizeEmailHeaderValue(v string) (string, error) {
	if strings.ContainsAny(v, "\r\n\x00") {
		return "", fmt.Errorf("%w: email header value contains forbidden line breaks", ErrValidation)
	}
	return strings.TrimSpace(v), nil
}

// ValidateEmailRecipient checks recipient format and guards against header injection.
func ValidateEmailRecipient(addr string) (string, error) {
	clean, err := SanitizeEmailHeaderValue(addr)
	if err != nil || clean == "" {
		return "", fmt.Errorf("%w: valid recipient email address is required", ErrValidation)
	}
	parsed, parseErr := mail.ParseAddress(clean)
	if parseErr != nil || parsed == nil || strings.TrimSpace(parsed.Address) == "" {
		return "", fmt.Errorf("%w: invalid recipient email address", ErrValidation)
	}
	return NormalizeEmail(parsed.Address), nil
}

// ValidateTemplatePlaceholders verifies that a template only uses allowlisted {{tokens}}.
func ValidateTemplatePlaceholders(subject, bodyText, bodyHTML string) error {
	unknown := map[string]bool{}
	for _, src := range []string{subject, bodyText, bodyHTML} {
		for _, m := range placeholderRegex.FindAllStringSubmatch(src, -1) {
			key := m[1]
			if _, ok := AllowedTemplatePlaceholders[key]; !ok {
				unknown[key] = true
			}
		}
	}
	if len(unknown) > 0 {
		keys := make([]string, 0, len(unknown))
		for k := range unknown {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return fmt.Errorf("%w: unsupported template placeholder(s): %s", ErrValidation, strings.Join(keys, ", "))
	}
	return nil
}

// RenderTemplate substitutes allowlisted variables safely into subject, plain text, and HTML.
// Placeholder values rendered into HTML are strictly HTML-escaped to prevent injection.
func RenderTemplate(subjectTpl, textTpl, htmlTpl string, vars map[string]string) (string, string, string, error) {
	render := func(src string, escapeHTML bool) string {
		if strings.TrimSpace(src) == "" {
			return ""
		}
		return placeholderRegex.ReplaceAllStringFunc(src, func(tok string) string {
			m := placeholderRegex.FindStringSubmatch(tok)
			if len(m) < 2 {
				return ""
			}
			val := vars[m[1]]
			if escapeHTML {
				return html.EscapeString(val)
			}
			return val
		})
	}
	subj := render(subjectTpl, false)
	subjClean, err := SanitizeEmailHeaderValue(strings.ReplaceAll(strings.ReplaceAll(subj, "\r", " "), "\n", " "))
	if err != nil {
		return "", "", "", err
	}
	txt := render(textTpl, false)
	htm := render(htmlTpl, true)
	return subjClean, txt, htm, nil
}

// BuildReservationTemplateVars builds an allowlisted template map without leaking internal staff notes.
func BuildReservationTemplateVars(prop Property, guest Guest, res *Reservation, unitNames []string, enquiry *GuestEnquiry) map[string]string {
	first, last, full := SplitFullName(guest.FirstName, guest.LastName, guest.FullName)
	if full == "" && res != nil {
		first, last, full = SplitFullName("", "", res.GuestName)
	}
	vars := map[string]string{
		"guest_first_name": first,
		"guest_last_name":  last,
		"guest_full_name":  full,
		"property_name":    prop.Name,
		"property_email":   prop.ContactEmail,
		"property_phone":   prop.ContactPhone,
		"check_in_time":    prop.CheckInTime,
		"check_out_time":   prop.CheckOutTime,
		"unit_names":       strings.Join(unitNames, ", "),
	}
	if res != nil {
		vars["reservation_reference"] = res.Reference
		vars["arrival_date"] = FormatDate(res.ArrivalDate)
		vars["departure_date"] = FormatDate(res.DepartureDate)
		vars["adults"] = strconv.Itoa(res.Adults)
		vars["children"] = strconv.Itoa(res.Children)
		vars["infants"] = strconv.Itoa(res.Infants)
		vars["total_amount"] = FormatMoney(res.TotalPence, prop.Currency)
		vars["balance_due"] = FormatMoney(res.BalanceDuePence, prop.Currency)
	}
	if enquiry != nil {
		vars["enquiry_reference"] = enquiry.Reference
		vars["enquiry_subject"] = enquiry.Subject
	}
	return vars
}

// ComputeRuleTriggerUTC calculates the exact UTC dispatch timestamp for an automation rule
// using the property's IANA timezone and local check-in/out times.
func ComputeRuleTriggerUTC(rule CommunicationAutomationRule, prop Property, res Reservation, now time.Time) time.Time {
	loc, err := time.LoadLocation(strings.TrimSpace(prop.Timezone))
	if err != nil || loc == nil {
		loc = time.UTC
	}
	parseClock := func(hhmm string, defH, defM int) (int, int) {
		parts := strings.Split(strings.TrimSpace(hhmm), ":")
		if len(parts) != 2 {
			return defH, defM
		}
		h, e1 := strconv.Atoi(parts[0])
		m, e2 := strconv.Atoi(parts[1])
		if e1 != nil || e2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
			return defH, defM
		}
		return h, m
	}

	inH, inM := parseClock(prop.CheckInTime, 15, 0)
	outH, outM := parseClock(prop.CheckOutTime, 10, 0)

	var anchorLocal time.Time
	switch rule.TriggerEvent {
	case TriggerBookingConfirmation:
		return now.UTC().Add(time.Duration(rule.OffsetHours) * time.Hour)
	case TriggerPreArrival, TriggerArrivalInfo, TriggerPaymentReminder:
		anchorLocal = time.Date(res.ArrivalDate.Year(), res.ArrivalDate.Month(), res.ArrivalDate.Day(), inH, inM, 0, 0, loc)
	case TriggerDepartureInstructions, TriggerPostStayThankYou:
		anchorLocal = time.Date(res.DepartureDate.Year(), res.DepartureDate.Month(), res.DepartureDate.Day(), outH, outM, 0, 0, loc)
	default:
		anchorLocal = time.Date(res.ArrivalDate.Year(), res.ArrivalDate.Month(), res.ArrivalDate.Day(), inH, inM, 0, 0, loc)
	}

	targetLocal := anchorLocal.Add(time.Duration(rule.OffsetHours) * time.Hour)
	if rule.SendHourLocal != nil {
		targetLocal = time.Date(targetLocal.Year(), targetLocal.Month(), targetLocal.Day(), *rule.SendHourLocal, 0, 0, 0, loc)
	}
	return targetLocal.UTC()
}

// ReservationStayFingerprint captures the fields that invalidate scheduled automation sends on edit.
func ReservationStayFingerprint(res Reservation) string {
	return fmt.Sprintf("%s|%s|%s|%d|%d",
		res.Status,
		FormatDate(res.ArrivalDate),
		FormatDate(res.DepartureDate),
		res.BalanceDuePence,
		res.Version,
	)
}

// ScoreDuplicatePair evaluates how likely two active guest profiles represent the same person.
func ScoreDuplicatePair(a, b GuestListItem) (int, []string) {
	if a.ID == b.ID || a.Status == GuestStatusMerged || b.Status == GuestStatusMerged ||
		a.Status == GuestStatusAnonymized || b.Status == GuestStatusAnonymized {
		return 0, nil
	}
	score := 0
	var reasons []string
	emailA := NormalizeEmail(a.Email)
	emailB := NormalizeEmail(b.Email)
	if emailA != "" && emailA == emailB {
		score += 70
		reasons = append(reasons, "Exact normalised email match")
	}
	phoneA := NormalizePhone(a.Phone)
	phoneB := NormalizePhone(b.Phone)
	if phoneA != "" && phoneA == phoneB {
		score += 55
		reasons = append(reasons, "Exact normalised phone match")
	}
	nameA := strings.ToLower(strings.TrimSpace(a.FullName))
	nameB := strings.ToLower(strings.TrimSpace(b.FullName))
	if nameA != "" && nameB != "" {
		if nameA == nameB {
			score += 35
			reasons = append(reasons, "Exact full name match")
		} else {
			fa, la, _ := SplitFullName(a.FirstName, a.LastName, a.FullName)
			fb, lb, _ := SplitFullName(b.FirstName, b.LastName, b.FullName)
			if strings.EqualFold(la, lb) && la != "" && (strings.HasPrefix(strings.ToLower(fa), strings.ToLower(fb)) || strings.HasPrefix(strings.ToLower(fb), strings.ToLower(fa))) {
				score += 20
				reasons = append(reasons, "Matching surname and initial/first name")
			}
		}
	}
	if score < 40 {
		return 0, nil
	}
	return score, reasons
}

// FormatMoney renders minor pence/cents into a readable GBP/currency label for templates.
func FormatMoney(minor int64, currency string) string {
	sym := "£"
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "EUR":
		sym = "€"
	case "USD":
		sym = "$"
	}
	sign := ""
	if minor < 0 {
		sign = "-"
		minor = -minor
	}
	return fmt.Sprintf("%s%s%d.%02d", sign, sym, minor/100, minor%100)
}
