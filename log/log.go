// Package log is golusoris's logging layer. It returns a configured
// [*slog.Logger] with one of two handlers:
//
//   - tint (colored, human-readable) when stdout is a TTY OR LOG_FORMAT=tint
//   - JSON (slog default) otherwise — production-friendly, structured
//
// Apps inject *slog.Logger via fx and use slog as normal. Pod metadata
// (pod.name, pod.namespace, etc.) from the k8s downward API is added as
// default attributes when present in the environment.
package log

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
	"go.uber.org/fx"
)

// Format selects the handler.
type Format string

const (
	// FormatAuto picks tint when stdout is a TTY, JSON otherwise.
	FormatAuto Format = "auto"
	FormatTint Format = "tint"
	FormatJSON Format = "json"
)

// Options configures the logger. Zero value is usable.
type Options struct {
	// Format selects the handler. Default FormatAuto.
	Format Format
	// Level is the minimum level. Default slog.LevelInfo.
	Level slog.Level
	// Output is where logs are written. Default os.Stderr.
	Output io.Writer
	// AddSource adds source-file/line attributes. Default false (perf cost).
	AddSource bool
}

// New builds a logger per opts. Pod metadata from POD_NAME, POD_NAMESPACE,
// POD_IP, NODE_NAME, SERVICE_ACCOUNT env vars (set by k8s downward API) is
// attached as default attributes.
func New(opts Options) *slog.Logger {
	if opts.Output == nil {
		opts.Output = os.Stderr
	}
	if opts.Format == "" {
		opts.Format = FormatAuto
	}

	var h slog.Handler
	useTint := opts.Format == FormatTint ||
		(opts.Format == FormatAuto && isTTY(opts.Output))

	if useTint {
		h = tint.NewHandler(opts.Output, &tint.Options{
			Level:     opts.Level,
			AddSource: opts.AddSource,
		})
	} else {
		h = slog.NewJSONHandler(opts.Output, &slog.HandlerOptions{
			Level:     opts.Level,
			AddSource: opts.AddSource,
		})
	}

	logger := slog.New(h)

	// Default pod-info attrs if present.
	if attrs := podInfoAttrs(); len(attrs) > 0 {
		logger = logger.With(attrs...)
	}
	return logger
}

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

func podInfoAttrs() []any {
	attrs := []any{}
	for envVar, key := range map[string]string{
		"POD_NAME":        "k8s.pod.name",
		"POD_NAMESPACE":   "k8s.namespace",
		"POD_IP":          "k8s.pod.ip",
		"NODE_NAME":       "k8s.node.name",
		"SERVICE_ACCOUNT": "k8s.service_account",
	} {
		if v := os.Getenv(envVar); v != "" {
			attrs = append(attrs, slog.String(key, v))
		}
	}
	return attrs
}

// LevelFromString parses log levels case-insensitively.
// Unknown values return slog.LevelInfo with ok=false.
func LevelFromString(s string) (slog.Level, bool) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, true
	case "info", "":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error", "err":
		return slog.LevelError, true
	}
	return slog.LevelInfo, false
}

// Module provides a *slog.Logger driven by environment vars LOG_LEVEL and
// LOG_FORMAT (tint/json/auto). Sets it as the global slog default for any
// code that hasn't migrated yet.
var Module = fx.Module("golusoris.log",
	fx.Provide(func() Options {
		level, _ := LevelFromString(os.Getenv("LOG_LEVEL"))
		return Options{
			Format: Format(strings.ToLower(os.Getenv("LOG_FORMAT"))),
			Level:  level,
		}
	}),
	fx.Provide(func(opts Options) *slog.Logger {
		l := New(opts)
		slog.SetDefault(l)
		return l
	}),
)
