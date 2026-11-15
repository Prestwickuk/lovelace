#!/bin/sh
set -eu

# Runs during initial PostgreSQL cluster initialization (/docker-entrypoint-initdb.d)
# using the container bootstrap superuser over the local socket.
export PGHOST=""
export POSTGRES_BOOTSTRAP_USER="${POSTGRES_USER:-postgres}"

exec sh /usr/local/share/lovelace/ensure-roles.sh
