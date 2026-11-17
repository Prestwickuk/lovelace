package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/channels"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/secrets"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

const (
	defaultSyncWindowDays    = 30
	maxChannelSyncWindowDays = 365
	confirmScopeDayThreshold = 14
	defaultJobLeaseTTL       = 60 * time.Second
)

type VerifyChannelResult struct {
	Connection   channels.ChannelConnection `json:"connection"`
	AccountID    string                     `json:"account_id,omitempty"`
	AccountName  string                     `json:"account_name,omitempty"`
	Environment  string                     `json:"environment,omitempty"`
	Capabilities []channels.CapabilityItem  `json:"capabilities"`
}

type DisconnectChannelResult struct {
	Connection               channels.ChannelConnection `json:"connection"`
	RemoteRevoked            bool                       `json:"remote_revoked"`
	CapabilityStatus         channels.CapabilityStatus  `json:"capability_status"`
	WebhookDeleteAttempted   bool                       `json:"webhook_delete_attempted"`
	WebhookDeleted           bool                       `json:"webhook_deleted"`
	WebhookDeleteStatus      channels.CapabilityStatus  `json:"webhook_delete_status"`
	WebhookErrorCode         string                     `json:"webhook_error_code,omitempty"`
	TokenRevocationStatus    channels.CapabilityStatus  `json:"token_revocation_status"`
	TokenRevocationErrorCode string                     `json:"token_revocation_error_code,omitempty"`
	Detail                   string                     `json:"detail"`
}

type UpsertChannelMappingInput struct {
	ID                 *uuid.UUID                  `json:"id,omitempty"`
	PropertyID         uuid.UUID                   `json:"property_id"`
	ProviderCode       string                      `json:"provider_code"`
	ExternalPropertyID string                      `json:"external_property_id,omitempty"`
	ExternalListingID  string                      `json:"external_listing_id"`
	ExternalRatePlanID string                      `json:"external_rate_plan_id,omitempty"`
	ExternalName       string                      `json:"external_name,omitempty"`
	UnitTypeID         uuid.UUID                   `json:"unit_type_id"`
	UnitID             *uuid.UUID                  `json:"unit_id,omitempty"`
	RatePlanID         *uuid.UUID                  `json:"rate_plan_id,omitempty"`
	BookingChannelCode string                      `json:"booking_channel_code"`
	Currency           string                      `json:"currency"`
	Timezone           string                      `json:"timezone"`
	SyncAvailability   *bool                       `json:"sync_availability,omitempty"`
	SyncRates          *bool                       `json:"sync_rates,omitempty"`
	SyncReservations   *bool                       `json:"sync_reservations,omitempty"`
	OwnershipMode      domain.ChannelOwnershipMode `json:"ownership_mode,omitempty"`
	IsActive           *bool                       `json:"is_active,omitempty"`
}

type ChannelSyncRequest struct {
	PropertyID       uuid.UUID  `json:"property_id"`
	ProviderCode     string     `json:"provider_code"`
	MappingID        *uuid.UUID `json:"mapping_id,omitempty"`
	StartDate        string     `json:"start_date,omitempty"`
	EndDate          string     `json:"end_date,omitempty"` // Exclusive YYYY-MM-DD
	SyncAvailability bool       `json:"sync_availability"`
	SyncRates        bool       `json:"sync_rates"`
	PullReservations bool       `json:"pull_reservations"`
	ConfirmScope     bool       `json:"confirm_scope"`
}

type ChannelSyncMappingPreview struct {
	MappingID               uuid.UUID `json:"mapping_id"`
	ExternalListingID       string    `json:"external_listing_id"`
	ExternalRatePlanID      string    `json:"external_rate_plan_id,omitempty"`
	UnitTypeID              uuid.UUID `json:"unit_type_id"`
	Nights                  int       `json:"nights"`
	AvailabilityStatus      string    `json:"availability_status"` // READY | BLOCKED | SKIPPED_UNCHANGED | DISABLED
	RateStatus              string    `json:"rate_status"`         // READY | SKIPPED_UNCHANGED | DISABLED
	AvailabilityChangedDays int       `json:"availability_changed_days"`
	RatesChangedDays        int       `json:"rates_changed_days"`
	SamplePriceMinor        int64     `json:"sample_price_minor,omitempty"`
	Currency                string    `json:"currency"`
	Note                    string    `json:"note,omitempty"`
}

type ChannelSyncPreview struct {
	PropertyID           uuid.UUID                   `json:"property_id"`
	ProviderCode         string                      `json:"provider_code"`
	StartDate            string                      `json:"start_date"`
	EndDate              string                      `json:"end_date"`
	TotalNights          int                         `json:"total_nights"`
	MappingCount         int                         `json:"mapping_count"`
	RequiresConfirmation bool                        `json:"requires_confirmation"`
	PullReservations     bool                        `json:"pull_reservations"`
	Capabilities         []channels.CapabilityItem   `json:"capabilities"`
	Mappings             []ChannelSyncMappingPreview `json:"mappings"`
}

type ChannelSyncTriggerResult struct {
	Jobs             []domain.ChannelSyncJob `json:"jobs"`
	BlockedWarnings  []string                `json:"blocked_warnings,omitempty"`
	RequiresWorker   bool                    `json:"requires_worker"`
	DryRunCheckCount int                     `json:"dry_run_check_count"`
}

type ChannelHealthSummary struct {
	Connection           *channels.ChannelConnection         `json:"connection,omitempty"`
	Capabilities         []channels.CapabilityItem           `json:"capabilities"`
	Mappings             []domain.ChannelListingMapping      `json:"mappings"`
	Jobs                 []domain.ChannelSyncJob             `json:"jobs"`
	Checkpoints          []domain.ChannelSyncCheckpoint      `json:"checkpoints"`
	WebhookEvents        []domain.ChannelWebhookEvent        `json:"webhook_events"`
	ExternalReservations []domain.ChannelExternalReservation `json:"external_reservations"`
}

type WebhookProcessResult struct {
	WebhookEventID uuid.UUID `json:"webhook_event_id"`
	JobID          uuid.UUID `json:"job_id,omitempty"`
	Deduplicated   bool      `json:"deduplicated"`
	Action         string    `json:"action"`
	Status         string    `json:"status"`
}

func (s *Service) getLiveAdapter(providerCode string) (channels.LiveAdapter, channels.ProviderDefinition, error) {
	def, ok := channels.GetProvider(providerCode)
	if !ok {
		return nil, channels.ProviderDefinition{}, domain.NewValidationError(map[string]string{
			"provider_code": "unsupported channel manager provider",
		})
	}
	adapter, ok := channels.GetLiveAdapter(def.Code, s.effectiveChannelAdapterConfig())
	if !ok {
		return nil, def, domain.ErrChannelSyncBlocked
	}
	return adapter, def, nil
}

func (s *Service) openConnectionCreds(rec postgres.ChannelConnectionRecord) (map[string]string, error) {
	if s.secretBox == nil {
		return nil, domain.NewValidationError(map[string]string{"encryption": "channel encryption key is not configured"})
	}
	if len(rec.EncryptedCredentials) == 0 {
		return map[string]string{}, nil
	}
	return s.secretBox.OpenMap(rec.EncryptedCredentials)
}

func (s *Service) sealCredsAndHints(creds map[string]string) ([]byte, map[string]string, error) {
	if s.secretBox == nil {
		return nil, nil, domain.NewValidationError(map[string]string{"encryption": "channel encryption key is not configured"})
	}
	encrypted, err := s.secretBox.SealMap(creds)
	if err != nil {
		return nil, nil, err
	}
	hints := make(map[string]string, len(creds))
	for k, v := range creds {
		hints[k] = secrets.MaskSecret(v)
	}
	return encrypted, hints, nil
}

// ensureFreshConnectionCreds refreshes short-lived Beds24 access tokens under a per-connection single-flight lock.
func (s *Service) ensureFreshConnectionCreds(
	ctx context.Context,
	rec postgres.ChannelConnectionRecord,
	adapter channels.LiveAdapter,
) (postgres.ChannelConnectionRecord, map[string]string, error) {
	creds, err := s.openConnectionCreds(rec)
	if err != nil {
		return rec, nil, err
	}
	if rec.Connection.ProviderCode != "beds24" {
		return rec, creds, nil
	}
	now := s.nowUTC()
	expStr := strings.TrimSpace(creds["token_expires_at"])
	needsRefresh := strings.TrimSpace(creds["access_token"]) == ""
	if !needsRefresh && expStr != "" {
		if parsed, err := time.Parse(time.RFC3339, expStr); err == nil {
			if !parsed.After(now.Add(5 * time.Minute)) {
				needsRefresh = true
			}
		}
	}
	if !needsRefresh && rec.Connection.TokenExpiresAt != nil && !rec.Connection.TokenExpiresAt.After(now.Add(5*time.Minute)) {
		needsRefresh = true
	}
	if !needsRefresh {
		return rec, creds, nil
	}

	mu := s.channelConnectionLock(rec.Connection.ID)
	mu.Lock()
	defer mu.Unlock()

	// Double-check DB after acquiring per-connection lock to coalesce concurrent refreshes.
	latest, err := s.store.GetChannelConnectionByID(ctx, nil, rec.Connection.ID)
	if err != nil {
		return rec, nil, err
	}
	latestCreds, err := s.openConnectionCreds(latest)
	if err != nil {
		return latest, nil, err
	}
	if strings.TrimSpace(latestCreds["access_token"]) != "" {
		if latest.Connection.TokenExpiresAt != nil && latest.Connection.TokenExpiresAt.After(now.Add(5*time.Minute)) {
			return latest, latestCreds, nil
		}
		if tStr := strings.TrimSpace(latestCreds["token_expires_at"]); tStr != "" {
			if parsed, err := time.Parse(time.RFC3339, tStr); err == nil && parsed.After(now.Add(5*time.Minute)) {
				return latest, latestCreds, nil
			}
		}
	}

	updatedCreds, expPtr, err := adapter.RefreshToken(ctx, latest.Connection.PublicConfig, latestCreds)
	if err != nil {
		return latest, nil, err
	}
	sealed, hints, err := s.sealCredsAndHints(updatedCreds)
	if err != nil {
		return latest, nil, err
	}
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		return s.store.UpdateChannelConnectionTokens(ctx, tx, latest.Connection.ID, sealed, hints, expPtr)
	})
	if err != nil {
		return latest, nil, err
	}
	refreshed, _ := s.store.GetChannelConnectionByID(ctx, nil, latest.Connection.ID)
	return refreshed, updatedCreds, nil
}

func (s *Service) VerifyChannelConnection(ctx context.Context, actor Actor, propertyID uuid.UUID, providerCode string) (VerifyChannelResult, error) {
	if err := s.requirePerm(actor, auth.PermChannelsWrite); err != nil {
		return VerifyChannelResult{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return VerifyChannelResult{}, err
	}
	adapter, def, err := s.getLiveAdapter(providerCode)
	if err != nil {
		return VerifyChannelResult{}, err
	}

	rec, err := s.store.GetChannelConnection(ctx, nil, propertyID, def.Code)
	if err != nil {
		return VerifyChannelResult{}, err
	}
	creds, err := s.openConnectionCreds(rec)
	if err != nil {
		return VerifyChannelResult{}, err
	}

	// Execute provider network verification OUTSIDE PostgreSQL transaction.
	ver, vErr := adapter.VerifyConnectionDetailed(ctx, rec.Connection.PublicConfig, creds)
	if vErr != nil {
		errCode := channels.ExtractErrorCode(vErr)
		targetStatus := channels.StatusConnectionError
		if errors.Is(vErr, channels.ErrRateLimited) || errors.Is(vErr, channels.ErrProviderUnavailable) {
			targetStatus = rec.Connection.Status
		}
		_ = s.store.UpdateChannelConnectionSyncResult(ctx, nil, rec.Connection.ID, &targetStatus, errCode, errCode, nil)
		_ = s.store.WithTx(ctx, func(tx pgx.Tx) error {
			after, _ := s.store.GetChannelConnection(ctx, tx, propertyID, def.Code)
			return s.recordAudit(ctx, tx, actor, &propertyID, "channel_connection.verify_failed", "channel_connection", rec.Connection.ID.String(), rec.Connection, after.Connection)
		})
		return VerifyChannelResult{}, vErr
	}

	var sealedCreds []byte
	var hints map[string]string
	if len(ver.UpdatedCreds) > 0 {
		sealedCreds, hints, err = s.sealCredsAndHints(ver.UpdatedCreds)
		if err != nil {
			return VerifyChannelResult{}, err
		}
	}

	capNames := make([]string, 0, len(ver.Capabilities))
	capMap := make(map[string]channels.CapabilityStatus, len(ver.Capabilities))
	for _, c := range ver.Capabilities {
		capMap[c.Name] = c.Status
		if c.Status == channels.CapabilitySupported {
			capNames = append(capNames, c.Name)
		}
	}

	var saved postgres.ChannelConnectionRecord
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		saved, err = s.store.UpdateChannelConnectionVerified(
			ctx, tx, rec.Connection.ID, channels.StatusConnected,
			capNames, capMap, sealedCreds, hints, ver.TokenExpiresAt,
			ver.VerifiedAt, "", "", &actor.UserID,
		)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "channel_connection.verified", "channel_connection", saved.Connection.ID.String(), rec.Connection, saved.Connection)
	})
	if err != nil {
		return VerifyChannelResult{}, err
	}
	return VerifyChannelResult{
		Connection:   saved.Connection,
		AccountID:    ver.AccountID,
		AccountName:  ver.AccountName,
		Environment:  ver.Environment,
		Capabilities: ver.Capabilities,
	}, nil
}

func (s *Service) DisconnectChannelConnection(ctx context.Context, actor Actor, propertyID uuid.UUID, providerCode string) (DisconnectChannelResult, error) {
	if err := s.requirePerm(actor, auth.PermChannelsWrite); err != nil {
		return DisconnectChannelResult{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return DisconnectChannelResult{}, err
	}
	adapter, def, err := s.getLiveAdapter(providerCode)
	if err != nil {
		return DisconnectChannelResult{}, err
	}
	rec, err := s.store.GetChannelConnection(ctx, nil, propertyID, def.Code)
	if err != nil {
		return DisconnectChannelResult{}, err
	}
	creds, _ := s.openConnectionCreds(rec)

	outcome, _ := adapter.DisconnectDetailed(ctx, rec.Connection.PublicConfig, creds)
	remoteRevoked := outcome.TokenRevocationStatus == channels.CapabilitySupported
	webhookDeleted := outcome.WebhookDeleteStatus == channels.CapabilitySupported

	capStatus := channels.CapabilityNotImplemented
	if outcome.WebhookDeleteStatus == channels.CapabilityBlocked || outcome.TokenRevocationStatus == channels.CapabilityBlocked {
		capStatus = channels.CapabilityBlocked
	} else if outcome.TokenRevocationStatus == channels.CapabilitySupported &&
		(outcome.WebhookDeleteStatus == channels.CapabilitySupported || outcome.WebhookDeleteStatus == channels.CapabilityNotApplicable) {
		capStatus = channels.CapabilitySupported
	}

	var detail string
	lastErrCode := ""
	lastErrMsg := ""
	switch outcome.WebhookDeleteStatus {
	case channels.CapabilityBlocked:
		lastErrCode = outcome.WebhookDeleteErrorCode
		if lastErrCode == "" {
			lastErrCode = "WEBHOOK_DELETE_FAILED"
		}
		lastErrMsg = fmt.Sprintf("Remote webhook deletion failed (%s) during disconnect; orphaned webhook_id retained in public_config.", lastErrCode)
		detail = fmt.Sprintf("Remote webhook deletion failed (%s); provider API does not expose remote token revocation. Local encrypted credentials cleared and connection disabled.", lastErrCode)
	case channels.CapabilitySupported:
		detail = "Remote webhook deleted; provider API does not expose remote token revocation. Local encrypted credentials cleared and connection disabled."
	case channels.CapabilityNotApplicable:
		detail = "No remote webhook registered and provider API does not expose remote token revocation; local encrypted credentials cleared and connection disabled."
	default:
		detail = "Provider API does not expose remote webhook deletion or token revocation; local encrypted credentials cleared and connection disabled."
	}

	var cleared postgres.ChannelConnectionRecord
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		cleared, err = s.store.ClearChannelConnectionCredentials(ctx, tx, rec.Connection.ID, actor.UserID, outcome.ClearedWebhookConfig, lastErrCode, lastErrMsg)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "channel_connection.disconnected", "channel_connection", cleared.Connection.ID.String(), rec.Connection, cleared.Connection)
	})
	if err != nil {
		return DisconnectChannelResult{}, err
	}
	return DisconnectChannelResult{
		Connection:               cleared.Connection,
		RemoteRevoked:            remoteRevoked,
		CapabilityStatus:         capStatus,
		WebhookDeleteAttempted:   outcome.WebhookDeleteAttempted,
		WebhookDeleted:           webhookDeleted,
		WebhookDeleteStatus:      outcome.WebhookDeleteStatus,
		WebhookErrorCode:         outcome.WebhookDeleteErrorCode,
		TokenRevocationStatus:    outcome.TokenRevocationStatus,
		TokenRevocationErrorCode: outcome.TokenRevocationErrorCode,
		Detail:                   detail,
	}, nil
}

func (s *Service) DiscoverChannelListings(ctx context.Context, actor Actor, propertyID uuid.UUID, providerCode string) ([]channels.ExternalListing, error) {
	if err := s.requirePerm(actor, auth.PermChannelsRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	adapter, def, err := s.getLiveAdapter(providerCode)
	if err != nil {
		return nil, err
	}
	rec, err := s.store.GetChannelConnection(ctx, nil, propertyID, def.Code)
	if err != nil {
		return nil, err
	}
	if !rec.Connection.HasCredentials {
		return nil, domain.NewValidationError(map[string]string{
			"credentials": "channel credentials must be configured before discovering listings",
		})
	}
	rec, creds, err := s.ensureFreshConnectionCreds(ctx, rec, adapter)
	if err != nil {
		return nil, err
	}
	return adapter.DiscoverListingsDetailed(ctx, rec.Connection.PublicConfig, creds)
}

func (s *Service) ListChannelListingMappings(ctx context.Context, actor Actor, propertyID uuid.UUID, providerCode string) ([]domain.ChannelListingMapping, error) {
	if err := s.requirePerm(actor, auth.PermChannelsRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListChannelListingMappings(ctx, nil, propertyID, providerCode)
}

func (s *Service) UpsertChannelListingMapping(ctx context.Context, actor Actor, in UpsertChannelMappingInput) (domain.ChannelListingMapping, error) {
	if err := s.requirePerm(actor, auth.PermChannelsWrite); err != nil {
		return domain.ChannelListingMapping{}, err
	}
	if err := s.requirePropertyScope(actor, in.PropertyID); err != nil {
		return domain.ChannelListingMapping{}, err
	}
	_, def, err := s.getLiveAdapter(in.ProviderCode)
	if err != nil {
		return domain.ChannelListingMapping{}, err
	}

	errs := make(map[string]string)
	extListingID := strings.TrimSpace(in.ExternalListingID)
	if extListingID == "" {
		errs["external_listing_id"] = "is required"
	}
	if in.UnitTypeID == uuid.Nil {
		errs["unit_type_id"] = "is required"
	}
	chanCode := strings.ToUpper(strings.TrimSpace(in.BookingChannelCode))
	if chanCode == "" {
		chanCode = strings.ToUpper(def.Code)
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "GBP"
	}
	if _, cErr := channels.CurrencyDecimals(currency); cErr != nil {
		errs["currency"] = "must be a valid 3-letter ISO 4217 currency code"
	}
	tz := strings.TrimSpace(in.Timezone)
	if tz == "" {
		tz = "Europe/London"
	}
	if _, tErr := time.LoadLocation(tz); tErr != nil {
		errs["timezone"] = "must be a valid IANA timezone identifier"
	}
	ownerMode := in.OwnershipMode
	if ownerMode == "" {
		ownerMode = domain.ChannelOwnershipLovelaceARI
	}
	if ownerMode != domain.ChannelOwnershipLovelaceARI && ownerMode != domain.ChannelOwnershipReadOnlyImport {
		errs["ownership_mode"] = "must be LOVELACE_ARI_PROVIDER_BOOKINGS or READ_ONLY_IMPORT"
	}
	if len(errs) > 0 {
		return domain.ChannelListingMapping{}, domain.NewValidationError(errs)
	}

	syncAvail := true
	if in.SyncAvailability != nil {
		syncAvail = *in.SyncAvailability
	}
	syncRates := true
	if in.SyncRates != nil {
		syncRates = *in.SyncRates
	}
	syncRes := true
	if in.SyncReservations != nil {
		syncRes = *in.SyncReservations
	}
	isActive := true
	if in.IsActive != nil {
		isActive = *in.IsActive
	}

	var saved domain.ChannelListingMapping
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		conn, err := s.store.GetChannelConnection(ctx, tx, in.PropertyID, def.Code)
		if err != nil {
			return err
		}
		prop, err := s.store.GetPropertyByID(ctx, tx, in.PropertyID)
		if err != nil {
			return err
		}
		if prop.Currency != "" && currency != strings.ToUpper(prop.Currency) {
			return domain.NewValidationError(map[string]string{
				"currency": fmt.Sprintf("must match property currency %s to prevent silent currency conversion drift", prop.Currency),
			})
		}

		// Ensure target booking_channel exists for this property if canonical OTA channel code isn't seeded yet?
		// Keep explicit requirement unless active check fails cleanly via trigger/FK.
		var id uuid.UUID
		var before any
		if in.ID != nil && *in.ID != uuid.Nil {
			id = *in.ID
			if prev, gErr := s.store.GetChannelListingMapping(ctx, tx, in.PropertyID, id); gErr == nil {
				before = prev
			}
		}

		saved, err = s.store.UpsertChannelListingMapping(ctx, tx, domain.ChannelListingMapping{
			ID:                  id,
			PropertyID:          in.PropertyID,
			ChannelConnectionID: conn.Connection.ID,
			ProviderCode:        def.Code,
			ExternalPropertyID:  strings.TrimSpace(in.ExternalPropertyID),
			ExternalListingID:   extListingID,
			ExternalRatePlanID:  strings.TrimSpace(in.ExternalRatePlanID),
			ExternalName:        strings.TrimSpace(in.ExternalName),
			UnitTypeID:          in.UnitTypeID,
			UnitID:              in.UnitID,
			RatePlanID:          in.RatePlanID,
			BookingChannelCode:  chanCode,
			Currency:            currency,
			Timezone:            tz,
			SyncAvailability:    syncAvail,
			SyncRates:           syncRates,
			SyncReservations:    syncRes,
			OwnershipMode:       ownerMode,
			IsActive:            isActive,
		}, actor.UserID)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &in.PropertyID, "channel_listing_mapping.saved", "channel_listing_mapping", saved.ID.String(), before, saved)
	})
	return saved, err
}

func (s *Service) DeleteChannelListingMapping(ctx context.Context, actor Actor, propertyID, mappingID uuid.UUID) error {
	if err := s.requirePerm(actor, auth.PermChannelsWrite); err != nil {
		return err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetChannelListingMapping(ctx, tx, propertyID, mappingID)
		if err != nil {
			return err
		}
		if err := s.store.DeleteChannelListingMapping(ctx, tx, propertyID, mappingID); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "channel_listing_mapping.deleted", "channel_listing_mapping", mappingID.String(), before, nil)
	})
}

func (s *Service) GetChannelHealth(ctx context.Context, actor Actor, propertyID uuid.UUID, providerCode string) (ChannelHealthSummary, error) {
	if err := s.requirePerm(actor, auth.PermChannelsRead); err != nil {
		return ChannelHealthSummary{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return ChannelHealthSummary{}, err
	}
	adapter, def, err := s.getLiveAdapter(providerCode)
	if err != nil {
		return ChannelHealthSummary{}, err
	}

	var connPtr *channels.ChannelConnection
	if rec, gErr := s.store.GetChannelConnection(ctx, nil, propertyID, def.Code); gErr == nil {
		c := rec.Connection
		connPtr = &c
	} else if !errors.Is(gErr, domain.ErrNotFound) {
		return ChannelHealthSummary{}, gErr
	}

	mappings, err := s.store.ListChannelListingMappings(ctx, nil, propertyID, def.Code)
	if err != nil {
		return ChannelHealthSummary{}, err
	}
	jobs, err := s.store.ListChannelSyncJobs(ctx, nil, propertyID, def.Code, 30)
	if err != nil {
		return ChannelHealthSummary{}, err
	}
	cps, err := s.store.ListChannelSyncCheckpoints(ctx, nil, propertyID, def.Code)
	if err != nil {
		return ChannelHealthSummary{}, err
	}
	webhooks, err := s.store.ListChannelWebhookEvents(ctx, nil, propertyID, def.Code, 30)
	if err != nil {
		return ChannelHealthSummary{}, err
	}
	extRes, err := s.store.ListExternalReservations(ctx, nil, propertyID, def.Code, "", 50)
	if err != nil {
		return ChannelHealthSummary{}, err
	}
	return ChannelHealthSummary{
		Connection:           connPtr,
		Capabilities:         adapter.CapabilitiesMatrix(ctx),
		Mappings:             mappings,
		Jobs:                 jobs,
		Checkpoints:          cps,
		WebhookEvents:        webhooks,
		ExternalReservations: extRes,
	}, nil
}

func (s *Service) resolveSyncDateWindow(startStr, endStr string) (time.Time, time.Time, int, error) {
	today := s.nowUTC().Truncate(24 * time.Hour)
	var startDate, endDate time.Time
	var err error
	if strings.TrimSpace(startStr) == "" {
		startDate = today
	} else {
		startDate, err = time.Parse("2006-01-02", strings.TrimSpace(startStr))
		if err != nil {
			return time.Time{}, time.Time{}, 0, domain.NewValidationError(map[string]string{"start_date": "must be YYYY-MM-DD"})
		}
	}
	if strings.TrimSpace(endStr) == "" {
		endDate = startDate.AddDate(0, 0, defaultSyncWindowDays)
	} else {
		endDate, err = time.Parse("2006-01-02", strings.TrimSpace(endStr))
		if err != nil {
			return time.Time{}, time.Time{}, 0, domain.NewValidationError(map[string]string{"end_date": "must be YYYY-MM-DD"})
		}
	}
	nights := int(endDate.Sub(startDate).Hours() / 24)
	if nights <= 0 {
		return time.Time{}, time.Time{}, 0, domain.NewValidationError(map[string]string{"end_date": "must be after start_date"})
	}
	if nights > maxChannelSyncWindowDays {
		return time.Time{}, time.Time{}, 0, domain.NewValidationError(map[string]string{
			"end_date": fmt.Sprintf("sync date window cannot exceed %d days", maxChannelSyncWindowDays),
		})
	}
	return startDate, endDate, nights, nil
}

func (s *Service) PreviewChannelSync(ctx context.Context, actor Actor, in ChannelSyncRequest) (ChannelSyncPreview, error) {
	if err := s.requirePerm(actor, auth.PermChannelsRead); err != nil {
		return ChannelSyncPreview{}, err
	}
	if err := s.requirePropertyScope(actor, in.PropertyID); err != nil {
		return ChannelSyncPreview{}, err
	}
	adapter, def, err := s.getLiveAdapter(in.ProviderCode)
	if err != nil {
		return ChannelSyncPreview{}, err
	}
	startDate, endDate, nights, err := s.resolveSyncDateWindow(in.StartDate, in.EndDate)
	if err != nil {
		return ChannelSyncPreview{}, err
	}

	rec, err := s.store.GetChannelConnection(ctx, nil, in.PropertyID, def.Code)
	if err != nil {
		return ChannelSyncPreview{}, err
	}
	mappings, err := s.filterActiveMappings(ctx, in.PropertyID, def.Code, in.MappingID)
	if err != nil {
		return ChannelSyncPreview{}, err
	}

	caps := adapter.CapabilitiesMatrix(ctx)
	capByName := make(map[string]channels.CapabilityStatus, len(caps))
	for _, c := range caps {
		capByName[c.Name] = c.Status
	}

	previews := make([]ChannelSyncMappingPreview, 0, len(mappings))
	for _, m := range mappings {
		mp := ChannelSyncMappingPreview{
			MappingID:          m.ID,
			ExternalListingID:  m.ExternalListingID,
			ExternalRatePlanID: m.ExternalRatePlanID,
			UnitTypeID:         m.UnitTypeID,
			Nights:             nights,
			AvailabilityStatus: "DISABLED",
			RateStatus:         "DISABLED",
			Currency:           m.Currency,
		}
		if in.SyncAvailability && m.SyncAvailability && m.OwnershipMode == domain.ChannelOwnershipLovelaceARI {
			if capByName["availability_sync"] == channels.CapabilityBlocked {
				mp.AvailabilityStatus = "BLOCKED"
				mp.Note = "Provider official API blocks direct outbound availability writes"
			} else {
				req, err := s.buildAvailabilityPushRequest(ctx, rec.Connection.PublicConfig, m, startDate, endDate)
				if err != nil {
					return ChannelSyncPreview{}, err
				}
				digest := computeJSONDigest(req)
				cpKey := fmt.Sprintf("push_avail:%s:%s:%s", m.ID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
				if cp, cErr := s.store.GetChannelSyncCheckpoint(ctx, nil, rec.Connection.ID, cpKey); cErr == nil && cp.PayloadDigest == digest {
					mp.AvailabilityStatus = "SKIPPED_UNCHANGED"
				} else {
					mp.AvailabilityStatus = "READY"
					mp.AvailabilityChangedDays = len(req.Days)
				}
			}
		}
		if in.SyncRates && m.SyncRates && m.OwnershipMode == domain.ChannelOwnershipLovelaceARI {
			req, err := s.buildRatePushRequest(ctx, rec.Connection.PublicConfig, m, startDate, endDate)
			if err != nil {
				return ChannelSyncPreview{}, err
			}
			if len(req.Days) > 0 {
				mp.SamplePriceMinor = req.Days[0].PriceMinor
			}
			digest := computeJSONDigest(req)
			cpKey := fmt.Sprintf("push_rates:%s:%s:%s", m.ID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
			if cp, cErr := s.store.GetChannelSyncCheckpoint(ctx, nil, rec.Connection.ID, cpKey); cErr == nil && cp.PayloadDigest == digest {
				mp.RateStatus = "SKIPPED_UNCHANGED"
			} else {
				mp.RateStatus = "READY"
				mp.RatesChangedDays = len(req.Days)
			}
		}
		previews = append(previews, mp)
	}

	requiresConfirm := (in.SyncAvailability || in.SyncRates) && (len(mappings) > 1 || nights > confirmScopeDayThreshold)
	return ChannelSyncPreview{
		PropertyID:           in.PropertyID,
		ProviderCode:         def.Code,
		StartDate:            startDate.Format("2006-01-02"),
		EndDate:              endDate.Format("2006-01-02"),
		TotalNights:          nights,
		MappingCount:         len(mappings),
		RequiresConfirmation: requiresConfirm,
		PullReservations:     in.PullReservations,
		Capabilities:         caps,
		Mappings:             previews,
	}, nil
}

func (s *Service) TriggerChannelSync(ctx context.Context, actor Actor, in ChannelSyncRequest) (ChannelSyncTriggerResult, error) {
	if err := s.requirePerm(actor, auth.PermChannelsWrite); err != nil {
		return ChannelSyncTriggerResult{}, err
	}
	if err := s.requirePropertyScope(actor, in.PropertyID); err != nil {
		return ChannelSyncTriggerResult{}, err
	}
	if !in.SyncAvailability && !in.SyncRates && !in.PullReservations {
		return ChannelSyncTriggerResult{}, domain.NewValidationError(map[string]string{
			"sync": "at least one of sync_availability, sync_rates, or pull_reservations must be selected",
		})
	}
	adapter, def, err := s.getLiveAdapter(in.ProviderCode)
	if err != nil {
		return ChannelSyncTriggerResult{}, err
	}
	startDate, endDate, nights, err := s.resolveSyncDateWindow(in.StartDate, in.EndDate)
	if err != nil {
		return ChannelSyncTriggerResult{}, err
	}

	rec, err := s.store.GetChannelConnection(ctx, nil, in.PropertyID, def.Code)
	if err != nil {
		return ChannelSyncTriggerResult{}, err
	}
	if rec.Connection.Status != channels.StatusConnected || !rec.Connection.HasCredentials {
		return ChannelSyncTriggerResult{}, domain.NewValidationError(map[string]string{
			"connection": "channel connection must be verified (CONNECTED) before running sync jobs",
		})
	}

	caps := adapter.CapabilitiesMatrix(ctx)
	capByName := make(map[string]channels.CapabilityStatus, len(caps))
	for _, c := range caps {
		capByName[c.Name] = c.Status
	}

	mappings, err := s.filterActiveMappings(ctx, in.PropertyID, def.Code, in.MappingID)
	if err != nil {
		return ChannelSyncTriggerResult{}, err
	}
	if (in.SyncAvailability || in.SyncRates) && len(mappings) == 0 {
		return ChannelSyncTriggerResult{}, domain.NewValidationError(map[string]string{
			"mappings": "at least one active listing mapping is required for outbound sync",
		})
	}

	if (in.SyncAvailability || in.SyncRates) && (len(mappings) > 1 || nights > confirmScopeDayThreshold) && !in.ConfirmScope {
		return ChannelSyncTriggerResult{}, domain.ErrChannelConfirmationRequired
	}

	if in.SyncAvailability && !in.SyncRates && !in.PullReservations && capByName["availability_sync"] == channels.CapabilityBlocked {
		return ChannelSyncTriggerResult{}, domain.ErrChannelSyncBlocked
	}

	var res ChannelSyncTriggerResult
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")

	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		for _, m := range mappings {
			if in.SyncAvailability && m.SyncAvailability && m.OwnershipMode == domain.ChannelOwnershipLovelaceARI {
				if capByName["availability_sync"] == channels.CapabilityBlocked {
					res.BlockedWarnings = append(res.BlockedWarnings, fmt.Sprintf("%s availability push blocked by provider requirements", def.Code))
				} else {
					scopeRaw, _ := json.Marshal(map[string]any{
						"mapping_id": m.ID.String(),
						"start_date": startStr,
						"end_date":   endStr,
					})
					job, _, jErr := s.store.EnqueueChannelSyncJob(ctx, tx, domain.ChannelSyncJob{
						PropertyID:          in.PropertyID,
						ChannelConnectionID: rec.Connection.ID,
						ListingMappingID:    &m.ID,
						ProviderCode:        def.Code,
						JobType:             domain.ChannelJobPushAvailability,
						IdempotencyKey:      fmt.Sprintf("avail:%s:%s:%s:%d", m.ID, startStr, endStr, s.nowUTC().UnixNano()),
						ScopePayload:        scopeRaw,
						CreatedBy:           &actor.UserID,
					})
					if jErr != nil {
						return jErr
					}
					res.Jobs = append(res.Jobs, job)
					_ = s.recordAudit(ctx, tx, actor, &in.PropertyID, "channel_sync_job.enqueued", "channel_sync_job", job.ID.String(), nil, job)
				}
			}
			if in.SyncRates && m.SyncRates && m.OwnershipMode == domain.ChannelOwnershipLovelaceARI {
				scopeRaw, _ := json.Marshal(map[string]any{
					"mapping_id": m.ID.String(),
					"start_date": startStr,
					"end_date":   endStr,
				})
				job, _, jErr := s.store.EnqueueChannelSyncJob(ctx, tx, domain.ChannelSyncJob{
					PropertyID:          in.PropertyID,
					ChannelConnectionID: rec.Connection.ID,
					ListingMappingID:    &m.ID,
					ProviderCode:        def.Code,
					JobType:             domain.ChannelJobPushRates,
					IdempotencyKey:      fmt.Sprintf("rates:%s:%s:%s:%d", m.ID, startStr, endStr, s.nowUTC().UnixNano()),
					ScopePayload:        scopeRaw,
					CreatedBy:           &actor.UserID,
				})
				if jErr != nil {
					return jErr
				}
				res.Jobs = append(res.Jobs, job)
				_ = s.recordAudit(ctx, tx, actor, &in.PropertyID, "channel_sync_job.enqueued", "channel_sync_job", job.ID.String(), nil, job)
			}
		}

		if in.PullReservations {
			scopeRaw, _ := json.Marshal(map[string]any{"trigger": "manual"})
			job, _, jErr := s.store.EnqueueChannelSyncJob(ctx, tx, domain.ChannelSyncJob{
				PropertyID:          in.PropertyID,
				ChannelConnectionID: rec.Connection.ID,
				ProviderCode:        def.Code,
				JobType:             domain.ChannelJobPullReservations,
				IdempotencyKey:      fmt.Sprintf("pull:%s:%d", rec.Connection.ID, s.nowUTC().UnixNano()),
				ScopePayload:        scopeRaw,
				CreatedBy:           &actor.UserID,
			})
			if jErr != nil {
				return jErr
			}
			res.Jobs = append(res.Jobs, job)
			_ = s.recordAudit(ctx, tx, actor, &in.PropertyID, "channel_sync_job.enqueued", "channel_sync_job", job.ID.String(), nil, job)
		}
		return nil
	})
	res.RequiresWorker = len(res.Jobs) > 0
	return res, err
}

func (s *Service) filterActiveMappings(ctx context.Context, propertyID uuid.UUID, providerCode string, mappingID *uuid.UUID) ([]domain.ChannelListingMapping, error) {
	all, err := s.store.ListChannelListingMappings(ctx, nil, propertyID, providerCode)
	if err != nil {
		return nil, err
	}
	out := make([]domain.ChannelListingMapping, 0, len(all))
	for _, m := range all {
		if !m.IsActive {
			continue
		}
		if mappingID != nil && *mappingID != uuid.Nil && m.ID != *mappingID {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *Service) buildAvailabilityPushRequest(
	ctx context.Context,
	publicCfg map[string]string,
	m domain.ChannelListingMapping,
	startDate, endDate time.Time,
) (channels.AvailabilityPushRequest, error) {
	units, err := s.store.ListUnits(ctx, m.PropertyID, &m.UnitTypeID, false)
	if err != nil {
		return channels.AvailabilityPushRequest{}, err
	}
	var targetUnits []domain.Unit
	for _, u := range units {
		if m.UnitID != nil && *m.UnitID != uuid.Nil && u.ID != *m.UnitID {
			continue
		}
		if u.Status == domain.UnitStatusActive {
			targetUnits = append(targetUnits, u)
		}
	}

	claims, err := s.store.ListActiveClaimsInRange(ctx, m.PropertyID, startDate, endDate)
	if err != nil {
		return channels.AvailabilityPushRequest{}, err
	}
	releaseRules, err := s.store.ListActiveReleaseRulesInRange(ctx, nil, m.PropertyID, startDate, endDate)
	if err != nil {
		return channels.AvailabilityPushRequest{}, err
	}

	now := s.nowUTC()
	days := make([]channels.AvailabilityDay, 0, int(endDate.Sub(startDate).Hours()/24))
	for d := startDate; d.Before(endDate); d = d.AddDate(0, 0, 1) {
		nextDay := d.AddDate(0, 0, 1)
		openUnits := 0
		for _, u := range targetUnits {
			if isUnitOpenForChannelDay(u, m.BookingChannelCode, d, nextDay, now, claims, releaseRules) {
				openUnits++
			}
		}
		stopSell := openUnits == 0
		days = append(days, channels.AvailabilityDay{
			Date:           d.Format("2006-01-02"),
			AvailableUnits: openUnits,
			StopSell:       &stopSell,
		})
	}
	extPropID := strings.TrimSpace(m.ExternalPropertyID)
	if extPropID == "" {
		extPropID = strings.TrimSpace(publicCfg["property_id"])
	}
	return channels.AvailabilityPushRequest{
		ExternalPropertyID: extPropID,
		ExternalListingID:  m.ExternalListingID,
		ExternalRatePlanID: m.ExternalRatePlanID,
		Currency:           m.Currency,
		Timezone:           m.Timezone,
		Days:               days,
	}, nil
}

func isUnitOpenForChannelDay(
	u domain.Unit,
	channelCode string,
	day, nextDay, now time.Time,
	claims []domain.InventoryClaim,
	releaseRules []domain.AvailabilityReleaseRule,
) bool {
	for _, c := range claims {
		if c.UnitID != u.ID || c.Status != domain.ClaimStatusActive {
			continue
		}
		if !(c.StartDate.Before(nextDay) && c.EndDate.After(day)) {
			continue
		}
		if c.ClaimType == domain.ClaimChannelRestriction {
			if c.ChannelScope == channelCode || c.ChannelScope == "ALL" {
				return false
			}
			continue
		}
		if c.ClaimType == domain.ClaimChannelAllocation {
			if c.ChannelScope != "" && c.ChannelScope != channelCode && c.ChannelScope != "ALL" {
				return false
			}
			continue
		}
		return false
	}
	allowed, _, _ := domain.EvaluateReleaseForStay(releaseRules, u, channelCode, day, nextDay, now)
	return allowed
}

func (s *Service) buildRatePushRequest(
	ctx context.Context,
	publicCfg map[string]string,
	m domain.ChannelListingMapping,
	startDate, endDate time.Time,
) (channels.RatePushRequest, error) {
	ut, err := s.store.GetUnitType(ctx, nil, m.PropertyID, m.UnitTypeID)
	if err != nil {
		return channels.RatePushRequest{}, err
	}
	var rp domain.RatePlan
	if m.RatePlanID != nil && *m.RatePlanID != uuid.Nil {
		rp, err = s.store.GetRatePlan(ctx, nil, m.PropertyID, *m.RatePlanID)
	} else {
		rp, err = s.store.GetDefaultRatePlan(ctx, nil, m.PropertyID)
	}
	if err != nil {
		return channels.RatePushRequest{}, err
	}
	rules, err := s.store.ListRateRules(ctx, m.PropertyID, &rp.ID, &m.UnitTypeID)
	if err != nil {
		return channels.RatePushRequest{}, err
	}

	var allDays []channels.RateDay
	for chunkStart := startDate; chunkStart.Before(endDate); {
		chunkEnd := chunkStart.AddDate(0, 0, domain.MaxRatePreviewNights)
		if chunkEnd.After(endDate) {
			chunkEnd = endDate
		}
		prev, err := domain.CalculateRatePreview(ut, rp, rules, m.Currency, chunkStart, chunkEnd)
		if err != nil {
			return channels.RatePushRequest{}, err
		}
		for _, bd := range prev.Breakdown {
			allDays = append(allDays, channels.RateDay{
				Date:       bd.Date,
				PriceMinor: bd.RatePence,
				Currency:   m.Currency,
				MinStay:    bd.MinNights,
			})
		}
		chunkStart = chunkEnd
	}
	extPropID := strings.TrimSpace(m.ExternalPropertyID)
	if extPropID == "" {
		extPropID = strings.TrimSpace(publicCfg["property_id"])
	}
	return channels.RatePushRequest{
		ExternalPropertyID: extPropID,
		ExternalListingID:  m.ExternalListingID,
		ExternalRatePlanID: m.ExternalRatePlanID,
		Currency:           m.Currency,
		Timezone:           m.Timezone,
		Days:               allDays,
	}, nil
}

// ProcessPendingChannelSyncJobs claims due jobs with PostgreSQL FOR UPDATE SKIP LOCKED + leases + fencing
// and executes them outside DB transactions with periodic lease renewal.
func (s *Service) ProcessPendingChannelSyncJobs(ctx context.Context, limit int) (int, error) {
	return s.processChannelSyncJobs(ctx, nil, limit)
}

// ProcessPropertyChannelSyncJobs claims and executes due channel sync jobs strictly scoped to propertyID.
func (s *Service) ProcessPropertyChannelSyncJobs(ctx context.Context, actor Actor, propertyID uuid.UUID, limit int) (int, error) {
	if err := s.requirePerm(actor, auth.PermChannelsWrite); err != nil {
		return 0, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return 0, err
	}
	return s.processChannelSyncJobs(ctx, &propertyID, limit)
}

func (s *Service) processChannelSyncJobs(ctx context.Context, propertyID *uuid.UUID, limit int) (int, error) {
	if limit <= 0 {
		limit = 10
	}
	workerID := s.effectiveChannelWorkerID()
	jobs, err := s.store.ClaimDueChannelSyncJobsForProperty(ctx, workerID, propertyID, limit, defaultJobLeaseTTL)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, job := range jobs {
		s.executeClaimedChannelSyncJob(ctx, workerID, job)
		processed++
	}
	return processed, nil
}

func (s *Service) executeClaimedChannelSyncJob(parentCtx context.Context, workerID string, job domain.ChannelSyncJob) {
	if job.LeaseToken == nil {
		return
	}
	jobCtx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	// Lease heartbeat loop cancels jobCtx immediately if lease was lost/fenced.
	hbStop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(defaultJobLeaseTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-hbStop:
				return
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				ok, err := s.store.RenewChannelSyncJobLease(jobCtx, job.ID, workerID, *job.LeaseToken, job.FenceEpoch, defaultJobLeaseTTL)
				if err != nil || !ok {
					cancel()
					return
				}
			}
		}
	}()

	execErr := s.runChannelSyncJobBody(jobCtx, job)
	close(hbStop)

	nextStatus := domain.ChannelJobSucceeded
	var nextAttemptAt *time.Time
	errCode := ""
	errSummary := ""
	if execErr != nil {
		errCode = channels.ExtractErrorCode(execErr)
		errSummary = execErr.Error()
		if len(errSummary) > 160 {
			errSummary = errSummary[:160]
		}
		if channels.IsRetryableError(execErr) && job.Attempts < job.MaxAttempts {
			nextStatus = domain.ChannelJobFailedRetryable
			delay := time.Duration(1<<uint(job.Attempts)) * 10 * time.Second
			var pe *channels.ProviderError
			if errors.As(execErr, &pe) && pe.RetryAfter > delay {
				delay = pe.RetryAfter
			}
			if delay > 10*time.Minute {
				delay = 10 * time.Minute
			}
			t := s.nowUTC().Add(delay)
			nextAttemptAt = &t
		} else {
			nextStatus = domain.ChannelJobFailedTerminal
		}
	}

	settled, _ := s.store.CompleteChannelSyncJob(
		parentCtx, job.ID, workerID, *job.LeaseToken, job.FenceEpoch,
		nextStatus, nextAttemptAt, errCode, errSummary,
	)
	if !settled {
		// Stale/fenced worker lost its lease before settling; must not mutate telemetry or emit spurious completion audit.
		return
	}

	now := s.nowUTC()
	var stOverride *channels.ConnectionStatus
	if execErr != nil && errors.Is(execErr, channels.ErrAuthFailed) {
		errSt := channels.StatusConnectionError
		stOverride = &errSt
	}
	if execErr == nil {
		_ = s.store.UpdateChannelConnectionSyncResult(parentCtx, nil, job.ChannelConnectionID, nil, "", "", &now)
	} else {
		_ = s.store.UpdateChannelConnectionSyncResult(parentCtx, nil, job.ChannelConnectionID, stOverride, errCode, errSummary, nil)
	}
	_ = s.store.WithTx(parentCtx, func(tx pgx.Tx) error {
		updated, gErr := s.store.GetChannelSyncJob(parentCtx, tx, job.PropertyID, job.ID)
		if gErr != nil {
			return gErr
		}
		action := "channel_sync_job.succeeded"
		if nextStatus != domain.ChannelJobSucceeded {
			action = "channel_sync_job.failed"
		}
		return s.recordAudit(parentCtx, tx, channelSystemActor(), &job.PropertyID, action, "channel_sync_job", job.ID.String(), job, updated)
	})
}

func (s *Service) runChannelSyncJobBody(ctx context.Context, job domain.ChannelSyncJob) error {
	adapter, _, err := s.getLiveAdapter(job.ProviderCode)
	if err != nil {
		return err
	}
	rec, err := s.store.GetChannelConnectionByID(ctx, nil, job.ChannelConnectionID)
	if err != nil {
		return err
	}
	if rec.Connection.Status == channels.StatusDisabled || !rec.Connection.HasCredentials {
		return domain.ErrValidation
	}
	rec, creds, err := s.ensureFreshConnectionCreds(ctx, rec, adapter)
	if err != nil {
		return err
	}

	switch job.JobType {
	case domain.ChannelJobPushAvailability:
		return s.runPushAvailabilityJob(ctx, job, rec, creds, adapter)
	case domain.ChannelJobPushRates:
		return s.runPushRatesJob(ctx, job, rec, creds, adapter)
	case domain.ChannelJobPullReservations:
		return s.runPullReservationsJob(ctx, job, rec, creds, adapter)
	case domain.ChannelJobProcessWebhookEvent:
		return s.runProcessWebhookJob(ctx, job, rec, creds, adapter)
	default:
		return fmt.Errorf("unsupported job type %q", job.JobType)
	}
}

func (s *Service) runPushAvailabilityJob(ctx context.Context, job domain.ChannelSyncJob, rec postgres.ChannelConnectionRecord, creds map[string]string, adapter channels.LiveAdapter) error {
	var scope struct {
		MappingID string `json:"mapping_id"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
	}
	if err := json.Unmarshal(job.ScopePayload, &scope); err != nil {
		return err
	}
	mappingID, err := uuid.Parse(scope.MappingID)
	if err != nil {
		return err
	}
	m, err := s.store.GetChannelListingMapping(ctx, nil, job.PropertyID, mappingID)
	if err != nil {
		return err
	}
	if !m.IsActive || !m.SyncAvailability || m.OwnershipMode != domain.ChannelOwnershipLovelaceARI {
		return nil
	}
	startDate, endDate, _, err := s.resolveSyncDateWindow(scope.StartDate, scope.EndDate)
	if err != nil {
		return err
	}
	req, err := s.buildAvailabilityPushRequest(ctx, rec.Connection.PublicConfig, m, startDate, endDate)
	if err != nil {
		return err
	}
	digest := computeJSONDigest(req)
	cpKey := fmt.Sprintf("push_avail:%s:%s:%s", m.ID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
	if cp, cErr := s.store.GetChannelSyncCheckpoint(ctx, nil, rec.Connection.ID, cpKey); cErr == nil && cp.PayloadDigest == digest {
		return nil // Loop prevention: skip identical payload push
	}
	if err := adapter.PushAvailability(ctx, rec.Connection.PublicConfig, creds, req); err != nil {
		return err
	}
	now := s.nowUTC()
	_, err = s.store.UpsertChannelSyncCheckpoint(ctx, nil, domain.ChannelSyncCheckpoint{
		PropertyID:          job.PropertyID,
		ChannelConnectionID: rec.Connection.ID,
		ProviderCode:        rec.Connection.ProviderCode,
		CursorKey:           cpKey,
		CursorValue:         endDate.Format("2006-01-02"),
		PayloadDigest:       digest,
		LastSyncedAt:        &now,
	})
	return err
}

func (s *Service) runPushRatesJob(ctx context.Context, job domain.ChannelSyncJob, rec postgres.ChannelConnectionRecord, creds map[string]string, adapter channels.LiveAdapter) error {
	var scope struct {
		MappingID string `json:"mapping_id"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
	}
	if err := json.Unmarshal(job.ScopePayload, &scope); err != nil {
		return err
	}
	mappingID, err := uuid.Parse(scope.MappingID)
	if err != nil {
		return err
	}
	m, err := s.store.GetChannelListingMapping(ctx, nil, job.PropertyID, mappingID)
	if err != nil {
		return err
	}
	if !m.IsActive || !m.SyncRates || m.OwnershipMode != domain.ChannelOwnershipLovelaceARI {
		return nil
	}
	startDate, endDate, _, err := s.resolveSyncDateWindow(scope.StartDate, scope.EndDate)
	if err != nil {
		return err
	}
	req, err := s.buildRatePushRequest(ctx, rec.Connection.PublicConfig, m, startDate, endDate)
	if err != nil {
		return err
	}
	digest := computeJSONDigest(req)
	cpKey := fmt.Sprintf("push_rates:%s:%s:%s", m.ID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
	if cp, cErr := s.store.GetChannelSyncCheckpoint(ctx, nil, rec.Connection.ID, cpKey); cErr == nil && cp.PayloadDigest == digest {
		return nil
	}
	if err := adapter.PushRates(ctx, rec.Connection.PublicConfig, creds, req); err != nil {
		return err
	}
	now := s.nowUTC()
	_, err = s.store.UpsertChannelSyncCheckpoint(ctx, nil, domain.ChannelSyncCheckpoint{
		PropertyID:          job.PropertyID,
		ChannelConnectionID: rec.Connection.ID,
		ProviderCode:        rec.Connection.ProviderCode,
		CursorKey:           cpKey,
		CursorValue:         endDate.Format("2006-01-02"),
		PayloadDigest:       digest,
		LastSyncedAt:        &now,
	})
	return err
}

func (s *Service) runPullReservationsJob(ctx context.Context, job domain.ChannelSyncJob, rec postgres.ChannelConnectionRecord, creds map[string]string, adapter channels.LiveAdapter) error {
	var since time.Time
	cpKey := "pull_reservations"
	if cp, cErr := s.store.GetChannelSyncCheckpoint(ctx, nil, rec.Connection.ID, cpKey); cErr == nil && cp.LastSyncedAt != nil {
		since = cp.LastSyncedAt.Add(-5 * time.Minute)
	}
	items, nextCursor, err := adapter.PullReservations(ctx, rec.Connection.PublicConfig, creds, since, "")
	if err != nil {
		return err
	}
	for _, er := range items {
		if err := s.applyExternalReservation(ctx, rec, er); err != nil {
			return err
		}
		if er.RevisionAckID != "" {
			if ackErr := adapter.AcknowledgeRevision(ctx, rec.Connection.PublicConfig, creds, er.RevisionAckID); ackErr != nil {
				return ackErr
			}
		}
	}
	now := s.nowUTC()
	_, err = s.store.UpsertChannelSyncCheckpoint(ctx, nil, domain.ChannelSyncCheckpoint{
		PropertyID:          job.PropertyID,
		ChannelConnectionID: rec.Connection.ID,
		ProviderCode:        rec.Connection.ProviderCode,
		CursorKey:           cpKey,
		CursorValue:         nextCursor,
		LastSyncedAt:        &now,
	})
	return err
}

func (s *Service) runProcessWebhookJob(ctx context.Context, job domain.ChannelSyncJob, rec postgres.ChannelConnectionRecord, creds map[string]string, adapter channels.LiveAdapter) error {
	var scope struct {
		WebhookEventID        string `json:"webhook_event_id"`
		Action                string `json:"action"`
		ExternalPropertyID    string `json:"external_property_id"`
		ExternalReservationID string `json:"external_reservation_id"`
		RevisionAckID         string `json:"revision_ack_id"`
	}
	if err := json.Unmarshal(job.ScopePayload, &scope); err != nil {
		return err
	}
	whID, _ := uuid.Parse(scope.WebhookEventID)

	if scope.Action == "availability_ping" {
		if whID != uuid.Nil {
			_ = s.store.UpdateChannelWebhookEventStatus(ctx, nil, whID, domain.ChannelWebhookIgnored, "")
		}
		return nil
	}

	var items []channels.ExternalReservation
	var hydErr error
	targetID := scope.ExternalReservationID
	if rec.Connection.ProviderCode == "channex" && scope.RevisionAckID != "" {
		targetID = scope.RevisionAckID
	}
	if targetID != "" {
		er, err := adapter.HydrateReservation(ctx, rec.Connection.PublicConfig, creds, scope.ExternalPropertyID, targetID)
		if err != nil {
			hydErr = err
		} else {
			items = []channels.ExternalReservation{er}
		}
	} else {
		items, _, hydErr = adapter.PullReservations(ctx, rec.Connection.PublicConfig, creds, s.nowUTC().Add(-24*time.Hour), "")
	}
	if hydErr != nil {
		if whID != uuid.Nil {
			_ = s.store.UpdateChannelWebhookEventStatus(ctx, nil, whID, domain.ChannelWebhookFailed, channels.ExtractErrorCode(hydErr))
		}
		return hydErr
	}

	whStatus := domain.ChannelWebhookProcessed
	for _, er := range items {
		if err := s.applyExternalReservation(ctx, rec, er); err != nil {
			if whID != uuid.Nil {
				_ = s.store.UpdateChannelWebhookEventStatus(ctx, nil, whID, domain.ChannelWebhookFailed, channels.ExtractErrorCode(err))
			}
			return err
		}
		if ledger, gErr := s.store.GetExternalReservationByExtID(ctx, nil, rec.Connection.PropertyID, rec.Connection.ProviderCode, er.ExternalReservationID); gErr == nil {
			if ledger.ImportStatus == domain.ChannelImportConflictOverbooked {
				whStatus = domain.ChannelWebhookConflict
			}
		}
		ackID := er.RevisionAckID
		if ackID == "" {
			ackID = scope.RevisionAckID
		}
		if ackID != "" {
			if ackErr := adapter.AcknowledgeRevision(ctx, rec.Connection.PublicConfig, creds, ackID); ackErr != nil {
				return ackErr
			}
		}
	}
	if whID != uuid.Nil {
		_ = s.store.UpdateChannelWebhookEventStatus(ctx, nil, whID, whStatus, "")
	}
	return nil
}

func (s *Service) applyExternalReservation(ctx context.Context, rec postgres.ChannelConnectionRecord, er channels.ExternalReservation) error {
	if strings.TrimSpace(er.ExternalReservationID) == "" {
		return nil
	}
	propID := rec.Connection.PropertyID
	existing, existErr := s.store.GetExternalReservationByExtID(ctx, nil, propID, rec.Connection.ProviderCode, er.ExternalReservationID)
	hasExisting := existErr == nil
	if existErr != nil && !errors.Is(existErr, domain.ErrNotFound) {
		return existErr
	}

	// Out-of-order and idempotent replay protection.
	if hasExisting {
		if existing.PayloadDigest != "" && existing.PayloadDigest == er.PayloadHash && existing.ImportStatus != domain.ChannelImportUnmapped {
			return nil
		}
		if existing.ExternalUpdatedAt != nil && er.ProviderUpdatedAt != nil && er.ProviderUpdatedAt.Before(*existing.ExternalUpdatedAt) {
			return nil
		}
	}

	mapping, mapErr := s.store.FindChannelListingMappingByExternal(ctx, nil, propID, rec.Connection.ProviderCode, er.ExternalListingID, er.ExternalRatePlanID)
	if errors.Is(mapErr, domain.ErrNotFound) {
		return s.store.WithTx(ctx, func(tx pgx.Tx) error {
			saved, uErr := s.store.UpsertExternalReservation(ctx, tx, domain.ChannelExternalReservation{
				PropertyID:            propID,
				ChannelConnectionID:   rec.Connection.ID,
				ProviderCode:          rec.Connection.ProviderCode,
				ExternalReservationID: er.ExternalReservationID,
				ExternalRevision:      er.ExternalRevision,
				ExternalUpdatedAt:     er.ProviderUpdatedAt,
				ImportStatus:          domain.ChannelImportUnmapped,
				ConflictReason:        "UNMAPPED_LISTING",
				PayloadDigest:         er.PayloadHash,
			})
			if uErr != nil {
				return uErr
			}
			return s.recordAudit(ctx, tx, channelSystemActor(), &propID, "channel_external_reservation.unmapped", "channel_external_reservation", saved.ID.String(), nil, saved)
		})
	}
	if mapErr != nil {
		return mapErr
	}
	if !mapping.SyncReservations {
		return nil
	}

	// Cancellation handling
	if er.Status == "CANCELLED" {
		return s.store.WithTx(ctx, func(tx pgx.Tx) error {
			lockKey := propID.String() + ":" + rec.Connection.ProviderCode + ":" + er.ExternalReservationID
			if _, lErr := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); lErr != nil {
				return lErr
			}
			curExt, extErr := s.store.GetExternalReservationByExtID(ctx, tx, propID, rec.Connection.ProviderCode, er.ExternalReservationID)
			hasExtNow := extErr == nil
			var resIDPtr *uuid.UUID
			if hasExtNow && curExt.ReservationID != nil {
				resIDPtr = curExt.ReservationID
				curRes, gErr := s.store.GetReservation(ctx, tx, propID, *curExt.ReservationID)
				if gErr == nil && curRes.Status != domain.ResStatusCancelled {
					isAnon := false
					if curRes.GuestID != uuid.Nil {
						if curGuest, cgErr := s.store.GetGuest(ctx, tx, propID, curRes.GuestID); cgErr == nil {
							isAnon = curGuest.Status == domain.GuestStatusAnonymized || curGuest.AnonymizedAt != nil || curGuest.ErasureRequestedAt != nil
						}
					}
					if !isAnon && curRes.Status.CanTransitionTo(domain.ResStatusCancelled) {
						cancelledRes, tErr := s.store.TransitionReservationStatus(
							ctx, tx, propID, curRes.ID, domain.ResStatusCancelled,
							fmt.Sprintf("Cancelled remotely via %s", rec.Connection.ProviderCode),
							uuid.Nil, curRes.Version,
						)
						if tErr != nil {
							return tErr
						}
						if prop, pErr := s.store.GetPropertyByID(ctx, tx, propID); pErr == nil {
							if jErr := s.store.SyncReservationAutomationJobs(ctx, tx, prop, cancelledRes, s.nowUTC()); jErr != nil {
								return jErr
							}
						}
					}
				}
			}
			saved, uErr := s.store.UpsertExternalReservation(ctx, tx, domain.ChannelExternalReservation{
				PropertyID:            propID,
				ChannelConnectionID:   rec.Connection.ID,
				ListingMappingID:      &mapping.ID,
				ProviderCode:          rec.Connection.ProviderCode,
				ExternalReservationID: er.ExternalReservationID,
				ExternalRevision:      er.ExternalRevision,
				ExternalUpdatedAt:     er.ProviderUpdatedAt,
				ReservationID:         resIDPtr,
				ImportStatus:          domain.ChannelImportCancelled,
				PayloadDigest:         er.PayloadHash,
			})
			if uErr != nil {
				return uErr
			}
			return s.recordAudit(ctx, tx, channelSystemActor(), &propID, "channel_external_reservation.cancelled", "channel_external_reservation", saved.ID.String(), nil, saved)
		})
	}

	arrival, aErr := domain.ParseDate(er.CheckIn)
	departure, dErr := domain.ParseDate(er.CheckOut)
	if aErr != nil || dErr != nil || !departure.After(arrival) {
		return s.recordConflictLedger(ctx, propID, rec.Connection.ID, &mapping.ID, rec.Connection.ProviderCode, er, nil, "INVALID_STAY_DATES")
	}

	// Verify currency alignment
	prop, err := s.store.GetPropertyByID(ctx, nil, propID)
	if err != nil {
		return err
	}
	if prop.Currency != "" && er.Currency != "" && !strings.EqualFold(prop.Currency, er.Currency) {
		return s.recordConflictLedger(ctx, propID, rec.Connection.ID, &mapping.ID, rec.Connection.ProviderCode, er, nil, "CURRENCY_MISMATCH")
	}

	var resOut domain.Reservation
	var importStat domain.ChannelImportStatus
	txErr := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		lockKey := propID.String() + ":" + rec.Connection.ProviderCode + ":" + er.ExternalReservationID
		if _, lErr := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); lErr != nil {
			return lErr
		}
		if curExt, extErr := s.store.GetExternalReservationByExtID(ctx, tx, propID, rec.Connection.ProviderCode, er.ExternalReservationID); extErr == nil {
			existing = curExt
			hasExisting = true
			if existing.PayloadDigest != "" && existing.PayloadDigest == er.PayloadHash && existing.ImportStatus != domain.ChannelImportUnmapped {
				return nil
			}
			if existing.ExternalUpdatedAt != nil && er.ProviderUpdatedAt != nil && er.ProviderUpdatedAt.Before(*existing.ExternalUpdatedAt) {
				return nil
			}
		} else if !errors.Is(extErr, domain.ErrNotFound) {
			return extErr
		}

		guest, gErr := s.store.FindOrCreateGuestForChannelImport(ctx, tx, propID, er.GuestName, er.GuestEmail, er.GuestPhone)
		if gErr != nil {
			return gErr
		}
		targetStatus := domain.ResStatusConfirmed
		if er.Status == "PENDING" {
			targetStatus = domain.ResStatusPending
		}

		if hasExisting && existing.ReservationID != nil {
			curRes, gErr := s.store.GetReservation(ctx, tx, propID, *existing.ReservationID)
			if gErr == nil {
				// Never overwrite terminal or GDPR-anonymised local reservations on historical OTA re-syncs.
				isAnon := false
				if curRes.GuestID != uuid.Nil {
					if curGuest, cgErr := s.store.GetGuest(ctx, tx, propID, curRes.GuestID); cgErr == nil {
						isAnon = curGuest.Status == domain.GuestStatusAnonymized || curGuest.AnonymizedAt != nil || curGuest.ErasureRequestedAt != nil
					}
				}
				if isAnon || curRes.Status == domain.ResStatusCheckedOut || curRes.Status == domain.ResStatusCancelled || curRes.Status == domain.ResStatusNoShow {
					resOut = curRes
					importStat = domain.ChannelImportUpdated
					return nil
				}
				curRes.GuestID = guest.ID
				curRes.GuestName = guest.FullName
				curRes.GuestEmail = guest.Email
				curRes.GuestPhone = guest.Phone
				curRes.ChannelCode = mapping.BookingChannelCode
				curRes.ArrivalDate = arrival
				curRes.DepartureDate = departure
				curRes.Adults = maxIntSvc(er.Adults, 1)
				curRes.Children = maxIntSvc(er.Children, 0)
				curRes.Infants = maxIntSvc(er.Infants, 0)
				curRes.UpdatedBy = nil
				updated, uErr := s.store.UpdateReservation(ctx, tx, curRes)
				if uErr != nil {
					return uErr
				}
				if _, fErr := tx.Exec(ctx, `
					UPDATE reservations SET total_pence = $3, balance_due_pence = GREATEST($3 - amount_paid_pence, 0), currency = $4
					WHERE property_id = $1 AND id = $2
				`, propID, updated.ID, er.TotalMinor, mapping.Currency); fErr != nil {
					return fErr
				}
				resOut = updated
				importStat = domain.ChannelImportUpdated
			}
		}

		if resOut.ID == uuid.Nil {
			unitIDs, uErr := s.selectAvailableUnitForChannelImport(ctx, tx, propID, mapping, arrival, departure)
			if uErr != nil {
				return uErr
			}
			newResID, uErr := postgres.NewUUID()
			if uErr != nil {
				return uErr
			}
			created, cErr := s.store.CreateReservation(ctx, tx, domain.Reservation{
				ID:            newResID,
				PropertyID:    propID,
				GuestID:       guest.ID,
				GuestName:     guest.FullName,
				GuestEmail:    guest.Email,
				GuestPhone:    guest.Phone,
				ChannelCode:   mapping.BookingChannelCode,
				ArrivalDate:   arrival,
				DepartureDate: departure,
				Adults:        maxIntSvc(er.Adults, 1),
				Children:      maxIntSvc(er.Children, 0),
				Infants:       maxIntSvc(er.Infants, 0),
				Status:        targetStatus,
				InternalNotes: fmt.Sprintf("Imported via %s (%s)", rec.Connection.ProviderCode, er.ExternalReservationID),
				UnitIDs:       unitIDs,
			})
			if cErr != nil {
				return cErr
			}
			if _, fErr := tx.Exec(ctx, `
				UPDATE reservations SET total_pence = $3, balance_due_pence = $3, currency = $4
				WHERE property_id = $1 AND id = $2
			`, propID, created.ID, er.TotalMinor, mapping.Currency); fErr != nil {
				return fErr
			}
			resOut = created
			importStat = domain.ChannelImportImported
		}

		if err := s.store.SyncReservationAutomationJobs(ctx, tx, prop, resOut, s.nowUTC()); err != nil {
			return err
		}

		savedLedger, lErr := s.store.UpsertExternalReservation(ctx, tx, domain.ChannelExternalReservation{
			PropertyID:            propID,
			ChannelConnectionID:   rec.Connection.ID,
			ListingMappingID:      &mapping.ID,
			ProviderCode:          rec.Connection.ProviderCode,
			ExternalReservationID: er.ExternalReservationID,
			ExternalRevision:      er.ExternalRevision,
			ExternalUpdatedAt:     er.ProviderUpdatedAt,
			ReservationID:         &resOut.ID,
			ImportStatus:          importStat,
			PayloadDigest:         er.PayloadHash,
		})
		if lErr != nil {
			return lErr
		}
		return s.recordAudit(ctx, tx, channelSystemActor(), &propID, "channel_external_reservation.synced", "channel_external_reservation", savedLedger.ID.String(), nil, savedLedger)
	})
	if txErr != nil {
		if errors.Is(txErr, domain.ErrOverlap) || errors.Is(txErr, domain.ErrChannelReservationOverbooked) {
			var existingResID *uuid.UUID
			if hasExisting {
				existingResID = existing.ReservationID
			}
			return s.recordConflictLedger(ctx, propID, rec.Connection.ID, &mapping.ID, rec.Connection.ProviderCode, er, existingResID, "OVERBOOKING_INVENTORY_CONFLICT")
		}
		return txErr
	}
	return nil
}

func (s *Service) recordConflictLedger(
	ctx context.Context,
	propertyID, connID uuid.UUID,
	mappingID *uuid.UUID,
	providerCode string,
	er channels.ExternalReservation,
	existingResID *uuid.UUID,
	reason string,
) error {
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		saved, err := s.store.UpsertExternalReservation(ctx, tx, domain.ChannelExternalReservation{
			PropertyID:            propertyID,
			ChannelConnectionID:   connID,
			ListingMappingID:      mappingID,
			ProviderCode:          providerCode,
			ExternalReservationID: er.ExternalReservationID,
			ExternalRevision:      er.ExternalRevision,
			ExternalUpdatedAt:     er.ProviderUpdatedAt,
			ReservationID:         existingResID,
			ImportStatus:          domain.ChannelImportConflictOverbooked,
			ConflictReason:        reason,
			PayloadDigest:         er.PayloadHash,
		})
		if err != nil {
			return err
		}
		_ = s.store.UpdateChannelConnectionSyncResult(ctx, tx, connID, nil, reason, reason, nil)
		return s.recordAudit(ctx, tx, channelSystemActor(), &propertyID, "channel_external_reservation.conflict", "channel_external_reservation", saved.ID.String(), nil, saved)
	})
}

func (s *Service) selectAvailableUnitForChannelImport(
	ctx context.Context,
	tx pgx.Tx,
	propertyID uuid.UUID,
	mapping domain.ChannelListingMapping,
	arrival, departure time.Time,
) ([]uuid.UUID, error) {
	if mapping.UnitID != nil && *mapping.UnitID != uuid.Nil {
		var cnt int
		err := tx.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM inventory_claims
			WHERE property_id = $1
			  AND unit_id = $2
			  AND status = 'ACTIVE'
			  AND claim_type <> 'CHANNEL_RESTRICTION'
			  AND claim_range && daterange($3::date, $4::date, '[)')
		`, propertyID, *mapping.UnitID, domain.FormatDate(arrival), domain.FormatDate(departure)).Scan(&cnt)
		if err != nil {
			return nil, postgres.MapErr(err)
		}
		if cnt > 0 {
			return nil, domain.ErrChannelReservationOverbooked
		}
		return []uuid.UUID{*mapping.UnitID}, nil
	}

	var unitID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT u.id
		FROM units u
		WHERE u.property_id = $1
		  AND u.unit_type_id = $2
		  AND u.archived_at IS NULL
		  AND u.status = 'active'
		  AND NOT EXISTS (
			  SELECT 1
			  FROM inventory_claims ic
			  WHERE ic.property_id = u.property_id
			    AND ic.unit_id = u.id
			    AND ic.status = 'ACTIVE'
			    AND ic.claim_type <> 'CHANNEL_RESTRICTION'
			    AND ic.claim_range && daterange($3::date, $4::date, '[)')
		  )
		ORDER BY u.code ASC
		LIMIT 1
		FOR UPDATE OF u SKIP LOCKED
	`, propertyID, mapping.UnitTypeID, domain.FormatDate(arrival), domain.FormatDate(departure)).Scan(&unitID)
	if err == pgx.ErrNoRows {
		return nil, domain.ErrChannelReservationOverbooked
	}
	if err != nil {
		return nil, postgres.MapErr(err)
	}
	return []uuid.UUID{unitID}, nil
}

// ProcessChannelWebhook verifies cryptographic signatures/secrets, deduplicates events,
// and enqueues an authoritative API hydration/sync job without leaking secrets or PII.
func (s *Service) ProcessChannelWebhook(
	ctx context.Context,
	providerCode string,
	connectionID uuid.UUID,
	endpointToken string,
	rawPath string,
	headers map[string]string,
	body []byte,
) (WebhookProcessResult, error) {
	adapter, def, err := s.getLiveAdapter(providerCode)
	if err != nil {
		return WebhookProcessResult{}, err
	}
	rec, err := s.store.GetChannelConnectionByID(ctx, nil, connectionID)
	if err != nil {
		return WebhookProcessResult{}, domain.ErrChannelWebhookUnauthorized
	}
	if rec.Connection.ProviderCode != def.Code || rec.Connection.Status == channels.StatusDisabled || !rec.Connection.HasCredentials {
		return WebhookProcessResult{}, domain.ErrChannelWebhookUnauthorized
	}
	requireEndpointToken := strings.EqualFold(strings.TrimSpace(rec.Connection.PublicConfig["require_endpoint_token"]), "true")
	if rec.Connection.WebhookEndpointToken != "" && (requireEndpointToken || strings.TrimSpace(endpointToken) != "") {
		if !auth.ConstantTimeEqual(strings.TrimSpace(endpointToken), rec.Connection.WebhookEndpointToken) {
			return WebhookProcessResult{}, domain.ErrChannelWebhookUnauthorized
		}
	}

	creds, err := s.openConnectionCreds(rec)
	if err != nil {
		return WebhookProcessResult{}, domain.ErrChannelWebhookUnauthorized
	}

	mergedHeaders := make(map[string]string, len(headers)+1)
	for k, v := range headers {
		mergedHeaders[k] = v
	}
	if rawPath != "" {
		mergedHeaders["x-lovelace-webhook-path"] = rawPath
	}

	ev, vErr := adapter.VerifyWebhook(ctx, rec.Connection.PublicConfig, creds, mergedHeaders, body, func(ctx context.Context, pCode, nonce string, exp time.Time) (bool, error) {
		return s.store.ConsumeChannelHMACNonce(ctx, pCode, nonce, exp)
	})
	if vErr != nil {
		return WebhookProcessResult{}, domain.ErrChannelWebhookUnauthorized
	}

	var result WebhookProcessResult
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		safeSummary, _ := json.Marshal(map[string]string{
			"action":        ev.Action,
			"event_type":    ev.EventType,
			"provider_code": def.Code,
		})
		whRec, inserted, rErr := s.store.RecordChannelWebhookEvent(ctx, tx, domain.ChannelWebhookEvent{
			PropertyID:          rec.Connection.PropertyID,
			ChannelConnectionID: rec.Connection.ID,
			ProviderCode:        def.Code,
			ExternalEventID:     ev.ExternalEventID,
			EventType:           ev.EventType,
			SignatureVerified:   ev.SignatureVerified,
			Status:              domain.ChannelWebhookQueued,
			SafeSummary:         safeSummary,
		})
		if rErr != nil {
			return rErr
		}
		result.WebhookEventID = whRec.ID
		result.Action = ev.Action
		result.Deduplicated = !inserted
		result.Status = string(whRec.Status)
		if !inserted {
			return nil
		}

		_ = s.recordAudit(ctx, tx, channelSystemActor(), &rec.Connection.PropertyID, "channel_webhook_event.received", "channel_webhook_event", whRec.ID.String(), nil, whRec)

		scopeRaw, _ := json.Marshal(map[string]string{
			"webhook_event_id":        whRec.ID.String(),
			"action":                  ev.Action,
			"external_property_id":    ev.ExternalPropertyID,
			"external_reservation_id": ev.ExternalReservationID,
			"revision_ack_id":         ev.RevisionAckID,
		})
		job, _, jErr := s.store.EnqueueChannelSyncJob(ctx, tx, domain.ChannelSyncJob{
			PropertyID:          rec.Connection.PropertyID,
			ChannelConnectionID: rec.Connection.ID,
			ProviderCode:        def.Code,
			JobType:             domain.ChannelJobProcessWebhookEvent,
			IdempotencyKey:      "webhook:" + whRec.ID.String(),
			ScopePayload:        scopeRaw,
		})
		if jErr != nil {
			return jErr
		}
		result.JobID = job.ID
		return s.recordAudit(ctx, tx, channelSystemActor(), &rec.Connection.PropertyID, "channel_sync_job.enqueued", "channel_sync_job", job.ID.String(), nil, job)
	})
	return result, err
}

func channelSystemActor() Actor {
	return Actor{
		Email: "channel-sync@system.lovelace",
		Role:  domain.RoleOwnerAdmin,
	}
}

// ValidateNoCustomBaseURLs rejects unexpected arbitrary URL fields in public provider configuration.
func ValidateChannelPublicURLSafe(pub map[string]string) error {
	for k, v := range pub {
		lk := strings.ToLower(strings.TrimSpace(k))
		if strings.Contains(lk, "url") || strings.Contains(lk, "endpoint") || strings.Contains(lk, "host") {
			u, err := url.Parse(strings.TrimSpace(v))
			if err != nil || (u.Scheme != "" && u.Scheme != "https") {
				return domain.NewValidationError(map[string]string{k: "custom provider URLs are prohibited"})
			}
		}
	}
	return nil
}

func computeJSONDigest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func maxIntSvc(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// SortedCapabilityItems returns capabilities in stable UI display order.
func SortedCapabilityItems(items []channels.CapabilityItem) []channels.CapabilityItem {
	out := append([]channels.CapabilityItem(nil), items...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
