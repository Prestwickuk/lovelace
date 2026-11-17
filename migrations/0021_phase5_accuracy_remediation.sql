-- Phase 5 Accuracy Remediation & Phase 4 Disconnect Clarification
-- 1. Persist occurrence timestamps and provider-refund deduplication keys on refunds.
-- 2. Track effective-dated unit commissioning/status/archive lifecycle transitions and explicit history boundaries.

-- --- 1. Refund occurrence timestamps & idempotent provider_refund_id uniqueness ---

ALTER TABLE public.refunds
    ADD COLUMN IF NOT EXISTS occurred_at TIMESTAMPTZ NULL;

UPDATE public.refunds
SET occurred_at = created_at
WHERE occurred_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_refunds_property_provider_refund_id
    ON public.refunds (property_id, provider, provider_refund_id)
    WHERE provider_refund_id <> '';

CREATE INDEX IF NOT EXISTS idx_refunds_property_status_occurred
    ON public.refunds (property_id, status, COALESCE(occurred_at, created_at));

-- --- 2. Effective-dated unit inventory history & explicit pre-tracking gap boundaries ---

CREATE TABLE IF NOT EXISTS public.unit_status_history (
    id             UUID PRIMARY KEY,
    property_id    UUID NOT NULL REFERENCES public.properties(id) ON DELETE RESTRICT,
    unit_id        UUID NOT NULL REFERENCES public.units(id) ON DELETE RESTRICT,
    unit_type_id   UUID NOT NULL REFERENCES public.unit_types(id) ON DELETE RESTRICT,
    status         TEXT NOT NULL CHECK (status IN ('active', 'maintenance', 'inactive', 'retired')),
    is_archived    BOOLEAN NOT NULL DEFAULT FALSE,
    effective_from TIMESTAMPTZ NOT NULL,
    source         TEXT NOT NULL CHECK (source IN (
        'unit_created',
        'unit_updated',
        'unit_archived',
        'unit_restored',
        'audit_backfill',
        'migration_backfill',
        'migration_baseline'
    )),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_unit_status_history_lookup
    ON public.unit_status_history (property_id, unit_id, effective_from DESC, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_unit_status_history_property_eff
    ON public.unit_status_history (property_id, effective_from);

CREATE TABLE IF NOT EXISTS public.inventory_history_gaps (
    id            UUID PRIMARY KEY,
    property_id   UUID NOT NULL REFERENCES public.properties(id) ON DELETE RESTRICT,
    unit_id       UUID NULL REFERENCES public.units(id) ON DELETE RESTRICT,
    reliable_from TIMESTAMPTZ NOT NULL,
    reason        TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_inventory_history_gaps_prop_unit
    ON public.inventory_history_gaps (property_id, COALESCE(unit_id, '00000000-0000-0000-0000-000000000000'::uuid));

-- Backfill reliably establishable history for units existing prior to migration 0021.
-- Never guess unrecorded intermediate transitions: when pre-0021 updates/archivals lack
-- complete audit snapshots, record only a post-migration baseline and log an explicit gap boundary.
DO $$
DECLARE
    u_rec RECORD;
    a_rec RECORD;
    v_has_created_audit BOOLEAN;
    v_change_audit_count INT;
    v_expected_changes INT;
    v_unit_type_text TEXT;
    v_status TEXT;
    v_archived BOOLEAN;
    v_last_type UUID;
    v_last_status TEXT;
    v_last_archived BOOLEAN;
    v_audit_valid BOOLEAN;
    v_mig_now TIMESTAMPTZ := NOW();
BEGIN
    FOR u_rec IN
        SELECT u.id, u.property_id, u.unit_type_id, u.status, u.version, u.archived_at, u.created_at
        FROM public.units u
        WHERE NOT EXISTS (
            SELECT 1 FROM public.unit_status_history h WHERE h.unit_id = u.id
        )
    LOOP
        v_has_created_audit := FALSE;
        v_change_audit_count := 0;
        v_expected_changes := GREATEST(u_rec.version - 1, 0);
        v_audit_valid := TRUE;
        v_last_type := NULL;
        v_last_status := NULL;
        v_last_archived := NULL;

        SELECT EXISTS (
            SELECT 1 FROM public.audit_logs al
            WHERE al.resource_type = 'unit'
              AND al.resource_id = u_rec.id::text
              AND al.action = 'unit.created'
              AND al.after_state IS NOT NULL
              AND NOT COALESCE((al.after_state->>'redacted')::boolean, FALSE)
        ) INTO v_has_created_audit;

        SELECT COUNT(*)::int INTO v_change_audit_count
        FROM public.audit_logs al
        WHERE al.resource_type = 'unit'
          AND al.resource_id = u_rec.id::text
          AND al.action IN ('unit.updated', 'unit.archived');

        IF v_has_created_audit AND v_change_audit_count >= v_expected_changes THEN
            -- Verify all audit rows yield valid unit snapshots before replaying.
            FOR a_rec IN
                SELECT al.action, al.before_state, al.after_state, al.created_at
                FROM public.audit_logs al
                WHERE al.resource_type = 'unit'
                  AND al.resource_id = u_rec.id::text
                  AND al.action IN ('unit.created', 'unit.updated', 'unit.archived')
                ORDER BY al.created_at ASC, al.id ASC
            LOOP
                IF a_rec.action = 'unit.archived' THEN
                    v_unit_type_text := COALESCE(a_rec.before_state->>'unit_type_id', u_rec.unit_type_id::text);
                    v_status := 'retired';
                    v_archived := TRUE;
                ELSE
                    IF a_rec.after_state IS NULL OR COALESCE((a_rec.after_state->>'redacted')::boolean, FALSE) THEN
                        v_audit_valid := FALSE;
                        EXIT;
                    END IF;
                    v_unit_type_text := COALESCE(a_rec.after_state->>'unit_type_id', '');
                    v_status := COALESCE(a_rec.after_state->>'status', '');
                    v_archived := (NULLIF(COALESCE(a_rec.after_state->>'archived_at', ''), '') IS NOT NULL);
                END IF;

                IF v_unit_type_text !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                   OR v_status NOT IN ('active', 'maintenance', 'inactive', 'retired') THEN
                    v_audit_valid := FALSE;
                    EXIT;
                END IF;

                v_last_type := v_unit_type_text::uuid;
                v_last_status := v_status;
                v_last_archived := v_archived;
            END LOOP;

            IF v_audit_valid AND v_last_type IS NOT NULL THEN
                FOR a_rec IN
                    SELECT al.action, al.before_state, al.after_state, al.created_at
                    FROM public.audit_logs al
                    WHERE al.resource_type = 'unit'
                      AND al.resource_id = u_rec.id::text
                      AND al.action IN ('unit.created', 'unit.updated', 'unit.archived')
                    ORDER BY al.created_at ASC, al.id ASC
                LOOP
                    IF a_rec.action = 'unit.archived' THEN
                        v_unit_type_text := COALESCE(a_rec.before_state->>'unit_type_id', u_rec.unit_type_id::text);
                        v_status := 'retired';
                        v_archived := TRUE;
                    ELSE
                        v_unit_type_text := COALESCE(a_rec.after_state->>'unit_type_id', u_rec.unit_type_id::text);
                        v_status := COALESCE(a_rec.after_state->>'status', u_rec.status);
                        v_archived := (NULLIF(COALESCE(a_rec.after_state->>'archived_at', ''), '') IS NOT NULL);
                    END IF;

                    INSERT INTO public.unit_status_history (
                        id, property_id, unit_id, unit_type_id, status, is_archived, effective_from, source
                    ) VALUES (
                        gen_random_uuid(),
                        u_rec.property_id,
                        u_rec.id,
                        v_unit_type_text::uuid,
                        v_status,
                        v_archived,
                        CASE WHEN a_rec.action = 'unit.created' THEN u_rec.created_at ELSE a_rec.created_at END,
                        'audit_backfill'
                    );
                END LOOP;

                IF v_last_type IS DISTINCT FROM u_rec.unit_type_id
                   OR v_last_status IS DISTINCT FROM u_rec.status
                   OR v_last_archived IS DISTINCT FROM (u_rec.archived_at IS NOT NULL) THEN
                    INSERT INTO public.unit_status_history (
                        id, property_id, unit_id, unit_type_id, status, is_archived, effective_from, source
                    ) VALUES (
                        gen_random_uuid(), u_rec.property_id, u_rec.id, u_rec.unit_type_id, u_rec.status,
                        (u_rec.archived_at IS NOT NULL), v_mig_now, 'migration_baseline'
                    );
                    INSERT INTO public.inventory_history_gaps (id, property_id, unit_id, reliable_from, reason)
                    VALUES (gen_random_uuid(), u_rec.property_id, u_rec.id, v_mig_now, 'pre_0021_audit_drift_detected')
                    ON CONFLICT DO NOTHING;
                END IF;
                CONTINUE;
            END IF;
        END IF;

        IF u_rec.version = 1 AND u_rec.archived_at IS NULL THEN
            INSERT INTO public.unit_status_history (
                id, property_id, unit_id, unit_type_id, status, is_archived, effective_from, source
            ) VALUES (
                gen_random_uuid(), u_rec.property_id, u_rec.id, u_rec.unit_type_id, u_rec.status,
                FALSE, u_rec.created_at, 'migration_backfill'
            );
        ELSE
            -- Pre-0021 mutated or archived unit without complete audit history:
            -- record only current verified baseline from migration time forward and surface a coverage gap.
            INSERT INTO public.unit_status_history (
                id, property_id, unit_id, unit_type_id, status, is_archived, effective_from, source
            ) VALUES (
                gen_random_uuid(), u_rec.property_id, u_rec.id, u_rec.unit_type_id, u_rec.status,
                (u_rec.archived_at IS NOT NULL), v_mig_now, 'migration_baseline'
            );
            INSERT INTO public.inventory_history_gaps (id, property_id, unit_id, reliable_from, reason)
            VALUES (gen_random_uuid(), u_rec.property_id, u_rec.id, v_mig_now, 'pre_0021_unrecorded_unit_transitions')
            ON CONFLICT DO NOTHING;
        END IF;
    END LOOP;
END $$;

-- Automatically track future unit commissioning, status transitions, unit_type reclassifications, and archivals.
CREATE OR REPLACE FUNCTION public.trg_record_unit_status_history()
RETURNS TRIGGER AS $$
DECLARE
    v_src TEXT;
    v_eff TIMESTAMPTZ;
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO public.unit_status_history (
            id, property_id, unit_id, unit_type_id, status, is_archived, effective_from, source
        ) VALUES (
            gen_random_uuid(), NEW.property_id, NEW.id, NEW.unit_type_id, NEW.status, (NEW.archived_at IS NOT NULL),
            COALESCE(NEW.created_at, NOW()), 'unit_created'
        );
        RETURN NEW;
    END IF;

    IF TG_OP = 'UPDATE' THEN
        IF NEW.unit_type_id IS DISTINCT FROM OLD.unit_type_id
           OR NEW.status IS DISTINCT FROM OLD.status
           OR (NEW.archived_at IS NOT NULL) IS DISTINCT FROM (OLD.archived_at IS NOT NULL) THEN
            IF NEW.archived_at IS NOT NULL AND OLD.archived_at IS NULL THEN
                v_src := 'unit_archived';
                v_eff := NEW.archived_at;
            ELSIF NEW.archived_at IS NULL AND OLD.archived_at IS NOT NULL THEN
                v_src := 'unit_restored';
                v_eff := COALESCE(NEW.updated_at, NOW());
            ELSE
                v_src := 'unit_updated';
                v_eff := COALESCE(NEW.updated_at, NOW());
            END IF;

            INSERT INTO public.unit_status_history (
                id, property_id, unit_id, unit_type_id, status, is_archived, effective_from, source
            ) VALUES (
                gen_random_uuid(), NEW.property_id, NEW.id, NEW.unit_type_id, NEW.status, (NEW.archived_at IS NOT NULL),
                v_eff, v_src
            );
        END IF;
        RETURN NEW;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql SET search_path = pg_catalog, public;

DROP TRIGGER IF EXISTS trg_units_record_status_history ON public.units;
CREATE TRIGGER trg_units_record_status_history
    AFTER INSERT OR UPDATE ON public.units
    FOR EACH ROW
    EXECUTE FUNCTION public.trg_record_unit_status_history();

-- Least-privilege runtime grants for lovelace_app (append-only insert/read on unit_status_history, read-only on gaps).
DO $$
BEGIN
    REVOKE ALL ON TABLE public.unit_status_history FROM PUBLIC;
    REVOKE ALL ON TABLE public.inventory_history_gaps FROM PUBLIC;

    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
        REVOKE ALL ON TABLE public.unit_status_history FROM lovelace_app;
        GRANT SELECT, INSERT ON TABLE public.unit_status_history TO lovelace_app;
        REVOKE ALL ON TABLE public.inventory_history_gaps FROM lovelace_app;
        GRANT SELECT ON TABLE public.inventory_history_gaps TO lovelace_app;
    END IF;
END $$;
