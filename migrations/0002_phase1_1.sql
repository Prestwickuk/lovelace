-- Phase 1.1: Reservations, Central Inventory Claims, Bulk Blockouts,
-- Scheduled Availability Releases, Channel Manager Foundation & Security Hardening.
--
-- IMPORTANT: Preserves 0001_init.sql intact and migrates existing availability_blocks
-- into blockout_groups + inventory_claims without losing history.

-- 1. Booking Channels (extensible per-property source definitions)
CREATE TABLE booking_channels (
    id          UUID PRIMARY KEY,
    property_id UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    code        TEXT NOT NULL CHECK (code ~ '^[A-Z0-9_]+$'),
    name        TEXT NOT NULL CHECK (char_length(trim(name)) > 0),
    category    TEXT NOT NULL CHECK (category IN ('direct', 'ota', 'manual', 'other')),
    is_active   BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order  INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, code)
);

-- Seed default booking channels for any existing properties
INSERT INTO booking_channels (id, property_id, code, name, category, sort_order)
SELECT
    gen_random_uuid(),
    p.id,
    c.code,
    c.name,
    c.category,
    c.sort_order
FROM properties p
CROSS JOIN (VALUES
    ('DIRECT',      'Direct Website / Admin', 'direct', 10),
    ('PHONE',       'Telephone',              'manual', 20),
    ('EMAIL',       'Email Inquiry',          'manual', 30),
    ('WALK_IN',     'Walk-in',                'manual', 40),
    ('AIRBNB',      'Airbnb',                 'ota',    50),
    ('BOOKING_COM', 'Booking.com',            'ota',    60),
    ('OTHER',       'Other Source',           'other',  70)
) AS c(code, name, category, sort_order)
ON CONFLICT (property_id, code) DO NOTHING;

-- 2. Guests (expandable in Phase 3 for Guest Portal & Profiles)
CREATE TABLE guests (
    id           UUID PRIMARY KEY,
    property_id  UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    full_name    TEXT NOT NULL CHECK (char_length(trim(full_name)) > 0),
    email        TEXT NOT NULL DEFAULT '',
    phone        TEXT NOT NULL DEFAULT '',
    address_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    preferences  JSONB NOT NULL DEFAULT '{}'::jsonb,
    notes        TEXT NOT NULL DEFAULT '',
    version      INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_guests_property_email
    ON guests (property_id, LOWER(email))
    WHERE email <> '';

-- 3. Reservations (Administrative Manual Reservations — no payment status in Phase 1.1)
CREATE TABLE reservations (
    id                  UUID PRIMARY KEY,
    property_id         UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    reference           TEXT NOT NULL CHECK (reference ~ '^[A-Z0-9-]+$'),
    guest_id            UUID NOT NULL REFERENCES guests(id) ON DELETE RESTRICT,
    guest_name          TEXT NOT NULL CHECK (char_length(trim(guest_name)) > 0),
    guest_email         TEXT NOT NULL DEFAULT '',
    guest_phone         TEXT NOT NULL DEFAULT '',
    channel_code        TEXT NOT NULL,
    stay_range          DATERANGE NOT NULL CHECK (
                            NOT isempty(stay_range)
                            AND lower_inc(stay_range)
                            AND NOT upper_inc(stay_range)
                        ),
    adults              INTEGER NOT NULL DEFAULT 1 CHECK (adults >= 1),
    children            INTEGER NOT NULL DEFAULT 0 CHECK (children >= 0),
    infants             INTEGER NOT NULL DEFAULT 0 CHECK (infants >= 0),
    status              TEXT NOT NULL CHECK (status IN (
                            'INQUIRY', 'PENDING', 'CONFIRMED',
                            'CHECKED_IN', 'CHECKED_OUT', 'CANCELLED', 'NO_SHOW'
                        )),
    internal_notes      TEXT NOT NULL DEFAULT '',
    cancellation_reason TEXT NOT NULL DEFAULT '',
    cancelled_at        TIMESTAMPTZ NULL,
    created_by          UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    updated_by          UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    version             INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, reference),
    FOREIGN KEY (property_id, channel_code) REFERENCES booking_channels(property_id, code) ON DELETE RESTRICT
);

CREATE INDEX idx_reservations_property_dates
    ON reservations USING gist (property_id, stay_range);

CREATE INDEX idx_reservations_property_status
    ON reservations (property_id, status, created_at DESC);

-- Multi-unit assignment per reservation
CREATE TABLE reservation_units (
    id             UUID PRIMARY KEY,
    property_id    UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    reservation_id UUID NOT NULL REFERENCES reservations(id) ON DELETE CASCADE,
    unit_id        UUID NOT NULL REFERENCES units(id) ON DELETE RESTRICT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (reservation_id, unit_id)
);

CREATE INDEX idx_reservation_units_unit
    ON reservation_units (property_id, unit_id);

-- 4. Blockout Groups (preserving relationship between bulk blockouts and individual claims)
CREATE TABLE blockout_groups (
    id                    UUID PRIMARY KEY,
    property_id           UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    scope_type            TEXT NOT NULL CHECK (scope_type IN ('UNIT', 'UNITS', 'UNIT_TYPE', 'PROPERTY')),
    target_unit_type_id   UUID NULL REFERENCES unit_types(id) ON DELETE RESTRICT,
    category              TEXT NOT NULL CHECK (category ~ '^[A-Z0-9_]+$'),
    channel_scope         TEXT NOT NULL DEFAULT 'ALL' CHECK (channel_scope ~ '^[A-Z0-9_]+$'),
    block_range           DATERANGE NOT NULL CHECK (
                              NOT isempty(block_range)
                              AND lower_inc(block_range)
                              AND NOT upper_inc(block_range)
                          ),
    reason                TEXT NOT NULL DEFAULT '',
    apply_to_future_units BOOLEAN NOT NULL DEFAULT FALSE,
    status                TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'PARTIALLY_RELEASED', 'RELEASED')),
    created_by            UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    released_by           UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    released_at           TIMESTAMPTZ NULL,
    version               INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_blockout_groups_property_status
    ON blockout_groups (property_id, status, created_at DESC);

-- 5. Central Inventory Claims (single source of truth preventing ALL competing overlaps)
CREATE TABLE inventory_claims (
    id                UUID PRIMARY KEY,
    property_id       UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    unit_id           UUID NOT NULL REFERENCES units(id) ON DELETE RESTRICT,
    claim_type        TEXT NOT NULL CHECK (claim_type ~ '^[A-Z0-9_]+$'),
    reservation_id    UUID NULL REFERENCES reservations(id) ON DELETE CASCADE,
    blockout_group_id UUID NULL REFERENCES blockout_groups(id) ON DELETE CASCADE,
    channel_scope     TEXT NOT NULL DEFAULT 'ALL' CHECK (channel_scope ~ '^[A-Z0-9_]+$'),
    claim_range       DATERANGE NOT NULL CHECK (
                          NOT isempty(claim_range)
                          AND lower_inc(claim_range)
                          AND NOT upper_inc(claim_range)
                      ),
    status            TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'RELEASED', 'COMPLETED')),
    reason            TEXT NOT NULL DEFAULT '',
    created_by        UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    released_by       UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    released_at       TIMESTAMPTZ NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (
        (reservation_id IS NOT NULL AND blockout_group_id IS NULL) OR
        (reservation_id IS NULL AND blockout_group_id IS NOT NULL)
    ),
    CONSTRAINT excl_inventory_claims_no_overlap
        EXCLUDE USING gist (unit_id WITH =, claim_range WITH &&)
        WHERE (status = 'ACTIVE')
);

CREATE INDEX idx_inventory_claims_property_active_range
    ON inventory_claims USING gist (property_id, claim_range)
    WHERE status = 'ACTIVE';

CREATE INDEX idx_inventory_claims_blockout_group
    ON inventory_claims (blockout_group_id)
    WHERE blockout_group_id IS NOT NULL;

CREATE INDEX idx_inventory_claims_reservation
    ON inventory_claims (reservation_id)
    WHERE reservation_id IS NOT NULL;

-- Migrate existing Phase 1 availability_blocks safely into blockout_groups + inventory_claims
INSERT INTO blockout_groups (
    id, property_id, scope_type, category, channel_scope, block_range,
    reason, apply_to_future_units, status, created_by, released_by, released_at,
    created_at, updated_at
)
SELECT
    ab.id,
    ab.property_id,
    'UNIT',
    CASE ab.block_type
        WHEN 'owner_hold' THEN 'OWNER_USE'
        WHEN 'maintenance' THEN 'MAINTENANCE'
        WHEN 'seasonal_closure' THEN 'SEASONAL_CLOSURE'
        WHEN 'weather_closure' THEN 'WEATHER_CLOSURE'
        ELSE 'ADMINISTRATIVE_BLOCK'
    END,
    'ALL',
    ab.block_range,
    ab.reason,
    FALSE,
    CASE WHEN ab.released_at IS NULL THEN 'ACTIVE' ELSE 'RELEASED' END,
    ab.created_by,
    ab.released_by,
    ab.released_at,
    ab.created_at,
    ab.updated_at
FROM availability_blocks ab;

INSERT INTO inventory_claims (
    id, property_id, unit_id, claim_type, blockout_group_id, channel_scope,
    claim_range, status, reason, created_by, released_by, released_at,
    created_at, updated_at
)
SELECT
    ab.id,
    ab.property_id,
    ab.unit_id,
    CASE ab.block_type
        WHEN 'owner_hold' THEN 'OWNER_USE'
        WHEN 'maintenance' THEN 'MAINTENANCE'
        WHEN 'seasonal_closure' THEN 'SEASONAL_CLOSURE'
        WHEN 'weather_closure' THEN 'WEATHER_CLOSURE'
        ELSE 'ADMINISTRATIVE_BLOCK'
    END,
    ab.id,
    'ALL',
    ab.block_range,
    CASE WHEN ab.released_at IS NULL THEN 'ACTIVE' ELSE 'RELEASED' END,
    ab.reason,
    ab.created_by,
    ab.released_by,
    ab.released_at,
    ab.created_at,
    ab.updated_at
FROM availability_blocks ab;

DROP TABLE availability_blocks;

-- 6. Scheduled Availability Release Rules (controls per-channel sale eligibility)
CREATE TABLE availability_release_rules (
    id                   UUID PRIMARY KEY,
    property_id          UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    scope_type           TEXT NOT NULL CHECK (scope_type IN ('UNIT', 'UNIT_TYPE', 'PROPERTY')),
    unit_id              UUID NULL REFERENCES units(id) ON DELETE RESTRICT,
    unit_type_id         UUID NULL REFERENCES unit_types(id) ON DELETE RESTRICT,
    stay_range           DATERANGE NOT NULL CHECK (
                             NOT isempty(stay_range)
                             AND lower_inc(stay_range)
                             AND NOT upper_inc(stay_range)
                         ),
    channel_code         TEXT NOT NULL CHECK (channel_code ~ '^[A-Z0-9_]+$'),
    release_at           TIMESTAMPTZ NOT NULL,
    released_manually_at TIMESTAMPTZ NULL,
    released_manually_by UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    is_active            BOOLEAN NOT NULL DEFAULT TRUE,
    notes                TEXT NOT NULL DEFAULT '',
    created_by           UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    updated_by           UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    cancelled_at         TIMESTAMPTZ NULL,
    cancelled_by         UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    version              INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (
        (scope_type = 'UNIT' AND unit_id IS NOT NULL AND unit_type_id IS NULL) OR
        (scope_type = 'UNIT_TYPE' AND unit_type_id IS NOT NULL AND unit_id IS NULL) OR
        (scope_type = 'PROPERTY' AND unit_id IS NULL AND unit_type_id IS NULL)
    )
);

CREATE INDEX idx_release_rules_property_active
    ON availability_release_rules USING gist (property_id, stay_range)
    WHERE is_active = TRUE AND cancelled_at IS NULL;

-- 7. Channel Manager Connections (encrypted credentials at rest; Phase 4 adapter ready)
CREATE TABLE channel_connections (
    id                      UUID PRIMARY KEY,
    property_id             UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    provider_code           TEXT NOT NULL CHECK (provider_code ~ '^[a-z0-9_]+$'),
    display_name            TEXT NOT NULL DEFAULT '',
    auth_method             TEXT NOT NULL CHECK (auth_method IN ('api_key', 'oauth2', 'client_credentials', 'token')),
    status                  TEXT NOT NULL CHECK (status IN (
                                'NOT_CONFIGURED', 'CONFIGURATION_REQUIRED', 'CREDENTIALS_SAVED',
                                'AWAITING_AUTHORIZATION', 'CONNECTED', 'CONNECTION_ERROR', 'DISABLED'
                            )),
    public_config           JSONB NOT NULL DEFAULT '{}'::jsonb,
    encrypted_credentials   BYTEA NULL,
    credentials_key_version INTEGER NOT NULL DEFAULT 1,
    credential_hints        JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_error              TEXT NOT NULL DEFAULT '',
    credentials_updated_at  TIMESTAMPTZ NULL,
    created_by              UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    updated_by              UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    version                 INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, provider_code)
);

-- 8. Database-level append-only protection on audit_logs
CREATE OR REPLACE FUNCTION prevent_audit_log_mutation()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs is append-only; % operations are prohibited', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_audit_logs_append_only
    BEFORE UPDATE OR DELETE ON audit_logs
    FOR EACH ROW
    EXECUTE FUNCTION prevent_audit_log_mutation();
