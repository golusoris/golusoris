// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tiny_test

import (
	"encoding/json"
	"errors"
	"strings"
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
	require.NoError(t, r.SaveJob(t.Context(), &j))
	require.NotEmpty(t, j.ID)
	generated, err := r.GetJob(t.Context(), j.ID)
	require.NoError(t, err)
	require.Equal(t, j.ID, generated.ID)

	j2 := tiny.Job{ID: "fixed-id", Name: "x", BaseModel: "b", Dataset: tiny.Dataset{URI: "u", Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify}}
	require.NoError(t, r.SaveJob(t.Context(), &j2))
	got, err := r.GetJob(t.Context(), "fixed-id")
	require.NoError(t, err)
	require.Equal(t, "fixed-id", got.ID)
	require.WithinDuration(t, clk.Now().UTC(), got.CreatedAt, time.Second)
}

func TestMemoryRegistry_snapshotsMutableFields(t *testing.T) {
	t.Parallel()
	r := tiny.NewMemoryRegistry()
	job := tiny.Job{
		ID: "job-1", Name: "classifier", BaseModel: "base",
		Dataset: tiny.Dataset{
			URI: "file:///dataset", Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify,
			SchemaHint: map[string]any{"nested": map[string]any{"value": "original"}},
		},
		Hyperparams: map[string]any{"nested": map[string]any{"value": "original"}},
		Tags:        map[string]string{"owner": "original"},
	}
	require.NoError(t, r.SaveJob(t.Context(), &job))
	job.Dataset.SchemaHint["nested"].(map[string]any)["value"] = "mutated"
	job.Hyperparams["nested"].(map[string]any)["value"] = "mutated"
	job.Tags["owner"] = "mutated"

	stored, err := r.GetJob(t.Context(), job.ID)
	require.NoError(t, err)
	require.Equal(t, "original", stored.Dataset.SchemaHint["nested"].(map[string]any)["value"])
	require.Equal(t, "original", stored.Hyperparams["nested"].(map[string]any)["value"])
	require.Equal(t, "original", stored.Tags["owner"])
	stored.Tags["owner"] = "second mutation"
	again, err := r.GetJob(t.Context(), job.ID)
	require.NoError(t, err)
	require.Equal(t, "original", again.Tags["owner"])

	model := &tiny.Model{
		Name: "classifier", Labels: []string{"a", "b"},
		Metrics: map[string]float64{"loss": 1}, Metadata: map[string]string{"owner": "original"},
	}
	require.NoError(t, r.SaveModel(t.Context(), model))
	model.Labels[0] = "changed"
	model.Metrics["loss"] = 2
	model.Metadata["owner"] = "changed"
	storedModel, err := r.GetModel(t.Context(), tiny.Ref{Name: "classifier", Version: 1})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, storedModel.Labels)
	require.Equal(t, float64(1), storedModel.Metrics["loss"])
	require.Equal(t, "original", storedModel.Metadata["owner"])
}

func TestMemoryRegistry_SaveJobRejectsNonCanonicalMetadata(t *testing.T) {
	t.Parallel()
	for name, unsupported := range map[string]any{
		"int":     1,
		"bytes":   []byte("value"),
		"time":    time.Unix(1, 0),
		"strings": []string{"value"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			registry := tiny.NewMemoryRegistry()
			job := tiny.Job{
				ID: "job-1", Name: "classifier", BaseModel: "base",
				Dataset: tiny.Dataset{
					URI: "file:///dataset", Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify,
				},
				Hyperparams: map[string]any{"unsupported": unsupported},
			}
			require.ErrorContains(t, registry.SaveJob(t.Context(), &job), "non-canonical JSON type")
		})
	}
}

func TestMemoryRegistry_SaveJobRejectsSerializedFieldOverflow(t *testing.T) {
	t.Parallel()
	registry := tiny.NewMemoryRegistry()
	job := jobAtSerializedByteLimit(t)
	job.Dataset.URI += "x"
	require.ErrorContains(t, registry.SaveJob(t.Context(), &job), "exceed 1048576 encoded bytes")
}

func TestMemoryRegistry_SaveJobAcceptsSerializedFieldsAtExactLimit(t *testing.T) {
	t.Parallel()
	registry := tiny.NewMemoryRegistry()
	job := jobAtSerializedByteLimit(t)
	require.NoError(t, registry.SaveJob(t.Context(), &job))
}

func TestMemoryRegistry_SaveJobCountsFixedNotationFloatBytes(t *testing.T) {
	t.Parallel()
	const floatCount = 50_000
	values := make([]any, floatCount)
	for index := range floatCount {
		values[index] = float64(1e20)
	}
	registry := tiny.NewMemoryRegistry()
	job := tiny.Job{
		ID: "job-1", Name: "classifier", BaseModel: "base",
		Dataset: tiny.Dataset{
			URI: "file:///dataset", Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify,
		},
		Hyperparams: map[string]any{"values": values},
	}
	require.ErrorContains(t, registry.SaveJob(t.Context(), &job), "exceed 1048576 encoded bytes")
}

func TestMemoryRegistry_SaveJobAcceptsExactValueLimit(t *testing.T) {
	t.Parallel()
	job := jobAtJSONValueLimit()
	require.NoError(t, tiny.NewMemoryRegistry().SaveJob(t.Context(), &job))
}

func TestMemoryRegistry_SaveModelAcceptsJSONAtExactByteLimit(t *testing.T) {
	t.Parallel()
	model := modelAtJSONByteLimit(t, "model-byte-limit")
	require.NoError(t, tiny.NewMemoryRegistry().SaveModel(t.Context(), model))
}

func TestMemoryRegistry_SaveModelAcceptsExactValueLimit(t *testing.T) {
	t.Parallel()
	model := modelWithMetadataValues("model-value-limit", 100_000)
	require.NoError(t, tiny.NewMemoryRegistry().SaveModel(t.Context(), model))
}

func TestMemoryRegistry_SaveJobPreservesCreatedAtOnUpdate(t *testing.T) {
	t.Parallel()
	clock := clockwork.NewFakeClockAt(time.Date(2026, 9, 20, 1, 0, 0, 0, time.UTC))
	registry := tiny.NewMemoryRegistryWithClock(clock)
	job := tiny.Job{
		ID: "job-1", Name: "first", BaseModel: "base",
		Dataset: tiny.Dataset{URI: "file:///dataset", Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify},
	}
	require.NoError(t, registry.SaveJob(t.Context(), &job))
	createdAt := job.CreatedAt
	clock.Advance(time.Hour)
	update := job
	update.Name = "second"
	update.CreatedAt = time.Time{}
	require.NoError(t, registry.SaveJob(t.Context(), &update))
	require.Equal(t, createdAt, update.CreatedAt)
	stored, err := registry.GetJob(t.Context(), job.ID)
	require.NoError(t, err)
	require.Equal(t, "second", stored.Name)
	require.Equal(t, createdAt, stored.CreatedAt)
}

func TestMemoryRegistryWithClockDefaultsNilClocks(t *testing.T) {
	t.Parallel()
	var typedNil *typedNilClock
	for name, clock := range map[string]clockwork.Clock{"nil": nil, "typed nil": typedNil} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			registry := tiny.NewMemoryRegistryWithClock(clock)
			job := tiny.Job{
				ID: "job-1", Name: "classifier", BaseModel: "base",
				Dataset: tiny.Dataset{
					URI: "file:///dataset", Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify,
				},
			}
			require.NoError(t, registry.SaveJob(t.Context(), &job))
			require.False(t, job.CreatedAt.IsZero())
		})
	}
}

func jobAtSerializedByteLimit(t *testing.T) tiny.Job {
	t.Helper()
	job := tiny.Job{
		ID: "job-byte-limit", Name: "classifier", BaseModel: "base",
		Dataset: tiny.Dataset{
			Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify,
		},
	}
	base := serializedJobFieldBytes(t, job)
	require.Less(t, base, 1<<20)
	job.Dataset.URI = strings.Repeat("x", (1<<20)-base)
	require.Equal(t, 1<<20, serializedJobFieldBytes(t, job))
	return job
}

func serializedJobFieldBytes(t *testing.T, job tiny.Job) int {
	t.Helper()
	values := []any{
		job.ID, job.Name, job.TenantID, job.BaseModel,
		job.Dataset.ID, job.Dataset.URI, job.Dataset.Format,
		string(job.Dataset.Modality), string(job.Dataset.TaskKind),
		job.Dataset.SchemaHint, job.Hyperparams, job.Tags,
	}
	total := 0
	for _, value := range values {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		total += len(encoded)
	}
	return total
}

func modelAtJSONByteLimit(t *testing.T, name string) *tiny.Model {
	t.Helper()
	model := &tiny.Model{
		Name: name, Labels: []string{}, Metrics: map[string]float64{},
		Metadata: map[string]string{"payload": ""},
	}
	base := modelJSONFieldBytes(t, model)
	require.Less(t, base, 1<<20)
	model.Metadata["payload"] = strings.Repeat("x", (1<<20)-base)
	require.Equal(t, 1<<20, modelJSONFieldBytes(t, model))
	return model
}

func modelJSONFieldBytes(t *testing.T, model *tiny.Model) int {
	t.Helper()
	total := 0
	for _, value := range []any{model.Labels, model.Metrics, model.Metadata} {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		total += len(encoded)
	}
	return total
}

func modelWithMetadataValues(name string, count int) *tiny.Model {
	metadata := make(map[string]string, count)
	for index := range count {
		metadata[string(rune(0x20000+index))] = ""
	}
	return &tiny.Model{Name: name, Metadata: metadata}
}

func jobAtJSONValueLimit() tiny.Job {
	return tiny.Job{
		ID: "job-value-limit", Name: "classifier", BaseModel: "base",
		Dataset: tiny.Dataset{
			URI: "file:///dataset", Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify,
		},
		Hyperparams: map[string]any{"values": make([]any, 99_999)},
	}
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

func TestValidateClassifierLabels(t *testing.T) {
	t.Parallel()
	require.NoError(t, tiny.ValidateClassifierLabels([]string{"cat", "dog"}))
	tests := []struct {
		name   string
		labels []string
		want   string
	}{
		{name: "missing", want: "at least two"},
		{name: "one", labels: []string{"cat"}, want: "at least two"},
		{name: "blank", labels: []string{"cat", "\t"}, want: "non-empty"},
		{name: "duplicate", labels: []string{"cat", "cat"}, want: "unique"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.ErrorContains(t, tiny.ValidateClassifierLabels(tc.labels), tc.want)
		})
	}
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
	require.NoError(t, r.SaveModel(ctx, &tiny.Model{Name: "a", TenantID: "acme", URI: "u", TaskKind: tiny.TaskClassify, Format: tiny.FormatTFLite}))
	require.NoError(t, r.SaveModel(ctx, &tiny.Model{Name: "a", TenantID: "acme", URI: "u", TaskKind: tiny.TaskClassify, Format: tiny.FormatTFLite}))
	require.NoError(t, r.SaveModel(ctx, &tiny.Model{Name: "b", TenantID: "globex", URI: "u", TaskKind: tiny.TaskGenerate, Format: tiny.FormatGGUF}))

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

	// Boundary: TenantID filter matches its tenant only.
	acme, err := r.List(ctx, tiny.ListFilter{TenantID: "acme"})
	require.NoError(t, err)
	require.Len(t, acme, 2)

	// Negative: an unknown tenant matches nothing.
	none, err := r.List(ctx, tiny.ListFilter{TenantID: "nope"})
	require.NoError(t, err)
	require.Empty(t, none)

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
		ID:        "job-1",
		Name:      "n",
		BaseModel: "b",
		Dataset:   tiny.Dataset{URI: "u", Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify},
	}))
	for _, invalid := range []tiny.Job{
		{ID: "", Name: "n", BaseModel: "b", Dataset: tiny.Dataset{URI: "u", Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify}},
		{ID: "../job", Name: "n", BaseModel: "b", Dataset: tiny.Dataset{URI: "u", Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify}},
		{ID: "job", Name: "../name", BaseModel: "b", Dataset: tiny.Dataset{URI: "u", Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify}},
		{ID: "job", Name: "n", TenantID: "../tenant", BaseModel: "b", Dataset: tiny.Dataset{URI: "u", Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify}},
	} {
		require.Error(t, tiny.ValidateJob(invalid))
	}
}

func TestErrNotFound_isWrappable(t *testing.T) {
	t.Parallel()
	r := tiny.NewMemoryRegistry()
	_, err := r.GetJob(t.Context(), "nope")
	require.True(t, errors.Is(err, tiny.ErrNotFound))
}
