package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/domain"
)

// ListEffectiveAuditLogs returns recent audit events overlaid with any authorized historical redactions.
func (s *Store) ListEffectiveAuditLogs(ctx context.Context, propertyID *uuid.UUID, resourceType string, limit int) ([]domain.AuditLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, property_id, actor_user_id, actor_email, actor_role,
		       action, resource_type, resource_id, request_id,
		       ip_address, user_agent, before_state, after_state, metadata, created_at,
		       is_redacted, COALESCE(redaction_reason, ''), redacted_by, redacted_at
		FROM audit_logs_effective
		WHERE ($1::uuid IS NULL OR property_id = $1)
		  AND ($2 = '' OR resource_type = $2)
		ORDER BY created_at DESC, id DESC
		LIMIT $3
	`, propertyID, resourceType, limit)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.AuditLog
	for rows.Next() {
		var l domain.AuditLog
		if err := rows.Scan(
			&l.ID, &l.PropertyID, &l.ActorUserID, &l.ActorEmail, &l.ActorRole,
			&l.Action, &l.ResourceType, &l.ResourceID, &l.RequestID,
			&l.IPAddress, &l.UserAgent, &l.BeforeState, &l.AfterState, &l.Metadata, &l.CreatedAt,
			&l.IsRedacted, &l.RedactionReason, &l.RedactedBy, &l.RedactedAt,
		); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, l)
	}
	return out, MapErr(rows.Err())
}

// GetAuditLogByID fetches a single audit log row (effective view) for authorized inspection/redaction.
func (s *Store) GetAuditLogByID(ctx context.Context, db DBTX, auditLogID int64) (domain.AuditLog, error) {
	if db == nil {
		db = s.pool
	}
	var l domain.AuditLog
	err := db.QueryRow(ctx, `
		SELECT id, property_id, actor_user_id, actor_email, actor_role,
		       action, resource_type, resource_id, request_id,
		       ip_address, user_agent, before_state, after_state, metadata, created_at,
		       is_redacted, COALESCE(redaction_reason, ''), redacted_by, redacted_at
		FROM audit_logs_effective
		WHERE id = $1
	`, auditLogID).Scan(
		&l.ID, &l.PropertyID, &l.ActorUserID, &l.ActorEmail, &l.ActorRole,
		&l.Action, &l.ResourceType, &l.ResourceID, &l.RequestID,
		&l.IPAddress, &l.UserAgent, &l.BeforeState, &l.AfterState, &l.Metadata, &l.CreatedAt,
		&l.IsRedacted, &l.RedactionReason, &l.RedactedBy, &l.RedactedAt,
	)
	if err != nil {
		return domain.AuditLog{}, MapErr(err)
	}
	return l, nil
}

// RedactAuditLogEntry invokes the controlled PostgreSQL SECURITY DEFINER historical redaction function.
func (s *Store) RedactAuditLogEntry(
	ctx context.Context,
	tx pgx.Tx,
	auditLogID int64,
	redactedBefore, redactedAfter, redactedMetadata json.RawMessage,
	reason string,
	actorID uuid.UUID,
) (uuid.UUID, time.Time, error) {
	redactionID, err := NewUUID()
	if err != nil {
		return uuid.Nil, time.Time{}, err
	}
	var outID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT redact_audit_log_entry($1, $2, $3, $4, $5, $6, $7)
	`, redactionID, auditLogID, redactedBefore, redactedAfter, redactedMetadata, reason, actorID).Scan(&outID)
	if err != nil {
		return uuid.Nil, time.Time{}, MapErr(err)
	}
	var redactedAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT redacted_at
		FROM audit_logs_effective
		WHERE id = $1
	`, auditLogID).Scan(&redactedAt)
	if err != nil {
		return uuid.Nil, time.Time{}, MapErr(err)
	}
	return outID, redactedAt, nil
}
