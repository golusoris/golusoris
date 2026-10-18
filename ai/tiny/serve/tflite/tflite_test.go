// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tflite_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/ai/tiny/serve/tflite"
	httpclient "github.com/golusoris/golusoris/httpx/client"
)

// newServer builds a LiteRT sidecar mock. /load records the request and
// returns 200; /classify records the request and returns the canned score map.
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
	mux.HandleFunc("/classify", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capture.classifyReq)
		resp, _ := json.Marshal(map[string]any{"scores": scores})
		_, _ = w.Write(resp)
	})
	return httptest.NewServer(mux), capture
}

type loadCapture struct {
	req struct {
		URI    string   `json:"uri"`
		SHA256 string   `json:"sha256"`
		Labels []string `json:"labels"`
	}
	classifyReq struct {
		Image string `json:"image"`
	}
}

const testSHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func classifyModel() tiny.Model {
	return tiny.Model{
		Name:     "pets",
		URI:      "https://storage.example.com/models/pets/v1/model.tflite",
		Format:   tiny.FormatTFLite,
		Modality: tiny.ModalityImage,
		TaskKind: tiny.TaskClassify,
		Labels:   []string{"cat", "dog"},
		Metadata: map[string]string{
			"sha256": testSHA256,
		},
	}
}

func TestPredictor_LoadAndPredict_happyPath(t *testing.T) {
	t.Parallel()
	srv, capture := newServer(t, map[string]float32{"cat": 0.9, "dog": 0.1})
	defer srv.Close()

	p := tflite.NewPredictor(tflite.Options{Endpoint: srv.URL})
	require.NoError(t, p.Load(context.Background(), classifyModel()))
	require.Equal(t, "https://storage.example.com/models/pets/v1/model.tflite", capture.req.URI)
	require.Equal(t, testSHA256, capture.req.SHA256)
	require.Equal(t, []string{"cat", "dog"}, capture.req.Labels)

	fakeBytes := []byte("image-bytes-png")
	got, err := p.Predict(t.Context(), fakeBytes)
	require.NoError(t, err)
	require.Equal(t, base64.StdEncoding.EncodeToString(fakeBytes), capture.classifyReq.Image)
	require.Len(t, got.Labels, 2)
	// Sorted desc by score: cat (0.9) first.
	require.Equal(t, "cat", got.Labels[0].Label)
	require.InDelta(t, 0.9, got.Labels[0].Score, 1e-6)
	require.Equal(t, "dog", got.Labels[1].Label)
	require.NoError(t, p.Close())
}

func TestPredictor_Predict_acceptsBase64String(t *testing.T) {
	t.Parallel()
	srv, capture := newServer(t, map[string]float32{"cat": 0.8, "dog": 0.2})
	defer srv.Close()

	p := tflite.NewPredictor(tflite.Options{Endpoint: srv.URL})
	require.NoError(t, p.Load(t.Context(), classifyModel()))

	rawB64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAAAAAA6fptVAAAACklEQVR4nGNiAAAABgADNjd8qAAAAABJRU5ErkJggg=="
	got, err := p.Predict(t.Context(), rawB64)
	require.NoError(t, err)
	require.Equal(t, rawB64, capture.classifyReq.Image)
	require.Len(t, got.Labels, 2)
}

func TestPredictor_Predict_rejectsUnsupportedInputType(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t, map[string]float32{"cat": 0.5, "dog": 0.5})
	defer srv.Close()

	p := tflite.NewPredictor(tflite.Options{Endpoint: srv.URL})
	require.NoError(t, p.Load(t.Context(), classifyModel()))

	_, err := p.Predict(t.Context(), 12345)
	require.ErrorContains(t, err, "unsupported input type")

	_, err = p.Predict(t.Context(), []byte{})
	require.ErrorContains(t, err, "empty image bytes")

	_, err = p.Predict(t.Context(), "   ")
	require.ErrorContains(t, err, "empty image string")
}

func TestPredictor_Predict_topK(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t, map[string]float32{"a": 0.5, "b": 0.3, "c": 0.2})
	defer srv.Close()

	p := tflite.NewPredictor(tflite.Options{Endpoint: srv.URL, TopK: 2})
	model := classifyModel()
	model.Labels = []string{"a", "b", "c"}
	require.NoError(t, p.Load(t.Context(), model))
	got, err := p.Predict(t.Context(), []byte("sample"))
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
	model := classifyModel()
	model.Labels = []string{"apple", "zebra"}
	require.NoError(t, p.Load(t.Context(), model))
	got, err := p.Predict(t.Context(), []byte("sample"))
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

func TestPredictor_Load_rejectsNonImageModality(t *testing.T) {
	t.Parallel()
	p := tflite.NewPredictor(tflite.Options{Endpoint: "http://127.0.0.1:9"})
	m := classifyModel()
	m.Modality = tiny.ModalityText
	err := p.Load(t.Context(), m)
	require.ErrorContains(t, err, "Modality")

	m.Modality = tiny.ModalityAudio
	err = p.Load(t.Context(), m)
	require.ErrorContains(t, err, "Modality")
}

func TestPredictor_Load_rejectsEmptyURI(t *testing.T) {
	t.Parallel()
	p := tflite.NewPredictor(tflite.Options{Endpoint: "http://127.0.0.1:9"})
	m := classifyModel()
	m.URI = ""
	err := p.Load(t.Context(), m)
	require.ErrorContains(t, err, "URI empty")
}

func TestPredictor_Load_rejectsMissingSha256(t *testing.T) {
	t.Parallel()
	p := tflite.NewPredictor(tflite.Options{Endpoint: "http://127.0.0.1:9"})
	m := classifyModel()
	m.Metadata = nil
	err := p.Load(t.Context(), m)
	require.ErrorContains(t, err, "sha256 empty")

	m.Metadata = map[string]string{"sha256": ""}
	err = p.Load(t.Context(), m)
	require.ErrorContains(t, err, "sha256 empty")
}

func TestPredictor_Load_rejectsInvalidClassifierMetadata(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*tiny.Model)
		want   string
	}{
		{name: "unknown modality", mutate: func(m *tiny.Model) { m.Modality = tiny.Modality("video") }, want: "Modality"},
		{name: "missing labels", mutate: func(m *tiny.Model) { m.Labels = nil }, want: "at least two"},
		{name: "blank label", mutate: func(m *tiny.Model) { m.Labels = []string{"cat", " "} }, want: "non-empty"},
		{name: "duplicate label", mutate: func(m *tiny.Model) { m.Labels = []string{"cat", "cat"} }, want: "unique"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			model := classifyModel()
			tc.mutate(&model)
			predictor := tflite.NewPredictor(tflite.Options{Endpoint: "http://127.0.0.1:9"})
			err := predictor.Load(t.Context(), model)
			require.ErrorContains(t, err, tc.want)
		})
	}
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
	_, err := p.Predict(t.Context(), []byte("sample"))
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
	_, err := p.Predict(t.Context(), []byte("sample"))
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
	_, err := p.Predict(t.Context(), []byte("sample"))
	require.ErrorContains(t, err, "no scores")
}

func TestPredictor_Predict_rejectsScoresOutsideLoadedModel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		scores map[string]float32
		want   string
	}{
		{name: "missing label", scores: map[string]float32{"cat": 1}, want: "label count"},
		{name: "foreign label", scores: map[string]float32{"cat": 0.5, "other": 0.5}, want: "not loaded"},
		{name: "negative probability", scores: map[string]float32{"cat": -0.1, "dog": 1}, want: "outside [0,1]"},
		{name: "probability above one", scores: map[string]float32{"cat": 1.1, "dog": 0}, want: "outside [0,1]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t, tc.scores)
			defer srv.Close()
			predictor := tflite.NewPredictor(tflite.Options{Endpoint: srv.URL})
			require.NoError(t, predictor.Load(t.Context(), classifyModel()))
			_, err := predictor.Predict(t.Context(), []byte("sample"))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestPredictor_Predict_rejectsOversizedBody(t *testing.T) {
	t.Parallel()
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
	_, err := p.Predict(t.Context(), []byte("sample"))
	require.ErrorIs(t, err, httpclient.ErrBodyTooLarge)
}

func TestPredictor_Close_noop(t *testing.T) {
	t.Parallel()
	p := tflite.NewPredictor(tflite.Options{})
	require.NoError(t, p.Close())
}

func TestNewPredictor_defaults(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t, map[string]float32{"a": 1, "b": 0})
	defer srv.Close()
	p := tflite.NewPredictor(tflite.Options{Endpoint: srv.URL})
	model := classifyModel()
	model.Labels = []string{"a", "b"}
	require.NoError(t, p.Load(t.Context(), model))
	got, err := p.Predict(t.Context(), []byte("sample"))
	require.NoError(t, err)
	require.Equal(t, "a", got.Labels[0].Label)
}

var errBoom = errors.New("boom")

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type closeFailBody struct {
	io.Reader
	err error
}

func (b *closeFailBody) Close() error { return b.err }

func TestPredictor_Load_closeErrorReturnsErr(t *testing.T) {
	t.Parallel()
	hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       &closeFailBody{Reader: strings.NewReader(""), err: errBoom},
		}, nil
	})}
	p := tflite.NewPredictor(tflite.Options{Endpoint: "http://litert.invalid", HTTPClient: hc})
	err := p.Load(t.Context(), classifyModel())
	require.ErrorIs(t, err, errBoom)
	require.Contains(t, err.Error(), "close /load body")
}
