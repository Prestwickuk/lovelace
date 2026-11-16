-- PWE-4 — Property Branding
-- Separates public-safe property branding metadata from admin-only branding and
-- bounded administrative terminology overrides, tracks validated logo attachment
-- metadata outside any web root (via BlobStore), and extends database audit
-- allowlists cleanly for sanitized property_branding events.

CREATE TABLE IF NOT EXISTS public.property_branding_settings (
    property_id       UUID PRIMARY KEY REFERENCES public.properties(id) ON DELETE CASCADE,
    schema_version    SMALLINT NOT NULL DEFAULT 1 CHECK (schema_version >= 1),
    public_branding   JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(public_branding) = 'object'),
    admin_branding    JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(admin_branding) = 'object'),
    logo_storage_key  TEXT NULL,
    logo_content_type TEXT NULL CHECK (logo_content_type IS NULL OR logo_content_type IN ('image/png', 'image/jpeg', 'image/webp')),
    logo_size_bytes   BIGINT NOT NULL DEFAULT 0 CHECK (logo_size_bytes >= 0 AND logo_size_bytes <= 2097152),
    logo_sha256       TEXT NULL CHECK (logo_sha256 IS NULL OR logo_sha256 ~ '^[0-9a-f]{64}$'),
    logo_width        INT NOT NULL DEFAULT 0 CHECK (logo_width >= 0 AND logo_width <= 2048),
    logo_height       INT NOT NULL DEFAULT 0 CHECK (logo_height >= 0 AND logo_height <= 2048),
    logo_version      INT NOT NULL DEFAULT 0 CHECK (logo_version >= 0),
    version           INT NOT NULL DEFAULT 1 CHECK (version >= 1),
    updated_by        UUID NULL REFERENCES public.admin_users(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed baseline rows for any existing properties without touching properties.branding_config.
-- Migrate only recognized safe tagline / hex colour keys into public_branding if present.
INSERT INTO public.property_branding_settings (
    property_id,
    schema_version,
    public_branding,
    admin_branding,
    created_at,
    updated_at
)
SELECT
    p.id,
    1,
    jsonb_strip_nulls(
        jsonb_build_object(
            'display_name', p.name,
            'tagline', COALESCE(NULLIF(BTRIM(p.branding_config->>'display_tagline'), ''), p.tagline),
            'region_label', p.region,
            'primary_color', CASE
                WHEN UPPER(COALESCE(p.branding_config->>'primary_color', '')) ~ '^#[0-9A-F]{6}$'
                THEN UPPER(p.branding_config->>'primary_color')
                ELSE NULL
            END,
            'secondary_color', CASE
                WHEN UPPER(COALESCE(p.branding_config->>'secondary_color', p.branding_config->>'accent_color', '')) ~ '^#[0-9A-F]{6}$'
                THEN UPPER(COALESCE(NULLIF(p.branding_config->>'secondary_color', ''), p.branding_config->>'accent_color'))
                ELSE NULL
            END
        )
    ),
    '{}'::jsonb,
    p.created_at,
    p.updated_at
FROM public.properties p
ON CONFLICT (property_id) DO NOTHING;

-- Least-privilege table grants for lovelace_app.
DO $$
BEGIN
    REVOKE ALL ON TABLE public.property_branding_settings FROM PUBLIC;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.property_branding_settings TO lovelace_app;
    END IF;
END $$;

-- Extend database-enforced audit redaction allowlists cleanly for property_branding,
-- delegating all Phase 1-5 resource types and enums to _phase5 wrappers.
DO $$
BEGIN
    IF to_regprocedure('public.audit_is_allowed_enum_phase5(text,text)') IS NULL THEN
        ALTER FUNCTION public.audit_is_allowed_enum(TEXT, TEXT)
            RENAME TO audit_is_allowed_enum_phase5;
    END IF;
    IF to_regprocedure('public.audit_resource_key_rule_phase5(text,text,boolean)') IS NULL THEN
        ALTER FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN)
            RENAME TO audit_resource_key_rule_phase5;
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
            IF p_val = 'property_branding' THEN
                RETURN TRUE;
            END IF;
            RETURN public.audit_is_allowed_enum_phase5(p_val, p_enum_name);
        WHEN 'branding_action' THEN
            RETURN p_val IN ('updated', 'logo_uploaded', 'logo_deleted');
        WHEN 'branding_logo_mime' THEN
            RETURN p_val IN ('image/png', 'image/jpeg', 'image/webp');
        ELSE
            RETURN public.audit_is_allowed_enum_phase5(p_val, p_enum_name);
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
        WHEN 'property_branding' THEN
            CASE p_key
                WHEN 'redacted' THEN RETURN 'bool_true';
                WHEN 'property_id' THEN RETURN 'uuid';
                WHEN 'action' THEN RETURN 'enum:branding_action';
                WHEN 'schema_version' THEN RETURN 'int_count';
                WHEN 'version' THEN RETURN 'int_version';
                WHEN 'has_custom_palette' THEN RETURN 'bool';
                WHEN 'has_terminology_overrides' THEN RETURN 'bool';
                WHEN 'has_logo' THEN RETURN 'bool';
                WHEN 'logo_content_type' THEN RETURN 'enum:branding_logo_mime';
                WHEN 'logo_size_bytes' THEN RETURN 'int_size_bytes';
                WHEN 'logo_sha256' THEN RETURN 'sha256';
                WHEN 'logo_version' THEN RETURN 'int_count';
                WHEN 'updated_at' THEN RETURN 'timestamp';
                WHEN 'created_at' THEN RETURN 'timestamp';
                ELSE RETURN NULL;
            END CASE;
        ELSE
            RETURN public.audit_resource_key_rule_phase5(p_resource_type, p_key, p_is_metadata);
    END CASE;
END;
$$ LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public;

DO $$
BEGIN
    REVOKE ALL ON FUNCTION public.audit_is_allowed_enum_phase5(TEXT, TEXT) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_resource_key_rule_phase5(TEXT, TEXT, BOOLEAN) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_is_allowed_enum(TEXT, TEXT) FROM PUBLIC;
    REVOKE ALL ON FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN) FROM PUBLIC;

    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
        GRANT EXECUTE ON FUNCTION public.audit_is_allowed_enum_phase5(TEXT, TEXT) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_resource_key_rule_phase5(TEXT, TEXT, BOOLEAN) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_is_allowed_enum(TEXT, TEXT) TO lovelace_app;
        GRANT EXECUTE ON FUNCTION public.audit_resource_key_rule(TEXT, TEXT, BOOLEAN) TO lovelace_app;
    END IF;
END $$;
