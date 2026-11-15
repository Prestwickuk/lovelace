-- Phase 3 Milestone 1: Operations Centre, Daily Changeover Workflow & Housekeeping Task Tracking
--
-- Design invariants:
--   1. Housekeeping / changeover state is stored independently from inventory_claims and
--      availability_blocks. Completing housekeeping never lifts a maintenance blockout, and
--      ending a reservation never automatically marks accommodation READY.
--   2. Checking out a reservation automatically generates idempotent CHANGEOVER_CLEAN
--      housekeeping tasks per assigned unit in CLEANING_REQUIRED stage.

ALTER TABLE units
    ADD COLUMN IF NOT EXISTS housekeeping_status TEXT NOT NULL DEFAULT 'READY'
        CHECK (housekeeping_status IN (
            'READY',
            'GUEST_DEPARTING',
            'CLEANING_REQUIRED',
            'CLEANING_IN_PROGRESS',
            'INSPECTION_REQUIRED'
        )),
    ADD COLUMN IF NOT EXISTS housekeeping_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS housekeeping_updated_by UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS housekeeping_tasks (
    id             UUID PRIMARY KEY,
    property_id    UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    unit_id        UUID NOT NULL REFERENCES units(id) ON DELETE RESTRICT,
    reservation_id UUID NULL REFERENCES reservations(id) ON DELETE SET NULL,
    task_type      TEXT NOT NULL DEFAULT 'CHANGEOVER_CLEAN'
                       CHECK (task_type IN (
                           'CHANGEOVER_CLEAN',
                           'INSPECTION',
                           'DEEP_CLEAN',
                           'MID_STAY_REFRESH',
                           'GENERAL'
                       )),
    stage          TEXT NOT NULL DEFAULT 'CLEANING_REQUIRED'
                       CHECK (stage IN (
                           'GUEST_DEPARTING',
                           'CLEANING_REQUIRED',
                           'CLEANING_IN_PROGRESS',
                           'INSPECTION_REQUIRED',
                           'READY'
                       )),
    priority       TEXT NOT NULL DEFAULT 'NORMAL'
                       CHECK (priority IN ('LOW', 'NORMAL', 'HIGH', 'URGENT')),
    scheduled_date DATE NOT NULL,
    due_at         TIMESTAMPTZ NULL,
    assigned_to    UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    title          TEXT NOT NULL DEFAULT '',
    notes          TEXT NOT NULL DEFAULT '',
    started_at     TIMESTAMPTZ NULL,
    started_by     UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    inspected_at   TIMESTAMPTZ NULL,
    inspected_by   UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    completed_at   TIMESTAMPTZ NULL,
    completed_by   UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    created_by     UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    updated_by     UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    version        INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_housekeeping_tasks_checkout_unit
    ON housekeeping_tasks (reservation_id, unit_id, task_type)
    WHERE reservation_id IS NOT NULL AND task_type = 'CHANGEOVER_CLEAN';

CREATE INDEX IF NOT EXISTS idx_housekeeping_tasks_property_date_stage
    ON housekeeping_tasks (property_id, scheduled_date, stage);

CREATE INDEX IF NOT EXISTS idx_housekeeping_tasks_unit_updated
    ON housekeeping_tasks (property_id, unit_id, updated_at DESC);
