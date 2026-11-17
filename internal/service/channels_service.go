package service

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/channels"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/secrets"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

type ChannelProviderView struct {
	Provider   channels.ProviderDefinition `json:"provider"`
	Connection *channels.ChannelConnection `json:"connection,omitempty"`
}

func (s *Service) ListChannelProviders(ctx context.Context, actor Actor, propertyID uuid.UUID) ([]ChannelProviderView, error) {
	if err := s.requirePerm(actor, auth.PermChannelsRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	records, err := s.store.ListChannelConnections(ctx, nil, propertyID)
	if err != nil {
		return nil, err
	}
	canWrite := auth.HasPermission(actor.Role, auth.PermChannelsWrite)
	byCode := make(map[string]channels.ChannelConnection, len(records))
	for _, rec := range records {
		conn := rec.Connection
		if !canWrite {
			conn.WebhookEndpointToken = ""
		}
		if def, ok := channels.GetProvider(conn.ProviderCode); ok {
			conn.ProviderName = def.Name
			conn.HasLiveAdapter = def.HasLiveAdapter
		}
		byCode[conn.ProviderCode] = conn
	}

	providers := channels.ListProviders()
	out := make([]ChannelProviderView, 0, len(providers))
	for _, def := range providers {
		view := ChannelProviderView{Provider: def}
		if conn, ok := byCode[def.Code]; ok {
			copyConn := conn
			view.Connection = &copyConn
		}
		out = append(out, view)
	}
	return out, nil
}

type SaveChannelConfigInput struct {
	PropertyID   uuid.UUID                 `json:"property_id"`
	ProviderCode string                    `json:"provider_code"`
	DisplayName  string                    `json:"display_name"`
	Inputs       map[string]string         `json:"inputs"`
	TargetStatus channels.ConnectionStatus `json:"status,omitempty"`
}

func (s *Service) SaveChannelConfig(ctx context.Context, actor Actor, in SaveChannelConfigInput) (channels.ChannelConnection, error) {
	if err := s.requirePerm(actor, auth.PermChannelsWrite); err != nil {
		return channels.ChannelConnection{}, err
	}
	if err := s.requirePropertyScope(actor, in.PropertyID); err != nil {
		return channels.ChannelConnection{}, err
	}
	if s.secretBox == nil {
		return channels.ChannelConnection{}, domain.NewValidationError(map[string]string{
			"encryption": "channel encryption key is not configured",
		})
	}

	def, ok := channels.GetProvider(in.ProviderCode)
	if !ok {
		return channels.ChannelConnection{}, domain.NewValidationError(map[string]string{
			"provider_code": "unsupported channel manager provider",
		})
	}

	if in.TargetStatus == channels.StatusConnected {
		return channels.ChannelConnection{}, domain.NewValidationError(map[string]string{
			"status": "CONNECTED can only be entered via live connection verification",
		})
	}

	var saved postgres.ChannelConnectionRecord
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.store.GetPropertyByID(ctx, tx, in.PropertyID); err != nil {
			return err
		}

		existing, err := s.store.GetChannelConnection(ctx, tx, in.PropertyID, def.Code)
		hasExistingSecrets := err == nil && len(existing.EncryptedCredentials) > 0
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}

		pubCfg, newSecrets, err := channels.ValidateAndSplitInputs(def, in.Inputs, hasExistingSecrets)
		if err != nil {
			return domain.NewValidationError(map[string]string{
				"inputs": err.Error(),
			})
		}

		mergedSecrets := make(map[string]string)
		if hasExistingSecrets {
			prev, err := s.secretBox.OpenMap(existing.EncryptedCredentials)
			if err != nil {
				return err
			}
			for k, v := range prev {
				mergedSecrets[k] = v
			}
		}
		for k, v := range newSecrets {
			mergedSecrets[k] = v
		}

		hints := make(map[string]string, len(mergedSecrets))
		for k, v := range mergedSecrets {
			hints[k] = secrets.MaskSecret(v)
		}

		var encrypted []byte
		if len(mergedSecrets) > 0 {
			encrypted, err = s.secretBox.SealMap(mergedSecrets)
			if err != nil {
				return err
			}
		}

		status := in.TargetStatus
		switch status {
		case channels.StatusDisabled, channels.StatusAwaitingAuth, channels.StatusCredentialsSaved, channels.StatusConfigurationReq:
		case "":
			if def.AuthMethod == channels.AuthOAuth2 || def.AuthMethod == channels.AuthClientCredentials {
				status = channels.StatusAwaitingAuth
			} else if len(mergedSecrets) > 0 {
				status = channels.StatusCredentialsSaved
			} else {
				status = channels.StatusConfigurationReq
			}
		default:
			return domain.NewValidationError(map[string]string{
				"status": "invalid configuration status",
			})
		}

		connID := existing.Connection.ID
		if connID == uuid.Nil {
			connID, err = postgres.NewUUID()
			if err != nil {
				return err
			}
		}
		displayName := strings.TrimSpace(in.DisplayName)
		if displayName == "" {
			displayName = def.Name
		}

		saved, err = s.store.UpsertChannelConnection(
			ctx, tx,
			connID, in.PropertyID, def.Code, displayName, def.AuthMethod, status,
			pubCfg, encrypted, hints, actor.UserID,
		)
		if err != nil {
			return err
		}
		saved.Connection.ProviderName = def.Name
		saved.Connection.HasLiveAdapter = def.HasLiveAdapter

		// Never write raw secrets to audit_logs — record only non-secret config and masked hints.
		return s.recordAudit(ctx, tx, actor, &in.PropertyID, "channel_connection.saved", "channel_connection", saved.Connection.ID.String(), nil, saved.Connection)
	})
	return saved.Connection, err
}

func (s *Service) SetChannelStatus(ctx context.Context, actor Actor, propertyID uuid.UUID, providerCode string, status channels.ConnectionStatus) (channels.ChannelConnection, error) {
	if err := s.requirePerm(actor, auth.PermChannelsWrite); err != nil {
		return channels.ChannelConnection{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return channels.ChannelConnection{}, err
	}
	def, ok := channels.GetProvider(providerCode)
	if !ok {
		return channels.ChannelConnection{}, domain.NewValidationError(map[string]string{
			"provider_code": "unsupported channel manager provider",
		})
	}
	if status == channels.StatusConnected {
		return channels.ChannelConnection{}, domain.NewValidationError(map[string]string{
			"status": "CONNECTED can only be entered via live connection verification",
		})
	}
	switch status {
	case channels.StatusDisabled, channels.StatusCredentialsSaved, channels.StatusAwaitingAuth, channels.StatusConfigurationReq:
	default:
		return channels.ChannelConnection{}, domain.NewValidationError(map[string]string{
			"status": "unsupported status transition",
		})
	}

	var updated postgres.ChannelConnectionRecord
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetChannelConnection(ctx, tx, propertyID, def.Code)
		if err != nil {
			return err
		}
		updated, err = s.store.SetChannelConnectionStatus(ctx, tx, propertyID, def.Code, status, actor.UserID)
		if err != nil {
			return err
		}
		updated.Connection.ProviderName = def.Name
		updated.Connection.HasLiveAdapter = def.HasLiveAdapter
		return s.recordAudit(ctx, tx, actor, &propertyID, "channel_connection.status_changed", "channel_connection", updated.Connection.ID.String(), before.Connection, updated.Connection)
	})
	return updated.Connection, err
}

func (s *Service) DeleteChannelConfig(ctx context.Context, actor Actor, propertyID uuid.UUID, providerCode string) error {
	if err := s.requirePerm(actor, auth.PermChannelsWrite); err != nil {
		return err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return err
	}
	def, ok := channels.GetProvider(providerCode)
	if !ok {
		return domain.NewValidationError(map[string]string{
			"provider_code": "unsupported channel manager provider",
		})
	}

	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetChannelConnection(ctx, tx, propertyID, def.Code)
		if err != nil {
			return err
		}
		if err := s.store.DeleteChannelConnection(ctx, tx, propertyID, def.Code); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "channel_connection.deleted", "channel_connection", before.Connection.ID.String(), before.Connection, nil)
	})
}
