package log_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/golusoris/golusoris/log"
)

func TestJSONFormatProducesJSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l := log.New(log.Options{Format: log.FormatJSON, Level: slog.LevelInfo, Output: &buf})
	l.Info("hello", "k", "v")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("output not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if rec["msg"] != "hello" || rec["k"] != "v" {
		t.Errorf("unexpected record: %+v", rec)
	}
}

func TestPodInfoAttrs(t *testing.T) {
	t.Setenv("POD_NAME", "test-pod-1")
	t.Setenv("POD_NAMESPACE", "default")

	var buf bytes.Buffer
	l := log.New(log.Options{Format: log.FormatJSON, Output: &buf})
	l.Info("test")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if rec["k8s.pod.name"] != "test-pod-1" {
		t.Errorf("missing pod.name attr: %+v", rec)
	}
	if rec["k8s.namespace"] != "default" {
		t.Errorf("missing namespace attr: %+v", rec)
	}
}

func TestLevelFromString(t *testing.T) {
	t.Parallel()
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"INFO":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"WARNING": slog.LevelWarn,
		"error":   slog.LevelError,
		"":        slog.LevelInfo,
	}
	for in, want := range cases {
		if got, _ := log.LevelFromString(in); got != want {
			t.Errorf("LevelFromString(%q) = %v, want %v", in, got, want)
		}
	}
}
