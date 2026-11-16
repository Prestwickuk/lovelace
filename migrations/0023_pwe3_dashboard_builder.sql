-- PWE-3 — Customisable Dashboard Builder
-- Additive schema-versioned personal dashboard layout column on the
-- (user_id, property_id) workspace table. Preserves all PWE-2 preferences
-- and saved_views untouched. A NULL dashboard_layout means "use resolved
-- platform (and future property/profile) default layout".

ALTER TABLE public.admin_user_property_workspaces
    ADD COLUMN IF NOT EXISTS dashboard_schema_version SMALLINT NOT NULL DEFAULT 1 CHECK (dashboard_schema_version >= 1),
    ADD COLUMN IF NOT EXISTS dashboard_layout JSONB NULL CHECK (dashboard_layout IS NULL OR jsonb_typeof(dashboard_layout) = 'object');

-- Re-assert least-privilege DML grants on the extended table for lovelace_app.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
        REVOKE ALL ON TABLE public.admin_user_property_workspaces FROM lovelace_app;
        GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.admin_user_property_workspaces TO lovelace_app;
    END IF;
END $$;
