# Guide 1: Local Evaluation & Self-Hosted Linux Server Deployment

This guide covers running Lovelace **locally on your laptop/workstation** for development or evaluation, and deploying a **hardened production instance on your own Linux server** (VPS, dedicated server, or on-premise hardware) using Docker Compose or native `systemd` services.

---

## Part A: Quick Local Evaluation (2 Minutes)

Use `docker-compose.dev.yml` when testing Lovelace locally on macOS, Linux, or Windows (WSL2). It automatically provisions PostgreSQL 17, runs all migrations, seeds a sample campsite (**Tir Bach Campsite**) and demo Owner Admin account, and enables local Stripe Sandbox checkout.

### 1. Prerequisites
- **Docker Engine 24+** & **Docker Compose v2** (`docker compose version`)
- **Git**
- At least **2 GB** free RAM and **2 GB** free disk space

### 2. Clone & Start the Local Stack
```bash
git clone https://github.com/Prestwickuk/lovelace.git
cd lovelace

# Important: docker-compose.dev.yml is standalone — never combine it with docker-compose.yml
docker compose -f docker-compose.dev.yml up -d --build
```

### 3. Open the Interfaces
- **Lovelace Admin Console:** `http://127.0.0.1:8080/login`
  - **Default Local Dev Login:** `owner@tirbach.wales`
  - **Default Local Dev Password:** `tirbach-dev-admin-2026`
  - **Guided Setup Wizard (add a new campsite/cottage/hotel):** `http://127.0.0.1:8080/onboarding?new=1`
- **Public Guest Booking Site:** `http://127.0.0.1:3000`
- **Embeddable Website Widget Preview:** `http://127.0.0.1:3000/embed/tir-bach`

### 4. Stopping or Resetting Local Dev
```bash
# Stop containers while keeping local database & uploaded files:
docker compose -f docker-compose.dev.yml down

# Wipe local dev database & start completely fresh:
docker compose -f docker-compose.dev.yml down -v
```

> **Warning:** Never run `docker compose down -v` on a production server — `-v` permanently deletes Docker volumes!

---

## Part B: Production Self-Hosted Linux Server (Docker Compose + TLS)

This is the recommended setup for running Lovelace on a dedicated Ubuntu 24.04 LTS or Debian 12 server (e.g. Hetzner, DigitalOcean, Linode, OVH, Vultr, or on-premise hardware).

### 1. Server & DNS Preparation

1. **Recommended Server Specs:**
   - **Small / Single Property:** 2 vCPU, 4 GB RAM, 40 GB NVMe SSD
   - **Multi-Property / High Traffic:** 4 vCPU, 8 GB RAM, 80+ GB NVMe SSD
2. **DNS Records:** Point two `A` (and optional `AAAA`) records at your server's public IP:
   - `admin.yourdomain.com` → `<SERVER_PUBLIC_IP>` (Lovelace Admin Console & Stripe webhooks)
   - `book.yourdomain.com` → `<SERVER_PUBLIC_IP>` (Public Guest Booking Site & `/embed/[slug]` widgets)
3. **Firewall (`ufw`):** Expose **only** SSH (`22`), HTTP (`80`), and HTTPS (`443`). Both `docker-compose.yml` ports (`127.0.0.1:8080` and `127.0.0.1:3000`) bind strictly to loopback so containers can never bypass your TLS proxy:
   ```bash
   sudo apt update && sudo apt install -y ca-certificates curl git ufw openssl
   sudo ufw default deny incoming
   sudo ufw default allow outgoing
   sudo ufw allow OpenSSH
   sudo ufw allow 80/tcp
   sudo ufw allow 443/tcp
   sudo ufw enable
   ```
4. **Install Docker Engine & Compose Plugin:**
   Follow the [official Docker apt repository install instructions](https://docs.docker.com/engine/install/ubuntu/) and verify `sudo docker compose version`.

---

### 2. Clone Repository & Generate Production `.env`

Create `/opt/lovelace` and generate cryptographically strong, unique credentials for each database role and application secret:

```bash
sudo mkdir -p /opt/lovelace
sudo chown "$USER:$USER" /opt/lovelace
git clone https://github.com/Prestwickuk/lovelace.git /opt/lovelace
cd /opt/lovelace
cp .env.example .env
chmod 600 .env
```

Generate your secrets right on the server:
```bash
echo "POSTGRES_BOOTSTRAP_PASSWORD=$(openssl rand -hex 24)"
echo "LOVELACE_MIGRATOR_PASSWORD=$(openssl rand -hex 24)"
echo "LOVELACE_APP_PASSWORD=$(openssl rand -hex 24)"
echo "TIRBACH_ENCRYPTION_KEY=$(openssl rand -hex 32)"
echo "TIRBACH_PUBLIC_API_SECRET=$(openssl rand -hex 32)"
echo "TIRBACH_BOOTSTRAP_ADMIN_PASSWORD=$(openssl rand -base64 18)"
```

Edit `/opt/lovelace/.env` and populate the generated values:

```dotenv
TIRBACH_ENV=production
TIRBACH_COOKIE_SECURE=true
TIRBACH_TRUST_PROXY=true
TIRBACH_TRUSTED_PROXIES=127.0.0.1/32,::1/128,172.16.0.0/12,10.0.0.0/8
LOVELACE_TRUST_PROXY=true
LOVELACE_TRUSTED_PROXIES=127.0.0.1/32,::1/128,172.16.0.0/12,10.0.0.0/8

# Loopback-only host bindings (Caddy/Nginx proxies TLS 443 -> 127.0.0.1:8080 & :3000)
TIRBACH_BIND_ADDR=127.0.0.1
TIRBACH_PORT=8080
PUBLIC_WEB_BIND_ADDR=127.0.0.1
PUBLIC_WEB_PORT=3000

# PostgreSQL 17 3-role privilege separation
POSTGRES_DB=lovelace
POSTGRES_BOOTSTRAP_USER=postgres
POSTGRES_BOOTSTRAP_PASSWORD=<PASTE_GENERATED_BOOTSTRAP_PASSWORD>
LOVELACE_MIGRATOR_USER=lovelace_migrator
LOVELACE_MIGRATOR_PASSWORD=<PASTE_GENERATED_MIGRATOR_PASSWORD>
LOVELACE_APP_USER=lovelace_app
LOVELACE_APP_PASSWORD=<PASTE_GENERATED_APP_PASSWORD>

# Master 32-byte encryption key (back up offline in your password manager!)
TIRBACH_ENCRYPTION_KEY=<PASTE_64_CHAR_HEX_KEY>

# First-boot Owner Admin (used ONLY when admin_users table is empty; forces password change + recovery codes on first login)
TIRBACH_AUTO_SEED=false
TIRBACH_BOOTSTRAP_ADMIN_EMAIL=you@yourdomain.com
TIRBACH_BOOTSTRAP_ADMIN_NAME="Property Owner"
TIRBACH_BOOTSTRAP_ADMIN_PASSWORD=<PASTE_GENERATED_INITIAL_ADMIN_PASSWORD>

# Public Booking Frontend isolation
TIRBACH_PUBLIC_ORIGIN=https://book.yourdomain.com
LOVELACE_PROPERTY_SLUG=my-property
TIRBACH_PUBLIC_API_SECRET=<PASTE_64_CHAR_PUBLIC_API_SECRET>

# Safe dark-by-default flags (enable Stripe later once your property is configured)
TIRBACH_GUEST_PORTAL_ENABLED=false
TIRBACH_STRIPE_SANDBOX=false
LOVELACE_STRIPE_SANDBOX=false
TIRBACH_STRIPE_SECRET_KEY=
TIRBACH_STRIPE_WEBHOOK_SECRET=

# Transactional SMTP (set TIRBACH_REQUIRE_EMAIL=false temporarily if configuring SMTP later)
TIRBACH_REQUIRE_EMAIL=true
TIRBACH_EMAIL_FROM=bookings@yourdomain.com
TIRBACH_SMTP_ADDR=smtp.resend.com:587
TIRBACH_SMTP_USERNAME=resend
TIRBACH_SMTP_PASSWORD=<YOUR_SMTP_PASSWORD_OR_API_KEY>
```

> **CRITICAL:** Save `TIRBACH_ENCRYPTION_KEY` in a secure offline password vault (1Password, Bitwarden, etc.) immediately. It encrypts OTA channel tokens and cloud backup credentials at rest in PostgreSQL.

---

### 3. Launch the Production Compose Stack

```bash
cd /opt/lovelace
docker compose up -d --build
```

Verify startup sequence (`postgres` → `db-bootstrap` → `migrate` → `app` → `public-web`):
```bash
docker compose ps
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:3000/api/healthz
```

Both health endpoints should return HTTP `200 OK`.

---

### 4. Configure Reverse Proxy & Automatic HTTPS

Choose **Option 1 (Caddy — simplest, automatic Let's Encrypt TLS)** or **Option 2 (Nginx + Certbot)**.

#### Option 1: Caddy (Recommended)
Install Caddy (`sudo apt install -y caddy`) and write `/etc/caddy/Caddyfile`:

```caddyfile
admin.yourdomain.com {
    encode zstd gzip
    request_body {
        max_size 52MB
    }
    reverse_proxy 127.0.0.1:8080
}

book.yourdomain.com {
    encode zstd gzip
    reverse_proxy 127.0.0.1:3000
}
```

Reload Caddy:
```bash
sudo systemctl enable --now caddy
sudo systemctl reload caddy
```

#### Option 2: Nginx + Let's Encrypt Certbot
Create `/etc/nginx/sites-available/lovelace.conf` (and obtain TLS certs with `sudo certbot --nginx -d admin.yourdomain.com -d book.yourdomain.com`):

```nginx
server {
    listen 443 ssl http2;
    server_name admin.yourdomain.com;

    # Allow up to 52 MiB for backup archive verification/restore & logo/photo uploads
    client_max_body_size 52m;

    ssl_certificate     /etc/letsencrypt/live/admin.yourdomain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/admin.yourdomain.com/privkey.pem;

    location / {
        proxy_pass         http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto https;
        proxy_read_timeout 60s;
    }
}

server {
    listen 443 ssl http2;
    server_name book.yourdomain.com;

    client_max_body_size 2m;

    ssl_certificate     /etc/letsencrypt/live/admin.yourdomain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/admin.yourdomain.com/privkey.pem;

    location / {
        proxy_pass         http://127.0.0.1:3000;
        proxy_http_version 1.1;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto https;
        proxy_read_timeout 60s;
    }
}
```

> **Note on Stripe Webhooks:** Once you enable Stripe online payments, register `https://admin.yourdomain.com/api/webhooks/stripe` as your Stripe webhook endpoint URL (events: `checkout.session.completed`, `checkout.session.expired`, `charge.refunded`). Never apply body-rewriting middleware on your reverse proxy so Stripe's raw HMAC signature remains intact.

---

## Part C: Native Bare-Metal Installation (`systemd`, Without Docker)

If you prefer running native binaries directly on Ubuntu/Debian without Docker:

1. **Install PostgreSQL 17 (`postgresql-17`, `postgresql-contrib-17`), Go 1.24+, and Node.js 22 LTS.**
2. **Initialize Database & Least-Privilege Roles:**
   ```bash
   sudo -u postgres psql -c "CREATE DATABASE lovelace;"
   sudo -u postgres env \
     POSTGRES_DB=lovelace \
     POSTGRES_BOOTSTRAP_USER=postgres \
     LOVELACE_MIGRATOR_PASSWORD="<MIGRATOR_PW>" \
     LOVELACE_APP_PASSWORD="<APP_PW>" \
     /opt/lovelace/scripts/postgres/ensure-roles.sh
   ```
3. **Compile Binaries & Run Migrations:**
   ```bash
   cd /opt/lovelace
   CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /usr/local/bin/tirbach-server ./cmd/server
   CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /usr/local/bin/tirbach-migrate ./cmd/migrate

   MIGRATION_DATABASE_URL="postgres://lovelace_migrator:<MIGRATOR_PW>@127.0.0.1:5432/lovelace?sslmode=disable" \
     /usr/local/bin/tirbach-migrate

   # Re-run ensure-roles.sh once after initial migration to grant DML on all newly created tables
   sudo -u postgres env \
     POSTGRES_DB=lovelace \
     POSTGRES_BOOTSTRAP_USER=postgres \
     LOVELACE_MIGRATOR_PASSWORD="<MIGRATOR_PW>" \
     LOVELACE_APP_PASSWORD="<APP_PW>" \
     /opt/lovelace/scripts/postgres/ensure-roles.sh
   ```
4. **Build `public-web` Standalone Bundle:**
   ```bash
   cd /opt/lovelace/apps/public-web
   npm ci --no-audit --no-fund
   npm run build
   cp -R public .next/standalone/public
   cp -R .next/static .next/standalone/.next/static
   ```
5. **Create Unprivileged System User & Attachment Directory:**
   ```bash
   sudo useradd --system --no-create-home --shell /usr/sbin/nologin lovelace
   sudo mkdir -p /var/lib/lovelace/attachments
   sudo chown -R lovelace:lovelace /var/lib/lovelace
   sudo chmod 750 /var/lib/lovelace/attachments
   ```
6. **Create `systemd` Units (`/etc/systemd/system/lovelace-app.service` & `lovelace-public-web.service`):**
   - Run `/usr/local/bin/tirbach-server` as `User=lovelace` with `EnvironmentFile=/etc/lovelace/app.env` (`TIRBACH_LISTEN_ADDR=127.0.0.1:8080`).
   - Run `/usr/bin/node /opt/lovelace/apps/public-web/.next/standalone/server.js` as `User=lovelace` with `EnvironmentFile=/etc/lovelace/web.env` (`HOSTNAME=127.0.0.1`, `PORT=3000`).
   - Place behind Caddy or Nginx identically to Section 4 above.

---

## Next Step
Proceed to **[Guide 4: First-Boot Setup, Website Embeds & Maintenance](04-first-login-configuration-and-maintenance.md)** to sign in, save your 8 recovery codes, run the 7-Step Property Setup Wizard, and configure off-site backups.
