package tflite_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/ai/tiny/serve/tflite"
)

// newServer builds a LiteRT sidecar mock. /load records the request and
// returns 200; /classify returns the canned score map.
func newServer(t *testing.T, scores map[string]float32) (*httptest.Server, *loadCapture) {
	t.Helper()
	capture := &loadCapture{}
	mux := http.NewServeMux()
	mux.HandleFunc("/load", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &capture.req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/classify", func(w http.ResponseWriter, _ *http.Request) {
		resp, _ := json.Marshal(map[string]any{"scores": scores})
		_, _ = w.Write(resp)
	})
	return httptest.NewServer(mux), capture
}

type loadCapture struct {
	req struct {
		URI    string   `json:"uri"`
		Labels []string `json:"labels"`
	}
}

func classifyModel() tiny.Model {
	return tiny.Model{
		Name:     "intent",
		URI:      "s3://bucket/models/intent/job-1/model.tflite",
		Format:   tiny.FormatTFLite,
		Modality: tiny.ModalityText,
		TaskKind: tiny.TaskClassify,
		Labels:   []string{"spam", "ham"},
	}
}

func TestPredictor_LoadAndPredict_happyPath(t *testing.T) {
	t.Parallel()
	srv, capture := newServer(t, map[string]float32{"spam": 0.9, "ham": 0.1})
	defer srv.Close()

	p := tflite.NewPredictor(tflite.Options{Endpoint: srv.URL})
	require.NoError(t, p.Load(context.Background(), classifyModel()))
	require.Equal(t, "s3://bucket/models/intent/job-1/model.tflite", capture.req.URI)
	require.Equal(t, []string{"spam", "ham"}, capture.req.Labels)

	got, err := p.Predict(t.Context(), "buy now")
	require.NoError(t, err)
	require.Len(t, got.Labels, 2)
	// Sorted desc by score: spam (0.9) first.
	require.Equal(t, "spam", got.Labels[0].Label)
	require.InDelta(t, 0.9, got.Labels[0].Score, 1e-6)
	require.Equal(t, "ham", got.Labels[1].Label)
	require.NoError(t, p.Close())
}

func TestPredictor_Predict_topK(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t, map[string]float32{"a": 0.5, "b": 0.3, "c": 0.2})
	defer srv.Close()

	p := tflite.NewPredictor(tflite.Options{Endpoint: srv.URL, TopK: 2})
	require.NoError(t, p.Load(t.Context(), classifyModel()))
	got, err := p.Predict(t.Context(), "x")
	require.NoError(t, err)
	require.Len(t, got.Labels, 2)
	require.Equal(t, "a", got.Labels[0].Label)
	require.Equal(t, "b", got.Labels[1].Label)
}

func TestPredictor_Predict_tieBrokenByLabel(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t, map[string]float32{"zebra": 0.5, "apple": 0.5})
	defer srv.Close()

	p := tflite.NewPredictor(tflite.Options{Endpoint: srv.URL})
	require.NoError(t, p.Load(t.Context(), classifyModel()))
	got, err := p.Predict(t.Context(), "x")
	require.NoError(t, err)
	// Equal scores → deterministic order by label asc.
	require.Equal(t, "apple", got.Labels[0].Label)
	require.Equal(t, "zebra", got.Labels[1].Label)
}

func TestPredictor_Load_rejectsWrongTaskKind(t *testing.T) {
	t.Parallel()
	p := tflite.NewPredictor(tflite.Options{Endpoint: "http://127.0.0.1:9"})
	m := classifyModel()
	m.TaskKind = tiny.TaskGenerate
	err := p.Load(t.Context(), m)
	require.ErrorContains(t, err, "TaskKind")
}

func TestPredictor_Load_rejectsWrongFormat(t *testing.T) {
	t.Parallel()
	p := tflite.NewPredictor(tflite.Options{Endpoint: "http://127.0.0.1:9"})
	m := classifyModel()
	m.Format = tiny.FormatGGUF
	err := p.Load(t.Context(), m)
	require.ErrorContains(t, err, "Format")
}

func TestPredictor_Load_rejectsEmptyURI(t *testing.T) {
	t.Parallel()
	p := tflite.NewPredictor(tflite.Options{Endpoint: "http://127.0.0.1:9"})
	m := classifyModel()
	m.URI = ""
	err := p.Load(t.Context(), m)
	require.ErrorContains(t, err, "URI empty")
}

func TestPredictor_Load_sidecarError(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/load", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := tflite.NewPredictor(tflite.Options{Endpoint: srv.URL})
	err := p.Load(t.Context(), classifyModel())
	require.ErrorContains(t, err, "status 404")
}

func TestPredictor_Predict_notLoaded(t *testing.T) {
	t.Parallel()
	p := tflite.NewPredictor(tflite.Options{Endpoint: "http://127.0.0.1:9"})
	_, err := p.Predict(t.Context(), "x")
	require.ErrorContains(t, err, "Load not called")
}

func TestPredictor_Predict_serverError(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/load", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/classify", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := tflite.NewPredictor(tflite.Options{Endpoint: srv.URL})
	require.NoError(t, p.Load(t.Context(), classifyModel()))
	_, err := p.Predict(t.Context(), "x")
	require.ErrorContains(t, err, "status 500")
}

func TestPredictor_Predict_noScores(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/load", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/classify", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := tflite.NewPredictor(tflite.Options{Endpoint: srv.URL})
	require.NoError(t, p.Load(t.Context(), classifyModel()))
	_, err := p.Predict(t.Context(), "x")
	require.ErrorContains(t, err, "no scores")
}

func TestPredictor_Predict_bodyLimitTruncation(t *testing.T) {
	t.Parallel()
	// A big-but-valid JSON body; MaxResponseBytes clips it and the JSON
	// decode fails.
	mux := http.NewServeMux()
	mux.HandleFunc("/load", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/classify", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"scores":{"x":` + strings.Repeat("9", 512) + `}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := tflite.NewPredictor(tflite.Options{Endpoint: srv.URL, MaxResponseBytes: 32})
	require.NoError(t, p.Load(t.Context(), classifyModel()))
	_, err := p.Predict(t.Context(), "x")
	require.ErrorContains(t, err, "decode")
}

func TestPredictor_Close_noop(t *testing.T) {
	t.Parallel()
	p := tflite.NewPredictor(tflite.Options{})
	require.NoError(t, p.Close())
}

func TestNewPredictor_defaults(t *testing.T) {
	t.Parallel()
	// Zero options must not panic and must apply defaults (covered by a
	// Predict against a live mock to exercise the default client).
	srv, _ := newServer(t, map[string]float32{"a": 1})
	defer srv.Close()
	p := tflite.NewPredictor(tflite.Options{Endpoint: srv.URL})
	require.NoError(t, p.Load(t.Context(), classifyModel()))
	got, err := p.Predict(t.Context(), "x")
	require.NoError(t, err)
	require.Equal(t, "a", got.Labels[0].Label)
}
