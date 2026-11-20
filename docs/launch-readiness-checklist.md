# LOVELACE — Production Launch Sign-Off Checklist

Before opening public direct bookings to guests on a live domain, the property owner and platform operator must explicitly review and verify every item below alongside the full infrastructure architecture in [Production Foundation & Launch-Readiness Plan](production-foundation-plan.md). Items seeded in development/demo databases are **not** verified production property facts until confirmed by the property owner in the Admin Console (`/property`) and environment configuration.

## 1. Property Facts & Policies (Owner Confirmation Required)
Verify each field in LOVELACE Admin Console (`/property`):
- [ ] **Business Name & Tagline** (`properties.name`, `tagline`, `public_description`) match actual property copy.
- [ ] **Physical Address & Postcode** (`address_line1`, `address_line2`, `town`, `postcode`, `region`) confirmed — seeded demo defaults (`Market Town`, `AB1 2CD`) must be verified/updated to the real property address.
- [ ] **Contact Email & Telephone** (`contact_email`, `contact_phone`) confirmed — public UI omits telephone unless explicitly configured; verify real monitored email and phone number.
- [ ] **Check-In & Check-Out Times** (`check_in_time`, `check_out_time`) confirmed.
- [ ] **VAT Status & Registration** (`vat_registered`, `vat_number`) confirmed against HMRC / tax advisor guidance.
- [ ] **Quiet Hours Policy** (`property_booking_policies.quiet_hours`) configured by owner (public frontend never hardcodes sample hours).
- [ ] **Fires & BBQ Policy** (`property_booking_policies.fire_policy`) configured by owner.
- [ ] **Pet Policy** (`property_booking_policies.pet_policy` and per-unit-type `pets_allowed` / `max_pets`) verified.
- [ ] **Cancellation & Deposit Rules** (`cancellation_policy`, `deposit_mode`, `deposit_percentage`, `deposit_fixed_pence`, `balance_due_days_before_arrival`) reviewed by owner.
- [ ] **Privacy Notice & Terms Version** (`privacy_notice`, `booking_terms_version`) reviewed for UK GDPR / local privacy compliance.
- [ ] **Accommodation Photos & Descriptions** (`unit_types.description`, `unit_types.amenities`, images) reflect actual pitches, rooms, or units on site.
- [ ] **Pricing, Extras & Release Rules** verified for live season dates.

## 2. Payments & Stripe Production Readiness
- [ ] Set `LOVELACE_ENV=production` and `LOVELACE_STRIPE_SANDBOX=false`.
- [ ] Decide launch payment posture:
  - **Live Stripe Checkout enabled:** set `stripe_enabled=true` in Admin Console AND configure **live** credentials (`LOVELACE_STRIPE_SECRET_KEY=sk_live_...` or appropriately scoped `rk_live_...` + live `LOVELACE_STRIPE_WEBHOOK_SECRET=whsec_...`; test keys `sk_test_`/`rk_test_` fail production startup). Point the live Stripe webhook endpoint to `https://<api-origin>/api/public/v1/webhooks/stripe`.
  - **Online card payments temporarily off:** set `stripe_enabled=false` in Admin Console (backend returns `409 stripe_disabled`, frontend displays unavailable-payment guidance).
- [ ] Confirm webhook `livemode: true` events are active in the Stripe live dashboard and perform one low-value live card checkout + webhook verification + refund reconciliation check before launch.

## 3. Transactional Email Readiness
- [ ] Configure real SMTP credentials (`LOVELACE_SMTP_ADDR`, `LOVELACE_SMTP_USERNAME`, `LOVELACE_SMTP_PASSWORD`, `LOVELACE_EMAIL_FROM`). Live public bookings (`stripe_enabled=true`) refuse startup and `/readyz` in production unless SMTP is configured; `docker-compose.yml` defaults `LOVELACE_REQUIRE_EMAIL=true` (set `false` only for admin-only / payments-disabled operation).
- [ ] Confirm `GET /readyz` reports `"email": {"status": "configured", ...}`.
- [ ] Verify SPF, DKIM, and DMARC records on the sending domain so confirmation emails reach guest inboxes.

## 4. Network, Domain & Security Hardening
- [ ] Explicitly configure `LOVELACE_PUBLIC_ORIGIN=https://book.example.com` (and `NEXT_PUBLIC_SITE_URL`, which tracks `LOVELACE_PUBLIC_ORIGIN`). Production Compose and server startup require explicit HTTPS origin configuration and never substitute an alternative domain.
- [ ] Generate and inject high-entropy secrets for `LOVELACE_PUBLIC_API_SECRET` (Go + Next.js BFF shared key), `LOVELACE_ENCRYPTION_KEY`, and distinct PostgreSQL role passwords (`POSTGRES_BOOTSTRAP_PASSWORD`, `LOVELACE_MIGRATOR_PASSWORD`, `LOVELACE_APP_PASSWORD`).
- [ ] Verify `LOVELACE_TRUSTED_PROXIES` lists ONLY the reverse proxy CIDRs sitting in front of `app` and `public-web`.
- [ ] Run initial launch topology as a single Go instance + single Next.js instance (current rate-limit counters are process-local; migrate to Redis/shared store before horizontal scaling).
- [ ] Confirm `/api/public/v1/*` rejects unauthenticated direct external requests (`401 unauthorized`) and `/admin` / `/api/v1/*` is inaccessible via the public booking origin.
- [ ] Confirm PostgreSQL automated backups (`pg_dump`) and `schema_migrations` health check pass.
