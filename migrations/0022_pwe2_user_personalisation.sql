-- PWE-2 — User Personalisation
-- Hybrid relational + schema-versioned JSONB persistence for per-user global preferences
-- and (user_id, property_id) scoped operational workspace preferences & saved views.
-- Retention rule: preference rows are retained across soft deactivation (admin_users.is_active = FALSE)
-- and cascade-deleted only on hard deletion of the parent admin user or property.

CREATE TABLE IF NOT EXISTS public.admin_user_preferences (
    user_id           UUID PRIMARY KEY REFERENCES public.admin_users(id) ON DELETE CASCADE,
    schema_version    SMALLINT NOT NULL DEFAULT 1 CHECK (schema_version >= 1),
    theme_mode        TEXT NOT NULL DEFAULT 'system' CHECK (theme_mode IN ('system', 'light', 'dark')),
    accent            TEXT NOT NULL DEFAULT 'teal' CHECK (accent IN ('teal', 'olive', 'slate', 'brass')),
    density           TEXT NOT NULL DEFAULT 'comfortable' CHECK (density IN ('comfortable', 'compact')),
    text_scale        TEXT NOT NULL DEFAULT 'default' CHECK (text_scale IN ('default', 'large', 'xl')),
    high_contrast     BOOLEAN NOT NULL DEFAULT FALSE,
    reduced_motion    BOOLEAN NOT NULL DEFAULT FALSE,
    default_route     TEXT NOT NULL DEFAULT '/',
    last_property_id  UUID NULL REFERENCES public.properties(id) ON DELETE SET NULL,
    preferences       JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(preferences) = 'object'),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS public.admin_user_property_workspaces (
    user_id         UUID NOT NULL REFERENCES public.admin_users(id) ON DELETE CASCADE,
    property_id     UUID NOT NULL REFERENCES public.properties(id) ON DELETE CASCADE,
    schema_version  SMALLINT NOT NULL DEFAULT 1 CHECK (schema_version >= 1),
    preferences     JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(preferences) = 'object'),
    saved_views     JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(saved_views) = 'array'),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, property_id)
);

CREATE INDEX IF NOT EXISTS idx_admin_user_property_workspaces_property
    ON public.admin_user_property_workspaces (property_id);

-- Least-privilege runtime grants for lovelace_app.
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
