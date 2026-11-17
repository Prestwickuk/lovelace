-- LOVELACE Phase 3 Milestone 4 Staging Hardening:
-- Safely scrubs pre-remediation / expired portal security-challenge email_outbox bodies across
-- all queue states (including active PROCESSING, DISPATCHING, CANCEL_REQUESTED, and DISPATCH_UNKNOWN)
-- WITHOUT clearing active worker lease tokens, lease expiries, fence epochs, or DISPATCH_UNKNOWN
-- reconciliation states. Immutable audit_logs rows remain untouched.

UPDATE public.email_outbox eo
SET status = CASE
        WHEN eo.status IN ('PENDING', 'FAILED')
          OR (eo.status = 'PROCESSING' AND (eo.locked_at IS NULL OR eo.locked_at < NOW() - INTERVAL '5 minutes'))
        THEN 'CANCELLED'
        ELSE eo.status
    END,
    cancel_reason = CASE
        WHEN (
            eo.status IN ('PENDING', 'FAILED')
            OR (eo.status = 'PROCESSING' AND (eo.locked_at IS NULL OR eo.locked_at < NOW() - INTERVAL '5 minutes'))
        ) AND eo.cancel_reason = ''
        THEN 'Invalidated or expired portal security challenge cancelled before delivery'
        ELSE eo.cancel_reason
    END,
    cancelled_at = CASE
        WHEN eo.status IN ('PENDING', 'FAILED')
          OR (eo.status = 'PROCESSING' AND (eo.locked_at IS NULL OR eo.locked_at < NOW() - INTERVAL '5 minutes'))
        THEN COALESCE(eo.cancelled_at, NOW())
        ELSE eo.cancelled_at
    END,
    locked_at = CASE
        WHEN eo.status IN ('PENDING', 'FAILED')
          OR (eo.status = 'PROCESSING' AND (eo.locked_at IS NULL OR eo.locked_at < NOW() - INTERVAL '5 minutes'))
        THEN NULL
        ELSE eo.locked_at
    END,
    worker_token = CASE
        WHEN eo.status IN ('PENDING', 'FAILED')
          OR (eo.status = 'PROCESSING' AND (eo.locked_at IS NULL OR eo.locked_at < NOW() - INTERVAL '5 minutes'))
        THEN NULL
        ELSE eo.worker_token
    END,
    body_text = '[redacted security challenge]',
    body_html = '',
    updated_at = NOW()
WHERE eo.template IN ('portal_verification', 'portal_step_up', 'portal_email_change_verify')
  AND (eo.body_text <> '[redacted security challenge]' OR eo.body_html <> '')
  AND (
      eo.status IN ('SENT', 'CANCELLED', 'FAILED', 'DISPATCH_UNKNOWN')
      OR eo.created_at <= NOW() - INTERVAL '15 minutes'
      OR NOT EXISTS (
          SELECT 1
          FROM public.guest_portal_challenges gpc
          WHERE gpc.property_id = eo.property_id
            AND gpc.normalized_email = LOWER(TRIM(eo.recipient_email))
            AND gpc.otp_hash_alg = 'HMAC-SHA256-V1'
            AND gpc.consumed_at IS NULL
            AND gpc.invalidated_at IS NULL
            AND gpc.expires_at > NOW()
      )
  );
