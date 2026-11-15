package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/channels"
	"github.com/tirbach/tir-bach/internal/domain"
)

func (s *Store) UpdateChannelConnectionVerified(
	ctx context.Context,
	tx pgx.Tx,
	connectionID uuid.UUID,
	status channels.ConnectionStatus,
	capabilities []string,
	capStatuses map[string]channels.CapabilityStatus,
	encryptedCreds []byte,
	hints map[string]string,
	tokenExpiresAt *time.Time,
	verifiedAt time.Time,
	lastErrCode string,
	lastErrMsg string,
	actorID *uuid.UUID,
) (ChannelConnectionRecord, error) {
	capsJSON, _ := json.Marshal(capabilities)
	statJSON, _ := json.Marshal(capStatuses)
	hintsJSON, _ := json.Marshal(nonNilStringMap(hints))
	var credsUpdatedAt *time.Time
	if len(encryptedCreds) > 0 {
		now := time.Now().UTC()
		credsUpdatedAt = &now
	}
	row := tx.QueryRow(ctx, `
		UPDATE channel_connections
		SET status = $2,
		    capabilities = $3,
		    capability_statuses = $4,
		    encrypted_credentials = COALESCE($5, encrypted_credentials),
		    credential_hints = CASE WHEN $5 IS NOT NULL THEN $6 ELSE credential_hints END,
		    credentials_updated_at = COALESCE($7, credentials_updated_at),
		    token_expires_at = COALESCE($8, token_expires_at),
		    last_verified_at = $9,
		    last_sync_error_code = $10,
		    last_error = $11,
		    updated_by = COALESCE($12, updated_by),
		    version = version + 1,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING `+channelConnectionSelectCols+`
	`, connectionID, string(status), capsJSON, statJSON,
		nullableBytes(encryptedCreds), hintsJSON, credsUpdatedAt, tokenExpiresAt,
		verifiedAt.UTC(), lastErrCode, lastErrMsg, actorID)
	rec, err := scanChannelConnection(row)
	return rec, MapErr(err)
}

func (s *Store) UpdateChannelConnectionTokens(
	ctx context.Context,
	tx pgx.Tx,
	connectionID uuid.UUID,
	encryptedCreds []byte,
	hints map[string]string,
	tokenExpiresAt *time.Time,
) error {
	hintsJSON, _ := json.Marshal(nonNilStringMap(hints))
	_, err := tx.Exec(ctx, `
		UPDATE channel_connections
		SET encrypted_credentials = $2,
		    credential_hints = $3,
		    credentials_updated_at = NOW(),
		    token_expires_at = $4,
		    updated_at = NOW()
		WHERE id = $1
	`, connectionID, encryptedCreds, hintsJSON, tokenExpiresAt)
	return MapErr(err)
}

func (s *Store) ClearChannelConnectionCredentials(
	ctx context.Context,
	tx pgx.Tx,
	connectionID uuid.UUID,
	actorID uuid.UUID,
	clearWebhookID bool,
	lastErrCode string,
	lastErr string,
) (ChannelConnectionRecord, error) {
	row := tx.QueryRow(ctx, `
		UPDATE channel_connections
		SET status = 'DISABLED',
		    encrypted_credentials = NULL,
		    credential_hints = '{}'::jsonb,
		    token_expires_at = NULL,
		    public_config = CASE WHEN $3 THEN public_config - 'webhook_id' ELSE public_config END,
		    last_sync_error_code = $4,
		    last_error = $5,
		    updated_by = $2,
		    version = version + 1,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING `+channelConnectionSelectCols+`
	`, connectionID, actorID, clearWebhookID, lastErrCode, lastErr)
	rec, err := scanChannelConnection(row)
	return rec, MapErr(err)
}

func (s *Store) UpdateChannelConnectionSyncResult(
	ctx context.Context,
	db DBTX,
	connectionID uuid.UUID,
	statusOverride *channels.ConnectionStatus,
	lastErrCode string,
	lastErrMsg string,
	syncedAt *time.Time,
) error {
	if db == nil {
		db = s.pool
	}
	var st *string
	if statusOverride != nil {
		v := string(*statusOverride)
		st = &v
	}
	_, err := db.Exec(ctx, `
		UPDATE channel_connections
		SET status = COALESCE($2, status),
		    last_sync_error_code = $3,
		    last_error = $4,
		    last_synced_at = COALESCE($5, last_synced_at),
		    updated_at = NOW()
		WHERE id = $1
	`, connectionID, st, lastErrCode, lastErrMsg, syncedAt)
	return MapErr(err)
}

func (s *Store) ListChannelListingMappings(ctx context.Context, db DBTX, propertyID uuid.UUID, providerCode string) ([]domain.ChannelListingMapping, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT id, property_id, channel_connection_id, provider_code,
		       external_property_id, external_listing_id, external_rate_plan_id, external_name,
		       unit_type_id, unit_id, rate_plan_id, booking_channel_code,
		       currency, timezone, sync_availability, sync_rates, sync_reservations,
		       ownership_mode, is_active, version, created_at, updated_at
		FROM channel_listing_mappings
		WHERE property_id = $1
		  AND ($2 = '' OR provider_code = $2)
		ORDER BY provider_code ASC, external_listing_id ASC, created_at ASC
	`, propertyID, strings.ToLower(strings.TrimSpace(providerCode)))
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	out := make([]domain.ChannelListingMapping, 0)
	for rows.Next() {
		m, err := scanChannelListingMapping(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, m)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetChannelListingMapping(ctx context.Context, db DBTX, propertyID, mappingID uuid.UUID) (domain.ChannelListingMapping, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT id, property_id, channel_connection_id, provider_code,
		       external_property_id, external_listing_id, external_rate_plan_id, external_name,
		       unit_type_id, unit_id, rate_plan_id, booking_channel_code,
		       currency, timezone, sync_availability, sync_rates, sync_reservations,
		       ownership_mode, is_active, version, created_at, updated_at
		FROM channel_listing_mappings
		WHERE property_id = $1 AND id = $2
	`, propertyID, mappingID)
	m, err := scanChannelListingMapping(row)
	return m, MapErr(err)
}

func (s *Store) FindChannelListingMappingByExternal(
	ctx context.Context,
	db DBTX,
	propertyID uuid.UUID,
	providerCode string,
	externalListingID string,
	externalRatePlanID string,
) (domain.ChannelListingMapping, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT id, property_id, channel_connection_id, provider_code,
		       external_property_id, external_listing_id, external_rate_plan_id, external_name,
		       unit_type_id, unit_id, rate_plan_id, booking_channel_code,
		       currency, timezone, sync_availability, sync_rates, sync_reservations,
		       ownership_mode, is_active, version, created_at, updated_at
		FROM channel_listing_mappings
		WHERE property_id = $1
		  AND provider_code = $2
		  AND external_listing_id = $3
		  AND is_active = TRUE
		ORDER BY CASE WHEN external_rate_plan_id = $4 THEN 0 ELSE 1 END, created_at ASC
		LIMIT 1
	`, propertyID, providerCode, strings.TrimSpace(externalListingID), strings.TrimSpace(externalRatePlanID))
	m, err := scanChannelListingMapping(row)
	return m, MapErr(err)
}

func (s *Store) UpsertChannelListingMapping(ctx context.Context, tx pgx.Tx, in domain.ChannelListingMapping, actorID uuid.UUID) (domain.ChannelListingMapping, error) {
	if in.ID == uuid.Nil {
		id, err := NewUUID()
		if err != nil {
			return domain.ChannelListingMapping{}, err
		}
		in.ID = id
	}
	if strings.TrimSpace(in.Currency) == "" {
		in.Currency = "GBP"
	}
	if strings.TrimSpace(in.Timezone) == "" {
		in.Timezone = "Europe/London"
	}
	if in.OwnershipMode == "" {
		in.OwnershipMode = domain.ChannelOwnershipLovelaceARI
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO channel_listing_mappings (
			id, property_id, channel_connection_id, provider_code,
			external_property_id, external_listing_id, external_rate_plan_id, external_name,
			unit_type_id, unit_id, rate_plan_id, booking_channel_code,
			currency, timezone, sync_availability, sync_rates, sync_reservations,
			ownership_mode, is_active, created_by, updated_by
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8,
			$9, $10, $11, $12,
			$13, $14, $15, $16, $17,
			$18, $19, $20, $20
		)
		ON CONFLICT (property_id, provider_code, external_listing_id, external_rate_plan_id) DO UPDATE SET
			external_property_id = EXCLUDED.external_property_id,
			external_name = EXCLUDED.external_name,
			unit_type_id = EXCLUDED.unit_type_id,
			unit_id = EXCLUDED.unit_id,
			rate_plan_id = EXCLUDED.rate_plan_id,
			booking_channel_code = EXCLUDED.booking_channel_code,
			currency = EXCLUDED.currency,
			timezone = EXCLUDED.timezone,
			sync_availability = EXCLUDED.sync_availability,
			sync_rates = EXCLUDED.sync_rates,
			sync_reservations = EXCLUDED.sync_reservations,
			ownership_mode = EXCLUDED.ownership_mode,
			is_active = EXCLUDED.is_active,
			updated_by = EXCLUDED.updated_by,
			version = channel_listing_mappings.version + 1,
			updated_at = NOW()
		RETURNING id, property_id, channel_connection_id, provider_code,
		          external_property_id, external_listing_id, external_rate_plan_id, external_name,
		          unit_type_id, unit_id, rate_plan_id, booking_channel_code,
		          currency, timezone, sync_availability, sync_rates, sync_reservations,
		          ownership_mode, is_active, version, created_at, updated_at
	`,
		in.ID, in.PropertyID, in.ChannelConnectionID, in.ProviderCode,
		strings.TrimSpace(in.ExternalPropertyID), strings.TrimSpace(in.ExternalListingID),
		strings.TrimSpace(in.ExternalRatePlanID), strings.TrimSpace(in.ExternalName),
		in.UnitTypeID, in.UnitID, in.RatePlanID, strings.ToUpper(strings.TrimSpace(in.BookingChannelCode)),
		strings.ToUpper(strings.TrimSpace(in.Currency)), strings.TrimSpace(in.Timezone),
		in.SyncAvailability, in.SyncRates, in.SyncReservations,
		string(in.OwnershipMode), in.IsActive, actorID,
	)
	m, err := scanChannelListingMapping(row)
	return m, MapErr(err)
}

func (s *Store) DeleteChannelListingMapping(ctx context.Context, tx pgx.Tx, propertyID, mappingID uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		DELETE FROM channel_listing_mappings WHERE property_id = $1 AND id = $2
	`, propertyID, mappingID)
	if err != nil {
		return MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func scanChannelListingMapping(r scannable) (domain.ChannelListingMapping, error) {
	var m domain.ChannelListingMapping
	var ownerMode string
	err := r.Scan(
		&m.ID, &m.PropertyID, &m.ChannelConnectionID, &m.ProviderCode,
		&m.ExternalPropertyID, &m.ExternalListingID, &m.ExternalRatePlanID, &m.ExternalName,
		&m.UnitTypeID, &m.UnitID, &m.RatePlanID, &m.BookingChannelCode,
		&m.Currency, &m.Timezone, &m.SyncAvailability, &m.SyncRates, &m.SyncReservations,
		&ownerMode, &m.IsActive, &m.Version, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return domain.ChannelListingMapping{}, err
	}
	m.OwnershipMode = domain.ChannelOwnershipMode(ownerMode)
	return m, nil
}

const channelSyncJobSelectCols = `
	id, property_id, channel_connection_id, listing_mapping_id, provider_code,
	job_type, idempotency_key, scope_payload, status, attempts, max_attempts,
	next_attempt_at, worker_id, lease_token, lease_expires_at, fence_epoch,
	last_error_code, last_error_summary, started_at, completed_at, created_by,
	created_at, updated_at
`

func (s *Store) EnqueueChannelSyncJob(ctx context.Context, db DBTX, job domain.ChannelSyncJob) (domain.ChannelSyncJob, bool, error) {
	if db == nil {
		db = s.pool
	}
	if job.ID == uuid.Nil {
		id, err := NewUUID()
		if err != nil {
			return domain.ChannelSyncJob{}, false, err
		}
		job.ID = id
	}
	if job.MaxAttempts <= 0 {
		job.MaxAttempts = 5
	}
	if len(job.ScopePayload) == 0 {
		job.ScopePayload = json.RawMessage("{}")
	}
	var nextAttemptAt *time.Time
	if !job.NextAttemptAt.IsZero() {
		t := job.NextAttemptAt.UTC()
		nextAttemptAt = &t
	}

	var insertedID uuid.UUID
	row := db.QueryRow(ctx, `
		INSERT INTO channel_sync_jobs (
			id, property_id, channel_connection_id, listing_mapping_id, provider_code,
			job_type, idempotency_key, scope_payload, status, attempts, max_attempts,
			next_attempt_at, created_by
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, 'PENDING', 0, $9,
			COALESCE($10::timestamptz, NOW()), $11
		)
		ON CONFLICT (property_id, provider_code, idempotency_key) DO NOTHING
		RETURNING id
	`, job.ID, job.PropertyID, job.ChannelConnectionID, job.ListingMappingID, job.ProviderCode,
		string(job.JobType), job.IdempotencyKey, job.ScopePayload, job.MaxAttempts,
		nextAttemptAt, job.CreatedBy,
	)
	err := row.Scan(&insertedID)
	if err == nil {
		out, gErr := s.GetChannelSyncJob(ctx, db, job.PropertyID, insertedID)
		return out, true, gErr
	}
	if err != pgx.ErrNoRows {
		return domain.ChannelSyncJob{}, false, MapErr(err)
	}
	existingRow := db.QueryRow(ctx, `
		SELECT `+channelSyncJobSelectCols+`
		FROM channel_sync_jobs
		WHERE property_id = $1 AND provider_code = $2 AND idempotency_key = $3
	`, job.PropertyID, job.ProviderCode, job.IdempotencyKey)
	existing, gErr := scanChannelSyncJob(existingRow)
	return existing, false, MapErr(gErr)
}

func (s *Store) GetChannelSyncJob(ctx context.Context, db DBTX, propertyID, jobID uuid.UUID) (domain.ChannelSyncJob, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT `+channelSyncJobSelectCols+`
		FROM channel_sync_jobs
		WHERE property_id = $1 AND id = $2
	`, propertyID, jobID)
	j, err := scanChannelSyncJob(row)
	return j, MapErr(err)
}

func (s *Store) ListChannelSyncJobs(ctx context.Context, db DBTX, propertyID uuid.UUID, providerCode string, limit int) ([]domain.ChannelSyncJob, error) {
	if db == nil {
		db = s.pool
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.Query(ctx, `
		SELECT `+channelSyncJobSelectCols+`
		FROM channel_sync_jobs
		WHERE property_id = $1
		  AND ($2 = '' OR provider_code = $2)
		ORDER BY created_at DESC
		LIMIT $3
	`, propertyID, strings.ToLower(strings.TrimSpace(providerCode)), limit)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	out := make([]domain.ChannelSyncJob, 0)
	for rows.Next() {
		j, err := scanChannelSyncJob(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, j)
	}
	return out, MapErr(rows.Err())
}

// ClaimDueChannelSyncJobs atomically leases due or expired-lease jobs using FOR UPDATE SKIP LOCKED
// and increments the monotonic fence_epoch to fence stale workers.
func (s *Store) ClaimDueChannelSyncJobs(ctx context.Context, workerID string, limit int, leaseTTL time.Duration) ([]domain.ChannelSyncJob, error) {
	if limit <= 0 {
		limit = 10
	}
	leaseSecs := int(leaseTTL.Seconds())
	if leaseSecs <= 0 {
		leaseSecs = 45
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	leaseToken, err := NewUUID()
	if err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		WITH due AS (
			SELECT j.id
			FROM channel_sync_jobs j
			WHERE (
				(j.status IN ('PENDING', 'FAILED_RETRYABLE') AND j.next_attempt_at <= NOW())
				OR (j.status = 'RUNNING' AND j.lease_expires_at IS NOT NULL AND j.lease_expires_at <= NOW())
			)
			ORDER BY j.next_attempt_at ASC, j.created_at ASC
			FOR UPDATE OF j SKIP LOCKED
			LIMIT $1
		)
		UPDATE channel_sync_jobs j
		SET status = 'RUNNING',
		    attempts = j.attempts + 1,
		    worker_id = $2,
		    lease_token = $3,
		    lease_expires_at = NOW() + make_interval(secs => $4),
		    fence_epoch = j.fence_epoch + 1,
		    started_at = COALESCE(j.started_at, NOW()),
		    updated_at = NOW()
		FROM due
		WHERE j.id = due.id
		RETURNING `+channelSyncJobSelectColsWithAlias("j")+`
	`, limit, workerID, leaseToken, leaseSecs)
	if err != nil {
		return nil, MapErr(err)
	}
	var claimed []domain.ChannelSyncJob
	for rows.Next() {
		j, err := scanChannelSyncJob(rows)
		if err != nil {
			rows.Close()
			return nil, MapErr(err)
		}
		claimed = append(claimed, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, MapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, MapErr(err)
	}
	return claimed, nil
}

// RenewChannelSyncJobLease extends a running job's lease strictly if worker_id, lease_token,
// fence_epoch, and non-expired lease_expires_at > NOW() still match.
func (s *Store) RenewChannelSyncJobLease(ctx context.Context, jobID uuid.UUID, workerID string, leaseToken uuid.UUID, fenceEpoch int64, leaseTTL time.Duration) (bool, error) {
	leaseSecs := int(leaseTTL.Seconds())
	if leaseSecs <= 0 {
		leaseSecs = 45
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE channel_sync_jobs
		SET lease_expires_at = NOW() + make_interval(secs => $5),
		    updated_at = NOW()
		WHERE id = $1
		  AND status = 'RUNNING'
		  AND worker_id = $2
		  AND lease_token = $3
		  AND fence_epoch = $4
		  AND lease_expires_at > NOW()
	`, jobID, workerID, leaseToken, fenceEpoch, leaseSecs)
	if err != nil {
		return false, MapErr(err)
	}
	return tag.RowsAffected() == 1, nil
}

// CompleteChannelSyncJob atomically settles a leased job iff worker_id, lease_token, fence_epoch,
// and an active non-expired RUNNING lease still hold.
func (s *Store) CompleteChannelSyncJob(
	ctx context.Context,
	jobID uuid.UUID,
	workerID string,
	leaseToken uuid.UUID,
	fenceEpoch int64,
	nextStatus domain.ChannelJobStatus,
	nextAttemptAt *time.Time,
	errCode string,
	errSummary string,
) (bool, error) {
	var nextAt *time.Time
	if nextAttemptAt != nil {
		t := nextAttemptAt.UTC()
		nextAt = &t
	}
	isTerminal := nextStatus == domain.ChannelJobSucceeded ||
		nextStatus == domain.ChannelJobFailedTerminal ||
		nextStatus == domain.ChannelJobCancelled
	tag, err := s.pool.Exec(ctx, `
		UPDATE channel_sync_jobs
		SET status = $5,
		    next_attempt_at = COALESCE($6::timestamptz, NOW()),
		    last_error_code = $7,
		    last_error_summary = $8,
		    completed_at = CASE WHEN $9 THEN COALESCE(completed_at, NOW()) ELSE completed_at END,
		    lease_token = NULL,
		    lease_expires_at = NULL,
		    updated_at = NOW()
		WHERE id = $1
		  AND status = 'RUNNING'
		  AND worker_id = $2
		  AND lease_token = $3
		  AND fence_epoch = $4
		  AND lease_expires_at > NOW()
	`, jobID, workerID, leaseToken, fenceEpoch, string(nextStatus), nextAt, errCode, errSummary, isTerminal)
	if err != nil {
		return false, MapErr(err)
	}
	return tag.RowsAffected() == 1, nil
}

func channelSyncJobSelectColsWithAlias(alias string) string {
	return alias + `.id, ` + alias + `.property_id, ` + alias + `.channel_connection_id, ` + alias + `.listing_mapping_id, ` + alias + `.provider_code, ` +
		alias + `.job_type, ` + alias + `.idempotency_key, ` + alias + `.scope_payload, ` + alias + `.status, ` + alias + `.attempts, ` + alias + `.max_attempts, ` +
		alias + `.next_attempt_at, ` + alias + `.worker_id, ` + alias + `.lease_token, ` + alias + `.lease_expires_at, ` + alias + `.fence_epoch, ` +
		alias + `.last_error_code, ` + alias + `.last_error_summary, ` + alias + `.started_at, ` + alias + `.completed_at, ` + alias + `.created_by, ` +
		alias + `.created_at, ` + alias + `.updated_at`
}

func scanChannelSyncJob(r scannable) (domain.ChannelSyncJob, error) {
	var j domain.ChannelSyncJob
	var jobType, status string
	err := r.Scan(
		&j.ID, &j.PropertyID, &j.ChannelConnectionID, &j.ListingMappingID, &j.ProviderCode,
		&jobType, &j.IdempotencyKey, &j.ScopePayload, &status, &j.Attempts, &j.MaxAttempts,
		&j.NextAttemptAt, &j.WorkerID, &j.LeaseToken, &j.LeaseExpiresAt, &j.FenceEpoch,
		&j.LastErrorCode, &j.LastErrorSummary, &j.StartedAt, &j.CompletedAt, &j.CreatedBy,
		&j.CreatedAt, &j.UpdatedAt,
	)
	if err != nil {
		return domain.ChannelSyncJob{}, err
	}
	j.JobType = domain.ChannelJobType(jobType)
	j.Status = domain.ChannelJobStatus(status)
	return j, nil
}

func (s *Store) GetChannelSyncCheckpoint(ctx context.Context, db DBTX, connectionID uuid.UUID, cursorKey string) (domain.ChannelSyncCheckpoint, error) {
	if db == nil {
		db = s.pool
	}
	var cp domain.ChannelSyncCheckpoint
	err := db.QueryRow(ctx, `
		SELECT id, property_id, channel_connection_id, provider_code, cursor_key,
		       cursor_value, payload_digest, last_synced_at, metadata, updated_at
		FROM channel_sync_checkpoints
		WHERE channel_connection_id = $1 AND cursor_key = $2
	`, connectionID, cursorKey).Scan(
		&cp.ID, &cp.PropertyID, &cp.ChannelConnectionID, &cp.ProviderCode, &cp.CursorKey,
		&cp.CursorValue, &cp.PayloadDigest, &cp.LastSyncedAt, &cp.Metadata, &cp.UpdatedAt,
	)
	return cp, MapErr(err)
}

func (s *Store) UpsertChannelSyncCheckpoint(ctx context.Context, db DBTX, cp domain.ChannelSyncCheckpoint) (domain.ChannelSyncCheckpoint, error) {
	if db == nil {
		db = s.pool
	}
	if cp.ID == uuid.Nil {
		id, err := NewUUID()
		if err != nil {
			return domain.ChannelSyncCheckpoint{}, err
		}
		cp.ID = id
	}
	if len(cp.Metadata) == 0 {
		cp.Metadata = json.RawMessage("{}")
	}
	var out domain.ChannelSyncCheckpoint
	err := db.QueryRow(ctx, `
		INSERT INTO channel_sync_checkpoints (
			id, property_id, channel_connection_id, provider_code, cursor_key,
			cursor_value, payload_digest, last_synced_at, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (channel_connection_id, cursor_key) DO UPDATE SET
			cursor_value = EXCLUDED.cursor_value,
			payload_digest = EXCLUDED.payload_digest,
			last_synced_at = EXCLUDED.last_synced_at,
			metadata = EXCLUDED.metadata,
			updated_at = NOW()
		RETURNING id, property_id, channel_connection_id, provider_code, cursor_key,
		          cursor_value, payload_digest, last_synced_at, metadata, updated_at
	`, cp.ID, cp.PropertyID, cp.ChannelConnectionID, cp.ProviderCode, cp.CursorKey,
		cp.CursorValue, cp.PayloadDigest, cp.LastSyncedAt, cp.Metadata).Scan(
		&out.ID, &out.PropertyID, &out.ChannelConnectionID, &out.ProviderCode, &out.CursorKey,
		&out.CursorValue, &out.PayloadDigest, &out.LastSyncedAt, &out.Metadata, &out.UpdatedAt,
	)
	return out, MapErr(err)
}

func (s *Store) GetExternalReservationByExtID(ctx context.Context, db DBTX, propertyID uuid.UUID, providerCode, extResID string) (domain.ChannelExternalReservation, error) {
	if db == nil {
		db = s.pool
	}
	var out domain.ChannelExternalReservation
	var status string
	err := db.QueryRow(ctx, `
		SELECT id, property_id, channel_connection_id, listing_mapping_id, provider_code,
		       external_reservation_id, external_revision, external_updated_at, reservation_id,
		       import_status, conflict_reason, payload_digest, created_at, updated_at
		FROM channel_external_reservations
		WHERE property_id = $1 AND provider_code = $2 AND external_reservation_id = $3
	`, propertyID, providerCode, extResID).Scan(
		&out.ID, &out.PropertyID, &out.ChannelConnectionID, &out.ListingMappingID, &out.ProviderCode,
		&out.ExternalReservationID, &out.ExternalRevision, &out.ExternalUpdatedAt, &out.ReservationID,
		&status, &out.ConflictReason, &out.PayloadDigest, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return domain.ChannelExternalReservation{}, MapErr(err)
	}
	out.ImportStatus = domain.ChannelImportStatus(status)
	return out, nil
}

func (s *Store) ListExternalReservations(ctx context.Context, db DBTX, propertyID uuid.UUID, providerCode, statusFilter string, limit int) ([]domain.ChannelExternalReservation, error) {
	if db == nil {
		db = s.pool
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := db.Query(ctx, `
		SELECT id, property_id, channel_connection_id, listing_mapping_id, provider_code,
		       external_reservation_id, external_revision, external_updated_at, reservation_id,
		       import_status, conflict_reason, payload_digest, created_at, updated_at
		FROM channel_external_reservations
		WHERE property_id = $1
		  AND ($2 = '' OR provider_code = $2)
		  AND ($3 = '' OR import_status = $3)
		ORDER BY updated_at DESC
		LIMIT $4
	`, propertyID, strings.ToLower(strings.TrimSpace(providerCode)), strings.ToUpper(strings.TrimSpace(statusFilter)), limit)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	out := make([]domain.ChannelExternalReservation, 0)
	for rows.Next() {
		var r domain.ChannelExternalReservation
		var st string
		if err := rows.Scan(
			&r.ID, &r.PropertyID, &r.ChannelConnectionID, &r.ListingMappingID, &r.ProviderCode,
			&r.ExternalReservationID, &r.ExternalRevision, &r.ExternalUpdatedAt, &r.ReservationID,
			&st, &r.ConflictReason, &r.PayloadDigest, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, MapErr(err)
		}
		r.ImportStatus = domain.ChannelImportStatus(st)
		out = append(out, r)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) UpsertExternalReservation(ctx context.Context, db DBTX, in domain.ChannelExternalReservation) (domain.ChannelExternalReservation, error) {
	if db == nil {
		db = s.pool
	}
	if in.ID == uuid.Nil {
		id, err := NewUUID()
		if err != nil {
			return domain.ChannelExternalReservation{}, err
		}
		in.ID = id
	}
	var out domain.ChannelExternalReservation
	var st string
	err := db.QueryRow(ctx, `
		INSERT INTO channel_external_reservations (
			id, property_id, channel_connection_id, listing_mapping_id, provider_code,
			external_reservation_id, external_revision, external_updated_at, reservation_id,
			import_status, conflict_reason, payload_digest
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (property_id, provider_code, external_reservation_id) DO UPDATE SET
			listing_mapping_id = COALESCE(EXCLUDED.listing_mapping_id, channel_external_reservations.listing_mapping_id),
			external_revision = EXCLUDED.external_revision,
			external_updated_at = COALESCE(EXCLUDED.external_updated_at, channel_external_reservations.external_updated_at),
			reservation_id = COALESCE(EXCLUDED.reservation_id, channel_external_reservations.reservation_id),
			import_status = EXCLUDED.import_status,
			conflict_reason = EXCLUDED.conflict_reason,
			payload_digest = EXCLUDED.payload_digest,
			updated_at = NOW()
		RETURNING id, property_id, channel_connection_id, listing_mapping_id, provider_code,
		          external_reservation_id, external_revision, external_updated_at, reservation_id,
		          import_status, conflict_reason, payload_digest, created_at, updated_at
	`, in.ID, in.PropertyID, in.ChannelConnectionID, in.ListingMappingID, in.ProviderCode,
		in.ExternalReservationID, in.ExternalRevision, in.ExternalUpdatedAt, in.ReservationID,
		string(in.ImportStatus), in.ConflictReason, in.PayloadDigest).Scan(
		&out.ID, &out.PropertyID, &out.ChannelConnectionID, &out.ListingMappingID, &out.ProviderCode,
		&out.ExternalReservationID, &out.ExternalRevision, &out.ExternalUpdatedAt, &out.ReservationID,
		&st, &out.ConflictReason, &out.PayloadDigest, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return domain.ChannelExternalReservation{}, MapErr(err)
	}
	out.ImportStatus = domain.ChannelImportStatus(st)
	return out, nil
}

func (s *Store) RecordChannelWebhookEvent(ctx context.Context, db DBTX, ev domain.ChannelWebhookEvent) (domain.ChannelWebhookEvent, bool, error) {
	if db == nil {
		db = s.pool
	}
	if ev.ID == uuid.Nil {
		id, err := NewUUID()
		if err != nil {
			return domain.ChannelWebhookEvent{}, false, err
		}
		ev.ID = id
	}
	if len(ev.SafeSummary) == 0 {
		ev.SafeSummary = json.RawMessage("{}")
	}
	var inserted domain.ChannelWebhookEvent
	var st string
	err := db.QueryRow(ctx, `
		INSERT INTO channel_webhook_events (
			id, property_id, channel_connection_id, provider_code,
			external_event_id, event_type, signature_verified, status, safe_summary
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (property_id, provider_code, external_event_id) DO NOTHING
		RETURNING id, property_id, channel_connection_id, provider_code,
		          external_event_id, event_type, signature_verified, status,
		          safe_summary, error_summary, received_at, processed_at
	`, ev.ID, ev.PropertyID, ev.ChannelConnectionID, ev.ProviderCode,
		ev.ExternalEventID, ev.EventType, ev.SignatureVerified, string(ev.Status), ev.SafeSummary).Scan(
		&inserted.ID, &inserted.PropertyID, &inserted.ChannelConnectionID, &inserted.ProviderCode,
		&inserted.ExternalEventID, &inserted.EventType, &inserted.SignatureVerified, &st,
		&inserted.SafeSummary, &inserted.ErrorSummary, &inserted.ReceivedAt, &inserted.ProcessedAt,
	)
	if err == nil {
		inserted.Status = domain.ChannelWebhookStatus(st)
		return inserted, true, nil
	}
	if err != pgx.ErrNoRows {
		return domain.ChannelWebhookEvent{}, false, MapErr(err)
	}
	var existing domain.ChannelWebhookEvent
	err = db.QueryRow(ctx, `
		SELECT id, property_id, channel_connection_id, provider_code,
		       external_event_id, event_type, signature_verified, status,
		       safe_summary, error_summary, received_at, processed_at
		FROM channel_webhook_events
		WHERE property_id = $1 AND provider_code = $2 AND external_event_id = $3
	`, ev.PropertyID, ev.ProviderCode, ev.ExternalEventID).Scan(
		&existing.ID, &existing.PropertyID, &existing.ChannelConnectionID, &existing.ProviderCode,
		&existing.ExternalEventID, &existing.EventType, &existing.SignatureVerified, &st,
		&existing.SafeSummary, &existing.ErrorSummary, &existing.ReceivedAt, &existing.ProcessedAt,
	)
	if err != nil {
		return domain.ChannelWebhookEvent{}, false, MapErr(err)
	}
	existing.Status = domain.ChannelWebhookStatus(st)
	return existing, false, nil
}

func (s *Store) UpdateChannelWebhookEventStatus(ctx context.Context, db DBTX, eventID uuid.UUID, status domain.ChannelWebhookStatus, errSummary string) error {
	if db == nil {
		db = s.pool
	}
	var processedAt *time.Time
	if status == domain.ChannelWebhookProcessed || status == domain.ChannelWebhookConflict || status == domain.ChannelWebhookIgnored || status == domain.ChannelWebhookFailed {
		now := time.Now().UTC()
		processedAt = &now
	}
	_, err := db.Exec(ctx, `
		UPDATE channel_webhook_events
		SET status = $2,
		    error_summary = $3,
		    processed_at = COALESCE($4, processed_at)
		WHERE id = $1
	`, eventID, string(status), errSummary, processedAt)
	return MapErr(err)
}

func (s *Store) ConsumeChannelHMACNonce(ctx context.Context, providerCode, nonce string, expiresAt time.Time) (bool, error) {
	_, _ = s.pool.Exec(ctx, `DELETE FROM channel_hmac_nonces WHERE expires_at <= NOW()`)
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO channel_hmac_nonces (provider_code, nonce, expires_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (provider_code, nonce) DO NOTHING
	`, providerCode, nonce, expiresAt.UTC())
	if err != nil {
		return false, MapErr(err)
	}
	return tag.RowsAffected() == 1, nil
}

// FindOrCreateGuestForChannelImport locates or creates an active guest for OTA imports,
// never matching guests marked MERGED, ANONYMIZED, or pending erasure (erasure_requested_at IS NOT NULL).
func (s *Store) FindOrCreateGuestForChannelImport(ctx context.Context, tx pgx.Tx, propertyID uuid.UUID, fullName, email, phone string) (domain.Guest, error) {
	normEmail := domain.NormalizeEmail(email)
	normPhone := domain.NormalizePhone(phone)
	firstName, lastName, cleanName := domain.SplitFullName("", "", fullName)
	phone = strings.TrimSpace(phone)

	if normEmail != "" {
		var g domain.Guest
		var status string
		err := tx.QueryRow(ctx, `
			SELECT id, property_id, first_name, last_name, full_name, email, normalized_email,
			       phone, normalized_phone, preferred_contact_method, preferred_language, status,
			       address_json, preferences, notes, merged_into_guest_id, merged_at, anonymized_at,
			       version, created_at, updated_at
			FROM guests
			WHERE property_id = $1
			  AND status NOT IN ('MERGED', 'ANONYMIZED')
			  AND anonymized_at IS NULL
			  AND erasure_requested_at IS NULL
			  AND (normalized_email = $2 OR LOWER(email) = $2)
			ORDER BY created_at ASC
			LIMIT 1
		`, propertyID, normEmail).Scan(
			&g.ID, &g.PropertyID, &g.FirstName, &g.LastName, &g.FullName, &g.Email, &g.NormalizedEmail,
			&g.Phone, &g.NormalizedPhone, &g.PreferredContactMethod, &g.PreferredLanguage, &status,
			&g.AddressJSON, &g.Preferences, &g.Notes, &g.MergedIntoGuestID, &g.MergedAt, &g.AnonymizedAt,
			&g.Version, &g.CreatedAt, &g.UpdatedAt,
		)
		if err == nil {
			g.Status = domain.GuestStatus(status)
			return g, nil
		}
		if err != pgx.ErrNoRows {
			return domain.Guest{}, MapErr(err)
		}
	}
	gid, err := NewUUID()
	if err != nil {
		return domain.Guest{}, err
	}
	var out domain.Guest
	var status string
	err = tx.QueryRow(ctx, `
		INSERT INTO guests (
			id, property_id, first_name, last_name, full_name, email, normalized_email, phone, normalized_phone
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, property_id, first_name, last_name, full_name, email, normalized_email,
		          phone, normalized_phone, preferred_contact_method, preferred_language, status,
		          address_json, preferences, notes, merged_into_guest_id, merged_at, anonymized_at,
		          version, created_at, updated_at
	`, gid, propertyID, firstName, lastName, cleanName, normEmail, normEmail, phone, normPhone).Scan(
		&out.ID, &out.PropertyID, &out.FirstName, &out.LastName, &out.FullName, &out.Email, &out.NormalizedEmail,
		&out.Phone, &out.NormalizedPhone, &out.PreferredContactMethod, &out.PreferredLanguage, &status,
		&out.AddressJSON, &out.Preferences, &out.Notes, &out.MergedIntoGuestID, &out.MergedAt, &out.AnonymizedAt,
		&out.Version, &out.CreatedAt, &out.UpdatedAt,
	)
	if err == nil {
		out.Status = domain.GuestStatus(status)
	}
	return out, MapErr(err)
}

func (s *Store) ListChannelWebhookEvents(ctx context.Context, db DBTX, propertyID uuid.UUID, providerCode string, limit int) ([]domain.ChannelWebhookEvent, error) {
	if db == nil {
		db = s.pool
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.Query(ctx, `
		SELECT id, property_id, channel_connection_id, provider_code,
		       external_event_id, event_type, signature_verified, status,
		       safe_summary, error_summary, received_at, processed_at
		FROM channel_webhook_events
		WHERE property_id = $1
		  AND ($2 = '' OR provider_code = $2)
		ORDER BY received_at DESC
		LIMIT $3
	`, propertyID, strings.ToLower(strings.TrimSpace(providerCode)), limit)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	out := make([]domain.ChannelWebhookEvent, 0)
	for rows.Next() {
		var ev domain.ChannelWebhookEvent
		var st string
		if err := rows.Scan(
			&ev.ID, &ev.PropertyID, &ev.ChannelConnectionID, &ev.ProviderCode,
			&ev.ExternalEventID, &ev.EventType, &ev.SignatureVerified, &st,
			&ev.SafeSummary, &ev.ErrorSummary, &ev.ReceivedAt, &ev.ProcessedAt,
		); err != nil {
			return nil, MapErr(err)
		}
		ev.Status = domain.ChannelWebhookStatus(st)
		out = append(out, ev)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) ListChannelSyncCheckpoints(ctx context.Context, db DBTX, propertyID uuid.UUID, providerCode string) ([]domain.ChannelSyncCheckpoint, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT id, property_id, channel_connection_id, provider_code, cursor_key,
		       cursor_value, payload_digest, last_synced_at, metadata, updated_at
		FROM channel_sync_checkpoints
		WHERE property_id = $1
		  AND ($2 = '' OR provider_code = $2)
		ORDER BY updated_at DESC
	`, propertyID, strings.ToLower(strings.TrimSpace(providerCode)))
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	out := make([]domain.ChannelSyncCheckpoint, 0)
	for rows.Next() {
		var cp domain.ChannelSyncCheckpoint
		if err := rows.Scan(
			&cp.ID, &cp.PropertyID, &cp.ChannelConnectionID, &cp.ProviderCode, &cp.CursorKey,
			&cp.CursorValue, &cp.PayloadDigest, &cp.LastSyncedAt, &cp.Metadata, &cp.UpdatedAt,
		); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, cp)
	}
	return out, MapErr(rows.Err())
}
