#!/bin/sh
set -eu

# Prepare a disposable, empty PostgreSQL test database at migration 0017 with
# pre-existing immutable audit history and a redaction overlay. The caller then
# runs the normal `go run ./cmd/migrate` command to apply the pending 0018.
: "${MIGRATION_DATABASE_URL:?set MIGRATION_DATABASE_URL to a disposable *_test database}"

identity=$(psql "$MIGRATION_DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 -c \
  "SELECT current_database() || '|' || current_user || '|' || r.rolsuper || '|' || (to_regclass('public.schema_migrations') IS NOT NULL) FROM pg_roles r WHERE r.rolname = current_user")
case "$identity" in
  *_test\|lovelace_migrator\|false\|false) ;;
  *) echo "refusing migration-upgrade fixture: expected empty *_test database and non-superuser lovelace_migrator" >&2; exit 1 ;;
esac

psql "$MIGRATION_DATABASE_URL" -X -v ON_ERROR_STOP=1 -c \
  'CREATE TABLE schema_migrations (filename TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())' >/dev/null

for migration in migrations/0*.sql; do
  name=${migration##*/}
  case "$name" in
    0018_*) break ;;
  esac
  psql "$MIGRATION_DATABASE_URL" -X -1 -v ON_ERROR_STOP=1 -f "$migration" >/dev/null
  checksum=$(shasum -a 256 "$migration" | awk '{print $1}')
  psql "$MIGRATION_DATABASE_URL" -X -v ON_ERROR_STOP=1 -c \
    "INSERT INTO schema_migrations (filename, checksum) VALUES ('$name', '$checksum')" >/dev/null
done

# Preserve a representative raw PII audit row and its already-existing overlay
# across the pending migration. These values exist only in the disposable DB.
psql "$MIGRATION_DATABASE_URL" -X -v ON_ERROR_STOP=1 <<'SQL'
INSERT INTO public.properties (id, slug, name)
VALUES ('10000000-0000-4000-8000-000000000001', 'upgrade-test', 'Upgrade Test Property');
INSERT INTO public.admin_users (id, property_id, email, full_name, role, password_hash)
VALUES ('20000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000001', 'owner@upgrade.test', 'Upgrade Owner', 'owner_admin', 'test-hash');
WITH audit AS (
  INSERT INTO public.audit_logs (property_id, actor_user_id, actor_email, actor_role, action, resource_type, resource_id, before_state, after_state, metadata)
  VALUES ('10000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000001', 'owner@upgrade.test', 'owner_admin', 'guest.updated', 'guest', '30000000-0000-4000-8000-000000000001', '{"guest_email":"private@example.test"}'::jsonb, '{"guest_phone":"07700900123"}'::jsonb, '{"source":"upgrade-fixture"}'::jsonb)
  RETURNING id
)
INSERT INTO public.audit_log_redactions (id, audit_log_id, property_id, redacted_before_state, redacted_after_state, redacted_metadata, reason, reason_category, redacted_by)
SELECT '40000000-0000-4000-8000-000000000001', audit.id, '10000000-0000-4000-8000-000000000001',
       '{"guest_id":"30000000-0000-4000-8000-000000000001","status":"ANONYMIZED","version":2}'::jsonb,
       '{"guest_id":"30000000-0000-4000-8000-000000000001","status":"ANONYMIZED","version":3}'::jsonb,
       '{"redacted":true}'::jsonb, 'historical guest privacy cleanup', 'HISTORICAL_PII_MASK', '20000000-0000-4000-8000-000000000001'
FROM audit;
SQL

