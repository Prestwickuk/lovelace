package domain

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrChannelSyncBlocked           = errors.New("channel sync operation blocked by provider capability")
	ErrChannelConfirmationRequired  = errors.New("explicit confirm_scope confirmation required for broad live sync")
	ErrChannelWebhookUnauthorized   = errors.New("channel webhook signature or secret verification failed")
	ErrChannelListingNotMapped      = errors.New("external channel listing is not mapped to any local unit type")
	ErrChannelReservationOverbooked = errors.New("imported channel reservation conflicts with existing inventory claim")
)

type ChannelOwnershipMode string

const (
	ChannelOwnershipLovelaceARI    ChannelOwnershipMode = "LOVELACE_ARI_PROVIDER_BOOKINGS"
	ChannelOwnershipReadOnlyImport ChannelOwnershipMode = "READ_ONLY_IMPORT"
)

type ChannelJobType string

const (
	ChannelJobVerifyConnection    ChannelJobType = "VERIFY_CONNECTION"
	ChannelJobDiscoverListings    ChannelJobType = "DISCOVER_LISTINGS"
	ChannelJobPushAvailability    ChannelJobType = "PUSH_AVAILABILITY"
	ChannelJobPushRates           ChannelJobType = "PUSH_RATES"
	ChannelJobPullReservations    ChannelJobType = "PULL_RESERVATIONS"
	ChannelJobProcessWebhookEvent ChannelJobType = "PROCESS_WEBHOOK_EVENT"
)

type ChannelJobStatus string

const (
	ChannelJobPending         ChannelJobStatus = "PENDING"
	ChannelJobRunning         ChannelJobStatus = "RUNNING"
	ChannelJobSucceeded       ChannelJobStatus = "SUCCEEDED"
	ChannelJobFailedRetryable ChannelJobStatus = "FAILED_RETRYABLE"
	ChannelJobFailedTerminal  ChannelJobStatus = "FAILED_TERMINAL"
	ChannelJobCancelled       ChannelJobStatus = "CANCELLED"
)

type ChannelImportStatus string

const (
	ChannelImportImported           ChannelImportStatus = "IMPORTED"
	ChannelImportUpdated            ChannelImportStatus = "UPDATED"
	ChannelImportCancelled          ChannelImportStatus = "CANCELLED"
	ChannelImportConflictOverbooked ChannelImportStatus = "CONFLICT_OVERBOOKED"
	ChannelImportUnmapped           ChannelImportStatus = "UNMAPPED"
	ChannelImportFailed             ChannelImportStatus = "FAILED"
)

type ChannelWebhookStatus string

const (
	ChannelWebhookReceived  ChannelWebhookStatus = "RECEIVED"
	ChannelWebhookQueued    ChannelWebhookStatus = "QUEUED"
	ChannelWebhookProcessed ChannelWebhookStatus = "PROCESSED"
	ChannelWebhookConflict  ChannelWebhookStatus = "CONFLICT"
	ChannelWebhookIgnored   ChannelWebhookStatus = "IGNORED"
	ChannelWebhookFailed    ChannelWebhookStatus = "FAILED"
)

type ChannelListingMapping struct {
	ID                  uuid.UUID            `json:"id"`
	PropertyID          uuid.UUID            `json:"property_id"`
	ChannelConnectionID uuid.UUID            `json:"channel_connection_id"`
	ProviderCode        string               `json:"provider_code"`
	ExternalPropertyID  string               `json:"external_property_id,omitempty"`
	ExternalListingID   string               `json:"external_listing_id"`
	ExternalRatePlanID  string               `json:"external_rate_plan_id,omitempty"`
	ExternalName        string               `json:"external_name,omitempty"`
	UnitTypeID          uuid.UUID            `json:"unit_type_id"`
	UnitID              *uuid.UUID           `json:"unit_id,omitempty"`
	RatePlanID          *uuid.UUID           `json:"rate_plan_id,omitempty"`
	BookingChannelCode  string               `json:"booking_channel_code"`
	Currency            string               `json:"currency"`
	Timezone            string               `json:"timezone"`
	SyncAvailability    bool                 `json:"sync_availability"`
	SyncRates           bool                 `json:"sync_rates"`
	SyncReservations    bool                 `json:"sync_reservations"`
	OwnershipMode       ChannelOwnershipMode `json:"ownership_mode"`
	IsActive            bool                 `json:"is_active"`
	Version             int                  `json:"version"`
	CreatedAt           time.Time            `json:"created_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
}

type ChannelSyncJob struct {
	ID                  uuid.UUID        `json:"id"`
	PropertyID          uuid.UUID        `json:"property_id"`
	ChannelConnectionID uuid.UUID        `json:"channel_connection_id"`
	ListingMappingID    *uuid.UUID       `json:"listing_mapping_id,omitempty"`
	ProviderCode        string           `json:"provider_code"`
	JobType             ChannelJobType   `json:"job_type"`
	IdempotencyKey      string           `json:"idempotency_key"`
	ScopePayload        json.RawMessage  `json:"scope_payload"`
	Status              ChannelJobStatus `json:"status"`
	Attempts            int              `json:"attempts"`
	MaxAttempts         int              `json:"max_attempts"`
	NextAttemptAt       time.Time        `json:"next_attempt_at"`
	WorkerID            string           `json:"worker_id,omitempty"`
	LeaseToken          *uuid.UUID       `json:"lease_token,omitempty"`
	LeaseExpiresAt      *time.Time       `json:"lease_expires_at,omitempty"`
	FenceEpoch          int64            `json:"fence_epoch"`
	LastErrorCode       string           `json:"last_error_code,omitempty"`
	LastErrorSummary    string           `json:"last_error_summary,omitempty"`
	StartedAt           *time.Time       `json:"started_at,omitempty"`
	CompletedAt         *time.Time       `json:"completed_at,omitempty"`
	CreatedBy           *uuid.UUID       `json:"created_by,omitempty"`
	CreatedAt           time.Time        `json:"created_at"`
	UpdatedAt           time.Time        `json:"updated_at"`
}

type ChannelSyncCheckpoint struct {
	ID                  uuid.UUID       `json:"id"`
	PropertyID          uuid.UUID       `json:"property_id"`
	ChannelConnectionID uuid.UUID       `json:"channel_connection_id"`
	ProviderCode        string          `json:"provider_code"`
	CursorKey           string          `json:"cursor_key"`
	CursorValue         string          `json:"cursor_value"`
	PayloadDigest       string          `json:"payload_digest"`
	LastSyncedAt        *time.Time      `json:"last_synced_at,omitempty"`
	Metadata            json.RawMessage `json:"metadata,omitempty"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type ChannelExternalReservation struct {
	ID                    uuid.UUID           `json:"id"`
	PropertyID            uuid.UUID           `json:"property_id"`
	ChannelConnectionID   uuid.UUID           `json:"channel_connection_id"`
	ListingMappingID      *uuid.UUID          `json:"listing_mapping_id,omitempty"`
	ProviderCode          string              `json:"provider_code"`
	ExternalReservationID string              `json:"external_reservation_id"`
	ExternalRevision      string              `json:"external_revision,omitempty"`
	ExternalUpdatedAt     *time.Time          `json:"external_updated_at,omitempty"`
	ReservationID         *uuid.UUID          `json:"reservation_id,omitempty"`
	ImportStatus          ChannelImportStatus `json:"import_status"`
	ConflictReason        string              `json:"conflict_reason,omitempty"`
	PayloadDigest         string              `json:"payload_digest,omitempty"`
	CreatedAt             time.Time           `json:"created_at"`
	UpdatedAt             time.Time           `json:"updated_at"`
}

type ChannelWebhookEvent struct {
	ID                  uuid.UUID            `json:"id"`
	PropertyID          uuid.UUID            `json:"property_id"`
	ChannelConnectionID uuid.UUID            `json:"channel_connection_id"`
	ProviderCode        string               `json:"provider_code"`
	ExternalEventID     string               `json:"external_event_id"`
	EventType           string               `json:"event_type"`
	SignatureVerified   bool                 `json:"signature_verified"`
	Status              ChannelWebhookStatus `json:"status"`
	SafeSummary         json.RawMessage      `json:"safe_summary"`
	ErrorSummary        string               `json:"error_summary,omitempty"`
	ReceivedAt          time.Time            `json:"received_at"`
	ProcessedAt         *time.Time           `json:"processed_at,omitempty"`
}
