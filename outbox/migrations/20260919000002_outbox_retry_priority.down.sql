-- SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
--
-- SPDX-License-Identifier: EUPL-1.2

DROP INDEX IF EXISTS golusoris_outbox_pending_idx;

ALTER TABLE golusoris_outbox
    DROP COLUMN IF EXISTS next_attempt_at;

CREATE INDEX golusoris_outbox_pending_idx
    ON golusoris_outbox (created_at)
    WHERE dispatched_at IS NULL;
