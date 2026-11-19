-- 0031_backup_targets_and_runs.sql
-- Portable encrypted data backup targets (AWS S3, S3-compatible R2/B2, Google Cloud Storage, Azure Blob),
-- backup/restore execution history, least-privilege role grants, and 1:1 Go/PostgreSQL audit allowlist parity.

CREATE TABLE IF NOT EXISTS public.backup_storage_targets (
    id                      UUID PRIMARY KEY,
    name                    TEXT NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 80),
    provider                TEXT NOT NULL CHECK (provider IN ('AWS_S3', 'S3_COMPATIBLE', 'GCS', 'AZURE_BLOB')),
    bucket_or_container     TEXT NOT NULL CHECK (char_length(btrim(bucket_or_container)) BETWEEN 1 AND 128),
    region                  TEXT NOT NULL DEFAULT '',
    endpoint_url            TEXT NOT NULL DEFAULT '',
    key_prefix              TEXT NOT NULL DEFAULT 'lovelace-backups/',
    encrypted_credentials   BYTEA NOT NULL,
    credential_hint         TEXT NOT NULL DEFAULT '',
    encrypt_archive         BOOLEAN NOT NULL DEFAULT TRUE,
    schedule_enabled        BOOLEAN NOT NULL DEFAULT FALSE,
    schedule_interval_hours INTEGER NOT NULL DEFAULT 24 CHECK (schedule_interval_hours BETWEEN 1 AND 720),
    retention_count         INTEGER NOT NULL DEFAULT 14 CHECK (retention_count BETWEEN 1 AND 365),
    is_active               BOOLEAN NOT NULL DEFAULT TRUE,
    last_verified_at        TIMESTAMPTZ NULL,
    last_backup_at          TIMESTAMPTZ NULL,
    last_status             TEXT NOT NULL DEFAULT 'NEVER_RUN' CHECK (last_status IN ('NEVER_RUN', 'VERIFIED', 'SUCCEEDED', 'FAILED')),
    last_error_summary      TEXT NOT NULL DEFAULT '',
    next_run_at             TIMESTAMPTZ NULL,
    created_by              UUID NULL REFERENCES public.admin_users(id) ON DELETE SET NULL,
    updated_by              UUID NULL REFERENCES public.admin_users(id) ON DELETE SET NULL,
    version                 INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_backup_storage_targets_due
    ON public.backup_storage_targets (next_run_at)
    WHERE is_active = TRUE AND schedule_enabled = TRUE;

CREATE TABLE IF NOT EXISTS public.backup_runs (
    id               UUID PRIMARY KEY,
    target_id        UUID NULL REFERENCES public.backup_storage_targets(id) ON DELETE SET NULL,
    run_kind         TEXT NOT NULL CHECK (run_kind IN ('LOCAL_DOWNLOAD', 'CLOUD_MANUAL', 'CLOUD_SCHEDULED', 'VERIFY_ARCHIVE', 'RESTORE_EMPTY_DB')),
    provider         TEXT NOT NULL CHECK (provider IN ('LOCAL_ZIP', 'AWS_S3', 'S3_COMPATIBLE', 'GCS', 'AZURE_BLOB')),
    status           TEXT NOT NULL CHECK (status IN ('RUNNING', 'SUCCEEDED', 'FAILED')),
    encrypted        BOOLEAN NOT NULL DEFAULT FALSE,
    object_key       TEXT NOT NULL DEFAULT '',
    archive_sha256   TEXT NOT NULL DEFAULT '',
    size_bytes       BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    table_count      INTEGER NOT NULL DEFAULT 0 CHECK (table_count >= 0),
    row_count        BIGINT NOT NULL DEFAULT 0 CHECK (row_count >= 0),
    attachment_count INTEGER NOT NULL DEFAULT 0 CHECK (attachment_count >= 0),
    error_summary    TEXT NOT NULL DEFAULT '',
    triggered_by     UUID NULL REFERENCES public.admin_users(id) ON DELETE SET NULL,
    started_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at     TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS idx_backup_runs_started_at
    ON public.backup_runs (started_at DESC);

DO $$
BEGIN
    REVOKE ALL ON TABLE public.backup_storage_targets FROM PUBLIC;
    REVOKE ALL ON TABLE public.backup_runs FROM PUBLIC;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.backup_storage_targets TO lovelace_app;
        GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.backup_runs TO lovelace_app;
    END IF;
END $$;

-- Extend database-enforced audit redaction allowlists 1:1 with internal/service/audit_sanitizer.go,
-- delegating all prior resource types and enums through _pre_backups wrappers.
DO $$
BEGIN
    IF to_regprocedure('public.audit_is_allowed_enum_pre_backups(text,text)') IS NULL THEN
        ALTER FUNCTION public.audit_is_allowed_enum(TEXT, TEXT)
            RENAME TO audit_is_allowed_enum_pre_backups;
    END IF;
    IF to_regprocedure('public.audit_resource_key_rule_pre_backups(text,text,boolean)') IS NULL THEN
        ALTER FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN)
            RENAME TO audit_resource_key_rule_pre_backups;
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
            IF p_val IN ('backup_target', 'backup_run') THEN
                RETURN TRUE;
            END IF;
            RETURN public.audit_is_allowed_enum_pre_backups(p_val, p_enum_name);
        WHEN 'backup_provider' THEN
            RETURN p_val IN ('LOCAL_ZIP', 'AWS_S3', 'S3_COMPATIBLE', 'GCS', 'AZURE_BLOB');
        WHEN 'backup_target_status' THEN
            RETURN p_val IN ('NEVER_RUN', 'VERIFIED', 'SUCCEEDED', 'FAILED');
        WHEN 'backup_run_kind' THEN
            RETURN p_val IN ('LOCAL_DOWNLOAD', 'CLOUD_MANUAL', 'CLOUD_SCHEDULED', 'VERIFY_ARCHIVE', 'RESTORE_EMPTY_DB');
        WHEN 'backup_run_status' THEN
            RETURN p_val IN ('RUNNING', 'SUCCEEDED', 'FAILED');
        ELSE
            RETURN public.audit_is_allowed_enum_pre_backups(p_val, p_enum_name);
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
        WHEN 'backup_target' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'provider' THEN RETURN 'enum:backup_provider';
                WHEN 'encrypt_archive' THEN RETURN 'bool';
                WHEN 'schedule_enabled' THEN RETURN 'bool';
                WHEN 'schedule_interval_hours' THEN RETURN 'int_count';
                WHEN 'retention_count' THEN RETURN 'int_days_1_365';
                WHEN 'is_active' THEN RETURN 'bool';
                WHEN 'last_status' THEN RETURN 'enum:backup_target_status';
                WHEN 'last_verified_at' THEN RETURN 'timestamp_or_null';
                WHEN 'last_backup_at' THEN RETURN 'timestamp_or_null';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;
        WHEN 'backup_run' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'target_id' THEN RETURN 'uuid_or_null';
                WHEN 'run_kind' THEN RETURN 'enum:backup_run_kind';
                WHEN 'provider' THEN RETURN 'enum:backup_provider';
                WHEN 'status' THEN RETURN 'enum:backup_run_status';
                WHEN 'encrypted' THEN RETURN 'bool';
                WHEN 'archive_sha256' THEN RETURN 'sha256';
                WHEN 'size_bytes' THEN RETURN 'int_size_bytes';
                WHEN 'table_count' THEN RETURN 'int_count';
                WHEN 'row_count' THEN RETURN 'int_count';
                WHEN 'attachment_count' THEN RETURN 'int_count';
                WHEN 'started_at' THEN RETURN 'timestamp';
                WHEN 'completed_at' THEN RETURN 'timestamp_or_null';
                ELSE RETURN NULL;
            END CASE;
        ELSE
            RETURN public.audit_resource_key_rule_pre_backups(p_resource_type, p_key, p_is_metadata);
    END CASE;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;
