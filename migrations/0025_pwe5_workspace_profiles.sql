-- PWE-5 — Workspace Profiles
-- Additive schema support for per-(user_id, property_id) workspace profile selection
-- and backward-compatible presence flags distinguishing explicit user overrides from
-- platform fallbacks so Workspace Profiles resolve cleanly underneath explicit PWE-2 overrides.

ALTER TABLE public.admin_user_preferences
    ADD COLUMN IF NOT EXISTS explicit_density BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS explicit_default_route BOOLEAN NOT NULL DEFAULT FALSE;

-- Preserve any non-default PWE-2 settings on existing rows as explicit overrides.
UPDATE public.admin_user_preferences
SET explicit_density = TRUE
WHERE density <> 'comfortable';

UPDATE public.admin_user_preferences
SET explicit_default_route = TRUE
WHERE default_route <> '/';

ALTER TABLE public.admin_user_property_workspaces
    ADD COLUMN IF NOT EXISTS selected_profile_code TEXT NULL
        CHECK (selected_profile_code IS NULL OR selected_profile_code IN ('owner_admin', 'reception', 'housekeeping', 'maintenance'));

-- Re-assert least-privilege DML grants on both personalisation tables for lovelace_app.
DO $$
BEGIN
    REVOKE ALL ON TABLE public.admin_user_preferences FROM PUBLIC;
    REVOKE ALL ON TABLE public.admin_user_property_workspaces FROM PUBLIC;

    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
        REVOKE ALL ON TABLE public.admin_user_preferences FROM lovelace_app;
        GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.admin_user_preferences TO lovelace_app;

        REVOKE ALL ON TABLE public.admin_user_property_workspaces FROM lovelace_app;
        GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.admin_user_property_workspaces TO lovelace_app;
    END IF;
END $$;
