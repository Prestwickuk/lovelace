-- Phase 3 — Milestone 3.2: Guest Privacy Completeness, Communication Safety & Audit Integrity.
-- Additive, non-destructive incremental migration on top of 0001..0012.

-- 1. Controlled, privileged, append-only redaction overlay for historical audit_logs.
-- The audit_logs table retains its immutable BEFORE UPDATE/DELETE/TRUNCATE triggers.
-- Effective audit views and application queries overlay audit_log_redactions when present,
-- and each redaction records an immutable audit_log.redacted entry.
CREATE TABLE IF NOT EXISTS audit_log_redactions (
    id                    UUID PRIMARY KEY,
    audit_log_id          BIGINT NOT NULL UNIQUE REFERENCES audit_logs(id) ON DELETE RESTRICT,
    property_id           UUID NULL REFERENCES properties(id) ON DELETE RESTRICT,
    redacted_before_state JSONB NULL,
    redacted_after_state  JSONB NULL,
    redacted_metadata     JSONB NULL,
    reason                TEXT NOT NULL CHECK (char_length(trim(reason)) > 0),
    redacted_by           UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    redacted_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_log_redactions_property
    ON audit_log_redactions (property_id, redacted_at DESC);

DROP TRIGGER IF EXISTS trg_audit_log_redactions_append_only ON audit_log_redactions;
CREATE TRIGGER trg_audit_log_redactions_append_only
    BEFORE UPDATE OR DELETE ON audit_log_redactions
    FOR EACH ROW EXECUTE FUNCTION prevent_audit_log_mutation();

DROP TRIGGER IF EXISTS trg_audit_log_redactions_no_truncate ON audit_log_redactions;
CREATE TRIGGER trg_audit_log_redactions_no_truncate
    BEFORE TRUNCATE ON audit_log_redactions
    FOR EACH STATEMENT EXECUTE FUNCTION prevent_audit_log_mutation();

CREATE OR REPLACE VIEW audit_logs_effective AS
SELECT
    a.id,
    a.property_id,
    a.actor_user_id,
    a.actor_email,
    a.actor_role,
    a.action,
    a.resource_type,
    a.resource_id,
    a.request_id,
    a.ip_address,
    a.user_agent,
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

CREATE OR REPLACE FUNCTION redact_audit_log_entry(
    p_redaction_id UUID,
    p_audit_log_id BIGINT,
    p_before_state JSONB,
    p_after_state  JSONB,
    p_metadata     JSONB,
    p_reason       TEXT,
    p_redacted_by  UUID
) RETURNS UUID AS $$
DECLARE
    v_prop_id UUID;
    v_existing UUID;
BEGIN
    IF p_reason IS NULL OR char_length(trim(p_reason)) = 0 THEN
        RAISE EXCEPTION 'redaction reason is required' USING ERRCODE = '22023';
    END IF;

    SELECT property_id INTO v_prop_id
    FROM audit_logs
    WHERE id = p_audit_log_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'audit_log entry % not found', p_audit_log_id USING ERRCODE = 'P0002';
    END IF;

    SELECT id INTO v_existing
    FROM audit_log_redactions
    WHERE audit_log_id = p_audit_log_id;

    IF FOUND THEN
        RETURN v_existing;
    END IF;

    INSERT INTO audit_log_redactions (
        id, audit_log_id, property_id,
        redacted_before_state, redacted_after_state, redacted_metadata,
        reason, redacted_by
    ) VALUES (
        p_redaction_id, p_audit_log_id, v_prop_id,
        p_before_state, p_after_state, p_metadata,
        trim(p_reason), p_redacted_by
    );

    RETURN p_redaction_id;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- 2. Nullable guest_id on booking_holds with property-scoped composite FK.
-- Abandoned anonymous holds retain guest_id = NULL until confirmed checkout associates a guest.
ALTER TABLE booking_holds
    ADD COLUMN IF NOT EXISTS guest_id UUID NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'booking_holds_property_guest_fkey'
    ) THEN
        ALTER TABLE booking_holds
            ADD CONSTRAINT booking_holds_property_guest_fkey
            FOREIGN KEY (property_id, guest_id)
            REFERENCES guests (property_id, id)
            ON DELETE SET NULL (guest_id);
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_booking_holds_property_guest
    ON booking_holds (property_id, guest_id)
    WHERE guest_id IS NOT NULL;

-- Backfill only already-confirmed holds directly via their confirmed_reservation_id.
UPDATE booking_holds h
SET guest_id = r.guest_id
FROM reservations r
WHERE h.confirmed_reservation_id = r.id
  AND h.property_id = r.property_id
  AND h.guest_id IS NULL;

-- 3. Email outbox cancellation & lease coordination columns for safe concurrent anonymisation.
ALTER TABLE email_outbox
    ADD COLUMN IF NOT EXISTS worker_token UUID NULL,
    ADD COLUMN IF NOT EXISTS cancel_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cancelled_at TIMESTAMPTZ NULL;

DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'email_outbox'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) ILIKE '%status%'
    LOOP
        EXECUTE format('ALTER TABLE email_outbox DROP CONSTRAINT %I', c.conname);
    END LOOP;

    ALTER TABLE email_outbox
        ADD CONSTRAINT email_outbox_status_check
        CHECK (status IN ('PENDING', 'PROCESSING', 'CANCEL_REQUESTED', 'CANCELLED', 'SENT', 'FAILED'));
END $$;

CREATE INDEX IF NOT EXISTS idx_email_outbox_guest_status
    ON email_outbox (property_id, guest_id, status)
    WHERE guest_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_email_outbox_reservation_status
    ON email_outbox (property_id, reservation_id, status)
    WHERE reservation_id IS NOT NULL;

-- Allow CANCELLED status cleanly on guest_communications & delivery_events when cancelled pre-send.
DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'guest_communications'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) ILIKE '%delivery_status%'
    LOOP
        EXECUTE format('ALTER TABLE guest_communications DROP CONSTRAINT %I', c.conname);
    END LOOP;

    ALTER TABLE guest_communications
        ADD CONSTRAINT guest_communications_delivery_status_check
        CHECK (delivery_status IN (
            'DRAFT', 'QUEUED', 'SENDING', 'ACCEPTED', 'DELIVERED', 'BOUNCED', 'FAILED', 'CANCELLED', 'LOGGED'
        ));

    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'communication_delivery_events'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) ILIKE '%event_type%'
    LOOP
        EXECUTE format('ALTER TABLE communication_delivery_events DROP CONSTRAINT %I', c.conname);
    END LOOP;

    ALTER TABLE communication_delivery_events
        ADD CONSTRAINT communication_delivery_events_event_type_check
        CHECK (event_type IN (
            'QUEUED', 'ACCEPTED', 'DELIVERED', 'BOUNCED', 'FAILED', 'RETRY_SCHEDULED', 'CANCELLED', 'WEBHOOK_EVENT'
        ));
END $$;

-- 4. Duplicate dismissals structured non-PII reason category so free-text reasons can be scrubbed cleanly.
ALTER TABLE guest_duplicate_dismissals
    ADD COLUMN IF NOT EXISTS reason_category TEXT NOT NULL DEFAULT 'NOT_DUPLICATE';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chk_guest_duplicate_dismissals_reason_category'
    ) THEN
        ALTER TABLE guest_duplicate_dismissals
            ADD CONSTRAINT chk_guest_duplicate_dismissals_reason_category
            CHECK (reason_category IN (
                'NOT_DUPLICATE', 'DISTINCT_PEOPLE', 'SHARED_CONTACT', 'HOUSEHOLD', 'CORPORATE', 'ANONYMISED', 'OTHER'
            ));
    END IF;
END $$;

-- 5. Strengthen append-only trigger on guest_consent_records:
-- guest_id reassignment is only permitted when backed by an explicit, active merge
-- recorded in guest_merge_events within the same property where OLD.guest_id is MERGED
-- into NEW.guest_id and NEW.guest_id is a valid non-merged, non-anonymized target.
CREATE OR REPLACE FUNCTION enforce_guest_consent_append_only()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'guest_consent_records is append-only and cannot be deleted'
            USING ERRCODE = 'P0001';
    END IF;

    IF TG_OP = 'UPDATE' THEN
        IF NEW.id = OLD.id
           AND NEW.property_id = OLD.property_id
           AND NEW.consent_type = OLD.consent_type
           AND NEW.status = OLD.status
           AND NEW.method = OLD.method
           AND NEW.source = OLD.source
           AND NEW.policy_version = OLD.policy_version
           AND NEW.notes = OLD.notes
           AND NEW.recorded_by IS NOT DISTINCT FROM OLD.recorded_by
           AND NEW.occurred_at = OLD.occurred_at
           AND NEW.created_at = OLD.created_at
           AND NEW.guest_id IS DISTINCT FROM OLD.guest_id
           AND EXISTS (
               SELECT 1
               FROM guest_merge_events m
               JOIN guests src ON src.property_id = m.property_id AND src.id = m.source_guest_id
               JOIN guests tgt ON tgt.property_id = m.property_id AND tgt.id = m.target_guest_id
               WHERE m.property_id = NEW.property_id
                 AND m.source_guest_id = OLD.guest_id
                 AND m.target_guest_id = NEW.guest_id
                 AND src.status = 'MERGED'
                 AND src.merged_into_guest_id = NEW.guest_id
                 AND tgt.status NOT IN ('MERGED', 'ANONYMIZED')
           ) THEN
            RETURN NEW;
        END IF;
        RAISE EXCEPTION 'guest_consent_records is append-only; guest_id can only be reassigned via verified merge'
            USING ERRCODE = 'P0001';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
