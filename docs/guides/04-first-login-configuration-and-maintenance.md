# Guide 4: First-Boot Setup, Website Embeds & Day-2 Maintenance

Once your Lovelace instance is running (via [Self-Hosted Server](01-local-and-self-hosted-server.md), [AWS / GCP / Azure](02-cloud-deployments-aws-gcp-azure.md), or [Railway](03-railway-and-paas-deployment.md)), follow this guide to complete your initial login, configure your properties, embed bookings on your website or WordPress site, and maintain your system safely over time.

---

## 1. First Sign-In, Password Rotation & Email-Free Recovery Codes

1. Open `https://<YOUR_ADMIN_DOMAIN>/login` and sign in using the `LOVELACE_BOOTSTRAP_ADMIN_EMAIL` and `LOVELACE_BOOTSTRAP_ADMIN_PASSWORD` you set during deployment.
2. **Mandatory Password Rotation (`/account?rotate=1`):**
   - On first login, Lovelace immediately prompts you to replace the temporary bootstrap password with your own private password (`>= 12` characters).
   - Once rotated, you can safely delete `LOVELACE_BOOTSTRAP_ADMIN_PASSWORD` from your server/cloud environment variables (even if left set, Lovelace never overwrites existing accounts on restart).
3. **Save Your 8 One-Time Recovery Codes:**
   - Immediately after setting your password, `/account` displays **8 one-time Recovery Codes** (e.g. `ABCD-EFGH-JKLM`) with **Copy Codes** and **Download `.txt`** buttons.
   - Store these alongside `LOVELACE_ENCRYPTION_KEY` in your password manager.
   - **Self-Service Password Reset Without Email (`/login/recover`):** If you ever forget your password (even before SMTP email is configured!), click **Forgot password? Use a recovery code** on `/login`, enter your email + any unused recovery code + a new password, and you are back in immediately. You can regenerate a fresh set of 8 codes anytime in **My Account (`/account`)**.
   - **Resetting Staff Passwords:** Global `owner_admin` users can issue a temporary reset password for any staff account directly in **Settings → Admin Users (`/users`)** without needing reset emails.

---

## 2. Running the 7-Step Guided Property Setup Wizard (`/onboarding`)

On an empty database (`0` properties), signing in automatically launches the **Guided Property Setup Wizard** at `/onboarding` (also accessible anytime under **Settings → Setup Wizard** or **Site Settings → + Add Another Property**):

1. **Step 1 — Property Type, Identity & Brand Style:**
   - Pick a tailored starter preset (**Campsite / Glamping**, **Holiday Cottage / Airbnb**, **Boutique Hotel / B&B**, or **Custom Venue**).
   - Sets property name, URL slug, timezone, currency (`GBP`, `EUR`, `USD`, etc.), check-in/out times, contrast-verified brand colours (`>= 4.5:1` WCAG AA enforced automatically), and console terminology (`Pitch`/`Room`/`Property`).
2. **Step 2 — Add Accommodation & Bulk Unit Generator:**
   - Use one-click starter templates or custom categories, and automatically generate numbered physical units/pitches/rooms in bulk (e.g. `Quantity: 10, Prefix: MP` creates `MP-01` through `MP-10`).
3. **Step 3 — Pricing, Seasonal Uplifts & Extras:**
   - Configure extra adult/child nightly rates, VAT status, weekend/peak-season tariff rules, and optional guest extras (e.g. Dog Stay, Firewood Basket, Late Check-Out, Breakfast Hamper, EV Charging).
4. **Step 4 — Deposits, Cancellation & House Rules:**
   - Choose **Full Upfront (100%)**, **Percentage Deposit** (e.g. `30%` now, balance `28` days before arrival), or **Fixed Deposit**, basket hold duration (`15` mins default), and plain-English cancellation, quiet-hours, fire/BBQ, and pet policies.
5. **Step 5 — Team & Staff Accounts *(Optional)*:**
   - Invite reception staff (`staff`), managers (`manager`), housekeepers (`housekeeping_staff`), maintenance technicians (`maintenance_staff`), or read-only viewers (`viewer`). Each colleague chooses their own password and recovery codes on first sign-in.
6. **Step 6 — Off-Site Cloud Backup Source *(Optional)*:**
   - Connect an **Amazon S3**, **Cloudflare R2 / Backblaze B2 / Wasabi (`S3_COMPATIBLE`)**, **Google Cloud Storage (`GCS`)**, or **Microsoft Azure Blob (`AZURE_BLOB`)** bucket for automatic `AES-256-GCM` encrypted `.zip.enc` backups — or skip in one click and use manual `.zip` exports.
7. **Step 7 — Review Summary, Website Booking Code & Finish:**
   - Review your setup, grab your **Official WordPress Plugin (`.zip`)** or HTML website embed snippets, and launch into the **Operations Centre (`/`)**.

> **Multi-Property Switching:** Managing more than one campsite, cottage, or B&B? Click **+ Add Another Property** in **Site Settings (`/property`)** and switch between properties anytime from the top-left sidebar dropdown. Also update `LOVELACE_PROPERTY_SLUG` on `public-web` to match your primary property's slug.

---

## 3. Embedding Bookings on Your Own Website or WordPress

Open **Site Settings → Website Booking Section (`/property#website-embed`)** at any time:

### Option A — WordPress Websites (Official Plugin `.zip`)
1. Click **Download WordPress Plugin (`.zip`)** inside `/property#website-embed` (or Onboarding Step 7). Your Booking URL, Property Slug, Display Name, and verified brand colours come **pre-configured inside the `.zip`**.
2. In your WordPress admin dashboard, go to **Plugins → Add New → Upload Plugin**, upload `lovelace-booking-<slug>.zip`, and click **Activate**.
3. Open **Settings → Lovelace Booking** and click **Test Connection & Sync Brand Colours**.
4. Insert any **Lovelace** block in the Gutenberg Block Editor — or paste any shortcode into Elementor, Divi, Classic Editor, or PHP templates:
   - `[lovelace_booking]` — Full interactive live booking section with auto-resizing height.
   - `[lovelace_search target="modal"]` — Check-in / Check-out / Guests bar opening an inline booking modal.
   - `[lovelace_book_button mode="modal"]` — Branded *"Book Direct"* button opening a booking modal overlay on the page.
   - `[lovelace_accommodation columns="2" schema="true"]` — Cached, SEO-indexable accommodation cards + `LodgingBusiness` JSON-LD structured data.

### Option B — Squarespace, Wix, Webflow, Shopify, or Static HTML
Copy any of the 3 self-contained HTML snippets from `/property#website-embed`:
1. **Full Embedded Live Booking Section (`<iframe>` + Auto-Resize `<script>`)** — embeds `<PUBLIC_ORIGIN>/embed/<property-slug>` directly inside your webpage; Stripe 3DS card payment automatically breaks out into the top window.
2. **Pure HTML Availability Search Bar (Zero JS / Zero Iframe)** — works even on builders that disallow `<script>` or `<iframe>` tags.
3. **Branded "Book Direct" Button** — simple styled `<a>` link.

---

## 4. Going Live with Online Card Payments (Stripe) & OTA Channels

By default, every newly onboarded property launches in **Safe Dark-by-Default Mode** (`stripe_enabled = false`, `guest_portal_enabled = false`) so you can inspect pricing, add existing manual/telephone reservations, and test website embeds with zero risk of accidental live card charges.

### Enabling Live Stripe Checkout
1. In your [Stripe Dashboard](https://dashboard.stripe.com/webhooks), create a live webhook endpoint pointing to:
   ```text
   https://<YOUR_ADMIN_DOMAIN>/api/webhooks/stripe
   ```
   Subscribe to events:
   - `checkout.session.completed`
   - `checkout.session.expired`
   - `charge.refunded`
2. Set `LOVELACE_STRIPE_SECRET_KEY=sk_live_...` (or restricted `rk_live_...`) and `LOVELACE_STRIPE_WEBHOOK_SECRET=whsec_...` on `lovelace-app` and restart/redeploy `lovelace-app`.
   *(Note: In `LOVELACE_ENV=production`, Lovelace rejects `sk_test_` keys and rejects any webhook with `livemode: false`.)*
3. Confirm `GET /readyz` reports healthy SMTP and payment readiness.
4. In **Lovelace Admin → Site Settings (`/property`)**, tick **Enable Public Online Checkout (Stripe)** and click **Save Booking Policy**.

### Connecting OTA Channel Managers *(Optional)*
Under **Settings → Channel Manager (`/settings/integrations/channel-manager`)**, you can connect **Beds24**, **Smoobu**, **Lodgify**, or **Channex** to sync availability and external OTA bookings (Airbnb, Booking.com, Expedia, Pitchup). Credentials are encrypted at rest with `LOVELACE_ENCRYPTION_KEY`.

---

## 5. Ongoing Maintenance, Backups, Upgrades & Disaster Recovery

### A. Health Monitoring Endpoints
Point your uptime monitor (UptimeRobot, Better Stack, CloudWatch, GCP Monitoring) at:
- **`https://<YOUR_ADMIN_DOMAIN>/healthz`** — lightweight liveness probe (`200 OK`).
- **`https://<YOUR_ADMIN_DOMAIN>/readyz`** — deep readiness probe checking PostgreSQL connectivity, applied migration integrity, public BFF secret isolation, and SMTP/Stripe readiness (`200 OK` when ready, `503` if degraded).
- **`https://<YOUR_PUBLIC_WEB_DOMAIN>/api/healthz`** — Next.js public BFF health probe (`200 OK`).

---

### B. 3-2-1 Backup Strategy (`/settings/backups` + Database Snapshots)

Lovelace includes a built-in, portable backup and verification system under **Settings → Backups & Cloud Sync (`/settings/backups`)**:

1. **On-Demand Local Backups (`.zip` / `.zip.enc`):**
   - Exports all PostgreSQL tables (in deterministic foreign-key restore order) **plus** all binary files in `/var/lib/lovelace/attachments` (logos & maintenance photos) and a `manifest.json` containing per-file SHA-256 digests.
   - Entering an optional **Archive Encryption Passphrase (`>= 12` chars)** wraps the entire archive in **PBKDF2-HMAC-SHA256 (`210,000` iterations) + AES-256-GCM** (`.zip.enc`).
2. **Automated Off-Site Cloud Backups:**
   - Configure **AWS S3**, **Cloudflare R2 / Backblaze B2 / Wasabi**, **Google Cloud Storage**, or **Azure Blob Storage** with automatic schedules (e.g. every `24` hours) and automatic retention pruning (e.g. keep newest `14` archives).
3. **Routine Restore Drills & Integrity Verification:**
   - Upload any `.zip` or `.zip.enc` archive into the **Verify Backup Integrity** tool on `/settings/backups` once a month to confirm passphrase validity and 100% SHA-256 checksum integrity without modifying your live database.
4. **Disaster Recovery Into a Fresh Instance:**
   - Boot a fresh, empty Lovelace instance (`0` properties, `1` bootstrapped `owner_admin`), sign in, open **Settings → Backups & Cloud Sync → Restore Into Empty Environment**, and upload your `.zip` / `.zip.enc` archive. Lovelace refuses to overwrite any non-empty database with existing properties or bookings, preventing accidental data loss.

---

### C. Safe Zero-Data-Loss Upgrade Procedure

#### 1. Self-Hosted Docker Compose Server
```bash
cd /opt/lovelace

# 1. Take a pre-upgrade backup (via /settings/backups UI or pg_dump):
docker exec lovelace-postgres pg_dump -U "${POSTGRES_BOOTSTRAP_USER:-postgres}" -Fc "${POSTGRES_DB:-lovelace}" \
  > "/opt/lovelace/pre_upgrade_$(date +%Y%m%d_%H%M%S).dump"

# 2. Pull the latest release commit/tag:
git fetch origin main
git checkout main
git merge --ff-only origin/main

# 3. Rebuild and roll forward (db-bootstrap + migrate run automatically before app restarts):
#    NEVER pass `-v` to docker compose!
docker compose up -d --build

# 4. Verify readiness:
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:3000/api/healthz
```

#### 2. AWS / GCP / Azure / Railway
1. Download or trigger an off-site backup in **Settings → Backups & Cloud Sync** (or take an RDS / Cloud SQL / Azure snapshot).
2. Run `lovelace-migrate` (`MIGRATION_DATABASE_URL` as `lovelace_migrator`) if the release includes new SQL files in `migrations/`. *(Migrations in Lovelace are strictly forward-compatible and idempotent.)*
3. If new tables were added on a managed DB outside Docker Compose, run `./scripts/postgres/ensure-roles.sh` once so `lovelace_app` receives DML grants on the new tables.
4. Deploy the new `lovelace-app` and `lovelace-public-web` container images and verify `/readyz` returns `200 OK`.

---

### D. Rotating Passwords & Secrets Safely

- **Admin & Staff Passwords:** Rotate anytime in **My Account (`/account`)** or **Settings → Admin Users (`/users`)**.
- **Public BFF Secret (`LOVELACE_PUBLIC_API_SECRET` / `LOVELACE_PUBLIC_API_SECRET`):** Generate a new `openssl rand -hex 32` value, update it on both `lovelace-app` and `lovelace-public-web`, and restart both services together.
- **Database Role Passwords (`lovelace_app` / `lovelace_migrator`):** Update `LOVELACE_APP_PASSWORD` / `LOVELACE_MIGRATOR_PASSWORD`, run `./scripts/postgres/ensure-roles.sh` (or `docker compose up -d --no-deps db-bootstrap`), and restart `lovelace-app`.
- **`LOVELACE_ENCRYPTION_KEY`:** Keep this key backed up! If you ever need to rotate it, temporarily clear/re-enter encrypted OTA channel credentials (`/settings/integrations/channel-manager`) and cloud backup bucket credentials (`/settings/backups`) after updating the key.

---

## 6. Troubleshooting Quick Reference

| Symptom | Cause | Fix |
| :--- | :--- | :--- |
| `noteG-server` exits on startup with `privileged database role rejected` | `DATABASE_URL` connected as `postgres` or a superuser in `LOVELACE_ENV=production` | Point `DATABASE_URL` to the non-superuser `lovelace_app` role created by `scripts/postgres/ensure-roles.sh`. |
| `GET /readyz` returns `503` mentioning `email` | `LOVELACE_REQUIRE_EMAIL=true` while `LOVELACE_SMTP_ADDR` is blank | Configure `LOVELACE_SMTP_ADDR` / `USERNAME` / `PASSWORD`, or set `LOVELACE_REQUIRE_EMAIL=false` until ready to send emails. |
| Logo or maintenance photo upload fails with `permission denied` | Persistent volume at `/var/lib/lovelace/attachments` is owned by `root:root` | Set ownership on the mounted directory to `100:101` (`chown -R 100:101 /var/lib/lovelace/attachments` inside Alpine). |
| Login returns `403 Security Token Invalid` | Accessing admin UI over plain `http://` while `LOVELACE_COOKIE_SECURE=true`, or proxy stripping `Origin` / `Host` | Access via `https://` and ensure reverse proxy forwards `Host` and `X-Forwarded-Proto: https`. |
| `public-web` shows `Unable to connect to the reservation system` | `LOVELACE_API_BASE_URL` unreachable or `LOVELACE_PUBLIC_API_SECRET` mismatch | Verify `LOVELACE_PUBLIC_API_SECRET` matches `LOVELACE_PUBLIC_API_SECRET` byte-for-byte, and on Railway/Fly set `HOSTNAME=::` & `LOVELACE_LISTEN_ADDR=:8080`. |
| WordPress `[lovelace_booking]` iframe does not load | `LOVELACE_PUBLIC_ORIGIN` / `NEXT_PUBLIC_SITE_URL` pointing to admin URL instead of `public-web` URL | Point WordPress **Settings → Lovelace Booking → Public Booking Site URL** to your `public-web` HTTPS domain (`https://book.yourdomain.com`). |
