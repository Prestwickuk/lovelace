#!/bin/sh
set -eu

# Idempotent bootstrapper for LOVELACE PostgreSQL privilege separation.
# Must be run using bootstrap administrator credentials (never by the app container).
# Safe on both fresh PostgreSQL clusters and existing Phase 1.5/1.6 volumes.
#
# Security: Reads sensitive passwords directly from environment variables inside psql
# via \getenv and binds session parameters silently with \gset so credentials never
# appear in psql process arguments (ps/argv) or PostgreSQL bootstrap stdout/stderr.

DB_NAME="${POSTGRES_DB:-tir_bach}"
BOOTSTRAP_USER="${POSTGRES_BOOTSTRAP_USER:-postgres}"
MIGRATOR_USER="${LOVELACE_MIGRATOR_USER:-lovelace_migrator}"
APP_USER="${LOVELACE_APP_USER:-lovelace_app}"

: "${LOVELACE_MIGRATOR_PASSWORD:?LOVELACE_MIGRATOR_PASSWORD must be set}"
: "${LOVELACE_APP_PASSWORD:?LOVELACE_APP_PASSWORD must be set}"

export LOVELACE_BOOTSTRAP_DB="$DB_NAME"
export LOVELACE_BOOTSTRAP_MIGRATOR_USER="$MIGRATOR_USER"
export LOVELACE_BOOTSTRAP_APP_USER="$APP_USER"

set -- -X -q -v ON_ERROR_STOP=1
if [ -n "${PGHOST:-}" ]; then
  set -- "$@" -h "$PGHOST" -p "${PGPORT:-5432}"
fi

psql "$@" \
  -U "$BOOTSTRAP_USER" \
  -d "$DB_NAME" <<'SQL'
\getenv db_name LOVELACE_BOOTSTRAP_DB
\getenv migrator_user LOVELACE_BOOTSTRAP_MIGRATOR_USER
\getenv migrator_pass LOVELACE_MIGRATOR_PASSWORD
\getenv app_user LOVELACE_BOOTSTRAP_APP_USER
\getenv app_pass LOVELACE_APP_PASSWORD

SELECT
    set_config('lovelace.db_name', :'db_name', false) AS _lovelace_s1,
    set_config('lovelace.migrator_user', :'migrator_user', false) AS _lovelace_s2,
    set_config('lovelace.migrator_pass', :'migrator_pass', false) AS _lovelace_s3,
    set_config('lovelace.app_user', :'app_user', false) AS _lovelace_s4,
    set_config('lovelace.app_pass', :'app_pass', false) AS _lovelace_s5 \gset

\unset migrator_pass
\unset app_pass
\unset _lovelace_s3
\unset _lovelace_s5

CREATE EXTENSION IF NOT EXISTS btree_gist;

DO $$
DECLARE
    v_db        TEXT := current_setting('lovelace.db_name');
    v_migrator  TEXT := current_setting('lovelace.migrator_user');
    v_m_pass    TEXT := current_setting('lovelace.migrator_pass');
    v_app       TEXT := current_setting('lovelace.app_user');
    v_a_pass    TEXT := current_setting('lovelace.app_pass');
    r           RECORD;
BEGIN
    PERFORM set_config('lovelace.migrator_pass', '', false);
    PERFORM set_config('lovelace.app_pass', '', false);

    IF v_m_pass IS NULL OR v_m_pass = '' OR v_a_pass IS NULL OR v_a_pass = '' THEN
        RAISE EXCEPTION 'bootstrap role passwords must not be empty';
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = v_migrator) THEN
        EXECUTE format(
            'CREATE ROLE %I WITH LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT',
            v_migrator, v_m_pass
        );
    ELSE
        EXECUTE format(
            'ALTER ROLE %I WITH LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT',
            v_migrator, v_m_pass
        );
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = v_app) THEN
        EXECUTE format(
            'CREATE ROLE %I WITH LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE',
            v_app, v_a_pass
        );
    ELSE
        EXECUTE format(
            'ALTER ROLE %I WITH LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE',
            v_app, v_a_pass
        );
    END IF;

    -- Lock down public access and assign database / schema permissions explicitly.
    EXECUTE format('REVOKE ALL ON DATABASE %I FROM PUBLIC', v_db);
    EXECUTE format('GRANT CONNECT, TEMP ON DATABASE %I TO %I', v_db, v_migrator);
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO %I', v_db, v_app);

    REVOKE CREATE ON SCHEMA public FROM PUBLIC;
    EXECUTE format('ALTER SCHEMA public OWNER TO %I', v_migrator);
    EXECUTE format('GRANT USAGE, CREATE ON SCHEMA public TO %I', v_migrator);
    EXECUTE format('GRANT USAGE ON SCHEMA public TO %I', v_app);
    EXECUTE format('REVOKE CREATE ON SCHEMA public FROM %I', v_app);

    -- Transfer ownership of pre-existing Phase 1/1.1/1.5/1.6 objects to lovelace_migrator (idempotent).
    FOR r IN SELECT tablename FROM pg_tables WHERE schemaname = 'public' LOOP
        EXECUTE format('ALTER TABLE public.%I OWNER TO %I', r.tablename, v_migrator);
    END LOOP;

    FOR r IN SELECT sequencename FROM pg_sequences WHERE schemaname = 'public' LOOP
        EXECUTE format('ALTER SEQUENCE public.%I OWNER TO %I', r.sequencename, v_migrator);
    END LOOP;

    FOR r IN SELECT viewname FROM pg_views WHERE schemaname = 'public' LOOP
        EXECUTE format('ALTER VIEW public.%I OWNER TO %I', r.viewname, v_migrator);
    END LOOP;

    FOR r IN
        SELECT p.proname, pg_get_function_identity_arguments(p.oid) AS args
        FROM pg_proc p
        WHERE p.pronamespace = 'public'::regnamespace
          AND p.prokind IN ('f', 'p')
          AND NOT EXISTS (
              SELECT 1 FROM pg_depend d
              WHERE d.objid = p.oid AND d.deptype = 'e'
          )
    LOOP
        EXECUTE format('ALTER FUNCTION public.%I(%s) OWNER TO %I', r.proname, r.args, v_migrator);
    END LOOP;

    -- Strictly DML-only permissions for lovelace_app on existing objects.
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %I', v_app);
    EXECUTE format('REVOKE TRUNCATE, REFERENCES, TRIGGER ON ALL TABLES IN SCHEMA public FROM %I', v_app);
    EXECUTE format('GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO %I', v_app);

    IF to_regclass('public.schema_migrations') IS NOT NULL THEN
        EXECUTE 'REVOKE ALL ON TABLE public.schema_migrations FROM PUBLIC';
        EXECUTE format('REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON TABLE public.schema_migrations FROM %I', v_app);
        EXECUTE format('GRANT SELECT ON TABLE public.schema_migrations TO %I', v_app);
    END IF;
    IF to_regclass('public.property_branding_settings') IS NOT NULL THEN
        EXECUTE 'REVOKE ALL ON TABLE public.property_branding_settings FROM PUBLIC';
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.property_branding_settings TO %I', v_app);
    END IF;
    IF to_regclass('public.guest_portal_identities') IS NOT NULL THEN
        EXECUTE 'REVOKE ALL ON TABLE public.guest_portal_identities FROM PUBLIC';
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.guest_portal_identities TO %I', v_app);
    END IF;
    IF to_regclass('public.guest_portal_challenges') IS NOT NULL THEN
        EXECUTE 'REVOKE ALL ON TABLE public.guest_portal_challenges FROM PUBLIC';
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.guest_portal_challenges TO %I', v_app);
    END IF;
    IF to_regclass('public.guest_portal_sessions') IS NOT NULL THEN
        EXECUTE 'REVOKE ALL ON TABLE public.guest_portal_sessions FROM PUBLIC';
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.guest_portal_sessions TO %I', v_app);
    END IF;
    IF to_regclass('public.admin_recovery_codes') IS NOT NULL THEN
        EXECUTE 'REVOKE ALL ON TABLE public.admin_recovery_codes FROM PUBLIC';
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.admin_recovery_codes TO %I', v_app);
    END IF;
    IF to_regclass('public.backup_storage_targets') IS NOT NULL THEN
        EXECUTE 'REVOKE ALL ON TABLE public.backup_storage_targets FROM PUBLIC';
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.backup_storage_targets TO %I', v_app);
    END IF;
    IF to_regclass('public.backup_runs') IS NOT NULL THEN
        EXECUTE 'REVOKE ALL ON TABLE public.backup_runs FROM PUBLIC';
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.backup_runs TO %I', v_app);
    END IF;

    -- Harden append-only audit and redaction overlay privileges idempotently across reruns.
    IF to_regclass('public.audit_logs') IS NOT NULL THEN
        EXECUTE format('REVOKE ALL ON TABLE public.audit_logs FROM PUBLIC, %I', v_app);
        EXECUTE format('GRANT INSERT ON TABLE public.audit_logs TO %I', v_app);
    END IF;
    IF to_regclass('public.audit_logs_id_seq') IS NOT NULL THEN
        EXECUTE format('GRANT USAGE, SELECT ON SEQUENCE public.audit_logs_id_seq TO %I', v_app);
    END IF;
    IF to_regclass('public.audit_logs_effective') IS NOT NULL THEN
        EXECUTE format('GRANT SELECT ON TABLE public.audit_logs_effective TO %I', v_app);
    END IF;
    IF to_regclass('public.audit_log_redactions') IS NOT NULL THEN
        EXECUTE format('REVOKE ALL ON TABLE public.audit_log_redactions FROM PUBLIC, %I', v_app);
    END IF;
    IF to_regclass('public.guest_consent_redactions') IS NOT NULL THEN
        EXECUTE format('REVOKE ALL ON TABLE public.guest_consent_redactions FROM PUBLIC, %I', v_app);
    END IF;
    IF to_regclass('public.guest_consent_records') IS NOT NULL THEN
        EXECUTE format('REVOKE ALL ON TABLE public.guest_consent_records FROM PUBLIC, %I', v_app);
        EXECUTE format('GRANT SELECT, INSERT ON TABLE public.guest_consent_records TO %I', v_app);
        EXECUTE format('GRANT UPDATE (guest_id) ON TABLE public.guest_consent_records TO %I', v_app);
    END IF;
    IF to_regclass('public.guest_consent_records_effective') IS NOT NULL THEN
        EXECUTE format('GRANT SELECT ON TABLE public.guest_consent_records_effective TO %I', v_app);
    END IF;
    IF to_regclass('public.unit_status_history') IS NOT NULL THEN
        EXECUTE format('REVOKE ALL ON TABLE public.unit_status_history FROM PUBLIC, %I', v_app);
        EXECUTE format('GRANT SELECT, INSERT ON TABLE public.unit_status_history TO %I', v_app);
    END IF;
    IF to_regclass('public.inventory_history_gaps') IS NOT NULL THEN
        EXECUTE format('REVOKE ALL ON TABLE public.inventory_history_gaps FROM PUBLIC, %I', v_app);
        EXECUTE format('GRANT SELECT ON TABLE public.inventory_history_gaps TO %I', v_app);
    END IF;
    IF to_regprocedure('public.redact_audit_log_entry(uuid,bigint,jsonb,jsonb,jsonb,text,uuid)') IS NOT NULL THEN
        EXECUTE 'REVOKE ALL ON FUNCTION public.redact_audit_log_entry(UUID, BIGINT, JSONB, JSONB, JSONB, TEXT, UUID) FROM PUBLIC';
        EXECUTE format('GRANT EXECUTE ON FUNCTION public.redact_audit_log_entry(UUID, BIGINT, JSONB, JSONB, JSONB, TEXT, UUID) TO %I', v_app);
    END IF;
    IF to_regprocedure('public.redact_guest_consent_note(uuid,uuid,text,text,uuid)') IS NOT NULL THEN
        EXECUTE 'REVOKE ALL ON FUNCTION public.redact_guest_consent_note(UUID, UUID, TEXT, TEXT, UUID) FROM PUBLIC';
        EXECUTE format('GRANT EXECUTE ON FUNCTION public.redact_guest_consent_note(UUID, UUID, TEXT, TEXT, UUID) TO %I', v_app);
    END IF;

    -- Default privileges so future migrations grant baseline DML without exposing restricted redaction tables.
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %I', v_migrator, v_app);
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO %I', v_migrator, v_app);
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public GRANT EXECUTE ON FUNCTIONS TO %I', v_migrator, v_app);
END $$;
SQL
