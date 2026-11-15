package channels

import (
	"context"
	"net/http"
	"time"
)

// CapabilityStatus expresses whether a provider operation is supported,
// partner/plan-blocked, or absent from the provider's official API.
type CapabilityStatus string

const (
	CapabilitySupported      CapabilityStatus = "SUPPORTED"
	CapabilityBlocked        CapabilityStatus = "BLOCKED"
	CapabilityNotImplemented CapabilityStatus = "NOT_IMPLEMENTED"
)

// CapabilityItem documents one provider capability and its operational constraints.
type CapabilityItem struct {
	Name   string           `json:"name"`
	Status CapabilityStatus `json:"status"`
	Detail string           `json:"detail"`
}

// AdapterConfig injects transport dependencies and test-only endpoint overrides.
// Endpoint overrides must never come from user-editable public configuration.
type AdapterConfig struct {
	HTTPClient        *http.Client
	Clock             func() time.Time
	Sleep             func(context.Context, time.Duration) error
	Jitter            func(time.Duration) time.Duration
	NonceFunc         func() string
	MaxRetries        int
	RequestTimeout    time.Duration
	MaxResponseBytes  int64
	AllowHTTPForTests bool
	// ProviderBaseURLs maps canonical provider codes (or "channex:staging") to mock URLs in tests.
	ProviderBaseURLs map[string]string
}

// ConnectionVerification captures verified account metadata and any updated/refreshed secrets.
type ConnectionVerification struct {
	VerifiedAt       time.Time
	AccountID        string
	AccountName      string
	Environment      string
	DiscoveredFields map[string]string
	UpdatedCreds     map[string]string
	TokenExpiresAt   *time.Time
	Capabilities     []CapabilityItem
}

// AvailabilityDay represents one calendar date of availability/restriction state to push.
type AvailabilityDay struct {
	Date              string `json:"date"` // YYYY-MM-DD
	AvailableUnits    int    `json:"available_units"`
	ClosedToArrival   *bool  `json:"closed_to_arrival,omitempty"`
	ClosedToDeparture *bool  `json:"closed_to_departure,omitempty"`
	StopSell          *bool  `json:"stop_sell,omitempty"`
}

// AvailabilityPushRequest targets a mapped external listing.
type AvailabilityPushRequest struct {
	ExternalPropertyID string            `json:"external_property_id,omitempty"`
	ExternalListingID  string            `json:"external_listing_id"`
	ExternalRatePlanID string            `json:"external_rate_plan_id,omitempty"`
	Currency           string            `json:"currency,omitempty"`
	Timezone           string            `json:"timezone,omitempty"`
	Days               []AvailabilityDay `json:"days"`
}

// RateDay represents one calendar date of pricing/restriction state to push.
type RateDay struct {
	Date              string `json:"date"` // YYYY-MM-DD
	PriceMinor        int64  `json:"price_minor"`
	Currency          string `json:"currency"`
	MinStay           int    `json:"min_stay,omitempty"`
	MaxStay           *int   `json:"max_stay,omitempty"`
	StopSell          *bool  `json:"stop_sell,omitempty"`
	ClosedToArrival   *bool  `json:"closed_to_arrival,omitempty"`
	ClosedToDeparture *bool  `json:"closed_to_departure,omitempty"`
}

// RatePushRequest targets a mapped external listing / rate plan.
type RatePushRequest struct {
	ExternalPropertyID string    `json:"external_property_id,omitempty"`
	ExternalListingID  string    `json:"external_listing_id"`
	ExternalRatePlanID string    `json:"external_rate_plan_id,omitempty"`
	Currency           string    `json:"currency"`
	Timezone           string    `json:"timezone,omitempty"`
	Days               []RateDay `json:"days"`
}

// ExternalReservation is the normalized provider-agnostic reservation state returned by adapters.
// Guest PII fields exist strictly for in-memory reservation/guest persistence and are never audited or logged.
type ExternalReservation struct {
	ProviderCode          string
	ExternalReservationID string
	ExternalRevision      string
	RevisionAckID         string
	ExternalPropertyID    string
	ExternalListingID     string
	ExternalRatePlanID    string
	Status                string // CONFIRMED | CANCELLED | PENDING
	SourceChannel         string // e.g. airbnb, booking_com, vrbo, beds24, smoobu, lodgify
	CheckIn               string // YYYY-MM-DD
	CheckOut              string // YYYY-MM-DD
	Currency              string // ISO 4217 uppercase
	TotalMinor            int64
	Adults                int
	Children              int
	Infants               int
	GuestName             string
	GuestEmail            string
	GuestPhone            string
	ProviderUpdatedAt     *time.Time
	PayloadHash           string
}

// WebhookEvent is the normalized, verified webhook envelope produced by an adapter.
type WebhookEvent struct {
	ProviderCode          string
	ExternalEventID       string
	EventType             string
	Action                string // reservation_sync | reservation_cancel | availability_ping | ignored
	ExternalPropertyID    string
	ExternalListingID     string
	ExternalReservationID string
	ExternalRevision      string
	RevisionAckID         string
	SignatureVerified     bool
}

// NonceChecker validates and records single-use request/webhook nonces against replay.
type NonceChecker func(ctx context.Context, providerCode, nonce string, expiresAt time.Time) (bool, error)

// LiveAdapter extends Adapter with structured Phase 4 operations while keeping Adapter compatible.
type LiveAdapter interface {
	Adapter
	CapabilitiesMatrix(ctx context.Context) []CapabilityItem
	VerifyConnectionDetailed(ctx context.Context, publicCfg, creds map[string]string) (ConnectionVerification, error)
	RefreshToken(ctx context.Context, publicCfg, creds map[string]string) (map[string]string, *time.Time, error)
	DiscoverListingsDetailed(ctx context.Context, publicCfg, creds map[string]string) ([]ExternalListing, error)
	PushAvailability(ctx context.Context, publicCfg, creds map[string]string, req AvailabilityPushRequest) error
	PushRates(ctx context.Context, publicCfg, creds map[string]string, req RatePushRequest) error
	PullReservations(ctx context.Context, publicCfg, creds map[string]string, since time.Time, cursor string) ([]ExternalReservation, string, error)
	HydrateReservation(ctx context.Context, publicCfg, creds map[string]string, externalPropertyID, externalReservationID string) (ExternalReservation, error)
	AcknowledgeRevision(ctx context.Context, publicCfg, creds map[string]string, revisionAckID string) error
	VerifyWebhook(ctx context.Context, publicCfg, creds map[string]string, headers map[string]string, body []byte, checkNonce NonceChecker) (WebhookEvent, error)
	RegisterWebhook(ctx context.Context, publicCfg, creds map[string]string, callbackURL string) (string, error)
	DeleteWebhook(ctx context.Context, publicCfg, creds map[string]string, webhookID string) error
}
