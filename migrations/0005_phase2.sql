-- Phase 2 Migration: Direct Booking Engine, Temporary Inventory Holds,
-- Authoritative Price Snapshots, Stripe Payments, Webhooks & Email Outbox.
-- Strictly additive and backward-compatible with Phases 1 through 1.6.1.

-- 1. Extend properties & unit_types with public website metadata & gallery support
ALTER TABLE properties
    ADD COLUMN IF NOT EXISTS public_enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS custom_domain       TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS website_url         TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS public_description  TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS uq_properties_custom_domain
    ON properties (LOWER(custom_domain))
    WHERE custom_domain <> '';

ALTER TABLE unit_types
    ADD COLUMN IF NOT EXISTS is_public_bookable BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS images             JSONB NOT NULL DEFAULT '[]'::jsonb;

-- 2. Property Booking, Deposit, Hold & Occupancy Surcharge Policies
CREATE TABLE IF NOT EXISTS property_booking_policies (
    property_id                     UUID PRIMARY KEY REFERENCES properties(id) ON DELETE CASCADE,
    hold_duration_minutes           INTEGER NOT NULL DEFAULT 15 CHECK (hold_duration_minutes BETWEEN 5 AND 120),
    deposit_mode                    TEXT NOT NULL DEFAULT 'PERCENTAGE' CHECK (deposit_mode IN ('FULL', 'PERCENTAGE', 'FIXED')),
    deposit_percentage              INTEGER NOT NULL DEFAULT 25 CHECK (deposit_percentage BETWEEN 0 AND 100),
    deposit_fixed_pence             BIGINT NOT NULL DEFAULT 0 CHECK (deposit_fixed_pence >= 0),
    balance_due_days_before_arrival INTEGER NOT NULL DEFAULT 14 CHECK (balance_due_days_before_arrival >= 0),
    extra_adult_nightly_pence       BIGINT NOT NULL DEFAULT 0 CHECK (extra_adult_nightly_pence >= 0),
    child_nightly_pence             BIGINT NOT NULL DEFAULT 0 CHECK (child_nightly_pence >= 0),
    infant_nightly_pence            BIGINT NOT NULL DEFAULT 0 CHECK (infant_nightly_pence >= 0),
    booking_terms_version           TEXT NOT NULL DEFAULT '2026-01',
    booking_terms_url               TEXT NOT NULL DEFAULT '',
    privacy_policy_url              TEXT NOT NULL DEFAULT '',
    cancellation_policy             TEXT NOT NULL DEFAULT 'Full refund when cancelled at least 14 days before arrival. Within 14 days of arrival, deposits are non-refundable unless cancelled by the property.',
    stripe_enabled                  BOOLEAN NOT NULL DEFAULT TRUE,
    version                         INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    updated_at                      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO property_booking_policies (property_id)
SELECT id FROM properties
ON CONFLICT (property_id) DO NOTHING;

-- 3. Temporary Inventory Holds backed atomically by central inventory_claims
CREATE TABLE IF NOT EXISTS booking_holds (
    id                       UUID PRIMARY KEY,
    property_id              UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    hold_reference           TEXT NOT NULL CHECK (hold_reference ~ '^[A-Z0-9-]+$'),
    unit_type_id             UUID NOT NULL REFERENCES unit_types(id) ON DELETE RESTRICT,
    unit_id                  UUID NOT NULL REFERENCES units(id) ON DELETE RESTRICT,
    rate_plan_id             UUID NOT NULL REFERENCES rate_plans(id) ON DELETE RESTRICT,
    channel_code             TEXT NOT NULL DEFAULT 'DIRECT',
    stay_range               DATERANGE NOT NULL CHECK (
                                 NOT isempty(stay_range)
                                 AND lower_inc(stay_range)
                                 AND NOT upper_inc(stay_range)
                             ),
    adults                   INTEGER NOT NULL DEFAULT 1 CHECK (adults >= 1),
    children                 INTEGER NOT NULL DEFAULT 0 CHECK (children >= 0),
    infants                  INTEGER NOT NULL DEFAULT 0 CHECK (infants >= 0),
    guest_name               TEXT NOT NULL DEFAULT '',
    guest_email              TEXT NOT NULL DEFAULT '',
    guest_phone              TEXT NOT NULL DEFAULT '',
    special_requests         TEXT NOT NULL DEFAULT '',
    terms_version            TEXT NOT NULL DEFAULT '',
    terms_accepted_at        TIMESTAMPTZ NULL,
    customer_session_hash    TEXT NOT NULL DEFAULT '',
    status                   TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (
                                 status IN ('ACTIVE', 'CONFIRMED', 'EXPIRED', 'CANCELLED', 'FAILED')
                             ),
    expires_at               TIMESTAMPTZ NOT NULL,
    confirmed_reservation_id UUID NULL REFERENCES reservations(id) ON DELETE SET NULL,
    version                  INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, hold_reference)
);

CREATE INDEX IF NOT EXISTS idx_booking_holds_active_expiry
    ON booking_holds (expires_at)
    WHERE status = 'ACTIVE';

CREATE INDEX IF NOT EXISTS idx_booking_holds_property_status
    ON booking_holds (property_id, status, created_at DESC);

-- Extend inventory_claims with mutually exclusive hold_id owner reference
ALTER TABLE inventory_claims
    ADD COLUMN IF NOT EXISTS hold_id UUID NULL REFERENCES booking_holds(id) ON DELETE CASCADE;

DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'inventory_claims'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) ILIKE '%blockout_group_id%'
    LOOP
        EXECUTE format('ALTER TABLE inventory_claims DROP CONSTRAINT %I', c.conname);
    END LOOP;
END $$;

ALTER TABLE inventory_claims
    ADD CONSTRAINT chk_inventory_claims_owner
    CHECK (
        (reservation_id IS NOT NULL AND blockout_group_id IS NULL AND hold_id IS NULL) OR
        (reservation_id IS NULL AND blockout_group_id IS NOT NULL AND hold_id IS NULL) OR
        (reservation_id IS NULL AND blockout_group_id IS NULL AND hold_id IS NOT NULL)
    );

CREATE INDEX IF NOT EXISTS idx_inventory_claims_hold
    ON inventory_claims (hold_id)
    WHERE hold_id IS NOT NULL;

-- 4. Authoritative Pricing Quotes & Immutable Reservation Price Snapshots
CREATE TABLE IF NOT EXISTS booking_quotes (
    id                          UUID PRIMARY KEY,
    property_id                 UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    hold_id                     UUID NULL REFERENCES booking_holds(id) ON DELETE SET NULL,
    reservation_id              UUID NULL REFERENCES reservations(id) ON DELETE CASCADE,
    unit_type_id                UUID NOT NULL REFERENCES unit_types(id) ON DELETE RESTRICT,
    rate_plan_id                UUID NOT NULL REFERENCES rate_plans(id) ON DELETE RESTRICT,
    currency                    CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    start_date                  DATE NOT NULL,
    end_date                    DATE NOT NULL,
    nights                      INTEGER NOT NULL CHECK (nights >= 1),
    adults                      INTEGER NOT NULL DEFAULT 1 CHECK (adults >= 1),
    children                    INTEGER NOT NULL DEFAULT 0 CHECK (children >= 0),
    infants                     INTEGER NOT NULL DEFAULT 0 CHECK (infants >= 0),
    nightly_breakdown           JSONB NOT NULL DEFAULT '[]'::jsonb,
    extras_breakdown            JSONB NOT NULL DEFAULT '[]'::jsonb,
    discounts_breakdown         JSONB NOT NULL DEFAULT '[]'::jsonb,
    tax_breakdown               JSONB NOT NULL DEFAULT '[]'::jsonb,
    accommodation_subtotal_pence BIGINT NOT NULL DEFAULT 0 CHECK (accommodation_subtotal_pence >= 0),
    occupancy_surcharge_pence   BIGINT NOT NULL DEFAULT 0 CHECK (occupancy_surcharge_pence >= 0),
    extras_subtotal_pence       BIGINT NOT NULL DEFAULT 0 CHECK (extras_subtotal_pence >= 0),
    discount_pence              BIGINT NOT NULL DEFAULT 0 CHECK (discount_pence >= 0),
    tax_pence                   BIGINT NOT NULL DEFAULT 0 CHECK (tax_pence >= 0),
    total_pence                 BIGINT NOT NULL CHECK (total_pence >= 0),
    deposit_mode                TEXT NOT NULL CHECK (deposit_mode IN ('FULL', 'PERCENTAGE', 'FIXED')),
    deposit_due_pence           BIGINT NOT NULL CHECK (deposit_due_pence >= 0),
    balance_due_pence           BIGINT NOT NULL CHECK (balance_due_pence >= 0),
    balance_due_date            DATE NULL,
    policy_version              INTEGER NOT NULL DEFAULT 1,
    quote_hash                  TEXT NOT NULL,
    expires_at                  TIMESTAMPTZ NOT NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_booking_quotes_hold
    ON booking_quotes (hold_id)
    WHERE hold_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_booking_quotes_reservation
    ON booking_quotes (reservation_id)
    WHERE reservation_id IS NOT NULL;

-- 5. Extend reservations with Phase 2 payment state, snapshots & customer cancellation request tracking
ALTER TABLE reservations
    ADD COLUMN IF NOT EXISTS payment_status             TEXT NOT NULL DEFAULT 'MANUAL'
        CHECK (payment_status IN ('MANUAL', 'UNPAID', 'PARTIALLY_PAID', 'PAID', 'REFUNDED', 'PARTIALLY_REFUNDED', 'FAILED')),
    ADD COLUMN IF NOT EXISTS total_pence                BIGINT NOT NULL DEFAULT 0 CHECK (total_pence >= 0),
    ADD COLUMN IF NOT EXISTS deposit_pence              BIGINT NOT NULL DEFAULT 0 CHECK (deposit_pence >= 0),
    ADD COLUMN IF NOT EXISTS amount_paid_pence          BIGINT NOT NULL DEFAULT 0 CHECK (amount_paid_pence >= 0),
    ADD COLUMN IF NOT EXISTS balance_due_pence          BIGINT NOT NULL DEFAULT 0 CHECK (balance_due_pence >= 0),
    ADD COLUMN IF NOT EXISTS currency                   CHAR(3) NOT NULL DEFAULT 'GBP',
    ADD COLUMN IF NOT EXISTS origin_hold_id             UUID NULL REFERENCES booking_holds(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS terms_version              TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS terms_accepted_at          TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS confirmed_at               TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS cancellation_requested_at  TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS cancellation_request_note  TEXT NOT NULL DEFAULT '';

-- 6. Payment Attempts, Verified Payments, Refunds, Webhook Deduplication & Reconciliation Exceptions
CREATE TABLE IF NOT EXISTS payment_attempts (
    id                         UUID PRIMARY KEY,
    property_id                UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    hold_id                    UUID NULL REFERENCES booking_holds(id) ON DELETE SET NULL,
    reservation_id             UUID NULL REFERENCES reservations(id) ON DELETE SET NULL,
    provider                   TEXT NOT NULL CHECK (provider IN ('stripe')),
    mode                       TEXT NOT NULL DEFAULT 'checkout_session',
    provider_session_id        TEXT NOT NULL DEFAULT '',
    provider_payment_intent_id TEXT NOT NULL DEFAULT '',
    payment_type               TEXT NOT NULL CHECK (payment_type IN ('FULL', 'DEPOSIT', 'BALANCE')),
    amount_pence               BIGINT NOT NULL CHECK (amount_pence >= 0),
    currency                   CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    status                     TEXT NOT NULL CHECK (status IN (
                                   'CREATED', 'REQUIRES_ACTION', 'SUCCEEDED', 'FAILED',
                                   'CANCELLED', 'EXPIRED', 'RECONCILIATION_REQUIRED',
                                   'REFUNDED', 'PARTIALLY_REFUNDED'
                               )),
    idempotency_key            TEXT NOT NULL,
    checkout_url               TEXT NOT NULL DEFAULT '',
    failure_reason             TEXT NOT NULL DEFAULT '',
    metadata                   JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_payment_attempts_provider_session
    ON payment_attempts (provider, provider_session_id)
    WHERE provider_session_id <> '';

CREATE INDEX IF NOT EXISTS idx_payment_attempts_hold
    ON payment_attempts (hold_id)
    WHERE hold_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS payments (
    id                         UUID PRIMARY KEY,
    property_id                UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    reservation_id             UUID NOT NULL REFERENCES reservations(id) ON DELETE RESTRICT,
    payment_attempt_id         UUID NOT NULL REFERENCES payment_attempts(id) ON DELETE RESTRICT,
    provider                   TEXT NOT NULL CHECK (provider IN ('stripe')),
    provider_payment_intent_id TEXT NOT NULL DEFAULT '',
    provider_charge_id         TEXT NOT NULL DEFAULT '',
    payment_type               TEXT NOT NULL CHECK (payment_type IN ('FULL', 'DEPOSIT', 'BALANCE')),
    amount_pence               BIGINT NOT NULL CHECK (amount_pence >= 0),
    currency                   CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    status                     TEXT NOT NULL CHECK (status IN ('SUCCEEDED', 'REFUNDED', 'PARTIALLY_REFUNDED')),
    paid_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (payment_attempt_id)
);

CREATE INDEX IF NOT EXISTS idx_payments_reservation
    ON payments (reservation_id, created_at DESC);

CREATE TABLE IF NOT EXISTS refunds (
    id                 UUID PRIMARY KEY,
    property_id        UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    payment_attempt_id UUID NOT NULL REFERENCES payment_attempts(id) ON DELETE RESTRICT,
    payment_id         UUID NULL REFERENCES payments(id) ON DELETE RESTRICT,
    reservation_id     UUID NULL REFERENCES reservations(id) ON DELETE RESTRICT,
    provider           TEXT NOT NULL CHECK (provider IN ('stripe')),
    provider_refund_id TEXT NOT NULL DEFAULT '',
    amount_pence       BIGINT NOT NULL CHECK (amount_pence > 0),
    currency           CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    reason             TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL CHECK (status IN ('PENDING', 'SUCCEEDED', 'FAILED')),
    idempotency_key    TEXT NOT NULL,
    created_by         UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (property_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS payment_webhook_events (
    id            UUID PRIMARY KEY,
    provider      TEXT NOT NULL CHECK (provider IN ('stripe')),
    event_id      TEXT NOT NULL,
    event_type    TEXT NOT NULL,
    payload_sha256 TEXT NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('PROCESSING', 'PROCESSED', 'IGNORED', 'FAILED')),
    error_message TEXT NOT NULL DEFAULT '',
    processed_at  TIMESTAMPTZ NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (provider, event_id)
);

CREATE TABLE IF NOT EXISTS payment_exceptions (
    id                 UUID PRIMARY KEY,
    property_id        UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    hold_id            UUID NULL REFERENCES booking_holds(id) ON DELETE SET NULL,
    payment_attempt_id UUID NOT NULL REFERENCES payment_attempts(id) ON DELETE RESTRICT,
    reservation_id     UUID NULL REFERENCES reservations(id) ON DELETE SET NULL,
    exception_type     TEXT NOT NULL CHECK (exception_type IN ('EXPIRED_HOLD_PAID', 'OVERLAP_ON_CONFIRM', 'AMOUNT_MISMATCH', 'ORPHAN_PAYMENT')),
    amount_pence       BIGINT NOT NULL CHECK (amount_pence >= 0),
    currency           CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    details            TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'AUTO_REFUNDED', 'MANUALLY_RESOLVED')),
    resolution_notes   TEXT NOT NULL DEFAULT '',
    resolved_by        UUID NULL REFERENCES admin_users(id) ON DELETE SET NULL,
    resolved_at        TIMESTAMPTZ NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 7. High-Entropy Secure Customer Confirmation Lookup Tokens
CREATE TABLE IF NOT EXISTS confirmation_access_tokens (
    id             UUID PRIMARY KEY,
    property_id    UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    reservation_id UUID NULL REFERENCES reservations(id) ON DELETE CASCADE,
    hold_id        UUID NULL REFERENCES booking_holds(id) ON DELETE CASCADE,
    token_hash     TEXT NOT NULL UNIQUE,
    expires_at     TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (reservation_id IS NOT NULL OR hold_id IS NOT NULL)
);

-- 8. Transactional Email Outbox (with retry tracking so email errors never lose confirmed bookings)
CREATE TABLE IF NOT EXISTS email_outbox (
    id              UUID PRIMARY KEY,
    property_id     UUID NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
    reservation_id  UUID NULL REFERENCES reservations(id) ON DELETE CASCADE,
    recipient_email TEXT NOT NULL,
    template        TEXT NOT NULL,
    subject         TEXT NOT NULL,
    body_text       TEXT NOT NULL,
    body_html       TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'SENT', 'FAILED')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at         TIMESTAMPTZ NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_email_outbox_dispatch
    ON email_outbox (status, next_attempt_at)
    WHERE status IN ('PENDING', 'FAILED');
