-- Foundation: tenancy helpers (ADR-01), row versioning (ADR-03) and the audit log (RN-03).

-- +goose Up
-- +goose StatementBegin
-- current_account_id returns the tenant of the current transaction, set by the API with
-- set_config('app.account_id', ..., true). NULL when unset, which matches no row.
CREATE FUNCTION current_account_id() RETURNS uuid
LANGUAGE sql STABLE AS $$
    SELECT NULLIF(current_setting('app.account_id', true), '')::uuid
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- touch_row bumps the sync version and the update timestamp on every UPDATE.
CREATE FUNCTION touch_row() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.version := OLD.version + 1;
    NEW.updated_at := now();
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- enable_tenancy turns on (and forces) row-level security for a table with an
-- account_id column, isolating each account's rows.
CREATE PROCEDURE enable_tenancy(tbl regclass, account_column text DEFAULT 'account_id')
LANGUAGE plpgsql AS $$
BEGIN
    EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', tbl);
    EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', tbl);
    EXECUTE format(
        'CREATE POLICY tenant_isolation ON %s USING (%I = current_account_id()) WITH CHECK (%I = current_account_id())',
        tbl, account_column, account_column);
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- setup_business_table applies the conventions of every business table: tenancy and
-- the version/updated_at trigger.
CREATE PROCEDURE setup_business_table(tbl regclass)
LANGUAGE plpgsql AS $$
BEGIN
    CALL enable_tenancy(tbl);
    EXECUTE format('CREATE TRIGGER touch_row BEFORE UPDATE ON %s FOR EACH ROW EXECUTE FUNCTION touch_row()', tbl);
END
$$;
-- +goose StatementEnd

CREATE TABLE audit_log (
    id         uuid PRIMARY KEY,
    account_id uuid NOT NULL,
    branch_id  uuid,
    user_id    uuid,
    device_id  uuid,
    entity     text NOT NULL,
    entity_id  uuid,
    action     text NOT NULL,
    changes    jsonb NOT NULL DEFAULT '{}',
    request_id text,
    ip         inet,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_entity_idx ON audit_log (account_id, entity, entity_id);
CREATE INDEX audit_log_created_idx ON audit_log (account_id, created_at DESC);
CALL enable_tenancy('audit_log');
-- The audit log is append-only.
REVOKE UPDATE, DELETE, TRUNCATE ON audit_log FROM PUBLIC;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bancada_app') THEN
        REVOKE UPDATE, DELETE, TRUNCATE ON audit_log FROM bancada_app;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE audit_log;
DROP PROCEDURE setup_business_table(regclass);
DROP PROCEDURE enable_tenancy(regclass, text);
DROP FUNCTION touch_row();
DROP FUNCTION current_account_id();
