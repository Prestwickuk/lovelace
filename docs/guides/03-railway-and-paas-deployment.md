# Guide 3: Deploying Lovelace on Railway (and Similar Container PaaS Platforms)

This guide walks through deploying a production-grade Lovelace instance on **[Railway](https://railway.com)** (with brief adaptation notes at the end for **Fly.io** and **Render**).

Railway is an ideal low-ops platform for Lovelace because it supports multi-Dockerfile monorepos, persistent SSD volumes, private internal service networking (`.railway.internal`), automatic Let's Encrypt HTTPS domains, and hard monthly spend caps.

---

## 1. Railway Project Topology

Create a single Railway Project (e.g. `lovelace-production`) containing **three services**:

| Service Name | Source / Image | Persistent Volume | Public Domain? |
| :--- | :--- | :--- | :--- |
| `lovelace-pg17` | Docker image `postgres:17-alpine` | Mounted at `/var/lib/postgresql/data` | **No** (Private `.railway.internal` only) |
| `lovelace-app` | Repo root `Dockerfile` | Mounted at `/var/lib/lovelace/attachments` | **Yes** (`admin.yourdomain.com` or `*.up.railway.app`, port `8080`) |
| `lovelace-public-web` | `apps/public-web/Dockerfile` (`--path-as-root`) | None (stateless Next.js BFF) | **Yes** (`book.yourdomain.com` or `*.up.railway.app`, port `3000`) |

---

## 2. Step-by-Step Railway Provisioning

### Step 1 — Prerequisites & Spend Guardrails
1. Install the Railway CLI (`brew install railway` or `npm i -g @railway/cli`) and authenticate (`railway login`).
2. In **Railway Dashboard → Workspace → Usage / Billing**, configure a **Usage Limit** (e.g. `$25`–`$75` soft alert and `$100` hard cap) so costs stay predictable.
3. Generate your unique production secrets locally (keep `TIRBACH_ENCRYPTION_KEY` saved safely in your password manager!):
   ```bash
   POSTGRES_BOOTSTRAP_PASSWORD="$(openssl rand -hex 24)"
   LOVELACE_MIGRATOR_PASSWORD="$(openssl rand -hex 24)"
   LOVELACE_APP_PASSWORD="$(openssl rand -hex 24)"
   TIRBACH_ENCRYPTION_KEY="$(openssl rand -hex 32)"
   TIRBACH_PUBLIC_API_SECRET="$(openssl rand -hex 32)"
   TIRBACH_BOOTSTRAP_ADMIN_PASSWORD="$(openssl rand -base64 18)"
   ```

---

### Step 2 — Provision `lovelace-pg17` & Initialize Least-Privilege Roles

1. Add a **Database → PostgreSQL 17** service (or deploy `postgres:17-alpine` with a Volume mounted at `/var/lib/postgresql/data`, `POSTGRES_DB=lovelace`, `POSTGRES_USER=postgres`, `POSTGRES_PASSWORD=<POSTGRES_BOOTSTRAP_PASSWORD>`).
2. Enable a **temporary TCP Proxy** on `lovelace-pg17` (under **Service → Settings → Networking → TCP Proxy → port `5432`**) solely for initial schema & role bootstrap from your workstation:
   ```bash
   export PGHOST="<RAILWAY_TCP_PROXY_HOST>"
   export PGPORT="<RAILWAY_TCP_PROXY_PORT>"
   export POSTGRES_DB="lovelace"
   export POSTGRES_BOOTSTRAP_USER="postgres"
   export PGPASSWORD="$POSTGRES_BOOTSTRAP_PASSWORD"
   export LOVELACE_MIGRATOR_PASSWORD="$LOVELACE_MIGRATOR_PASSWORD"
   export LOVELACE_APP_PASSWORD="$LOVELACE_APP_PASSWORD"

   # 1. Create non-superuser roles (lovelace_migrator + lovelace_app) & btree_gist:
   ./scripts/postgres/ensure-roles.sh

   # 2. Apply all SQL migrations as lovelace_migrator:
   MIGRATION_DATABASE_URL="postgres://lovelace_migrator:${LOVELACE_MIGRATOR_PASSWORD}@${PGHOST}:${PGPORT}/lovelace?sslmode=disable" \
     go run ./cmd/migrate

   # 3. Finalize strict DML-only grants for lovelace_app:
   ./scripts/postgres/ensure-roles.sh
   ```
3. **Remove the temporary TCP Proxy** immediately (`Settings → Networking → Delete TCP Proxy`). From this point onward, PostgreSQL is reachable **only** inside Railway's private network (`lovelace-pg17.railway.internal:5432`)!

---

### Step 3 — Provision `lovelace-app` & Attachment Volume Permissions

1. Create empty service **`lovelace-app`** in your Railway project and attach a **Volume** mounted at `/var/lib/lovelace/attachments`.
2. **Important Volume Permission Detail:** Railway mounts brand-new volumes owned by `root:root` (`0:0`), whereas Lovelace's `Dockerfile` runs as non-root `USER tirbach` (`uid=100, gid=101` on `alpine:3.21`).
   - Either set Railway variable **`RAILWAY_RUN_UID=0`** *only* if running a one-time init command `chown -R 100:101 /var/lib/lovelace/attachments`, **or** run a 5-second one-shot Alpine init deploy on `lovelace-app` before deploying Lovelace so `/var/lib/lovelace/attachments` is owned by `100:101` (`tirbach:tirbach`).
3. Generate a Railway public domain (or attach custom domain `admin.yourdomain.com`) targeting port **`8080`**.
4. Configure **Variables** on `lovelace-app` (never pass `POSTGRES_BOOTSTRAP_PASSWORD` or `LOVELACE_MIGRATOR_PASSWORD` to `lovelace-app`):

   ```dotenv
   PORT=8080
   TIRBACH_ENV=production
   TIRBACH_LISTEN_ADDR=:8080
   TIRBACH_COOKIE_SECURE=true
   TIRBACH_TRUST_PROXY=true
   # Include Railway edge & IPv6 internal mesh CIDRs:
   TIRBACH_TRUSTED_PROXIES=127.0.0.1/32,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,100.64.0.0/10,fd12::/16
   DATABASE_URL=postgres://lovelace_app:<LOVELACE_APP_PASSWORD>@lovelace-pg17.railway.internal:5432/lovelace?sslmode=disable
   TIRBACH_ENCRYPTION_KEY=<64_CHAR_HEX_KEY>
   TIRBACH_AUTO_SEED=false
   TIRBACH_BOOTSTRAP_ADMIN_EMAIL=you@yourdomain.com
   TIRBACH_BOOTSTRAP_ADMIN_NAME=Owner Administrator
   TIRBACH_BOOTSTRAP_ADMIN_PASSWORD=<INITIAL_ADMIN_PASSWORD>
   TIRBACH_PUBLIC_ORIGIN=https://<YOUR_PUBLIC_WEB_DOMAIN>
   TIRBACH_PUBLIC_API_SECRET=<64_CHAR_PUBLIC_API_SECRET>
   TIRBACH_GUEST_PORTAL_ENABLED=false
   TIRBACH_STRIPE_SANDBOX=false
   TIRBACH_ATTACHMENT_DIR=/var/lib/lovelace/attachments
   TIRBACH_REQUIRE_EMAIL=true
   TIRBACH_EMAIL_FROM=bookings@yourdomain.com
   TIRBACH_SMTP_ADDR=smtp.resend.com:587
   TIRBACH_SMTP_USERNAME=resend
   TIRBACH_SMTP_PASSWORD=<RESEND_OR_SMTP_API_KEY>
   ```
   *(Note: Notice `TIRBACH_LISTEN_ADDR=:8080` — omitting `0.0.0.0` lets Go listen dual-stack on both IPv4 and Railway's IPv6 `.railway.internal` private network!)*

---

### Step 4 — Provision `lovelace-public-web`

1. Create empty service **`lovelace-public-web`** and generate a public domain (or attach `book.yourdomain.com`) targeting port **`3000`**.
2. Configure **Variables** on `lovelace-public-web`:
   ```dotenv
   PORT=3000
   # Essential on Railway: bind dual-stack IPv6/IPv4
   HOSTNAME=::
   LOVELACE_ENV=production
   LOVELACE_API_BASE_URL=http://lovelace-app.railway.internal:8080
   LOVELACE_PROPERTY_SLUG=my-property
   LOVELACE_PUBLIC_API_SECRET=<SAME_64_CHAR_PUBLIC_API_SECRET>
   LOVELACE_TRUST_PROXY=true
   LOVELACE_TRUSTED_PROXIES=127.0.0.1/32,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,100.64.0.0/10,fd12::/16
   LOVELACE_STRIPE_SANDBOX=false
   NEXT_PUBLIC_SITE_URL=https://<YOUR_PUBLIC_WEB_DOMAIN>
   ```
   *(Keep replica count at `1` for both `lovelace-app` and `lovelace-public-web`.)*

---

### Step 5 — Deploy Both Services via Railway CLI (or GitHub Autodeploy)

From a clean checkout of `main`:

```bash
railway link --project <YOUR_PROJECT_ID> --environment production

# 1. Deploy Go backend & Admin Console from repo root:
railway up --service lovelace-app --environment production --detach

# 2. Deploy Next.js Public Booking BFF from apps/public-web:
railway up apps/public-web --path-as-root --service lovelace-public-web --environment production --detach
```

*(Alternatively, connect both services to your GitHub repository in **Service → Settings → Source**, setting **Root Directory** to `/` on `lovelace-app` and `/apps/public-web` on `lovelace-public-web`.)*

### Step 6 — Verify Live Readiness
```bash
curl -fsS https://<YOUR_ADMIN_DOMAIN>/readyz
curl -fsS https://<YOUR_PUBLIC_WEB_DOMAIN>/api/healthz
```

---

## 3. Adapting for Other PaaS Platforms (Fly.io & Render)

- **Fly.io:**
  - Create two Fly apps (`lovelace-app` and `lovelace-public-web`) in the same region/org so `.internal` WireGuard DNS (`http://lovelace-app.internal:8080`) works privately over IPv6 (`TIRBACH_LISTEN_ADDR=:8080`, `HOSTNAME=::`).
  - Set `auto_stop_machines = "off"` and `min_machines_running = 1` in `fly.toml` so background hold-expiry, email outbox, and scheduled cloud backup workers stay running continuously.
  - Attach a `fly volumes create lovelace_attachments` volume mounted at `/var/lib/lovelace/attachments`.
- **Render:**
  - Provision a **Render PostgreSQL 17** instance + two **Web Services** (`Docker` runtime: root `Dockerfile` for `lovelace-app` with a Render Persistent Disk at `/var/lib/lovelace/attachments`, and root directory `apps/public-web` for `lovelace-public-web`).
  - Connect `LOVELACE_API_BASE_URL` to `lovelace-app`'s private Render `hostport` (`http://lovelace-app:8080`).

---

## Next Step
Proceed to **[Guide 4: First-Boot Setup, Website Embeds & Maintenance](04-first-login-configuration-and-maintenance.md)** to sign in for the first time, save your recovery codes, launch the 7-Step Setup Wizard, and configure automated cloud backups.
