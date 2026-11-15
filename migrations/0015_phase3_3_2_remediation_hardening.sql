-- Phase 3 — Milestone 3.2 Remediation and Final Hardening.
-- Additive, non-destructive migration on top of 0001..0014.

-- 1. Durable anonymisation pending marker on guests so active in-flight dispatches
-- block premature completion while blocking new queueing/dispatches cleanly.
ALTER TABLE public.guests
    ADD COLUMN IF NOT EXISTS erasure_requested_at TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS idx_guests_erasure_requested
    ON public.guests (property_id, erasure_requested_at)
    WHERE erasure_requested_at IS NOT NULL;

-- 2. Explicit durable external dispatch state on email_outbox + delivery event type.
ALTER TABLE public.email_outbox
    ADD COLUMN IF NOT EXISTS dispatched_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS dispatch_worker_id TEXT NOT NULL DEFAULT '';

DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'public.email_outbox'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) ILIKE '%status%'
    LOOP
        EXECUTE format('ALTER TABLE public.email_outbox DROP CONSTRAINT %I', c.conname);
    END LOOP;

    ALTER TABLE public.email_outbox
        ADD CONSTRAINT email_outbox_status_check
        CHECK (status IN (
            'PENDING',
            'PROCESSING',
            'DISPATCHING',
            'DISPATCH_UNKNOWN',
            'CANCEL_REQUESTED',
            'CANCELLED',
            'SENT',
            'FAILED'
        ));

    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'public.communication_delivery_events'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) ILIKE '%event_type%'
    LOOP
        EXECUTE format('ALTER TABLE public.communication_delivery_events DROP CONSTRAINT %I', c.conname);
    END LOOP;

    ALTER TABLE public.communication_delivery_events
        ADD CONSTRAINT communication_delivery_events_event_type_check
        CHECK (event_type IN (
            'QUEUED',
            'ACCEPTED',
            'DELIVERED',
            'BOUNCED',
            'FAILED',
            'RETRY_SCHEDULED',
            'CANCELLED',
            'DISPATCH_UNKNOWN',
            'WEBHOOK_EVENT'
        ));
END $$;

CREATE INDEX IF NOT EXISTS idx_email_outbox_dispatch_inflight
    ON public.email_outbox (status, dispatched_at)
    WHERE status IN ('DISPATCHING', 'DISPATCH_UNKNOWN');

-- 3. Remove arbitrary 8-hop cap from resolve_canonical_guest_id while preserving cycle safety.
CREATE OR REPLACE FUNCTION public.resolve_canonical_guest_id(p_property_id UUID, p_guest_id UUID)
RETURNS UUID AS $$
DECLARE
    v_curr    UUID := p_guest_id;
    v_next    UUID;
    v_visited UUID[] := ARRAY[p_guest_id];
BEGIN
    IF p_guest_id IS NULL THEN
        RETURN NULL;
    END IF;
    LOOP
        SELECT g.merged_into_guest_id INTO v_next
        FROM public.guests g
        WHERE g.id = v_curr AND g.property_id = p_property_id;
        EXIT WHEN NOT FOUND OR v_next IS NULL OR v_next = ANY(v_visited);
        v_visited := array_append(v_visited, v_next);
        v_curr := v_next;
    END LOOP;
    RETURN v_curr;
END;
$$ LANGUAGE plpgsql STABLE SET search_path = pg_catalog, public;

-- Fix search_path on existing trigger functions as defense in depth.
ALTER FUNCTION public.prevent_audit_log_mutation() SET search_path = pg_catalog, public;
ALTER FUNCTION public.touch_updated_at() SET search_path = pg_catalog, public;
ALTER FUNCTION public.enforce_guest_reservation_link() SET search_path = pg_catalog, public;
ALTER FUNCTION public.enforce_guest_consent_append_only() SET search_path = pg_catalog, public;

-- 4. Consent note redaction overlay preserving append-only immutability on guest_consent_records.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'public.guest_consent_records'::regclass
          AND conname = 'guest_consent_records_property_id_uq'
    ) THEN
        ALTER TABLE public.guest_consent_records
            ADD CONSTRAINT guest_consent_records_property_id_uq UNIQUE (property_id, id);
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS public.guest_consent_redactions (
    id                UUID PRIMARY KEY,
    consent_record_id UUID NOT NULL UNIQUE REFERENCES public.guest_consent_records(id) ON DELETE RESTRICT,
    property_id       UUID NOT NULL REFERENCES public.properties(id) ON DELETE RESTRICT,
    redacted_notes    TEXT NOT NULL DEFAULT '[Redacted on anonymisation]',
    reason            TEXT NOT NULL CHECK (char_length(trim(reason)) > 0),
    redacted_by       UUID NULL REFERENCES public.admin_users(id) ON DELETE SET NULL,
    redacted_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT guest_consent_redactions_property_record_fkey
        FOREIGN KEY (property_id, consent_record_id)
        REFERENCES public.guest_consent_records (property_id, id)
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_guest_consent_redactions_property
    ON public.guest_consent_redactions (property_id, redacted_at DESC);

DROP TRIGGER IF EXISTS trg_guest_consent_redactions_append_only ON public.guest_consent_redactions;
CREATE TRIGGER trg_guest_consent_redactions_append_only
    BEFORE UPDATE OR DELETE ON public.guest_consent_redactions
    FOR EACH ROW EXECUTE FUNCTION public.prevent_audit_log_mutation();

DROP TRIGGER IF EXISTS trg_guest_consent_redactions_no_truncate ON public.guest_consent_redactions;
CREATE TRIGGER trg_guest_consent_redactions_no_truncate
    BEFORE TRUNCATE ON public.guest_consent_redactions
    FOR EACH STATEMENT EXECUTE FUNCTION public.prevent_audit_log_mutation();

CREATE OR REPLACE VIEW public.guest_consent_records_effective AS
SELECT
    c.id,
    c.property_id,
    c.guest_id,
    c.consent_type,
    c.status,
    c.method,
    c.source,
    c.policy_version,
    CASE WHEN r.id IS NOT NULL THEN r.redacted_notes ELSE c.notes END AS notes,
    CASE WHEN r.id IS NOT NULL THEN NULL::uuid ELSE c.recorded_by END AS recorded_by,
    c.occurred_at,
    c.created_at,
    (r.id IS NOT NULL) AS is_note_redacted,
    r.redacted_at AS note_redacted_at
FROM public.guest_consent_records c
LEFT JOIN public.guest_consent_redactions r ON r.consent_record_id = c.id;

-- Controlled consent note redaction helper: requires active owner_admin/manager scoped to the property.
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
BEGIN
    IF p_reason IS NULL OR pg_catalog.char_length(pg_catalog.btrim(p_reason)) = 0 THEN
        RAISE EXCEPTION 'consent note redaction reason is required' USING ERRCODE = '22023';
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

    INSERT INTO public.guest_consent_redactions (
        id, consent_record_id, property_id, redacted_notes, reason, redacted_by
    ) VALUES (
        p_redaction_id, p_consent_record_id, v_prop_id, v_clean_notes, pg_catalog.btrim(p_reason), p_redacted_by
    );

    RETURN p_redaction_id;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public;

-- 5. Harden SECURITY DEFINER audit log redaction function: fixed search_path, schema-qualified refs,
-- active privileged admin actor validation, and property-scope enforcement inside PostgreSQL.
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
BEGIN
    IF p_reason IS NULL OR pg_catalog.char_length(pg_catalog.btrim(p_reason)) = 0 THEN
        RAISE EXCEPTION 'redaction reason is required' USING ERRCODE = '22023';
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
        reason, redacted_by
    ) VALUES (
        p_redaction_id, p_audit_log_id, v_prop_id,
        p_before_state, p_after_state, p_metadata,
        pg_catalog.btrim(p_reason), p_redacted_by
    );

    RETURN p_redaction_id;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public;

-- 6. Apply strict runtime privilege lockdown for lovelace_app if the role exists in the cluster.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
        -- Raw audit logs: append-only INSERT + sequence only; reads must go through audit_logs_effective.
        REVOKE ALL ON TABLE public.audit_logs FROM PUBLIC, lovelace_app;
        GRANT INSERT ON TABLE public.audit_logs TO lovelace_app;
        GRANT USAGE, SELECT ON SEQUENCE public.audit_logs_id_seq TO lovelace_app;
        GRANT SELECT ON TABLE public.audit_logs_effective TO lovelace_app;

        -- Redaction overlays: zero direct table access for runtime role; reads via effective views only.
        REVOKE ALL ON TABLE public.audit_log_redactions FROM PUBLIC, lovelace_app;
        REVOKE ALL ON TABLE public.guest_consent_redactions FROM PUBLIC, lovelace_app;

        -- Consent ledger: SELECT + INSERT + guest_id-only UPDATE (merge reassignment trigger guarded).
        REVOKE ALL ON TABLE public.guest_consent_records FROM PUBLIC, lovelace_app;
        GRANT SELECT, INSERT ON TABLE public.guest_consent_records TO lovelace_app;
        GRANT UPDATE (guest_id) ON TABLE public.guest_consent_records TO lovelace_app;
        GRANT SELECT ON TABLE public.guest_consent_records_effective TO lovelace_app;

        -- Controlled SECURITY DEFINER redaction functions.
        REVOKE ALL ON FUNCTION public.redact_audit_log_entry(UUID, BIGINT, JSONB, JSONB, JSONB, TEXT, UUID) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION public.redact_audit_log_entry(UUID, BIGINT, JSONB, JSONB, JSONB, TEXT, UUID) TO lovelace_app;

        REVOKE ALL ON FUNCTION public.redact_guest_consent_note(UUID, UUID, TEXT, TEXT, UUID) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION public.redact_guest_consent_note(UUID, UUID, TEXT, TEXT, UUID) TO lovelace_app;
    END IF;
END $$;
