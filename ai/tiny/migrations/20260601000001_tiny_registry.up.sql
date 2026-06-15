-- golusoris/ai/tiny: per-tenant model + job registry.
-- Backs PGRegistry. Model.Version is monotonic per (tenant_id, name);
-- the unique index enforces that no two models share a version within a
-- tenant/name pair, so concurrent SaveModel calls cannot collide.
--
-- tenant_id is stored as '' (never NULL) for the single-tenant case so
-- the unique index and Latest() lookups stay simple — NULLs would break
-- both UNIQUE and equality semantics.

CREATE TABLE IF NOT EXISTS golusoris_tiny_jobs (
    id          TEXT         PRIMARY KEY,
    name        TEXT         NOT NULL,
    tenant_id   TEXT         NOT NULL DEFAULT '',
    base_model  TEXT         NOT NULL,
    spec        JSONB        NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS golusoris_tiny_jobs_tenant_name_idx
    ON golusoris_tiny_jobs (tenant_id, name);

CREATE TABLE IF NOT EXISTS golusoris_tiny_models (
    id          TEXT         PRIMARY KEY,
    job_id      TEXT         NOT NULL,
    name        TEXT         NOT NULL,
    tenant_id   TEXT         NOT NULL DEFAULT '',
    version     INTEGER      NOT NULL,
    uri         TEXT         NOT NULL,
    format      TEXT         NOT NULL,
    modality    TEXT         NOT NULL,
    task_kind   TEXT         NOT NULL,
    base_model  TEXT         NOT NULL,
    labels      JSONB        NOT NULL DEFAULT '[]'::jsonb,
    metrics     JSONB        NOT NULL DEFAULT '{}'::jsonb,
    metadata    JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS golusoris_tiny_models_tenant_name_version_idx
    ON golusoris_tiny_models (tenant_id, name, version);

CREATE INDEX IF NOT EXISTS golusoris_tiny_models_list_idx
    ON golusoris_tiny_models (tenant_id, name, created_at DESC);
