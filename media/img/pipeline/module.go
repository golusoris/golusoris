// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pipeline

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/media/img"
	"github.com/golusoris/golusoris/storage"
)

// bucketSource adapts a storage.Bucket to the narrow [Source] the pipeline
// needs: it drops the Object metadata Bucket.Get also returns. Bucket.Get maps a
// missing key to storage.ErrNotFound, which the handler turns into a 404.
type bucketSource struct{ b storage.Bucket }

var _ Source = bucketSource{}

func (s bucketSource) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, _, err := s.b.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("pipeline: bucket get: %w", err)
	}
	return rc, nil
}

// SourceFromBucket adapts bucket to the narrow [Source] accepted by [New]. It
// returns nil for a nil bucket so New reports [ErrInvalidDependency].
func SourceFromBucket(bucket storage.Bucket) Source {
	if validate.IsNil(bucket) {
		return nil
	}
	return bucketSource{b: bucket}
}

// loadOptions unmarshals the "media.img.pipeline" config prefix into Options.
func loadOptions(cfg *config.Config) (Options, error) {
	var opts Options
	if err := cfg.Unmarshal("media.img.pipeline", &opts); err != nil {
		return Options{}, fmt.Errorf("pipeline: load options: %w", err)
	}
	return opts, nil
}

// newPipeline is the fx constructor for *Pipeline.
func newPipeline(opts Options, proc img.Processor, b storage.Bucket, clk clock.Clock, log *slog.Logger) (*Pipeline, error) {
	return New(opts, proc, SourceFromBucket(b), clk, log)
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
// Config keys live under the "media.img.pipeline" prefix; Secret and an
// application-provided img.Processor are required. The provider that owns the
// processor also owns its lifecycle.
var Module = fx.Module(
	"golusoris.media.img.pipeline",
	fx.Provide(loadOptions),
	fx.Provide(newPipeline),
	fx.Provide(
		fx.Annotate(
			func(p *Pipeline) http.Handler { return p.Handler() },
			fx.ResultTags(`name:"media.img.pipeline"`),
		),
	),
)
