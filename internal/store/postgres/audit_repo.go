package postgres

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/domain"
)

// AppendAuditLog inserts an immutable audit log entry within the caller's DB transaction or pool.
func (s *Store) AppendAuditLog(ctx context.Context, db DBTX, entry domain.AuditLog) error {
	if db == nil {
		db = s.pool
	}
	meta := entry.Metadata
	if len(meta) == 0 {
		meta = json.RawMessage(`{}`)
	}
	_, err := db.Exec(ctx, `
		INSERT INTO audit_logs (
			property_id, actor_user_id, actor_email, actor_role,
			action, resource_type, resource_id, request_id,
			ip_address, user_agent, before_state, after_state, metadata
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8,
			$9, $10, $11, $12, $13
		)
	`,
		entry.PropertyID, entry.ActorUserID, entry.ActorEmail, entry.ActorRole,
		entry.Action, entry.ResourceType, entry.ResourceID, entry.RequestID,
		entry.IPAddress, entry.UserAgent, nullableJSON(entry.BeforeState), nullableJSON(entry.AfterState), meta,
	)
	return MapErr(err)
}

func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// ListAuditLogs returns recent effective audit events (with any RBAC-approved redaction applied)
// filtered optionally by property and resource type.
func (s *Store) ListAuditLogs(ctx context.Context, propertyID *uuid.UUID, resourceType string, limit int) ([]domain.AuditLog, error) {
	return s.ListEffectiveAuditLogs(ctx, propertyID, resourceType, limit)
}
