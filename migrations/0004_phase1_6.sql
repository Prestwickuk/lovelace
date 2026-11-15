-- Phase 1.6 Migration: Blockout/Allocation Lifecycle Semantics & Consumption Provenance.
-- Additive and backward-compatible with Phases 1, 1.1, and 1.5.

-- 1. Expand blockout_groups.status to distinguish consumed vs manually released allocations.
DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'blockout_groups'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) ILIKE '%status%'
    LOOP
        EXECUTE format('ALTER TABLE blockout_groups DROP CONSTRAINT %I', c.conname);
    END LOOP;
END $$;

ALTER TABLE blockout_groups
    ADD CONSTRAINT chk_blockout_groups_status
    CHECK (status IN ('ACTIVE', 'PARTIALLY_RELEASED', 'RELEASED', 'PARTIALLY_CONSUMED', 'CONSUMED'));

-- 2. Allocation consumption provenance & reconciliation table.
-- Tracks exact consumed reservation intervals so date edits and cancellations/amendments
-- can safely recompute residuals and restore inventory back to its originating blockout_group_id.
CREATE TABLE allocation_consumptions (
    id                 UUID PRIMARY KEY,
    property_id        UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    blockout_group_id  UUID NOT NULL REFERENCES blockout_groups(id) ON DELETE CASCADE,
    source_claim_id    UUID NOT NULL REFERENCES inventory_claims(id) ON DELETE CASCADE,
    reservation_id     UUID NOT NULL REFERENCES reservations(id) ON DELETE CASCADE,
    unit_id            UUID NOT NULL REFERENCES units(id) ON DELETE RESTRICT,
    channel_scope      TEXT NOT NULL CHECK (channel_scope ~ '^[A-Z0-9_]+$'),
    consumed_range     DATERANGE NOT NULL CHECK (
                           NOT isempty(consumed_range)
                           AND lower_inc(consumed_range)
                           AND NOT upper_inc(consumed_range)
                       ),
    status             TEXT NOT NULL DEFAULT 'CONSUMED'
                       CHECK (status IN ('CONSUMED', 'RESTORED', 'DETACHED')),
    restored_at        TIMESTAMPTZ NULL,
    restored_by        UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_allocation_consumptions_reservation_status
    ON allocation_consumptions (reservation_id, status);

CREATE INDEX idx_allocation_consumptions_group_unit_status
    ON allocation_consumptions (blockout_group_id, unit_id, status);

-- 3. Remediate Phase 1.5 consumed claims that mistakenly recorded released_at / released_by,
--    backfill allocation_consumptions, and recompute blockout_groups statuses cleanly.
UPDATE inventory_claims
SET released_at = NULL,
    released_by = NULL,
    updated_at = NOW()
WHERE status = 'CONSUMED'
  AND (released_at IS NOT NULL OR released_by IS NOT NULL);

INSERT INTO allocation_consumptions (
    id, property_id, blockout_group_id, source_claim_id, reservation_id, unit_id,
    channel_scope, consumed_range, status, created_at, updated_at
)
SELECT
    gen_random_uuid(),
    ic.property_id,
    ic.blockout_group_id,
    ic.id,
    ic.consumed_by_reservation_id,
    ic.unit_id,
    ic.channel_scope,
    ic.claim_range * r.stay_range,
    CASE
        WHEN r.status IN ('INQUIRY', 'CANCELLED', 'NO_SHOW') THEN 'DETACHED'
        ELSE 'CONSUMED'
    END,
    ic.updated_at,
    NOW()
FROM inventory_claims ic
JOIN reservations r ON r.id = ic.consumed_by_reservation_id
WHERE ic.status = 'CONSUMED'
  AND ic.blockout_group_id IS NOT NULL
  AND ic.consumed_by_reservation_id IS NOT NULL
  AND NOT isempty(ic.claim_range * r.stay_range);

WITH stats AS (
    SELECT
        bg.id,
        (SELECT COUNT(*) FROM inventory_claims ic WHERE ic.blockout_group_id = bg.id AND ic.status = 'ACTIVE') AS active_cnt,
        (SELECT COUNT(*) FROM allocation_consumptions ac WHERE ac.blockout_group_id = bg.id AND ac.status = 'CONSUMED') AS consumed_cnt,
        (SELECT COUNT(*) FROM inventory_claims ic WHERE ic.blockout_group_id = bg.id AND ic.status = 'RELEASED') AS released_cnt
    FROM blockout_groups bg
)
UPDATE blockout_groups bg
SET status = CASE
        WHEN s.active_cnt > 0 AND s.released_cnt > 0 THEN 'PARTIALLY_RELEASED'
        WHEN s.active_cnt > 0 AND s.consumed_cnt > 0 THEN 'PARTIALLY_CONSUMED'
        WHEN s.active_cnt > 0 THEN 'ACTIVE'
        WHEN s.consumed_cnt > 0 AND s.released_cnt = 0 THEN 'CONSUMED'
        WHEN s.consumed_cnt > 0 AND s.released_cnt > 0 THEN 'PARTIALLY_RELEASED'
        ELSE 'RELEASED'
    END,
    released_at = CASE WHEN s.released_cnt = 0 THEN NULL ELSE bg.released_at END,
    released_by = CASE WHEN s.released_cnt = 0 THEN NULL ELSE bg.released_by END,
    updated_at = NOW()
FROM stats s
WHERE bg.id = s.id;
