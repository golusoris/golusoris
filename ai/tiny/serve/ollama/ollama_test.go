package ollama_test

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
	"github.com/golusoris/golusoris/ai/tiny/serve/ollama"
)

// newServer builds an ollama mock. show returns 200 for `knownTag`, 404
// otherwise. /api/generate echoes a canned response.
func newServer(t *testing.T, knownTag, cannedResponse string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/show", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.Name != knownTag {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"details":{}}`))
	})
	mux.HandleFunc("/api/generate", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		resp, _ := json.Marshal(map[string]any{"response": cannedResponse, "model": req.Model})
		_, _ = w.Write(resp)
	})
	return httptest.NewServer(mux)
}

func TestPredictor_LoadAndPredict_happyPath(t *testing.T) {
	t.Parallel()
	srv := newServer(t, "intent-v1", "hello world")
	defer srv.Close()

	p := ollama.NewPredictor(ollama.Options{Endpoint: srv.URL})
	ctx := context.Background()
	err := p.Load(ctx, tiny.Model{
		Name:     "intent-v1",
		Modality: tiny.ModalityText,
		TaskKind: tiny.TaskGenerate,
	})
	require.NoError(t, err)

	got, err := p.Predict(ctx, "say hi")
	require.NoError(t, err)
	require.Equal(t, "hello world", got.Text)
	require.NoError(t, p.Close())
}

func TestPredictor_TagOverride(t *testing.T) {
	t.Parallel()
	srv := newServer(t, "override-tag", "ok")
	defer srv.Close()

	p := ollama.NewPredictor(ollama.Options{Endpoint: srv.URL, TagOverride: "override-tag"})
	err := p.Load(t.Context(), tiny.Model{
		Name:     "logical-name",
		Modality: tiny.ModalityText,
		TaskKind: tiny.TaskGenerate,
	})
	require.NoError(t, err)
}

func TestPredictor_Load_missingModelIs404(t *testing.T) {
	t.Parallel()
	srv := newServer(t, "some-other-model", "")
	defer srv.Close()

	p := ollama.NewPredictor(ollama.Options{Endpoint: srv.URL})
	err := p.Load(t.Context(), tiny.Model{
		Name:     "does-not-exist",
		Modality: tiny.ModalityText,
		TaskKind: tiny.TaskGenerate,
	})
	require.ErrorContains(t, err, "not registered")
}

func TestPredictor_Load_rejectsWrongTaskKind(t *testing.T) {
	t.Parallel()
	p := ollama.NewPredictor(ollama.Options{Endpoint: "http://127.0.0.1:9"})
	err := p.Load(t.Context(), tiny.Model{
		Name:     "x",
		Modality: tiny.ModalityText,
		TaskKind: tiny.TaskClassify,
	})
	require.ErrorContains(t, err, "TaskKind")
}

func TestPredictor_Load_rejectsWrongModality(t *testing.T) {
	t.Parallel()
	p := ollama.NewPredictor(ollama.Options{Endpoint: "http://127.0.0.1:9"})
	err := p.Load(t.Context(), tiny.Model{
		Name:     "x",
		Modality: tiny.ModalityImage,
		TaskKind: tiny.TaskGenerate,
	})
	require.ErrorContains(t, err, "Modality")
}

func TestPredictor_Load_rejectsEmptyName(t *testing.T) {
	t.Parallel()
	p := ollama.NewPredictor(ollama.Options{Endpoint: "http://127.0.0.1:9"})
	err := p.Load(t.Context(), tiny.Model{
		Modality: tiny.ModalityText,
		TaskKind: tiny.TaskGenerate,
	})
	require.ErrorContains(t, err, "empty")
}

func TestPredictor_Predict_notLoaded(t *testing.T) {
	t.Parallel()
	p := ollama.NewPredictor(ollama.Options{Endpoint: "http://127.0.0.1:9"})
	_, err := p.Predict(t.Context(), "x")
	require.ErrorContains(t, err, "Load not called")
}

func TestPredictor_Predict_nonStringInput(t *testing.T) {
	t.Parallel()
	srv := newServer(t, "m1", "r")
	defer srv.Close()
	p := ollama.NewPredictor(ollama.Options{Endpoint: srv.URL})
	require.NoError(t, p.Load(t.Context(), tiny.Model{
		Name:     "m1",
		Modality: tiny.ModalityText,
		TaskKind: tiny.TaskGenerate,
	}))
	_, err := p.Predict(t.Context(), 42)
	require.ErrorContains(t, err, "input must be string")
}

func TestPredictor_Predict_serverError(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/show", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/api/generate", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := ollama.NewPredictor(ollama.Options{Endpoint: srv.URL})
	require.NoError(t, p.Load(t.Context(), tiny.Model{
		Name:     "m1",
		Modality: tiny.ModalityText,
		TaskKind: tiny.TaskGenerate,
	}))
	_, err := p.Predict(t.Context(), "hi")
	require.ErrorContains(t, err, "status 500")
}

func TestPredictor_Predict_bodyLimitTruncation(t *testing.T) {
	t.Parallel()
	// Send a big-but-valid JSON body; MaxResponseBytes=64 clips it and
	// JSON decode should fail with a decode error.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/show", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/api/generate", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"response":"` + strings.Repeat("x", 512) + `"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := ollama.NewPredictor(ollama.Options{Endpoint: srv.URL, MaxResponseBytes: 64})
	require.NoError(t, p.Load(t.Context(), tiny.Model{
		Name:     "m",
		Modality: tiny.ModalityText,
		TaskKind: tiny.TaskGenerate,
	}))
	_, err := p.Predict(t.Context(), "hi")
	require.ErrorContains(t, err, "decode response")
}

func TestPredictor_Close_noop(t *testing.T) {
	t.Parallel()
	p := ollama.NewPredictor(ollama.Options{})
	require.NoError(t, p.Close())
}
