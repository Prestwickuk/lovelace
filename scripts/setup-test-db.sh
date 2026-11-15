#!/usr/bin/env bash
set -euo pipefail

# Provision an isolated non-superuser PostgreSQL test database and role for
# LOVELACE Booking System automated integration tests.
# Never grants access to the development/production `tir_bach` database.

PG_CONTAINER="${PG_CONTAINER:-tir-bach-postgres}"
PG_SUPERUSER="${PG_SUPERUSER:-tir_bach}"
TEST_DB="${TEST_DB:-tir_bach_test}"
TEST_USER="${TEST_USER:-tir_bach_test_runner}"
TEST_PASS="${TEST_PASS:-tir_bach_test_runner_pw}"

docker exec -i "$PG_CONTAINER" psql -v ON_ERROR_STOP=1 -U "$PG_SUPERUSER" -d postgres <<SQL
DO \$\$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '$TEST_USER') THEN
        CREATE ROLE $TEST_USER WITH LOGIN PASSWORD '$TEST_PASS' NOSUPERUSER NOCREATEDB NOCREATEROLE;
    ELSE
        ALTER ROLE $TEST_USER WITH LOGIN PASSWORD '$TEST_PASS' NOSUPERUSER NOCREATEDB NOCREATEROLE;
    END IF;
END \$\$;

DROP DATABASE IF EXISTS $TEST_DB;
CREATE DATABASE $TEST_DB OWNER $TEST_USER;
REVOKE ALL ON DATABASE $TEST_DB FROM PUBLIC;
GRANT ALL PRIVILEGES ON DATABASE $TEST_DB TO $TEST_USER;
SQL

docker exec -i "$PG_CONTAINER" psql -v ON_ERROR_STOP=1 -U "$PG_SUPERUSER" -d "$TEST_DB" <<SQL
CREATE EXTENSION IF NOT EXISTS btree_gist;
ALTER SCHEMA public OWNER TO $TEST_USER;
GRANT ALL ON SCHEMA public TO $TEST_USER;
SQL

echo "Provisioned isolated non-superuser test DB '$TEST_DB' owned by '$TEST_USER'."
echo "Export: TEST_DATABASE_URL=postgres://$TEST_USER:$TEST_PASS@127.0.0.1:5432/$TEST_DB?sslmode=disable"
