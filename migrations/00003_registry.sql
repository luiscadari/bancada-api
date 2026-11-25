-- Registry (cadastros): customers, contacts, cost centers, equipment types, equipment
-- and the catalog of services, products and suppliers (RF-01 to RF-06, RF-45).
-- Shared by every branch of the account (RN-02).

-- +goose Up
-- +goose StatementBegin
-- Immutable wrapper so unaccent() can be used in indexes.
CREATE FUNCTION f_unaccent(text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $$
    SELECT public.unaccent('public.unaccent'::regdictionary, $1)
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- search_text normalizes free text for case- and accent-insensitive search. Names are
-- schema-qualified because index maintenance runs with a restricted search_path (PG17+).
CREATE FUNCTION search_text(text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $$
    SELECT lower(public.f_unaccent($1))
$$;
-- +goose StatementEnd

CREATE TABLE customers (
    id               uuid PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    type             text NOT NULL CHECK (type IN ('individual', 'company')),
    name             text NOT NULL,
    document         text CHECK (document ~ '^[0-9]{11}$|^[0-9]{14}$'),
    notes            text,
    legal_basis      text NOT NULL DEFAULT 'contract'
                     CHECK (legal_basis IN ('contract', 'consent', 'legal_obligation', 'legitimate_interest')),
    consents         jsonb NOT NULL DEFAULT '{}',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    version          bigint NOT NULL DEFAULT 1,
    origin_device_id uuid,
    created_by       uuid,
    updated_by       uuid
);
CREATE UNIQUE INDEX customers_document_key ON customers (account_id, document)
    WHERE document IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX customers_name_trgm ON customers USING gin (search_text(name) gin_trgm_ops);
CREATE INDEX customers_account_idx ON customers (account_id, id DESC);
CALL setup_business_table('customers');

CREATE TABLE customer_contacts (
    id                   uuid PRIMARY KEY,
    account_id           uuid NOT NULL REFERENCES accounts (id),
    customer_id          uuid NOT NULL REFERENCES customers (id),
    name                 text NOT NULL,
    phone                text,
    phone_digits         text GENERATED ALWAYS AS (NULLIF(regexp_replace(COALESCE(phone, ''), '\D', '', 'g'), '')) STORED,
    email                text,
    is_primary           boolean NOT NULL DEFAULT false,
    is_requester         boolean NOT NULL DEFAULT false,
    is_approver          boolean NOT NULL DEFAULT false,
    approval_limit_cents bigint CHECK (approval_limit_cents >= 0),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    deleted_at           timestamptz,
    version              bigint NOT NULL DEFAULT 1,
    origin_device_id     uuid,
    created_by           uuid,
    updated_by           uuid
);
CREATE INDEX customer_contacts_customer_idx ON customer_contacts (account_id, customer_id);
CREATE INDEX customer_contacts_phone_trgm ON customer_contacts USING gin (phone_digits gin_trgm_ops);
CREATE INDEX customer_contacts_email_idx ON customer_contacts (account_id, lower(email));
CALL setup_business_table('customer_contacts');

CREATE TABLE cost_centers (
    id               uuid PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    customer_id      uuid NOT NULL REFERENCES customers (id),
    name             text NOT NULL,
    code             text,
    active           boolean NOT NULL DEFAULT true,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    version          bigint NOT NULL DEFAULT 1,
    origin_device_id uuid,
    created_by       uuid,
    updated_by       uuid
);
CREATE INDEX cost_centers_customer_idx ON cost_centers (account_id, customer_id);
CALL setup_business_table('cost_centers');

-- The generic core (ADR-05): each type defines its custom fields and entry checklist.
CREATE TABLE equipment_types (
    id               uuid PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    name             text NOT NULL,
    description      text,
    active           boolean NOT NULL DEFAULT true,
    field_schema     jsonb NOT NULL DEFAULT '{}',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    version          bigint NOT NULL DEFAULT 1,
    origin_device_id uuid,
    created_by       uuid,
    updated_by       uuid
);
CREATE UNIQUE INDEX equipment_types_name_key ON equipment_types (account_id, search_text(name)) WHERE deleted_at IS NULL;
CALL setup_business_table('equipment_types');

CREATE TABLE custom_fields (
    id               uuid PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    type_id          uuid NOT NULL REFERENCES equipment_types (id),
    key              text NOT NULL CHECK (key ~ '^[a-z][a-z0-9_]{0,49}$'),
    label            text NOT NULL,
    kind             text NOT NULL CHECK (kind IN ('text', 'number', 'integer', 'boolean', 'date', 'select')),
    required         boolean NOT NULL DEFAULT false,
    options          jsonb NOT NULL DEFAULT '[]',
    position         integer NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    version          bigint NOT NULL DEFAULT 1,
    origin_device_id uuid,
    created_by       uuid,
    updated_by       uuid
);
CREATE UNIQUE INDEX custom_fields_key ON custom_fields (type_id, key) WHERE deleted_at IS NULL;
CALL setup_business_table('custom_fields');

CREATE TABLE checklist_items (
    id               uuid PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    type_id          uuid NOT NULL REFERENCES equipment_types (id),
    key              text NOT NULL CHECK (key ~ '^[a-z][a-z0-9_]{0,49}$'),
    label            text NOT NULL,
    kind             text NOT NULL CHECK (kind IN ('check', 'text')),
    required         boolean NOT NULL DEFAULT false,
    position         integer NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    version          bigint NOT NULL DEFAULT 1,
    origin_device_id uuid,
    created_by       uuid,
    updated_by       uuid
);
CREATE UNIQUE INDEX checklist_items_key ON checklist_items (type_id, key) WHERE deleted_at IS NULL;
CALL setup_business_table('checklist_items');

CREATE TABLE equipment (
    id               uuid PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    customer_id      uuid NOT NULL REFERENCES customers (id),
    type_id          uuid NOT NULL REFERENCES equipment_types (id),
    brand            text,
    model            text,
    serial_number    text,
    attributes       jsonb NOT NULL DEFAULT '{}',
    notes            text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    version          bigint NOT NULL DEFAULT 1,
    origin_device_id uuid,
    created_by       uuid,
    updated_by       uuid
);
CREATE INDEX equipment_customer_idx ON equipment (account_id, customer_id);
CREATE INDEX equipment_serial_idx ON equipment (account_id, lower(serial_number));
CREATE INDEX equipment_serial_trgm ON equipment USING gin (lower(serial_number) gin_trgm_ops);
CREATE INDEX equipment_attributes_idx ON equipment USING gin (attributes jsonb_path_ops);
CALL setup_business_table('equipment');

CREATE TABLE services (
    id                 uuid PRIMARY KEY,
    account_id         uuid NOT NULL REFERENCES accounts (id),
    name               text NOT NULL,
    description        text,
    price_cents        bigint NOT NULL CHECK (price_cents >= 0),
    estimated_minutes  integer CHECK (estimated_minutes >= 0),
    warranty_days      integer NOT NULL DEFAULT 90 CHECK (warranty_days >= 0),
    commission_percent numeric(5, 2) NOT NULL DEFAULT 0 CHECK (commission_percent BETWEEN 0 AND 100),
    active             boolean NOT NULL DEFAULT true,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    deleted_at         timestamptz,
    version            bigint NOT NULL DEFAULT 1,
    origin_device_id   uuid,
    created_by         uuid,
    updated_by         uuid
);
CREATE INDEX services_name_trgm ON services USING gin (search_text(name) gin_trgm_ops);
CALL setup_business_table('services');

CREATE TABLE products (
    id               uuid PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    name             text NOT NULL,
    description      text,
    sku              text,
    barcode          text,
    unit             text NOT NULL DEFAULT 'un',
    cost_cents       bigint NOT NULL DEFAULT 0 CHECK (cost_cents >= 0),
    price_cents      bigint NOT NULL CHECK (price_cents >= 0),
    min_stock        numeric(12, 3) NOT NULL DEFAULT 0 CHECK (min_stock >= 0),
    warranty_days    integer NOT NULL DEFAULT 90 CHECK (warranty_days >= 0),
    active           boolean NOT NULL DEFAULT true,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    version          bigint NOT NULL DEFAULT 1,
    origin_device_id uuid,
    created_by       uuid,
    updated_by       uuid
);
CREATE UNIQUE INDEX products_sku_key ON products (account_id, lower(sku)) WHERE sku IS NOT NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX products_barcode_key ON products (account_id, barcode) WHERE barcode IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX products_name_trgm ON products USING gin (search_text(name) gin_trgm_ops);
CALL setup_business_table('products');

CREATE TABLE suppliers (
    id               uuid PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    name             text NOT NULL,
    document         text,
    email            text,
    phone            text,
    contact_name     text,
    notes            text,
    active           boolean NOT NULL DEFAULT true,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    version          bigint NOT NULL DEFAULT 1,
    origin_device_id uuid,
    created_by       uuid,
    updated_by       uuid
);
CREATE INDEX suppliers_name_trgm ON suppliers USING gin (search_text(name) gin_trgm_ops);
CALL setup_business_table('suppliers');

-- +goose Down
DROP TABLE suppliers;
DROP TABLE products;
DROP TABLE services;
DROP TABLE equipment;
DROP TABLE checklist_items;
DROP TABLE custom_fields;
DROP TABLE equipment_types;
DROP TABLE cost_centers;
DROP TABLE customer_contacts;
DROP TABLE customers;
DROP FUNCTION search_text(text);
DROP FUNCTION f_unaccent(text);
