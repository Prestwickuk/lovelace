-- Phase 3 — Milestone 3.2 Final Hardening.
-- Additive migration on top of 0001..0015 (preserves 0001..0015 checksums cleanly).

-- 1. Worker liveness heartbeat lease, monotonic fencing epoch, explicit dispatch outcome,
-- and structured operator reconciliation metadata on email_outbox.
ALTER TABLE public.email_outbox
    ADD COLUMN IF NOT EXISTS dispatch_lease_expires_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS dispatch_heartbeat_at     TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS dispatch_fence_epoch      BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS dispatch_fenced_at        TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS dispatch_outcome          TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS reconciled_by             UUID NULL REFERENCES public.admin_users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS reconciled_at             TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS reconciled_decision       TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS reconciled_evidence_type  TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS reconciled_evidence_ref   TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS reconciled_reason_code    TEXT NOT NULL DEFAULT '';

DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'public.email_outbox'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) ILIKE '%dispatch_outcome%'
    LOOP
        EXECUTE format('ALTER TABLE public.email_outbox DROP CONSTRAINT %I', c.conname);
    END LOOP;

    ALTER TABLE public.email_outbox
        ADD CONSTRAINT email_outbox_dispatch_outcome_check
        CHECK (dispatch_outcome IN (
            '',
            'PROVIDER_ACCEPTED',
            'PRE_SUBMIT_FAILED',
            'UNCERTAIN_TIMEOUT',
            'WORKER_FENCED',
            'TERMINATED_BEFORE_SEND'
        ));
END $$;

UPDATE public.email_outbox
SET dispatch_lease_expires_at = COALESCE(dispatch_lease_expires_at, dispatched_at + INTERVAL '60 seconds'),
    dispatch_heartbeat_at = COALESCE(dispatch_heartbeat_at, dispatched_at)
WHERE status = 'DISPATCHING'
  AND dispatched_at IS NOT NULL
  AND dispatch_lease_expires_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_email_outbox_dispatch_lease
    ON public.email_outbox (status, dispatch_lease_expires_at)
    WHERE status = 'DISPATCHING';

-- 2. Controlled redaction reason categories and anti-PII-smuggling constraints on overlay tables.
ALTER TABLE public.audit_log_redactions
    ADD COLUMN IF NOT EXISTS reason_category TEXT NOT NULL DEFAULT 'HISTORICAL_PII_MASK';

ALTER TABLE public.guest_consent_redactions
    ADD COLUMN IF NOT EXISTS reason_category TEXT NOT NULL DEFAULT 'GUEST_ERASURE';

DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'public.audit_log_redactions'::regclass
          AND contype = 'c'
          AND conname IN ('audit_log_redactions_reason_category_check', 'audit_log_redactions_reason_bounded_check')
    LOOP
        EXECUTE format('ALTER TABLE public.audit_log_redactions DROP CONSTRAINT %I', c.conname);
    END LOOP;

    ALTER TABLE public.audit_log_redactions
        ADD CONSTRAINT audit_log_redactions_reason_category_check
        CHECK (reason_category IN ('HISTORICAL_PII_MASK', 'ERASURE_ALIGNMENT', 'INADVERTENT_SECRET', 'COMPLIANCE_REVIEW')),
        ADD CONSTRAINT audit_log_redactions_reason_bounded_check
        CHECK (char_length(btrim(reason)) BETWEEN 5 AND 240);

    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'public.guest_consent_redactions'::regclass
          AND contype = 'c'
          AND conname IN (
              'guest_consent_redactions_reason_category_check',
              'guest_consent_redactions_reason_bounded_check',
              'guest_consent_redactions_notes_placeholder_check'
          )
    LOOP
        EXECUTE format('ALTER TABLE public.guest_consent_redactions DROP CONSTRAINT %I', c.conname);
    END LOOP;

    ALTER TABLE public.guest_consent_redactions
        ADD CONSTRAINT guest_consent_redactions_reason_category_check
        CHECK (reason_category IN ('GUEST_ERASURE', 'PRIVACY_MINIMISATION', 'PII_CONTAMINATION', 'LEGAL_ORDER')),
        ADD CONSTRAINT guest_consent_redactions_reason_bounded_check
        CHECK (char_length(btrim(reason)) BETWEEN 5 AND 240),
        ADD CONSTRAINT guest_consent_redactions_notes_placeholder_check
        CHECK (redacted_notes IN ('[Redacted on anonymisation]', '[Redacted on guest anonymisation]', '[Redacted for privacy]', '[redacted]'));
END $$;

-- 3. Tighten redact_guest_consent_note and redact_audit_log_entry to reject free-text PII smuggling
-- and arbitrary replacement text while keeping the existing SECURITY DEFINER signatures intact.
CREATE OR REPLACE FUNCTION public.redact_guest_consent_note(
    p_redaction_id      UUID,
    p_consent_record_id UUID,
    p_redacted_notes    TEXT,
    p_reason            TEXT,
    p_redacted_by       UUID
) RETURNS UUID AS $$
DECLARE
    v_prop_id       UUID;
    v_existing      UUID;
    v_actor_role    TEXT;
    v_actor_prop_id UUID;
    v_clean_notes   TEXT;
    v_clean_reason  TEXT;
BEGIN
    v_clean_reason := pg_catalog.btrim(COALESCE(p_reason, ''));
    IF pg_catalog.char_length(v_clean_reason) < 5 OR pg_catalog.char_length(v_clean_reason) > 240
       OR v_clean_reason ~ E'[\r\n]'
       OR v_clean_reason ~* '[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}' THEN
        RAISE EXCEPTION 'consent note redaction reason must be 5..240 non-PII single-line characters' USING ERRCODE = '22023';
    END IF;

    IF p_redacted_by IS NULL THEN
        RAISE EXCEPTION 'authorized redaction actor is required' USING ERRCODE = '42501';
    END IF;

    SELECT u.role, u.property_id INTO v_actor_role, v_actor_prop_id
    FROM public.admin_users u
    WHERE u.id = p_redacted_by
      AND u.is_active = TRUE
      AND u.must_rotate_password = FALSE;

    IF NOT FOUND OR v_actor_role NOT IN ('owner_admin', 'manager') THEN
        RAISE EXCEPTION 'actor % is not authorized to redact consent notes', p_redacted_by USING ERRCODE = '42501';
    END IF;

    SELECT c.property_id INTO v_prop_id
    FROM public.guest_consent_records c
    WHERE c.id = p_consent_record_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'consent record % not found', p_consent_record_id USING ERRCODE = 'P0002';
    END IF;

    IF v_actor_prop_id IS NOT NULL AND v_actor_prop_id <> v_prop_id THEN
        RAISE EXCEPTION 'actor % cannot redact consent records outside assigned property', p_redacted_by USING ERRCODE = '42501';
    END IF;

    SELECT r.id INTO v_existing
    FROM public.guest_consent_redactions r
    WHERE r.consent_record_id = p_consent_record_id;

    IF FOUND THEN
        RETURN v_existing;
    END IF;

    v_clean_notes := COALESCE(NULLIF(pg_catalog.btrim(p_redacted_notes), ''), '[Redacted on anonymisation]');
    IF v_clean_notes NOT IN ('[Redacted on anonymisation]', '[Redacted on guest anonymisation]', '[Redacted for privacy]', '[redacted]') THEN
        RAISE EXCEPTION 'redacted_notes must use an approved non-PII placeholder token' USING ERRCODE = '22023';
    END IF;

    INSERT INTO public.guest_consent_redactions (
        id, consent_record_id, property_id, redacted_notes, reason, reason_category, redacted_by
    ) VALUES (
        p_redaction_id, p_consent_record_id, v_prop_id, v_clean_notes, v_clean_reason, 'GUEST_ERASURE', p_redacted_by
    );

    RETURN p_redaction_id;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public;

CREATE OR REPLACE FUNCTION public.redact_audit_log_entry(
    p_redaction_id UUID,
    p_audit_log_id BIGINT,
    p_before_state JSONB,
    p_after_state  JSONB,
    p_metadata     JSONB,
    p_reason       TEXT,
    p_redacted_by  UUID
) RETURNS UUID AS $$
DECLARE
    v_prop_id       UUID;
    v_existing      UUID;
    v_actor_role    TEXT;
    v_actor_prop_id UUID;
    v_clean_reason  TEXT;
BEGIN
    v_clean_reason := pg_catalog.btrim(COALESCE(p_reason, ''));
    IF pg_catalog.char_length(v_clean_reason) < 5 OR pg_catalog.char_length(v_clean_reason) > 240
       OR v_clean_reason ~ E'[\r\n]'
       OR v_clean_reason ~* '[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}' THEN
        RAISE EXCEPTION 'audit redaction reason must be 5..240 non-PII single-line characters' USING ERRCODE = '22023';
    END IF;

    IF p_redacted_by IS NULL THEN
        RAISE EXCEPTION 'authorized redaction actor is required' USING ERRCODE = '42501';
    END IF;

    SELECT u.role, u.property_id INTO v_actor_role, v_actor_prop_id
    FROM public.admin_users u
    WHERE u.id = p_redacted_by
      AND u.is_active = TRUE
      AND u.must_rotate_password = FALSE;

    IF NOT FOUND OR v_actor_role NOT IN ('owner_admin', 'manager') THEN
        RAISE EXCEPTION 'actor % is not authorized to redact audit log entries', p_redacted_by USING ERRCODE = '42501';
    END IF;

    SELECT a.property_id INTO v_prop_id
    FROM public.audit_logs a
    WHERE a.id = p_audit_log_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'audit_log entry % not found', p_audit_log_id USING ERRCODE = 'P0002';
    END IF;

    IF v_actor_prop_id IS NOT NULL AND (v_prop_id IS NULL OR v_actor_prop_id <> v_prop_id) THEN
        RAISE EXCEPTION 'actor % cannot redact audit log entry % outside assigned property', p_redacted_by, p_audit_log_id USING ERRCODE = '42501';
    END IF;

    SELECT r.id INTO v_existing
    FROM public.audit_log_redactions r
    WHERE r.audit_log_id = p_audit_log_id;

    IF FOUND THEN
        RETURN v_existing;
    END IF;

    INSERT INTO public.audit_log_redactions (
        id, audit_log_id, property_id,
        redacted_before_state, redacted_after_state, redacted_metadata,
        reason, reason_category, redacted_by
    ) VALUES (
        p_redaction_id, p_audit_log_id, v_prop_id,
        p_before_state, p_after_state, p_metadata,
        v_clean_reason, 'HISTORICAL_PII_MASK', p_redacted_by
    );

    RETURN p_redaction_id;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public;
