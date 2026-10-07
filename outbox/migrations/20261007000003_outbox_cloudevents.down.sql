-- SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
--
-- SPDX-License-Identifier: EUPL-1.2

DROP INDEX IF EXISTS golusoris_outbox_event_id_idx;

ALTER TABLE golusoris_outbox
    DROP COLUMN IF EXISTS tracestate,
    DROP COLUMN IF EXISTS traceparent,
    DROP COLUMN IF EXISTS tenant,
    DROP COLUMN IF EXISTS data_schema,
    DROP COLUMN IF EXISTS subject,
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS event_id;
