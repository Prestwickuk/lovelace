-- 0026_phase3_m4_guest_portal.sql
-- Phase 3 Milestone 4: Self-Service Public Guest Portal (Owner-Approved D1–D10 & Section 7.2 Gates).
-- Additive per-property guest portal identities, single-use hashed OTP/magic-link challenges,
-- opaque public sessions (zero IP/user-agent telemetry hashes per Section 7.2), reservation
-- arrival fields, booking policy portal flag, least-privilege grants, and 1:1 Go/PostgreSQL
-- audit allowlist extensions.

-- 1. Feature flag on property_booking_policies (default FALSE: disabled by default).
ALTER TABLE public.property_booking_policies
    ADD COLUMN IF NOT EXISTS guest_portal_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- 2. Customer-editable arrival ETA & arrival notes on reservations (distinct from internal_notes).
ALTER TABLE public.reservations
    ADD COLUMN IF NOT EXISTS estimated_arrival_time TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS guest_arrival_notes    TEXT NOT NULL DEFAULT '';

-- 3. Extend guest_enquiries.source CHECK constraint to allow 'PORTAL'.
DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'public.guest_enquiries'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) ILIKE '%source%'
    LOOP
        EXECUTE format('ALTER TABLE public.guest_enquiries DROP CONSTRAINT %I', c.conname);
    END LOOP;

    ALTER TABLE public.guest_enquiries
        ADD CONSTRAINT guest_enquiries_source_check
        CHECK (source IN ('PHONE', 'IN_PERSON', 'EMAIL', 'WEB', 'ADMIN', 'PORTAL'));
END $$;

-- 4. Per-property verified Guest Portal Identities (strictly isolated per property_id + canonical_guest_id).
CREATE TABLE IF NOT EXISTS public.guest_portal_identities (
    id                        UUID PRIMARY KEY,
    property_id               UUID NOT NULL REFERENCES public.properties(id) ON DELETE RESTRICT,
    canonical_guest_id        UUID NOT NULL,
    verified_email            TEXT NOT NULL CHECK (char_length(trim(verified_email)) > 0),
    normalized_verified_email TEXT NOT NULL CHECK (char_length(trim(normalized_verified_email)) > 0),
    pending_email             TEXT NOT NULL DEFAULT '',
    pending_normalized_email  TEXT NOT NULL DEFAULT '',
    pending_email_expires_at  TIMESTAMPTZ NULL,
    email_verified_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at             TIMESTAMPTZ NULL,
    status                    TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'SUSPENDED')),
    version                   INT NOT NULL DEFAULT 1,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, id),
    UNIQUE (property_id, canonical_guest_id),
    UNIQUE (property_id, normalized_verified_email),
    CONSTRAINT fk_portal_identities_property_guest
        FOREIGN KEY (property_id, canonical_guest_id)
        REFERENCES public.guests (property_id, id)
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_guest_portal_identities_property_guest
    ON public.guest_portal_identities (property_id, canonical_guest_id);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger WHERE tgname = 'trg_guest_portal_identities_updated_at'
    ) THEN
        CREATE TRIGGER trg_guest_portal_identities_updated_at
            BEFORE UPDATE ON public.guest_portal_identities
            FOR EACH ROW EXECUTE FUNCTION public.touch_updated_at();
    END IF;
END $$;

-- 5. Single-use hashed OTP & POST-redeemed verification challenges (zero plaintext tokens or IP/UA hashes).
CREATE TABLE IF NOT EXISTS public.guest_portal_challenges (
    id                 UUID PRIMARY KEY,
    property_id        UUID NOT NULL REFERENCES public.properties(id) ON DELETE RESTRICT,
    portal_identity_id UUID NULL,
    guest_id           UUID NULL,
    reservation_id     UUID NULL,
    normalized_email   TEXT NOT NULL CHECK (char_length(trim(normalized_email)) > 0),
    purpose            TEXT NOT NULL CHECK (purpose IN ('LOGIN', 'VERIFY_EMAIL', 'STEP_UP')),
    otp_hash           TEXT NOT NULL CHECK (char_length(otp_hash) = 64),
    token_hash         TEXT NOT NULL UNIQUE CHECK (char_length(token_hash) = 64),
    browser_nonce_hash TEXT NOT NULL DEFAULT '',
    booking_reference  TEXT NOT NULL DEFAULT '',
    failed_attempts    INT NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
    max_attempts       INT NOT NULL DEFAULT 5 CHECK (max_attempts >= 1 AND max_attempts <= 10),
    expires_at         TIMESTAMPTZ NOT NULL,
    consumed_at        TIMESTAMPTZ NULL,
    invalidated_at     TIMESTAMPTZ NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_portal_challenges_property_identity
        FOREIGN KEY (property_id, portal_identity_id)
        REFERENCES public.guest_portal_identities (property_id, id)
        ON DELETE CASCADE,
    CONSTRAINT fk_portal_challenges_property_guest
        FOREIGN KEY (property_id, guest_id)
        REFERENCES public.guests (property_id, id)
        ON DELETE CASCADE,
    CONSTRAINT fk_portal_challenges_property_reservation
        FOREIGN KEY (property_id, reservation_id)
        REFERENCES public.reservations (property_id, id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_guest_portal_challenges_lookup
    ON public.guest_portal_challenges (property_id, normalized_email, purpose, created_at DESC)
    WHERE consumed_at IS NULL AND invalidated_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_guest_portal_challenges_expires
    ON public.guest_portal_challenges (expires_at);

-- 6. Opaque Public Guest Portal Sessions (disjoint from admin_sessions / tirbach_session; zero IP/UA hashes).
CREATE TABLE IF NOT EXISTS public.guest_portal_sessions (
    id                    UUID PRIMARY KEY,
    property_id           UUID NOT NULL REFERENCES public.properties(id) ON DELETE RESTRICT,
    portal_identity_id    UUID NULL,
    scoped_reservation_id UUID NULL,
    scope_type            TEXT NOT NULL CHECK (scope_type IN ('BOOKING', 'ACCOUNT')),
    session_token_hash    TEXT NOT NULL UNIQUE CHECK (char_length(session_token_hash) = 64),
    csrf_secret_hash      TEXT NOT NULL CHECK (char_length(csrf_secret_hash) = 64),
    verified_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    idle_expires_at       TIMESTAMPTZ NOT NULL,
    absolute_expires_at   TIMESTAMPTZ NOT NULL,
    revoked_at            TIMESTAMPTZ NULL,
    last_seen_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_portal_session_scope_target CHECK (
        (scope_type = 'ACCOUNT' AND portal_identity_id IS NOT NULL AND scoped_reservation_id IS NULL)
        OR (scope_type = 'BOOKING' AND scoped_reservation_id IS NOT NULL AND portal_identity_id IS NULL)
    ),
    CONSTRAINT fk_portal_sessions_property_identity
        FOREIGN KEY (property_id, portal_identity_id)
        REFERENCES public.guest_portal_identities (property_id, id)
        ON DELETE CASCADE,
    CONSTRAINT fk_portal_sessions_property_reservation
        FOREIGN KEY (property_id, scoped_reservation_id)
        REFERENCES public.reservations (property_id, id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_guest_portal_sessions_identity
    ON public.guest_portal_sessions (property_id, portal_identity_id)
    WHERE portal_identity_id IS NOT NULL AND revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_guest_portal_sessions_reservation
    ON public.guest_portal_sessions (property_id, scoped_reservation_id)
    WHERE scoped_reservation_id IS NOT NULL AND revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_guest_portal_sessions_expiry
    ON public.guest_portal_sessions (idle_expires_at, absolute_expires_at);

-- 7. Least-privilege table grants for lovelace_app.
DO $$
BEGIN
    REVOKE ALL ON TABLE public.guest_portal_identities FROM PUBLIC;
    REVOKE ALL ON TABLE public.guest_portal_challenges FROM PUBLIC;
    REVOKE ALL ON TABLE public.guest_portal_sessions FROM PUBLIC;

    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.guest_portal_identities TO lovelace_app;
        GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.guest_portal_challenges TO lovelace_app;
        GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.guest_portal_sessions TO lovelace_app;
    END IF;
END $$;

-- 8. Extend database-enforced audit redaction allowlists 1:1 with internal/service/audit_sanitizer.go,
-- delegating all prior resource types and enums through _pwe4 wrappers.
DO $$
BEGIN
    IF to_regprocedure('public.audit_is_allowed_enum_pwe4(text,text)') IS NULL THEN
        ALTER FUNCTION public.audit_is_allowed_enum(TEXT, TEXT)
            RENAME TO audit_is_allowed_enum_pwe4;
    END IF;
    IF to_regprocedure('public.audit_resource_key_rule_pwe4(text,text,boolean)') IS NULL THEN
        ALTER FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN)
            RENAME TO audit_resource_key_rule_pwe4;
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
            IF p_val IN ('guest_portal_identity', 'guest_portal_session') THEN
                RETURN TRUE;
            END IF;
            RETURN public.audit_is_allowed_enum_pwe4(p_val, p_enum_name);
        WHEN 'enquiry_source' THEN
            IF p_val = 'PORTAL' THEN
                RETURN TRUE;
            END IF;
            RETURN public.audit_is_allowed_enum_pwe4(p_val, p_enum_name);
        WHEN 'reservation_changed_field' THEN
            IF p_val = 'arrival_details' THEN
                RETURN TRUE;
            END IF;
            RETURN public.audit_is_allowed_enum_pwe4(p_val, p_enum_name);
        WHEN 'portal_identity_status' THEN
            RETURN p_val IN ('ACTIVE', 'SUSPENDED');
        WHEN 'portal_challenge_purpose' THEN
            RETURN p_val IN ('LOGIN', 'VERIFY_EMAIL', 'STEP_UP');
        WHEN 'portal_session_scope' THEN
            RETURN p_val IN ('BOOKING', 'ACCOUNT');
        ELSE
            RETURN public.audit_is_allowed_enum_pwe4(p_val, p_enum_name);
    END CASE;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

CREATE OR REPLACE FUNCTION public.audit_resource_key_rule(
    p_resource_type TEXT,
    p_key           TEXT,
    p_is_metadata   BOOLEAN DEFAULT FALSE
) RETURNS TEXT AS $$
BEGIN
    IF p_resource_type = 'property_booking_policy' AND p_key = 'guest_portal_enabled' THEN
        RETURN 'bool';
    END IF;
    IF p_resource_type = 'guest' AND p_key = 'enquiry_id' THEN
        RETURN 'uuid_or_null';
    END IF;

    CASE p_resource_type
        WHEN 'guest_portal_identity' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'guest_id' THEN RETURN 'uuid';
                WHEN 'status' THEN RETURN 'enum:portal_identity_status';
                WHEN 'purpose' THEN RETURN 'enum:portal_challenge_purpose';
                WHEN 'challenge_id' THEN RETURN 'uuid_or_null';
                WHEN 'sessions_revoked' THEN RETURN 'int_count';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'email_verified_at' THEN RETURN 'timestamp_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;
        WHEN 'guest_portal_session' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'id' THEN RETURN 'uuid';
                WHEN 'session_id' THEN RETURN 'uuid';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'portal_identity_id' THEN RETURN 'uuid_or_null';
                WHEN 'scoped_reservation_id' THEN RETURN 'uuid_or_null';
                WHEN 'scope_type' THEN RETURN 'enum:portal_session_scope';
                WHEN 'revoked_count' THEN RETURN 'int_count';
                WHEN 'idle_expires_at' THEN RETURN 'timestamp';
                WHEN 'absolute_expires_at' THEN RETURN 'timestamp';
                WHEN 'revoked_at' THEN RETURN 'timestamp_or_null';
                WHEN 'created_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;
        ELSE
            RETURN public.audit_resource_key_rule_pwe4(p_resource_type, p_key, p_is_metadata);
    END CASE;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

DO $$
BEGIN
    REVOKE ALL ON FUNCTION public.audit_is_allowed_enum_pwe4(TEXT, TEXT) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_resource_key_rule_pwe4(TEXT, TEXT, BOOLEAN) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_is_allowed_enum(TEXT, TEXT) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN) FROM PUBLIC;

    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
        GRANT EXECUTE ON FUNCTION public.audit_is_allowed_enum_pwe4(TEXT, TEXT) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_resource_key_rule_pwe4(TEXT, TEXT, BOOLEAN) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_is_allowed_enum(TEXT, TEXT) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN) TO lovelace_app;
    END IF;
END $$;
