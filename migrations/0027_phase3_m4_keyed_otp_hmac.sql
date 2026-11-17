-- LOVELACE Phase 3 Milestone 4 P1 Remediation: Keyed HMAC-SHA256 OTP Protection
-- Replaces unkeyed SHA-256 OTP digests with server-keyed HMAC-SHA256 digests (`otp_hash_alg = 'HMAC-SHA256-V1'`),
-- safely invalidates and scrubs any pre-existing unkeyed challenge rows without altering audit_logs,
-- and enforces at the schema level that active challenges must use keyed HMAC-SHA256.

ALTER TABLE public.guest_portal_challenges
    ADD COLUMN IF NOT EXISTS otp_hash_alg TEXT;

-- Scrub and cancel any queued or sent security-challenge outbox items tied to pre-remediation
-- unkeyed challenge rows so stale codes are never delivered or retained in email_outbox.
-- Immutable audit_logs provenance rows remain completely untouched.
UPDATE public.email_outbox eo
SET status = CASE WHEN eo.status IN ('PENDING', 'FAILED') THEN 'CANCELLED' ELSE eo.status END,
    cancel_reason = CASE
        WHEN eo.status IN ('PENDING', 'FAILED') AND eo.cancel_reason = '' THEN 'Invalidated during 0027 keyed OTP HMAC upgrade'
        ELSE eo.cancel_reason
    END,
    cancelled_at = CASE
        WHEN eo.status IN ('PENDING', 'FAILED') THEN COALESCE(eo.cancelled_at, NOW())
        ELSE eo.cancelled_at
    END,
    body_text = '[redacted security challenge]',
    body_html = '',
    locked_at = NULL,
    worker_token = NULL,
    dispatch_lease_expires_at = NULL,
    updated_at = NOW()
WHERE eo.template IN ('portal_verification', 'portal_step_up', 'portal_email_change_verify')
  AND eo.status IN ('PENDING', 'FAILED', 'SENT', 'CANCELLED')
  AND EXISTS (
      SELECT 1
      FROM public.guest_portal_challenges gpc
      WHERE gpc.property_id = eo.property_id
        AND LOWER(TRIM(eo.recipient_email)) = gpc.normalized_email
        AND (gpc.otp_hash_alg IS NULL OR gpc.otp_hash_alg <> 'HMAC-SHA256-V1')
  );

-- Invalidate all pre-existing unconsumed challenges and overwrite all pre-existing unkeyed otp_hash
-- digests so offline enumeration is impossible even against historical rows prior to expiry cleanup.
UPDATE public.guest_portal_challenges
SET invalidated_at = CASE
        WHEN consumed_at IS NULL AND invalidated_at IS NULL THEN NOW()
        ELSE invalidated_at
    END,
    otp_hash = repeat('0', 64),
    otp_hash_alg = 'INVALIDATED_UNKEYED'
WHERE otp_hash_alg IS NULL OR otp_hash_alg <> 'HMAC-SHA256-V1';

ALTER TABLE public.guest_portal_challenges
    ALTER COLUMN otp_hash_alg SET DEFAULT 'HMAC-SHA256-V1',
    ALTER COLUMN otp_hash_alg SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chk_portal_challenges_otp_hash_alg'
    ) THEN
        ALTER TABLE public.guest_portal_challenges
            ADD CONSTRAINT chk_portal_challenges_otp_hash_alg CHECK (
                otp_hash_alg = 'HMAC-SHA256-V1'
                OR (
                    otp_hash_alg = 'INVALIDATED_UNKEYED'
                    AND (invalidated_at IS NOT NULL OR consumed_at IS NOT NULL)
                )
            );
    END IF;
END $$;

GRANT SELECT, INSERT, UPDATE, DELETE ON public.guest_portal_challenges TO lovelace_app;
