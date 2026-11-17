package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/domain"
)

func (s *Service) ListAuditLogs(ctx context.Context, actor Actor, propertyID *uuid.UUID, resourceType string, limit int) ([]domain.AuditLog, error) {
	if err := s.requirePerm(actor, auth.PermAuditRead); err != nil {
		return nil, err
	}
	if scopePropID := s.effectiveActorPropertyScope(ctx, nil, actor); scopePropID != nil {
		if propertyID != nil && *propertyID != *scopePropID {
			return nil, domain.ErrForbidden
		}
		pid := *scopePropID
		propertyID = &pid
	}
	return s.store.ListEffectiveAuditLogs(ctx, propertyID, resourceType, limit)
}

// RedactHistoricalAuditLog performs a narrowly scoped, RBAC-gated historical PII redaction
// via the append-only audit_log_redactions overlay while leaving audit_logs immutable and
// appending an immutable audit_log.redacted record.
func (s *Service) RedactHistoricalAuditLog(ctx context.Context, actor Actor, propertyID uuid.UUID, auditLogID int64, reason string) (domain.AuditLog, error) {
	if err := s.requirePerm(actor, auth.PermGuestsAnonymize); err != nil {
		return domain.AuditLog{}, err
	}
	if err := s.requirePerm(actor, auth.PermAuditRead); err != nil {
		return domain.AuditLog{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.AuditLog{}, err
	}
	cleanReason, err := domain.ValidateNonPIIText("reason", reason, 5, 240)
	if err != nil {
		return domain.AuditLog{}, err
	}

	var updated domain.AuditLog
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		target, err := s.store.GetAuditLogByID(ctx, tx, auditLogID)
		if err != nil {
			return err
		}
		if target.PropertyID == nil || *target.PropertyID != propertyID {
			return domain.ErrNotFound
		}
		if target.IsRedacted {
			return fmt.Errorf("%w: audit log entry %d has already been redacted", domain.ErrConflict, auditLogID)
		}

		cleanBefore, err := prepareAuditOverlaySnapshot(target.ResourceType, target.BeforeState)
		if err != nil {
			return err
		}
		cleanAfter, err := prepareAuditOverlaySnapshot(target.ResourceType, target.AfterState)
		if err != nil {
			return err
		}
		var cleanMeta json.RawMessage
		if len(target.Metadata) > 0 && string(target.Metadata) != "null" {
			cleanMeta, err = prepareAuditOverlaySnapshot(target.ResourceType, target.Metadata)
			if err != nil {
				return err
			}
		}

		redID, redactedAt, err := s.store.RedactAuditLogEntry(ctx, tx, auditLogID, cleanBefore, cleanAfter, cleanMeta, cleanReason, actor.UserID)
		if err != nil {
			return err
		}

		if err := s.recordAudit(ctx, tx, actor, &propertyID, "audit_log.redacted", "audit_log", strconv.FormatInt(auditLogID, 10), nil, map[string]any{
			"redaction_id":         redID,
			"target_audit_log_id":  auditLogID,
			"target_resource_type": target.ResourceType,
			"target_resource_id":   target.ResourceID,
			"redacted_at":          redactedAt,
		}); err != nil {
			return err
		}

		updated, err = s.store.GetAuditLogByID(ctx, tx, auditLogID)
		return err
	})
	return updated, err
}

// prepareAuditOverlaySnapshot applies the strict resource-type-aware allowlist to build
// minimized non-PII replacement snapshots that align 1:1 with PostgreSQL validation.
func prepareAuditOverlaySnapshot(resourceType string, raw json.RawMessage) (json.RawMessage, error) {
	return buildRedactionOverlaySnapshot(resourceType, raw)
}

func hasASCIIControlChars(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}
