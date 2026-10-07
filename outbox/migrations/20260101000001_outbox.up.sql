-- SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
--
-- SPDX-License-Identifier: EUPL-1.2

-- golusoris/outbox: transactional outbox table.
-- Apps write events into this table in the same tx as their domain
-- changes. Concurrent drainers claim rows and enqueue River jobs atomically.
--
-- pending_idx keeps the drainer's scan O(batch_size).

CREATE TABLE IF NOT EXISTS golusoris_outbox (
    id          BIGSERIAL    PRIMARY KEY,
    kind        TEXT         NOT NULL,
    payload     JSONB        NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    dispatched_at TIMESTAMPTZ,
    attempts    INTEGER      NOT NULL DEFAULT 0,
    last_error  TEXT,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS golusoris_outbox_pending_idx
    ON golusoris_outbox (next_attempt_at, id)
    WHERE dispatched_at IS NULL;
