package audio

import (
	"fmt"
	"log/slog"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// loadOptions unmarshals the "media.audio" config prefix into Options.
func loadOptions(cfg *config.Config) (Options, error) {
	var opts Options
	if err := cfg.Unmarshal("media.audio", &opts); err != nil {
		return Options{}, fmt.Errorf("audio: load options: %w", err)
	}
	return opts.withDefaults(), nil
}

// newAnalyzer is the fx constructor for Analyzer.
func newAnalyzer(opts Options, logger *slog.Logger) (Analyzer, error) {
	return NewAnalyzer(opts, logger)
}

// Module provides audio.Analyzer to the fx graph. The analyzer holds no
// goroutines, sockets, or hardware handles, so no lifecycle hook is needed.
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    audio.Module, // provides audio.Analyzer
//	)
//
// Config keys live under the "media.audio" prefix.
var Module = fx.Module(
	"golusoris.media.audio",
	fx.Provide(loadOptions),
	fx.Provide(newAnalyzer),
)
