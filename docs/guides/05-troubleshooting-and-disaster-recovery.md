# Guide 5: Deep Troubleshooting & The Hospitality Catastrophe Playbook

This runbook has three parts:

- **Part I — Infrastructure & Deployment Troubleshooting:** Deep technical diagnostics and exact fixes for **1) Local & Self-Hosted Linux Servers**, **2) AWS, Google Cloud (GCP) & Microsoft Azure**, and **3) Railway & PaaS Platforms**.
- **Part II — The Hospitality Catastrophe Playbook (21 Real-World Disasters & Rescues):** Every realistic way a busy owner or helper can accidentally break their setup across **any property**, plus dedicated disaster rescues for **Campsites & Holiday Parks**, **Glamping Retreats**, **B&Bs & Guesthouses**, and **Multi-Site Portfolio Operators**.
- **Part III — 15 Deep Operational, Security & Integration Troubleshooting Scenarios (Issues 22–36):** Code-verified diagnostics and step-by-step fixes for fail-closed Stripe/Portal startup loops, `403 origin_not_allowed` & `400 query_token_forbidden`, missing Stripe webhook bookings & `rk_live_` refund permissions, `DISPATCH_UNKNOWN` outbox fencing & GDPR `409` locks, OTA `UNMAPPED` / `CONFLICT_OVERBOOKED` / `422` sync blocks / `401` webhook drift, non-auto-releasing maintenance restrictions, unready-unit alerts, `LOVELACE_ENCRYPTION_KEY` mismatches, `48 MiB` `.zip.enc` backup restores, Guest Portal edge cases, BI report warnings, and WordPress/iframe embeds.

---

## Rapid 60-Second Triage (Start Here First)

Before changing any configuration, check these four probes to pinpoint which layer is unhappy:

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
| `/healthz` connection refused / `502 Bad Gateway` | `lovelace-app` exited at startup (fail-closed config guard) or proxy port mismatch | Part I, Sections 1.1–1.4, 2.5, 3.1 |
| `/healthz` `200`, `/readyz` `503` | Database role/migration mismatch, missing `LOVELACE_PUBLIC_API_SECRET`, or `LOVELACE_REQUIRE_EMAIL=true` without SMTP | Part I, Sections 1.2–1.3, 2.1 |
| `/api/healthz` `200`, `/api/public/catalog` `502`/`401`/`500` | `LOVELACE_API_BASE_URL` unreachable (e.g. IPv6 binding) or `LOVELACE_PUBLIC_API_SECRET` mismatch | Part I, Sections 2.1, 3.1 |
| All 4 probes `200 OK`, but bookings/prices/logins/sites look wrong | Operational or configuration mistake inside the Admin Console | **Part II (Catastrophes 1–21)** |
| Webhooks, Stripe, OTA sync, Guest Portal, Email outbox, Reports, or WordPress embeds failing | Security gate, HMAC/Origin check, lease fence, or integration state | **Part III (Issues 22–36)** |

---

# Part I: Deep Deployment Troubleshooting by Platform

---

## 1. Local & Self-Hosted Linux Server (Docker Compose, Caddy/Nginx & `systemd`)

### 1.1 Combining `docker-compose.yml` and `docker-compose.dev.yml` by Mistake
- **Symptom:** Containers fail to start, connect with wrong credentials, or `noteG-server` exits immediately complaining about `lovelace_dev_password` or `LOVELACE_AUTO_SEED must be false in production`.
- **Root Cause:** Running `docker compose -f docker-compose.yml -f docker-compose.dev.yml up` merges development placeholder secrets into a `LOVELACE_ENV=production` container, which Lovelace's fail-closed config loader (`internal/config/config.go`) intentionally rejects.
- **Fix:** Always run **either** `docker compose -f docker-compose.dev.yml up -d --build` (local dev only) **or** `docker compose up -d --build` (production only) — never both together.

### 1.2 PostgreSQL Authentication Failure After Changing `.env` Passwords on an Existing Volume
- **Symptom:** `db-bootstrap` or `postgres` healthcheck loops with `FATAL: password authentication failed for user "postgres"` (or `role "postgres" does not exist` on an older Phase 1.5 `lovelace_pgdata` volume).
- **Root Cause:** PostgreSQL's official Docker image **only** reads `POSTGRES_USER` and `POSTGRES_PASSWORD` when initializing a brand-new, empty `/var/lib/postgresql/data` volume. Editing `.env` later does *not* change the password stored inside PostgreSQL's `pg_authid` catalog! Furthermore, legacy Phase 1.5 volumes were initialized with superuser `lovelace` rather than `postgres`.
- **Fix:**
  1. **Never** run `docker compose down -v` (that deletes your database!).
  2. Restore the previous `POSTGRES_BOOTSTRAP_PASSWORD` in `.env` (and set `POSTGRES_BOOTSTRAP_USER=lovelace` if upgrading a pre-existing Phase 1.5 volume) so `postgres` passes its healthcheck.
  3. If you want to rotate the bootstrap password, change it inside PostgreSQL *first*, then update `.env`:
     ```bash
     docker exec -it lovelace-postgres psql \
       -U "${POSTGRES_BOOTSTRAP_USER:-postgres}" \
       -d "${POSTGRES_DB:-lovelace}" \
       -c "ALTER ROLE \"${POSTGRES_BOOTSTRAP_USER:-postgres}\" WITH PASSWORD '<NEW_PASSWORD>';"
     ```
  4. To rotate `lovelace_app` or `lovelace_migrator` passwords, update `LOVELACE_APP_PASSWORD` / `LOVELACE_MIGRATOR_PASSWORD` in `.env` and run:
     ```bash
     docker compose up -d --no-deps db-bootstrap app
     ```

### 1.3 `noteG-server` Exits Immediately on Startup (Fail-Closed Production Guards)
Inspect `docker logs lovelace-app` (or `journalctl -u lovelace-app -n 50`). Lovelace refuses to start in `LOVELACE_ENV=production` if any security invariant is violated:

| Startup Log Error | Exact Fix |
| :--- | :--- |
| `privileged database role rejected` | `DATABASE_URL` is connecting as `postgres` or a `SUPERUSER`/`CREATEDB`/`CREATEROLE` role. Connect strictly as `lovelace_app`. |
| `LOVELACE_COOKIE_SECURE must be true in production` | Set `LOVELACE_COOKIE_SECURE=true` and serve the Admin Console over `https://`. |
| `LOVELACE_TRUSTED_PROXIES must be explicitly configured` | When `LOVELACE_TRUST_PROXY=true`, explicitly list your reverse-proxy CIDRs (`127.0.0.1/32,::1/128,172.16.0.0/12,10.0.0.0/8`). |
| `invalid LOVELACE_ENCRYPTION_KEY` / `low-entropy value` | Generate a fresh 32-byte key (`openssl rand -hex 32` → 64 hex chars) — never use `0000...` or dev keys. |
| `LOVELACE_STRIPE_SECRET_KEY ... test key rejected in production` | Production rejects `sk_test_` / `rk_test_`. Either supply live `sk_live_` + `whsec_` keys, or leave **both** blank while `stripe_enabled=false`. |
| `invalid LOVELACE_PUBLIC_ORIGIN` | Must be a valid `https://` origin in production (no trailing path). |

### 1.4 Reverse Proxy Issues (`403 Security Token Invalid`, `413 Entity Too Large`, Broken Stripe Webhooks)
- **`403 Security Token Invalid` on `/login`:**
  1. You are visiting `http://` instead of `https://` while `LOVELACE_COOKIE_SECURE=true` (browser refuses to store/send the `Secure` cookie).
  2. Your Nginx/Apache/Cloudflare proxy is stripping the `Origin` header or rewriting `Host` (`Host` must match `Origin` for Lovelace's pre-session Login CSRF guard). Ensure `proxy_set_header Host $host;` and `proxy_set_header X-Forwarded-Proto https;` are set.
- **`413 Request Entity Too Large` when uploading a backup `.zip` or high-res property logo:**
  - Nginx defaults `client_max_body_size` to `1m`. Set `client_max_body_size 52m;` on your Nginx admin `server` block (or `request_body { max_size 52MB }` in Caddy) so 48 MiB backup archives upload cleanly.
- **Stripe Webhooks Returning `400` Signature Verification Errors (or `404 Not Found`):**
  - Confirm your Stripe Dashboard endpoint URL is `https://<ADMIN_DOMAIN>/api/public/v1/webhooks/stripe` (not `/api/webhooks/stripe`).
  - If returning `400`, a proxy/WAF or JSON-formatting middleware modified whitespace in the POST body to `/api/public/v1/webhooks/stripe`. Stripe's `Stripe-Signature` HMAC is computed over the exact raw bytes — pass `/api/public/v1/webhooks/stripe` straight through unmodified.

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
  - **Fix:** Run `./scripts/postgres/ensure-roles.sh` once after running `lovelace-migrate` — it is 100% idempotent and grants strict DML-only access across all newly created tables, views, and sequences.

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
- **Root Cause:** `Dockerfile` runs as non-root `USER lovelace` (`uid=100, gid=101` in `alpine:3.21`), while raw EFS file systems mount as `root:root` (`0755`).
- **Fix:**
  - **AWS EFS:** Create an **EFS Access Point** with POSIX User `Uid: 100, Gid: 101` and Root Directory `/lovelace-attachments` with Creation Info `OwnerUid: 100, OwnerGid: 101, Permissions: "0750"`, and mount via that Access Point ID in your ECS Task Definition.
  - **Azure Files / Kubernetes PVC:** Mount with `uid=100,gid=101,dir_mode=0750,file_mode=0640` (or `securityContext.fsGroup: 101`).

### 2.5 Load Balancer Restart Loops When Pointing Liveness Probes at `/readyz`
- **Symptom:** Your SMTP provider (Resend / SES / Postmark) has a brief 2-minute outage or an expired API key; suddenly AWS ECS / Cloud Run / Azure starts killing and restarting `lovelace-app` every 60 seconds!
- **Root Cause:** `/readyz` checks external dependencies (including SMTP when `LOVELACE_REQUIRE_EMAIL=true`) and returns `503` when degraded. If a container orchestrator uses `/readyz` as a **container kill/liveness probe**, an email outage causes container restarts!
- **Fix:**
  - Point recurring **container kill / liveness probes** on AWS ECS, Cloud Run, Kubernetes, or Azure at **`GET /healthz`** (`curl -fsS http://127.0.0.1:8080/healthz`). *(Note: `noteG-server -healthcheck` probes **`/readyz`**, which is great for Docker Compose `service_healthy` startup ordering and deployment gates, but should not be wired as an aggressive container-kill liveness probe when external SMTP may flap.)*
  - Point **external uptime alerts & deployment readiness gates** at **`/readyz`**.

---

## 3. Railway (and Container PaaS Platforms: Fly.io & Render)

### 3.1 `public-web` Shows *"Unable to connect to the reservation system right now"* on Railway
- **Root Cause (3 common culprits on Railway):**
  1. **IPv4-only bind on an IPv6 private network:** Railway's `.railway.internal` private DNS resolves to IPv6 (`fd12::/16`) addresses. If `LOVELACE_LISTEN_ADDR` on `lovelace-app` was set to `0.0.0.0:8080` instead of `:8080`, Go listens on IPv4 only and refuses internal IPv6 connections from `lovelace-public-web`! Similarly, `lovelace-public-web` needs `HOSTNAME=::`.
  2. **Secret mismatch:** `LOVELACE_PUBLIC_API_SECRET` on `lovelace-public-web` does not match `LOVELACE_PUBLIC_API_SECRET` on `lovelace-app` (or has an accidental trailing newline).
  3. **Default slug mismatch:** `LOVELACE_PROPERTY_SLUG` on `lovelace-public-web` is still set to `demo-property`, whereas you named your property `orchard-cottages` in the Setup Wizard!
- **Fix:**
  1. On `lovelace-app`, set `LOVELACE_LISTEN_ADDR=:8080`.
  2. On `lovelace-public-web`, set `HOSTNAME=::`, `LOVELACE_API_BASE_URL=http://<app-service-name>.railway.internal:8080`, `LOVELACE_PROPERTY_SLUG=<your-actual-property-slug>`, and ensure `LOVELACE_PUBLIC_API_SECRET` matches `LOVELACE_PUBLIC_API_SECRET` exactly.
  3. Include `100.64.0.0/10,fd12::/16` in `LOVELACE_TRUSTED_PROXIES` on both `lovelace-app` and `lovelace-public-web`.

### 3.2 Railway CLI Fails with `prefix not found` or Deploys the Wrong Dockerfile to `public-web`
- **Root Cause:** `railway up` matches your current working directory's real path (`realpath`) against `~/.railway/config.json` (on macOS, `/tmp` is a symlink to `/private/tmp`), and deploying `public-web` without `--path-as-root` uploads the repo root `Dockerfile` instead of `apps/public-web/Dockerfile`.
- **Fix:**
  ```bash
  cd "$(pwd -P)"
  railway link --project <PROJECT_ID> --environment production
  railway up --service lovelace-app --environment production --detach
  railway up apps/public-web --path-as-root --service lovelace-public-web --environment production --detach
  ```

### 3.3 Railway Spend Limit Reached Mid-Month or Forgotten Public TCP Proxy
- **Spend Cap:** If services pause because the workspace hit a low trial/hard spend limit, raise the workspace usage cap in **Railway → Workspace → Billing / Usage** and redeploy/wake the service — persistent volumes (`pg17` and `attachments`) remain intact.
- **Security Check:** Verify in **Railway → `lovelace-pg17` → Settings → Networking** that **no TCP Proxy** is left enabled after running initial migrations — `lovelace-pg17` should only show its `.railway.internal` private hostname.

---

# Part II: The Hospitality Catastrophe Playbook (21 Real-World Disasters & Rescues)

Hospitality owners are busy welcoming tired travellers, cooking fry-ups, unblocking drains, and rescuing tents in the rain — not reading software manuals. Below are **21 realistic disasters** across every type of hospitality business, along with how to rescue each one calmly without losing data:

- **Section A (Scenarios 1–10):** Universal Mistakes That Can Happen at Any Property
- **Section B (Scenarios 11–13):** Campsite, Touring & Holiday Park Catastrophes
- **Section C (Scenarios 14–15):** Glamping, Bell Tent, Yurt & Hot-Tub Retreat Catastrophes
- **Section D (Scenarios 16–17):** B&B, Guesthouse & Small Inn Catastrophes
- **Section E (Scenarios 18–21):** Multi-Site & Portfolio Owner Catastrophes

---

## Section A: Universal Owner & Reception Disasters (Scenarios 1–10)

### Catastrophe 1: The Pence-vs-Pounds Pricing Disaster
> *"Help! I tried to set our £140/night summer tariff, typed `140` into the Seasonal Rate Rule box, and someone just booked 4 nights in August for £5.60 total!! (Or I typed `14000` in the Setup Wizard and listed our cottage at £14,000 a night!)"*

- **Why It Happened:**
  - In the **Guided Setup Wizard (`/onboarding`)**, money boxes take pounds & pence with a decimal point (e.g. `140.00`).
  - In **Inventory & Holds → Rates & Preview (`/rates`) → Add Seasonal / Weekend Rate Rule**, the field is labelled **Nightly Rate (pence)** (`14000` = `£140.00`, whereas `140` = `£1.40`!).
- **Immediate 60-Second Fix:**
  1. Go to **Inventory & Holds → Rates & Preview (`/rates`)**, find the `£1.40` (or `£14,000`) rule, and click **Archive**.
  2. Add a new rule with `14000` (`£140.00`), then test those dates in the **Tariff Preview Calculator** at the top of `/rates` to confirm the exact `£` total.
  3. If someone already booked at `£1.40/night`, open **Reservations**, click their booking reference, contact them politely, and cancel/refund or rebook under your booking terms.

---

### Catastrophe 2: The Total Owner Lockout (Lost Password + Lost Recovery Codes + Rate-Limited)
> *"I forgot my password, threw away the envelope with my 8 recovery codes during spring cleaning, AND guessed wrong 10 times so now Lovelace won't even let me try!"*

- **Why It Happened:** After `8` failed attempts for an email within `15` minutes, Lovelace rate-limits sign-in attempts. Passwords and recovery codes are one-way hashed in PostgreSQL, and restarting with `LOVELACE_BOOTSTRAP_ADMIN_PASSWORD` intentionally refuses to overwrite existing accounts.
- **Rescue Path A — If a Partner / Co-Owner Also Has an `owner_admin` Login:**
  1. Have them sign in → **Settings → Admin Users (`/users`)** → issue a temporary 12-character password for your account.
  2. Wait 15 minutes for the failed-attempt cooldown to clear, sign in, set your new password, and **download/print your new 8 recovery codes**!
- **Rescue Path B — Break-Glass Server Rescue (When You Are the ONLY `owner_admin`):**
  Have your IT/hosting helper run this safe break-glass reset (preserves 100% of bookings, guests, and settings):
  ```bash
  # 1. Clear the 15-minute failed-login cooldown:
  psql "$DATABASE_URL" -c "DELETE FROM login_attempts WHERE LOWER(email) = LOWER('you@yourdomain.com');"

  # 2. Set an Argon2id-hashed temporary password with must_rotate_password = TRUE:
  go run -C /opt/lovelace /dev/stdin <<'EOF'
  package main
  import (
      "context"
      "fmt"
      "os"
      "github.com/jackc/pgx/v5/pgxpool"
      "github.com/Prestwickuk/lovelace/internal/auth"
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
      fmt.Println("Emergency temporary password set! Sign in now to rotate and save 8 new recovery codes.")
  }
  EOF
  ```

---

### Catastrophe 3: The Accidental Whole-Property Summer Blockout
> *"I meant to close one unit for two days of repairs, left `Target Scope` on `All Units on Property` for July–September, and now our website says SOLD OUT all summer!"*

- **Immediate 30-Second Fix:** Open **Inventory & Holds → Bulk Blockouts (`/blocks`)**, scroll down to **Blockout Groups**, and click **Release** next to that blockout (or release every unit except the one being repaired). Availability returns online within 1 second.

---

### Catastrophe 4: The Check-Out Date Off-By-One Trap
> *"Mr. Evans phoned to book Friday night AND Saturday night (2 nights). I put Arrival = Friday 14th and Departure = Saturday 15th. Now Mrs. Patel booked Saturday 15th online and both families want the same space on Saturday night!"*

- **Why It Happened:** **Departure Date** always means **the morning the guest packs up and leaves** (Friday + Saturday night = Arrival `Friday 14th`, Departure `Sunday 16th`).
- **Immediate Fix:** Check **Booking Calendar (`/calendar`)** for another free pitch/room/cottage of the same or upgraded category on Saturday 15th, reassign one of the two bookings onto the free unit so Mr. Evans has `Friday 14th → Sunday 16th`, and always glance at the **`Nights`** counter (`2 nights`) right after creating a phone booking.

---

### Catastrophe 5: The "Tidy-Up" Archive Spree
> *"I didn't like how our unit codes looked, so I went into `Units & Pitches` and clicked **Archive** on all of them. Now our website says `No accommodation available` for the entire year!"*

- **Why It Happened:** **Unit Types** define the category and tariff, while **Units & Pitches** are the physical spaces guests actually stay in. Zero active physical units = zero bookable availability!
- **Immediate Fix:** Open **Inventory & Holds → Units & Pitches (`/units`)** (or **Settings → Setup Wizard → Step 2**) to add active units — or have an admin un-archive the exact original rows (`UPDATE units SET archived_at = NULL, status = 'active', version = version + 1 WHERE property_id = '<UUID>' AND archived_at IS NOT NULL;`).

---

### Catastrophe 6: The "Three Weeks, Zero Online Card Bookings" Mystery
> *"I put the booking box onto our website three weeks ago, and callers keep saying our website says `Online Card Checkout Unavailable`!"*

- **Why It Happened:** Every new property starts in **Safe Dark-by-Default Mode** (`Enable Public Online Checkout (Stripe)` is **unticked** in **Site Settings**).
- **Immediate Fix:** Once live `sk_live_...` and `whsec_...` (`https://<ADMIN_DOMAIN>/api/public/v1/webhooks/stripe`) keys are configured, go to **Settings → Site Settings (`/property`)**, tick **Enable Public Online Checkout (Stripe)**, and click **Save Booking Policy**. Also check **Payment Exceptions (`/payment-exceptions`)** if a guest ever paid right as their 15-minute hold expired.

---

### Catastrophe 7: Clicking "Anonymise Guest (GDPR)" Instead of "Cancel Booking"
> *"A guest rang to cancel next month's stay, and instead of cancelling their reservation, I went into `Guest Directory` and tried to click `Anonymise Guest`!"*

- **Built-In Safety Net:** Lovelace **blocks** anonymisation whenever a guest has any active or upcoming reservation (`PENDING`, `CONFIRMED`, or `CHECKED_IN`)! To cancel a stay, always open **Reservations (`/reservations`) → click the booking reference → Cancel Reservation**.

---

### Catastrophe 8: Merging Two Different "David Jones" Guests Together
> *"We have two guests named David Jones — one is a lovely regular, and one broke house rules last summer — and I merged them in `Duplicate Merge Review`!"*

- **Immediate Fix:** Open **Guests & Comms → Guest Directory (`/guests`)**, clarify both email addresses/phone numbers inside **Internal Staff Notes**, and in future click **Dismiss (Not Same Person)** on `/guests/merge` whenever two guests merely share a common surname.

---

### Catastrophe 9: "Backup Restore Refused — Environment Is Not Empty!"
> *"Our old server died! I booted a new Lovelace server, clicked through Step 1 of the Setup Wizard to test it, and now `/settings/backups` refuses to restore last Sunday's `.zip` backup!"*

- **Why It Happened:** Lovelace refuses to overwrite any database that already has properties or bookings (`properties > 0`) so nobody accidentally wipes a live business with an old backup! Clicking Step 1 of `/onboarding` created `1` property.
- **Immediate Fix:** Start the replacement server with `0` properties (`1` bootstrapped `owner_admin` only), go **straight** to **Settings → Backups & Cloud Sync (`/settings/backups`)** *before* touching `/onboarding`, and upload your `.zip` / `.zip.enc` archive. *(Note: Even if you lost `LOVELACE_ENCRYPTION_KEY`, 100% of bookings, guests, rooms, prices, and photos restore cleanly — you only re-type external OTA and cloud-bucket API keys once.)*

---

### Catastrophe 10: Guests Are Booking Fine, But Confirmation Emails Aren't Arriving
> *"Guests appear on our Booking Calendar, yet three people phoned today saying they haven't received a confirmation email!"*

- **Why It Happened:** Lovelace records every booking first and queues emails safely in `email_outbox` so an SMTP hiccup never loses a reservation. Usually `LOVELACE_SMTP_ADDR` / API key / domain SPF-DKIM needs attention.
- **Immediate Fix:** Check `failed emails` on **Operations Centre (`/`)**, inspect **Guests & Comms → Templates & Automation (`/communications/settings`)**, and verify `LOVELACE_SMTP_*` + verified `LOVELACE_EMAIL_FROM`.

---

## Section B: Campsite, Touring & Holiday Park Catastrophes (Scenarios 11–13)

### Catastrophe 11: The Overnight Flash-Flood / Waterlogged Lower Meadow
> *"45mm of rain fell overnight! Grass Pitches 1 to 10 by the stream are ankle-deep in mud, and 8 families in heavy campervans are already driving down the motorway for Friday check-in!"*

- **Step-by-Step Campsite Rescue:**
  1. Open **Booking Calendar (`/calendar`)** and identify which higher-ground / gravel / hardstanding pitches are `Open` for this weekend.
  2. Reassign the incoming campervans off `MP-01`..`MP-10` onto dry pitches (or contact any overflow guests before they arrive).
  3. Open **Inventory & Holds → Bulk Blockouts (`/blocks`)** → choose **`Selected Multiple Units`**, tick `MP-01`..`MP-10`, select category **`WEATHER_CLOSURE`**, set today → expected dry-out date, and click **Preview Affected Inventory & Conflicts**. Lovelace immediately flags if *any* guest booking is still sitting on one of those muddy pitches so nobody is forgotten at the gate!
  4. Once dry weather returns, click **Release** on that blockout group (or release pitches one by one as the grass firms up).

---

### Catastrophe 12: The Premature Next-Season Stampede (or Forgotten Scheduled Release!)
> **Variant A:** *"I set up our pitches in October before deciding next year's August Bank Holiday prices — and regulars just booked all 15 Electric Hook-Up pitches for August next year at winter £22/night base rates!"*  
> **Variant B:** *"It's 1st March, campers are phoning saying next summer shows Completely Unavailable, and I forgot I set a Scheduled Release hold back in autumn!"*

- **How to Prevent Variant A (Season Hold):** Use **Inventory & Holds → Scheduled Releases (`/releases`)** to hold next year's summer dates in **`UNRELEASED`** state until you have entered next year's Bank Holiday & school-holiday rate rules on `/rates` — or set your `Base Nightly Rate` to your *mid/high-season* rate rather than your cheapest winter rate!
- **How to Fix Variant B in 10 Seconds:** Open **Inventory & Holds → Scheduled Releases (`/releases`)** (or look for purple `UNRELEASED` cells on **Booking Calendar**) and click **Release Now** to open the season immediately!

---

### Catastrophe 13: The £280 Dog Fee & 8-Metre Twin-Axle Caravan on a Small Tent Pitch
> *"A couple with 2 children booked 7 nights with 1 dog — why did Lovelace quote them £280 just for the dog?! And yesterday someone booked an 8-metre caravan onto a tiny walk-in backpacker tent pitch!"*

- **Why the £280 Dog Fee Happened:**
  - When creating the **Dog Stay (£10)** add-on in **Inventory & Holds → Extras & Add-ons (`/extras`)**, the **Pricing Model** was set to `per_person_per_night` (`4` people × `7` nights × `£10` = `£280`!) instead of **`per_stay`** (`£10` flat) or **`per_night`** (`£10` × `7` = `£70`)!
  - **Fix:** Edit the extra on `/extras` (or archive it and create a new one with `per_stay` or `per_night`).
- **How to Stop Caravans Booking Tent Pitches:**
  - Separate **Tent Pitches (`tent_pitch`)** and **Touring / Campervan / EHU Pitches (`touring_pitch`)** into distinct **Unit Types (`/unit-types`)**, state max vehicle/tent length clearly in the Unit Type **Description** (e.g. *"Tents only — no vehicles on grass; max 5m × 5m"*), and set accurate `Max Adults`, `Max Guests`, and `Max Pets` caps per category.

---

## Section C: Glamping, Yurt, Shepherd's Hut & Hot-Tub Catastrophes (Scenarios 14–15)

### Catastrophe 14: Ripped Bell-Tent Canvas or Failed Wood-Fired Hot Tub on Changeover Day
> *"A storm tore the canvas on Safari Tent 3 (unsafe to sleep in) — AND on Shepherd's Hut 1, the hut itself is fine, the repairman fixed the hot tub pump and marked the job `COMPLETED`, yet Shepherd's Hut 1 is STILL blocked from next week's bookings!"*

- **Two Crucial Glamping Controls in `/maintenance`:**
  1. **Partial vs Full Closure:** When only an outdoor amenity (like a private hot tub or fire-pit) has a minor fault while the hut itself remains safe to occupy, set **Availability Impact = `PARTIAL`** rather than `UNAVAILABLE` so future bookings aren't blocked while you call tonight's guest to offer a goodwill discount/bottle of wine. Use **`UNAVAILABLE`** for torn canvas, wood-burner flue faults, or water/power loss.
  2. **Post-Repair Inspection Sign-Off & Manual Restriction Release:** Any `UNAVAILABLE` maintenance ticket stays open until you open the ticket in `/maintenance` and mark the Post-Completion Inspection as **`PASSED`** (confirming canvas tension, CO alarm, and cleanliness are guest-ready), transition the ticket to **`CLOSED`**, **and click `Release Restriction`** on the ticket (plus switch the unit's status back to `active` in `/units` if you changed it to `maintenance`). Lovelace intentionally never auto-releases maintenance blockouts without explicit operator release!

---

### Catastrophe 15: Back-to-Back 1-Night Stays Causing Hot-Tub & Linen Changeover Burnout
> *"We left `Minimum Nights = 1` on our 4 Luxury Geodomes, and now Saturday, Sunday, Monday, and Tuesday are all separate 1-night stays — draining, scrubbing, refilling, and heating 4 wood-fired hot tubs in a 4-hour window every day is impossible!"*

- **Immediate Fix:**
  1. Open **Inventory & Holds → Unit Types (`/unit-types`)** and change **Min Nights** from `1` to **`2`** (and use `Min Nights = 3` on Bank Holiday rate rules in `/rates`). Existing bookings stay untouched, while all new bookings must stay at least 2 nights!
  2. Need a 1-night breathing gap right after a big group checks out? Add a 1-night **`OPERATIONAL_BUFFER`** blockout in **Bulk Blockouts (`/blocks`)**.

---

## Section D: B&B, Guesthouse & Small Inn Catastrophes (Scenarios 16–17)

### Catastrophe 16: Missed Coeliac Breakfast Allergy or Steep-Stairs Mobility Request
> *"Mrs. Clarke has coeliac disease and cannot climb the steep Georgian stairs to our Attic Room — we didn't spot her note until she arrived with her suitcase at 5:30 PM!"*

- **How to Make Guest Notes Impossible to Miss in a B&B:**
  1. Check your morning **Operations Centre (`/`)** — look at the **`upcoming arrival preferences`** counter inside **Guest Relations & Communications** and click **Front Desk (Arrivals & Departures)** to read every incoming guest's special requests *before* doing the morning food shop!
  2. For returning B&B regulars, save permanent dietary/room needs (e.g. *"Coeliac — separate toaster; prefers Ground Floor Garden Room"*) into **Structured Preferences** on their profile in **Guest Directory (`/guests`)** so it appears automatically on every future stay!
  3. Mention stair access clearly in upper-floor **Unit Type Descriptions** (*"Second floor via historic spiral staircase — no lift"*).

---

### Catastrophe 17: Live-In B&B Hosts Forgot to Block Off Their Own Two-Week Holiday!
> *"Unlike a hotel with night porters, my wife and I ARE the B&B! We booked two weeks in Portugal in November, forgot to close the calendar, and two guests just booked rooms while the house will be locked up!"*

- **Immediate Rescue & Prevention Habit:**
  1. The moment you book personal time away (even a family wedding weekend!), open **Inventory & Holds → Bulk Blockouts (`/blocks`)**, set **Target Scope = `All Units on Property`**, category **`OWNER_USE`**, and click **Preview Affected Inventory & Conflicts**.
  2. If `Preview` shows `1` or `2` guests already booked during those dates, Lovelace highlights their names and booking references immediately so you can ring them straight away (months in advance!) to offer alternative dates or help arrange a neighbouring B&B.

---

## Section E: Multi-Site & Portfolio Owner Catastrophes (Scenarios 18–21)

Running two or more properties inside one Lovelace account (for instance, **Meadowbrook Campsite**, **Harbour View Cottage**, and **The Coaching Inn B&B**) unlocks the top-left **Property Switcher** — and introduces four unique multi-site pitfalls:

### Catastrophe 18: Editing Prices, Rules, or Blockouts on the WRONG Property!
> *"I just spent 25 minutes adding winter blockouts, changing check-in time to 4:00 PM, and editing rates — and then looked up at the top-left corner and realised I was still on `Meadowbrook Campsite` instead of `Harbour View Cottage`!!"*

- **How to Undo Every Accidental Change Using the Audit Log:**
  1. Don't guess what the old settings were! Open **Settings → Audit Log (`/audit-logs`)** while still on `Meadowbrook Campsite` to see the exact chronological list of every edit (`Timestamp`, `Actor`, `Action`, `Resource`, `IP`).
  2. Need the exact previous numbers (`before_state` vs `after_state`)? In the same signed-in browser window, open `https://<YOUR_ADMIN_DOMAIN>/api/v1/audit-logs?limit=25` — each entry includes sanitized structural **`before_state`** (your previous check-in/out times, rates, and policy numbers) and **`after_state`**!
  3. Copy the original values back into **Site Settings** / **Rates**, and click **Release** on any accidental blockout in **Bulk Blockouts (`/blocks`)**.
  4. Now use the top-left **Switch** dropdown under **ACTIVE PROPERTY** to switch to `Harbour View Cottage` and apply your changes there.
- **Best Trick to Prevent Multi-Site Mix-Ups Forever:**
  - Give each property a **distinct Primary Colour and Sidebar Header Label** in **Site Settings → Property Branding** (e.g. **Forest Green** for the Campsite, **Deep Coastal Blue** for Harbour View Cottage, **Warm Burgundy** for the B&B). Your eyes will notice instantaneously which site you are editing!

---

### Catastrophe 19: Putting Property A's Website Booking Code (or WordPress Plugin) onto Property B's Website!
> *"Guests visiting `harbourviewcottage.co.uk` are seeing Bell Tents and Grass Pitches from our campsite 12 miles inland — and booking them!"*

- **Why It Happened:** You downloaded the WordPress `.zip` plugin (or copied the HTML embed snippet) from **Site Settings → Website Booking Section** while `Meadowbrook Campsite` was active in the top-left switcher!
- **Immediate Fix:**
  - **On HTML / Squarespace / Wix:** Switch to `Harbour View Cottage` in the top-left sidebar first, open **Site Settings → Website Booking Section**, check that the badge at the top-right says **`Property: harbour-view-cottage`**, click **Copy Embed Code**, and replace the snippet on `harbourviewcottage.co.uk`.
  - **On WordPress:** You don't even need to reinstall the plugin! Open your WordPress dashboard → **Settings → Lovelace Booking**, change **Property Slug** to `harbour-view-cottage`, and click **Save Settings** then **Test Connection & Sync Brand Colours**. *(And if you have ONE group WordPress website showcasing all 3 properties on different pages, simply pass `property="..."` per block or shortcode: `[lovelace_booking property="harbour-view-cottage"]` on the Cottage page and `[lovelace_booking property="meadowbrook-campsite"]` on the Campsite page!)*

---

### Catastrophe 20: Multi-Site Staff Permissions Mix-Up (Manager Can't See Site B — or Cleaner Sees Wrong Site)
> *"I hired a manager to help across BOTH of our cottages, added them in Step 5 of Cottage A's Setup Wizard, and they say they can only see Cottage A — OR I gave a seasonal warden my own `owner_admin` password and they saw financials for all three of our businesses!"*

- **How Multi-Site Permissions Work in Lovelace:**
  - **`owner_admin` (Global Owner):** Can see and switch between **all** properties, manage users, and download system backups. **Never share your `owner_admin` password with seasonal staff!**
  - **Property-Scoped Roles (`manager`, `staff`, `housekeeping_staff`, `maintenance_staff`, `viewer`):** Locked at the database level (`property_id`) to the specific property they are assigned to — a cleaner at the Campsite cannot view guests or bookings at your B&B!
  - If one trusted manager looks after two separate properties without needing global owner powers, create separate property-scoped logins per site (e.g. `sam+cottage1@domain.com` and `sam+cottage2@domain.com`) or promote only a trusted business partner to `owner_admin`.

---

### Catastrophe 21: Mixing Up VAT-Registered and Non-VAT Properties in Accounting Exports
> *"Our Campsite is run through a VAT-registered Limited Company (`20%` VAT), while our Holiday Cottage is owned personally below the VAT threshold (`0%` VAT). My bookkeeper says I sent them the Campsite CSV instead of the Cottage CSV!"*

- **Immediate Fix & Safeguard:**
  1. Check **Settings → Site Settings (`/property`)** on *each* property separately (using the top-left property switcher) to confirm `VAT Registered` is ticked **only** on the VAT-registered entity, and verify `Tax Rate` on its Unit Types (`/unit-types`) and Extras (`/extras`).
  2. In **Inventory & Holds → Reports & BI (`/reports`)**, accounting CSV exports (`lovelace.accounting.v1`) are **strictly per-property** (they never commingle two businesses' revenue into one spreadsheet!). Always check the active property name at the top of `/reports` before clicking **Export Accounting CSV**, and name the downloaded file clearly per business entity.


---

# Part III: 15 Deep Operational, Security & Integration Troubleshooting Scenarios (Issues 22–36)

These 15 scenarios cover subtle, code-verified edge cases across **Stripe webhooks**, **SMTP / email outbox fencing**, **GDPR erasure barriers**, **Phase 4 OTA Channel Manager sync**, **Maintenance restrictions & RBAC**, **`LOVELACE_ENCRYPTION_KEY` vault boundaries**, **Argon2id `.zip.enc` backups**, **Self-Service Guest Portal**, **Phase 5 BI Reports**, and **WordPress / Website iframe embeds**.

---

### Issue 22: Ticking "Enable Stripe" or "Enable Guest Portal" in `/property` Before `.env` Setup Crash-Loops `lovelace-app` (Plus Implicit TLS Port `:465` Hanging SMTP)
- **Symptom:** Everything was running smoothly; you ticked **Enable Public Online Checkout (Stripe)** or **Enable Self-Service Guest Portal** in **Settings → Site Settings (`/property`)**, `/readyz` flipped to `503 Service Unavailable`, and the next time `lovelace-app` restarted it refused to boot! Or you set `LOVELACE_SMTP_ADDR=smtp.provider.com:465` and every email hangs for 30 seconds before failing.
- **Root Cause (`internal/service/service.go`, `internal/mailer/mailer.go`):**
  1. `ValidateProductionStartup` inspects `property_booking_policies` in PostgreSQL on boot: if **any** active public property has `stripe_enabled = true`, production **requires** both live `LOVELACE_STRIPE_SECRET_KEY` (`sk_live_` / `rk_live_`) + `LOVELACE_STRIPE_WEBHOOK_SECRET` (`whsec_`) **and** configured `LOVELACE_SMTP_ADDR` (even if `LOVELACE_REQUIRE_EMAIL=false`!). Likewise, if `LOVELACE_GUEST_PORTAL_ENABLED=true` and `guest_portal_enabled=true`, SMTP and a 32-byte `LOVELACE_ENCRYPTION_KEY` are mandatory.
  2. `SMTPSender` connects via plain TCP and upgrades opportunistically using **`STARTTLS` (port `587`)**. Implicit TLS port `465` waits silently for a client TLS ClientHello and times out.
- **Fix:**
  - **Option A (Ready to go live):** Supply live `LOVELACE_STRIPE_SECRET_KEY`, `LOVELACE_STRIPE_WEBHOOK_SECRET`, and `LOVELACE_SMTP_ADDR=smtp.<provider>:587` (`STARTTLS`, never `:465`), then start `lovelace-app`.
  - **Option B (Turn the DB toggle back off immediately so `lovelace-app` boots without keys):**
    ```sql
    UPDATE property_booking_policies
    SET stripe_enabled = FALSE, guest_portal_enabled = FALSE, version = version + 1, updated_at = NOW();
    ```

---

### Issue 23: Public Checkout or Guest Portal Fails with `403 origin_not_allowed` or `400 query_token_forbidden`
- **Symptom:** Guests clicking **Proceed to Checkout**, looking up a booking on `/lookup`, or submitting a 6-digit OTP on `/portal` receive `403 origin_not_allowed` — OR clicking a shared confirmation link returns `400 query_token_forbidden`.
- **Root Cause (`apps/public-web/src/lib/portal-bff.ts`, `internal/httpapi/public_handlers.go`):**
  1. **`403 origin_not_allowed`:** `lovelace-public-web` enforces strict `Origin` matching on every POST mutation. It fails when:
     - Visitors browse `https://www.book.yourdomain.com` while `NEXT_PUBLIC_SITE_URL` / `LOVELACE_PUBLIC_ORIGIN` only lists `https://book.yourdomain.com` (apex vs `www` mismatch);
     - `LOVELACE_TRUST_PROXY=true` and `LOVELACE_TRUSTED_PROXIES` are missing on `lovelace-public-web`, so behind Nginx/ALB/Railway the Next.js container sees internal `Host: 0.0.0.0:3000` instead of `X-Forwarded-Host: book.yourdomain.com`; or
     - A privacy browser extension or cross-site redirect strips `Origin` to `Origin: null`.
  2. **`400 query_token_forbidden`:** To prevent bearer tokens from leaking into browser history, server access logs, or `Referer` headers, Lovelace **forbids** `?token=...`, `?confirmation_token=...`, or `?code=...` in URL query strings.
- **Fix:**
  1. Redirect `www` → canonical host at your DNS/proxy layer (or list comma-separated allowed origins in `LOVELACE_PUBLIC_ORIGIN`), and set `LOVELACE_TRUST_PROXY=true` + `LOVELACE_TRUSTED_PROXIES` + `NEXT_PUBLIC_SITE_URL=https://book.yourdomain.com` on `lovelace-public-web`.
  2. Always direct guests to `/lookup` (where tokens are submitted via `POST` body / `Authorization: Bearer`) — never append `?token=` to custom email links.

---

### Issue 24: Customer Paid on Stripe, Yet No Booking Appeared on `/calendar` (Webhook Path, `EXPIRED_HOLD_PAID`, `rk_live_` Refund Scope, or `livemode_mismatch`)
- **Symptom:** Stripe Dashboard shows a succeeded payment, yet `/reservations` and `/calendar` do not show the stay.
- **Root Cause (`internal/service/public_booking_service.go` — 4 distinct cases):**
  1. **Wrong Webhook Path (`404` in Stripe Dashboard):** Webhook registered as `/api/webhooks/stripe` instead of **`https://<ADMIN_DOMAIN>/api/public/v1/webhooks/stripe`**, or pointing at `book.yourdomain.com` (`:3000`) instead of `admin.yourdomain.com` (`:8080`).
  2. **Late Payment After 15-Minute Hold Expired (`EXPIRED_HOLD_PAID`) or Concurrent Unit Claim (`OVERLAP_ON_CONFIRM`):** The guest completed 3DS authentication *after* `ExpireStaleBookingHolds` expired their 15-minute hold. Lovelace records an open row in **`/payment-exceptions`** and automatically calls Stripe's Refunds API outside the DB transaction — **unless** you used a restricted Stripe key (`rk_live_...`) without `Charges / Refunds: Write` permission, in which case the refund fails and awaits manual refund in Stripe!
  3. **`AMOUNT_MISMATCH`:** Paid amount/currency differed from `payment_attempts.amount_pence`; logged to `/payment-exceptions` without confirming the hold.
  4. **Silent `200 OK` on `livemode_mismatch`:** Sending a Stripe CLI test webhook (`livemode: false`) to a `LOVELACE_ENV=production` server returns HTTP `200` (so Stripe stops retrying) while recording `status = 'REJECTED'` in `payment_webhook_events` and `payment.webhook_livemode_mismatch` in `audit_logs`.
- **Fix:**
  1. Check **Inventory & Holds → Payment Exceptions (`/payment-exceptions`)** first — any late, overlapping, or mismatched payment is listed there with its Stripe PaymentIntent ID.
  2. In Stripe Dashboard → **Developers → Webhooks**, verify the URL is `https://<ADMIN_DOMAIN>/api/public/v1/webhooks/stripe`, resend any failed `checkout.session.completed` event (idempotently safe!), and ensure any `rk_live_` key grants **Checkout Sessions (Write)**, **PaymentIntents (Read)**, and **Refunds (Write)**.

---

### Issue 25: Emails Stuck in `DISPATCH_UNKNOWN` (No Retry Button for Staff) Blocking Guest Edits & GDPR Erasure (`409 Conflict`)
- **Symptom:** On `/communications/settings`, an email sits in red **`DISPATCH_UNKNOWN`** with no **Retry Send** button; reception `staff` see *"Manager/Owner approval required (`comms:reconcile`)"*; AND trying to update, merge, or GDPR-anonymise that guest returns **`409 Conflict`**.
- **Root Cause (`internal/service/guest_crm_service.go`):**
  - If an SMTP socket drops or worker lease (`60s` TTL) expires *after* `DATA` transmission starts, Lovelace cannot know whether the remote mail server accepted the message. Auto-retrying could send duplicate payment/confirmation emails, so Lovelace fences the row into `DISPATCH_UNKNOWN`.
  - If `AnonymizeGuest` was attempted while a message was `DISPATCHING` or `DISPATCH_UNKNOWN`, Lovelace stamped `guests.erasure_requested_at` — which intentionally freezes profile edits, merges, and new emails until every uncertain dispatch is reconciled!
- **Fix (Owner Admin or Manager):**
  1. Sign in as **`owner_admin`** or **`manager`** and open **Guests & Comms → Templates & Automation (`/communications/settings#failed-emails`)**.
  2. Check your SMTP provider's sent/activity logs (Resend / Postmark / SES) to see whether the email left their server:
     - **If delivered/accepted:** Pick `Confirm Sent (Provider Accepted)` + `PROVIDER_WEBHOOK_ACCEPTED` (or `MTA_ACCEPTED` / `CONSERVATIVE_ASSUME_SENT`).
     - **If never received by provider:** Pick `Confirm Failed` or `Cancel Without Re-Send` + `PROVIDER_NOT_SUBMITTED` (or `MTA_REJECTED` / `PROVIDER_WEBHOOK_FAILED`).
  3. **Avoid `422` PII Rejection on Evidence Fields:** Enter a machine reason code (e.g. `SMTP_POST_DATA_TIMEOUT`), an opaque log/message ID (e.g. `resend_msg_01j9abc`), and a short non-PII note (e.g. `Checked relay dashboard; message not accepted`). **Never type the guest's email address or phone number** into `reason_code`, `evidence_ref`, or `note` — `ValidateNonPIIText` rejects emails and 7+ digit phone runs so audit logs remain 100% PII-free!
  4. Once reconciled, re-run **Anonymise Guest** on `/guests/{id}` to complete erasure cleanly.

---

### Issue 26: Airbnb / Booking.com Reservations Arrive as `UNMAPPED` (Zero Calendar Block!) or `CONFLICT_OVERBOOKED`
- **Symptom:** A guest arrives saying they booked on Airbnb or Booking.com, or `/reports` shows an orange completeness banner warning of `UNMAPPED` or `CONFLICT_OVERBOOKED` external reservations!
- **Root Cause (`internal/service/channel_sync_service.go`):**
  1. **`UNMAPPED`:** Your Channel Manager (`Beds24`, `Smoobu`, `Lodgify`, `Channex`) sent a booking for an `external_listing_id` that has no active row in `channel_listing_mappings` yet. Because Lovelace does not know which cottage/pitch it belongs to, **no `inventory_claims` row can be placed** until mapped!
  2. **`CONFLICT_OVERBOOKED`:**
     - `OVERBOOKING_INVENTORY_CONFLICT` — all units of the mapped `Unit Type` were already claimed on those dates (protected by PostgreSQL's `excl_inventory_claims_no_overlap` GiST constraint so existing direct guests are never overwritten); or
     - `CURRENCY_MISMATCH` — the OTA booking arrived in e.g. `EUR` while your mapping/property currency is `GBP`.
- **Fix:**
  1. Open **Settings → Channel Manager (`/settings/integrations/channel-manager`)** and inspect the **External Reservation Exceptions** queue.
  2. For **`UNMAPPED`:** Create the missing listing mapping (`external_listing_id` → local `Unit Type`, matching currency & timezone) and trigger **Pull Reservations** so Lovelace imports and claims the dates immediately.
  3. For **`CONFLICT_OVERBOOKED`:** Check `/calendar` to see if another unit in the same or upgraded category is free, move the flexible direct booking onto the free unit (or contact the OTA guest immediately), and re-pull / resolve the conflict. *(Channex revisions intentionally remain un-acknowledged until `CONFLICT_OVERBOOKED` is cleared so nothing is silently lost.)*

---

### Issue 27: Outbound OTA Sync Returns `422 confirmation_required`, `422 capability_blocked` (Lodgify), or `SKIPPED_UNCHANGED`
- **Symptom:** Triggering a manual OTA availability/rate push returns `422` — or succeeds in 50ms saying `0` updates pushed (`SKIPPED_UNCHANGED`).
- **Root Cause (`internal/service/channel_sync_service.go`, `internal/channels/lodgify.go`):**
  1. **`422 confirmation_required`:** High-blast-radius safety rail — pushing across **more than 1 listing** or a date window **`> 14` days** requires running **Sync Preview** first and passing `"confirm_scope": true`.
  2. **`422 capability_blocked` on `lodgify`:** Lodgify's public REST API does not expose safe programmatic calendar availability writes outside Partner Connectivity integrations. Lovelace fails closed rather than pretending availability synced!
  3. **`SKIPPED_UNCHANGED`:** `channel_sync_checkpoints` stores the canonical SHA-256 digest of the last pushed ARI payload per listing/window and skips redundant API calls when neither availability nor rates changed.
- **Fix:**
  - Always preview multi-week/multi-listing pushes first and tick/pass `confirm_scope = true`.
  - For **Lodgify**, configure the mapping with `sync_availability = false` and `ownership_mode = READ_ONLY_IMPORT` (or use Channex/Beds24/Smoobu if you require two-way automated availability blocking).

---

### Issue 28: Incoming OTA Webhooks Fail with `401 webhook_unauthorized` (Clock Drift, Channex Secret Rotation, or Orphaned Webhooks)
- **Symptom:** External OTA bookings only appear on scheduled background pulls rather than instantaneously, and logs show `401 webhook_unauthorized` on `POST /api/v1/webhooks/channels/{providerCode}/{connectionID}`.
- **Root Cause (`internal/channels/signing.go`, `internal/channels/channex.go`):**
  1. **Host Clock Skew `> 5 minutes`:** Smoobu/HMAC timestamp validation (`maxWebhookTimestampSkew = 5 * time.Minute`) rejects webhooks if your Linux VM clock has drifted!
  2. **Zero-Downtime Secret Rotation Missed:** You rotated your Channex webhook secret without placing the old secret into `webhook_secret_previous` during the transition window.
  3. **Revoking Remote API Keys *Before* Clicking `Disconnect`:** If you delete your API key inside Channex/Beds24 *before* clicking **Disconnect** in Lovelace, Lovelace cannot call the provider API to delete the remote webhook subscription (`webhook_error_code` surfaces on the card while keeping `webhook_id` visible).
- **Fix:**
  1. Enable NTP (`timedatectl status` → `System clock synchronized: yes`).
  2. When rotating Channex webhook secrets, paste the outgoing secret into **Previous Webhook Secret (`webhook_secret_previous`)** and the new secret into **Webhook Secret**, save, update Channex, and clear `webhook_secret_previous` 24 hours later.
  3. Always click **Disconnect** inside Lovelace *before* revoking the API key in the provider's dashboard (or delete any orphaned webhook manually in the provider's webhook settings).

---

### Issue 29: Maintenance Ticket Marked `PASSED` & `CLOSED`, Yet Unit Still Shows `BLOCKED` Online (Plus 3-Role RBAC Split)
- **Symptom:** A plumber fixed a leaking shower, a manager recorded Inspection = **`PASSED`** and transitioned the ticket to **`CLOSED`**, yet guests STILL cannot book that cottage next week! Also, your maintenance technician says Lovelace gives `403 Forbidden` when they try to sign off their own repair or release the blockout.
- **Root Cause (`internal/service/maintenance_service.go`, `internal/auth/rbac.go`):**
  1. **Intentional Non-Auto-Release:** Completing, inspecting (`PASSED`), or closing a maintenance request **never automatically releases** linked `MAINTENANCE` blockouts (`maintenance_restriction_links` / `inventory_claims`), nor does it revert `units.status = 'maintenance'` back to `'active'`. Why? Because a room often still needs deep housekeeping changeover or paint drying before guests sleep in it!
  2. **Segregation of Duties (RBAC):**
     - `maintenance_staff` can update work orders (`maintenance:write`) and upload photos, **cannot** inspect/close (`maintenance:inspect`) or release blockouts (`blocks:write`).
     - `staff` can create/release blockouts (`blocks:write`), **cannot** record formal post-repair inspections (`maintenance:inspect`).
     - Only **`manager`** and **`owner_admin`** hold both `maintenance:inspect` and `blocks:write`.
- **Fix:**
  1. Sign in as **`manager`** or **`owner_admin`**, open the maintenance request in **`/maintenance/{id}`**, scroll to **Linked Inventory Restrictions**, and click **Release Restriction** (or release it on `/blocks`).
  2. Also check **Inventory & Holds → Units & Pitches (`/units`)** — if someone set that unit's operational status dropdown to `maintenance` or `inactive`, switch it back to **`active`**!

---

### Issue 30: *"Arrival Today on Unready Unit"* Won't Clear Even After Marking Today's Clean `READY` (Plus Unassigned Preventative Schedules)
- **Symptom:** Housekeeping marked today's changeover task **`READY`**, yet the red **"Arrival Today on Unready Unit"** alert stays at the top of Operations Centre (`/`)! Separately, monthly preventative maintenance jobs suddenly started appearing as **`Unassigned` (`REPORTED`)** with `[Schedule Assignment Warning]`.
- **Root Cause (`internal/store/postgres/operations_repo.go`, `maintenance_repo.go`):**
  1. **Worst-Open-Task Safety Rule:** `ListArrivalReadinessIssues` evaluates **all** tasks on that `unit_id` with `scheduled_date <= today`. If a task from **yesterday or last week** on the same unit was left sitting in `CLEANING_REQUIRED` or `INSPECTION_REQUIRED`, Lovelace treats the unit as unready until every open task on that unit is `READY`!
  2. **Assignee Re-Validation on Sweep:** When you deactivate a seasonal staff member (`/users`) or contractor (`/maintenance/facilities`), `AcquireDueSchedulesAndGenerateRequests` refuses to assign new preventative tickets to an inactive person, clears the stale assignee with `[Schedule Assignment Warning]`, and places the ticket into `Unassigned` so it isn't silently forgotten!
- **Fix:**
  1. Filter **Operations Centre (`/`)** (or inspect previous dates `?date=YYYY-MM-DD`) for older unfinished housekeeping tasks on that unit and advance them to **`READY`**.
  2. Open **Maintenance → Workload & Schedule (`/maintenance/schedule`)**, edit the recurring schedule, and pick the replacement active staff member or contractor.

---

### Issue 31: Moving PostgreSQL to a New Server with a Different `LOVELACE_ENCRYPTION_KEY` Breaks Recovery Codes, Guest Portal OTPs, OTAs & Cloud Backups
- **Symptom:** You migrated your PostgreSQL database (`pg_dump` / volume move) onto a new server, generated a *brand-new* `LOVELACE_ENCRYPTION_KEY` instead of copying your old one, and suddenly:
  1. Admin Recovery Codes say `Invalid recovery code` on `/login/recover`;
  2. Active Guest Portal 6-digit OTPs fail verification;
  3. Channel Manager (`Beds24`/`Smoobu`/`Lodgify`/`Channex`) fails decryption (`cipher: message authentication failed`); and
  4. Scheduled Cloud Backups fail to decrypt bucket credentials!
- **Root Cause (`internal/auth/recovery.go`, `internal/secrets/box.go`, `internal/service/portal_service.go`):**
  - All four subsystems derive keys from `LOVELACE_ENCRYPTION_KEY` (`AES-256-GCM` for `channel_connections` and `backup_targets`, plus domain-separated `HMAC-SHA256` keys for `admin_recovery_codes` and `guest_portal_challenges`). Reservations, guests, prices, units, and admin passwords (`Argon2id`) are completely unaffected!
- **Fix:**
  - **Best Fix (10 seconds):** Copy your original 64-hex-char `LOVELACE_ENCRYPTION_KEY` from your password vault into `.env` / cloud secrets and restart `lovelace-app` — all 4 subsystems work immediately!
  - **If the old `LOVELACE_ENCRYPTION_KEY` is permanently lost (3-minute re-key):**
    1. Sign in with your normal password → **My Account (`/account`)** → **Generate New Recovery Codes** (re-keys all 8 codes under the new key).
    2. Open **Settings → Channel Manager (`/settings/integrations/channel-manager`)**, re-paste your OTA API keys, and click **Save** then **Verify Live Connection**.
    3. Open **Settings → Backups & Cloud Sync (`/settings/backups`)**, re-enter your S3/R2/GCS/Azure bucket secret, and save.
    4. Any guest at `/portal` simply clicks **Send a new code** (new OTPs use the new key immediately).

---

### Issue 32: Portable Backup Hits the `48 MiB` Ceiling, Fails Passphrase Decryption, or Locks Other Staff Logins After Empty-DB Restore
- **Symptom:**
  - **A:** Exporting or uploading a `.zip` / `.zip.enc` backup returns `archive exceeds maximum portable size (48 MiB)`.
  - **B:** Verifying a `.zip.enc` fails decryption because you pasted `LOVELACE_ENCRYPTION_KEY` into the passphrase box.
  - **C:** After restoring a `.zip` backup into a fresh server, your own login works, but your manager and receptionists say their old passwords no longer work!
- **Root Cause (`internal/backup/crypto_archive.go`, `internal/store/postgres/backup_repo.go`):**
  1. Portable UI backups bundle PostgreSQL JSONL tables + `LOVELACE_ATTACHMENT_DIR` photos up to **`48 MiB` (`MaxArchiveSizeBytes`)** to keep memory bounded inside 512 MiB–1 GiB containers.
  2. `.zip.enc` (`LVBK1` header) uses **`Argon2id (t=2, m=64 MiB, p=2) + AES-256-GCM`** keyed by the **human passphrase you typed when creating/configuring that backup** — *not* `LOVELACE_ENCRYPTION_KEY`!
  3. For security, portable backups **never export `password_hash` or session tokens**. During `RestoreIntoEmptyDatabase`, your currently signed-in `owner_admin` keeps their live password/session, while all *other* imported `admin_users` receive a locked placeholder hash and `must_rotate_password = true`.
- **Fix:**
  - **If > 48 MiB of high-res maintenance photos:** Use `pg_dump -Fc` + filesystem `rsync` / cloud volume snapshots for full disaster backups (and keep uploaded maintenance images compressed to 1–2 MiB JPEGs/WebPs).
  - **Post-restore staff unlock:** Open **Settings → Admin Users (`/users`)** and issue each active colleague a fresh 12-character temporary password so they can sign in and pick their own new passphrase.

---

### Issue 33: Guest Portal Returns `404`, `409 duplicate_guest_verification_required`, `409 ota_managed_booking`, `403 step_up_required`, or `403 do_not_contact_restricted`
- **Symptom:** Guests using `/portal` encounter `404 Not Found` or specific `403`/`409` status banners.
- **Root Cause & Exact Resolution (`internal/service/portal_service.go`):**
  - **`404 Not Found`:** **Both** gates must be enabled: `LOVELACE_GUEST_PORTAL_ENABLED=true` on `lovelace-app` **AND** **Enable Self-Service Guest Portal** ticked in `/property` for that property!
  - **`409 duplicate_guest_verification_required`:** Two or more unmerged rows in `guests` share that email address (`normalized_email`), so Lovelace refuses to guess which record owns the inbox! **Fix:** Open **Guests & Comms → Duplicate Merge Review (`/guests/merge`)** and merge the duplicate guest profiles (or trigger staff-verified email confirmation from `/guests/{id}`).
  - **`409 ota_managed_booking`:** Guests can view stay details, but date-amendment and cancellation requests for externally managed OTA stays (`BOOKING_COM`, `AIRBNB`, etc.) must be made on the original booking platform so OTA commissions and refunds stay consistent.
  - **`403 step_up_required`:** Sensitive mutations (requesting cancellation or submitting a GDPR privacy request) require fresh authentication within the last **`15` minutes** (`StepUpFreshnessWindow`). Prompt the guest to complete the quick 6-digit step-up OTP shown on screen.
  - **`403 do_not_contact_restricted`:** That guest's CRM profile status is set to **`DO_NOT_CONTACT`** (or has a pending GDPR erasure `erasure_requested_at`), which blocks automated portal actions and emails. Review their status in `/guests/{id}`.

---

### Issue 34: Understanding `/reports` Completeness Warnings & Accounting CSV `NOT IMPLEMENTED` / Leading `'` Columns
- **Symptom:** `/reports` shows a yellow **Data Completeness & Integrity Notices** banner (`inventory_history_incomplete`, `ManualUnquotedBookingsCount`, `MultiCurrencyDetected`, or legacy refunds), or your accountant asks why 3 columns in the exported CSV say `NOT IMPLEMENTED` and why a note starting with `-` has a leading apostrophe `'`.
- **Root Cause (`internal/service/reporting.go`, `internal/domain/reporting.go`):**
  1. **Completeness Banners Are Truthfulness Guards (Not Errors):**
     - `inventory_history_incomplete`: Your report date range reaches back before migration `0021` started recording exact daily `unit_status_history` transitions. Narrow `start_date` to on/after `inventory_history_reliable_from` for exact historical sellable-night denominators.
     - `ManualUnquotedBookingsCount`: Telephone/manual bookings without an online quote snapshot prorate `reservations.total_pence` evenly across stay nights.
     - `unresolved_legacy_refund_payments_count`: Pre-`0021` refunded payments without a dated `refunds` row are excluded from cash-flow date buckets rather than misattributed to `paid_at`.
     - Open `UNMAPPED` / `CONFLICT_OVERBOOKED` OTA rows or open `/payment-exceptions` are flagged so you resolve them before filing month-end accounts!
  2. **`NOT IMPLEMENTED` Columns (`ota_commission_pence`, `payout_settlement_date`, `net_of_fee_settlement_pence`):** Lovelace records gross booking totals, Stripe payments, and refunds accurately, but does not ingest bank payout batch settlements or OTA commission invoices — so it explicitly outputs `NOT IMPLEMENTED` rather than outputting misleading `0`s!
  3. **Leading `'` in CSV Cells (`SanitizeCSVCell`):** Any text cell starting with `=`, `+`, `-`, or `@` is prefixed with `'` to prevent Excel/Google Sheets CSV formula injection while keeping numeric money columns (`-5000`) pure signed integers.

---

### Issue 35: Website / WordPress Booking Embed Shows *"Refused to Connect"*, Disappears for Logged-Out Visitors, Returns `404`, or Stays Stuck at `680px`
- **Symptom:** Your embedded booking widget shows a grey browser error box (*"book.yourdomain.com refused to connect"*), vanishes as soon as you log out of WordPress Admin, shows `404`, or fails to auto-resize beyond `680px`.
- **Root Cause & Fix (`apps/public-web/next.config.ts`, `lovelace-frontend.js`):**
  1. ***"Refused to connect"* (`X-Frame-Options` / `CSP frame-ancestors`):**
     - Standard storefront pages (`/`, `/stays`, `/book`, `/portal`) send `frame-ancestors 'none'` + `X-Frame-Options: DENY` to block clickjacking. **Only `/embed/<property-slug>` allows cross-origin framing (`frame-ancestors *`)!** Make sure your `<iframe>` `src` uses `https://book.yourdomain.com/embed/<slug>` — and check that Nginx/Cloudflare in front of `book.yourdomain.com` is **not** injecting a blanket `add_header X-Frame-Options DENY;` header!
  2. **Widget Visible to WordPress Admin, Invisible to Logged-Out Visitors:**
     - `class-lovelace-booking-plugin.php` intentionally hides embeds from public visitors if **Public Booking Site URL** in **WordPress → Settings → Lovelace Booking** uses `http://` instead of `https://` (only localhost loopback is allowed over `http://`). Update it to `https://book.yourdomain.com`!
  3. **`404` Inside `/embed/<slug>`:** Either the `slug` has a typo, or the property is inactive (`public_enabled = false`).
  4. **Iframe Stuck at `680px` Without Auto-Resizing:** `lovelace-frontend.js` verifies `event.origin === configuredBookingOrigin` on `lovelace:embed:resize` `postMessage` events. If `https://book.yourdomain.com` HTTP-redirects to `https://www.book.yourdomain.com`, `event.origin` won't match! Update **Public Booking Site URL** (or re-copy the HTML snippet) to match the final non-redirecting origin.

---

### Issue 36: WordPress `[lovelace_accommodation]` Cards Show Old Prices — or Clicking *"Book Direct"* Does Nothing in Modal Mode
- **Symptom:** You updated room rates or descriptions in Lovelace 5 minutes ago, `/stays` shows the new price, yet your WordPress page's `[lovelace_accommodation]` cards still show the old price! Or clicking *"Book Direct"* (`mode="modal"`) does nothing on your WordPress page.
- **Root Cause & Fix (`class-lovelace-api-client.php`, `lovelace-frontend.js`):**
  1. **15-Minute WordPress Transient + Page Cache:** `Lovelace_Booking_API_Client` caches `/api/public/catalog` in a WordPress transient (`lovelace_cat_<md5>`) for **15 minutes (`900s`)** so your WordPress site stays fast even under heavy traffic.
     - **Instant Refresh:** In WordPress Admin → **Settings → Lovelace Booking**, click **Save Changes** or **Test Connection & Sync Brand Colours** (clears the transient immediately), then purge any full-page cache plugin (**WP Rocket**, **LiteSpeed Cache**, **W3 Total Cache**, **Cloudflare APO**).
  2. **Modal Button Does Nothing (`mode="modal"` / `target="modal"`):**
     - **Parent Website CSP:** If your WordPress security plugin (Wordfence / HTTP Headers) sets a restrictive `Content-Security-Policy: frame-src 'self'` on your marketing website, the browser blocks the modal iframe from loading `https://book.yourdomain.com`. Add `https://book.yourdomain.com` to your WordPress site's `frame-src` directive — **or** switch shortcode attribute `mode="new_tab"` (`[lovelace_book_button mode="new_tab"]`), which works with zero iframes or JS!
     - **Aggressive JS Deferral / Minification:** Exclude `lovelace-frontend.js` from "Delay JS Execution" in WP Rocket / Autoptimize so modal click handlers attach immediately on page load.
