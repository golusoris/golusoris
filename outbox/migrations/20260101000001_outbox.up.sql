-- golusoris/outbox: transactional outbox table.
-- Apps write events into this table in the same tx as their domain
-- changes. A leader-gated drainer picks them up and enqueues river jobs.
--
-- pending_idx keeps the drainer's scan O(batch_size).

CREATE TABLE IF NOT EXISTS golusoris_outbox (
    id          BIGSERIAL    PRIMARY KEY,
    kind        TEXT         NOT NULL,
    payload     JSONB        NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    dispatched_at TIMESTAMPTZ,
    attempts    INTEGER      NOT NULL DEFAULT 0,
    last_error  TEXT
);

CREATE INDEX IF NOT EXISTS golusoris_outbox_pending_idx
    ON golusoris_outbox (created_at)
    WHERE dispatched_at IS NULL;
