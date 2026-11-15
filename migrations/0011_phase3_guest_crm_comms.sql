-- Phase 3 Milestone 3: Guest Management (CRM), Enquiries, Communications,
-- Templates, Automated Booking Comms, Marketing Consent & Data Retention.

-- Composite uniqueness anchors for strict property-isolated foreign keys.
ALTER TABLE guests
    ADD CONSTRAINT uq_guests_property_id_id UNIQUE (property_id, id);

ALTER TABLE reservations
    ADD CONSTRAINT uq_reservations_property_id_id UNIQUE (property_id, id);

-- Extend guests with CRM contact metadata, normalized search keys, and lifecycle state.
ALTER TABLE guests
    ADD COLUMN IF NOT EXISTS first_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS normalized_email TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS normalized_phone TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS preferred_contact_method TEXT NOT NULL DEFAULT 'EMAIL'
        CHECK (preferred_contact_method IN ('EMAIL', 'PHONE', 'SMS', 'POST')),
    ADD COLUMN IF NOT EXISTS preferred_language TEXT NOT NULL DEFAULT 'en',
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'ACTIVE'
        CHECK (status IN ('ACTIVE', 'VIP', 'DO_NOT_CONTACT', 'MERGED', 'ANONYMIZED')),
    ADD COLUMN IF NOT EXISTS merged_into_guest_id UUID NULL,
    ADD COLUMN IF NOT EXISTS merged_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS anonymized_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS created_by UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS updated_by UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL;

ALTER TABLE guests
    ADD CONSTRAINT guests_property_merged_target_fkey
        FOREIGN KEY (property_id, merged_into_guest_id)
        REFERENCES guests (property_id, id)
        ON DELETE RESTRICT;

UPDATE guests
SET normalized_email = lower(trim(email)),
    normalized_phone = regexp_replace(trim(phone), '[^0-9+]', '', 'g'),
    first_name = CASE
        WHEN first_name = '' AND strpos(trim(full_name), ' ') > 0
            THEN split_part(trim(full_name), ' ', 1)
        WHEN first_name = '' THEN trim(full_name)
        ELSE first_name
    END,
    last_name = CASE
        WHEN last_name = '' AND strpos(trim(full_name), ' ') > 0
            THEN trim(substring(trim(full_name) from strpos(trim(full_name), ' ') + 1))
        ELSE last_name
    END;

CREATE INDEX IF NOT EXISTS idx_guests_property_norm_email
    ON guests (property_id, normalized_email)
    WHERE normalized_email <> '';

CREATE INDEX IF NOT EXISTS idx_guests_property_norm_phone
    ON guests (property_id, normalized_phone)
    WHERE normalized_phone <> '';

CREATE INDEX IF NOT EXISTS idx_guests_property_status
    ON guests (property_id, status, updated_at DESC);

-- Distinguish primary booking contact from additional occupant details on reservations.
ALTER TABLE reservations
    ADD COLUMN IF NOT EXISTS occupant_details TEXT NOT NULL DEFAULT '';

-- Structured operational preferences for returning guests (separate from booking requests).
CREATE TABLE IF NOT EXISTS guest_preferences (
    id               UUID PRIMARY KEY,
    property_id      UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    guest_id         UUID NOT NULL,
    category         TEXT NOT NULL CHECK (category IN (
        'ACCOMMODATION', 'PET', 'ARRIVAL', 'EQUIPMENT', 'COMMUNICATION', 'ACCESSIBILITY', 'OTHER'
    )),
    preference_key   TEXT NOT NULL CHECK (char_length(trim(preference_key)) > 0),
    preference_value TEXT NOT NULL DEFAULT '',
    notes            TEXT NOT NULL DEFAULT '',
    created_by       UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    updated_by       UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, id),
    CONSTRAINT guest_preferences_property_guest_fkey
        FOREIGN KEY (property_id, guest_id)
        REFERENCES guests (property_id, id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_guest_preferences_guest
    ON guest_preferences (property_id, guest_id, category, preference_key);

-- Internal staff CRM notes (NEVER exposed to guests or outbound communications).
CREATE TABLE IF NOT EXISTS guest_notes (
    id             UUID PRIMARY KEY,
    property_id    UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    guest_id       UUID NOT NULL,
    reservation_id UUID NULL,
    category       TEXT NOT NULL CHECK (category IN (
        'GENERAL', 'BOOKING', 'PREFERENCE', 'CUSTOMER_SERVICE', 'OPERATIONAL'
    )),
    content        TEXT NOT NULL CHECK (char_length(trim(content)) > 0),
    is_pinned      BOOLEAN NOT NULL DEFAULT FALSE,
    created_by     UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    updated_by     UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, id),
    CONSTRAINT guest_notes_property_guest_fkey
        FOREIGN KEY (property_id, guest_id)
        REFERENCES guests (property_id, id)
        ON DELETE CASCADE,
    CONSTRAINT guest_notes_property_reservation_fkey
        FOREIGN KEY (property_id, reservation_id)
        REFERENCES reservations (property_id, id)
        ON DELETE SET NULL (reservation_id)
);

CREATE INDEX IF NOT EXISTS idx_guest_notes_guest
    ON guest_notes (property_id, guest_id, is_pinned DESC, created_at DESC);

-- Audit snapshot ledger for explicit non-destructive duplicate guest merges.
CREATE TABLE IF NOT EXISTS guest_merge_events (
    id              UUID PRIMARY KEY,
    property_id     UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    source_guest_id UUID NOT NULL,
    target_guest_id UUID NOT NULL,
    source_snapshot JSONB NOT NULL,
    moved_counts    JSONB NOT NULL DEFAULT '{}'::jsonb,
    notes           TEXT NOT NULL DEFAULT '',
    merged_by       UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    merged_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT guest_merge_distinct CHECK (source_guest_id <> target_guest_id),
    CONSTRAINT guest_merge_source_fkey
        FOREIGN KEY (property_id, source_guest_id)
        REFERENCES guests (property_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT guest_merge_target_fkey
        FOREIGN KEY (property_id, target_guest_id)
        REFERENCES guests (property_id, id)
        ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS guest_duplicate_dismissals (
    property_id  UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    guest_a_id   UUID NOT NULL,
    guest_b_id   UUID NOT NULL,
    reason       TEXT NOT NULL DEFAULT '',
    dismissed_by UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    dismissed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (property_id, guest_a_id, guest_b_id),
    CHECK (guest_a_id < guest_b_id),
    FOREIGN KEY (property_id, guest_a_id) REFERENCES guests (property_id, id) ON DELETE CASCADE,
    FOREIGN KEY (property_id, guest_b_id) REFERENCES guests (property_id, id) ON DELETE CASCADE
);

-- Guest enquiries, special requests, lost property & complaints.
CREATE SEQUENCE IF NOT EXISTS enquiry_ref_seq START 1;

CREATE TABLE IF NOT EXISTS guest_enquiries (
    id                     UUID PRIMARY KEY,
    property_id            UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    reference              TEXT NOT NULL UNIQUE,
    guest_id               UUID NULL,
    reservation_id         UUID NULL,
    maintenance_request_id UUID NULL,
    category               TEXT NOT NULL CHECK (category IN (
        'GENERAL', 'BOOKING', 'AVAILABILITY', 'ACCOMMODATION', 'FACILITIES',
        'ARRIVAL_DEPARTURE', 'SPECIAL_REQUEST', 'GROUP_BOOKING', 'CANCELLATION',
        'COMPLAINT', 'LOST_PROPERTY', 'OTHER'
    )),
    status                 TEXT NOT NULL DEFAULT 'NEW' CHECK (status IN (
        'NEW', 'TRIAGED', 'IN_PROGRESS', 'WAITING_FOR_GUEST', 'WAITING_FOR_PROPERTY', 'RESOLVED', 'CLOSED'
    )),
    priority               TEXT NOT NULL DEFAULT 'NORMAL' CHECK (priority IN ('LOW', 'NORMAL', 'HIGH', 'URGENT')),
    source                 TEXT NOT NULL DEFAULT 'ADMIN' CHECK (source IN ('PHONE', 'IN_PERSON', 'EMAIL', 'WEB', 'ADMIN')),
    subject                TEXT NOT NULL CHECK (char_length(trim(subject)) > 0),
    description            TEXT NOT NULL DEFAULT '',
    requester_name         TEXT NOT NULL DEFAULT '',
    requester_email        TEXT NOT NULL DEFAULT '',
    requester_phone        TEXT NOT NULL DEFAULT '',
    assigned_user_id       UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    target_response_at     TIMESTAMPTZ NULL,
    follow_up_at           TIMESTAMPTZ NULL,
    resolved_at            TIMESTAMPTZ NULL,
    resolved_by            UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    resolution_notes       TEXT NOT NULL DEFAULT '',
    closed_at              TIMESTAMPTZ NULL,
    closed_by              UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    version                INT NOT NULL DEFAULT 1,
    created_by             UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    updated_by             UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, id),
    CONSTRAINT guest_enquiries_property_guest_fkey
        FOREIGN KEY (property_id, guest_id)
        REFERENCES guests (property_id, id)
        ON DELETE SET NULL (guest_id),
    CONSTRAINT guest_enquiries_property_reservation_fkey
        FOREIGN KEY (property_id, reservation_id)
        REFERENCES reservations (property_id, id)
        ON DELETE SET NULL (reservation_id),
    CONSTRAINT guest_enquiries_property_maint_fkey
        FOREIGN KEY (property_id, maintenance_request_id)
        REFERENCES maintenance_requests (property_id, id)
        ON DELETE SET NULL (maintenance_request_id)
);

CREATE INDEX IF NOT EXISTS idx_guest_enquiries_property_status
    ON guest_enquiries (property_id, status, priority, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_guest_enquiries_guest
    ON guest_enquiries (property_id, guest_id, created_at DESC)
    WHERE guest_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_guest_enquiries_followup
    ON guest_enquiries (property_id, follow_up_at)
    WHERE status NOT IN ('RESOLVED', 'CLOSED') AND follow_up_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS guest_enquiry_messages (
    id            UUID PRIMARY KEY,
    property_id   UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    enquiry_id    UUID NOT NULL,
    direction     TEXT NOT NULL CHECK (direction IN ('INBOUND', 'OUTBOUND', 'INTERNAL')),
    sender_name   TEXT NOT NULL DEFAULT '',
    sender_email  TEXT NOT NULL DEFAULT '',
    body          TEXT NOT NULL CHECK (char_length(trim(body)) > 0),
    is_internal   BOOLEAN NOT NULL DEFAULT FALSE,
    staff_user_id UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT guest_enquiry_messages_property_enquiry_fkey
        FOREIGN KEY (property_id, enquiry_id)
        REFERENCES guest_enquiries (property_id, id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_guest_enquiry_messages_enquiry
    ON guest_enquiry_messages (property_id, enquiry_id, created_at ASC);

-- Reusable templates + immutable sent version snapshots.
CREATE TABLE IF NOT EXISTS communication_templates (
    id                 UUID PRIMARY KEY,
    property_id        UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    code               TEXT NOT NULL CHECK (char_length(trim(code)) > 0),
    name               TEXT NOT NULL CHECK (char_length(trim(name)) > 0),
    category           TEXT NOT NULL CHECK (category IN (
        'BOOKING_CONFIRMATION', 'PRE_ARRIVAL', 'ARRIVAL_INFO',
        'DEPARTURE_INSTRUCTIONS', 'POST_STAY_THANK_YOU', 'PAYMENT_REMINDER',
        'ENQUIRY_RESPONSE', 'GENERAL'
    )),
    locale             TEXT NOT NULL DEFAULT 'en' CHECK (char_length(trim(locale)) > 0),
    subject_template   TEXT NOT NULL,
    body_text_template TEXT NOT NULL,
    body_html_template TEXT NOT NULL DEFAULT '',
    is_active          BOOLEAN NOT NULL DEFAULT TRUE,
    version            INT NOT NULL DEFAULT 1,
    created_by         UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    updated_by         UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, id),
    UNIQUE (property_id, code, locale)
);

CREATE TABLE IF NOT EXISTS communication_template_versions (
    id                 UUID PRIMARY KEY,
    property_id        UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    template_id        UUID NOT NULL,
    version            INT NOT NULL,
    subject_template   TEXT NOT NULL,
    body_text_template TEXT NOT NULL,
    body_html_template TEXT NOT NULL DEFAULT '',
    edited_by          UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, id),
    UNIQUE (template_id, version),
    CONSTRAINT comm_template_versions_property_template_fkey
        FOREIGN KEY (property_id, template_id)
        REFERENCES communication_templates (property_id, id)
        ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS communication_automation_rules (
    id                  UUID PRIMARY KEY,
    property_id         UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    name                TEXT NOT NULL CHECK (char_length(trim(name)) > 0),
    trigger_event       TEXT NOT NULL CHECK (trigger_event IN (
        'BOOKING_CONFIRMATION', 'PRE_ARRIVAL', 'ARRIVAL_INFO',
        'DEPARTURE_INSTRUCTIONS', 'POST_STAY_THANK_YOU', 'PAYMENT_REMINDER'
    )),
    offset_hours        INT NOT NULL DEFAULT 0,
    send_hour_local     SMALLINT NULL CHECK (send_hour_local IS NULL OR send_hour_local BETWEEN 0 AND 23),
    template_id         UUID NOT NULL,
    applicable_statuses TEXT[] NOT NULL DEFAULT ARRAY['CONFIRMED']::TEXT[],
    is_active           BOOLEAN NOT NULL DEFAULT TRUE,
    created_by          UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    updated_by          UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, id),
    CONSTRAINT comm_automation_rules_property_template_fkey
        FOREIGN KEY (property_id, template_id)
        REFERENCES communication_templates (property_id, id)
        ON DELETE RESTRICT
);

-- Unified guest communication timeline (inbound, outbound, phone, in-person).
CREATE TABLE IF NOT EXISTS guest_communications (
    id                    UUID PRIMARY KEY,
    property_id           UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    guest_id              UUID NULL,
    reservation_id        UUID NULL,
    enquiry_id            UUID NULL,
    interaction_type      TEXT NOT NULL CHECK (interaction_type IN (
        'EMAIL_OUTBOUND', 'EMAIL_INBOUND', 'PHONE_CALL', 'IN_PERSON',
        'WEBSITE_ENQUIRY', 'BOOKING_NOTIFICATION', 'SYSTEM_NOTIFICATION', 'COMPLAINT'
    )),
    direction             TEXT NOT NULL CHECK (direction IN ('INBOUND', 'OUTBOUND', 'INTERNAL')),
    subject               TEXT NOT NULL DEFAULT '',
    body_text             TEXT NOT NULL DEFAULT '',
    body_html             TEXT NOT NULL DEFAULT '',
    summary               TEXT NOT NULL DEFAULT '',
    recipient_email       TEXT NOT NULL DEFAULT '',
    sender_email          TEXT NOT NULL DEFAULT '',
    template_id           UUID NULL,
    template_version_id   UUID NULL,
    rule_id               UUID NULL,
    trigger_event         TEXT NOT NULL DEFAULT '',
    email_outbox_id       UUID NULL REFERENCES email_outbox(id) ON DELETE SET NULL,
    delivery_status       TEXT NOT NULL DEFAULT 'LOGGED' CHECK (delivery_status IN (
        'DRAFT', 'QUEUED', 'SENDING', 'ACCEPTED', 'DELIVERED', 'BOUNCED', 'FAILED', 'LOGGED'
    )),
    provider_message_id   TEXT NOT NULL DEFAULT '',
    follow_up_required    BOOLEAN NOT NULL DEFAULT FALSE,
    follow_up_assigned_to UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    follow_up_at          TIMESTAMPTZ NULL,
    follow_up_completed_at TIMESTAMPTZ NULL,
    staff_user_id         UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    occurred_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, id),
    CONSTRAINT guest_comms_property_guest_fkey
        FOREIGN KEY (property_id, guest_id)
        REFERENCES guests (property_id, id)
        ON DELETE SET NULL (guest_id),
    CONSTRAINT guest_comms_property_reservation_fkey
        FOREIGN KEY (property_id, reservation_id)
        REFERENCES reservations (property_id, id)
        ON DELETE SET NULL (reservation_id),
    CONSTRAINT guest_comms_property_enquiry_fkey
        FOREIGN KEY (property_id, enquiry_id)
        REFERENCES guest_enquiries (property_id, id)
        ON DELETE SET NULL (enquiry_id),
    CONSTRAINT guest_comms_property_template_fkey
        FOREIGN KEY (property_id, template_id)
        REFERENCES communication_templates (property_id, id)
        ON DELETE SET NULL (template_id),
    CONSTRAINT guest_comms_property_template_ver_fkey
        FOREIGN KEY (property_id, template_version_id)
        REFERENCES communication_template_versions (property_id, id)
        ON DELETE SET NULL (template_version_id),
    CONSTRAINT guest_comms_property_rule_fkey
        FOREIGN KEY (property_id, rule_id)
        REFERENCES communication_automation_rules (property_id, id)
        ON DELETE SET NULL (rule_id)
);

CREATE INDEX IF NOT EXISTS idx_guest_comms_guest_occurred
    ON guest_communications (property_id, guest_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_guest_comms_reservation
    ON guest_communications (property_id, reservation_id, occurred_at DESC)
    WHERE reservation_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_guest_comms_followup
    ON guest_communications (property_id, follow_up_at)
    WHERE follow_up_required = TRUE AND follow_up_completed_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_guest_comms_delivery_status
    ON guest_communications (property_id, delivery_status, occurred_at DESC);

-- Extend existing authoritative email_outbox so standalone guest/enquiry emails share the same queue.
ALTER TABLE email_outbox
    ALTER COLUMN reservation_id DROP NOT NULL,
    ADD COLUMN IF NOT EXISTS guest_id UUID NULL,
    ADD COLUMN IF NOT EXISTS communication_id UUID NULL;

ALTER TABLE email_outbox
    ADD CONSTRAINT email_outbox_property_guest_fkey
        FOREIGN KEY (property_id, guest_id)
        REFERENCES guests (property_id, id)
        ON DELETE SET NULL (guest_id),
    ADD CONSTRAINT email_outbox_property_comm_fkey
        FOREIGN KEY (property_id, communication_id)
        REFERENCES guest_communications (property_id, id)
        ON DELETE SET NULL (communication_id);

-- Provider / SMTP delivery lifecycle events (separate SMTP acceptance vs webhook delivery/bounce).
CREATE TABLE IF NOT EXISTS communication_delivery_events (
    id                UUID PRIMARY KEY,
    property_id       UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    communication_id  UUID NOT NULL,
    email_outbox_id   UUID NULL REFERENCES email_outbox(id) ON DELETE SET NULL,
    event_type        TEXT NOT NULL CHECK (event_type IN (
        'QUEUED', 'ACCEPTED', 'DELIVERED', 'BOUNCED', 'FAILED', 'RETRY_SCHEDULED', 'WEBHOOK_EVENT'
    )),
    provider          TEXT NOT NULL DEFAULT '',
    provider_event_id TEXT NOT NULL DEFAULT '',
    detail            TEXT NOT NULL DEFAULT '',
    payload           JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT comm_delivery_events_property_comm_fkey
        FOREIGN KEY (property_id, communication_id)
        REFERENCES guest_communications (property_id, id)
        ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_comm_delivery_provider_event
    ON communication_delivery_events (provider, provider_event_id)
    WHERE provider <> '' AND provider_event_id <> '';

CREATE INDEX IF NOT EXISTS idx_comm_delivery_events_comm
    ON communication_delivery_events (communication_id, occurred_at DESC);

-- Scheduled & idempotent automation execution queue.
CREATE TABLE IF NOT EXISTS communication_jobs (
    id                UUID PRIMARY KEY,
    property_id       UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    rule_id           UUID NOT NULL,
    reservation_id    UUID NOT NULL,
    guest_id          UUID NULL,
    trigger_event     TEXT NOT NULL,
    scheduled_for_utc TIMESTAMPTZ NOT NULL,
    stay_fingerprint  TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'SCHEDULED' CHECK (status IN (
        'SCHEDULED', 'QUEUED', 'CANCELLED', 'SUPERSEDED', 'SKIPPED'
    )),
    communication_id  UUID NULL,
    email_outbox_id   UUID NULL REFERENCES email_outbox(id) ON DELETE SET NULL,
    skip_reason       TEXT NOT NULL DEFAULT '',
    processed_at      TIMESTAMPTZ NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (rule_id, reservation_id, stay_fingerprint),
    CONSTRAINT comm_jobs_property_rule_fkey
        FOREIGN KEY (property_id, rule_id)
        REFERENCES communication_automation_rules (property_id, id)
        ON DELETE CASCADE,
    CONSTRAINT comm_jobs_property_reservation_fkey
        FOREIGN KEY (property_id, reservation_id)
        REFERENCES reservations (property_id, id)
        ON DELETE CASCADE,
    CONSTRAINT comm_jobs_property_guest_fkey
        FOREIGN KEY (property_id, guest_id)
        REFERENCES guests (property_id, id)
        ON DELETE SET NULL (guest_id),
    CONSTRAINT comm_jobs_property_comm_fkey
        FOREIGN KEY (property_id, communication_id)
        REFERENCES guest_communications (property_id, id)
        ON DELETE SET NULL (communication_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_comm_jobs_active_rule_reservation
    ON communication_jobs (rule_id, reservation_id)
    WHERE status IN ('SCHEDULED', 'QUEUED');

CREATE INDEX IF NOT EXISTS idx_comm_jobs_due
    ON communication_jobs (scheduled_for_utc, status)
    WHERE status = 'SCHEDULED';

-- Append-only marketing consent ledger (historical guests default to unsubscribed unless explicitly opted in).
CREATE TABLE IF NOT EXISTS guest_consent_records (
    id             UUID PRIMARY KEY,
    property_id    UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    guest_id       UUID NOT NULL,
    consent_type   TEXT NOT NULL CHECK (consent_type IN ('MARKETING_EMAIL')),
    status         TEXT NOT NULL CHECK (status IN ('GRANTED', 'WITHDRAWN')),
    method         TEXT NOT NULL DEFAULT '',
    source         TEXT NOT NULL DEFAULT '',
    policy_version TEXT NOT NULL DEFAULT 'v1',
    notes          TEXT NOT NULL DEFAULT '',
    recorded_by    UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT guest_consent_property_guest_fkey
        FOREIGN KEY (property_id, guest_id)
        REFERENCES guests (property_id, id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_guest_consent_guest_latest
    ON guest_consent_records (property_id, guest_id, consent_type, occurred_at DESC, created_at DESC);

-- Property-configurable GDPR & operational data retention policy.
CREATE TABLE IF NOT EXISTS property_retention_policies (
    property_id                  UUID PRIMARY KEY REFERENCES properties(id) ON DELETE CASCADE,
    enquiry_retention_days       INT NOT NULL DEFAULT 1095 CHECK (enquiry_retention_days >= 30),
    inactive_guest_retention_days INT NOT NULL DEFAULT 2555 CHECK (inactive_guest_retention_days >= 90),
    comms_retention_days         INT NOT NULL DEFAULT 1095 CHECK (comms_retention_days >= 30),
    auto_anonymize_enabled       BOOLEAN NOT NULL DEFAULT FALSE,
    updated_by                   UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    updated_at                   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Automatic updated_at trigger function and table triggers.
CREATE OR REPLACE FUNCTION touch_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DO $$
DECLARE
    t TEXT;
BEGIN
    FOR t IN SELECT unnest(ARRAY[
        'guest_preferences',
        'guest_notes',
        'guest_enquiries',
        'communication_templates',
        'communication_automation_rules',
        'guest_communications',
        'communication_jobs',
        'property_retention_policies'
    ])
    LOOP
        IF NOT EXISTS (
            SELECT 1 FROM pg_trigger WHERE tgname = 'trg_' || t || '_updated_at'
        ) THEN
            EXECUTE format(
                'CREATE TRIGGER trg_%I_updated_at BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION touch_updated_at()',
                t, t
            );
        END IF;
    END LOOP;
END $$;
