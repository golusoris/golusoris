package tiny_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/ai/tiny"
	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

// newPGRegistry boots a Postgres container, applies the tiny migrations,
// and returns a PGRegistry on a frozen clock for deterministic CreatedAt.
func newPGRegistry(t *testing.T) (*tiny.PGRegistry, *clockwork.FakeClock) {
	t.Helper()
	pool := pgtest.Start(t)
	applyMigration(t, pool.Config().ConnConfig.ConnString())
	clk := clockwork.NewFakeClockAt(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
	reg, err := tiny.NewPGRegistryWithClock(pool, clk)
	require.NoError(t, err)
	return reg, clk
}

func applyMigration(t *testing.T, dsn string) {
	t.Helper()
	m, err := dbmigrate.New(
		dbmigrate.Options{Path: "migrations"}.WithFS(tiny.MigrationsFS),
		dbpgx.Options{DSN: dsn},
		slog.New(slog.DiscardHandler),
	)
	require.NoError(t, err)
	defer func() { _ = m.Close() }()
	require.NoError(t, m.Up())
}

func sampleJob() tiny.Job {
	return tiny.Job{
		Name:      "support-intent",
		TenantID:  "acme",
		BaseModel: "mediapipe/text_classifier",
		Dataset: tiny.Dataset{
			URI:      "s3://bucket/ds.jsonl",
			Format:   "jsonl",
			Modality: tiny.ModalityText,
			TaskKind: tiny.TaskClassify,
			Examples: 1000,
		},
		Hyperparams: map[string]any{"epochs": float64(3)},
		Tags:        map[string]string{"team": "support"},
	}
}

func sampleModel(name, tenant string) *tiny.Model {
	return &tiny.Model{
		Name:      name,
		TenantID:  tenant,
		JobID:     "job-1",
		URI:       "s3://bucket/m.tflite",
		Format:    tiny.FormatTFLite,
		Modality:  tiny.ModalityText,
		TaskKind:  tiny.TaskClassify,
		BaseModel: "mediapipe/text_classifier",
		Labels:    []string{"billing", "tech", "sales"},
		Metrics:   map[string]float64{"accuracy": 0.93},
		Metadata:  map[string]string{"trainer": "litert"},
	}
}

func TestPGRegistry_SaveGetJob_roundTrip(t *testing.T) {
	t.Parallel()
	reg, clk := newPGRegistry(t)
	ctx := context.Background()

	// SaveJob assigns an ID on its own copy, so pin a deterministic ID to
	// fetch back. With a non-empty ID, SaveJob upserts under that ID.
	job := sampleJob()
	job.ID = "job-fixed"
	require.NoError(t, reg.SaveJob(ctx, job))

	got, err := reg.GetJob(ctx, "job-fixed")
	require.NoError(t, err)
	require.Equal(t, "support-intent", got.Name)
	require.Equal(t, "acme", got.TenantID)
	require.Equal(t, "mediapipe/text_classifier", got.BaseModel)
	require.Equal(t, "s3://bucket/ds.jsonl", got.Dataset.URI)
	require.Equal(t, tiny.ModalityText, got.Dataset.Modality)
	require.InEpsilon(t, float64(3), got.Hyperparams["epochs"], 1e-9)
	require.Equal(t, "support", got.Tags["team"])
	// pgx scans TIMESTAMPTZ into the local zone; compare the instant.
	require.True(t, clk.Now().Equal(got.CreatedAt))
}

func TestPGRegistry_GetJob_notFound(t *testing.T) {
	t.Parallel()
	reg, _ := newPGRegistry(t)
	_, err := reg.GetJob(context.Background(), "missing")
	require.ErrorIs(t, err, tiny.ErrNotFound)
}

func TestPGRegistry_SaveModel_assignsMonotonicVersion(t *testing.T) {
	t.Parallel()
	reg, _ := newPGRegistry(t)
	ctx := context.Background()

	m1 := sampleModel("intent", "acme")
	require.NoError(t, reg.SaveModel(ctx, m1))
	require.Equal(t, 1, m1.Version)
	require.NotEmpty(t, m1.ID)

	m2 := sampleModel("intent", "acme")
	require.NoError(t, reg.SaveModel(ctx, m2))
	require.Equal(t, 2, m2.Version)

	// Different name restarts versioning.
	m3 := sampleModel("other", "acme")
	require.NoError(t, reg.SaveModel(ctx, m3))
	require.Equal(t, 1, m3.Version)

	// Different tenant, same name, is isolated.
	m4 := sampleModel("intent", "globex")
	require.NoError(t, reg.SaveModel(ctx, m4))
	require.Equal(t, 1, m4.Version)
}

func TestPGRegistry_GetModel_byRefAndLatest(t *testing.T) {
	t.Parallel()
	reg, _ := newPGRegistry(t)
	ctx := context.Background()

	for range 3 {
		require.NoError(t, reg.SaveModel(ctx, sampleModel("intent", "acme")))
	}

	v2, err := reg.GetModel(ctx, tiny.Ref{Name: "intent", TenantID: "acme", Version: 2})
	require.NoError(t, err)
	require.Equal(t, 2, v2.Version)
	require.Equal(t, []string{"billing", "tech", "sales"}, v2.Labels)
	require.InDelta(t, 0.93, v2.Metrics["accuracy"], 1e-6)
	require.Equal(t, "litert", v2.Metadata["trainer"])

	// Version 0 → latest.
	latest, err := reg.GetModel(ctx, tiny.Ref{Name: "intent", TenantID: "acme"})
	require.NoError(t, err)
	require.Equal(t, 3, latest.Version)

	explicitLatest, err := reg.Latest(ctx, "acme", "intent")
	require.NoError(t, err)
	require.Equal(t, 3, explicitLatest.Version)
}

func TestPGRegistry_GetModel_notFound(t *testing.T) {
	t.Parallel()
	reg, _ := newPGRegistry(t)
	ctx := context.Background()
	_, err := reg.GetModel(ctx, tiny.Ref{Name: "x", TenantID: "t", Version: 7})
	require.ErrorIs(t, err, tiny.ErrNotFound)
	_, err = reg.Latest(ctx, "t", "x")
	require.ErrorIs(t, err, tiny.ErrNotFound)
}

func TestPGRegistry_SaveModel_rejectsNilAndEmptyName(t *testing.T) {
	t.Parallel()
	reg, _ := newPGRegistry(t)
	ctx := context.Background()
	require.ErrorContains(t, reg.SaveModel(ctx, nil), "nil model")
	require.ErrorContains(t, reg.SaveModel(ctx, &tiny.Model{}), "Name required")
}

func TestPGRegistry_List_filtersAndOrders(t *testing.T) {
	t.Parallel()
	reg, clk := newPGRegistry(t)
	ctx := context.Background()

	// Two acme/intent versions and one globex/intent, with advancing time
	// so ordering by created_at desc is observable.
	require.NoError(t, reg.SaveModel(ctx, sampleModel("intent", "acme")))
	clk.Advance(time.Hour)
	require.NoError(t, reg.SaveModel(ctx, sampleModel("intent", "acme")))
	clk.Advance(time.Hour)
	require.NoError(t, reg.SaveModel(ctx, sampleModel("intent", "globex")))

	// Tenant filter.
	acme, err := reg.List(ctx, tiny.ListFilter{TenantID: "acme"})
	require.NoError(t, err)
	require.Len(t, acme, 2)
	require.Equal(t, 2, acme[0].Version) // newest first

	// Name + tenant.
	byName, err := reg.List(ctx, tiny.ListFilter{TenantID: "globex", Name: "intent"})
	require.NoError(t, err)
	require.Len(t, byName, 1)

	// TaskKind filter (all are classify).
	classify, err := reg.List(ctx, tiny.ListFilter{TaskKind: tiny.TaskClassify})
	require.NoError(t, err)
	require.Len(t, classify, 3)

	// Wrong task kind → empty.
	gen, err := reg.List(ctx, tiny.ListFilter{TaskKind: tiny.TaskGenerate})
	require.NoError(t, err)
	require.Empty(t, gen)

	// Limit.
	limited, err := reg.List(ctx, tiny.ListFilter{Limit: 1})
	require.NoError(t, err)
	require.Len(t, limited, 1)
}

func TestPGRegistry_SaveModel_concurrentVersionsAreDistinct(t *testing.T) {
	t.Parallel()
	reg, _ := newPGRegistry(t)
	ctx := context.Background()

	const n = 8
	var wg sync.WaitGroup
	versions := make([]int, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			m := sampleModel("race", "acme")
			if err := reg.SaveModel(ctx, m); err != nil {
				errs[idx] = err
				return
			}
			versions[idx] = m.Version
		}(i)
	}
	wg.Wait()

	seen := map[int]bool{}
	for i := range n {
		require.NoError(t, errs[i])
		require.False(t, seen[versions[i]], "duplicate version %d", versions[i])
		seen[versions[i]] = true
	}
	require.Len(t, seen, n)
}

func TestPGRegistry_NewPGRegistry_rejectsNil(t *testing.T) {
	t.Parallel()
	_, err := tiny.NewPGRegistry(nil)
	require.ErrorContains(t, err, "nil pool")
	_, err = tiny.NewPGRegistryWithClock(nil, clockwork.NewRealClock())
	require.ErrorContains(t, err, "nil pool")
}
