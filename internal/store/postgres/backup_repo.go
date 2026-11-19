package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/backup"
	"github.com/tirbach/tir-bach/internal/domain"
)

type backupTableExportSpec struct {
	Name  string
	Query string
}

// orderedBackupTableSpecs lists all portable application data tables in topological foreign-key order.
// Secret/ephemeral auth tables (admin_sessions, admin_recovery_codes, login_attempts, guest_portal_sessions,
// guest_portal_challenges, confirmation_access_tokens, channel_hmac_nonces, backup_storage_targets) are excluded.
var orderedBackupTableSpecs = []backupTableExportSpec{
	{Name: "properties", Query: `SELECT to_jsonb(t) FROM public.properties t ORDER BY created_at ASC, id ASC`},
	{Name: "admin_users", Query: `SELECT (to_jsonb(t) - 'password_hash') || jsonb_build_object('must_rotate_password', true) FROM public.admin_users t ORDER BY created_at ASC, id ASC`},
	{Name: "property_branding_settings", Query: `SELECT to_jsonb(t) FROM public.property_branding_settings t`},
	{Name: "property_booking_policies", Query: `SELECT to_jsonb(t) FROM public.property_booking_policies t`},
	{Name: "property_retention_policies", Query: `SELECT to_jsonb(t) FROM public.property_retention_policies t`},
	{Name: "admin_user_preferences", Query: `SELECT to_jsonb(t) FROM public.admin_user_preferences t`},
	{Name: "admin_user_property_workspaces", Query: `SELECT to_jsonb(t) FROM public.admin_user_property_workspaces t`},
	{Name: "booking_channels", Query: `SELECT to_jsonb(t) FROM public.booking_channels t ORDER BY property_id ASC, sort_order ASC`},
	{Name: "unit_types", Query: `SELECT to_jsonb(t) FROM public.unit_types t ORDER BY property_id ASC, sort_order ASC`},
	{Name: "units", Query: `SELECT to_jsonb(t) FROM public.units t ORDER BY property_id ASC, sort_order ASC`},
	{Name: "extras", Query: `SELECT to_jsonb(t) FROM public.extras t ORDER BY property_id ASC, sort_order ASC`},
	{Name: "rate_plans", Query: `SELECT to_jsonb(t) FROM public.rate_plans t ORDER BY property_id ASC, priority ASC`},
	{Name: "rate_rules", Query: `SELECT to_jsonb(t) FROM public.rate_rules t ORDER BY property_id ASC, priority ASC`},
	{Name: "guests", Query: `SELECT to_jsonb(t) FROM public.guests t ORDER BY (merged_into_guest_id IS NOT NULL) ASC, created_at ASC`},
	{Name: "guest_portal_identities", Query: `SELECT to_jsonb(t) FROM public.guest_portal_identities t`},
	{Name: "booking_holds", Query: `SELECT to_jsonb(t) FROM public.booking_holds t`},
	{Name: "reservations", Query: `SELECT to_jsonb(t) FROM public.reservations t ORDER BY created_at ASC`},
	{Name: "reservation_units", Query: `SELECT to_jsonb(t) FROM public.reservation_units t`},
	{Name: "blockout_groups", Query: `SELECT to_jsonb(t) FROM public.blockout_groups t`},
	{Name: "inventory_claims", Query: `SELECT to_jsonb(t) FROM public.inventory_claims t`},
	{Name: "allocation_consumptions", Query: `SELECT to_jsonb(t) FROM public.allocation_consumptions t`},
	{Name: "availability_release_rules", Query: `SELECT to_jsonb(t) FROM public.availability_release_rules t`},
	{Name: "booking_quotes", Query: `SELECT to_jsonb(t) FROM public.booking_quotes t`},
	{Name: "payment_attempts", Query: `SELECT to_jsonb(t) FROM public.payment_attempts t`},
	{Name: "payments", Query: `SELECT to_jsonb(t) FROM public.payments t`},
	{Name: "refunds", Query: `SELECT to_jsonb(t) FROM public.refunds t`},
	{Name: "payment_exceptions", Query: `SELECT to_jsonb(t) FROM public.payment_exceptions t`},
	{Name: "housekeeping_tasks", Query: `SELECT to_jsonb(t) FROM public.housekeeping_tasks t`},
	{Name: "maintenance_categories", Query: `SELECT to_jsonb(t) FROM public.maintenance_categories t`},
	{Name: "maintenance_locations", Query: `SELECT to_jsonb(t) FROM public.maintenance_locations t`},
	{Name: "maintenance_assets", Query: `SELECT to_jsonb(t) FROM public.maintenance_assets t`},
	{Name: "maintenance_contractors", Query: `SELECT to_jsonb(t) FROM public.maintenance_contractors t`},
	{Name: "maintenance_schedules", Query: `SELECT to_jsonb(t) FROM public.maintenance_schedules t`},
	{Name: "maintenance_requests", Query: `SELECT to_jsonb(t) FROM public.maintenance_requests t`},
	{Name: "maintenance_assignments", Query: `SELECT to_jsonb(t) FROM public.maintenance_assignments t`},
	{Name: "maintenance_work_entries", Query: `SELECT to_jsonb(t) FROM public.maintenance_work_entries t`},
	{Name: "maintenance_inspections", Query: `SELECT to_jsonb(t) FROM public.maintenance_inspections t`},
	{Name: "maintenance_status_history", Query: `SELECT to_jsonb(t) FROM public.maintenance_status_history t`},
	{Name: "maintenance_attachments", Query: `SELECT to_jsonb(t) FROM public.maintenance_attachments t`},
	{Name: "maintenance_restriction_links", Query: `SELECT to_jsonb(t) FROM public.maintenance_restriction_links t`},
	{Name: "communication_templates", Query: `SELECT to_jsonb(t) FROM public.communication_templates t`},
	{Name: "communication_template_versions", Query: `SELECT to_jsonb(t) FROM public.communication_template_versions t`},
	{Name: "communication_automation_rules", Query: `SELECT to_jsonb(t) FROM public.communication_automation_rules t`},
	{Name: "guest_preferences", Query: `SELECT to_jsonb(t) FROM public.guest_preferences t`},
	{Name: "guest_notes", Query: `SELECT to_jsonb(t) FROM public.guest_notes t`},
	{Name: "guest_merge_events", Query: `SELECT to_jsonb(t) FROM public.guest_merge_events t`},
	{Name: "guest_duplicate_dismissals", Query: `SELECT to_jsonb(t) FROM public.guest_duplicate_dismissals t`},
	{Name: "guest_enquiries", Query: `SELECT to_jsonb(t) FROM public.guest_enquiries t`},
	{Name: "guest_enquiry_messages", Query: `SELECT to_jsonb(t) FROM public.guest_enquiry_messages t`},
	{Name: "email_outbox", Query: `SELECT to_jsonb(t) FROM public.email_outbox t`},
	{Name: "guest_communications", Query: `SELECT to_jsonb(t) FROM public.guest_communications t`},
	{Name: "communication_delivery_events", Query: `SELECT to_jsonb(t) FROM public.communication_delivery_events t`},
	{Name: "communication_jobs", Query: `SELECT to_jsonb(t) FROM public.communication_jobs t`},
	{Name: "guest_consent_records", Query: `SELECT (to_jsonb(t) - 'is_note_redacted') - 'note_redacted_at' FROM public.guest_consent_records_effective t`},
	{Name: "channel_connections", Query: `SELECT (to_jsonb(t) - 'encrypted_credentials') || jsonb_build_object('credential_hint', '', 'status', 'DISCONNECTED') FROM public.channel_connections t`},
	{Name: "channel_listing_mappings", Query: `SELECT to_jsonb(t) FROM public.channel_listing_mappings t`},
	{Name: "channel_sync_checkpoints", Query: `SELECT to_jsonb(t) FROM public.channel_sync_checkpoints t`},
	{Name: "channel_external_reservations", Query: `SELECT to_jsonb(t) FROM public.channel_external_reservations t`},
	{Name: "audit_logs_effective", Query: `SELECT to_jsonb(t) FROM public.audit_logs_effective t ORDER BY id ASC`},
}

// ExportBackupSnapshot captures a point-in-time REPEATABLE READ READ ONLY snapshot of all portable tables.
func (s *Store) ExportBackupSnapshot(ctx context.Context) ([]backup.MigrationRecord, []backup.TableDump, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, nil, MapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	migRows, err := tx.Query(ctx, `SELECT filename, checksum FROM public.schema_migrations ORDER BY filename ASC`)
	if err != nil {
		return nil, nil, MapErr(err)
	}
	var migrations []backup.MigrationRecord
	for migRows.Next() {
		var m backup.MigrationRecord
		if err := migRows.Scan(&m.Filename, &m.Checksum); err != nil {
			migRows.Close()
			return nil, nil, MapErr(err)
		}
		migrations = append(migrations, m)
	}
	migRows.Close()
	if err := migRows.Err(); err != nil {
		return nil, nil, MapErr(err)
	}

	dumps := make([]backup.TableDump, 0, len(orderedBackupTableSpecs))
	for _, spec := range orderedBackupTableSpecs {
		rows, err := tx.Query(ctx, spec.Query)
		if err != nil {
			return nil, nil, fmt.Errorf("export table %s: %w", spec.Name, MapErr(err))
		}
		var tableRows []json.RawMessage
		for rows.Next() {
			var raw json.RawMessage
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, nil, fmt.Errorf("scan table %s: %w", spec.Name, MapErr(err))
			}
			cp := make([]byte, len(raw))
			copy(cp, raw)
			tableRows = append(tableRows, json.RawMessage(cp))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, nil, fmt.Errorf("iterate table %s: %w", spec.Name, MapErr(err))
		}
		dumps = append(dumps, backup.TableDump{
			Table: spec.Name,
			Rows:  tableRows,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, MapErr(err)
	}
	return migrations, dumps, nil
}

// IsEnvironmentEmptyForRestore checks whether properties, reservations, and guests all have 0 rows.
func (s *Store) IsEnvironmentEmptyForRestore(ctx context.Context) (bool, error) {
	var propCount, resCount, guestCount int64
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM public.properties),
			(SELECT COUNT(*) FROM public.reservations),
			(SELECT COUNT(*) FROM public.guests)
	`).Scan(&propCount, &resCount, &guestCount)
	if err != nil {
		return false, MapErr(err)
	}
	return propCount == 0 && resCount == 0 && guestCount == 0, nil
}

var allowedRestoreTables = func() map[string]bool {
	m := make(map[string]bool, len(orderedBackupTableSpecs))
	for _, s := range orderedBackupTableSpecs {
		if s.Name == "audit_logs_effective" {
			continue // Read-only archival view; live restore event is recorded separately in audit_logs.
		}
		m[s.Name] = true
	}
	return m
}()

// RestoreBackupSnapshotIntoEmptyDB atomically restores a verified backup archive into an empty database
// while preserving the active restoring owner_admin's session & password hash.
func (s *Store) RestoreBackupSnapshotIntoEmptyDB(
	ctx context.Context,
	tx pgx.Tx,
	currentAdminID uuid.UUID,
	lockedPlaceholderPasswordHash string,
	tables map[string][]json.RawMessage,
) (int64, error) {
	// Serialize concurrent restore calls under non-privileged lovelace_app role.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(748291045)`); err != nil {
		return 0, MapErr(err)
	}

	var propCount, resCount, guestCount int64
	if err := tx.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM public.properties),
			(SELECT COUNT(*) FROM public.reservations),
			(SELECT COUNT(*) FROM public.guests)
	`).Scan(&propCount, &resCount, &guestCount); err != nil {
		return 0, MapErr(err)
	}
	if propCount > 0 || resCount > 0 || guestCount > 0 {
		return 0, fmt.Errorf("%w: restore is only allowed into an empty environment (found %d property(ies), %d reservation(s), %d guest(s))", domain.ErrConflict, propCount, resCount, guestCount)
	}

	var currentEmail string
	if err := tx.QueryRow(ctx, `SELECT lower(email) FROM public.admin_users WHERE id = $1`, currentAdminID).Scan(&currentEmail); err != nil {
		return 0, MapErr(err)
	}

	// Build UUID remap for the restoring admin if the backup had the same owner email under a different UUID.
	userIDRemap := make(map[string]string)
	for _, raw := range tables["admin_users"] {
		var row map[string]any
		if err := json.Unmarshal(raw, &row); err != nil {
			return 0, err
		}
		oldID, _ := row["id"].(string)
		email, _ := row["email"].(string)
		if oldID == "" {
			continue
		}
		if strings.EqualFold(oldID, currentAdminID.String()) || strings.EqualFold(strings.TrimSpace(email), currentEmail) {
			userIDRemap[oldID] = currentAdminID.String()
		}
	}

	rewriteJSONUserRefs := func(raw json.RawMessage) json.RawMessage {
		out := raw
		for oldID, newID := range userIDRemap {
			if strings.EqualFold(oldID, newID) {
				continue
			}
			out = bytes.ReplaceAll(out, []byte(oldID), []byte(newID))
		}
		return out
	}

	tableColumnsCache := make(map[string]string)
	getTableColumnsCSV := func(tableName string) (string, error) {
		if !allowedRestoreTables[tableName] {
			return "", fmt.Errorf("disallowed restore table %q", tableName)
		}
		if cached, ok := tableColumnsCache[tableName]; ok {
			return cached, nil
		}
		rows, err := tx.Query(ctx, `
			SELECT quote_ident(a.attname)
			FROM pg_attribute a
			WHERE a.attrelid = ('public.' || $1)::regclass
			  AND a.attnum > 0
			  AND NOT a.attisdropped
			ORDER BY a.attnum ASC
		`, tableName)
		if err != nil {
			return "", MapErr(err)
		}
		var cols []string
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				rows.Close()
				return "", MapErr(err)
			}
			cols = append(cols, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return "", MapErr(err)
		}
		csv := strings.Join(cols, ", ")
		tableColumnsCache[tableName] = csv
		return csv, nil
	}

	insertJSONRow := func(tableName string, doc json.RawMessage) (int64, error) {
		colsCSV, err := getTableColumnsCSV(tableName)
		if err != nil {
			return 0, err
		}
		q := fmt.Sprintf(
			`INSERT INTO public.%s (%s) SELECT %s FROM jsonb_populate_record(NULL::public.%s, $1::jsonb) ON CONFLICT DO NOTHING`,
			tableName, colsCSV, colsCSV, tableName,
		)
		tag, err := tx.Exec(ctx, q, string(doc))
		if err != nil {
			return 0, fmt.Errorf("restore table %s: %w", tableName, MapErr(err))
		}
		return tag.RowsAffected(), nil
	}

	type deferredFK struct {
		ID       string
		TargetID string
	}
	var deferredHoldReservations []deferredFK
	var deferredOutboxComms []deferredFK
	var restoredRows int64

	for _, spec := range orderedBackupTableSpecs {
		if !allowedRestoreTables[spec.Name] {
			continue
		}
		rows := tables[spec.Name]
		if len(rows) == 0 {
			continue
		}

		for _, origRow := range rows {
			doc := rewriteJSONUserRefs(origRow)

			switch spec.Name {
			case "admin_users":
				var m map[string]any
				if err := json.Unmarshal(doc, &m); err != nil {
					return 0, err
				}
				idStr, _ := m["id"].(string)
				emailStr, _ := m["email"].(string)
				if strings.EqualFold(idStr, currentAdminID.String()) || strings.EqualFold(strings.TrimSpace(emailStr), currentEmail) {
					// Preserve the active restoring owner_admin's credentials and session.
					continue
				}
				m["password_hash"] = lockedPlaceholderPasswordHash
				m["must_rotate_password"] = true
				encoded, err := json.Marshal(m)
				if err != nil {
					return 0, err
				}
				doc = encoded

			case "booking_holds":
				var m map[string]any
				if err := json.Unmarshal(doc, &m); err != nil {
					return 0, err
				}
				if crID, ok := m["confirmed_reservation_id"].(string); ok && strings.TrimSpace(crID) != "" {
					if holdID, ok2 := m["id"].(string); ok2 && holdID != "" {
						deferredHoldReservations = append(deferredHoldReservations, deferredFK{ID: holdID, TargetID: crID})
					}
					m["confirmed_reservation_id"] = nil
					encoded, _ := json.Marshal(m)
					doc = encoded
				}

			case "email_outbox":
				var m map[string]any
				if err := json.Unmarshal(doc, &m); err != nil {
					return 0, err
				}
				if commID, ok := m["communication_id"].(string); ok && strings.TrimSpace(commID) != "" {
					if outboxID, ok2 := m["id"].(string); ok2 && outboxID != "" {
						deferredOutboxComms = append(deferredOutboxComms, deferredFK{ID: outboxID, TargetID: commID})
					}
					m["communication_id"] = nil
				}
				// Never auto-dispatch stale pending/processing emails from a historical backup.
				if st, _ := m["status"].(string); st == "PENDING" || st == "PROCESSING" || st == "DISPATCHING" {
					m["status"] = "CANCELLED"
				}
				encoded, _ := json.Marshal(m)
				doc = encoded

			case "channel_connections":
				var m map[string]any
				if err := json.Unmarshal(doc, &m); err != nil {
					return 0, err
				}
				m["encrypted_credentials"] = `\x`
				m["credential_hint"] = ""
				m["status"] = "DISCONNECTED"
				encoded, _ := json.Marshal(m)
				doc = encoded
			}

			n, err := insertJSONRow(spec.Name, doc)
			if err != nil {
				return 0, err
			}
			restoredRows += n
		}

		// Re-link mutual foreign keys immediately once the target table has been populated.
		if spec.Name == "reservations" && len(deferredHoldReservations) > 0 {
			for _, d := range deferredHoldReservations {
				if _, err := tx.Exec(ctx, `UPDATE public.booking_holds SET confirmed_reservation_id = $2::uuid WHERE id = $1::uuid`, d.ID, d.TargetID); err != nil {
					return 0, fmt.Errorf("relink booking_holds.confirmed_reservation_id: %w", MapErr(err))
				}
			}
		}
		if spec.Name == "guest_communications" && len(deferredOutboxComms) > 0 {
			for _, d := range deferredOutboxComms {
				if _, err := tx.Exec(ctx, `UPDATE public.email_outbox SET communication_id = $2::uuid WHERE id = $1::uuid`, d.ID, d.TargetID); err != nil {
					return 0, fmt.Errorf("relink email_outbox.communication_id: %w", MapErr(err))
				}
			}
		}
	}

	return restoredRows, nil
}

// --- Backup Targets & Runs Persistence ---

const backupTargetSelectCols = `
	id, name, provider, bucket_or_container, region, endpoint_url, key_prefix,
	encrypted_credentials, credential_hint, encrypt_archive, schedule_enabled,
	schedule_interval_hours, retention_count, is_active,
	last_verified_at, last_backup_at, last_status, last_error_summary, next_run_at,
	created_by, updated_by, version, created_at, updated_at
`

func scanBackupTarget(r scannable) (domain.BackupStorageTarget, error) {
	var t domain.BackupStorageTarget
	var prov, status string
	err := r.Scan(
		&t.ID, &t.Name, &prov, &t.BucketOrContainer, &t.Region, &t.EndpointURL, &t.KeyPrefix,
		&t.EncryptedCredentials, &t.CredentialHint, &t.EncryptArchive, &t.ScheduleEnabled,
		&t.ScheduleIntervalHours, &t.RetentionCount, &t.IsActive,
		&t.LastVerifiedAt, &t.LastBackupAt, &status, &t.LastErrorSummary, &t.NextRunAt,
		&t.CreatedBy, &t.UpdatedBy, &t.Version, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return domain.BackupStorageTarget{}, MapErr(err)
	}
	t.Provider = domain.BackupProvider(prov)
	t.LastStatus = domain.BackupTargetStatus(status)
	return t, nil
}

func (s *Store) ListBackupTargets(ctx context.Context) ([]domain.BackupStorageTarget, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+backupTargetSelectCols+` FROM public.backup_storage_targets ORDER BY created_at ASC`)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.BackupStorageTarget
	for rows.Next() {
		t, err := scanBackupTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetBackupTarget(ctx context.Context, db DBTX, id uuid.UUID) (domain.BackupStorageTarget, error) {
	if db == nil {
		db = s.pool
	}
	return scanBackupTarget(db.QueryRow(ctx, `SELECT `+backupTargetSelectCols+` FROM public.backup_storage_targets WHERE id = $1`, id))
}

func (s *Store) ListDueScheduledBackupTargets(ctx context.Context, now time.Time, limit int) ([]domain.BackupStorageTarget, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+backupTargetSelectCols+`
		FROM public.backup_storage_targets
		WHERE is_active = TRUE
		  AND schedule_enabled = TRUE
		  AND (next_run_at IS NULL OR next_run_at <= $1)
		ORDER BY next_run_at ASC NULLS FIRST
		LIMIT $2
	`, now.UTC(), limit)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.BackupStorageTarget
	for rows.Next() {
		t, err := scanBackupTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) CreateBackupTarget(ctx context.Context, db DBTX, t domain.BackupStorageTarget) (domain.BackupStorageTarget, error) {
	if db == nil {
		db = s.pool
	}
	return scanBackupTarget(db.QueryRow(ctx, `
		INSERT INTO public.backup_storage_targets (
			id, name, provider, bucket_or_container, region, endpoint_url, key_prefix,
			encrypted_credentials, credential_hint, encrypt_archive, schedule_enabled,
			schedule_interval_hours, retention_count, is_active,
			last_verified_at, last_backup_at, last_status, last_error_summary, next_run_at,
			created_by, updated_by
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11,
			$12, $13, $14,
			$15, $16, $17, $18, $19,
			$20, $21
		)
		RETURNING `+backupTargetSelectCols,
		t.ID, t.Name, string(t.Provider), t.BucketOrContainer, t.Region, t.EndpointURL, t.KeyPrefix,
		t.EncryptedCredentials, t.CredentialHint, t.EncryptArchive, t.ScheduleEnabled,
		t.ScheduleIntervalHours, t.RetentionCount, t.IsActive,
		t.LastVerifiedAt, t.LastBackupAt, string(t.LastStatus), t.LastErrorSummary, t.NextRunAt,
		t.CreatedBy, t.UpdatedBy,
	))
}

func (s *Store) UpdateBackupTarget(ctx context.Context, db DBTX, t domain.BackupStorageTarget) (domain.BackupStorageTarget, error) {
	if db == nil {
		db = s.pool
	}
	out, err := scanBackupTarget(db.QueryRow(ctx, `
		UPDATE public.backup_storage_targets
		SET name = $2,
		    provider = $3,
		    bucket_or_container = $4,
		    region = $5,
		    endpoint_url = $6,
		    key_prefix = $7,
		    encrypted_credentials = $8,
		    credential_hint = $9,
		    encrypt_archive = $10,
		    schedule_enabled = $11,
		    schedule_interval_hours = $12,
		    retention_count = $13,
		    is_active = $14,
		    next_run_at = $15,
		    updated_by = $16,
		    version = version + 1,
		    updated_at = NOW()
		WHERE id = $1 AND version = $17
		RETURNING `+backupTargetSelectCols,
		t.ID, t.Name, string(t.Provider), t.BucketOrContainer, t.Region, t.EndpointURL, t.KeyPrefix,
		t.EncryptedCredentials, t.CredentialHint, t.EncryptArchive, t.ScheduleEnabled,
		t.ScheduleIntervalHours, t.RetentionCount, t.IsActive, t.NextRunAt, t.UpdatedBy, t.Version,
	))
	if err == domain.ErrNotFound {
		if _, gerr := s.GetBackupTarget(ctx, db, t.ID); gerr == nil {
			return domain.BackupStorageTarget{}, domain.ErrConflict
		}
	}
	return out, err
}

func (s *Store) RecordBackupTargetOutcome(
	ctx context.Context,
	db DBTX,
	id uuid.UUID,
	status domain.BackupTargetStatus,
	verifiedAt *time.Time,
	backupAt *time.Time,
	nextRunAt *time.Time,
	errSummary string,
) (domain.BackupStorageTarget, error) {
	if db == nil {
		db = s.pool
	}
	return scanBackupTarget(db.QueryRow(ctx, `
		UPDATE public.backup_storage_targets
		SET last_status = $2,
		    last_verified_at = COALESCE($3, last_verified_at),
		    last_backup_at = COALESCE($4, last_backup_at),
		    next_run_at = COALESCE($5, next_run_at),
		    last_error_summary = $6,
		    version = version + 1,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING `+backupTargetSelectCols,
		id, string(status), verifiedAt, backupAt, nextRunAt, errSummary,
	))
}

func (s *Store) DeleteBackupTarget(ctx context.Context, db DBTX, id uuid.UUID) error {
	if db == nil {
		db = s.pool
	}
	tag, err := db.Exec(ctx, `DELETE FROM public.backup_storage_targets WHERE id = $1`, id)
	if err != nil {
		return MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

const backupRunSelectCols = `
	id, target_id, run_kind, provider, status, encrypted, object_key, archive_sha256,
	size_bytes, table_count, row_count, attachment_count, error_summary, triggered_by,
	started_at, completed_at
`

func scanBackupRun(r scannable) (domain.BackupRun, error) {
	var run domain.BackupRun
	var kind, prov, status string
	err := r.Scan(
		&run.ID, &run.TargetID, &kind, &prov, &status, &run.Encrypted, &run.ObjectKey, &run.ArchiveSHA256,
		&run.SizeBytes, &run.TableCount, &run.RowCount, &run.AttachmentCount, &run.ErrorSummary, &run.TriggeredBy,
		&run.StartedAt, &run.CompletedAt,
	)
	if err != nil {
		return domain.BackupRun{}, MapErr(err)
	}
	run.RunKind = domain.BackupRunKind(kind)
	run.Provider = domain.BackupProvider(prov)
	run.Status = domain.BackupRunStatus(status)
	return run, nil
}

func (s *Store) CreateBackupRun(ctx context.Context, db DBTX, run domain.BackupRun) (domain.BackupRun, error) {
	if db == nil {
		db = s.pool
	}
	return scanBackupRun(db.QueryRow(ctx, `
		INSERT INTO public.backup_runs (
			id, target_id, run_kind, provider, status, encrypted, object_key, archive_sha256,
			size_bytes, table_count, row_count, attachment_count, error_summary, triggered_by,
			started_at, completed_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, $13, $14,
			$15, $16
		)
		RETURNING `+backupRunSelectCols,
		run.ID, run.TargetID, string(run.RunKind), string(run.Provider), string(run.Status), run.Encrypted, run.ObjectKey, run.ArchiveSHA256,
		run.SizeBytes, run.TableCount, run.RowCount, run.AttachmentCount, run.ErrorSummary, run.TriggeredBy,
		run.StartedAt, run.CompletedAt,
	))
}

func (s *Store) ListBackupRuns(ctx context.Context, limit int) ([]domain.BackupRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+backupRunSelectCols+`
		FROM public.backup_runs
		ORDER BY started_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.BackupRun
	for rows.Next() {
		run, err := scanBackupRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, MapErr(rows.Err())
}
