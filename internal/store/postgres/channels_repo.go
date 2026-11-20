package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Prestwickuk/lovelace/internal/channels"
	"github.com/Prestwickuk/lovelace/internal/domain"
)

const channelConnectionSelectCols = `
	id, property_id, provider_code, display_name, auth_method, status,
	public_config, encrypted_credentials, credentials_key_version,
	credential_hints, last_error, credentials_updated_at,
	capabilities, capability_statuses, token_expires_at,
	last_verified_at, last_synced_at, last_sync_error_code, webhook_endpoint_token,
	version, created_at, updated_at
`

type ChannelConnectionRecord struct {
	Connection           channels.ChannelConnection
	EncryptedCredentials []byte
	KeyVersion           int
}

func (s *Store) ListChannelConnections(ctx context.Context, db DBTX, propertyID uuid.UUID) ([]ChannelConnectionRecord, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT `+channelConnectionSelectCols+`
		FROM channel_connections
		WHERE property_id = $1
		ORDER BY provider_code ASC
	`, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []ChannelConnectionRecord
	for rows.Next() {
		rec, err := scanChannelConnection(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, rec)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetChannelConnection(ctx context.Context, db DBTX, propertyID uuid.UUID, providerCode string) (ChannelConnectionRecord, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT `+channelConnectionSelectCols+`
		FROM channel_connections
		WHERE property_id = $1 AND provider_code = $2
	`, propertyID, providerCode)
	rec, err := scanChannelConnection(row)
	return rec, MapErr(err)
}

func (s *Store) GetChannelConnectionByID(ctx context.Context, db DBTX, id uuid.UUID) (ChannelConnectionRecord, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT `+channelConnectionSelectCols+`
		FROM channel_connections
		WHERE id = $1
	`, id)
	rec, err := scanChannelConnection(row)
	return rec, MapErr(err)
}

func (s *Store) UpsertChannelConnection(
	ctx context.Context,
	tx pgx.Tx,
	id uuid.UUID,
	propertyID uuid.UUID,
	providerCode string,
	displayName string,
	authMethod channels.AuthMethod,
	status channels.ConnectionStatus,
	publicConfig map[string]string,
	encryptedCreds []byte,
	hints map[string]string,
	actorID uuid.UUID,
) (ChannelConnectionRecord, error) {
	pubJSON, err := json.Marshal(nonNilStringMap(publicConfig))
	if err != nil {
		return ChannelConnectionRecord{}, err
	}
	hintsJSON, err := json.Marshal(nonNilStringMap(hints))
	if err != nil {
		return ChannelConnectionRecord{}, err
	}

	var credUpdatedAt *time.Time
	if len(encryptedCreds) > 0 {
		now := time.Now().UTC()
		credUpdatedAt = &now
	}
	whToken := generateWebhookToken()

	row := tx.QueryRow(ctx, `
		INSERT INTO channel_connections (
			id, property_id, provider_code, display_name, auth_method, status,
			public_config, encrypted_credentials, credentials_key_version,
			credential_hints, credentials_updated_at, webhook_endpoint_token,
			created_by, updated_by
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, 1,
			$9, $10, $11,
			$12, $12
		)
		ON CONFLICT (property_id, provider_code) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			auth_method = EXCLUDED.auth_method,
			status = EXCLUDED.status,
			public_config = EXCLUDED.public_config,
			encrypted_credentials = COALESCE(EXCLUDED.encrypted_credentials, channel_connections.encrypted_credentials),
			credential_hints = CASE
				WHEN EXCLUDED.encrypted_credentials IS NOT NULL THEN EXCLUDED.credential_hints
				ELSE channel_connections.credential_hints
			END,
			credentials_updated_at = COALESCE(EXCLUDED.credentials_updated_at, channel_connections.credentials_updated_at),
			webhook_endpoint_token = CASE
				WHEN channel_connections.webhook_endpoint_token = '' THEN EXCLUDED.webhook_endpoint_token
				ELSE channel_connections.webhook_endpoint_token
			END,
			updated_by = EXCLUDED.updated_by,
			version = channel_connections.version + 1,
			updated_at = NOW()
		RETURNING `+channelConnectionSelectCols+`
	`,
		id, propertyID, providerCode, displayName, string(authMethod), string(status),
		pubJSON, nullableBytes(encryptedCreds), hintsJSON, credUpdatedAt, whToken, actorID,
	)
	rec, err := scanChannelConnection(row)
	return rec, MapErr(err)
}

func (s *Store) SetChannelConnectionStatus(
	ctx context.Context,
	tx pgx.Tx,
	propertyID uuid.UUID,
	providerCode string,
	status channels.ConnectionStatus,
	actorID uuid.UUID,
) (ChannelConnectionRecord, error) {
	row := tx.QueryRow(ctx, `
		UPDATE channel_connections
		SET status = $3, updated_by = $4, version = version + 1, updated_at = NOW()
		WHERE property_id = $1 AND provider_code = $2
		RETURNING `+channelConnectionSelectCols+`
	`, propertyID, providerCode, string(status), actorID)
	rec, err := scanChannelConnection(row)
	return rec, MapErr(err)
}

func (s *Store) DeleteChannelConnection(ctx context.Context, tx pgx.Tx, propertyID uuid.UUID, providerCode string) error {
	tag, err := tx.Exec(ctx, `
		DELETE FROM channel_connections WHERE property_id = $1 AND provider_code = $2
	`, propertyID, providerCode)
	if err != nil {
		return MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func scanChannelConnection(r scannable) (ChannelConnectionRecord, error) {
	var rec ChannelConnectionRecord
	var authMethod, status string
	var pubRaw, hintsRaw, capRaw, capStatRaw []byte
	err := r.Scan(
		&rec.Connection.ID, &rec.Connection.PropertyID, &rec.Connection.ProviderCode,
		&rec.Connection.DisplayName, &authMethod, &status,
		&pubRaw, &rec.EncryptedCredentials, &rec.KeyVersion,
		&hintsRaw, &rec.Connection.LastError, &rec.Connection.CredentialsUpdatedAt,
		&capRaw, &capStatRaw, &rec.Connection.TokenExpiresAt,
		&rec.Connection.LastVerifiedAt, &rec.Connection.LastSyncedAt,
		&rec.Connection.LastSyncErrorCode, &rec.Connection.WebhookEndpointToken,
		&rec.Connection.Version, &rec.Connection.CreatedAt, &rec.Connection.UpdatedAt,
	)
	if err != nil {
		return ChannelConnectionRecord{}, err
	}
	rec.Connection.AuthMethod = channels.AuthMethod(authMethod)
	rec.Connection.Status = channels.ConnectionStatus(status)
	rec.Connection.HasCredentials = len(rec.EncryptedCredentials) > 0
	if def, ok := channels.GetProvider(rec.Connection.ProviderCode); ok {
		rec.Connection.ProviderName = def.Name
		rec.Connection.HasLiveAdapter = def.HasLiveAdapter
	}
	_ = json.Unmarshal(pubRaw, &rec.Connection.PublicConfig)
	if rec.Connection.PublicConfig == nil {
		rec.Connection.PublicConfig = map[string]string{}
	}
	_ = json.Unmarshal(hintsRaw, &rec.Connection.CredentialHints)
	if rec.Connection.CredentialHints == nil {
		rec.Connection.CredentialHints = map[string]string{}
	}
	_ = json.Unmarshal(capRaw, &rec.Connection.Capabilities)
	_ = json.Unmarshal(capStatRaw, &rec.Connection.CapabilityStatuses)
	return rec, nil
}

func generateWebhookToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func nonNilStringMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func nullableBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
