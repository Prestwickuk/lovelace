-- Phase 3 — Milestone 3.2 followup: mask historical actor_email, ip_address, and user_agent
-- in the effective audit view whenever an entry has an approved redaction overlay.

CREATE OR REPLACE VIEW audit_logs_effective AS
SELECT
    a.id,
    a.property_id,
    a.actor_user_id,
    CASE WHEN r.id IS NOT NULL THEN 'redacted@redacted.lovelace' ELSE a.actor_email END AS actor_email,
    a.actor_role,
    a.action,
    a.resource_type,
    a.resource_id,
    a.request_id,
    CASE WHEN r.id IS NOT NULL THEN '' ELSE a.ip_address END AS ip_address,
    CASE WHEN r.id IS NOT NULL THEN '' ELSE a.user_agent END AS user_agent,
    CASE WHEN r.id IS NOT NULL THEN r.redacted_before_state ELSE a.before_state END AS before_state,
    CASE WHEN r.id IS NOT NULL THEN r.redacted_after_state  ELSE a.after_state  END AS after_state,
    CASE WHEN r.id IS NOT NULL THEN r.redacted_metadata     ELSE a.metadata     END AS metadata,
    a.created_at,
    (r.id IS NOT NULL) AS is_redacted,
    r.reason           AS redaction_reason,
    r.redacted_by      AS redacted_by,
    r.redacted_at      AS redacted_at
FROM audit_logs a
LEFT JOIN audit_log_redactions r ON r.audit_log_id = a.id;
