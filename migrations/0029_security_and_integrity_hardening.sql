-- 0029_security_and_integrity_hardening.sql
-- Multi-tenant composite foreign-key isolation, least-privilege role hardening,
-- and 1:1 Go/PostgreSQL audit allowlist parity fixes chained on top of Phase 3 M4 (0026..0028).

-- 1. Composite unique keys and multi-tenant foreign keys preventing cross-property linking.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'uq_rate_plans_property_id_id') THEN
        ALTER TABLE public.rate_plans
            ADD CONSTRAINT uq_rate_plans_property_id_id UNIQUE (property_id, id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'uq_unit_types_property_id_id') THEN
        ALTER TABLE public.unit_types
            ADD CONSTRAINT uq_unit_types_property_id_id UNIQUE (property_id, id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'uq_units_property_id_id') THEN
        ALTER TABLE public.units
            ADD CONSTRAINT uq_units_property_id_id UNIQUE (property_id, id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'uq_maintenance_requests_property_id_id') THEN
        ALTER TABLE public.maintenance_requests
            ADD CONSTRAINT uq_maintenance_requests_property_id_id UNIQUE (property_id, id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'uq_guest_enquiries_property_id_id') THEN
        ALTER TABLE public.guest_enquiries
            ADD CONSTRAINT uq_guest_enquiries_property_id_id UNIQUE (property_id, id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'uq_communication_templates_property_id_id') THEN
        ALTER TABLE public.communication_templates
            ADD CONSTRAINT uq_communication_templates_property_id_id UNIQUE (property_id, id);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_rate_rules_property_rate_plan') THEN
        ALTER TABLE public.rate_rules
            ADD CONSTRAINT fk_rate_rules_property_rate_plan
            FOREIGN KEY (property_id, rate_plan_id) REFERENCES public.rate_plans (property_id, id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_rate_rules_property_unit_type') THEN
        ALTER TABLE public.rate_rules
            ADD CONSTRAINT fk_rate_rules_property_unit_type
            FOREIGN KEY (property_id, unit_type_id) REFERENCES public.unit_types (property_id, id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_units_property_unit_type') THEN
        ALTER TABLE public.units
            ADD CONSTRAINT fk_units_property_unit_type
            FOREIGN KEY (property_id, unit_type_id) REFERENCES public.unit_types (property_id, id);
    END IF;
    IF to_regclass('public.inventory_claims') IS NOT NULL
       AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_inventory_claims_property_unit') THEN
        ALTER TABLE public.inventory_claims
            ADD CONSTRAINT fk_inventory_claims_property_unit
            FOREIGN KEY (property_id, unit_id) REFERENCES public.units (property_id, id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_guest_enquiries_property_maintenance') THEN
        ALTER TABLE public.guest_enquiries
            ADD CONSTRAINT fk_guest_enquiries_property_maintenance
            FOREIGN KEY (property_id, maintenance_request_id) REFERENCES public.maintenance_requests (property_id, id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_guest_communications_property_enquiry') THEN
        ALTER TABLE public.guest_communications
            ADD CONSTRAINT fk_guest_communications_property_enquiry
            FOREIGN KEY (property_id, enquiry_id) REFERENCES public.guest_enquiries (property_id, id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_comm_automation_rules_property_template') THEN
        ALTER TABLE public.communication_automation_rules
            ADD CONSTRAINT fk_comm_automation_rules_property_template
            FOREIGN KEY (property_id, template_id) REFERENCES public.communication_templates (property_id, id);
    END IF;
END $$;

-- Enforce at most one non-archived default rate plan per property under concurrent writes.
CREATE UNIQUE INDEX IF NOT EXISTS idx_rate_plans_one_active_default
    ON public.rate_plans (property_id)
    WHERE is_default = TRUE AND archived_at IS NULL;

-- 2. Enforce least-privilege table grants across schema_migrations, property_branding_settings,
-- Phase 3 M4 portal tables, and Phase 5 append-only / read-only inventory history tables.
DO $$
BEGIN
    IF to_regclass('public.schema_migrations') IS NOT NULL THEN
        REVOKE ALL ON TABLE public.schema_migrations FROM PUBLIC;
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
            REVOKE ALL ON TABLE public.schema_migrations FROM lovelace_app;
            GRANT SELECT ON TABLE public.schema_migrations TO lovelace_app;
        END IF;
    END IF;

    IF to_regclass('public.property_branding_settings') IS NOT NULL THEN
        REVOKE ALL ON TABLE public.property_branding_settings FROM PUBLIC;
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
            REVOKE ALL ON TABLE public.property_branding_settings FROM lovelace_app;
            GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.property_branding_settings TO lovelace_app;
        END IF;
    END IF;

    IF to_regclass('public.guest_portal_identities') IS NOT NULL THEN
        REVOKE ALL ON TABLE public.guest_portal_identities FROM PUBLIC;
        REVOKE ALL ON TABLE public.guest_portal_challenges FROM PUBLIC;
        REVOKE ALL ON TABLE public.guest_portal_sessions FROM PUBLIC;
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
            GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.guest_portal_identities TO lovelace_app;
            GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.guest_portal_challenges TO lovelace_app;
            GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.guest_portal_sessions TO lovelace_app;
        END IF;
    END IF;

    IF to_regclass('public.unit_status_history') IS NOT NULL THEN
        REVOKE ALL ON TABLE public.unit_status_history FROM PUBLIC;
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
            REVOKE ALL ON TABLE public.unit_status_history FROM lovelace_app;
            GRANT SELECT, INSERT ON TABLE public.unit_status_history TO lovelace_app;
        END IF;
    END IF;

    IF to_regclass('public.inventory_history_gaps') IS NOT NULL THEN
        REVOKE ALL ON TABLE public.inventory_history_gaps FROM PUBLIC;
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
            REVOKE ALL ON TABLE public.inventory_history_gaps FROM lovelace_app;
            GRANT SELECT ON TABLE public.inventory_history_gaps TO lovelace_app;
        END IF;
    END IF;
END $$;

-- 3. Audit allowlist parity helpers for external OTA IDs, consent status, work-performed entries,
-- duplicate dismissal categories, and nullable guest_communication.guest_id, chained onto _phase3_m4.
CREATE OR REPLACE FUNCTION public.audit_is_safe_opaque_id(
    p_val         TEXT,
    p_allow_empty BOOLEAN DEFAULT FALSE
) RETURNS BOOLEAN AS $$
BEGIN
    IF p_val IS NULL THEN
        RETURN FALSE;
    END IF;
    IF p_val = '' THEN
        RETURN p_allow_empty;
    END IF;
    IF length(p_val) > 128
       OR NOT (p_val ~ '^[A-Za-z0-9][A-Za-z0-9._:/#-]{0,127}$')
       OR position('@' in p_val) > 0 THEN
        RETURN FALSE;
    END IF;
    IF p_val ~ '^[0-9a-fA-F]{16,64}$' AND p_val ~ '[a-fA-F]' THEN
        RETURN TRUE;
    END IF;
    IF p_val ~ '0[1-35-9][0-9._:/-]{7,}' THEN
        RETURN FALSE;
    END IF;
    IF public.text_contains_pii(p_val) THEN
        RETURN FALSE;
    END IF;
    RETURN TRUE;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

DO $$
BEGIN
    IF to_regprocedure('public.audit_is_allowed_enum_phase3_m4(text,text)') IS NULL THEN
        ALTER FUNCTION public.audit_is_allowed_enum(TEXT, TEXT)
            RENAME TO audit_is_allowed_enum_phase3_m4;
    END IF;
    IF to_regprocedure('public.audit_resource_key_rule_phase3_m4(text,text,boolean)') IS NULL THEN
        ALTER FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN)
            RENAME TO audit_resource_key_rule_phase3_m4;
    END IF;
    IF to_regprocedure('public.audit_validate_key_rule_base(jsonb,text)') IS NULL THEN
        ALTER FUNCTION public.audit_validate_key_rule(JSONB, TEXT)
            RENAME TO audit_validate_key_rule_base;
    END IF;
END $$;

CREATE OR REPLACE FUNCTION public.audit_is_allowed_enum(
    p_val       TEXT,
    p_enum_name TEXT
) RETURNS BOOLEAN AS $$
BEGIN
    IF p_val IS NULL THEN
        RETURN FALSE;
    END IF;

    CASE p_enum_name
        WHEN 'maint_entry_type' THEN
            IF p_val = 'WORK_PERFORMED' THEN
                RETURN TRUE;
            END IF;
            RETURN public.audit_is_allowed_enum_phase3_m4(p_val, p_enum_name);
        WHEN 'dismissal_reason_category' THEN
            IF p_val IN ('NOT_DUPLICATE', 'DISTINCT_PEOPLE', 'SHARED_CONTACT', 'HOUSEHOLD', 'CORPORATE', 'ANONYMISED') THEN
                RETURN TRUE;
            END IF;
            RETURN public.audit_is_allowed_enum_phase3_m4(p_val, p_enum_name);
        WHEN 'consent_status' THEN
            RETURN p_val IN ('GRANTED', 'WITHDRAWN');
        ELSE
            RETURN public.audit_is_allowed_enum_phase3_m4(p_val, p_enum_name);
    END CASE;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

CREATE OR REPLACE FUNCTION public.audit_resource_key_rule(
    p_resource_type TEXT,
    p_key           TEXT,
    p_is_metadata   BOOLEAN DEFAULT FALSE
) RETURNS TEXT AS $$
BEGIN
    IF p_resource_type = 'guest' AND p_key = 'consent_status' THEN
        RETURN 'enum:consent_status';
    END IF;
    IF p_resource_type = 'guest_communication' AND p_key = 'guest_id' THEN
        RETURN 'uuid_or_null';
    END IF;
    IF p_resource_type = 'audit_log' AND p_key = 'target_resource_id' THEN
        RETURN 'opaque_id';
    END IF;
    IF p_resource_type = 'channel_external_reservation' THEN
        IF p_key = 'external_reservation_id' THEN
            RETURN 'opaque_id';
        END IF;
        IF p_key = 'external_revision' THEN
            RETURN 'opaque_id_or_empty';
        END IF;
    END IF;
    IF p_resource_type = 'channel_listing_mapping' THEN
        IF p_key = 'external_property_id' OR p_key = 'external_rate_plan_id' THEN
            RETURN 'opaque_id_or_empty';
        END IF;
        IF p_key = 'external_listing_id' THEN
            RETURN 'opaque_id';
        END IF;
    END IF;
    IF p_resource_type = 'channel_webhook_event' AND p_key = 'external_event_id' THEN
        RETURN 'opaque_id';
    END IF;
    IF p_resource_type = 'payment_webhook_event' AND p_key = 'event_id' THEN
        RETURN 'opaque_id';
    END IF;
    RETURN public.audit_resource_key_rule_phase3_m4(p_resource_type, p_key, p_is_metadata);
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

CREATE OR REPLACE FUNCTION public.audit_validate_key_rule(
    p_val  JSONB,
    p_rule TEXT
) RETURNS BOOLEAN AS $$
DECLARE
    v_type TEXT;
BEGIN
    IF p_val IS NULL OR p_rule IS NULL THEN
        RETURN FALSE;
    END IF;
    v_type := pg_catalog.jsonb_typeof(p_val);

    CASE p_rule
        WHEN 'opaque_id' THEN
            RETURN v_type = 'string' AND public.audit_is_safe_opaque_id(p_val #>> '{}', FALSE);
        WHEN 'opaque_id_or_empty' THEN
            RETURN v_type = 'string' AND public.audit_is_safe_opaque_id(p_val #>> '{}', TRUE);
        ELSE
            RETURN public.audit_validate_key_rule_base(p_val, p_rule);
    END CASE;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

DO $$
BEGIN
    REVOKE ALL ON FUNCTION public.audit_is_safe_opaque_id(TEXT, BOOLEAN) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_is_allowed_enum_phase3_m4(TEXT, TEXT) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_resource_key_rule_phase3_m4(TEXT, TEXT, BOOLEAN) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_validate_key_rule_base(JSONB, TEXT) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_is_allowed_enum(TEXT, TEXT) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_validate_key_rule(JSONB, TEXT) FROM PUBLIC;

    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
        GRANT EXECUTE ON FUNCTION public.audit_is_safe_opaque_id(TEXT, BOOLEAN) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_is_allowed_enum_phase3_m4(TEXT, TEXT) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_resource_key_rule_phase3_m4(TEXT, TEXT, BOOLEAN) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_validate_key_rule_base(JSONB, TEXT) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_is_allowed_enum(TEXT, TEXT) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_validate_key_rule(JSONB, TEXT) TO lovelace_app;
    END IF;
END $$;
