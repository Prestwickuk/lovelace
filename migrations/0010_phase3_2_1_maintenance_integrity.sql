-- Phase 3.2.1: Maintenance property-level composite integrity constraints & preventative schedule anchor day.
-- Enforces tenant/property isolation at the PostgreSQL schema layer across locations, assets,
-- categories, contractors, requests, assignments, work entries, inspections, history,
-- attachments (including matching work_entry_id -> request_id + property_id), restriction links,
-- and preventative maintenance schedules.

ALTER TABLE maintenance_schedules
    ADD COLUMN IF NOT EXISTS anchor_day SMALLINT CHECK (anchor_day BETWEEN 1 AND 31);

UPDATE maintenance_schedules
SET anchor_day = EXTRACT(DAY FROM next_due_date)::SMALLINT
WHERE anchor_day IS NULL AND next_due_date IS NOT NULL;

ALTER TABLE units
    ADD CONSTRAINT uq_units_property_id_id UNIQUE (property_id, id);

ALTER TABLE blockout_groups
    ADD CONSTRAINT uq_blockout_groups_property_id_id UNIQUE (property_id, id);

ALTER TABLE maintenance_categories
    ADD CONSTRAINT uq_maintenance_categories_property_id_id UNIQUE (property_id, id);

ALTER TABLE maintenance_locations
    ADD CONSTRAINT uq_maintenance_locations_property_id_id UNIQUE (property_id, id);

ALTER TABLE maintenance_assets
    ADD CONSTRAINT uq_maintenance_assets_property_id_id UNIQUE (property_id, id);

ALTER TABLE maintenance_contractors
    ADD CONSTRAINT uq_maintenance_contractors_property_id_id UNIQUE (property_id, id);

ALTER TABLE maintenance_schedules
    ADD CONSTRAINT uq_maintenance_schedules_property_id_id UNIQUE (property_id, id);

ALTER TABLE maintenance_requests
    ADD CONSTRAINT uq_maintenance_requests_property_id_id UNIQUE (property_id, id);

ALTER TABLE maintenance_work_entries
    ADD CONSTRAINT uq_maintenance_work_entries_property_request_id UNIQUE (property_id, request_id, id);

-- maintenance_locations -> units
ALTER TABLE maintenance_locations
    DROP CONSTRAINT IF EXISTS maintenance_locations_unit_id_fkey,
    ADD CONSTRAINT maintenance_locations_property_unit_fkey
        FOREIGN KEY (property_id, unit_id)
        REFERENCES units (property_id, id)
        ON DELETE SET NULL (unit_id);

-- maintenance_assets -> maintenance_locations, units
ALTER TABLE maintenance_assets
    DROP CONSTRAINT IF EXISTS maintenance_assets_location_id_fkey,
    ADD CONSTRAINT maintenance_assets_property_location_fkey
        FOREIGN KEY (property_id, location_id)
        REFERENCES maintenance_locations (property_id, id)
        ON DELETE SET NULL (location_id),
    DROP CONSTRAINT IF EXISTS maintenance_assets_unit_id_fkey,
    ADD CONSTRAINT maintenance_assets_property_unit_fkey
        FOREIGN KEY (property_id, unit_id)
        REFERENCES units (property_id, id)
        ON DELETE SET NULL (unit_id);

-- maintenance_schedules -> categories, locations, units, assets, contractors
ALTER TABLE maintenance_schedules
    DROP CONSTRAINT IF EXISTS maintenance_schedules_category_id_fkey,
    ADD CONSTRAINT maintenance_schedules_property_category_fkey
        FOREIGN KEY (property_id, category_id)
        REFERENCES maintenance_categories (property_id, id)
        ON DELETE RESTRICT,
    DROP CONSTRAINT IF EXISTS maintenance_schedules_location_id_fkey,
    ADD CONSTRAINT maintenance_schedules_property_location_fkey
        FOREIGN KEY (property_id, location_id)
        REFERENCES maintenance_locations (property_id, id)
        ON DELETE SET NULL (location_id),
    DROP CONSTRAINT IF EXISTS maintenance_schedules_unit_id_fkey,
    ADD CONSTRAINT maintenance_schedules_property_unit_fkey
        FOREIGN KEY (property_id, unit_id)
        REFERENCES units (property_id, id)
        ON DELETE SET NULL (unit_id),
    DROP CONSTRAINT IF EXISTS maintenance_schedules_asset_id_fkey,
    ADD CONSTRAINT maintenance_schedules_property_asset_fkey
        FOREIGN KEY (property_id, asset_id)
        REFERENCES maintenance_assets (property_id, id)
        ON DELETE SET NULL (asset_id),
    DROP CONSTRAINT IF EXISTS maintenance_schedules_assigned_contractor_id_fkey,
    ADD CONSTRAINT maintenance_schedules_property_contractor_fkey
        FOREIGN KEY (property_id, assigned_contractor_id)
        REFERENCES maintenance_contractors (property_id, id)
        ON DELETE SET NULL (assigned_contractor_id);

-- maintenance_requests -> categories, locations, units, assets, schedules, contractors
ALTER TABLE maintenance_requests
    DROP CONSTRAINT IF EXISTS maintenance_requests_category_id_fkey,
    ADD CONSTRAINT maintenance_requests_property_category_fkey
        FOREIGN KEY (property_id, category_id)
        REFERENCES maintenance_categories (property_id, id)
        ON DELETE RESTRICT,
    DROP CONSTRAINT IF EXISTS maintenance_requests_location_id_fkey,
    ADD CONSTRAINT maintenance_requests_property_location_fkey
        FOREIGN KEY (property_id, location_id)
        REFERENCES maintenance_locations (property_id, id)
        ON DELETE SET NULL (location_id),
    DROP CONSTRAINT IF EXISTS maintenance_requests_unit_id_fkey,
    ADD CONSTRAINT maintenance_requests_property_unit_fkey
        FOREIGN KEY (property_id, unit_id)
        REFERENCES units (property_id, id)
        ON DELETE SET NULL (unit_id),
    DROP CONSTRAINT IF EXISTS maintenance_requests_asset_id_fkey,
    ADD CONSTRAINT maintenance_requests_property_asset_fkey
        FOREIGN KEY (property_id, asset_id)
        REFERENCES maintenance_assets (property_id, id)
        ON DELETE SET NULL (asset_id),
    DROP CONSTRAINT IF EXISTS maintenance_requests_schedule_id_fkey,
    ADD CONSTRAINT maintenance_requests_property_schedule_fkey
        FOREIGN KEY (property_id, schedule_id)
        REFERENCES maintenance_schedules (property_id, id)
        ON DELETE SET NULL (schedule_id),
    DROP CONSTRAINT IF EXISTS maintenance_requests_assigned_contractor_id_fkey,
    ADD CONSTRAINT maintenance_requests_property_contractor_fkey
        FOREIGN KEY (property_id, assigned_contractor_id)
        REFERENCES maintenance_contractors (property_id, id)
        ON DELETE SET NULL (assigned_contractor_id);

-- maintenance_assignments -> requests, contractors
ALTER TABLE maintenance_assignments
    DROP CONSTRAINT IF EXISTS maintenance_assignments_request_id_fkey,
    ADD CONSTRAINT maintenance_assignments_property_request_fkey
        FOREIGN KEY (property_id, request_id)
        REFERENCES maintenance_requests (property_id, id)
        ON DELETE CASCADE,
    DROP CONSTRAINT IF EXISTS maintenance_assignments_assigned_contractor_id_fkey,
    ADD CONSTRAINT maintenance_assignments_property_contractor_fkey
        FOREIGN KEY (property_id, assigned_contractor_id)
        REFERENCES maintenance_contractors (property_id, id)
        ON DELETE SET NULL (assigned_contractor_id);

-- maintenance_work_entries -> requests
ALTER TABLE maintenance_work_entries
    DROP CONSTRAINT IF EXISTS maintenance_work_entries_request_id_fkey,
    ADD CONSTRAINT maintenance_work_entries_property_request_fkey
        FOREIGN KEY (property_id, request_id)
        REFERENCES maintenance_requests (property_id, id)
        ON DELETE CASCADE;

-- maintenance_inspections -> requests
ALTER TABLE maintenance_inspections
    DROP CONSTRAINT IF EXISTS maintenance_inspections_request_id_fkey,
    ADD CONSTRAINT maintenance_inspections_property_request_fkey
        FOREIGN KEY (property_id, request_id)
        REFERENCES maintenance_requests (property_id, id)
        ON DELETE CASCADE;

-- maintenance_status_history -> requests
ALTER TABLE maintenance_status_history
    DROP CONSTRAINT IF EXISTS maintenance_status_history_request_id_fkey,
    ADD CONSTRAINT maintenance_status_history_property_request_fkey
        FOREIGN KEY (property_id, request_id)
        REFERENCES maintenance_requests (property_id, id)
        ON DELETE CASCADE;

-- maintenance_attachments -> requests, work_entries (same property + request)
ALTER TABLE maintenance_attachments
    DROP CONSTRAINT IF EXISTS maintenance_attachments_request_id_fkey,
    ADD CONSTRAINT maintenance_attachments_property_request_fkey
        FOREIGN KEY (property_id, request_id)
        REFERENCES maintenance_requests (property_id, id)
        ON DELETE CASCADE,
    DROP CONSTRAINT IF EXISTS maintenance_attachments_work_entry_id_fkey,
    ADD CONSTRAINT maintenance_attachments_property_request_work_entry_fkey
        FOREIGN KEY (property_id, request_id, work_entry_id)
        REFERENCES maintenance_work_entries (property_id, request_id, id)
        ON DELETE SET NULL (work_entry_id);

-- maintenance_restriction_links -> requests, units, blockout_groups
ALTER TABLE maintenance_restriction_links
    DROP CONSTRAINT IF EXISTS maintenance_restriction_links_request_id_fkey,
    ADD CONSTRAINT maintenance_restriction_links_property_request_fkey
        FOREIGN KEY (property_id, request_id)
        REFERENCES maintenance_requests (property_id, id)
        ON DELETE CASCADE,
    DROP CONSTRAINT IF EXISTS maintenance_restriction_links_unit_id_fkey,
    ADD CONSTRAINT maintenance_restriction_links_property_unit_fkey
        FOREIGN KEY (property_id, unit_id)
        REFERENCES units (property_id, id)
        ON DELETE RESTRICT,
    DROP CONSTRAINT IF EXISTS maintenance_restriction_links_blockout_group_id_fkey,
    ADD CONSTRAINT maintenance_restriction_links_property_blockout_group_fkey
        FOREIGN KEY (property_id, blockout_group_id)
        REFERENCES blockout_groups (property_id, id)
        ON DELETE RESTRICT;
