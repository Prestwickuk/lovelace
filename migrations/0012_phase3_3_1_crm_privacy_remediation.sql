-- Phase 3.3.1: Guest CRM Security, Data Integrity & Privacy Remediation.
-- Enforces DB-level invariants for lifecycle statuses, append-only consent logs,
-- internal enquiry message visibility, and same-guest reservation links.

-- 1. Enforce guest lifecycle invariants at database level so MERGED / ANONYMIZED
-- records cannot exist without their required workflow metadata.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chk_guests_merged_target_invariant'
    ) THEN
        ALTER TABLE guests
            ADD CONSTRAINT chk_guests_merged_target_invariant
            CHECK (
                (status = 'MERGED' AND merged_into_guest_id IS NOT NULL AND merged_into_guest_id <> id)
                OR (status <> 'MERGED' AND merged_into_guest_id IS NULL)
            );
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chk_guests_anonymized_timestamp_invariant'
    ) THEN
        ALTER TABLE guests
            ADD CONSTRAINT chk_guests_anonymized_timestamp_invariant
            CHECK (
                (status = 'ANONYMIZED' AND anonymized_at IS NOT NULL)
                OR (status <> 'ANONYMIZED')
            );
    END IF;
END $$;

-- 2. Enforce append-only immutability on guest_consent_records.
-- Only explicit guest merge reparenting (updating guest_id alone within property_id) is permitted.
CREATE OR REPLACE FUNCTION enforce_guest_consent_append_only()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'guest_consent_records is append-only and cannot be deleted'
            USING ERRCODE = 'P0001';
    END IF;

    IF TG_OP = 'UPDATE' THEN
        IF NEW.id = OLD.id
           AND NEW.property_id = OLD.property_id
           AND NEW.consent_type = OLD.consent_type
           AND NEW.status = OLD.status
           AND NEW.method = OLD.method
           AND NEW.source = OLD.source
           AND NEW.policy_version = OLD.policy_version
           AND NEW.notes = OLD.notes
           AND NEW.recorded_by IS NOT DISTINCT FROM OLD.recorded_by
           AND NEW.occurred_at = OLD.occurred_at
           AND NEW.created_at = OLD.created_at
           AND NEW.guest_id IS DISTINCT FROM OLD.guest_id THEN
            RETURN NEW;
        END IF;
        RAISE EXCEPTION 'guest_consent_records is append-only and cannot be modified'
            USING ERRCODE = 'P0001';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guest_consent_records_append_only ON guest_consent_records;
CREATE TRIGGER trg_guest_consent_records_append_only
    BEFORE UPDATE OR DELETE ON guest_consent_records
    FOR EACH ROW EXECUTE FUNCTION enforce_guest_consent_append_only();

-- 3. Enforce canonical guest/reservation relationship integrity across CRM records.
CREATE OR REPLACE FUNCTION resolve_canonical_guest_id(p_property_id UUID, p_guest_id UUID)
RETURNS UUID AS $$
DECLARE
    v_curr UUID := p_guest_id;
    v_next UUID;
    v_hop  INT := 0;
BEGIN
    IF p_guest_id IS NULL THEN
        RETURN NULL;
    END IF;

    WHILE v_hop < 8 LOOP
        SELECT merged_into_guest_id
        INTO v_next
        FROM guests
        WHERE property_id = p_property_id AND id = v_curr;

        IF NOT FOUND THEN
            RETURN NULL;
        END IF;

        IF v_next IS NULL THEN
            RETURN v_curr;
        END IF;

        v_curr := v_next;
        v_hop := v_hop + 1;
    END LOOP;

    RETURN v_curr;
END;
$$ LANGUAGE plpgsql STABLE;

CREATE OR REPLACE FUNCTION enforce_guest_reservation_link()
RETURNS TRIGGER AS $$
DECLARE
    v_res_guest_id UUID;
    v_canon_record UUID;
    v_canon_res    UUID;
BEGIN
    IF NEW.guest_id IS NOT NULL AND NEW.reservation_id IS NOT NULL THEN
        SELECT guest_id
        INTO v_res_guest_id
        FROM reservations
        WHERE property_id = NEW.property_id AND id = NEW.reservation_id;

        IF NOT FOUND THEN
            RAISE EXCEPTION 'reservation_id does not belong to property'
                USING ERRCODE = '23503';
        END IF;

        v_canon_record := resolve_canonical_guest_id(NEW.property_id, NEW.guest_id);
        v_canon_res := resolve_canonical_guest_id(NEW.property_id, v_res_guest_id);

        IF v_canon_record IS NULL OR v_canon_res IS NULL OR v_canon_record <> v_canon_res THEN
            RAISE EXCEPTION 'guest_id does not match reservation guest_id'
                USING ERRCODE = '23514';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guest_notes_guest_reservation_link ON guest_notes;
CREATE TRIGGER trg_guest_notes_guest_reservation_link
    BEFORE INSERT OR UPDATE OF property_id, guest_id, reservation_id ON guest_notes
    FOR EACH ROW EXECUTE FUNCTION enforce_guest_reservation_link();

DROP TRIGGER IF EXISTS trg_guest_enquiries_guest_reservation_link ON guest_enquiries;
CREATE TRIGGER trg_guest_enquiries_guest_reservation_link
    BEFORE INSERT OR UPDATE OF property_id, guest_id, reservation_id ON guest_enquiries
    FOR EACH ROW EXECUTE FUNCTION enforce_guest_reservation_link();

DROP TRIGGER IF EXISTS trg_guest_communications_guest_reservation_link ON guest_communications;
CREATE TRIGGER trg_guest_communications_guest_reservation_link
    BEFORE INSERT OR UPDATE OF property_id, guest_id, reservation_id ON guest_communications
    FOR EACH ROW EXECUTE FUNCTION enforce_guest_reservation_link();

-- 4. Enforce strict equivalence between INTERNAL direction and is_internal flag on enquiry messages.
UPDATE guest_enquiry_messages
SET direction = 'INTERNAL'
WHERE is_internal = TRUE AND direction <> 'INTERNAL';

UPDATE guest_enquiry_messages
SET is_internal = TRUE
WHERE direction = 'INTERNAL' AND is_internal = FALSE;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chk_guest_enquiry_messages_internal_direction'
    ) THEN
        ALTER TABLE guest_enquiry_messages
            ADD CONSTRAINT chk_guest_enquiry_messages_internal_direction
            CHECK (
                (direction = 'INTERNAL' AND is_internal = TRUE)
                OR (direction <> 'INTERNAL' AND is_internal = FALSE)
            );
    END IF;
END $$;
