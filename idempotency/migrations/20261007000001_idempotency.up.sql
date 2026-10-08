-- SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
--
-- SPDX-License-Identifier: EUPL-1.2

-- golusoris/idempotency: shared idempotency-key reservations.
-- state 1 = in flight (token owns it), 2 = completed (response replayable).
-- expires_idx keeps the bounded sweep O(batch).

CREATE TABLE IF NOT EXISTS golusoris_idempotency_keys (
    scope_key    TEXT         PRIMARY KEY,
    state        SMALLINT     NOT NULL,
    token        TEXT         NOT NULL,
    fingerprint  TEXT         NOT NULL,
    status_code  INTEGER      NOT NULL DEFAULT 0,
    header       JSONB        NOT NULL DEFAULT '{}'::jsonb,
    body         BYTEA        NOT NULL DEFAULT ''::bytea,
    expires_at   TIMESTAMPTZ  NOT NULL
);

CREATE INDEX IF NOT EXISTS golusoris_idempotency_keys_expires_idx
    ON golusoris_idempotency_keys (expires_at);
