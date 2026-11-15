-- Phase 5: Reporting & Business Intelligence
-- 1. Targeted read indexes for property-scoped reporting queries.
-- 2. Audit allowlist extension for sanitized report_export audit events.

-- Cash-flow reporting grouped by property local date and currency
CREATE INDEX IF NOT EXISTS idx_payments_property_paid_status
    ON public.payments (property_id, paid_at, status);

CREATE INDEX IF NOT EXISTS idx_refunds_property_status_created
    ON public.refunds (property_id, status, created_at);

-- Channel performance and stay status reporting
CREATE INDEX IF NOT EXISTS idx_reservations_property_status_channel
    ON public.reservations (property_id, status, channel_code);

-- Out-of-order / owner block vs sellable inventory breakdown
CREATE INDEX IF NOT EXISTS idx_inventory_claims_property_type_status
    ON public.inventory_claims (property_id, claim_type, status);

-- Latest persisted booking quote lookup per reservation
CREATE INDEX IF NOT EXISTS idx_booking_quotes_property_res_created
    ON public.booking_quotes (property_id, reservation_id, created_at DESC)
    WHERE reservation_id IS NOT NULL;

-- Canonical guest merge lineage traversal for retention analytics
CREATE INDEX IF NOT EXISTS idx_guests_property_merged_into
    ON public.guests (property_id, merged_into_guest_id)
    WHERE merged_into_guest_id IS NOT NULL;

-- Channel completeness diagnostics for unmapped / conflict external bookings
CREATE INDEX IF NOT EXISTS idx_channel_ext_res_property_status
    ON public.channel_external_reservations (property_id, import_status, updated_at DESC);

-- Extend database-enforced audit redaction allowlist cleanly for report_export events,
-- delegating all Phase 1-4 resource types and enums to _phase4 wrappers.
DO $$
BEGIN
    IF to_regprocedure('public.audit_is_allowed_enum_phase4(text,text)') IS NULL THEN
        ALTER FUNCTION public.audit_is_allowed_enum(TEXT, TEXT)
            RENAME TO audit_is_allowed_enum_phase4;
    END IF;
    IF to_regprocedure('public.audit_resource_key_rule_phase4(text,text,boolean)') IS NULL THEN
        ALTER FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN)
            RENAME TO audit_resource_key_rule_phase4;
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
        WHEN 'resource_type' THEN
            IF p_val = 'report_export' THEN
                RETURN TRUE;
            END IF;
            RETURN public.audit_is_allowed_enum_phase4(p_val, p_enum_name);
        WHEN 'report_export_type' THEN
            RETURN p_val IN ('booking_line_items', 'payment_ledger', 'refund_ledger');
        WHEN 'report_export_format' THEN
            RETURN p_val IN ('csv', 'json');
        ELSE
            RETURN public.audit_is_allowed_enum_phase4(p_val, p_enum_name);
    END CASE;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

CREATE OR REPLACE FUNCTION public.audit_resource_key_rule(
    p_resource_type TEXT,
    p_key           TEXT,
    p_is_metadata   BOOLEAN DEFAULT FALSE
) RETURNS TEXT AS $$
BEGIN
    CASE p_resource_type
        WHEN 'report_export' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'export_type' THEN RETURN 'enum:report_export_type';
                WHEN 'format' THEN RETURN 'enum:report_export_format';
                WHEN 'start_date' THEN RETURN 'date_or_timestamp';
                WHEN 'end_date' THEN RETURN 'date_or_timestamp';
                WHEN 'currency' THEN RETURN 'code_or_empty';
                WHEN 'row_count' THEN RETURN 'int_count';
                WHEN 'created_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;
        ELSE
            RETURN public.audit_resource_key_rule_phase4(p_resource_type, p_key, p_is_metadata);
    END CASE;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

DO $$
BEGIN
    REVOKE ALL ON FUNCTION public.audit_is_allowed_enum_phase4(TEXT, TEXT) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_resource_key_rule_phase4(TEXT, TEXT, BOOLEAN) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_is_allowed_enum(TEXT, TEXT) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN) FROM PUBLIC;

    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
        GRANT EXECUTE ON FUNCTION public.audit_is_allowed_enum_phase4(TEXT, TEXT) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_resource_key_rule_phase4(TEXT, TEXT, BOOLEAN) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_is_allowed_enum(TEXT, TEXT) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN) TO lovelace_app;
    END IF;
END $$;
