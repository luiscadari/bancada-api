-- Service orders (RF-09 to RF-17): orders, versioned quotes with their items, and the
-- status history (RN-07). Orders belong to a branch (RN-02).

-- +goose Up
CREATE TABLE service_orders (
    id                        uuid PRIMARY KEY,
    account_id                uuid NOT NULL REFERENCES accounts (id),
    branch_id                 uuid NOT NULL REFERENCES branches (id),
    sequence                  bigint NOT NULL CHECK (sequence > 0),
    number                    text NOT NULL,
    customer_id               uuid NOT NULL REFERENCES customers (id),
    equipment_id              uuid NOT NULL REFERENCES equipment (id),
    technician_id             uuid REFERENCES users (id),
    priority                  text NOT NULL DEFAULT 'normal'
                              CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    origin_order_id           uuid REFERENCES service_orders (id),
    requester_contact_id      uuid REFERENCES customer_contacts (id),
    approver_contact_id       uuid REFERENCES customer_contacts (id),
    cost_center_id            uuid REFERENCES cost_centers (id),
    status                    text NOT NULL DEFAULT 'received' CHECK (status IN (
                                  'received', 'diagnosing', 'awaiting_approval', 'approved', 'in_progress',
                                  'awaiting_part', 'testing', 'ready', 'awaiting_pickup',
                                  'delivered', 'abandoned', 'canceled')),
    reported_defect           text NOT NULL,
    diagnosis                 text,
    technical_report          text,
    entry_checklist           jsonb NOT NULL DEFAULT '{}',
    accessories               text,
    device_password_encrypted bytea,
    pre_authorized_cents      bigint CHECK (pre_authorized_cents >= 0),
    diagnostic_fee_cents      bigint NOT NULL DEFAULT 0 CHECK (diagnostic_fee_cents >= 0),
    expected_delivery_at      timestamptz,
    ready_at                  timestamptz,
    delivered_at              timestamptz,
    canceled_at               timestamptz,
    abandoned_at              timestamptz,
    cancel_reason             text,
    picked_up_by_name         text,
    picked_up_by_document     text,
    open_balance_cents        bigint NOT NULL DEFAULT 0,
    balance_authorized_by     uuid REFERENCES users (id),
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now(),
    deleted_at                timestamptz,
    version                   bigint NOT NULL DEFAULT 1,
    origin_device_id          uuid,
    created_by                uuid,
    updated_by                uuid,
    CONSTRAINT service_orders_cancel_reason_check CHECK (status <> 'canceled' OR cancel_reason IS NOT NULL)
);
CREATE UNIQUE INDEX service_orders_sequence_key ON service_orders (branch_id, sequence);
CREATE INDEX service_orders_queue_idx ON service_orders (account_id, branch_id, status, id DESC);
CREATE INDEX service_orders_technician_idx ON service_orders (account_id, technician_id, status);
CREATE INDEX service_orders_customer_idx ON service_orders (account_id, customer_id, id DESC);
CREATE INDEX service_orders_equipment_idx ON service_orders (account_id, equipment_id, id DESC);
CREATE INDEX service_orders_number_trgm ON service_orders USING gin (lower(number) gin_trgm_ops);
CALL setup_business_table('service_orders');

CREATE TABLE quotes (
    id                    uuid PRIMARY KEY,
    account_id            uuid NOT NULL REFERENCES accounts (id),
    order_id              uuid NOT NULL REFERENCES service_orders (id),
    number                integer NOT NULL CHECK (number > 0),
    status                text NOT NULL DEFAULT 'draft'
                          CHECK (status IN ('draft', 'sent', 'approved', 'rejected', 'superseded')),
    notes                 text,
    subtotal_cents        bigint NOT NULL DEFAULT 0,
    discount_cents        bigint NOT NULL DEFAULT 0,
    total_cents           bigint NOT NULL DEFAULT 0 CHECK (total_cents >= 0),
    sent_at               timestamptz,
    valid_until           timestamptz,
    decision_channel      text CHECK (decision_channel IN
                              ('counter', 'phone', 'whatsapp', 'email', 'portal', 'pre_authorized', 'no_charge')),
    decided_by_name       text,
    decided_by_contact_id uuid REFERENCES customer_contacts (id),
    decided_by_user_id    uuid REFERENCES users (id),
    decided_at            timestamptz,
    decision_ip           inet,
    rejection_reason      text,
    previous_quote_id     uuid REFERENCES quotes (id),
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    deleted_at            timestamptz,
    version               bigint NOT NULL DEFAULT 1,
    origin_device_id      uuid,
    created_by            uuid,
    updated_by            uuid
);
CREATE UNIQUE INDEX quotes_number_key ON quotes (order_id, number);
-- At most one open (draft) quote per order.
CREATE UNIQUE INDEX quotes_one_draft_key ON quotes (order_id) WHERE status = 'draft';
CALL setup_business_table('quotes');

CREATE TABLE quote_items (
    id                uuid PRIMARY KEY,
    account_id        uuid NOT NULL REFERENCES accounts (id),
    quote_id          uuid NOT NULL REFERENCES quotes (id),
    position          integer NOT NULL,
    kind              text NOT NULL CHECK (kind IN ('service', 'part')),
    service_id        uuid REFERENCES services (id),
    product_id        uuid REFERENCES products (id),
    description       text NOT NULL,
    quantity          numeric(12, 3) NOT NULL CHECK (quantity > 0),
    list_price_cents  bigint NOT NULL CHECK (list_price_cents >= 0),
    unit_price_cents  bigint NOT NULL CHECK (unit_price_cents >= 0),
    discount_cents    bigint NOT NULL DEFAULT 0 CHECK (discount_cents >= 0),
    total_cents       bigint NOT NULL CHECK (total_cents >= 0),
    optional          boolean NOT NULL DEFAULT false,
    warranty_days     integer NOT NULL CHECK (warranty_days >= 0),
    warranty_until    date,
    covered_item_id   uuid REFERENCES quote_items (id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    deleted_at        timestamptz,
    version           bigint NOT NULL DEFAULT 1,
    origin_device_id  uuid,
    created_by        uuid,
    updated_by        uuid,
    CONSTRAINT quote_items_reference_check CHECK (
        (kind = 'service' AND service_id IS NOT NULL AND product_id IS NULL) OR
        (kind = 'part' AND product_id IS NOT NULL AND service_id IS NULL))
);
CREATE INDEX quote_items_quote_idx ON quote_items (quote_id, position);
CREATE INDEX quote_items_warranty_idx ON quote_items (account_id, warranty_until) WHERE warranty_until IS NOT NULL;
CALL setup_business_table('quote_items');

CREATE TABLE order_status_history (
    id          uuid PRIMARY KEY,
    account_id  uuid NOT NULL REFERENCES accounts (id),
    order_id    uuid NOT NULL REFERENCES service_orders (id),
    from_status text,
    to_status   text NOT NULL,
    reason      text,
    user_id     uuid REFERENCES users (id),
    device_id   uuid,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX order_status_history_order_idx ON order_status_history (order_id, created_at);
CALL enable_tenancy('order_status_history');

-- The status history is append-only (RN-07).
REVOKE UPDATE, DELETE, TRUNCATE ON order_status_history FROM PUBLIC;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bancada_app') THEN
        REVOKE UPDATE, DELETE, TRUNCATE ON order_status_history FROM bancada_app;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE order_status_history;
DROP TABLE quote_items;
DROP TABLE quotes;
DROP TABLE service_orders;
