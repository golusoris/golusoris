package pipeline

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/media/img"
	"github.com/golusoris/golusoris/storage"
)

// bucketSource adapts a storage.Bucket to the narrow [Source] the pipeline
// needs: it drops the Object metadata Bucket.Get also returns. Bucket.Get maps a
// missing key to storage.ErrNotFound, which the handler turns into a 404.
type bucketSource struct{ b storage.Bucket }

func (s bucketSource) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, _, err := s.b.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("pipeline: bucket get: %w", err)
	}
	return rc, nil
}

// loadOptions unmarshals the "media.img.pipeline" config prefix into Options.
func loadOptions(cfg *config.Config) (Options, error) {
	var opts Options
	if err := cfg.Unmarshal("media.img.pipeline", &opts); err != nil {
		return Options{}, fmt.Errorf("pipeline: load options: %w", err)
	}
	return opts, nil
}

// newProcessor constructs the media/img processor. On a runner without libvips
// this returns the CGO stub whose operations yield img.ErrCGORequired; the
// pipeline still builds and serves signing/validation, surfacing a 415 on
// resize. The processor is closed on fx stop.
func newProcessor(lc fx.Lifecycle, log *slog.Logger) (img.Processor, error) {
	proc, err := img.NewProcessor(img.Options{})
	if err != nil {
		// A missing CGO backend is not fatal at wiring time: keep the stub so
		// the rest of the graph (signing, routing) stays available.
		log.Warn("pipeline: image processor unavailable; resize will 415",
			slog.String("err", err.Error()))
		return proc, nil
	}
	lc.Append(fx.Hook{
		OnStop: func(_ context.Context) error {
			proc.Close()
			return nil
		},
	})
	return proc, nil
}

// newPipeline is the fx constructor for *Pipeline.
func newPipeline(opts Options, proc img.Processor, b storage.Bucket, clk clock.Clock, log *slog.Logger) (*Pipeline, error) {
	return New(opts, proc, bucketSource{b: b}, clk, log)
}

// Module provides *Pipeline to the fx graph and a named "media.img.pipeline"
// http.Handler an app mounts (e.g. chi: r.Handle("/img/{signed}", h)).
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    storage.Module,        // provides storage.Bucket
//	    clock.Module,          // provides clock.Clock
//	    pipeline.Module,       // provides *pipeline.Pipeline + the handler
//	)
//
// Config keys live under the "media.img.pipeline" prefix; Secret is required.
var Module = fx.Module(
	"golusoris.media.img.pipeline",
	fx.Provide(loadOptions),
	fx.Provide(newProcessor),
	fx.Provide(newPipeline),
	fx.Provide(
		fx.Annotate(
			func(p *Pipeline) http.Handler { return p.Handler() },
			fx.ResultTags(`name:"media.img.pipeline"`),
		),
	),
)
