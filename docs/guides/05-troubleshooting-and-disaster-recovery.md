# Guide 5: Deep Troubleshooting & The "Hotel Owner Catastrophe" Playbook

This runbook has two halves:

- **Part I — Infrastructure & Deployment Troubleshooting:** Deep technical diagnostics and exact fixes for **1) Local & Self-Hosted Linux Servers**, **2) AWS, Google Cloud (GCP) & Microsoft Azure**, and **3) Railway & PaaS Platforms**.
- **Part II — The "Friday Night at Reception" Catastrophe Playbook:** 14 realistic ways a busy, non-technical hotel, B&B, cottage, or campsite owner can accidentally "break" their setup, lock themselves out, misprice their suites at £1.50 a night, or close their hotel for August — and **how to fix every single one in minutes without losing data**.

---

## Rapid 60-Second Triage (Start Here First)

Before changing any configuration, check these three health probes to pinpoint which layer is unhappy:

```bash
# 1. Is the Go Admin/Backend process alive? (Should return 200 OK)
curl -i https://<YOUR_ADMIN_DOMAIN>/healthz

# 2. Are PostgreSQL, migrations, BFF secret, SMTP, and Stripe healthy? (200 OK or JSON reason on 503)
curl -i https://<YOUR_ADMIN_DOMAIN>/readyz

# 3. Is the Next.js Public Booking Frontend alive? (Should return 200 OK)
curl -i https://<YOUR_PUBLIC_WEB_DOMAIN>/api/healthz

# 4. Can the Public Frontend talk to the Go Backend with a valid BFF secret? (Should return 200 JSON)
curl -i "https://<YOUR_PUBLIC_WEB_DOMAIN>/api/public/catalog"
```

| Probe Result | Root Layer | Jump To |
| :--- | :--- | :--- |
| `/healthz` connection refused / `502 Bad Gateway` | `lovelace-app` exited at startup (fail-closed config guard) or proxy port mismatch | Sections 1.1–1.4, 2.5, 3.1 |
| `/healthz` `200`, `/readyz` `503` | Database role/migration mismatch, missing `TIRBACH_PUBLIC_API_SECRET`, or `TIRBACH_REQUIRE_EMAIL=true` without SMTP | Sections 1.2–1.3, 2.1 |
| `/api/healthz` `200`, `/api/public/catalog` `502`/`401`/`500` | `LOVELACE_API_BASE_URL` unreachable (e.g. IPv6 binding) or `LOVELACE_PUBLIC_API_SECRET` mismatch | Sections 2.1, 3.1 |
| All 4 probes `200 OK`, but bookings/prices/logins look wrong | Operational / configuration mistake inside the Admin Console | **Part II (Catastrophes 1–14)** |

---

# Part I: Deep Deployment Troubleshooting by Platform

---

## 1. Local & Self-Hosted Linux Server (Docker Compose, Caddy/Nginx & `systemd`)

### 1.1 Combining `docker-compose.yml` and `docker-compose.dev.yml` by Mistake
- **Symptom:** Containers fail to start, connect with wrong credentials, or `tirbach-server` exits immediately complaining about `tir_bach_dev_password` or `TIRBACH_AUTO_SEED must be false in production`.
- **Root Cause:** Running `docker compose -f docker-compose.yml -f docker-compose.dev.yml up` merges development placeholder secrets into a `TIRBACH_ENV=production` container, which Lovelace's fail-closed config loader (`internal/config/config.go`) intentionally rejects.
- **Fix:** Always run **either** `docker compose -f docker-compose.dev.yml up -d --build` (local dev only) **or** `docker compose up -d --build` (production only) — never both together.

### 1.2 PostgreSQL Authentication Failure After Changing `.env` Passwords on an Existing Volume
- **Symptom:** `db-bootstrap` or `postgres` healthcheck loops with `FATAL: password authentication failed for user "postgres"` (or `role "postgres" does not exist` on an older Phase 1.5 `tir_bach_pgdata` volume).
- **Root Cause:** PostgreSQL's official Docker image **only** reads `POSTGRES_USER` and `POSTGRES_PASSWORD` when initializing a brand-new, empty `/var/lib/postgresql/data` volume. Editing `.env` later does *not* change the password stored inside PostgreSQL's `pg_authid` catalog! Furthermore, legacy Phase 1.5 volumes were initialized with superuser `tir_bach` rather than `postgres`.
- **Fix:**
  1. **Never** run `docker compose down -v` (that deletes your database!).
  2. Restore the previous `POSTGRES_BOOTSTRAP_PASSWORD` in `.env` (and set `POSTGRES_BOOTSTRAP_USER=tir_bach` if upgrading a pre-existing Phase 1.5 volume) so `postgres` passes its healthcheck.
  3. If you want to rotate the bootstrap password, change it inside PostgreSQL *first*, then update `.env`:
     ```bash
     docker exec -it tir-bach-postgres psql \
       -U "${POSTGRES_BOOTSTRAP_USER:-postgres}" \
       -d "${POSTGRES_DB:-lovelace}" \
       -c "ALTER ROLE \"${POSTGRES_BOOTSTRAP_USER:-postgres}\" WITH PASSWORD '<NEW_PASSWORD>';"
     ```
  4. To rotate `lovelace_app` or `lovelace_migrator` passwords, update `LOVELACE_APP_PASSWORD` / `LOVELACE_MIGRATOR_PASSWORD` in `.env` and run:
     ```bash
     docker compose up -d --no-deps db-bootstrap app
     ```

### 1.3 `tirbach-server` Exits Immediately on Startup (Fail-Closed Production Guards)
Inspect `docker logs tir-bach-app` (or `journalctl -u lovelace-app -n 50`). Lovelace refuses to start in `TIRBACH_ENV=production` if any security invariant is violated:

| Startup Log Error | Exact Fix |
| :--- | :--- |
| `privileged database role rejected` | `DATABASE_URL` is connecting as `postgres` or a `SUPERUSER`/`CREATEDB`/`CREATEROLE` role. Connect strictly as `lovelace_app`. |
| `TIRBACH_COOKIE_SECURE must be true in production` | Set `TIRBACH_COOKIE_SECURE=true` and serve the Admin Console over `https://`. |
| `TIRBACH_TRUSTED_PROXIES must be explicitly configured` | When `TIRBACH_TRUST_PROXY=true`, explicitly list your reverse-proxy CIDRs (`127.0.0.1/32,::1/128,172.16.0.0/12,10.0.0.0/8`). |
| `invalid TIRBACH_ENCRYPTION_KEY` / `low-entropy value` | Generate a fresh 32-byte key (`openssl rand -hex 32` → 64 hex chars) — never use `0000...` or dev keys. |
| `TIRBACH_STRIPE_SECRET_KEY ... test key rejected in production` | Production rejects `sk_test_` / `rk_test_`. Either supply live `sk_live_` + `whsec_` keys, or leave **both** blank while `stripe_enabled=false`. |
| `invalid TIRBACH_PUBLIC_ORIGIN` | Must be a valid `https://` origin in production (no trailing path). |

### 1.4 Reverse Proxy Issues (`403 Security Token Invalid`, `413 Entity Too Large`, Broken Stripe Webhooks)
- **`403 Security Token Invalid` on `/login`:**
  1. You are visiting `http://` instead of `https://` while `TIRBACH_COOKIE_SECURE=true` (browser refuses to store/send the `Secure` cookie).
  2. Your Nginx/Apache/Cloudflare proxy is stripping the `Origin` header or rewriting `Host` (`Host` must match `Origin` for Lovelace's pre-session Login CSRF guard). Ensure `proxy_set_header Host $host;` and `proxy_set_header X-Forwarded-Proto https;` are set.
- **`413 Request Entity Too Large` when uploading a backup `.zip` or high-res property logo:**
  - Nginx defaults `client_max_body_size` to `1m`! Set `client_max_body_size 52m;` on your Nginx admin `server` block (or `request_body { max_size 52MB }` in Caddy) so 48 MiB backup archives upload cleanly.
- **Stripe Webhooks Returning `400` Signature Verification Errors:**
  - A proxy/WAF or JSON-formatting middleware modified whitespace in the POST body to `/api/webhooks/stripe`. Stripe's `Stripe-Signature` HMAC is computed over the exact raw bytes — pass `/api/webhooks/stripe` straight through unmodified.

### 1.5 Server Disk 100% Full (`No space left on device`)
- **Symptom:** Bookings fail with `500`, PostgreSQL stops accepting writes, or `docker compose up --build` fails mid-build.
- **Safe Cleanup (Preserves All Data Volumes):**
  ```bash
  df -h
  # Safe: removes unused build layers & old dangling images ONLY (never touch volumes!)
  docker builder prune -af
  docker image prune -af
  sudo journalctl --vacuum-time=7d
  ```
  > **NEVER** run `docker system prune --volumes` or `docker volume prune`!

---

## 2. Cloud Instances (AWS, Google Cloud Platform & Microsoft Azure)

### 2.1 Managed PostgreSQL 17 Role & Extension Errors (`RDS`, `Cloud SQL`, `Azure Flexible Server`)
- **`ERROR: must be able to SET ROLE "lovelace_migrator"` when running `ensure-roles.sh`:**
  - **Why:** On AWS RDS (`rds_superuser`), GCP Cloud SQL (`cloudsqlsuperuser`), and Azure Flexible Server (`azure_pg_admin`), the admin user is not a true PostgreSQL OS `SUPERUSER`. PostgreSQL 16/17 requires the executing user to be a member of `lovelace_migrator` before running `ALTER SCHEMA public OWNER TO lovelace_migrator`.
  - **Fix:** Connect as your cloud admin user and grant role membership first:
    ```sql
    CREATE ROLE lovelace_migrator WITH LOGIN PASSWORD '<STRONG_PW>' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT;
    GRANT lovelace_migrator TO "<YOUR_CLOUD_ADMIN_USER>";
    ```
    Then re-run `./scripts/postgres/ensure-roles.sh`.
- **`ERROR: extension "btree_gist" is not allow-listed for "azure_pg_admin"` (Azure Flexible Server):**
  - **Fix:** Allowlist `PGCRYPTO,BTREE_GIST` on the server before running migrations:
    ```bash
    az postgres flexible-server parameter set -g <RG> -s <SERVER> --name azure.extensions --value "PGCRYPTO,BTREE_GIST"
    ```
- **`ERROR: permission denied for table ...` as `lovelace_app` after a Manual Migration:**
  - **Why:** If new tables were created outside `docker-compose.yml` without re-running `ensure-roles.sh`, `lovelace_app` may lack `SELECT, INSERT, UPDATE, DELETE` on the newly added table.
  - **Fix:** Run `./scripts/postgres/ensure-roles.sh` once after running `tirbach-migrate` — it is 100% idempotent and grants strict DML-only access across all tables, views, and sequences.

### 2.2 GCP Cloud Run: Basket Holds Never Expire, Emails Stay Stuck in `PENDING`, Nightly Backups Never Run
- **Symptom:** HTTP pages load fast, but 15-minute checkout holds stay stuck past expiry, confirmation emails sit in the outbox until someone clicks a page, and scheduled 24-hour S3/GCS backups never trigger overnight.
- **Root Cause:** By default, Google Cloud Run uses *request-based CPU allocation*, which freezes container CPU to `0%` the millisecond an HTTP response finishes — pausing all Go background goroutines (`time.Ticker` loops)! Also, `min-instances=0` scales the container to zero overnight.
- **Fix:** Update `lovelace-app` with `--no-cpu-throttling` and `--min-instances=1`:
  ```bash
  gcloud run services update lovelace-app \
    --region=<REGION> \
    --min-instances=1 \
    --max-instances=1 \
    --no-cpu-throttling
  ```

### 2.3 Cloud Autoscaler Set `maxReplicas > 1` (Rate-Limit Dilution & Sporadic Behaviour)
- **Symptom:** Brute-force login protection takes `N × 8` attempts instead of `8`, or public BFF rate-limit buckets behave inconsistently.
- **Root Cause:** Both `lovelace-app` and `lovelace-public-web` maintain fast in-memory per-process sliding-window rate limiters designed for single-replica operation (`1` task/instance per service).
- **Fix:** Pin `desiredCount=1` (AWS ECS), `--min-instances=1 --max-instances=1` (GCP Cloud Run), or `--min-replicas 1 --max-replicas 1` (Azure Container Apps). Scale **vertically** (`1–2 vCPU`, `1–2 GiB RAM`) rather than horizontally unless a shared Redis limiter is added.

### 2.4 `permission denied` Uploading Logos or Maintenance Photos on AWS EFS / Azure Files
- **Symptom:** Saving text settings works, but uploading a PNG property logo or maintenance photo returns an internal storage error (`open /var/lib/lovelace/attachments/...: permission denied`).
- **Root Cause:** `Dockerfile` runs as non-root `USER tirbach` (`uid=100, gid=101` in `alpine:3.21`), while raw EFS file systems mount as `root:root` (`0755`).
- **Fix:**
  - **AWS EFS:** Create an **EFS Access Point** with POSIX User `Uid: 100, Gid: 101` and Root Directory `/lovelace-attachments` with Creation Info `OwnerUid: 100, OwnerGid: 101, Permissions: "0750"`, and mount via that Access Point ID in your ECS Task Definition.
  - **Azure Files / Kubernetes PVC:** Mount with `uid=100,gid=101,dir_mode=0750,file_mode=0640` (or `securityContext.fsGroup: 101`).

### 2.5 Load Balancer Restart Loops When Pointing Liveness Probes at `/readyz`
- **Symptom:** Your SMTP provider (Resend / SES / Postmark) has a brief 2-minute outage or an expired API key; suddenly AWS ECS / Cloud Run / Azure starts killing and restarting `lovelace-app` every 60 seconds!
- **Root Cause:** `/readyz` checks external dependencies (including SMTP when `TIRBACH_REQUIRE_EMAIL=true`) and returns `503` when degraded. If a container orchestrator uses `/readyz` as a **container kill/liveness probe**, an email outage causes container restarts!
- **Fix:**
  - Point **container liveness / healthcheck probes** at **`/healthz`** (or `tirbach-server -healthcheck`, which hits `/healthz`).
  - Point **external uptime alerts & deployment gates** at **`/readyz`**.

---

## 3. Railway (and Container PaaS Platforms: Fly.io & Render)

### 3.1 `public-web` Shows *"Unable to connect to the reservation system right now"* on Railway
- **Symptom:** Both `lovelace-app` and `lovelace-public-web` show green `SUCCESS` deployments in Railway, `/readyz` on `lovelace-app` returns `200 OK`, yet opening the public booking site fails to load accommodation!
- **Root Cause (3 common culprits on Railway):**
  1. **IPv4-only bind on an IPv6 private network:** Railway's `.railway.internal` private DNS resolves to IPv6 (`fd12::/16`) addresses! If `TIRBACH_LISTEN_ADDR` on `lovelace-app` was set to `0.0.0.0:8080` instead of `:8080`, Go listens on IPv4 only and refuses internal IPv6 connections from `lovelace-public-web`! Similarly, `lovelace-public-web` needs `HOSTNAME=::`.
  2. **Secret mismatch:** `LOVELACE_PUBLIC_API_SECRET` on `lovelace-public-web` does not match `TIRBACH_PUBLIC_API_SECRET` on `lovelace-app` (or has an accidental trailing newline).
  3. **Default slug mismatch:** `LOVELACE_PROPERTY_SLUG` on `lovelace-public-web` is still set to `tir-bach`, whereas you named your property `orchard-cottages` in the Setup Wizard!
- **Fix:**
  1. On `lovelace-app`, set `TIRBACH_LISTEN_ADDR=:8080`.
  2. On `lovelace-public-web`, set `HOSTNAME=::`, `LOVELACE_API_BASE_URL=http://<app-service-name>.railway.internal:8080`, `LOVELACE_PROPERTY_SLUG=<your-actual-property-slug>`, and ensure `LOVELACE_PUBLIC_API_SECRET` matches `TIRBACH_PUBLIC_API_SECRET` exactly.
  3. Include `100.64.0.0/10,fd12::/16` in both `TIRBACH_TRUSTED_PROXIES` and `LOVELACE_TRUSTED_PROXIES`.

### 3.2 Railway CLI Fails with `prefix not found` or Deploys the Wrong Dockerfile to `public-web`
- **Symptom:** Running `railway up .` fails with `prefix not found`, or deploying `lovelace-public-web` builds the Go backend instead of the Next.js frontend!
- **Root Cause:**
  1. `railway up` matches your current working directory's real path (`realpath`) against `~/.railway/config.json` (on macOS, `/tmp` is a symlink to `/private/tmp`).
  2. Deploying `public-web` without `--path-as-root` uploads the repo root `Dockerfile` instead of `apps/public-web/Dockerfile`.
- **Fix:**
  ```bash
  cd "$(pwd -P)"
  railway link --project <PROJECT_ID> --environment production
  railway up --service lovelace-app --environment production --detach
  railway up apps/public-web --path-as-root --service lovelace-public-web --environment production --detach
  ```

### 3.3 Railway Monthly Spend Limit Reached Mid-Month or Forgotten Public TCP Proxy
- **Spend Cap:** If services pause because the workspace hit a low trial/hard spend limit, raise the workspace usage cap in **Railway → Workspace → Billing / Usage** and redeploy/wake the service — persistent volumes (`pg17` and `attachments`) remain intact.
- **Security Check:** Verify in **Railway → `lovelace-pg17` → Settings → Networking** that **no TCP Proxy** is left enabled after running initial migrations — `lovelace-pg17` should only show its `.railway.internal` private hostname.

---

# Part II: The "Hotel Owner Catastrophe" Playbook (14 Real-World Disasters & Rescues)

Hospitality owners are busy welcoming guests, cooking breakfast, and dealing with muddy boots — not reading software manuals. Here is every realistic way an owner or receptionist can accidentally cause chaos in Lovelace, and how to fix each one calmly.

---

## Catastrophe 1: The Pence-vs-Pounds Pricing Disaster
> *"Help! I tried to set our £140/night Sea View Room for August, typed `140` into the Seasonal Rate Rule box, and someone just booked 4 nights in August for £5.60 total!! (Or I typed `14000` in the Setup Wizard and our room is listed at £14,000 a night!)"*

- **Why It Happened:**
  - In the **Guided Setup Wizard (`/onboarding`)**, money boxes take pounds & pence with a decimal point (e.g. `140.00`).
  - In the advanced **Inventory & Holds → Rates & Preview (`/rates`) → Add Seasonal / Weekend Rate Rule** form, the field is labelled **Nightly Rate (pence)** (so `£140.00` is `14000`, whereas `140` means `£1.40`!).
- **Immediate 60-Second Fix in the Browser:**
  1. Go to **Inventory & Holds → Rates & Preview (`/rates`)**.
  2. Scroll down to the **Active Rate Rules** table, find the `£1.40` (or `£14,000`) rule, and click **Archive** right next to it.
  3. Create a fresh rule with `14000` (for `£140.00`), then test those August dates in the **Tariff Preview Calculator** at the top of `/rates` to see the exact night-by-night price in `£` before walking away!
  4. If a guest already booked at `£1.40/night`, open **Reservations**, click their booking reference, contact them politely, and cancel/refund or adjust as appropriate under your booking terms.

---

## Catastrophe 2: The Total Owner Lockout
> *"I forgot my password, I lost the piece of paper with my 8 recovery codes during spring cleaning, AND I guessed my password 10 times in a row so now Lovelace won't even let me try!"*

- **Why It Happened:**
  - After `8` failed sign-in attempts for an email (or `20` from one IP) within `15` minutes, Lovelace temporarily rate-limits login attempts to stop hackers guessing passwords.
  - Because passwords and recovery codes are one-way cryptographically hashed in PostgreSQL, nobody can "read" the old password out of a file — and restarting the server with `TIRBACH_BOOTSTRAP_ADMIN_PASSWORD` intentionally **will not** overwrite an existing database with accounts in it!
- **Rescue Path A — If a Spouse / Co-Owner Also Has an `owner_admin` Login:**
  1. Have them sign in on their account → **Settings → Admin Users (`/users`)**.
  2. Find your account and use **Reset Password** to set a temporary 12-character password for you.
  3. Wait 15 minutes for the failed-attempt cooldown to clear, sign in with that temporary password, set your new password, and **print/download your new 8 recovery codes**!
- **Rescue Path B — Break-Glass Server Rescue (When You Are the ONLY `owner_admin`):**
  Have your IT helper / server administrator run this safe break-glass reset against PostgreSQL (works on Docker Compose, Cloud, or Railway without touching any bookings or properties!):
  ```bash
  # 1. Clear the 15-minute failed-login cooldown for your email:
  psql "$DATABASE_URL" -c "DELETE FROM login_attempts WHERE LOWER(email) = LOWER('you@yourdomain.com');"

  # 2. Generate a valid Argon2id hash for a temporary password using Lovelace's own auth package
  #    and set must_rotate_password = TRUE on your account:
  go run -C /opt/lovelace /dev/stdin <<'EOF'
  package main
  import (
      "context"
      "fmt"
      "os"
      "github.com/jackc/pgx/v5/pgxpool"
      "github.com/tirbach/tir-bach/internal/auth"
  )
  func main() {
      email := os.Getenv("RESET_EMAIL")
      tempPass := os.Getenv("RESET_TEMP_PASSWORD") // >= 12 chars
      hash, err := auth.HashPassword(tempPass)
      if err != nil { panic(err) }
      pool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
      if err != nil { panic(err) }
      defer pool.Close()
      tag, err := pool.Exec(context.Background(), `
          UPDATE admin_users
          SET password_hash = $1, must_rotate_password = TRUE, is_active = TRUE, updated_at = NOW()
          WHERE LOWER(email) = LOWER($2)
      `, hash, email)
      if err != nil || tag.RowsAffected() == 0 {
          panic(fmt.Sprintf("update failed (rows=%d, err=%v)", tag.RowsAffected(), err))
      }
      fmt.Println("Emergency temporary password set! Sign in now to rotate and generate 8 new recovery codes.")
  }
  EOF
  ```

---

## Catastrophe 3: The Accidental Whole-Hotel Summer Blockout
> *"I wanted to close Room 3 for two nights to repaint the ceiling, accidentally left `Target Scope` on `All Units on Property` for 1st July to 1st September, and now our entire hotel shows NO AVAILABILITY all summer!"*

- **Immediate 30-Second Fix in the Browser:**
  1. Open **Inventory & Holds → Bulk Blockouts (`/blocks`)** in the left-hand menu.
  2. Scroll down to **Blockout Groups** — you will see your `PROPERTY` blockout right at the top with the reason you typed.
  3. Click **Release** (or expand it to release every room except Room 3!).
  4. Every room is back on sale immediately — zero bookings were harmed.

---

## Catastrophe 4: The Check-Out Date Off-By-One Trap
> *"Mr. Evans phoned to book Friday night AND Saturday night (2 nights). I put Arrival = Friday 14th and Departure = Saturday 15th. Now Mrs. Patel booked Saturday 15th online and both families want the same cottage on Saturday night!"*

- **Why It Happened:** In all hotel systems, **Departure Date** is the **morning the guest hands back the keys and leaves** (so a Friday + Saturday stay is Arrival `Friday 14th` → Departure `Sunday 16th`).
- **Immediate Fix:**
  1. Check **Booking Calendar (`/calendar`)** right away to see if another room/cottage/pitch of the same (or nicer!) category is free on Saturday 15th.
  2. If another unit is free: open **Reservations**, click either Mr. Evans's or Mrs. Patel's booking, move/rebook onto the free unit so Mr. Evans has `Friday 14th → Sunday 16th`, and drop them a quick courtesy note if their room number changed.
  3. **Habit to prevent this forever:** Glance at the **`Nights`** counter on the booking summary right after saving — a Friday + Saturday weekend stay will always say **`2 nights`**.

---

## Catastrophe 5: The "Tidy-Up" Archive Spree
> *"I didn't like how our pitch codes looked (`MP-01`..`MP-06`), so I went into `Units & Pitches` and clicked **Archive** on all of them. Now our public website says `No accommodation available` for every single date of the year!"*

- **Why It Happened:**
  - **Unit Types** (e.g. *Meadow Grass Pitch*) describe the category and price, while **Units & Pitches** (`MP-01`..`MP-06`) are the actual physical spaces guests sleep in. If `0` active physical units exist under a Unit Type, Lovelace knows you have `0` spaces to sell!
- **Immediate Fix in the Browser:**
  1. Open **Inventory & Holds → Units & Pitches (`/units`)** (or open **Settings → Setup Wizard → Step 2** to bulk-create a fresh batch in one click!).
  2. Add your active rooms/pitches under that Unit Type — availability returns immediately!
- **Optional IT Helper SQL (to un-archive the exact original units if preferred):**
  ```sql
  UPDATE units
  SET archived_at = NULL, status = 'active', version = version + 1, updated_at = NOW()
  WHERE property_id = '<PROPERTY_UUID>' AND archived_at IS NOT NULL;
  ```

---

## Catastrophe 6: The "Fixed Boiler That Still Blocks Bookings" Mystery
> *"Our handyman fixed the leaking shower in Shepherd's Hut 1 yesterday and marked the Maintenance job `COMPLETED` — why is Shepherd's Hut 1 STILL blocked on the Booking Calendar?!"*

- **Why It Happened (Intentional Safety Feature):**
  - When a maintenance ticket is logged with **Availability Impact = `UNAVAILABLE`**, Lovelace holds that room offline not just while the repair is happening, but **until a manager or housekeeper confirms the room is clean, safe, and inspected**! (`COMPLETED` means *"repair done, awaiting sign-off"*; `CLOSED` after inspection means *"ready for paying guests"*.)
- **Immediate 20-Second Fix in the Browser:**
  1. Open **Maintenance & Facilities → Maintenance (`/maintenance`)** on the left.
  2. Click the `COMPLETED` work order for Shepherd's Hut 1.
  3. Fill in the **Post-Completion Inspection** box, mark it **PASSED**, and submit — the maintenance hold releases automatically and Shepherd's Hut 1 is back on sale!

---

## Catastrophe 7: The "Three Weeks, Zero Online Card Bookings" Mystery
> *"I pasted the booking widget onto our website three weeks ago. People keep ringing us saying 'Your website says Online Card Checkout Unavailable — please contact the property team'!"*

- **Why It Happened:**
  - Every new property in Lovelace starts in **Safe Dark-by-Default Launch Mode** (`Enable Public Online Checkout (Stripe)` is **unticked** in **Site Settings**!) so you never take real card payments before your prices and terms are ready.
- **Immediate Fix:**
  1. Ensure your server/cloud administrator has set `TIRBACH_STRIPE_SECRET_KEY` (`sk_live_...`) and `TIRBACH_STRIPE_WEBHOOK_SECRET` (`whsec_...` for `https://<ADMIN_DOMAIN>/api/webhooks/stripe`).
  2. Sign in to Lovelace → **Settings → Site Settings (`/property`)**.
  3. Scroll to **Public Direct Booking & Payment Policy**, tick **Enable Public Online Checkout (Stripe)**, and click **Save Booking Policy**.
- **Bonus Check — What if a guest paid right as their 15-minute basket timer ran out?**
  - Check **Maintenance & Facilities → Payment Exceptions (`/payment-exceptions`)** (also flagged right on your morning **Operations Centre**!). Lovelace never loses track of a late Stripe payment: it automatically attempts a safe Stripe refund if the room was taken in the meantime, or logs a clear Payment Exception for you to review in one click.

---

## Catastrophe 8: Clicking "Anonymise Guest (GDPR)" Instead of "Cancel Booking"
> *"Mrs. Jenkins phoned to cancel next month's stay. Instead of cancelling her reservation, I opened `Guest Directory`, found Mrs. Jenkins, and tried to click `Anonymise Guest`!"*

- **Good News First (Built-In Guardrail):**
  - If Mrs. Jenkins has **any active or upcoming reservation** (`PENDING`, `CONFIRMED`, or `CHECKED_IN`), Lovelace **refuses to anonymise her** and displays an error telling you to resolve active bookings first!
- **How to Actually Cancel a Booking:**
  - Go to **Reservations (`/reservations`)** → click the booking reference (e.g. `TB-2026-...`) → use the **Cancel Reservation** action on the booking page! That frees up the room on the calendar while keeping Mrs. Jenkins's contact details intact.
- **What if you *did* anonymise a past guest by mistake?**
  - GDPR Erasure permanently replaces their name, email, phone, and notes with `REDACTED` in the live database (while keeping financial/VAT totals intact for your accountant). If you had a lawful business/tax obligation to retain that invoice contact, you can inspect an earlier weekly `.zip` backup offline without overwriting your live database.

---

## Catastrophe 9: Merging Two Different "David Jones" Guests Together
> *"We have two guests called David Jones — one is a lovely retired teacher from Swansea, and the other brought 4 unauthorised dogs last year — and I just clicked Merge on them in `Duplicate Merge Review`!"*

- **Why It Happened:** Clicked merge on two guests who shared a name without checking their email/phone/postcode first.
- **How to Handle It Cleanly:**
  1. Open **Guests & Comms → Guest Directory (`/guests`)**, open the merged David Jones profile, and edit the **Internal Staff Notes** to clearly distinguish the two email addresses/phone numbers so reception doesn't turn away the retired teacher!
  2. When the *other* David Jones books next time by phone, enter his distinct email address and add a clear note directly on his specific **Reservation** rather than relying on the shared surname.
  3. In future on **Duplicate Merge Review (`/guests/merge`)**, click **Dismiss (Not Same Person)** whenever two guests merely share a common surname — Lovelace remembers your dismissal permanently so it won't ask again!

---

## Catastrophe 10: Changing Domains or Brand Colours Broke the Website Booking Box
> *"Our web designer moved our website to a new domain (or switched us to WordPress), and now the booking box on our website won't resize or says `Booking Section Temporarily Unavailable`!"*

- **Why It Happened (Check These 3 Things):**
  1. **WordPress stripped `<script>` tags:** If someone pasted the raw HTML `<iframe> + <script>` snippet into WordPress as a non-Admin editor, WordPress strips `<script>`, stopping auto-resize.
     - **Fix:** Go to Lovelace **Settings → Site Settings → Website Booking Section**, click **Download WordPress Plugin (.zip)**, upload it in **WordPress → Plugins → Add New → Upload Plugin**, and use the `[lovelace_booking]` shortcode or Gutenberg block!
  2. **Wrong URL in WordPress Settings:** WordPress **Settings → Lovelace Booking → Public Booking Site URL** must point to your **Public Booking Website** (`https://book.yourdomain.com`), *not* your Admin Console URL (`https://admin.yourdomain.com`)! Click **Test Connection & Sync Brand Colours** in WordPress to verify.
  3. **Public domain changed without updating `TIRBACH_PUBLIC_ORIGIN`:** If you changed your public booking domain (`book.yourdomain.com`), update both `TIRBACH_PUBLIC_ORIGIN` (on `lovelace-app`) and `NEXT_PUBLIC_SITE_URL` (on `lovelace-public-web`), then re-copy your embed snippet or click **Test Connection** in WordPress.

---

## Catastrophe 11: "Backup Restore Refused — Environment Is Not Empty!"
> *"Our old laptop/server died! I set up a brand-new Lovelace server, clicked through Step 1 of the Setup Wizard to test it, and now when I try to upload last Sunday's `.zip` backup on `/settings/backups`, Lovelace refuses to restore it!"*

- **Why It Happened (Safety Interlock):**
  - Lovelace's **Restore Into Empty Environment** feature intentionally **refuses** to wipe a database that already has properties or bookings in it — that stops someone from accidentally uploading an old 2025 backup over their live 2026 hotel bookings!
  - Because you clicked **Save** on Step 1 of the Setup Wizard on the new server, the new database now has `1` property in it instead of `0`!
- **Immediate Fix on the New Replacement Server:**
  Start with a genuinely empty database (`0` properties, `1` bootstrapped `owner_admin`), go **straight** to **Settings → Backups & Cloud Sync (`/settings/backups`)** *before* creating any property in `/onboarding`, and upload your `.zip` / `.zip.enc` backup!
- **What if I forgot my `.zip.enc` Encryption Passphrase vs my Admin Password?**
  - Remember there are **two** password boxes when restoring an encrypted `.zip.enc` file:
    1. **Current Admin Password:** The password you *just* used to sign in to the new Lovelace screen today.
    2. **Archive Encryption Passphrase:** The 12+ character passphrase you chose when creating the `.zip.enc` backup (if you left that blank when downloading a plain `.zip`, leave it blank on restore too!).
- **What if we lost `TIRBACH_ENCRYPTION_KEY` when moving servers?**
  - **Don't panic!** All of your **properties, rooms, pitches, bookings, calendar dates, guest records, prices, logos, and photos** are restored 100%! `TIRBACH_ENCRYPTION_KEY` is only used for external API keys stored in the vault — so after restoring onto a new server with a new `TIRBACH_ENCRYPTION_KEY`, simply re-enter your OTA Channel Manager API key (`/settings/integrations/channel-manager`) and your Cloud Backup bucket key (`/settings/backups`) once.

---

## Catastrophe 12: Double-Booking From an Unconnected Airbnb / Booking.com Listing
> *"Lovelace let Mrs. Brown book Cottage 1 on our website for the exact same weekend Mr. Green booked Cottage 1 on Airbnb!"*

- **Why It Happened:**
  - Lovelace guarantees zero double-bookings across your direct website, embedded WordPress widget, telephone bookings, blockouts, **and any connected Channel Manager**. However, if you list Cottage 1 separately on Airbnb/Booking.com *without* connecting a Channel Manager (or if you changed your Beds24/Smoobu/Lodgify/Channex API password so the connection went into `CONNECTION_ERROR`), Lovelace isn't told about Airbnb bookings!
- **Immediate Fix:**
  1. Open **Settings → Channel Manager (`/settings/integrations/channel-manager`)** and check the status badge:
     - Green **`CONNECTED`**: Sync is healthy.
     - Red **`CONNECTION_ERROR`** or Amber **`CONFIGURATION_REQUIRED`**: Re-enter your channel credentials and test sync immediately.
  2. Until Channel Manager sync is active, whenever an external Airbnb/Pitchup booking arrives by email, add it straight into Lovelace via **+ New Reservation** (choosing `AIRBNB` or `OTA` under **Booking Source / Channel**) within a couple of minutes so those nights are immediately blocked on your direct website!

---

## Catastrophe 13: Two People Editing at Once ("Version Conflict") or Cleaner Accidentally Changing Settings
> *"My husband and I both had `Site Settings` open on two computers, I clicked Save, and Lovelace gave me a conflict error — AND earlier our cleaner accidentally clicked into `Rates` on the reception iPad!"*

- **Why the Conflict Error Happened:**
  - Lovelace uses *version checking* on settings, rates, and bookings: if two tabs open Version `3` at 9:00 AM, Person A saves at 9:05 AM (bumping it to Version `4`), and Person B clicks Save on their stale tab at 9:30 AM, Lovelace stops Person B from accidentally wiping out Person A's 9:05 AM change!
  - **Fix:** Just refresh the page (`Cmd+R` / `F5`), review the current values, and click Save again.
- **How to Stop Cleaners or Reception Helpers Touching Prices & Backups:**
  - Never leave your **Owner Admin** account signed in on a shared reception iPad!
  - Go to **Settings → Admin Users (`/users`)** and create separate logins with the right role:
    - **`housekeeping_staff`** — can update cleaning/changeover boards and report a broken lamp, *cannot* touch prices, bookings, or settings.
    - **`maintenance_staff`** — can manage repairs and inspections, *cannot* touch prices or settings.
    - **`staff` (Front Desk)** — can take phone bookings and check guests in/out, *cannot* delete backups, manage users, or alter core site settings.
    - **`viewer`** — strictly read-only view.

---

## Catastrophe 14: Guests Are Booking Fine, But Nobody Is Getting Confirmation Emails
> *"Guests are booking and paying fine, they appear on our Booking Calendar, yet three guests rang today asking 'Did my booking go through? I haven't had an email!'"*

- **Why It Happened:**
  - Lovelace saves every confirmation email safely into a durable database **Outbox (`email_outbox`)** *inside* the booking transaction before ever talking to your email provider. That way, even if your email provider (Resend / Postmark / SES) is down, **you never lose a booking**!
  - Usually either `TIRBACH_SMTP_ADDR` wasn't configured yet, your SMTP API key expired, or your domain's DNS (`SPF` / `DKIM`) record was edited by your web host.
- **Immediate Fix:**
  1. Look at your morning **Operations Centre (`/`)** under **Guest Relations & Communications** — check if **`failed emails`** is greater than `0`.
  2. Open **Guests & Comms → Templates & Automation (`/communications/settings`)** to inspect outbox delivery status and any SMTP error message.
  3. Have your administrator verify `TIRBACH_SMTP_ADDR`, `TIRBACH_SMTP_USERNAME`, `TIRBACH_SMTP_PASSWORD`, and `TIRBACH_EMAIL_FROM` (ensuring the `From` domain is verified in Resend / Postmark / AWS SES). Once fixed, pending/retriable outbox emails send automatically — and you can view every guest's communication history directly on their profile in **Guest Directory (`/guests`)**.
