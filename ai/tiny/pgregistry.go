package tiny

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/id"
)

// maxVersionRetries bounds the optimistic-version retry loop. A unique
// index protects the (tenant, name, version) triple; concurrent writers
// racing on MAX(version)+1 collide and retry. 16 is generous — collisions
// resolve in O(concurrent writers) and that is far below 16 in practice.
const maxVersionRetries = 16

// MigrationsFS exposes the registry schema so apps can apply it via
// db/migrate (Options.WithFS) or bundle it into their own migration set.
//
//go:embed migrations
var MigrationsFS embed.FS

// PGRegistry is a durable, per-tenant [Registry] backed by PostgreSQL.
// Version is allocated monotonically per (TenantID, Name) inside a
// transaction; the unique index golusoris_tiny_models_tenant_name_version_idx
// guarantees two concurrent writers never share a version.
//
// Apply ai/tiny/migrations before use (see [MigrationsFS]).
type PGRegistry struct {
	pool  *pgxpool.Pool
	idGen id.Generator
	clk   clockwork.Clock
}

// NewPGRegistry returns a PGRegistry over pool using the real clock.
func NewPGRegistry(pool *pgxpool.Pool) (*PGRegistry, error) {
	return NewPGRegistryWithClock(pool, clockwork.NewRealClock())
}

// NewPGRegistryWithClock returns a PGRegistry with an injected clock —
// tests freeze time so CreatedAt is deterministic.
func NewPGRegistryWithClock(pool *pgxpool.Pool, clk clockwork.Clock) (*PGRegistry, error) {
	if pool == nil {
		return nil, errors.New("ai/tiny: nil pool")
	}
	if clk == nil {
		return nil, errors.New("ai/tiny: nil clock")
	}
	return &PGRegistry{pool: pool, idGen: id.New(), clk: clk}, nil
}

// SaveJob stores j, assigning ID + CreatedAt when unset. The free-form
// fields (Dataset, Hyperparams, Tags) are persisted as a JSON spec blob;
// only id/name/tenant_id/base_model/created_at are queryable columns.
func (r *PGRegistry) SaveJob(ctx context.Context, j Job) error {
	if j.ID == "" {
		j.ID = r.idGen.NewUUID().String()
	}
	if j.CreatedAt.IsZero() {
		j.CreatedAt = r.clk.Now().UTC()
	}
	spec, err := json.Marshal(jobSpec{
		Dataset:     j.Dataset,
		Hyperparams: j.Hyperparams,
		Tags:        j.Tags,
	})
	if err != nil {
		return fmt.Errorf("ai/tiny: marshal job spec: %w", err)
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO golusoris_tiny_jobs (id, name, tenant_id, base_model, spec, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (id) DO UPDATE
		 SET name = EXCLUDED.name, tenant_id = EXCLUDED.tenant_id,
		     base_model = EXCLUDED.base_model, spec = EXCLUDED.spec`,
		j.ID, j.Name, j.TenantID, j.BaseModel, spec, j.CreatedAt)
	if err != nil {
		return fmt.Errorf("ai/tiny: insert job: %w", err)
	}
	return nil
}

// GetJob looks up a Job by ID.
func (r *PGRegistry) GetJob(ctx context.Context, jobID string) (Job, error) {
	var (
		j        Job
		specRaw  []byte
		baseName string
	)
	err := r.pool.QueryRow(
		ctx,
		`SELECT id, name, tenant_id, base_model, spec, created_at
		   FROM golusoris_tiny_jobs WHERE id = $1`, jobID,
	).Scan(&j.ID, &j.Name, &j.TenantID, &baseName, &specRaw, &j.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, fmt.Errorf("ai/tiny: job %q: %w", jobID, ErrNotFound)
	}
	if err != nil {
		return Job{}, fmt.Errorf("ai/tiny: select job: %w", err)
	}
	j.BaseModel = baseName
	var spec jobSpec
	if uErr := json.Unmarshal(specRaw, &spec); uErr != nil {
		return Job{}, fmt.Errorf("ai/tiny: unmarshal job spec: %w", uErr)
	}
	j.Dataset = spec.Dataset
	j.Hyperparams = spec.Hyperparams
	j.Tags = spec.Tags
	return j, nil
}

// SaveModel assigns ID + Version + CreatedAt as needed and inserts m.
// When Version is 0 it is allocated as max(version)+1 for (TenantID,
// Name). Concurrent writers can read the same max; the unique index
// golusoris_tiny_models_tenant_name_version_idx rejects the loser, which
// retries with a fresh max. The loop is bounded by [maxVersionRetries].
//
// A caller-supplied non-zero Version is inserted as-is and never retried —
// a collision there is a genuine duplicate, surfaced to the caller.
func (r *PGRegistry) SaveModel(ctx context.Context, m *Model) error {
	if m == nil {
		return errors.New("ai/tiny: nil model")
	}
	if m.Name == "" {
		return errors.New("ai/tiny: model.Name required")
	}
	if m.ID == "" {
		m.ID = r.idGen.NewUUID().String()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = r.clk.Now().UTC()
	}
	labels, metrics, metadata, err := marshalModelJSON(m)
	if err != nil {
		return err
	}
	explicitVersion := m.Version != 0
	for range maxVersionRetries {
		if !explicitVersion {
			next, vErr := r.nextVersion(ctx, m.TenantID, m.Name)
			if vErr != nil {
				return vErr
			}
			m.Version = next
		}
		iErr := r.insertModel(ctx, m, labels, metrics, metadata)
		if iErr == nil {
			return nil
		}
		// Retry only on a version collision for auto-assigned versions;
		// errors.As (inside isUniqueViolation) unwraps the wrapped error.
		if !explicitVersion && isUniqueViolation(iErr) {
			continue
		}
		return iErr
	}
	return fmt.Errorf("ai/tiny: insert model: version contention after %d attempts", maxVersionRetries)
}

// nextVersion returns max(version)+1 for (tenantID, name).
func (r *PGRegistry) nextVersion(ctx context.Context, tenantID, name string) (int, error) {
	var maxV int
	err := r.pool.QueryRow(
		ctx,
		`SELECT COALESCE(MAX(version), 0) FROM golusoris_tiny_models
		  WHERE tenant_id = $1 AND name = $2`, tenantID, name,
	).Scan(&maxV)
	if err != nil {
		return 0, fmt.Errorf("ai/tiny: max version: %w", err)
	}
	return maxV + 1, nil
}

// insertModel writes one model row; the caller manages version assignment.
func (r *PGRegistry) insertModel(ctx context.Context, m *Model, labels, metrics, metadata []byte) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO golusoris_tiny_models
		   (id, job_id, name, tenant_id, version, uri, format, modality,
		    task_kind, base_model, labels, metrics, metadata, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		m.ID, m.JobID, m.Name, m.TenantID, m.Version, m.URI, string(m.Format),
		string(m.Modality), string(m.TaskKind), m.BaseModel, labels, metrics, metadata, m.CreatedAt)
	if err != nil {
		return fmt.Errorf("ai/tiny: insert model: %w", err)
	}
	return nil
}

// isUniqueViolation reports whether err is a Postgres 23505 unique_violation.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// GetModel looks up a model by Ref. Version 0 resolves to the latest.
func (r *PGRegistry) GetModel(ctx context.Context, ref Ref) (Model, error) {
	if ref.Version == 0 {
		return r.Latest(ctx, ref.TenantID, ref.Name)
	}
	row := r.pool.QueryRow(ctx, selectModelSQL+
		` WHERE tenant_id = $1 AND name = $2 AND version = $3`,
		ref.TenantID, ref.Name, ref.Version)
	m, err := scanModel(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Model{}, fmt.Errorf("ai/tiny: model %s v%d: %w", ref.Name, ref.Version, ErrNotFound)
	}
	if err != nil {
		return Model{}, fmt.Errorf("ai/tiny: select model: %w", err)
	}
	return m, nil
}

// Latest returns the highest-versioned model for (tenantID, name).
func (r *PGRegistry) Latest(ctx context.Context, tenantID, name string) (Model, error) {
	row := r.pool.QueryRow(ctx, selectModelSQL+
		` WHERE tenant_id = $1 AND name = $2 ORDER BY version DESC LIMIT 1`,
		tenantID, name)
	m, err := scanModel(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Model{}, fmt.Errorf("ai/tiny: latest %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return Model{}, fmt.Errorf("ai/tiny: select latest: %w", err)
	}
	return m, nil
}

// List returns models matching f, sorted by (created_at desc, version desc).
func (r *PGRegistry) List(ctx context.Context, f ListFilter) ([]Model, error) {
	query := selectModelSQL + ` WHERE ($1 = '' OR tenant_id = $1)
		   AND ($2 = '' OR name = $2)
		   AND ($3 = '' OR task_kind = $3)
		 ORDER BY created_at DESC, version DESC`
	args := []any{f.TenantID, f.Name, string(f.TaskKind)}
	if f.Limit > 0 {
		query += ` LIMIT $4`
		args = append(args, f.Limit)
	}
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("ai/tiny: list models: %w", err)
	}
	defer rows.Close()
	out := make([]Model, 0)
	for rows.Next() {
		m, sErr := scanModel(rows)
		if sErr != nil {
			return nil, fmt.Errorf("ai/tiny: scan model: %w", sErr)
		}
		out = append(out, m)
	}
	if rErr := rows.Err(); rErr != nil {
		return nil, fmt.Errorf("ai/tiny: list rows: %w", rErr)
	}
	return out, nil
}

// jobSpec is the JSON blob persisted for a [Job]'s free-form fields.
type jobSpec struct {
	Dataset     Dataset           `json:"dataset"`
	Hyperparams map[string]any    `json:"hyperparams,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
}

// selectModelSQL is the shared column list for model reads; callers
// append the WHERE/ORDER/LIMIT clauses.
const selectModelSQL = `SELECT id, job_id, name, tenant_id, version, uri, format,
	modality, task_kind, base_model, labels, metrics, metadata, created_at
	FROM golusoris_tiny_models`

// rowScanner is satisfied by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanModel(s rowScanner) (Model, error) {
	var (
		m                          Model
		format, modality, taskKind string
		labels, metrics, metadata  []byte
	)
	if err := s.Scan(&m.ID, &m.JobID, &m.Name, &m.TenantID, &m.Version, &m.URI,
		&format, &modality, &taskKind, &m.BaseModel,
		&labels, &metrics, &metadata, &m.CreatedAt); err != nil {
		// Wrapped, but errors.Is in callers still unwraps to pgx.ErrNoRows.
		return Model{}, fmt.Errorf("ai/tiny: scan model row: %w", err)
	}
	m.Format = Format(format)
	m.Modality = Modality(modality)
	m.TaskKind = TaskKind(taskKind)
	if err := unmarshalIfSet(labels, &m.Labels); err != nil {
		return Model{}, fmt.Errorf("ai/tiny: unmarshal labels: %w", err)
	}
	if err := unmarshalIfSet(metrics, &m.Metrics); err != nil {
		return Model{}, fmt.Errorf("ai/tiny: unmarshal metrics: %w", err)
	}
	if err := unmarshalIfSet(metadata, &m.Metadata); err != nil {
		return Model{}, fmt.Errorf("ai/tiny: unmarshal metadata: %w", err)
	}
	return m, nil
}

func marshalModelJSON(m *Model) (labels, metrics, metadata []byte, err error) {
	if labels, err = json.Marshal(orEmptySlice(m.Labels)); err != nil {
		return nil, nil, nil, fmt.Errorf("ai/tiny: marshal labels: %w", err)
	}
	if metrics, err = json.Marshal(orEmptyMetrics(m.Metrics)); err != nil {
		return nil, nil, nil, fmt.Errorf("ai/tiny: marshal metrics: %w", err)
	}
	if metadata, err = json.Marshal(orEmptyMetadata(m.Metadata)); err != nil {
		return nil, nil, nil, fmt.Errorf("ai/tiny: marshal metadata: %w", err)
	}
	return labels, metrics, metadata, nil
}

func unmarshalIfSet(raw []byte, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("ai/tiny: unmarshal json column: %w", err)
	}
	return nil
}

func orEmptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func orEmptyMetrics(m map[string]float64) map[string]float64 {
	if m == nil {
		return map[string]float64{}
	}
	return m
}

func orEmptyMetadata(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// compile-time assertions.
var (
	_ Registry = (*PGRegistry)(nil)
	_ Registry = (*MemoryRegistry)(nil)
)
