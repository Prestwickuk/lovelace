-- Phase 2.1.1: Stripe Webhook livemode audit & explicit security rejection state

ALTER TABLE payment_webhook_events
    ADD COLUMN IF NOT EXISTS livemode BOOLEAN NULL;

ALTER TABLE payment_webhook_events
    DROP CONSTRAINT IF EXISTS payment_webhook_events_status_check;

ALTER TABLE payment_webhook_events
    ADD CONSTRAINT payment_webhook_events_status_check
    CHECK (status IN ('PROCESSING', 'PROCESSED', 'IGNORED', 'REJECTED', 'FAILED'));
