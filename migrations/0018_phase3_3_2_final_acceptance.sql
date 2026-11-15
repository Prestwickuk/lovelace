-- Phase 3 — Milestone 3.2 Final Acceptance: Strict Resource-Type Allowlist for Audit Replacement Snapshots.
-- Additive migration on top of 0001..0017 (preserves 0001..0017 checksums cleanly).

-- Remove legacy denylist-based JSON validator so only the strict resource-type allowlist exists.
DROP FUNCTION IF EXISTS public.validate_safe_audit_redaction_json(JSONB, INT);

-- 1. Defensive structural and PII/control-character barriers applied before per-key allowlist matching.
-- Error messages intentionally avoid echoing caller keys, values, or PII payloads.
CREATE OR REPLACE FUNCTION public.audit_check_json_limits(
    p_node  JSONB,
    p_depth INT DEFAULT 0
) RETURNS BOOLEAN AS $$
DECLARE
    v_type  TEXT;
    v_kv    RECORD;
    v_elem  JSONB;
    v_count INT := 0;
    v_str   TEXT;
BEGIN
    IF p_node IS NULL THEN
        RETURN TRUE;
    END IF;

    IF p_depth > 4 THEN
        RAISE EXCEPTION 'audit redaction snapshot exceeds maximum nesting depth' USING ERRCODE = '22023';
    END IF;

    v_type := pg_catalog.jsonb_typeof(p_node);
    IF v_type IN ('null', 'boolean', 'number') THEN
        RETURN TRUE;
    END IF;

    IF v_type = 'string' THEN
        v_str := p_node #>> '{}';
        IF pg_catalog.char_length(v_str) > 128 THEN
            RAISE EXCEPTION 'audit redaction snapshot string exceeds maximum length' USING ERRCODE = '22023';
        END IF;
        IF v_str ~ E'[\\x00-\\x1F\\x7F]' THEN
            RAISE EXCEPTION 'audit redaction snapshot string contains control characters' USING ERRCODE = '22023';
        END IF;
        IF v_str LIKE '%@%' OR public.text_contains_pii(v_str) THEN
            RAISE EXCEPTION 'audit redaction snapshot string contains prohibited personal data' USING ERRCODE = '22023';
        END IF;
        RETURN TRUE;
    END IF;

    IF v_type = 'array' THEN
        IF pg_catalog.jsonb_array_length(p_node) > 64 THEN
            RAISE EXCEPTION 'audit redaction snapshot array exceeds maximum element count' USING ERRCODE = '22023';
        END IF;
        FOR v_elem IN SELECT value FROM pg_catalog.jsonb_array_elements(p_node) LOOP
            PERFORM public.audit_check_json_limits(v_elem, p_depth + 1);
        END LOOP;
        RETURN TRUE;
    END IF;

    IF v_type = 'object' THEN
        FOR v_kv IN SELECT key, value FROM pg_catalog.jsonb_each(p_node) LOOP
            v_count := v_count + 1;
            IF v_count > 40 THEN
                RAISE EXCEPTION 'audit redaction snapshot object exceeds maximum key count' USING ERRCODE = '22023';
            END IF;
            IF v_kv.key ~ E'[\\x00-\\x1F\\x7F]'
               OR pg_catalog.char_length(v_kv.key) > 64
               OR NOT (v_kv.key ~ '^[a-z][a-z0-9_]{0,63}$')
               OR v_kv.key LIKE '%@%'
               OR public.text_contains_pii(v_kv.key) THEN
                RAISE EXCEPTION 'audit redaction snapshot contains invalid or unapproved key format' USING ERRCODE = '22023';
            END IF;
            PERFORM public.audit_check_json_limits(v_kv.value, p_depth + 1);
        END LOOP;
        RETURN TRUE;
    END IF;

    RAISE EXCEPTION 'unsupported audit redaction snapshot JSON node' USING ERRCODE = '22023';
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

-- 2. Strict structured-token, number-range, timestamp, and enum scalar validators.
CREATE OR REPLACE FUNCTION public.audit_is_safe_code(
    p_val         TEXT,
    p_allow_empty BOOLEAN DEFAULT FALSE
) RETURNS BOOLEAN AS $$
DECLARE
    v_compact TEXT;
BEGIN
    IF p_val IS NULL THEN
        RETURN FALSE;
    END IF;
    IF p_val = '' THEN
        RETURN p_allow_empty;
    END IF;
    IF pg_catalog.char_length(p_val) > 64 THEN
        RETURN FALSE;
    END IF;
    IF p_val ~ E'[\\x00-\\x1F\\x7F[:space:]]' OR p_val LIKE '%@%' THEN
        RETURN FALSE;
    END IF;
    IF NOT (p_val ~ '^[A-Za-z0-9][A-Za-z0-9._:/+-]{0,63}$') THEN
        RETURN FALSE;
    END IF;
    IF public.text_contains_pii(p_val) THEN
        RETURN FALSE;
    END IF;

    -- Block phone-like digit runs disguised as structured codes/references.
    v_compact := pg_catalog.regexp_replace(p_val, '[._:/+-]', '', 'g');
    IF v_compact ~ '^[0-9]{7,}$' THEN
        RETURN FALSE;
    END IF;
    IF p_val ~ '0[1-35-9][0-9._:/-]{7,}' THEN
        RETURN FALSE;
    END IF;
    IF p_val ~ '[0-9]{9,}' THEN
        RETURN FALSE;
    END IF;

    RETURN TRUE;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

CREATE OR REPLACE FUNCTION public.audit_is_safe_integer(
    p_val JSONB,
    p_min BIGINT,
    p_max BIGINT
) RETURNS BOOLEAN AS $$
DECLARE
    v_txt TEXT;
    v_num BIGINT;
BEGIN
    IF pg_catalog.jsonb_typeof(p_val) <> 'number' THEN
        RETURN FALSE;
    END IF;
    v_txt := p_val #>> '{}';
    IF NOT (v_txt ~ '^-?[0-9]{1,12}$') THEN
        RETURN FALSE;
    END IF;
    v_num := v_txt::bigint;
    RETURN v_num >= p_min AND v_num <= p_max;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

CREATE OR REPLACE FUNCTION public.audit_is_allowed_enum(
    p_val       TEXT,
    p_enum_name TEXT
) RETURNS BOOLEAN AS $$
BEGIN
    IF p_val IS NULL THEN
        RETURN FALSE;
    END IF;

    CASE p_enum_name
        WHEN 'role' THEN
            RETURN p_val IN ('owner_admin', 'manager', 'staff', 'maintenance_staff', 'housekeeping_staff', 'viewer');
        WHEN 'resource_type' THEN
            RETURN p_val IN (
                'admin_session', 'admin_user', 'audit_log', 'availability_block', 'availability_release_rule',
                'blockout_group', 'booking_channel', 'booking_hold', 'channel_connection',
                'communication_automation_rule', 'communication_template', 'confirmation_access_token',
                'email_outbox', 'extra', 'guest', 'guest_communication', 'guest_enquiry', 'guest_note',
                'guest_preference', 'housekeeping_task', 'maintenance_asset', 'maintenance_attachment',
                'maintenance_category', 'maintenance_contractor', 'maintenance_location',
                'maintenance_request', 'maintenance_schedule', 'payment_exception', 'payment_webhook_event',
                'property', 'property_booking_policy', 'property_retention_policy',
                'rate_plan', 'rate_rule', 'reservation', 'unit', 'unit_type'
            );
        WHEN 'block_type' THEN
            RETURN p_val IN ('maintenance', 'owner_hold', 'seasonal_closure', 'weather_closure');
        WHEN 'release_scope' THEN
            RETURN p_val IN ('UNIT', 'UNIT_TYPE', 'PROPERTY');
        WHEN 'block_scope' THEN
            RETURN p_val IN ('UNIT', 'UNITS', 'UNIT_TYPE', 'PROPERTY');
        WHEN 'claim_type' THEN
            RETURN p_val IN (
                'RESERVATION', 'PENDING_RESERVATION', 'EXTERNAL_RESERVATION', 'OWNER_USE', 'MAINTENANCE',
                'SEASONAL_CLOSURE', 'WEATHER_CLOSURE', 'CHANNEL_ALLOCATION', 'CHANNEL_RESTRICTION',
                'OPERATIONAL_BUFFER', 'ADMINISTRATIVE_BLOCK', 'HOLD'
            );
        WHEN 'blockout_status' THEN
            RETURN p_val IN ('ACTIVE', 'PARTIALLY_CONSUMED', 'CONSUMED', 'PARTIALLY_RELEASED', 'RELEASED');
        WHEN 'hold_status' THEN
            RETURN p_val IN ('ACTIVE', 'CONFIRMED', 'EXPIRED', 'CANCELLED', 'FAILED');
        WHEN 'trigger_event' THEN
            RETURN p_val IN (
                'BOOKING_CONFIRMATION', 'PRE_ARRIVAL', 'ARRIVAL_INFO',
                'DEPARTURE_INSTRUCTIONS', 'POST_STAY_THANK_YOU', 'PAYMENT_REMINDER'
            );
        WHEN 'trigger_event_or_empty' THEN
            RETURN p_val = '' OR p_val IN (
                'BOOKING_CONFIRMATION', 'PRE_ARRIVAL', 'ARRIVAL_INFO',
                'DEPARTURE_INSTRUCTIONS', 'POST_STAY_THANK_YOU', 'PAYMENT_REMINDER'
            );
        WHEN 'reservation_status' THEN
            RETURN p_val IN ('INQUIRY', 'PENDING', 'CONFIRMED', 'CHECKED_IN', 'CHECKED_OUT', 'CANCELLED', 'NO_SHOW');
        WHEN 'payment_status' THEN
            RETURN p_val IN ('', 'MANUAL', 'UNPAID', 'PARTIALLY_PAID', 'PAID', 'REFUNDED', 'PARTIALLY_REFUNDED', 'FAILED');
        WHEN 'reconcile_decision' THEN
            RETURN p_val IN ('', 'CONFIRMED_SENT', 'CONFIRMED_FAILED', 'CANCEL');
        WHEN 'reconcile_evidence_type' THEN
            RETURN p_val IN (
                '', 'MTA_ACCEPTED', 'PROVIDER_WEBHOOK_ACCEPTED', 'CONSERVATIVE_ASSUME_SENT',
                'MTA_REJECTED', 'PROVIDER_WEBHOOK_FAILED', 'PROVIDER_NOT_SUBMITTED'
            );
        WHEN 'pricing_model' THEN
            RETURN p_val IN ('per_stay', 'per_night', 'per_person_per_stay', 'per_person_per_night');
        WHEN 'guest_status' THEN
            RETURN p_val IN ('ACTIVE', 'VIP', 'DO_NOT_CONTACT', 'MERGED', 'ANONYMIZED');
        WHEN 'contact_method_or_empty' THEN
            RETURN p_val IN ('', 'EMAIL', 'PHONE', 'SMS', 'POST');
        WHEN 'guest_changed_field' THEN
            RETURN p_val IN ('name', 'email', 'phone', 'preferred_contact_method', 'preferred_language', 'status', 'address', 'preferences', 'notes');
        WHEN 'reservation_changed_field' THEN
            RETURN p_val IN ('guest_name', 'guest_email', 'guest_phone', 'occupant_details', 'stay_dates', 'party_size', 'channel_code', 'status', 'financials', 'internal_notes');
        WHEN 'dismissal_reason_category' THEN
            RETURN p_val IN ('DIFFERENT_PEOPLE', 'FAMILY_SAME_ADDRESS', 'CORPORATE_SHARED', 'TEST_OR_STAFF', 'OTHER');
        WHEN 'interaction_type' THEN
            RETURN p_val IN (
                'EMAIL_OUTBOUND', 'EMAIL_INBOUND', 'PHONE_CALL', 'IN_PERSON',
                'WEBSITE_ENQUIRY', 'BOOKING_NOTIFICATION', 'SYSTEM_NOTIFICATION', 'COMPLAINT'
            );
        WHEN 'comm_direction' THEN
            RETURN p_val IN ('INBOUND', 'OUTBOUND', 'INTERNAL');
        WHEN 'delivery_status' THEN
            RETURN p_val IN (
                '', 'DRAFT', 'QUEUED', 'SENDING', 'DISPATCH_UNKNOWN',
                'ACCEPTED', 'DELIVERED', 'BOUNCED', 'FAILED', 'CANCELLED', 'LOGGED'
            );
        WHEN 'enquiry_category' THEN
            RETURN p_val IN (
                'GENERAL', 'BOOKING', 'AVAILABILITY', 'ACCOMMODATION', 'FACILITIES',
                'ARRIVAL_DEPARTURE', 'SPECIAL_REQUEST', 'GROUP_BOOKING', 'CANCELLATION',
                'COMPLAINT', 'LOST_PROPERTY', 'OTHER'
            );
        WHEN 'enquiry_status' THEN
            RETURN p_val IN ('NEW', 'TRIAGED', 'IN_PROGRESS', 'WAITING_FOR_GUEST', 'WAITING_FOR_PROPERTY', 'RESOLVED', 'CLOSED');
        WHEN 'priority_4' THEN
            RETURN p_val IN ('LOW', 'NORMAL', 'HIGH', 'URGENT');
        WHEN 'enquiry_source' THEN
            RETURN p_val IN ('PHONE', 'IN_PERSON', 'EMAIL', 'WEB', 'ADMIN');
        WHEN 'note_category' THEN
            RETURN p_val IN ('GENERAL', 'BOOKING', 'PREFERENCE', 'CUSTOMER_SERVICE', 'OPERATIONAL');
        WHEN 'pref_category' THEN
            RETURN p_val IN ('ACCOMMODATION', 'PET', 'ARRIVAL', 'EQUIPMENT', 'COMMUNICATION', 'ACCESSIBILITY', 'OTHER');
        WHEN 'task_type' THEN
            RETURN p_val IN ('CHANGEOVER_CLEAN', 'INSPECTION', 'DEEP_CLEAN', 'MID_STAY_REFRESH', 'GENERAL');
        WHEN 'hk_stage' THEN
            RETURN p_val IN ('GUEST_DEPARTING', 'CLEANING_REQUIRED', 'CLEANING_IN_PROGRESS', 'INSPECTION_REQUIRED', 'READY');
        WHEN 'unit_status' THEN
            RETURN p_val IN ('active', 'maintenance', 'inactive', 'retired');
        WHEN 'unit_status_or_empty' THEN
            RETURN p_val IN ('', 'active', 'maintenance', 'inactive', 'retired');
        WHEN 'unit_category' THEN
            RETURN p_val IN ('glamping', 'tent_pitch', 'touring_pitch', 'cabin', 'other');
        WHEN 'asset_status' THEN
            RETURN p_val IN ('ACTIVE', 'RETIRED');
        WHEN 'maint_status' THEN
            RETURN p_val IN ('REPORTED', 'TRIAGED', 'SCHEDULED', 'IN_PROGRESS', 'AWAITING_PARTS', 'ON_HOLD', 'COMPLETED', 'CANCELLED', 'CLOSED');
        WHEN 'maint_work_type' THEN
            RETURN p_val IN ('REACTIVE', 'PREVENTATIVE');
        WHEN 'maint_impact' THEN
            RETURN p_val IN ('NONE', 'PARTIAL', 'UNAVAILABLE');
        WHEN 'maint_inspection_result' THEN
            RETURN p_val IN ('PASSED', 'FAILED');
        WHEN 'maint_inspection_result_or_empty' THEN
            RETURN p_val IN ('', 'PASSED', 'FAILED');
        WHEN 'maint_entry_type' THEN
            RETURN p_val IN (
                'WORK_STARTED', 'DIAGNOSIS', 'PERFORMED', 'PARTS_USED',
                'ADDITIONAL_WORK', 'DELAY_REASON', 'CONTRACTOR_NOTE',
                'COMPLETION_NOTE', 'GENERAL_NOTE', 'REOPENED'
            );
        WHEN 'maint_frequency' THEN
            RETURN p_val IN ('ONCE', 'WEEKLY', 'MONTHLY', 'QUARTERLY', 'SIX_MONTHLY', 'ANNUALLY');
        WHEN 'deposit_mode' THEN
            RETURN p_val IN ('FULL', 'PERCENTAGE', 'FIXED');
        WHEN 'outbox_status' THEN
            RETURN p_val IN ('PENDING', 'PROCESSING', 'DISPATCHING', 'DISPATCH_UNKNOWN', 'CANCEL_REQUESTED', 'CANCELLED', 'SENT', 'FAILED');
        ELSE
            RETURN FALSE;
    END CASE;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

-- 3. Resource-type key allowlist mapping: maps exact (resource_type, key) -> value validation rule.
-- Returns NULL for any unknown resource_type or unapproved key (fail closed).
CREATE OR REPLACE FUNCTION public.audit_resource_key_rule(
    p_resource_type TEXT,
    p_key           TEXT,
    p_allow_nested  BOOLEAN DEFAULT TRUE
) RETURNS TEXT AS $$
BEGIN
    IF p_resource_type IS NULL OR p_key IS NULL THEN
        RETURN NULL;
    END IF;

    CASE p_resource_type
        WHEN 'admin_session' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'session_id' THEN RETURN 'uuid';
                WHEN 'user_id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'expires_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'admin_user' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'user_id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid_or_null';
                WHEN 'role' THEN RETURN 'enum:role';
                WHEN 'is_active' THEN RETURN 'bool';
                WHEN 'must_rotate_password' THEN RETURN 'bool';
                WHEN 'sessions_revoked' THEN RETURN 'bool';
                WHEN 'session_id' THEN RETURN 'uuid';
                WHEN 'new_session_id' THEN RETURN 'uuid';
                WHEN 'last_login_at' THEN RETURN 'timestamp_or_null';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'audit_log' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'redaction_id' THEN RETURN 'uuid';
                WHEN 'target_audit_log_id' THEN RETURN 'int_audit_log_id';
                WHEN 'target_resource_type' THEN RETURN 'enum:resource_type';
                WHEN 'target_resource_id' THEN RETURN 'code';
                WHEN 'redacted_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'availability_block' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'unit_id' THEN RETURN 'uuid';
                WHEN 'unit_code' THEN RETURN 'code_or_empty';
                WHEN 'start_date' THEN RETURN 'date_or_timestamp';
                WHEN 'end_date' THEN RETURN 'date_or_timestamp';
                WHEN 'block_type' THEN RETURN 'enum:block_type';
                WHEN 'created_by' THEN RETURN 'uuid_or_null';
                WHEN 'released_by' THEN RETURN 'uuid_or_null';
                WHEN 'released_at' THEN RETURN 'timestamp_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'availability_release_rule' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'scope_type' THEN RETURN 'enum:release_scope';
                WHEN 'unit_id' THEN RETURN 'uuid_or_null';
                WHEN 'unit_type_id' THEN RETURN 'uuid_or_null';
                WHEN 'unit_code' THEN RETURN 'code_or_empty';
                WHEN 'channel_code' THEN RETURN 'code_or_empty';
                WHEN 'start_date' THEN RETURN 'date_or_timestamp';
                WHEN 'end_date' THEN RETURN 'date_or_timestamp';
                WHEN 'release_at' THEN RETURN 'timestamp';
                WHEN 'released_manually_at' THEN RETURN 'timestamp_or_null';
                WHEN 'released_manually_by' THEN RETURN 'uuid_or_null';
                WHEN 'is_active' THEN RETURN 'bool';
                WHEN 'created_by' THEN RETURN 'uuid_or_null';
                WHEN 'updated_by' THEN RETURN 'uuid_or_null';
                WHEN 'cancelled_at' THEN RETURN 'timestamp_or_null';
                WHEN 'cancelled_by' THEN RETURN 'uuid_or_null';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'blockout_group' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'scope_type' THEN RETURN 'enum:block_scope';
                WHEN 'target_unit_type_id' THEN RETURN 'uuid_or_null';
                WHEN 'category' THEN RETURN 'enum:claim_type';
                WHEN 'channel_scope' THEN RETURN 'code_or_empty';
                WHEN 'start_date' THEN RETURN 'date_or_timestamp';
                WHEN 'end_date' THEN RETURN 'date_or_timestamp';
                WHEN 'apply_to_future_units' THEN RETURN 'bool';
                WHEN 'status' THEN RETURN 'enum:blockout_status';
                WHEN 'active_claim_count' THEN RETURN 'int_count';
                WHEN 'consumed_claim_count' THEN RETURN 'int_count';
                WHEN 'released_claim_count' THEN RETURN 'int_count';
                WHEN 'total_claim_count' THEN RETURN 'int_count';
                WHEN 'created_by' THEN RETURN 'uuid_or_null';
                WHEN 'released_by' THEN RETURN 'uuid_or_null';
                WHEN 'released_at' THEN RETURN 'timestamp_or_null';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                WHEN 'unit_count' THEN IF p_allow_nested THEN RETURN 'int_count'; ELSE RETURN NULL; END IF;
                WHEN 'released_unit_ids' THEN IF p_allow_nested THEN RETURN 'uuid_array'; ELSE RETURN NULL; END IF;
                WHEN 'reinstated_claim_ids' THEN IF p_allow_nested THEN RETURN 'uuid_array'; ELSE RETURN NULL; END IF;
                WHEN 'unit_ids' THEN IF p_allow_nested THEN RETURN 'uuid_array'; ELSE RETURN NULL; END IF;
                WHEN 'group' THEN IF p_allow_nested THEN RETURN 'obj:blockout_group_inner'; ELSE RETURN NULL; END IF;
                ELSE RETURN NULL;
            END CASE;

        WHEN 'booking_channel' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'code' THEN RETURN 'code';
                WHEN 'category' THEN RETURN 'code';
                WHEN 'is_active' THEN RETURN 'bool';
                WHEN 'sort_order' THEN RETURN 'int_sort';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'booking_hold' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'hold_id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'hold_reference' THEN RETURN 'code';
                WHEN 'guest_id' THEN RETURN 'uuid_or_null';
                WHEN 'unit_type_id' THEN RETURN 'uuid';
                WHEN 'rate_plan_id' THEN RETURN 'uuid';
                WHEN 'channel_code' THEN RETURN 'code_or_empty';
                WHEN 'arrival_date' THEN RETURN 'date_or_timestamp';
                WHEN 'departure_date' THEN RETURN 'date_or_timestamp';
                WHEN 'adults' THEN RETURN 'int_party';
                WHEN 'children' THEN RETURN 'int_party';
                WHEN 'infants' THEN RETURN 'int_party';
                WHEN 'terms_version' THEN RETURN 'code_or_empty';
                WHEN 'terms_accepted_at' THEN RETURN 'timestamp_or_null';
                WHEN 'status' THEN RETURN 'enum:hold_status';
                WHEN 'expires_at' THEN RETURN 'timestamp';
                WHEN 'confirmed_reservation_id' THEN RETURN 'uuid_or_null';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'channel_connection' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'provider_code' THEN RETURN 'code';
                WHEN 'auth_method' THEN RETURN 'code';
                WHEN 'status' THEN RETURN 'code';
                WHEN 'has_credentials' THEN RETURN 'bool';
                WHEN 'has_live_adapter' THEN RETURN 'bool';
                WHEN 'credentials_updated_at' THEN RETURN 'timestamp_or_null';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'communication_automation_rule' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'trigger_event' THEN RETURN 'enum:trigger_event';
                WHEN 'offset_hours' THEN RETURN 'int_offset_hours';
                WHEN 'send_hour_local' THEN RETURN 'int_or_null_hour_local';
                WHEN 'template_id' THEN RETURN 'uuid';
                WHEN 'applicable_statuses' THEN RETURN 'enum_array:reservation_status';
                WHEN 'is_active' THEN RETURN 'bool';
                WHEN 'created_by' THEN RETURN 'uuid_or_null';
                WHEN 'updated_by' THEN RETURN 'uuid_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'communication_template' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'code' THEN RETURN 'code';
                WHEN 'category' THEN RETURN 'code';
                WHEN 'locale' THEN RETURN 'locale_or_empty';
                WHEN 'is_active' THEN RETURN 'bool';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'created_by' THEN RETURN 'uuid_or_null';
                WHEN 'updated_by' THEN RETURN 'uuid_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'confirmation_access_token' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'reference' THEN RETURN 'code';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'email_outbox' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'outbox_id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'reservation_id' THEN RETURN 'uuid_or_null';
                WHEN 'guest_id' THEN RETURN 'uuid_or_null';
                WHEN 'communication_id' THEN RETURN 'uuid_or_null';
                WHEN 'actor_id' THEN RETURN 'uuid';
                WHEN 'template' THEN RETURN 'code_or_empty';
                WHEN 'status' THEN RETURN 'enum:outbox_status';
                WHEN 'attempts' THEN RETURN 'int_count';
                WHEN 'dispatched_at' THEN RETURN 'timestamp_or_null';
                WHEN 'dispatch_worker_id' THEN RETURN 'code_or_empty';
                WHEN 'dispatch_lease_expires_at' THEN RETURN 'timestamp_or_null';
                WHEN 'dispatch_heartbeat_at' THEN RETURN 'timestamp_or_null';
                WHEN 'dispatch_fence_epoch' THEN RETURN 'int_count';
                WHEN 'dispatch_fenced_at' THEN RETURN 'timestamp_or_null';
                WHEN 'dispatch_outcome' THEN RETURN 'code_or_empty';
                WHEN 'reconciled_by' THEN RETURN 'uuid_or_null';
                WHEN 'reconciled_at' THEN RETURN 'timestamp_or_null';
                WHEN 'resolution' THEN RETURN 'enum:reconcile_decision';
                WHEN 'reconciled_decision' THEN RETURN 'enum:reconcile_decision';
                WHEN 'evidence_type' THEN RETURN 'enum:reconcile_evidence_type';
                WHEN 'reconciled_evidence_type' THEN RETURN 'enum:reconcile_evidence_type';
                WHEN 'evidence_ref' THEN RETURN 'code_or_empty';
                WHEN 'reconciled_evidence_ref' THEN RETURN 'code_or_empty';
                WHEN 'reason_code' THEN RETURN 'code_or_empty';
                WHEN 'reconciled_reason_code' THEN RETURN 'code_or_empty';
                WHEN 'cancelled_at' THEN RETURN 'timestamp_or_null';
                WHEN 'next_attempt_at' THEN RETURN 'timestamp_or_null';
                WHEN 'sent_at' THEN RETURN 'timestamp_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'extra' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'code' THEN RETURN 'code';
                WHEN 'pricing_model' THEN RETURN 'enum:pricing_model';
                WHEN 'price_pence' THEN RETURN 'int_pence';
                WHEN 'tax_rate_bps' THEN RETURN 'int_bps';
                WHEN 'max_quantity' THEN RETURN 'int_count';
                WHEN 'is_active' THEN RETURN 'bool';
                WHEN 'sort_order' THEN RETURN 'int_sort';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'archived_at' THEN RETURN 'timestamp_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'guest' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'guest_id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'status' THEN RETURN 'enum:guest_status';
                WHEN 'preferred_contact_method' THEN RETURN 'enum:contact_method_or_empty';
                WHEN 'preferred_language' THEN RETURN 'locale_or_empty';
                WHEN 'merged_into_guest_id' THEN RETURN 'uuid_or_null';
                WHEN 'merged_at' THEN RETURN 'timestamp_or_null';
                WHEN 'anonymized_at' THEN RETURN 'timestamp_or_null';
                WHEN 'erasure_requested_at' THEN RETURN 'timestamp_or_null';
                WHEN 'has_email' THEN RETURN 'bool';
                WHEN 'has_phone' THEN RETURN 'bool';
                WHEN 'has_notes' THEN RETURN 'bool';
                WHEN 'has_reason' THEN RETURN 'bool';
                WHEN 'changed_fields' THEN RETURN 'enum_array:guest_changed_field';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'created_by' THEN RETURN 'uuid_or_null';
                WHEN 'updated_by' THEN RETURN 'uuid_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                WHEN 'guest_a_id' THEN RETURN 'uuid';
                WHEN 'guest_b_id' THEN RETURN 'uuid';
                WHEN 'reason_category' THEN RETURN 'enum:dismissal_reason_category';
                WHEN 'source_guest_id' THEN RETURN 'uuid';
                WHEN 'target_guest_id' THEN RETURN 'uuid';
                WHEN 'merge_event_id' THEN RETURN 'uuid';
                WHEN 'moved_counts' THEN IF p_allow_nested THEN RETURN 'obj:moved_counts'; ELSE RETURN NULL; END IF;
                WHEN 'consent_record_id' THEN RETURN 'uuid';
                WHEN 'consent_type' THEN RETURN 'code_or_empty';
                WHEN 'method' THEN RETURN 'code_or_empty';
                WHEN 'source' THEN RETURN 'code_or_empty';
                WHEN 'policy_version' THEN RETURN 'code_or_empty';
                WHEN 'occurred_at' THEN RETURN 'timestamp';
                WHEN 'pending_dispatches' THEN RETURN 'int_count';
                WHEN 'requested_guest_id' THEN RETURN 'uuid';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'guest_communication' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'communication_id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'guest_id' THEN RETURN 'uuid';
                WHEN 'reservation_id' THEN RETURN 'uuid_or_null';
                WHEN 'enquiry_id' THEN RETURN 'uuid_or_null';
                WHEN 'interaction_type' THEN RETURN 'enum:interaction_type';
                WHEN 'direction' THEN RETURN 'enum:comm_direction';
                WHEN 'template_id' THEN RETURN 'uuid_or_null';
                WHEN 'template_version_id' THEN RETURN 'uuid_or_null';
                WHEN 'rule_id' THEN RETURN 'uuid_or_null';
                WHEN 'trigger_event' THEN RETURN 'enum:trigger_event_or_empty';
                WHEN 'email_outbox_id' THEN RETURN 'uuid_or_null';
                WHEN 'outbox_id' THEN RETURN 'uuid_or_null';
                WHEN 'delivery_status' THEN RETURN 'enum:delivery_status';
                WHEN 'follow_up_required' THEN RETURN 'bool';
                WHEN 'follow_up_assigned_to' THEN RETURN 'uuid_or_null';
                WHEN 'follow_up_at' THEN RETURN 'timestamp_or_null';
                WHEN 'follow_up_completed_at' THEN RETURN 'timestamp_or_null';
                WHEN 'staff_user_id' THEN RETURN 'uuid_or_null';
                WHEN 'occurred_at' THEN RETURN 'timestamp';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'guest_enquiry' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'enquiry_id' THEN RETURN 'uuid';
                WHEN 'message_id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'reference' THEN RETURN 'code';
                WHEN 'guest_id' THEN RETURN 'uuid_or_null';
                WHEN 'reservation_id' THEN RETURN 'uuid_or_null';
                WHEN 'maintenance_request_id' THEN RETURN 'uuid_or_null';
                WHEN 'category' THEN RETURN 'enum:enquiry_category';
                WHEN 'status' THEN RETURN 'enum:enquiry_status';
                WHEN 'priority' THEN RETURN 'enum:priority_4';
                WHEN 'source' THEN RETURN 'enum:enquiry_source';
                WHEN 'direction' THEN RETURN 'enum:comm_direction';
                WHEN 'is_internal' THEN RETURN 'bool';
                WHEN 'assigned_user_id' THEN RETURN 'uuid_or_null';
                WHEN 'staff_user_id' THEN RETURN 'uuid_or_null';
                WHEN 'resolved_by' THEN RETURN 'uuid_or_null';
                WHEN 'closed_by' THEN RETURN 'uuid_or_null';
                WHEN 'target_response_at' THEN RETURN 'timestamp_or_null';
                WHEN 'follow_up_at' THEN RETURN 'timestamp_or_null';
                WHEN 'resolved_at' THEN RETURN 'timestamp_or_null';
                WHEN 'closed_at' THEN RETURN 'timestamp_or_null';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'guest_note' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'note_id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'guest_id' THEN RETURN 'uuid';
                WHEN 'reservation_id' THEN RETURN 'uuid_or_null';
                WHEN 'category' THEN RETURN 'enum:note_category';
                WHEN 'is_pinned' THEN RETURN 'bool';
                WHEN 'created_by' THEN RETURN 'uuid_or_null';
                WHEN 'updated_by' THEN RETURN 'uuid_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'guest_preference' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'preference_id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'guest_id' THEN RETURN 'uuid';
                WHEN 'category' THEN RETURN 'enum:pref_category';
                WHEN 'has_notes' THEN RETURN 'bool';
                WHEN 'created_by' THEN RETURN 'uuid_or_null';
                WHEN 'updated_by' THEN RETURN 'uuid_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'housekeeping_task' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'unit_id' THEN RETURN 'uuid';
                WHEN 'reservation_id' THEN RETURN 'uuid_or_null';
                WHEN 'task_type' THEN RETURN 'enum:task_type';
                WHEN 'stage' THEN RETURN 'enum:hk_stage';
                WHEN 'priority' THEN RETURN 'enum:priority_4';
                WHEN 'unit_status' THEN RETURN 'enum:unit_status_or_empty';
                WHEN 'scheduled_date' THEN RETURN 'date_or_timestamp';
                WHEN 'due_at' THEN RETURN 'timestamp_or_null';
                WHEN 'assigned_to' THEN RETURN 'uuid_or_null';
                WHEN 'same_day_arrival' THEN RETURN 'bool';
                WHEN 'has_active_block' THEN RETURN 'bool';
                WHEN 'overdue' THEN RETURN 'bool';
                WHEN 'started_at' THEN RETURN 'timestamp_or_null';
                WHEN 'started_by' THEN RETURN 'uuid_or_null';
                WHEN 'inspected_at' THEN RETURN 'timestamp_or_null';
                WHEN 'inspected_by' THEN RETURN 'uuid_or_null';
                WHEN 'completed_at' THEN RETURN 'timestamp_or_null';
                WHEN 'completed_by' THEN RETURN 'uuid_or_null';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'maintenance_asset' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'asset_code' THEN RETURN 'code';
                WHEN 'location_id' THEN RETURN 'uuid_or_null';
                WHEN 'unit_id' THEN RETURN 'uuid_or_null';
                WHEN 'status' THEN RETURN 'enum:asset_status';
                WHEN 'installed_on' THEN RETURN 'date_or_timestamp_or_null';
                WHEN 'warranty_expires_on' THEN RETURN 'date_or_timestamp_or_null';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'maintenance_attachment' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'request_id' THEN RETURN 'uuid';
                WHEN 'work_entry_id' THEN RETURN 'uuid_or_null';
                WHEN 'content_type' THEN RETURN 'code';
                WHEN 'size_bytes' THEN RETURN 'int_size_bytes';
                WHEN 'sha256' THEN RETURN 'sha256';
                WHEN 'uploaded_by' THEN RETURN 'uuid_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'maintenance_category' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'code' THEN RETURN 'code';
                WHEN 'is_active' THEN RETURN 'bool';
                WHEN 'sort_order' THEN RETURN 'int_sort';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'maintenance_contractor' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'is_active' THEN RETURN 'bool';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'maintenance_location' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'code' THEN RETURN 'code';
                WHEN 'location_type' THEN RETURN 'code';
                WHEN 'unit_id' THEN RETURN 'uuid_or_null';
                WHEN 'is_active' THEN RETURN 'bool';
                WHEN 'sort_order' THEN RETURN 'int_sort';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'maintenance_request' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'reference' THEN RETURN 'code';
                WHEN 'category_id' THEN RETURN 'uuid';
                WHEN 'priority' THEN RETURN 'enum:priority_4';
                WHEN 'status' THEN RETURN 'enum:maint_status';
                WHEN 'work_type' THEN RETURN 'enum:maint_work_type';
                WHEN 'location_id' THEN RETURN 'uuid_or_null';
                WHEN 'unit_id' THEN RETURN 'uuid_or_null';
                WHEN 'asset_id' THEN RETURN 'uuid_or_null';
                WHEN 'schedule_id' THEN RETURN 'uuid_or_null';
                WHEN 'scheduled_occurrence_date' THEN RETURN 'date_or_timestamp_or_null';
                WHEN 'reported_by' THEN RETURN 'uuid_or_null';
                WHEN 'assigned_user_id' THEN RETURN 'uuid_or_null';
                WHEN 'assigned_contractor_id' THEN RETURN 'uuid_or_null';
                WHEN 'reported_at' THEN RETURN 'timestamp';
                WHEN 'target_date' THEN RETURN 'date_or_timestamp_or_null';
                WHEN 'scheduled_for' THEN RETURN 'timestamp_or_null';
                WHEN 'started_at' THEN RETURN 'timestamp_or_null';
                WHEN 'completed_at' THEN RETURN 'timestamp_or_null';
                WHEN 'closed_at' THEN RETURN 'timestamp_or_null';
                WHEN 'guest_impact' THEN RETURN 'bool';
                WHEN 'safety_impact' THEN RETURN 'bool';
                WHEN 'availability_impact' THEN RETURN 'enum:maint_impact';
                WHEN 'inspection_required' THEN RETURN 'bool';
                WHEN 'overdue' THEN RETURN 'bool';
                WHEN 'has_active_restriction' THEN RETURN 'bool';
                WHEN 'latest_inspection_result' THEN RETURN 'enum:maint_inspection_result_or_empty';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                WHEN 'request_id' THEN RETURN 'uuid';
                WHEN 'entry_type' THEN RETURN 'enum:maint_entry_type';
                WHEN 'author_id' THEN RETURN 'uuid';
                WHEN 'blockout_group_id' THEN RETURN 'uuid';
                WHEN 'group_status' THEN RETURN 'enum:blockout_status';
                WHEN 'start_date' THEN RETURN 'date_or_timestamp';
                WHEN 'end_date' THEN RETURN 'date_or_timestamp';
                WHEN 'group_version' THEN RETURN 'int_version';
                WHEN 'created_by' THEN RETURN 'uuid_or_null';
                WHEN 'released_by' THEN RETURN 'uuid_or_null';
                WHEN 'released_at' THEN RETURN 'timestamp_or_null';
                WHEN 'request' THEN IF p_allow_nested THEN RETURN 'obj:maintenance_request_inner'; ELSE RETURN NULL; END IF;
                WHEN 'inspection' THEN IF p_allow_nested THEN RETURN 'obj:maintenance_inspection'; ELSE RETURN NULL; END IF;
                ELSE RETURN NULL;
            END CASE;

        WHEN 'maintenance_schedule' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'category_id' THEN RETURN 'uuid';
                WHEN 'location_id' THEN RETURN 'uuid_or_null';
                WHEN 'unit_id' THEN RETURN 'uuid_or_null';
                WHEN 'asset_id' THEN RETURN 'uuid_or_null';
                WHEN 'priority' THEN RETURN 'enum:priority_4';
                WHEN 'frequency' THEN RETURN 'enum:maint_frequency';
                WHEN 'next_due_date' THEN RETURN 'date_or_timestamp';
                WHEN 'anchor_day' THEN RETURN 'int_anchor_day';
                WHEN 'last_generated_for_date' THEN RETURN 'date_or_timestamp_or_null';
                WHEN 'assigned_user_id' THEN RETURN 'uuid_or_null';
                WHEN 'assigned_contractor_id' THEN RETURN 'uuid_or_null';
                WHEN 'inspection_required' THEN RETURN 'bool';
                WHEN 'is_active' THEN RETURN 'bool';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'payment_exception' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'hold_id' THEN RETURN 'uuid_or_null';
                WHEN 'payment_attempt_id' THEN RETURN 'uuid_or_null';
                WHEN 'reservation_id' THEN RETURN 'uuid_or_null';
                WHEN 'exception_type' THEN RETURN 'code';
                WHEN 'amount_pence' THEN RETURN 'int_pence';
                WHEN 'currency' THEN RETURN 'currency';
                WHEN 'status' THEN RETURN 'code';
                WHEN 'resolved_by' THEN RETURN 'uuid_or_null';
                WHEN 'resolved_at' THEN RETURN 'timestamp_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'payment_webhook_event' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'event_id' THEN RETURN 'code';
                WHEN 'event_type' THEN RETURN 'code';
                WHEN 'event_livemode' THEN RETURN 'bool';
                WHEN 'expected_livemode' THEN RETURN 'bool';
                WHEN 'environment' THEN RETURN 'code';
                WHEN 'payment_attempt' THEN RETURN 'code_or_empty';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'property' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'slug' THEN RETURN 'code';
                WHEN 'country_code' THEN RETURN 'country_code';
                WHEN 'timezone' THEN RETURN 'timezone';
                WHEN 'currency' THEN RETURN 'currency';
                WHEN 'check_in_time' THEN RETURN 'time_hhmm';
                WHEN 'check_out_time' THEN RETURN 'time_hhmm';
                WHEN 'vat_registered' THEN RETURN 'bool';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'property_booking_policy' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'hold_duration_minutes' THEN RETURN 'int_hold_min';
                WHEN 'deposit_mode' THEN RETURN 'enum:deposit_mode';
                WHEN 'deposit_percentage' THEN RETURN 'int_pct';
                WHEN 'deposit_fixed_pence' THEN RETURN 'int_pence';
                WHEN 'balance_due_days_before_arrival' THEN RETURN 'int_days_365';
                WHEN 'extra_adult_nightly_pence' THEN RETURN 'int_pence';
                WHEN 'child_nightly_pence' THEN RETURN 'int_pence';
                WHEN 'infant_nightly_pence' THEN RETURN 'int_pence';
                WHEN 'booking_terms_version' THEN RETURN 'code_or_empty';
                WHEN 'stripe_enabled' THEN RETURN 'bool';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'property_retention_policy' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'enquiry_retention_days' THEN RETURN 'int_retention_days';
                WHEN 'inactive_guest_retention_days' THEN RETURN 'int_retention_days';
                WHEN 'comms_retention_days' THEN RETURN 'int_retention_days';
                WHEN 'auto_anonymize_enabled' THEN RETURN 'bool';
                WHEN 'execution_mode' THEN RETURN 'code_or_empty';
                WHEN 'updated_by' THEN RETURN 'uuid_or_null';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'rate_plan' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'code' THEN RETURN 'code';
                WHEN 'is_default' THEN RETURN 'bool';
                WHEN 'is_active' THEN RETURN 'bool';
                WHEN 'priority' THEN RETURN 'int_sort';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'archived_at' THEN RETURN 'timestamp_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'rate_rule' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'rate_plan_id' THEN RETURN 'uuid';
                WHEN 'unit_type_id' THEN RETURN 'uuid';
                WHEN 'start_date' THEN RETURN 'date_or_timestamp';
                WHEN 'end_date' THEN RETURN 'date_or_timestamp';
                WHEN 'days_of_week' THEN RETURN 'int_dow_mask';
                WHEN 'nightly_rate_pence' THEN RETURN 'int_pence';
                WHEN 'min_nights_override' THEN RETURN 'int_or_null_1_365';
                WHEN 'priority' THEN RETURN 'int_sort';
                WHEN 'is_active' THEN RETURN 'bool';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'archived_at' THEN RETURN 'timestamp_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'reservation' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'reservation_id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'reference' THEN RETURN 'code';
                WHEN 'guest_id' THEN RETURN 'uuid_or_null';
                WHEN 'channel_code' THEN RETURN 'code_or_empty';
                WHEN 'arrival_date' THEN RETURN 'date_or_timestamp';
                WHEN 'departure_date' THEN RETURN 'date_or_timestamp';
                WHEN 'nights' THEN RETURN 'int_nights';
                WHEN 'adults' THEN RETURN 'int_party';
                WHEN 'children' THEN RETURN 'int_party';
                WHEN 'infants' THEN RETURN 'int_party';
                WHEN 'status' THEN RETURN 'enum:reservation_status';
                WHEN 'payment_status' THEN RETURN 'enum:payment_status';
                WHEN 'total_pence' THEN RETURN 'int_pence';
                WHEN 'deposit_pence' THEN RETURN 'int_pence';
                WHEN 'amount_paid_pence' THEN RETURN 'int_pence';
                WHEN 'balance_due_pence' THEN RETURN 'int_pence';
                WHEN 'currency' THEN RETURN 'currency';
                WHEN 'unit_ids' THEN RETURN 'uuid_array';
                WHEN 'changed_fields' THEN RETURN 'enum_array:reservation_changed_field';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'cancelled_at' THEN RETURN 'timestamp_or_null';
                WHEN 'cancellation_requested_at' THEN RETURN 'timestamp_or_null';
                WHEN 'created_by' THEN RETURN 'uuid_or_null';
                WHEN 'updated_by' THEN RETURN 'uuid_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'unit' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'unit_type_id' THEN RETURN 'uuid';
                WHEN 'code' THEN RETURN 'code';
                WHEN 'status' THEN RETURN 'enum:unit_status';
                WHEN 'sort_order' THEN RETURN 'int_sort';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'archived_at' THEN RETURN 'timestamp_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'unit_type' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'code' THEN RETURN 'code';
                WHEN 'category' THEN RETURN 'enum:unit_category';
                WHEN 'base_capacity_adults' THEN RETURN 'int_party';
                WHEN 'max_adults' THEN RETURN 'int_party';
                WHEN 'max_children' THEN RETURN 'int_party';
                WHEN 'max_infants' THEN RETURN 'int_party';
                WHEN 'max_guests' THEN RETURN 'int_party';
                WHEN 'pets_allowed' THEN RETURN 'bool';
                WHEN 'max_pets' THEN RETURN 'int_party';
                WHEN 'base_nightly_rate_pence' THEN RETURN 'int_pence';
                WHEN 'tax_rate_bps' THEN RETURN 'int_bps';
                WHEN 'min_nights' THEN RETURN 'int_days_1_365';
                WHEN 'max_nights' THEN RETURN 'int_or_null_1_365';
                WHEN 'is_active' THEN RETURN 'bool';
                WHEN 'is_public_bookable' THEN RETURN 'bool';
                WHEN 'sort_order' THEN RETURN 'int_sort';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'archived_at' THEN RETURN 'timestamp_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        ELSE
            RETURN NULL;
    END CASE;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

CREATE OR REPLACE FUNCTION public.audit_validate_key_rule(
    p_val  JSONB,
    p_rule TEXT
) RETURNS BOOLEAN AS $$
DECLARE
    v_type TEXT;
    v_str  TEXT;
    v_elem JSONB;
    v_kv   RECORD;
    v_sub  TEXT;
BEGIN
    IF p_val IS NULL OR p_rule IS NULL THEN
        RETURN FALSE;
    END IF;
    v_type := pg_catalog.jsonb_typeof(p_val);

    CASE p_rule
        WHEN 'bool_true' THEN
            RETURN v_type = 'boolean' AND (p_val #>> '{}') = 'true';
        WHEN 'bool' THEN
            RETURN v_type = 'boolean';
        WHEN 'uuid' THEN
            RETURN v_type = 'string' AND (p_val #>> '{}') ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$';
        WHEN 'uuid_or_null' THEN
            RETURN v_type = 'null' OR (v_type = 'string' AND (p_val #>> '{}') ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$');
        WHEN 'uuid_array' THEN
            IF v_type = 'null' THEN
                RETURN TRUE;
            END IF;
            IF v_type <> 'array' THEN
                RETURN FALSE;
            END IF;
            FOR v_elem IN SELECT value FROM pg_catalog.jsonb_array_elements(p_val) LOOP
                IF pg_catalog.jsonb_typeof(v_elem) <> 'string'
                   OR NOT ((v_elem #>> '{}') ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$') THEN
                    RETURN FALSE;
                END IF;
            END LOOP;
            RETURN TRUE;
        WHEN 'timestamp' THEN
            RETURN v_type = 'string' AND (p_val #>> '{}') ~ '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$';
        WHEN 'timestamp_or_null' THEN
            RETURN v_type = 'null' OR (v_type = 'string' AND (p_val #>> '{}') ~ '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$');
        WHEN 'date_or_timestamp' THEN
            RETURN v_type = 'string' AND (
                (p_val #>> '{}') ~ '^\d{4}-\d{2}-\d{2}$'
                OR (p_val #>> '{}') ~ '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$'
            );
        WHEN 'date_or_timestamp_or_null' THEN
            RETURN v_type = 'null' OR (v_type = 'string' AND (
                (p_val #>> '{}') ~ '^\d{4}-\d{2}-\d{2}$'
                OR (p_val #>> '{}') ~ '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$'
            ));
        WHEN 'time_hhmm' THEN
            RETURN v_type = 'string' AND ((p_val #>> '{}') = '' OR (p_val #>> '{}') ~ '^([01]\d|2[0-3]):[0-5]\d(:[0-5]\d)?$');
        WHEN 'currency' THEN
            RETURN v_type = 'string' AND (p_val #>> '{}') ~ '^[A-Z]{3}$';
        WHEN 'country_code' THEN
            RETURN v_type = 'string' AND (p_val #>> '{}') ~ '^[A-Z]{2}$';
        WHEN 'locale_or_empty' THEN
            RETURN v_type = 'string' AND ((p_val #>> '{}') = '' OR (p_val #>> '{}') ~ '^[a-z]{2}(-[A-Z]{2})?$');
        WHEN 'timezone' THEN
            RETURN v_type = 'string' AND (p_val #>> '{}') ~ '^[A-Za-z_]+(/[A-Za-z0-9_+-]+){0,2}$';
        WHEN 'sha256' THEN
            RETURN v_type = 'string' AND (p_val #>> '{}') ~ '^[0-9a-fA-F]{64}$';
        WHEN 'code' THEN
            RETURN v_type = 'string' AND public.audit_is_safe_code(p_val #>> '{}', FALSE);
        WHEN 'code_or_empty' THEN
            RETURN v_type = 'string' AND public.audit_is_safe_code(p_val #>> '{}', TRUE);
        WHEN 'int_version' THEN
            RETURN public.audit_is_safe_integer(p_val, 0, 100000);
        WHEN 'int_count' THEN
            RETURN public.audit_is_safe_integer(p_val, 0, 100000);
        WHEN 'int_party' THEN
            RETURN public.audit_is_safe_integer(p_val, 0, 100);
        WHEN 'int_days_365' THEN
            RETURN public.audit_is_safe_integer(p_val, 0, 365);
        WHEN 'int_days_1_365' THEN
            RETURN public.audit_is_safe_integer(p_val, 1, 365);
        WHEN 'int_or_null_1_365' THEN
            RETURN v_type = 'null' OR public.audit_is_safe_integer(p_val, 1, 365);
        WHEN 'int_nights' THEN
            RETURN public.audit_is_safe_integer(p_val, 0, 3650);
        WHEN 'int_retention_days' THEN
            RETURN public.audit_is_safe_integer(p_val, 1, 36500);
        WHEN 'int_pence' THEN
            RETURN public.audit_is_safe_integer(p_val, 0, 50000000);
        WHEN 'int_bps' THEN
            RETURN public.audit_is_safe_integer(p_val, 0, 10000);
        WHEN 'int_pct' THEN
            RETURN public.audit_is_safe_integer(p_val, 0, 100);
        WHEN 'int_sort' THEN
            RETURN public.audit_is_safe_integer(p_val, 0, 10000);
        WHEN 'int_hold_min' THEN
            RETURN public.audit_is_safe_integer(p_val, 1, 10080);
        WHEN 'int_offset_hours' THEN
            RETURN public.audit_is_safe_integer(p_val, -720, 720);
        WHEN 'int_or_null_hour_local' THEN
            RETURN v_type = 'null' OR public.audit_is_safe_integer(p_val, 0, 23);
        WHEN 'int_anchor_day' THEN
            RETURN public.audit_is_safe_integer(p_val, 0, 31);
        WHEN 'int_dow_mask' THEN
            RETURN public.audit_is_safe_integer(p_val, 0, 127);
        WHEN 'int_size_bytes' THEN
            RETURN public.audit_is_safe_integer(p_val, 0, 52428800);
        WHEN 'int_audit_log_id' THEN
            RETURN public.audit_is_safe_integer(p_val, 1, 100000000);
        WHEN 'obj:moved_counts' THEN
            IF v_type = 'null' THEN
                RETURN TRUE;
            END IF;
            IF v_type <> 'object' THEN
                RETURN FALSE;
            END IF;
            FOR v_kv IN SELECT key, value FROM pg_catalog.jsonb_each(p_val) LOOP
                IF v_kv.key NOT IN (
                    'reservations', 'booking_holds', 'preferences', 'notes',
                    'enquiries', 'communications', 'email_outbox', 'communication_jobs',
                    'consent_records', 'dismissals', 'delivery_events', 'jobs'
                ) OR NOT public.audit_is_safe_integer(v_kv.value, 0, 100000) THEN
                    RETURN FALSE;
                END IF;
            END LOOP;
            RETURN TRUE;
        WHEN 'obj:blockout_group_inner' THEN
            IF v_type <> 'object' THEN
                RETURN FALSE;
            END IF;
            FOR v_kv IN SELECT key, value FROM pg_catalog.jsonb_each(p_val) LOOP
                v_sub := public.audit_resource_key_rule('blockout_group', v_kv.key, FALSE);
                IF v_sub IS NULL OR NOT public.audit_validate_key_rule(v_kv.value, v_sub) THEN
                    RETURN FALSE;
                END IF;
            END LOOP;
            RETURN TRUE;
        WHEN 'obj:maintenance_request_inner' THEN
            IF v_type <> 'object' THEN
                RETURN FALSE;
            END IF;
            FOR v_kv IN SELECT key, value FROM pg_catalog.jsonb_each(p_val) LOOP
                v_sub := public.audit_resource_key_rule('maintenance_request', v_kv.key, FALSE);
                IF v_sub IS NULL OR NOT public.audit_validate_key_rule(v_kv.value, v_sub) THEN
                    RETURN FALSE;
                END IF;
            END LOOP;
            RETURN TRUE;
        WHEN 'obj:maintenance_inspection' THEN
            IF v_type <> 'object' THEN
                RETURN FALSE;
            END IF;
            FOR v_kv IN SELECT key, value FROM pg_catalog.jsonb_each(p_val) LOOP
                CASE v_kv.key
                    WHEN 'id' THEN v_sub := 'uuid';
                    WHEN 'property_id' THEN v_sub := 'uuid';
                    WHEN 'request_id' THEN v_sub := 'uuid';
                    WHEN 'inspector_id' THEN v_sub := 'uuid';
                    WHEN 'result' THEN v_sub := 'enum:maint_inspection_result';
                    WHEN 'inspected_at' THEN v_sub := 'timestamp';
                    ELSE RETURN FALSE;
                END CASE;
                IF NOT public.audit_validate_key_rule(v_kv.value, v_sub) THEN
                    RETURN FALSE;
                END IF;
            END LOOP;
            RETURN TRUE;
        ELSE
            IF p_rule LIKE 'enum:%' THEN
                IF v_type <> 'string' THEN
                    RETURN FALSE;
                END IF;
                v_str := p_val #>> '{}';
                RETURN public.audit_is_allowed_enum(v_str, pg_catalog.substr(p_rule, 6));
            END IF;
            IF p_rule LIKE 'enum_array:%' THEN
                IF v_type = 'null' THEN
                    RETURN TRUE;
                END IF;
                IF v_type <> 'array' THEN
                    RETURN FALSE;
                END IF;
                v_sub := pg_catalog.substr(p_rule, 12);
                FOR v_elem IN SELECT value FROM pg_catalog.jsonb_array_elements(p_val) LOOP
                    IF pg_catalog.jsonb_typeof(v_elem) <> 'string'
                       OR NOT public.audit_is_allowed_enum(v_elem #>> '{}', v_sub) THEN
                        RETURN FALSE;
                    END IF;
                END LOOP;
                RETURN TRUE;
            END IF;
            RETURN FALSE;
    END CASE;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

-- 4. Top-level resource-type-aware replacement snapshot validator (fail-closed).
CREATE OR REPLACE FUNCTION public.validate_audit_redaction_snapshot(
    p_resource_type TEXT,
    p_slot          TEXT,
    p_node          JSONB
) RETURNS BOOLEAN AS $$
DECLARE
    v_resource_type TEXT := pg_catalog.lower(pg_catalog.btrim(COALESCE(p_resource_type, '')));
    v_kv            RECORD;
    v_rule          TEXT;
BEGIN
    IF p_slot NOT IN ('before_state', 'after_state', 'metadata') THEN
        RAISE EXCEPTION 'invalid audit redaction snapshot slot' USING ERRCODE = '22023';
    END IF;

    IF NOT public.audit_is_allowed_enum(v_resource_type, 'resource_type') THEN
        RAISE EXCEPTION 'unsupported audit redaction resource type' USING ERRCODE = '22023';
    END IF;

    IF p_node IS NULL THEN
        RETURN TRUE;
    END IF;

    IF pg_catalog.jsonb_typeof(p_node) <> 'object' THEN
        RAISE EXCEPTION 'audit redaction % must be a JSON object or NULL', p_slot USING ERRCODE = '22023';
    END IF;

    PERFORM public.audit_check_json_limits(p_node, 0);

    FOR v_kv IN SELECT key, value FROM pg_catalog.jsonb_each(p_node) LOOP
        v_rule := public.audit_resource_key_rule(v_resource_type, v_kv.key, TRUE);
        IF v_rule IS NULL THEN
            RAISE EXCEPTION 'audit redaction % contains unapproved key for resource type', p_slot USING ERRCODE = '22023';
        END IF;
        IF NOT public.audit_validate_key_rule(v_kv.value, v_rule) THEN
            RAISE EXCEPTION 'audit redaction % contains invalid field value or type', p_slot USING ERRCODE = '22023';
        END IF;
    END LOOP;

    RETURN TRUE;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

-- 5. Replace redact_audit_log_entry (exact 7-arg signature) so resource_type is always looked up
-- authoritatively from public.audit_logs by p_audit_log_id and enforced via strict allowlist.
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
    v_resource_type TEXT;
    v_existing      UUID;
    v_actor_role    TEXT;
    v_actor_prop_id UUID;
    v_clean_reason  TEXT;
BEGIN
    v_clean_reason := public.validate_non_pii_reason('audit redaction reason', p_reason, 5, 240);

    IF p_redacted_by IS NULL THEN
        RAISE EXCEPTION 'authorized redaction actor is required' USING ERRCODE = '42501';
    END IF;

    SELECT u.role, u.property_id INTO v_actor_role, v_actor_prop_id
    FROM public.admin_users u
    WHERE u.id = p_redacted_by
      AND u.is_active = TRUE
      AND u.must_rotate_password = FALSE;

    IF NOT FOUND OR v_actor_role NOT IN ('owner_admin', 'manager') THEN
        RAISE EXCEPTION 'actor is not authorized to redact audit log entries' USING ERRCODE = '42501';
    END IF;

    SELECT a.property_id, a.resource_type INTO v_prop_id, v_resource_type
    FROM public.audit_logs a
    WHERE a.id = p_audit_log_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'audit_log entry not found' USING ERRCODE = 'P0002';
    END IF;

    IF v_actor_prop_id IS NOT NULL AND (v_prop_id IS NULL OR v_actor_prop_id <> v_prop_id) THEN
        RAISE EXCEPTION 'actor cannot redact audit log entry outside assigned property' USING ERRCODE = '42501';
    END IF;

    -- Strictly validate replacement snapshots against target audit entry resource_type BEFORE overlay write.
    PERFORM public.validate_audit_redaction_snapshot(v_resource_type, 'before_state', p_before_state);
    PERFORM public.validate_audit_redaction_snapshot(v_resource_type, 'after_state', p_after_state);
    PERFORM public.validate_audit_redaction_snapshot(v_resource_type, 'metadata', p_metadata);

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

-- 6. Replace audit_log_redactions BEFORE INSERT trigger to enforce identical resource-type allowlist
-- against audit_logs.resource_type even on privileged direct table inserts.
CREATE OR REPLACE FUNCTION public.enforce_audit_log_redactions_non_pii() RETURNS TRIGGER AS $$
DECLARE
    v_resource_type TEXT;
BEGIN
    NEW.reason := public.validate_non_pii_reason('audit_log_redactions.reason', NEW.reason, 5, 240);

    SELECT a.resource_type INTO v_resource_type
    FROM public.audit_logs a
    WHERE a.id = NEW.audit_log_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'audit_log entry not found' USING ERRCODE = 'P0002';
    END IF;

    PERFORM public.validate_audit_redaction_snapshot(v_resource_type, 'before_state', NEW.redacted_before_state);
    PERFORM public.validate_audit_redaction_snapshot(v_resource_type, 'after_state', NEW.redacted_after_state);
    PERFORM public.validate_audit_redaction_snapshot(v_resource_type, 'metadata', NEW.redacted_metadata);

    RETURN NEW;
END;
$$ LANGUAGE plpgsql SET search_path = pg_catalog, public;

DROP TRIGGER IF EXISTS trg_audit_log_redactions_non_pii ON public.audit_log_redactions;
CREATE TRIGGER trg_audit_log_redactions_non_pii
    BEFORE INSERT ON public.audit_log_redactions
    FOR EACH ROW EXECUTE FUNCTION public.enforce_audit_log_redactions_non_pii();

-- 7. Strict execution grants matching least-privilege application role posture.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
        REVOKE ALL ON FUNCTION public.audit_check_json_limits(JSONB, INT) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION public.audit_check_json_limits(JSONB, INT) TO lovelace_app;
        REVOKE ALL ON FUNCTION public.audit_is_safe_code(TEXT, BOOLEAN) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION public.audit_is_safe_code(TEXT, BOOLEAN) TO lovelace_app;
        REVOKE ALL ON FUNCTION public.audit_is_safe_integer(JSONB, BIGINT, BIGINT) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION public.audit_is_safe_integer(JSONB, BIGINT, BIGINT) TO lovelace_app;
        REVOKE ALL ON FUNCTION public.audit_is_allowed_enum(TEXT, TEXT) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION public.audit_is_allowed_enum(TEXT, TEXT) TO lovelace_app;
        REVOKE ALL ON FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN) TO lovelace_app;
        REVOKE ALL ON FUNCTION public.audit_validate_key_rule(JSONB, TEXT) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION public.audit_validate_key_rule(JSONB, TEXT) TO lovelace_app;
        REVOKE ALL ON FUNCTION public.validate_audit_redaction_snapshot(TEXT, TEXT, JSONB) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION public.validate_audit_redaction_snapshot(TEXT, TEXT, JSONB) TO lovelace_app;
        REVOKE ALL ON FUNCTION public.redact_audit_log_entry(UUID, BIGINT, JSONB, JSONB, JSONB, TEXT, UUID) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION public.redact_audit_log_entry(UUID, BIGINT, JSONB, JSONB, JSONB, TEXT, UUID) TO lovelace_app;
    END IF;
END $$;
