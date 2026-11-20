#!/usr/bin/env bash
set -euo pipefail

# Provision an isolated non-superuser PostgreSQL test database and role for
# LOVELACE Booking System automated integration tests.
# Never grants access to the development/production `lovelace` database.

PG_CONTAINER="${PG_CONTAINER:-lovelace-postgres}"
PG_SUPERUSER="${PG_SUPERUSER:-postgres}"
TEST_DB="${TEST_DB:-lovelace_test}"
TEST_USER="${TEST_USER:-lovelace_test_runner}"
TEST_PASS="${TEST_PASS:-lovelace_test_runner_pw}"

if [[ ! "$TEST_DB" =~ ^[A-Za-z_][A-Za-z0-9_-]*(_test|-test|_test_[A-Za-z0-9_-]*)$ ]]; then
  echo "Refusing unsafe TEST_DB name '$TEST_DB' (must end with _test or -test)." >&2
  exit 1
fi
if [[ ! "$TEST_USER" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
  echo "Refusing invalid PostgreSQL identifier TEST_USER '$TEST_USER'." >&2
  exit 1
fi

docker exec -i \
  -e LOVELACE_SETUP_TEST_DB="$TEST_DB" \
  -e LOVELACE_SETUP_TEST_USER="$TEST_USER" \
  -e LOVELACE_SETUP_TEST_PASS="$TEST_PASS" \
  "$PG_CONTAINER" psql -X -q -v ON_ERROR_STOP=1 -U "$PG_SUPERUSER" -d postgres <<'SQL'
\getenv test_db LOVELACE_SETUP_TEST_DB
\getenv test_user LOVELACE_SETUP_TEST_USER
\getenv test_pass LOVELACE_SETUP_TEST_PASS

SELECT
    set_config('lovelace.test_user', :'test_user', false) AS _s1,
    set_config('lovelace.test_pass', :'test_pass', false) AS _s2,
    format('DROP DATABASE IF EXISTS %I', :'test_db') AS drop_db_sql,
    format('CREATE DATABASE %I OWNER %I', :'test_db', :'test_user') AS create_db_sql,
    format('REVOKE ALL ON DATABASE %I FROM PUBLIC', :'test_db') AS revoke_db_sql,
    format('GRANT ALL PRIVILEGES ON DATABASE %I TO %I', :'test_db', :'test_user') AS grant_db_sql \gset

\unset test_pass
\unset _s2

DO $$
DECLARE
    v_user TEXT := current_setting('lovelace.test_user');
    v_pass TEXT := current_setting('lovelace.test_pass');
BEGIN
    PERFORM set_config('lovelace.test_pass', '', false);
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = v_user) THEN
        EXECUTE format('CREATE ROLE %I WITH LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE', v_user, v_pass);
    ELSE
        EXECUTE format('ALTER ROLE %I WITH LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE', v_user, v_pass);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app') THEN
        CREATE ROLE lovelace_app NOSUPERUSER NOCREATEDB NOCREATEROLE;
    END IF;
END $$;

:drop_db_sql;
:create_db_sql;
:revoke_db_sql;
:grant_db_sql;
SQL

docker exec -i \
  -e LOVELACE_SETUP_TEST_USER="$TEST_USER" \
  "$PG_CONTAINER" psql -X -q -v ON_ERROR_STOP=1 -U "$PG_SUPERUSER" -d "$TEST_DB" <<'SQL'
\getenv test_user LOVELACE_SETUP_TEST_USER
SELECT set_config('lovelace.test_user', :'test_user', false) AS _s1 \gset

CREATE EXTENSION IF NOT EXISTS btree_gist;

DO $$
DECLARE
    v_user TEXT := current_setting('lovelace.test_user');
BEGIN
    EXECUTE format('ALTER SCHEMA public OWNER TO %I', v_user);
    EXECUTE format('GRANT ALL ON SCHEMA public TO %I', v_user);
END $$;
SQL

echo "Provisioned isolated non-superuser test DB '$TEST_DB' owned by '$TEST_USER'."
echo "Export: TEST_DATABASE_URL=postgres://$TEST_USER:<TEST_PASS>@127.0.0.1:5432/$TEST_DB?sslmode=disable"
