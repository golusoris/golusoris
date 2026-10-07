-- SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
--
-- SPDX-License-Identifier: EUPL-1.2

ALTER TABLE golusoris_outbox
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ;

UPDATE golusoris_outbox
    SET next_attempt_at = created_at
    WHERE next_attempt_at IS NULL;

ALTER TABLE golusoris_outbox
    ALTER COLUMN next_attempt_at SET DEFAULT now(),
    ALTER COLUMN next_attempt_at SET NOT NULL;

DROP INDEX IF EXISTS golusoris_outbox_pending_idx;

CREATE INDEX golusoris_outbox_pending_idx
    ON golusoris_outbox (next_attempt_at, id)
    WHERE dispatched_at IS NULL;
