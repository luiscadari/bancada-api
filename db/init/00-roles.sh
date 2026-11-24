#!/bin/sh
# Runs once, as the superuser, when the PostgreSQL data directory is created.
# Creates the three roles used by Bancada:
#   bancada_owner  - owns the schema and runs migrations
#   bancada_app    - used by the API; subject to row-level security
#   bancada_system - BYPASSRLS; used only for pre-tenant lookups (login, refresh, invitations)
set -eu

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -v owner_pw="$BANCADA_OWNER_PASSWORD" \
  -v app_pw="$BANCADA_APP_PASSWORD" \
  -v system_pw="$BANCADA_SYSTEM_PASSWORD" \
  -v db="$POSTGRES_DB" <<'SQL'
CREATE ROLE bancada_owner  LOGIN PASSWORD :'owner_pw';
CREATE ROLE bancada_app    LOGIN PASSWORD :'app_pw'    NOBYPASSRLS;
CREATE ROLE bancada_system LOGIN PASSWORD :'system_pw' BYPASSRLS;

ALTER DATABASE :"db" OWNER TO bancada_owner;
ALTER SCHEMA public OWNER TO bancada_owner;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO bancada_app, bancada_system;

-- Extensions need superuser; create them here so migrations can use them.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS unaccent;

-- Tables created later by bancada_owner are readable and writable by the API roles.
ALTER DEFAULT PRIVILEGES FOR ROLE bancada_owner IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO bancada_app, bancada_system;
ALTER DEFAULT PRIVILEGES FOR ROLE bancada_owner IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO bancada_app, bancada_system;
ALTER DEFAULT PRIVILEGES FOR ROLE bancada_owner IN SCHEMA public
  GRANT EXECUTE ON FUNCTIONS TO bancada_app, bancada_system;
SQL
