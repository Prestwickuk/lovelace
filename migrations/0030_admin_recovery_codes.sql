-- 0030_admin_recovery_codes.sql
-- Email-free one-time administrator password recovery codes (stored solely as keyed HMAC-SHA256 digests).

CREATE TABLE IF NOT EXISTS public.admin_recovery_codes (
    id          UUID PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES public.admin_users(id) ON DELETE CASCADE,
    code_hash   TEXT NOT NULL CHECK (char_length(code_hash) = 64 AND code_hash ~ '^[0-9a-f]{64}$'),
    used_at     TIMESTAMPTZ NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_admin_recovery_codes_user_hash UNIQUE (user_id, code_hash)
);

CREATE INDEX IF NOT EXISTS idx_admin_recovery_codes_user_unused
    ON public.admin_recovery_codes (user_id)
    WHERE used_at IS NULL;

DO $$
BEGIN
    REVOKE ALL ON TABLE public.admin_recovery_codes FROM PUBLIC;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.admin_recovery_codes TO lovelace_app;
    END IF;
END $$;
