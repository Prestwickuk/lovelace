-- Phase 2.1 Migration: Production Hardening & Launch Readiness
-- Enforces one active payment attempt per booking hold, atomic outbox claiming,
-- confirmation token revocation/rotation metadata, and property-configurable rules.

-- 1. Enforce strictly one active/paid payment attempt per inventory hold.
WITH ranked AS (
    SELECT id,
           ROW_NUMBER() OVER (PARTITION BY hold_id ORDER BY created_at DESC, id DESC) AS rn
    FROM payment_attempts
    WHERE hold_id IS NOT NULL
      AND status IN ('CREATED', 'REQUIRES_ACTION', 'SUCCEEDED', 'RECONCILIATION_REQUIRED')
)
UPDATE payment_attempts pa
SET status = 'CANCELLED',
    failure_reason = COALESCE(NULLIF(pa.failure_reason, ''), 'superseded duplicate active attempt during phase 2.1 migration'),
    updated_at = NOW()
FROM ranked r
WHERE pa.id = r.id
  AND r.rn > 1;

CREATE UNIQUE INDEX IF NOT EXISTS uq_payment_attempts_one_active_per_hold
    ON payment_attempts (hold_id)
    WHERE hold_id IS NOT NULL
      AND status IN ('CREATED', 'REQUIRES_ACTION', 'SUCCEEDED', 'RECONCILIATION_REQUIRED');

-- 2. Email outbox atomic worker leasing + duplicate reservation confirmation prevention.
ALTER TABLE email_outbox
    ADD COLUMN IF NOT EXISTS locked_at TIMESTAMPTZ NULL;

ALTER TABLE email_outbox
    DROP CONSTRAINT IF EXISTS email_outbox_status_check;

ALTER TABLE email_outbox
    ADD CONSTRAINT email_outbox_status_check
    CHECK (status IN ('PENDING', 'PROCESSING', 'SENT', 'FAILED'));

DROP INDEX IF EXISTS idx_email_outbox_dispatch;
CREATE INDEX IF NOT EXISTS idx_email_outbox_dispatch
    ON email_outbox (status, next_attempt_at)
    WHERE status IN ('PENDING', 'PROCESSING', 'FAILED');

CREATE UNIQUE INDEX IF NOT EXISTS uq_email_outbox_reservation_template
    ON email_outbox (reservation_id, template)
    WHERE reservation_id IS NOT NULL;

-- 3. Confirmation token revocation & usage tracking (supports safe token rotation).
ALTER TABLE confirmation_access_tokens
    ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS revoked_at   TIMESTAMPTZ NULL;

-- 4. Property-configurable operational rules (quiet hours, fire policy, pet policy, privacy notice)
-- so site-specific policies are edited by the property owner rather than hardcoded in templates.
ALTER TABLE property_booking_policies
    ADD COLUMN IF NOT EXISTS quiet_hours    TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS fire_policy    TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS pet_policy     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS privacy_notice TEXT NOT NULL DEFAULT '';
