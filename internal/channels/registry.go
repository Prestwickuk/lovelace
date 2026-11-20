package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AuthMethod declares how a channel manager provider authenticates.
type AuthMethod string

const (
	AuthAPIKey            AuthMethod = "api_key"
	AuthHMAC              AuthMethod = "hmac_sha256"
	AuthOAuth2            AuthMethod = "oauth2"
	AuthClientCredentials AuthMethod = "client_credentials"
	AuthToken             AuthMethod = "token"
)

// SignatureSpec defines the canonical request-signing contract required by HMAC providers.
type SignatureSpec struct {
	Algorithm                string   `json:"algorithm"`
	SignatureEncoding        string   `json:"signature_encoding"`
	Headers                  []string `json:"headers"`
	CanonicalComponents      []string `json:"canonical_components"`
	AccountDiscoveryEndpoint string   `json:"account_discovery_endpoint,omitempty"`
	CanonicalNote            string   `json:"canonical_note"`
}

// ConnectionStatus tracks explicit configuration and adapter connection states.
type ConnectionStatus string

const (
	StatusNotConfigured    ConnectionStatus = "NOT_CONFIGURED"
	StatusConfigurationReq ConnectionStatus = "CONFIGURATION_REQUIRED"
	StatusCredentialsSaved ConnectionStatus = "CREDENTIALS_SAVED"
	StatusAwaitingAuth     ConnectionStatus = "AWAITING_AUTHORIZATION"
	StatusConnected        ConnectionStatus = "CONNECTED"
	StatusConnectionError  ConnectionStatus = "CONNECTION_ERROR"
	StatusDisabled         ConnectionStatus = "DISABLED"
)

// FieldSpec describes an authentication or account configuration input for a provider.
type FieldSpec struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	InputType   string `json:"input_type"` // text, password, url
	Required    bool   `json:"required"`
	Secret      bool   `json:"secret"`
	Placeholder string `json:"placeholder"`
	HelpText    string `json:"help_text"`
}

// ProviderDefinition declares a supported channel manager provider and its auth contract.
type ProviderDefinition struct {
	Code           string         `json:"code"`
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	AuthMethod     AuthMethod     `json:"auth_method"`
	AuthSummary    string         `json:"auth_summary"`
	DocsURL        string         `json:"docs_url,omitempty"`
	SignatureSpec  *SignatureSpec `json:"signature_spec,omitempty"`
	OAuthAuthURL   string         `json:"oauth_authorize_url,omitempty"`
	Fields         []FieldSpec    `json:"fields"`
	Capabilities   []string       `json:"capabilities"`
	HasLiveAdapter bool           `json:"has_live_adapter"`
	PhaseTarget    string         `json:"phase_target"`
}

// ChannelConnection is the persisted integration configuration (never exposes raw secrets).
type ChannelConnection struct {
	ID                   uuid.UUID                   `json:"id"`
	PropertyID           uuid.UUID                   `json:"property_id"`
	ProviderCode         string                      `json:"provider_code"`
	ProviderName         string                      `json:"provider_name"`
	DisplayName          string                      `json:"display_name"`
	AuthMethod           AuthMethod                  `json:"auth_method"`
	Status               ConnectionStatus            `json:"status"`
	PublicConfig         map[string]string           `json:"public_config"`
	CredentialHints      map[string]string           `json:"credential_hints"`
	HasCredentials       bool                        `json:"has_credentials"`
	LastError            string                      `json:"last_error,omitempty"`
	CredentialsUpdatedAt *time.Time                  `json:"credentials_updated_at,omitempty"`
	Capabilities         []string                    `json:"capabilities,omitempty"`
	CapabilityStatuses   map[string]CapabilityStatus `json:"capability_statuses,omitempty"`
	TokenExpiresAt       *time.Time                  `json:"token_expires_at,omitempty"`
	LastVerifiedAt       *time.Time                  `json:"last_verified_at,omitempty"`
	LastSyncedAt         *time.Time                  `json:"last_synced_at,omitempty"`
	LastSyncErrorCode    string                      `json:"last_sync_error_code,omitempty"`
	WebhookEndpointToken string                      `json:"webhook_endpoint_token,omitempty"`
	HasLiveAdapter       bool                        `json:"has_live_adapter"`
	Version              int                         `json:"version"`
	CreatedAt            time.Time                   `json:"created_at"`
	UpdatedAt            time.Time                   `json:"updated_at"`
}

// Adapter defines the provider-neutral synchronization contract implemented in Phase 4.
type Adapter interface {
	ProviderCode() string
	HasLiveImplementation() bool
	AuthorizeURL(ctx context.Context, state string, redirectURI string) (string, error)
	ExchangeCode(ctx context.Context, code string, redirectURI string) (map[string]string, error)
	RefreshCredentials(ctx context.Context, creds map[string]string) (map[string]string, error)
	VerifyConnection(ctx context.Context, publicCfg, creds map[string]string) error
	Disconnect(ctx context.Context, publicCfg, creds map[string]string) error
	DiscoverCapabilities(ctx context.Context) ([]string, error)
	ListListings(ctx context.Context, publicCfg, creds map[string]string) ([]ExternalListing, error)
	SyncAvailability(ctx context.Context, publicCfg, creds map[string]string, payload json.RawMessage) error
	SyncRates(ctx context.Context, publicCfg, creds map[string]string, payload json.RawMessage) error
	ImportReservations(ctx context.Context, publicCfg, creds map[string]string, since time.Time) ([]json.RawMessage, error)
	HandleWebhook(ctx context.Context, headers map[string]string, body []byte) error
}

// ExternalListing describes a remote accommodation listing discovered in Phase 4.
type ExternalListing struct {
	ExternalID         string `json:"external_id"`
	ExternalPropertyID string `json:"external_property_id,omitempty"`
	ExternalRatePlanID string `json:"external_rate_plan_id,omitempty"`
	Name               string `json:"name"`
	Channel            string `json:"channel"`
	Currency           string `json:"currency,omitempty"`
	SupportsPush       bool   `json:"supports_push"`
}

var registry = []ProviderDefinition{
	{
		Code:           "beds24",
		Name:           "Beds24",
		Description:    "Multi-channel PMS & distribution hub supporting Airbnb, Booking.com, and Vrbo.",
		AuthMethod:     AuthToken,
		AuthSummary:    "Official API v2 Refresh Token (refreshToken header → 24h access token) + Property ID",
		DocsURL:        "https://beds24.com/api/v2/",
		HasLiveAdapter: true,
		PhaseTarget:    "Phase 4",
		Capabilities:   []string{"token_refresh", "listing_discovery", "availability_sync", "rate_sync", "reservation_import", "webhooks"},
		Fields: []FieldSpec{
			{Name: "property_id", Label: "Beds24 Property ID", InputType: "text", Required: true, Secret: false, Placeholder: "123456", HelpText: "Numeric Beds24 property identifier."},
			{Name: "refresh_token", Label: "API v2 Refresh Token", InputType: "password", Required: true, Secret: true, Placeholder: "b24_rt_...", HelpText: "Generated in Beds24 Settings → Apps & Integrations → API; exchanged automatically for 24h access tokens."},
			{Name: "invite_code", Label: "One-Time Invite Code (Optional)", InputType: "password", Required: false, Secret: true, Placeholder: "Optional setup invite code", HelpText: "Optional setup code exchanged via GET /authentication/setup."},
			{Name: "webhook_secret", Label: "Webhook Shared Secret (Optional)", InputType: "password", Required: false, Secret: true, Placeholder: "Strong random webhook secret", HelpText: "Expected via X-Lovelace-Webhook-Secret or Bearer header; webhooks always trigger authoritative API re-fetch."},
		},
	},
	{
		Code:        "smoobu",
		Name:        "Smoobu",
		Description: "Channel manager for independent campsites, cabins, and holiday rentals.",
		AuthMethod:  AuthHMAC,
		AuthSummary: "HMAC-SHA256 Request Signing (api_key + api_secret; X-API-Key, X-Timestamp, X-Nonce, X-Signature)",
		DocsURL:     "https://docs.smoobu.com/",
		SignatureSpec: &SignatureSpec{
			Algorithm:         "HMAC-SHA256",
			SignatureEncoding: "base64",
			Headers:           []string{"X-API-Key", "X-Timestamp", "X-Nonce", "X-Signature"},
			CanonicalComponents: []string{
				"HTTP_METHOD",
				"REQUEST_PATH",
				"CANONICAL_QUERY_RFC3986",
				"UTC_TIMESTAMP",
				"NONCE",
				"SHA256_BODY_HASH",
				"API_KEY",
			},
			AccountDiscoveryEndpoint: "GET /api/me",
			CanonicalNote:            "Compute Base64 HMAC-SHA256 using api_secret over the canonical request string (method, path, RFC 3986 sorted query, UTC X-Timestamp, unique X-Nonce, lowercase hex SHA-256 request body digest, api_key). Enforce replay protection (<=5m timestamp skew and single-use nonce). Account identity is resolved via /api/me.",
		},
		HasLiveAdapter: true,
		PhaseTarget:    "Phase 4",
		Capabilities:   []string{"hmac_sha256_signing", "listing_discovery", "availability_sync", "rate_sync", "reservation_import", "webhooks"},
		Fields: []FieldSpec{
			{Name: "api_key", Label: "Smoobu API Key (X-API-Key)", InputType: "password", Required: true, Secret: true, Placeholder: "sm_key_...", HelpText: "Issued in Smoobu Settings → Advanced → API Keys; sent in X-API-Key header and encrypted at rest."},
			{Name: "api_secret", Label: "Smoobu API Secret (HMAC-SHA256)", InputType: "password", Required: true, Secret: true, Placeholder: "sm_secret_...", HelpText: "HMAC-SHA256 signing key (shown once on Smoobu key generation); account profile discovered via /api/me."},
			{Name: "webhook_secret", Label: "Webhook Shared Secret (Optional)", InputType: "password", Required: false, Secret: true, Placeholder: "Strong random webhook secret", HelpText: "Verifies incoming Smoobu webhook calls via header secret before triggering signed reservation re-fetch."},
		},
	},
	{
		Code:           "lodgify",
		Name:           "Lodgify",
		Description:    "Accommodation distribution and calendar synchronization platform.",
		AuthMethod:     AuthAPIKey,
		AuthSummary:    "Public API v1/v2 Key (X-ApiKey HTTP header against https://api.lodgify.com)",
		DocsURL:        "https://docs.lodgify.com/reference/getting-started-with-your-api",
		HasLiveAdapter: true,
		PhaseTarget:    "Phase 4",
		Capabilities:   []string{"listing_discovery", "rate_sync", "reservation_import", "webhooks"},
		Fields: []FieldSpec{
			{Name: "account_id", Label: "Lodgify Account Identifier", InputType: "text", Required: false, Secret: false, Placeholder: "meadowbrook", HelpText: "Optional account slug for operator reference."},
			{Name: "api_key", Label: "Lodgify Public API Key (X-ApiKey)", InputType: "password", Required: true, Secret: true, Placeholder: "ldg_...", HelpText: "Found in Lodgify Settings → Public API; sent via X-ApiKey header and encrypted at rest."},
			{Name: "webhook_secret", Label: "Webhook Secret / HMAC Key (Optional)", InputType: "password", Required: false, Secret: true, Placeholder: "Strong random webhook secret", HelpText: "Verifies incoming Lodgify webhooks via shared header or X-Lodgify-Signature HMAC before v2 booking hydration."},
		},
	},
	{
		Code:           "channex",
		Name:           "Channex",
		Description:    "API-first OTA connectivity layer for Booking.com, Airbnb, and Expedia.",
		AuthMethod:     AuthAPIKey,
		AuthSummary:    "Header API Key (user-api-key HTTP header; REST v1 production & staging)",
		DocsURL:        "https://docs.channex.io/api-v.1-documentation/api-reference",
		HasLiveAdapter: true,
		PhaseTarget:    "Phase 4",
		Capabilities:   []string{"listing_discovery", "availability_sync", "rate_sync", "reservation_import", "webhooks"},
		Fields: []FieldSpec{
			{Name: "property_id", Label: "Channex Property / Group ID", InputType: "text", Required: false, Secret: false, Placeholder: "uuid-or-group-id", HelpText: "Optional target Channex property or group UUID."},
			{Name: "environment", Label: "Target Environment", InputType: "text", Required: false, Secret: false, Placeholder: "production or staging", HelpText: "Use app.channex.io/api/v1 (production) or staging.channex.io/api/v1 (staging)."},
			{Name: "user_api_key", Label: "Channex User API Key (user-api-key header)", InputType: "password", Required: true, Secret: true, Placeholder: "Enter user-api-key", HelpText: "Generated in Channex Organisation/Profile → API Keys; sent via user-api-key header and encrypted at rest."},
			{Name: "webhook_secret", Label: "Webhook HMAC Signing Secret (Optional)", InputType: "password", Required: false, Secret: true, Placeholder: "Channex webhook signing key", HelpText: "Verifies x-hmac-timestamp + x-hmac-signature-256 on incoming webhooks."},
			{Name: "webhook_secret_previous", Label: "Previous Webhook Signing Secret (Optional)", InputType: "password", Required: false, Secret: true, Placeholder: "Rotated previous secret", HelpText: "Optional previous signing secret accepted during key rotation."},
		},
	},
}

// ListProviders returns all registered channel manager provider definitions.
func ListProviders() []ProviderDefinition {
	out := make([]ProviderDefinition, len(registry))
	copy(out, registry)
	return out
}

// GetProvider returns the provider definition for code if registered.
func GetProvider(code string) (ProviderDefinition, bool) {
	norm := strings.TrimSpace(strings.ToLower(code))
	for _, p := range registry {
		if p.Code == norm {
			return p, true
		}
	}
	return ProviderDefinition{}, false
}

// ValidateAndSplitInputs ensures only fields declared by the provider are accepted,
// separating public non-secret fields from secret fields.
func ValidateAndSplitInputs(
	def ProviderDefinition,
	inputs map[string]string,
	hasExistingSecrets bool,
) (publicCfg map[string]string, secrets map[string]string, err error) {
	allowed := make(map[string]FieldSpec, len(def.Fields))
	for _, f := range def.Fields {
		allowed[f.Name] = f
	}
	for k := range inputs {
		if _, ok := allowed[k]; !ok {
			return nil, nil, fmt.Errorf("unsupported configuration field %q for provider %s", k, def.Name)
		}
	}

	publicCfg = make(map[string]string)
	secrets = make(map[string]string)
	for _, f := range def.Fields {
		val := strings.TrimSpace(inputs[f.Name])
		if f.Secret {
			if val != "" {
				secrets[f.Name] = val
			} else if f.Required && !hasExistingSecrets {
				return nil, nil, fmt.Errorf("required credential %q is missing", f.Label)
			}
		} else {
			if val == "" && f.Required {
				return nil, nil, fmt.Errorf("required field %q is missing", f.Label)
			}
			if val != "" {
				publicCfg[f.Name] = val
			}
		}
	}
	return publicCfg, secrets, nil
}
