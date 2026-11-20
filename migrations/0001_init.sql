-- Phase 1: Core Inventory & Administrative Foundation
-- Project: Lovelace Hospitality Booking & Operations Platform
--
-- Conventions:
--   * All timestamps stored as TIMESTAMPTZ (UTC).
--   * Half-open date intervals [start_date, end_date) using PostgreSQL DATERANGE.
--   * Monetary amounts stored as integer minor currency units (pence for GBP).
--   * Tax rates stored in integer basis points (2000 = 20.00%).
--   * Multi-property anchor (`properties`) included on all domain entities for Phase 5.

CREATE EXTENSION IF NOT EXISTS btree_gist;

-- 1. Properties (multi-site anchor)
CREATE TABLE properties (
    id              UUID PRIMARY KEY,
    slug            TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'),
    name            TEXT NOT NULL CHECK (char_length(trim(name)) > 0),
    tagline         TEXT NOT NULL DEFAULT '',
    country_code    CHAR(2) NOT NULL DEFAULT 'GB' CHECK (country_code ~ '^[A-Z]{2}$'),
    region          TEXT NOT NULL DEFAULT '',
    address_line1   TEXT NOT NULL DEFAULT '',
    address_line2   TEXT NOT NULL DEFAULT '',
    town            TEXT NOT NULL DEFAULT '',
    postcode        TEXT NOT NULL DEFAULT '',
    timezone        TEXT NOT NULL DEFAULT 'Europe/London',
    currency        CHAR(3) NOT NULL DEFAULT 'GBP' CHECK (currency ~ '^[A-Z]{3}$'),
    check_in_time   TIME NOT NULL DEFAULT '15:00:00',
    check_out_time  TIME NOT NULL DEFAULT '10:30:00',
    vat_registered  BOOLEAN NOT NULL DEFAULT FALSE,
    vat_number      TEXT NOT NULL DEFAULT '',
    contact_email   TEXT NOT NULL DEFAULT '',
    contact_phone   TEXT NOT NULL DEFAULT '',
    settings        JSONB NOT NULL DEFAULT '{}'::jsonb,
    version         INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 2. Accommodation Unit Types (tariffs / categories)
CREATE TABLE unit_types (
    id                      UUID PRIMARY KEY,
    property_id             UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    code                    TEXT NOT NULL CHECK (code ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'),
    name                    TEXT NOT NULL CHECK (char_length(trim(name)) > 0),
    category                TEXT NOT NULL CHECK (category IN ('glamping', 'tent_pitch', 'touring_pitch', 'cabin', 'other')),
    description             TEXT NOT NULL DEFAULT '',
    base_capacity_adults    INTEGER NOT NULL DEFAULT 2 CHECK (base_capacity_adults >= 1),
    max_adults              INTEGER NOT NULL DEFAULT 2 CHECK (max_adults >= 1),
    max_children            INTEGER NOT NULL DEFAULT 0 CHECK (max_children >= 0),
    max_infants             INTEGER NOT NULL DEFAULT 0 CHECK (max_infants >= 0),
    max_guests              INTEGER NOT NULL DEFAULT 2 CHECK (max_guests >= 1),
    pets_allowed            BOOLEAN NOT NULL DEFAULT FALSE,
    max_pets                INTEGER NOT NULL DEFAULT 0 CHECK (max_pets >= 0),
    base_nightly_rate_pence BIGINT NOT NULL CHECK (base_nightly_rate_pence >= 0),
    tax_rate_bps            INTEGER NOT NULL DEFAULT 0 CHECK (tax_rate_bps >= 0 AND tax_rate_bps <= 10000),
    min_nights              INTEGER NOT NULL DEFAULT 1 CHECK (min_nights >= 1),
    max_nights              INTEGER NOT NULL DEFAULT 28 CHECK (max_nights >= min_nights),
    amenities               JSONB NOT NULL DEFAULT '[]'::jsonb,
    metadata                JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_active               BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order              INTEGER NOT NULL DEFAULT 0,
    version                 INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    archived_at             TIMESTAMPTZ NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (pets_allowed OR max_pets = 0)
);

CREATE UNIQUE INDEX uq_unit_types_property_code
    ON unit_types (property_id, code)
    WHERE archived_at IS NULL;

CREATE INDEX idx_unit_types_property_active
    ON unit_types (property_id, is_active, sort_order)
    WHERE archived_at IS NULL;

-- 3. Physical Units (individual pitches, bell tents, shepherd's huts)
CREATE TABLE units (
    id                UUID PRIMARY KEY,
    property_id       UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    unit_type_id      UUID NOT NULL REFERENCES unit_types(id) ON DELETE RESTRICT,
    code              TEXT NOT NULL CHECK (code ~ '^[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*$'),
    name              TEXT NOT NULL CHECK (char_length(trim(name)) > 0),
    zone              TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'maintenance', 'inactive', 'retired')),
    access_notes      TEXT NOT NULL DEFAULT '',
    operational_notes TEXT NOT NULL DEFAULT '',
    attributes        JSONB NOT NULL DEFAULT '{}'::jsonb,
    sort_order        INTEGER NOT NULL DEFAULT 0,
    version           INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    archived_at       TIMESTAMPTZ NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX uq_units_property_code
    ON units (property_id, LOWER(code))
    WHERE archived_at IS NULL;

CREATE INDEX idx_units_property_type_status
    ON units (property_id, unit_type_id, status, sort_order)
    WHERE archived_at IS NULL;

-- 4. Extras & Add-ons Catalog (ready for Phase 2 booking attachments & Phase 3 VAT)
CREATE TABLE extras (
    id            UUID PRIMARY KEY,
    property_id   UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    code          TEXT NOT NULL CHECK (code ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'),
    name          TEXT NOT NULL CHECK (char_length(trim(name)) > 0),
    description   TEXT NOT NULL DEFAULT '',
    pricing_model TEXT NOT NULL CHECK (pricing_model IN ('per_stay', 'per_night', 'per_person_per_stay', 'per_person_per_night')),
    price_pence   BIGINT NOT NULL CHECK (price_pence >= 0),
    tax_rate_bps  INTEGER NOT NULL DEFAULT 0 CHECK (tax_rate_bps >= 0 AND tax_rate_bps <= 10000),
    max_quantity  INTEGER NOT NULL DEFAULT 1 CHECK (max_quantity >= 1),
    is_active     BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order    INTEGER NOT NULL DEFAULT 0,
    metadata      JSONB NOT NULL DEFAULT '{}'::jsonb,
    version       INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    archived_at   TIMESTAMPTZ NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX uq_extras_property_code
    ON extras (property_id, code)
    WHERE archived_at IS NULL;

-- 5. Rate Plans & Seasonal Rate Rules
CREATE TABLE rate_plans (
    id          UUID PRIMARY KEY,
    property_id UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    code        TEXT NOT NULL CHECK (code ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'),
    name        TEXT NOT NULL CHECK (char_length(trim(name)) > 0),
    description TEXT NOT NULL DEFAULT '',
    is_default  BOOLEAN NOT NULL DEFAULT FALSE,
    is_active   BOOLEAN NOT NULL DEFAULT TRUE,
    priority    INTEGER NOT NULL DEFAULT 100,
    version     INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    archived_at TIMESTAMPTZ NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX uq_rate_plans_property_code
    ON rate_plans (property_id, code)
    WHERE archived_at IS NULL;

CREATE UNIQUE INDEX uq_rate_plans_default_per_property
    ON rate_plans (property_id)
    WHERE is_default = TRUE AND archived_at IS NULL;

CREATE TABLE rate_rules (
    id                  UUID PRIMARY KEY,
    property_id         UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    rate_plan_id        UUID NOT NULL REFERENCES rate_plans(id) ON DELETE RESTRICT,
    unit_type_id        UUID NOT NULL REFERENCES unit_types(id) ON DELETE RESTRICT,
    name                TEXT NOT NULL CHECK (char_length(trim(name)) > 0),
    date_range          DATERANGE NOT NULL CHECK (
                            NOT isempty(date_range)
                            AND lower_inc(date_range)
                            AND NOT upper_inc(date_range)
                        ),
    -- Bitmask: Mon=1, Tue=2, Wed=4, Thu=8, Fri=16, Sat=32, Sun=64 (127 = every day)
    days_of_week        SMALLINT NOT NULL DEFAULT 127 CHECK (days_of_week >= 1 AND days_of_week <= 127),
    nightly_rate_pence  BIGINT NOT NULL CHECK (nightly_rate_pence >= 0),
    min_nights_override INTEGER NULL CHECK (min_nights_override IS NULL OR min_nights_override >= 1),
    priority            INTEGER NOT NULL DEFAULT 100,
    is_active           BOOLEAN NOT NULL DEFAULT TRUE,
    version             INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    archived_at         TIMESTAMPTZ NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_rate_rules_lookup
    ON rate_rules USING gist (property_id, rate_plan_id, unit_type_id, date_range)
    WHERE archived_at IS NULL AND is_active = TRUE;

-- 6. Operational Availability Blocks (maintenance, owner holds, seasonal closures)
-- Phase 2 `booking_allocations` will mirror this exact DATERANGE + GIST pattern.
CREATE TABLE availability_blocks (
    id          UUID PRIMARY KEY,
    property_id UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    unit_id     UUID NOT NULL REFERENCES units(id) ON DELETE RESTRICT,
    block_range DATERANGE NOT NULL CHECK (
                    NOT isempty(block_range)
                    AND lower_inc(block_range)
                    AND NOT upper_inc(block_range)
                ),
    block_type  TEXT NOT NULL CHECK (block_type IN ('maintenance', 'owner_hold', 'seasonal_closure', 'weather_closure')),
    reason      TEXT NOT NULL DEFAULT '',
    created_by  UUID NULL,
    released_at TIMESTAMPTZ NULL,
    released_by UUID NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT excl_availability_blocks_no_overlap
        EXCLUDE USING gist (unit_id WITH =, block_range WITH &&)
        WHERE (released_at IS NULL)
);

CREATE INDEX idx_availability_blocks_property_active
    ON availability_blocks (property_id, unit_id)
    WHERE released_at IS NULL;

-- 7. Administrative Authentication, RBAC & Sessions
CREATE TABLE admin_users (
    id                   UUID PRIMARY KEY,
    property_id          UUID NULL REFERENCES properties(id) ON DELETE RESTRICT,
    email                TEXT NOT NULL CHECK (char_length(trim(email)) > 3),
    full_name            TEXT NOT NULL CHECK (char_length(trim(full_name)) > 0),
    role                 TEXT NOT NULL CHECK (role IN ('owner_admin', 'manager', 'staff', 'viewer')),
    password_hash        TEXT NOT NULL,
    is_active            BOOLEAN NOT NULL DEFAULT TRUE,
    must_rotate_password BOOLEAN NOT NULL DEFAULT FALSE,
    last_login_at        TIMESTAMPTZ NULL,
    version              INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX uq_admin_users_email_lower
    ON admin_users (LOWER(email));

ALTER TABLE availability_blocks
    ADD CONSTRAINT fk_availability_blocks_created_by
        FOREIGN KEY (created_by) REFERENCES admin_users(id) ON DELETE SET NULL,
    ADD CONSTRAINT fk_availability_blocks_released_by
        FOREIGN KEY (released_by) REFERENCES admin_users(id) ON DELETE SET NULL;

CREATE TABLE admin_sessions (
    id           UUID PRIMARY KEY,
    user_id      UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    token_hash   BYTEA NOT NULL UNIQUE,
    csrf_token   TEXT NOT NULL,
    ip_address   TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT '',
    expires_at   TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at   TIMESTAMPTZ NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_admin_sessions_user_active
    ON admin_sessions (user_id, expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE login_attempts (
    id           BIGSERIAL PRIMARY KEY,
    email        TEXT NOT NULL,
    ip_address   TEXT NOT NULL,
    succeeded    BOOLEAN NOT NULL,
    attempted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_login_attempts_ip_time ON login_attempts (ip_address, attempted_at DESC);
CREATE INDEX idx_login_attempts_email_time ON login_attempts (LOWER(email), attempted_at DESC);

-- 8. Immutable Audit Trail
CREATE TABLE audit_logs (
    id            BIGSERIAL PRIMARY KEY,
    property_id   UUID NULL REFERENCES properties(id) ON DELETE RESTRICT,
    actor_user_id UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    actor_email   TEXT NOT NULL DEFAULT '',
    actor_role    TEXT NOT NULL DEFAULT '',
    action        TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id   TEXT NOT NULL DEFAULT '',
    request_id    TEXT NOT NULL DEFAULT '',
    ip_address    TEXT NOT NULL DEFAULT '',
    user_agent    TEXT NOT NULL DEFAULT '',
    before_state  JSONB NULL,
    after_state   JSONB NULL,
    metadata      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_logs_property_created
    ON audit_logs (property_id, created_at DESC);

CREATE INDEX idx_audit_logs_resource
    ON audit_logs (resource_type, resource_id, created_at DESC);
