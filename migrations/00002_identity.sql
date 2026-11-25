-- Identity: accounts, branches, users, roles, invitations, devices, number blocks and
-- refresh tokens (requirements sections 3 and 9, ADR-01, ADR-03, ADR-07).

-- +goose Up
CREATE TABLE roles (
    code text PRIMARY KEY,
    name text NOT NULL
);
INSERT INTO roles (code, name) VALUES
    ('owner', 'Dono'),
    ('attendant', 'Atendente'),
    ('technician', 'Técnico');

CREATE TABLE accounts (
    id               uuid PRIMARY KEY,
    trade_name       text NOT NULL,
    document         text,
    niche            text NOT NULL DEFAULT 'it_repair',
    status           text NOT NULL DEFAULT 'trial'
                     CHECK (status IN ('trial', 'active', 'delinquent', 'suspended', 'closed')),
    trial_ends_at    timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    version          bigint NOT NULL DEFAULT 1,
    origin_device_id uuid,
    created_by       uuid,
    updated_by       uuid
);
CALL enable_tenancy('accounts', 'id');
CREATE TRIGGER touch_row BEFORE UPDATE ON accounts FOR EACH ROW EXECUTE FUNCTION touch_row();

CREATE TABLE branches (
    id               uuid PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    name             text NOT NULL,
    os_prefix        text NOT NULL CHECK (os_prefix ~ '^[A-Z0-9]{2,6}$'),
    timezone         text NOT NULL DEFAULT 'America/Sao_Paulo',
    settings         jsonb NOT NULL DEFAULT '{}',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    version          bigint NOT NULL DEFAULT 1,
    origin_device_id uuid,
    created_by       uuid,
    updated_by       uuid
);
CREATE UNIQUE INDEX branches_prefix_key ON branches (account_id, os_prefix) WHERE deleted_at IS NULL;
CALL setup_business_table('branches');

-- Next service order number of each branch (RN-04). Updated atomically.
CREATE TABLE branch_counters (
    branch_id   uuid PRIMARY KEY REFERENCES branches (id),
    account_id  uuid NOT NULL REFERENCES accounts (id),
    next_number bigint NOT NULL DEFAULT 1 CHECK (next_number > 0)
);
CALL enable_tenancy('branch_counters');

CREATE TABLE users (
    id               uuid PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    name             text NOT NULL,
    email            text NOT NULL,
    password_hash    text,
    mfa_enabled      boolean NOT NULL DEFAULT false,
    status           text NOT NULL DEFAULT 'active' CHECK (status IN ('invited', 'active', 'disabled')),
    last_login_at    timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    version          bigint NOT NULL DEFAULT 1,
    origin_device_id uuid,
    created_by       uuid,
    updated_by       uuid
);
-- Emails are unique across the platform so login does not need to ask for the account.
CREATE UNIQUE INDEX users_email_key ON users (lower(email)) WHERE deleted_at IS NULL;
CREATE INDEX users_account_idx ON users (account_id);
CALL setup_business_table('users');

-- Roles per branch. A NULL branch_id grants the role on every branch (owners only).
CREATE TABLE user_branch_roles (
    id               uuid PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    user_id          uuid NOT NULL REFERENCES users (id),
    branch_id        uuid REFERENCES branches (id),
    role             text NOT NULL REFERENCES roles (code),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    version          bigint NOT NULL DEFAULT 1,
    origin_device_id uuid,
    created_by       uuid,
    updated_by       uuid,
    CHECK ((role = 'owner') = (branch_id IS NULL))
);
CREATE UNIQUE INDEX user_branch_roles_key ON user_branch_roles
    (user_id, COALESCE(branch_id, '00000000-0000-0000-0000-000000000000'::uuid), role)
    WHERE deleted_at IS NULL;
CREATE INDEX user_branch_roles_user_idx ON user_branch_roles (account_id, user_id);
CALL setup_business_table('user_branch_roles');

CREATE TABLE invitations (
    id               uuid PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    user_id          uuid NOT NULL REFERENCES users (id),
    token_hash       text NOT NULL UNIQUE,
    expires_at       timestamptz NOT NULL,
    accepted_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    version          bigint NOT NULL DEFAULT 1,
    origin_device_id uuid,
    created_by       uuid,
    updated_by       uuid
);
CREATE INDEX invitations_user_idx ON invitations (account_id, user_id);
CALL setup_business_table('invitations');

-- Each installed desktop app (ADR-03, ADR-07).
CREATE TABLE devices (
    id               uuid PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    branch_id        uuid NOT NULL REFERENCES branches (id),
    name             text NOT NULL,
    platform         text NOT NULL CHECK (platform IN ('windows', 'macos', 'linux')),
    last_seen_at     timestamptz,
    revoked_at       timestamptz,
    revoked_by       uuid,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    version          bigint NOT NULL DEFAULT 1,
    origin_device_id uuid,
    created_by       uuid,
    updated_by       uuid
);
CREATE INDEX devices_branch_idx ON devices (account_id, branch_id);
CALL setup_business_table('devices');

-- Service order numbers reserved by a device for offline use (RN-04).
CREATE TABLE number_blocks (
    id               uuid PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    branch_id        uuid NOT NULL REFERENCES branches (id),
    device_id        uuid NOT NULL REFERENCES devices (id),
    range_start      bigint NOT NULL,
    range_end        bigint NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    version          bigint NOT NULL DEFAULT 1,
    origin_device_id uuid,
    created_by       uuid,
    updated_by       uuid,
    CHECK (range_end >= range_start)
);
CREATE INDEX number_blocks_device_idx ON number_blocks (account_id, branch_id, device_id);
CALL setup_business_table('number_blocks');

-- Opaque refresh tokens; only the SHA-256 hash is stored. Tokens of one login share a
-- family so a reused (stolen) token revokes the whole chain.
CREATE TABLE refresh_tokens (
    id          uuid PRIMARY KEY,
    account_id  uuid NOT NULL REFERENCES accounts (id),
    user_id     uuid NOT NULL REFERENCES users (id),
    device_id   uuid REFERENCES devices (id),
    family_id   uuid NOT NULL,
    token_hash  text NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    revoked_at  timestamptz,
    ip          inet,
    user_agent  text,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX refresh_tokens_family_idx ON refresh_tokens (account_id, family_id);
CREATE INDEX refresh_tokens_user_idx ON refresh_tokens (account_id, user_id);
CREATE INDEX refresh_tokens_device_idx ON refresh_tokens (account_id, device_id);
CALL enable_tenancy('refresh_tokens');

-- The audit log references accounts once they exist.
ALTER TABLE audit_log ADD CONSTRAINT audit_log_account_fk FOREIGN KEY (account_id) REFERENCES accounts (id);

-- +goose Down
ALTER TABLE audit_log DROP CONSTRAINT audit_log_account_fk;
DROP TABLE refresh_tokens;
DROP TABLE number_blocks;
DROP TABLE devices;
DROP TABLE invitations;
DROP TABLE user_branch_roles;
DROP TABLE users;
DROP TABLE branch_counters;
DROP TABLE branches;
DROP TABLE accounts;
DROP TABLE roles;
