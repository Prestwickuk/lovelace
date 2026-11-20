-- Phase 1.5 Migration: Inventory Allocation Semantics, Occupancy Tracking,
-- Property Branding Hooks, and Audit Log Hardening.
-- Additive and backward-compatible with Phases 1 & 1.1.

-- 1. Inventory claim lifecycle expansion & channel-aware GiST constraints
DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'inventory_claims'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) ILIKE '%status%'
    LOOP
        EXECUTE format('ALTER TABLE inventory_claims DROP CONSTRAINT %I', c.conname);
    END LOOP;
END $$;

ALTER TABLE inventory_claims
    ADD CONSTRAINT chk_inventory_claims_status
    CHECK (status IN ('ACTIVE', 'RELEASED', 'COMPLETED', 'CONSUMED'));

ALTER TABLE inventory_claims
    ADD COLUMN IF NOT EXISTS consumed_by_reservation_id UUID NULL REFERENCES reservations(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_inventory_claims_consumed_by
    ON inventory_claims (consumed_by_reservation_id)
    WHERE consumed_by_reservation_id IS NOT NULL;

-- Recreate exclusion constraints to cleanly separate:
-- (a) Physical occupancy, general closures, holds, and active channel allocations (cannot overlap each other on a unit).
-- (b) Channel-specific stop-sell restrictions (CHANNEL_RESTRICTION), scoped per (unit_id, channel_scope) so other channels remain bookable.
ALTER TABLE inventory_claims DROP CONSTRAINT IF EXISTS excl_inventory_claims_no_overlap;

ALTER TABLE inventory_claims
    ADD CONSTRAINT excl_inventory_claims_no_overlap
    EXCLUDE USING gist (unit_id WITH =, claim_range WITH &&)
    WHERE (status = 'ACTIVE' AND claim_type <> 'CHANNEL_RESTRICTION');

ALTER TABLE inventory_claims DROP CONSTRAINT IF EXISTS excl_channel_restriction_no_overlap;

ALTER TABLE inventory_claims
    ADD CONSTRAINT excl_channel_restriction_no_overlap
    EXCLUDE USING gist (unit_id WITH =, channel_scope WITH =, claim_range WITH &&)
    WHERE (status = 'ACTIVE' AND claim_type = 'CHANNEL_RESTRICTION');

-- 2. Per-unit guest allocation columns on reservation_units (preserves historical rows at 0,0,0 default)
ALTER TABLE reservation_units
    ADD COLUMN IF NOT EXISTS adults   INTEGER NOT NULL DEFAULT 0 CHECK (adults >= 0),
    ADD COLUMN IF NOT EXISTS children INTEGER NOT NULL DEFAULT 0 CHECK (children >= 0),
    ADD COLUMN IF NOT EXISTS infants  INTEGER NOT NULL DEFAULT 0 CHECK (infants >= 0);

-- 3. Extensible per-property branding hooks for Phases 2-5 (distinct from platform LOVELACE identity)
ALTER TABLE properties
    ADD COLUMN IF NOT EXISTS branding_config JSONB NOT NULL DEFAULT '{}'::jsonb;

-- 4. Harden append-only protection on audit_logs to prevent TRUNCATE as well as UPDATE/DELETE
CREATE OR REPLACE FUNCTION prevent_audit_log_mutation()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'TRUNCATE'
       AND current_database() ~ '(_test|-test)$'
       AND current_setting('lovelace.test_reset_token', true) = 'allow_test_reset' THEN
        RETURN NULL;
    END IF;
    RAISE EXCEPTION 'audit_logs is append-only; % operations are prohibited', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_audit_logs_no_truncate ON audit_logs;
CREATE TRIGGER trg_audit_logs_no_truncate
    BEFORE TRUNCATE ON audit_logs
    FOR EACH STATEMENT
    EXECUTE FUNCTION prevent_audit_log_mutation();

-- 5. Expand channel_connections.auth_method constraint to support HMAC-SHA256 signing (Smoobu)
DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'channel_connections'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) ILIKE '%auth_method%'
    LOOP
        EXECUTE format('ALTER TABLE channel_connections DROP CONSTRAINT %I', c.conname);
    END LOOP;
END $$;

ALTER TABLE channel_connections
    ADD CONSTRAINT chk_channel_connections_auth_method
    CHECK (auth_method IN ('api_key', 'hmac_sha256', 'oauth2', 'client_credentials', 'token'));
