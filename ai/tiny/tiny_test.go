package tiny_test

import (
	"errors"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/ai/tiny"
)

func TestMemoryRegistry_saveJob_assignsIDAndCreatedAt(t *testing.T) {
	t.Parallel()
	clk := clockwork.NewFakeClockAt(time.Date(2026, 4, 14, 10, 0, 0, 0, time.UTC))
	r := tiny.NewMemoryRegistryWithClock(clk)

	j := tiny.Job{
		Name:      "intent-classifier",
		BaseModel: "mediapipe/text_classifier",
		Dataset: tiny.Dataset{
			URI:      "file:///tmp/data.csv",
			Format:   "csv",
			Modality: tiny.ModalityText,
			TaskKind: tiny.TaskClassify,
		},
	}
	require.NoError(t, r.SaveJob(t.Context(), j))

	// j is saved by value — retrieve to inspect the stored copy.
	// Since SaveJob assigns ID internally, list all jobs via Registry isn't on the interface;
	// instead, we round-trip through a fresh job with a known ID.
	j2 := tiny.Job{ID: "fixed-id", Name: "x", BaseModel: "b", Dataset: tiny.Dataset{URI: "u", Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify}}
	require.NoError(t, r.SaveJob(t.Context(), j2))
	got, err := r.GetJob(t.Context(), "fixed-id")
	require.NoError(t, err)
	require.Equal(t, "fixed-id", got.ID)
	require.WithinDuration(t, clk.Now().UTC(), got.CreatedAt, time.Second)
}

func TestMemoryRegistry_saveModel_assignsMonotonicVersion(t *testing.T) {
	t.Parallel()
	r := tiny.NewMemoryRegistry()
	ctx := t.Context()

	m1 := &tiny.Model{Name: "support-intent", TenantID: "t1", URI: "s3://a/1", Format: tiny.FormatTFLite, TaskKind: tiny.TaskClassify}
	require.NoError(t, r.SaveModel(ctx, m1))
	require.Equal(t, 1, m1.Version)
	require.NotEmpty(t, m1.ID)

	m2 := &tiny.Model{Name: "support-intent", TenantID: "t1", URI: "s3://a/2", Format: tiny.FormatTFLite, TaskKind: tiny.TaskClassify}
	require.NoError(t, r.SaveModel(ctx, m2))
	require.Equal(t, 2, m2.Version)

	// Different tenant, same name — restarts at 1.
	m3 := &tiny.Model{Name: "support-intent", TenantID: "t2", URI: "s3://a/3", Format: tiny.FormatTFLite, TaskKind: tiny.TaskClassify}
	require.NoError(t, r.SaveModel(ctx, m3))
	require.Equal(t, 1, m3.Version)

	// Pinned version is preserved.
	m4 := &tiny.Model{Name: "support-intent", TenantID: "t1", Version: 99, URI: "s3://a/99", Format: tiny.FormatTFLite, TaskKind: tiny.TaskClassify}
	require.NoError(t, r.SaveModel(ctx, m4))
	require.Equal(t, 99, m4.Version)
}

func TestMemoryRegistry_saveModel_rejectsEmptyName(t *testing.T) {
	t.Parallel()
	r := tiny.NewMemoryRegistry()
	require.Error(t, r.SaveModel(t.Context(), &tiny.Model{URI: "x"}))
	require.Error(t, r.SaveModel(t.Context(), nil))
}

func TestMemoryRegistry_latest_picksHighestVersion(t *testing.T) {
	t.Parallel()
	r := tiny.NewMemoryRegistry()
	ctx := t.Context()
	for i := 1; i <= 3; i++ {
		require.NoError(t, r.SaveModel(ctx, &tiny.Model{Name: "n", URI: "u", Format: tiny.FormatTFLite}))
	}
	got, err := r.Latest(ctx, "", "n")
	require.NoError(t, err)
	require.Equal(t, 3, got.Version)

	_, err = r.Latest(ctx, "", "missing")
	require.ErrorIs(t, err, tiny.ErrNotFound)
}

func TestMemoryRegistry_getModel_zeroVersionResolvesLatest(t *testing.T) {
	t.Parallel()
	r := tiny.NewMemoryRegistry()
	ctx := t.Context()
	for range 2 {
		require.NoError(t, r.SaveModel(ctx, &tiny.Model{Name: "n", URI: "u", Format: tiny.FormatTFLite}))
	}
	got, err := r.GetModel(ctx, tiny.Ref{Name: "n"})
	require.NoError(t, err)
	require.Equal(t, 2, got.Version)

	// Explicit pinned version.
	got, err = r.GetModel(ctx, tiny.Ref{Name: "n", Version: 1})
	require.NoError(t, err)
	require.Equal(t, 1, got.Version)

	_, err = r.GetModel(ctx, tiny.Ref{Name: "n", Version: 99})
	require.ErrorIs(t, err, tiny.ErrNotFound)
}

func TestMemoryRegistry_list_filtersAndSorts(t *testing.T) {
	t.Parallel()
	r := tiny.NewMemoryRegistry()
	ctx := t.Context()
	require.NoError(t, r.SaveModel(ctx, &tiny.Model{Name: "a", URI: "u", TaskKind: tiny.TaskClassify, Format: tiny.FormatTFLite}))
	require.NoError(t, r.SaveModel(ctx, &tiny.Model{Name: "a", URI: "u", TaskKind: tiny.TaskClassify, Format: tiny.FormatTFLite}))
	require.NoError(t, r.SaveModel(ctx, &tiny.Model{Name: "b", URI: "u", TaskKind: tiny.TaskGenerate, Format: tiny.FormatGGUF}))

	all, err := r.List(ctx, tiny.ListFilter{})
	require.NoError(t, err)
	require.Len(t, all, 3)

	classifiers, err := r.List(ctx, tiny.ListFilter{TaskKind: tiny.TaskClassify})
	require.NoError(t, err)
	require.Len(t, classifiers, 2)

	named, err := r.List(ctx, tiny.ListFilter{Name: "b"})
	require.NoError(t, err)
	require.Len(t, named, 1)
	require.Equal(t, "b", named[0].Name)

	capped, err := r.List(ctx, tiny.ListFilter{Limit: 1})
	require.NoError(t, err)
	require.Len(t, capped, 1)
}

func TestValidateJob_missingFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		j    tiny.Job
	}{
		{"no name", tiny.Job{BaseModel: "b", Dataset: tiny.Dataset{URI: "u", Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify}}},
		{"no base", tiny.Job{Name: "n", Dataset: tiny.Dataset{URI: "u", Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify}}},
		{"no uri", tiny.Job{Name: "n", BaseModel: "b", Dataset: tiny.Dataset{Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify}}},
		{"no modality", tiny.Job{Name: "n", BaseModel: "b", Dataset: tiny.Dataset{URI: "u", TaskKind: tiny.TaskClassify}}},
		{"no task", tiny.Job{Name: "n", BaseModel: "b", Dataset: tiny.Dataset{URI: "u", Modality: tiny.ModalityText}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Error(t, tiny.ValidateJob(tc.j))
		})
	}

	// Happy path.
	require.NoError(t, tiny.ValidateJob(tiny.Job{
		Name:      "n",
		BaseModel: "b",
		Dataset:   tiny.Dataset{URI: "u", Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify},
	}))
}

func TestErrNotFound_isWrappable(t *testing.T) {
	t.Parallel()
	r := tiny.NewMemoryRegistry()
	_, err := r.GetJob(t.Context(), "nope")
	require.True(t, errors.Is(err, tiny.ErrNotFound))
}
