-- Phase 3 — Milestone 3.2 Final Corrections & Sign-off.
-- Additive migration on top of 0001..0016 (preserves 0001..0016 checksums cleanly).

-- 1. Canonical non-PII text and minimized audit JSONB validators inside PostgreSQL.
CREATE OR REPLACE FUNCTION public.text_contains_pii(p_val TEXT) RETURNS BOOLEAN AS $$
BEGIN
    IF p_val IS NULL OR pg_catalog.btrim(p_val) = '' THEN
        RETURN FALSE;
    END IF;

    -- Email addresses
    IF p_val ~* '[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}' THEN
        RETURN TRUE;
    END IF;

    -- International phone numbers (+44..., +1-555..., +44 (0) 1437 555123, etc.)
    IF p_val ~ '(?:^|[^0-9A-Za-z_])\+[[:space:].\-()]*(?:\d[[:space:].\-()]*){7,14}\d(?:$|[^0-9A-Za-z_])' THEN
        RETURN TRUE;
    END IF;

    -- Parenthesised local area code phone numbers e.g. (01437) 555123 or (555) 123-4567
    IF p_val ~ '(?:^|[^0-9A-Za-z_])\(\d{3,5}\)[[:space:].\-]*\d{3,4}[[:space:].\-]?\d{3,6}(?:$|[^0-9A-Za-z_])' THEN
        RETURN TRUE;
    END IF;

    -- UK domestic mobile/landline numbers (10 or 11 digits with or without delimiters: 07700 900123, 07700900123, 01437 555123)
    IF p_val ~ '(?:^|[^0-9A-Za-z_\-])0[1-35-9](?:[[:space:].\-]?\d){8,9}(?:$|[^0-9A-Za-z_\-])' THEN
        RETURN TRUE;
    END IF;

    RETURN FALSE;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

CREATE OR REPLACE FUNCTION public.validate_non_pii_reason(
    p_field   TEXT,
    p_reason  TEXT,
    p_min_len INT DEFAULT 5,
    p_max_len INT DEFAULT 240
) RETURNS TEXT AS $$
DECLARE
    v_label TEXT := COALESCE(NULLIF(pg_catalog.btrim(p_field), ''), 'reason');
    v_clean TEXT := pg_catalog.btrim(COALESCE(p_reason, ''));
BEGIN
    IF v_clean ~ E'[\\x00-\\x1F\\x7F]' THEN
        RAISE EXCEPTION '% must be single-line without control characters', v_label USING ERRCODE = '22023';
    END IF;

    IF pg_catalog.char_length(v_clean) < p_min_len OR pg_catalog.char_length(v_clean) > p_max_len THEN
        RAISE EXCEPTION '% must be %..% characters', v_label, p_min_len, p_max_len USING ERRCODE = '22023';
    END IF;

    IF public.text_contains_pii(v_clean) THEN
        RAISE EXCEPTION '% must not contain email addresses or phone numbers', v_label USING ERRCODE = '22023';
    END IF;

    RETURN v_clean;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

CREATE OR REPLACE FUNCTION public.validate_safe_audit_redaction_json(
    p_node  JSONB,
    p_depth INT DEFAULT 0
) RETURNS BOOLEAN AS $$
DECLARE
    v_type TEXT;
    v_kv   RECORD;
    v_key  TEXT;
    v_elem JSONB;
    v_str  TEXT;
BEGIN
    IF p_node IS NULL THEN
        RETURN TRUE;
    END IF;

    IF p_depth > 20 THEN
        RAISE EXCEPTION 'audit redaction JSON exceeds maximum nesting depth' USING ERRCODE = '22023';
    END IF;

    v_type := pg_catalog.jsonb_typeof(p_node);
    IF v_type = 'null' OR v_type = 'number' OR v_type = 'boolean' THEN
        RETURN TRUE;
    END IF;

    IF v_type = 'string' THEN
        v_str := p_node #>> '{}';
        IF v_str ~ E'[\\x00-\\x1F\\x7F]' THEN
            RAISE EXCEPTION 'audit redaction JSON string contains control characters' USING ERRCODE = '22023';
        END IF;
        IF public.text_contains_pii(v_str) THEN
            RAISE EXCEPTION 'audit redaction JSON string contains prohibited email or phone PII' USING ERRCODE = '22023';
        END IF;
        RETURN TRUE;
    END IF;

    IF v_type = 'array' THEN
        FOR v_elem IN SELECT value FROM pg_catalog.jsonb_array_elements(p_node) LOOP
            PERFORM public.validate_safe_audit_redaction_json(v_elem, p_depth + 1);
        END LOOP;
        RETURN TRUE;
    END IF;

    IF v_type = 'object' THEN
        FOR v_kv IN SELECT key, value FROM pg_catalog.jsonb_each(p_node) LOOP
            IF v_kv.key ~ E'[\\x00-\\x1F\\x7F]' THEN
                RAISE EXCEPTION 'audit redaction JSON key contains control characters' USING ERRCODE = '22023';
            END IF;
            v_key := pg_catalog.lower(pg_catalog.btrim(v_kv.key));
            IF v_key IN (
                'password', 'password_hash', 'passcode', 'secret', 'token',
                'session_token', 'access_token', 'refresh_token', 'reset_token',
                'api_key', 'csrf_token',
                'actor_email', 'email', 'guest_email', 'recipient_email', 'recipient',
                'sender_email', 'reply_to', 'author_email', 'contact_email',
                'phone', 'guest_phone', 'mobile', 'telephone', 'tel', 'contact_phone',
                'name', 'full_name', 'guest_name', 'first_name', 'last_name', 'surname',
                'given_name', 'family_name', 'display_name', 'recipient_name', 'author_name',
                'address', 'address_json', 'street', 'postcode', 'postal_code', 'zip',
                'notes', 'internal_notes', 'special_requests', 'dietary_notes',
                'accessibility_notes', 'consent_notes', 'message', 'body', 'html_body',
                'text_body', 'rendered_body', 'raw_body', 'subject', 'raw_subject',
                'description', 'free_text', 'emergency_contact',
                'ip_address', 'user_agent', 'date_of_birth', 'dob',
                'passport_number', 'vehicle_registration', 'card_number', 'iban', 'bank_account'
            ) OR public.text_contains_pii(v_kv.key) THEN
                RAISE EXCEPTION 'audit redaction JSON contains disallowed PII/credential/free-text key %', v_kv.key USING ERRCODE = '22023';
            END IF;

            PERFORM public.validate_safe_audit_redaction_json(v_kv.value, p_depth + 1);
        END LOOP;
        RETURN TRUE;
    END IF;

    RAISE EXCEPTION 'unsupported audit redaction JSON node type %', v_type USING ERRCODE = '22023';
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

-- 2. Re-replace redact_guest_consent_note and redact_audit_log_entry preserving exact signatures
-- while delegating to validate_non_pii_reason and validate_safe_audit_redaction_json.
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
    v_clean_reason := public.validate_non_pii_reason('consent note redaction reason', p_reason, 5, 240);

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
    v_clean_reason := public.validate_non_pii_reason('audit redaction reason', p_reason, 5, 240);

    PERFORM public.validate_safe_audit_redaction_json(p_before_state, 0);
    PERFORM public.validate_safe_audit_redaction_json(p_after_state, 0);
    PERFORM public.validate_safe_audit_redaction_json(p_metadata, 0);

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

-- 3. Defense-in-depth table triggers blocking any direct overlay insert containing PII or control characters.
CREATE OR REPLACE FUNCTION public.enforce_guest_consent_redactions_non_pii() RETURNS TRIGGER AS $$
BEGIN
    NEW.reason := public.validate_non_pii_reason('guest_consent_redactions.reason', NEW.reason, 5, 240);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql SET search_path = pg_catalog, public;

DROP TRIGGER IF EXISTS trg_guest_consent_redactions_non_pii ON public.guest_consent_redactions;
CREATE TRIGGER trg_guest_consent_redactions_non_pii
    BEFORE INSERT ON public.guest_consent_redactions
    FOR EACH ROW EXECUTE FUNCTION public.enforce_guest_consent_redactions_non_pii();

CREATE OR REPLACE FUNCTION public.enforce_audit_log_redactions_non_pii() RETURNS TRIGGER AS $$
BEGIN
    NEW.reason := public.validate_non_pii_reason('audit_log_redactions.reason', NEW.reason, 5, 240);
    PERFORM public.validate_safe_audit_redaction_json(NEW.redacted_before_state, 0);
    PERFORM public.validate_safe_audit_redaction_json(NEW.redacted_after_state, 0);
    PERFORM public.validate_safe_audit_redaction_json(NEW.redacted_metadata, 0);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql SET search_path = pg_catalog, public;

DROP TRIGGER IF EXISTS trg_audit_log_redactions_non_pii ON public.audit_log_redactions;
CREATE TRIGGER trg_audit_log_redactions_non_pii
    BEFORE INSERT ON public.audit_log_redactions
    FOR EACH ROW EXECUTE FUNCTION public.enforce_audit_log_redactions_non_pii();

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
        REVOKE ALL ON FUNCTION public.text_contains_pii(TEXT) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION public.text_contains_pii(TEXT) TO lovelace_app;
        REVOKE ALL ON FUNCTION public.validate_non_pii_reason(TEXT, TEXT, INT, INT) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION public.validate_non_pii_reason(TEXT, TEXT, INT, INT) TO lovelace_app;
        REVOKE ALL ON FUNCTION public.validate_safe_audit_redaction_json(JSONB, INT) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION public.validate_safe_audit_redaction_json(JSONB, INT) TO lovelace_app;
        REVOKE ALL ON FUNCTION public.redact_guest_consent_note(UUID, UUID, TEXT, TEXT, UUID) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION public.redact_guest_consent_note(UUID, UUID, TEXT, TEXT, UUID) TO lovelace_app;
        REVOKE ALL ON FUNCTION public.redact_audit_log_entry(UUID, BIGINT, JSONB, JSONB, JSONB, TEXT, UUID) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION public.redact_audit_log_entry(UUID, BIGINT, JSONB, JSONB, JSONB, TEXT, UUID) TO lovelace_app;
    END IF;
END $$;
