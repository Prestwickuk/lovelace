-- Phase 4: Live OTA and Channel Distribution
-- Providers: beds24, smoobu, lodgify, channex
-- Additive migration preserving all Phase 1-3 tables, constraints, audit triggers,
-- encrypted credential storage, and least-privilege role separation.

-- 1. Extend channel_connections with live verification, capability discovery,
--    token expiry tracking, sync health telemetry, and opaque webhook routing token.
ALTER TABLE channel_connections
    ADD COLUMN IF NOT EXISTS capabilities JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS capability_statuses JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS token_expires_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS last_verified_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS last_synced_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS last_sync_error_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS webhook_endpoint_token TEXT NOT NULL DEFAULT '';

-- 2. Explicit property-scoped provider listing mappings
CREATE TABLE IF NOT EXISTS channel_listing_mappings (
    id                      UUID PRIMARY KEY,
    property_id             UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    channel_connection_id   UUID NOT NULL REFERENCES channel_connections(id) ON DELETE CASCADE,
    provider_code           TEXT NOT NULL CHECK (provider_code IN ('beds24', 'smoobu', 'lodgify', 'channex')),
    external_property_id    TEXT NOT NULL DEFAULT '',
    external_listing_id     TEXT NOT NULL CHECK (char_length(trim(external_listing_id)) > 0),
    external_rate_plan_id   TEXT NOT NULL DEFAULT '',
    external_name           TEXT NOT NULL DEFAULT '',
    unit_type_id            UUID NOT NULL REFERENCES unit_types(id) ON DELETE RESTRICT,
    unit_id                 UUID NULL REFERENCES units(id) ON DELETE RESTRICT,
    rate_plan_id            UUID NULL REFERENCES rate_plans(id) ON DELETE RESTRICT,
    booking_channel_code    TEXT NOT NULL CHECK (booking_channel_code ~ '^[A-Z0-9_]+$'),
    currency                CHAR(3) NOT NULL DEFAULT 'GBP' CHECK (currency ~ '^[A-Z]{3}$'),
    timezone                TEXT NOT NULL DEFAULT 'Europe/London',
    sync_availability       BOOLEAN NOT NULL DEFAULT TRUE,
    sync_rates              BOOLEAN NOT NULL DEFAULT TRUE,
    sync_reservations       BOOLEAN NOT NULL DEFAULT TRUE,
    ownership_mode          TEXT NOT NULL DEFAULT 'LOVELACE_ARI_PROVIDER_BOOKINGS'
                                CHECK (ownership_mode IN ('LOVELACE_ARI_PROVIDER_BOOKINGS', 'READ_ONLY_IMPORT')),
    is_active               BOOLEAN NOT NULL DEFAULT TRUE,
    version                 INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_by              UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    updated_by              UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, provider_code, external_listing_id, external_rate_plan_id)
);

CREATE INDEX IF NOT EXISTS idx_channel_listing_mappings_conn_active
    ON channel_listing_mappings (channel_connection_id, is_active);

CREATE INDEX IF NOT EXISTS idx_channel_listing_mappings_unit_type
    ON channel_listing_mappings (property_id, unit_type_id)
    WHERE is_active = TRUE;

-- Enforce cross-property and parent/child ownership invariants at PostgreSQL level.
CREATE OR REPLACE FUNCTION public.validate_channel_listing_mapping_ownership()
RETURNS TRIGGER AS $$
DECLARE
    v_conn_prop     UUID;
    v_conn_provider TEXT;
    v_ut_prop       UUID;
    v_u_prop        UUID;
    v_u_ut          UUID;
    v_rp_prop       UUID;
    v_chan_active   BOOLEAN;
BEGIN
    SELECT property_id, provider_code INTO v_conn_prop, v_conn_provider
    FROM public.channel_connections
    WHERE id = NEW.channel_connection_id;
    IF v_conn_prop IS NULL THEN
        RAISE EXCEPTION 'channel_connection_id does not exist' USING ERRCODE = '23503';
    END IF;
    IF v_conn_prop <> NEW.property_id OR v_conn_provider <> NEW.provider_code THEN
        RAISE EXCEPTION 'cross-property or provider mismatch on channel_listing_mappings' USING ERRCODE = '22023';
    END IF;

    SELECT property_id INTO v_ut_prop
    FROM public.unit_types
    WHERE id = NEW.unit_type_id AND archived_at IS NULL;
    IF v_ut_prop IS NULL OR v_ut_prop <> NEW.property_id THEN
        RAISE EXCEPTION 'unit_type_id must belong to property_id' USING ERRCODE = '22023';
    END IF;

    IF NEW.unit_id IS NOT NULL THEN
        SELECT property_id, unit_type_id INTO v_u_prop, v_u_ut
        FROM public.units
        WHERE id = NEW.unit_id AND archived_at IS NULL;
        IF v_u_prop IS NULL OR v_u_prop <> NEW.property_id OR v_u_ut <> NEW.unit_type_id THEN
            RAISE EXCEPTION 'unit_id must belong to property_id and unit_type_id' USING ERRCODE = '22023';
        END IF;
    END IF;

    IF NEW.rate_plan_id IS NOT NULL THEN
        SELECT property_id INTO v_rp_prop
        FROM public.rate_plans
        WHERE id = NEW.rate_plan_id AND archived_at IS NULL;
        IF v_rp_prop IS NULL OR v_rp_prop <> NEW.property_id THEN
            RAISE EXCEPTION 'rate_plan_id must belong to property_id' USING ERRCODE = '22023';
        END IF;
    END IF;

    SELECT is_active INTO v_chan_active
    FROM public.booking_channels
    WHERE property_id = NEW.property_id AND code = NEW.booking_channel_code;
    IF COALESCE(v_chan_active, FALSE) = FALSE THEN
        RAISE EXCEPTION 'booking_channel_code must exist and be active for property_id' USING ERRCODE = '22023';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql SET search_path = pg_catalog, public;

DROP TRIGGER IF EXISTS trg_validate_channel_listing_mapping ON channel_listing_mappings;
CREATE TRIGGER trg_validate_channel_listing_mapping
    BEFORE INSERT OR UPDATE ON channel_listing_mappings
    FOR EACH ROW
    EXECUTE FUNCTION public.validate_channel_listing_mapping_ownership();

-- 3. Durable channel sync job queue with worker leases and monotonic fencing epochs
CREATE TABLE IF NOT EXISTS channel_sync_jobs (
    id                      UUID PRIMARY KEY,
    property_id             UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    channel_connection_id   UUID NOT NULL REFERENCES channel_connections(id) ON DELETE CASCADE,
    listing_mapping_id      UUID NULL REFERENCES channel_listing_mappings(id) ON DELETE SET NULL,
    provider_code           TEXT NOT NULL CHECK (provider_code IN ('beds24', 'smoobu', 'lodgify', 'channex')),
    job_type                TEXT NOT NULL CHECK (job_type IN (
                                'VERIFY_CONNECTION',
                                'DISCOVER_LISTINGS',
                                'PUSH_AVAILABILITY',
                                'PUSH_RATES',
                                'PULL_RESERVATIONS',
                                'PROCESS_WEBHOOK_EVENT'
                            )),
    idempotency_key         TEXT NOT NULL CHECK (char_length(trim(idempotency_key)) > 0),
    scope_payload           JSONB NOT NULL DEFAULT '{}'::jsonb,
    status                  TEXT NOT NULL CHECK (status IN (
                                'PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED_RETRYABLE', 'FAILED_TERMINAL', 'CANCELLED'
                            )),
    attempts                INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts            INTEGER NOT NULL DEFAULT 5 CHECK (max_attempts >= 1),
    next_attempt_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    worker_id               TEXT NOT NULL DEFAULT '',
    lease_token             UUID NULL,
    lease_expires_at        TIMESTAMPTZ NULL,
    fence_epoch             BIGINT NOT NULL DEFAULT 0 CHECK (fence_epoch >= 0),
    last_error_code         TEXT NOT NULL DEFAULT '',
    last_error_summary      TEXT NOT NULL DEFAULT '',
    started_at              TIMESTAMPTZ NULL,
    completed_at            TIMESTAMPTZ NULL,
    created_by              UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, provider_code, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_channel_sync_jobs_due
    ON channel_sync_jobs (status, next_attempt_at)
    WHERE status IN ('PENDING', 'FAILED_RETRYABLE');

CREATE INDEX IF NOT EXISTS idx_channel_sync_jobs_running_lease
    ON channel_sync_jobs (status, lease_expires_at)
    WHERE status = 'RUNNING';

CREATE INDEX IF NOT EXISTS idx_channel_sync_jobs_property_created
    ON channel_sync_jobs (property_id, created_at DESC);

-- 4. Durable incremental sync checkpoints & outbound payload digests
CREATE TABLE IF NOT EXISTS channel_sync_checkpoints (
    id                      UUID PRIMARY KEY,
    property_id             UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    channel_connection_id   UUID NOT NULL REFERENCES channel_connections(id) ON DELETE CASCADE,
    provider_code           TEXT NOT NULL CHECK (provider_code IN ('beds24', 'smoobu', 'lodgify', 'channex')),
    cursor_key              TEXT NOT NULL CHECK (char_length(trim(cursor_key)) > 0),
    cursor_value            TEXT NOT NULL DEFAULT '',
    payload_digest          TEXT NOT NULL DEFAULT '',
    last_synced_at          TIMESTAMPTZ NULL,
    metadata                JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (channel_connection_id, cursor_key)
);

-- 5. Idempotent external reservation ledger (PII-free)
CREATE TABLE IF NOT EXISTS channel_external_reservations (
    id                      UUID PRIMARY KEY,
    property_id             UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    channel_connection_id   UUID NOT NULL REFERENCES channel_connections(id) ON DELETE CASCADE,
    listing_mapping_id      UUID NULL REFERENCES channel_listing_mappings(id) ON DELETE SET NULL,
    provider_code           TEXT NOT NULL CHECK (provider_code IN ('beds24', 'smoobu', 'lodgify', 'channex')),
    external_reservation_id TEXT NOT NULL CHECK (char_length(trim(external_reservation_id)) > 0),
    external_revision       TEXT NOT NULL DEFAULT '',
    external_updated_at     TIMESTAMPTZ NULL,
    reservation_id          UUID NULL REFERENCES reservations(id) ON DELETE SET NULL,
    import_status           TEXT NOT NULL CHECK (import_status IN (
                                'IMPORTED', 'UPDATED', 'CANCELLED', 'CONFLICT_OVERBOOKED', 'UNMAPPED', 'FAILED'
                            )),
    conflict_reason         TEXT NOT NULL DEFAULT '',
    payload_digest          TEXT NOT NULL DEFAULT '',
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, provider_code, external_reservation_id)
);

CREATE INDEX IF NOT EXISTS idx_channel_ext_res_reservation
    ON channel_external_reservations (reservation_id)
    WHERE reservation_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_channel_ext_res_status
    ON channel_external_reservations (property_id, import_status, updated_at DESC);

-- 6. Inbound webhook event deduplication & PII-free queue summary
CREATE TABLE IF NOT EXISTS channel_webhook_events (
    id                      UUID PRIMARY KEY,
    property_id             UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    channel_connection_id   UUID NOT NULL REFERENCES channel_connections(id) ON DELETE CASCADE,
    provider_code           TEXT NOT NULL CHECK (provider_code IN ('beds24', 'smoobu', 'lodgify', 'channex')),
    external_event_id       TEXT NOT NULL CHECK (char_length(trim(external_event_id)) > 0),
    event_type              TEXT NOT NULL CHECK (char_length(trim(event_type)) > 0),
    signature_verified      BOOLEAN NOT NULL DEFAULT FALSE,
    status                  TEXT NOT NULL CHECK (status IN (
                                'RECEIVED', 'QUEUED', 'PROCESSED', 'CONFLICT', 'IGNORED', 'FAILED'
                            )),
    safe_summary            JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_summary           TEXT NOT NULL DEFAULT '',
    received_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at            TIMESTAMPTZ NULL,
    UNIQUE (property_id, provider_code, external_event_id)
);

CREATE INDEX IF NOT EXISTS idx_channel_webhook_events_conn_received
    ON channel_webhook_events (channel_connection_id, received_at DESC);

-- 7. Single-use HMAC nonce replay store
CREATE TABLE IF NOT EXISTS channel_hmac_nonces (
    provider_code   TEXT NOT NULL CHECK (provider_code IN ('beds24', 'smoobu', 'lodgify', 'channex')),
    nonce           TEXT NOT NULL CHECK (char_length(trim(nonce)) > 0),
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider_code, nonce)
);

CREATE INDEX IF NOT EXISTS idx_channel_hmac_nonces_expires
    ON channel_hmac_nonces (expires_at);

-- 8. Extend Phase 3.3.2 strict audit redaction allowlist cleanly for Phase 4 resource types
DO $$
BEGIN
    IF to_regprocedure('public.audit_is_allowed_enum_phase3(text,text)') IS NULL THEN
        ALTER FUNCTION public.audit_is_allowed_enum(TEXT, TEXT)
            RENAME TO audit_is_allowed_enum_phase3;
    END IF;
    IF to_regprocedure('public.audit_resource_key_rule_phase3(text,text,boolean)') IS NULL THEN
        ALTER FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN)
            RENAME TO audit_resource_key_rule_phase3;
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
            IF p_val IN (
                'channel_listing_mapping',
                'channel_sync_job',
                'channel_webhook_event',
                'channel_external_reservation'
            ) THEN
                RETURN TRUE;
            END IF;
            RETURN public.audit_is_allowed_enum_phase3(p_val, p_enum_name);
        WHEN 'channel_ownership_mode' THEN
            RETURN p_val IN ('LOVELACE_ARI_PROVIDER_BOOKINGS', 'READ_ONLY_IMPORT');
        WHEN 'channel_job_type' THEN
            RETURN p_val IN (
                'VERIFY_CONNECTION', 'DISCOVER_LISTINGS', 'PUSH_AVAILABILITY',
                'PUSH_RATES', 'PULL_RESERVATIONS', 'PROCESS_WEBHOOK_EVENT'
            );
        WHEN 'channel_job_status' THEN
            RETURN p_val IN ('PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED_RETRYABLE', 'FAILED_TERMINAL', 'CANCELLED');
        WHEN 'channel_import_status' THEN
            RETURN p_val IN ('IMPORTED', 'UPDATED', 'CANCELLED', 'CONFLICT_OVERBOOKED', 'UNMAPPED', 'FAILED');
        WHEN 'channel_webhook_status' THEN
            RETURN p_val IN ('RECEIVED', 'QUEUED', 'PROCESSED', 'CONFLICT', 'IGNORED', 'FAILED');
        ELSE
            RETURN public.audit_is_allowed_enum_phase3(p_val, p_enum_name);
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
        WHEN 'channel_connection' THEN
            CASE p_key
                WHEN 'token_expires_at' THEN RETURN 'timestamp_or_null';
                WHEN 'last_verified_at' THEN RETURN 'timestamp_or_null';
                WHEN 'last_synced_at' THEN RETURN 'timestamp_or_null';
                WHEN 'last_sync_error_code' THEN RETURN 'code_or_empty';
                ELSE RETURN public.audit_resource_key_rule_phase3(p_resource_type, p_key, p_is_metadata);
            END CASE;

        WHEN 'channel_listing_mapping' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'channel_connection_id' THEN RETURN 'uuid';
                WHEN 'provider_code' THEN RETURN 'code';
                WHEN 'external_property_id' THEN RETURN 'code_or_empty';
                WHEN 'external_listing_id' THEN RETURN 'code';
                WHEN 'external_rate_plan_id' THEN RETURN 'code_or_empty';
                WHEN 'unit_type_id' THEN RETURN 'uuid';
                WHEN 'unit_id' THEN RETURN 'uuid_or_null';
                WHEN 'rate_plan_id' THEN RETURN 'uuid_or_null';
                WHEN 'booking_channel_code' THEN RETURN 'code';
                WHEN 'currency' THEN RETURN 'currency';
                WHEN 'timezone' THEN RETURN 'timezone';
                WHEN 'sync_availability' THEN RETURN 'bool';
                WHEN 'sync_rates' THEN RETURN 'bool';
                WHEN 'sync_reservations' THEN RETURN 'bool';
                WHEN 'ownership_mode' THEN RETURN 'enum:channel_ownership_mode';
                WHEN 'is_active' THEN RETURN 'bool';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'channel_sync_job' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'channel_connection_id' THEN RETURN 'uuid';
                WHEN 'listing_mapping_id' THEN RETURN 'uuid_or_null';
                WHEN 'provider_code' THEN RETURN 'code';
                WHEN 'job_type' THEN RETURN 'enum:channel_job_type';
                WHEN 'status' THEN RETURN 'enum:channel_job_status';
                WHEN 'attempts' THEN RETURN 'int_count';
                WHEN 'max_attempts' THEN RETURN 'int_count';
                WHEN 'fence_epoch' THEN RETURN 'int_count';
                WHEN 'last_error_code' THEN RETURN 'code_or_empty';
                WHEN 'started_at' THEN RETURN 'timestamp_or_null';
                WHEN 'completed_at' THEN RETURN 'timestamp_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'channel_webhook_event' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'channel_connection_id' THEN RETURN 'uuid';
                WHEN 'provider_code' THEN RETURN 'code';
                WHEN 'external_event_id' THEN RETURN 'code';
                WHEN 'event_type' THEN RETURN 'code';
                WHEN 'signature_verified' THEN RETURN 'bool';
                WHEN 'status' THEN RETURN 'enum:channel_webhook_status';
                WHEN 'received_at' THEN RETURN 'timestamp';
                WHEN 'processed_at' THEN RETURN 'timestamp_or_null';
                ELSE RETURN NULL;
            END CASE;

        WHEN 'channel_external_reservation' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'channel_connection_id' THEN RETURN 'uuid';
                WHEN 'listing_mapping_id' THEN RETURN 'uuid_or_null';
                WHEN 'provider_code' THEN RETURN 'code';
                WHEN 'external_reservation_id' THEN RETURN 'code';
                WHEN 'external_revision' THEN RETURN 'code_or_empty';
                WHEN 'reservation_id' THEN RETURN 'uuid_or_null';
                WHEN 'import_status' THEN RETURN 'enum:channel_import_status';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;

        ELSE
            RETURN public.audit_resource_key_rule_phase3(p_resource_type, p_key, p_is_metadata);
    END CASE;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

-- 9. Explicit least-privilege role grants for lovelace_app
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE
            public.channel_listing_mappings,
            public.channel_sync_jobs,
            public.channel_sync_checkpoints,
            public.channel_external_reservations,
            public.channel_webhook_events,
            public.channel_hmac_nonces
        TO lovelace_app;
        REVOKE TRUNCATE, REFERENCES, TRIGGER ON TABLE
            public.channel_listing_mappings,
            public.channel_sync_jobs,
            public.channel_sync_checkpoints,
            public.channel_external_reservations,
            public.channel_webhook_events,
            public.channel_hmac_nonces
        FROM lovelace_app;
    END IF;

    REVOKE ALL ON FUNCTION public.audit_is_allowed_enum_phase3(TEXT, TEXT) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_resource_key_rule_phase3(TEXT, TEXT, BOOLEAN) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_is_allowed_enum(TEXT, TEXT) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN) FROM PUBLIC;

    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
        GRANT EXECUTE ON FUNCTION public.audit_is_allowed_enum_phase3(TEXT, TEXT) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_resource_key_rule_phase3(TEXT, TEXT, BOOLEAN) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_is_allowed_enum(TEXT, TEXT) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN) TO lovelace_app;
    END IF;
END $$;
