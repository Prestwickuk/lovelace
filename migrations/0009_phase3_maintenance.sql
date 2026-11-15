-- Phase 3 Milestone 2: Maintenance Management, Facilities, Assets,
-- Preventative Schedules, Secure Attachments & Inventory Restriction Links.
--
-- Design invariants:
--   1. Maintenance requests exist independently of reservations and accommodation units
--      (location-only and communal facility issues never require a unit or booking).
--   2. Inventory restrictions linked to maintenance use blockout_groups and
--      inventory_claims via existing domain rules and GiST overlap exclusions.
--   3. Completing or inspecting a maintenance request NEVER automatically releases an
--      inventory restriction; authorised staff must explicitly release it.
--   4. Work notes, status transitions, assignments, and inspections are append-only
--      histories linked safely across user/category/schedule edits or archival.

DO $$
DECLARE
    c_name TEXT;
BEGIN
    FOR c_name IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'admin_users'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%role%'
    LOOP
        EXECUTE format('ALTER TABLE admin_users DROP CONSTRAINT %I', c_name);
    END LOOP;
    ALTER TABLE admin_users
        ADD CONSTRAINT admin_users_role_check
        CHECK (role IN (
            'owner_admin',
            'manager',
            'staff',
            'maintenance_staff',
            'housekeeping_staff',
            'viewer'
        ));
END $$;

CREATE SEQUENCE IF NOT EXISTS maintenance_ref_seq START 1;

CREATE TABLE IF NOT EXISTS maintenance_categories (
    id          UUID PRIMARY KEY,
    property_id UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    code        TEXT NOT NULL CHECK (char_length(trim(code)) > 0),
    name        TEXT NOT NULL CHECK (char_length(trim(name)) > 0),
    description TEXT NOT NULL DEFAULT '',
    is_active   BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order  INTEGER NOT NULL DEFAULT 100,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, code)
);

CREATE INDEX IF NOT EXISTS idx_maintenance_categories_property
    ON maintenance_categories (property_id, is_active, sort_order, name);

CREATE TABLE IF NOT EXISTS maintenance_locations (
    id            UUID PRIMARY KEY,
    property_id   UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    code          TEXT NOT NULL CHECK (char_length(trim(code)) > 0),
    name          TEXT NOT NULL CHECK (char_length(trim(name)) > 0),
    location_type TEXT NOT NULL CHECK (char_length(trim(location_type)) > 0),
    description   TEXT NOT NULL DEFAULT '',
    unit_id       UUID NULL REFERENCES units(id) ON DELETE SET NULL,
    is_active     BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order    INTEGER NOT NULL DEFAULT 100,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, code)
);

CREATE INDEX IF NOT EXISTS idx_maintenance_locations_property
    ON maintenance_locations (property_id, is_active, sort_order, name);

CREATE TABLE IF NOT EXISTS maintenance_assets (
    id                  UUID PRIMARY KEY,
    property_id         UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    asset_code          TEXT NOT NULL CHECK (char_length(trim(asset_code)) > 0),
    name                TEXT NOT NULL CHECK (char_length(trim(name)) > 0),
    description         TEXT NOT NULL DEFAULT '',
    location_id         UUID NULL REFERENCES maintenance_locations(id) ON DELETE SET NULL,
    unit_id             UUID NULL REFERENCES units(id) ON DELETE SET NULL,
    asset_category      TEXT NOT NULL CHECK (char_length(trim(asset_category)) > 0),
    manufacturer        TEXT NOT NULL DEFAULT '',
    model               TEXT NOT NULL DEFAULT '',
    serial_number       TEXT NOT NULL DEFAULT '',
    installed_on        DATE NULL,
    warranty_expires_on DATE NULL,
    warranty_notes      TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'RETIRED')),
    notes               TEXT NOT NULL DEFAULT '',
    created_by          UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    updated_by          UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    version             INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, asset_code)
);

CREATE INDEX IF NOT EXISTS idx_maintenance_assets_property
    ON maintenance_assets (property_id, status, name);

CREATE TABLE IF NOT EXISTS maintenance_contractors (
    id          UUID PRIMARY KEY,
    property_id UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    name        TEXT NOT NULL CHECK (char_length(trim(name)) > 0),
    company     TEXT NOT NULL DEFAULT '',
    trade       TEXT NOT NULL DEFAULT '',
    email       TEXT NOT NULL DEFAULT '',
    phone       TEXT NOT NULL DEFAULT '',
    notes       TEXT NOT NULL DEFAULT '',
    is_active   BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_maintenance_contractors_property
    ON maintenance_contractors (property_id, is_active, name);

CREATE TABLE IF NOT EXISTS maintenance_schedules (
    id                       UUID PRIMARY KEY,
    property_id              UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    title                    TEXT NOT NULL CHECK (char_length(trim(title)) > 0),
    description              TEXT NOT NULL DEFAULT '',
    category_id              UUID NOT NULL REFERENCES maintenance_categories(id) ON DELETE RESTRICT,
    location_id              UUID NULL REFERENCES maintenance_locations(id) ON DELETE SET NULL,
    unit_id                  UUID NULL REFERENCES units(id) ON DELETE SET NULL,
    asset_id                 UUID NULL REFERENCES maintenance_assets(id) ON DELETE SET NULL,
    priority                 TEXT NOT NULL DEFAULT 'NORMAL'
                                 CHECK (priority IN ('LOW', 'NORMAL', 'HIGH', 'URGENT')),
    frequency                TEXT NOT NULL
                                 CHECK (frequency IN ('ONCE', 'WEEKLY', 'MONTHLY', 'QUARTERLY', 'SIX_MONTHLY', 'ANNUALLY')),
    next_due_date            DATE NOT NULL,
    last_generated_for_date  DATE NULL,
    assigned_user_id         UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    assigned_contractor_id   UUID NULL REFERENCES maintenance_contractors(id) ON DELETE SET NULL,
    inspection_required      BOOLEAN NOT NULL DEFAULT TRUE,
    notes                    TEXT NOT NULL DEFAULT '',
    is_active                BOOLEAN NOT NULL DEFAULT TRUE,
    version                  INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_by               UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    updated_by               UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_maintenance_schedules_due
    ON maintenance_schedules (property_id, is_active, next_due_date);

CREATE TABLE IF NOT EXISTS maintenance_requests (
    id                        UUID PRIMARY KEY,
    property_id               UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    reference                 TEXT NOT NULL CHECK (char_length(trim(reference)) > 0),
    title                     TEXT NOT NULL CHECK (char_length(trim(title)) > 0),
    description               TEXT NOT NULL DEFAULT '',
    category_id               UUID NOT NULL REFERENCES maintenance_categories(id) ON DELETE RESTRICT,
    category_name_snapshot    TEXT NOT NULL DEFAULT '',
    priority                  TEXT NOT NULL DEFAULT 'NORMAL'
                                  CHECK (priority IN ('LOW', 'NORMAL', 'HIGH', 'URGENT')),
    status                    TEXT NOT NULL DEFAULT 'REPORTED'
                                  CHECK (status IN (
                                      'REPORTED',
                                      'TRIAGED',
                                      'SCHEDULED',
                                      'IN_PROGRESS',
                                      'AWAITING_PARTS',
                                      'ON_HOLD',
                                      'COMPLETED',
                                      'CANCELLED',
                                      'CLOSED'
                                  )),
    work_type                 TEXT NOT NULL DEFAULT 'REACTIVE'
                                  CHECK (work_type IN ('REACTIVE', 'PREVENTATIVE')),
    location_id               UUID NULL REFERENCES maintenance_locations(id) ON DELETE SET NULL,
    unit_id                   UUID NULL REFERENCES units(id) ON DELETE SET NULL,
    asset_id                  UUID NULL REFERENCES maintenance_assets(id) ON DELETE SET NULL,
    schedule_id               UUID NULL REFERENCES maintenance_schedules(id) ON DELETE SET NULL,
    scheduled_occurrence_date DATE NULL,
    reported_by               UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    assigned_user_id          UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    assigned_contractor_id    UUID NULL REFERENCES maintenance_contractors(id) ON DELETE SET NULL,
    reported_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    target_date               DATE NULL,
    scheduled_for             DATE NULL,
    started_at                TIMESTAMPTZ NULL,
    completed_at              TIMESTAMPTZ NULL,
    closed_at                 TIMESTAMPTZ NULL,
    guest_impact              BOOLEAN NOT NULL DEFAULT FALSE,
    safety_impact             BOOLEAN NOT NULL DEFAULT FALSE,
    availability_impact       TEXT NOT NULL DEFAULT 'NONE'
                                  CHECK (availability_impact IN ('NONE', 'PARTIAL', 'UNAVAILABLE')),
    inspection_required       BOOLEAN NOT NULL DEFAULT TRUE,
    internal_notes            TEXT NOT NULL DEFAULT '',
    version                   INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_by                UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    updated_by                UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, reference)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_maintenance_schedule_occurrence
    ON maintenance_requests (schedule_id, scheduled_occurrence_date)
    WHERE schedule_id IS NOT NULL AND scheduled_occurrence_date IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_maintenance_requests_property_status_priority
    ON maintenance_requests (property_id, status, priority, target_date);

CREATE INDEX IF NOT EXISTS idx_maintenance_requests_unit
    ON maintenance_requests (property_id, unit_id, status);

CREATE INDEX IF NOT EXISTS idx_maintenance_requests_asset
    ON maintenance_requests (property_id, asset_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_maintenance_requests_assignee
    ON maintenance_requests (property_id, assigned_user_id, status);

CREATE TABLE IF NOT EXISTS maintenance_assignments (
    id                     UUID PRIMARY KEY,
    property_id            UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    request_id             UUID NOT NULL REFERENCES maintenance_requests(id) ON DELETE CASCADE,
    assigned_user_id       UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    assigned_contractor_id UUID NULL REFERENCES maintenance_contractors(id) ON DELETE SET NULL,
    assigned_by            UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    due_date               DATE NULL,
    notes                  TEXT NOT NULL DEFAULT '',
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_maintenance_assignments_req
    ON maintenance_assignments (request_id, created_at DESC);

CREATE TABLE IF NOT EXISTS maintenance_work_entries (
    id          UUID PRIMARY KEY,
    property_id UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    request_id  UUID NOT NULL REFERENCES maintenance_requests(id) ON DELETE CASCADE,
    entry_type  TEXT NOT NULL
                    CHECK (entry_type IN (
                        'WORK_STARTED',
                        'DIAGNOSIS',
                        'WORK_PERFORMED',
                        'PARTS_USED',
                        'ADDITIONAL_WORK',
                        'DELAY_REASON',
                        'CONTRACTOR_NOTE',
                        'COMPLETION_NOTE',
                        'GENERAL_NOTE',
                        'REOPENED'
                    )),
    body        TEXT NOT NULL CHECK (char_length(trim(body)) > 0),
    parts_used  TEXT NOT NULL DEFAULT '',
    author_id   UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_maintenance_work_entries_req
    ON maintenance_work_entries (request_id, created_at ASC);

CREATE TABLE IF NOT EXISTS maintenance_inspections (
    id           UUID PRIMARY KEY,
    property_id  UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    request_id   UUID NOT NULL REFERENCES maintenance_requests(id) ON DELETE CASCADE,
    inspector_id UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    result       TEXT NOT NULL CHECK (result IN ('PASSED', 'FAILED')),
    notes        TEXT NOT NULL DEFAULT '',
    inspected_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_maintenance_inspections_req
    ON maintenance_inspections (request_id, inspected_at DESC);

CREATE TABLE IF NOT EXISTS maintenance_status_history (
    id          UUID PRIMARY KEY,
    property_id UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    request_id  UUID NOT NULL REFERENCES maintenance_requests(id) ON DELETE CASCADE,
    from_status TEXT NOT NULL DEFAULT '',
    to_status   TEXT NOT NULL CHECK (char_length(trim(to_status)) > 0),
    reason      TEXT NOT NULL DEFAULT '',
    changed_by  UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_maintenance_status_history_req
    ON maintenance_status_history (request_id, created_at ASC);

CREATE TABLE IF NOT EXISTS maintenance_attachments (
    id                UUID PRIMARY KEY,
    property_id       UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    request_id        UUID NOT NULL REFERENCES maintenance_requests(id) ON DELETE CASCADE,
    work_entry_id     UUID NULL REFERENCES maintenance_work_entries(id) ON DELETE SET NULL,
    storage_key       TEXT NOT NULL UNIQUE,
    original_filename TEXT NOT NULL CHECK (char_length(trim(original_filename)) > 0),
    content_type      TEXT NOT NULL CHECK (char_length(trim(content_type)) > 0),
    size_bytes        BIGINT NOT NULL CHECK (size_bytes > 0),
    sha256            TEXT NOT NULL CHECK (char_length(sha256) = 64),
    caption           TEXT NOT NULL DEFAULT '',
    uploaded_by       UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_maintenance_attachments_req
    ON maintenance_attachments (request_id, created_at DESC);

CREATE TABLE IF NOT EXISTS maintenance_restriction_links (
    id                UUID PRIMARY KEY,
    property_id       UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    request_id        UUID NOT NULL REFERENCES maintenance_requests(id) ON DELETE CASCADE,
    unit_id           UUID NOT NULL REFERENCES units(id) ON DELETE RESTRICT,
    blockout_group_id UUID NOT NULL REFERENCES blockout_groups(id) ON DELETE RESTRICT,
    created_by        UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    released_by       UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    released_at       TIMESTAMPTZ NULL,
    notes             TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_maintenance_restriction_links_req
    ON maintenance_restriction_links (request_id, created_at DESC);
