-- SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
--
-- SPDX-License-Identifier: EUPL-1.2

-- golusoris/outbox: CloudEvents context attributes.
-- event_id is the stable CloudEvents id (and NATS Nats-Msg-Id). It is
-- assigned once at insert, so every retry and replay reuses it.
-- The volatile default rewrites existing rows under an exclusive lock.

ALTER TABLE golusoris_outbox
    ADD COLUMN IF NOT EXISTS event_id    UUID NOT NULL DEFAULT gen_random_uuid(),
    ADD COLUMN IF NOT EXISTS source      TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS subject     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS data_schema TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS tenant      TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS traceparent TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS tracestate  TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS golusoris_outbox_event_id_idx
    ON golusoris_outbox (event_id);
