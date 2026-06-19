// Package tus mounts a tus 1.0 resumable-upload endpoint backed by a
// storage.Bucket. It wraps github.com/tus/tusd/v2/pkg/handler with a
// Bucket-backed DataStore: chunks land in an append-capable scratch area
// during the upload and are streamed into the Bucket once on FinishUpload.
//
// Opt-in module (config "storage.tus.enabled"). The framework never grabs the
// router — the app mounts the handler so its own middleware (auth, ratelimit)
// wraps the tus routes:
//
//	fx.New(
//	    golusoris.Core,
//	    storage.Module,
//	    tus.Module,
//	    fx.Invoke(func(r chi.Router, h *tus.Handler) { h.Mount(r) }),
//	)
//
// Downstream wiring (e.g. enqueue a river job) hooks completion:
//
//	h.OnComplete(func(ctx context.Context, c tus.CompletedUpload) error {
//	    return jobs.Enqueue(ctx, scanJob{Key: c.Key})
//	})
//
// Scratch is node-local ("local"): a resumed PATCH must reach the same replica.
// Run single-replica or with sticky sessions until a distributed scratch lands.
// CORS for tus's custom headers is delegated to httpx/cors, not configured here.
package tus

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	tusd "github.com/tus/tusd/v2/pkg/handler"
	"github.com/tus/tusd/v2/pkg/memorylocker"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/storage"
)

// CompletedUpload is emitted once an upload finishes and its bytes are in the
// Bucket. It drives downstream wiring via OnComplete callbacks.
type CompletedUpload struct {
	ID       string            // tus upload id
	Key      string            // final storage.Bucket key (KeyFunc output)
	Size     int64             // persisted object size in bytes
	MetaData map[string]string // tus Upload-Metadata (filename, filetype, ...)
}

// Handler is the mountable tus component. It is an http.Handler covering the
// whole tus sub-tree and also exposes Mount for explicit chi wiring.
type Handler struct {
	routed    *tusd.Handler
	unrouted  *tusd.UnroutedHandler
	store     *bucketStore
	scratch   scratchStore
	opts      Options
	log       *slog.Logger
	clk       clock.Clock
	mu        sync.RWMutex
	callbacks []completionFn

	drainCancel context.CancelFunc
	drainDone   chan struct{}
}

// BasePath returns the URL prefix the handler routes under (e.g. "/files/").
func (h *Handler) BasePath() string { return h.opts.BasePath }

// ServeHTTP lets the Handler be mounted directly: r.Mount(h.BasePath(), h).
// tusd's routed mux matches on the path relative to BasePath, so the public
// prefix is stripped before delegating.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.stripped().ServeHTTP(w, r)
}

// Mount registers the tus routes on r under the handler's BasePath via chi, so
// app middleware (auth, ratelimit) still wraps them. The routed tusd handler
// sees paths relative to BasePath; tusd keeps BasePath for Location headers.
func (h *Handler) Mount(r chi.Router) {
	base := strings.TrimSuffix(h.opts.BasePath, "/")
	stripped := h.stripped()
	r.Handle(base, stripped)
	r.Handle(base+"/*", stripped)
}

// stripped returns the routed tusd handler with the public BasePath removed
// from the request path, as tusd's NewHandler mux expects.
func (h *Handler) stripped() http.Handler {
	base := strings.TrimSuffix(h.opts.BasePath, "/")
	return http.StripPrefix(base, h.routed)
}

// OnComplete registers a callback fired after FinishUpload persists to the
// Bucket. Multiple callbacks are allowed; they run in registration order and a
// callback error fails the upload's finish response.
func (h *Handler) OnComplete(fn func(context.Context, CompletedUpload) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.callbacks = append(h.callbacks, fn)
}

// fireComplete runs every registered callback; the first error stops the chain.
func (h *Handler) fireComplete(ctx context.Context, c CompletedUpload) error {
	h.mu.RLock()
	cbs := append([]completionFn(nil), h.callbacks...)
	h.mu.RUnlock()
	for _, fn := range cbs {
		if err := fn(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// params carries newHandler's named fx dependencies.
type params struct {
	fx.In
	LC     fx.Lifecycle
	Opts   Options
	Bucket storage.Bucket
	Logger *slog.Logger
	Clock  clock.Clock
}

func newHandler(p params) (*Handler, error) {
	if !p.Opts.Enabled {
		p.Logger.Debug("tus: module disabled (storage.tus.enabled=false)")
	}
	scratch, err := newLocalScratch(p.Opts.ScratchDir)
	if err != nil {
		return nil, err
	}
	h := &Handler{
		scratch:   scratch,
		opts:      p.Opts,
		log:       p.Logger,
		clk:       p.Clock,
		drainDone: make(chan struct{}),
	}
	h.store = newBucketStore(
		scratch, p.Bucket, defaultKeyFunc(p.Opts.KeyPrefix), p.Clock, p.Logger, h.fireComplete,
	)
	if err = h.buildTusd(); err != nil {
		return nil, err
	}
	h.wireLifecycle(p.LC)
	return h, nil
}

// buildTusd assembles the tusd store composer + routed handler from Options.
func (h *Handler) buildTusd() error {
	composer := tusd.NewStoreComposer()
	composer.UseCore(h.store)
	composer.UseTerminater(h.store)
	composer.UseLengthDeferrer(h.store)
	memorylocker.New().UseIn(composer) // node-local lock; single-replica scope

	cfg := tusd.Config{
		StoreComposer:                    composer,
		BasePath:                         h.opts.BasePath,
		MaxSize:                          h.opts.MaxSize,
		Logger:                           xslogLogger(h.log),
		NotifyCompleteUploads:            true,
		DisableDownload:                  h.opts.DisableDownload,
		DisableTermination:               h.opts.DisableTermination,
		DisableConcatenation:             h.opts.DisableConcatenation,
		RespectForwardedHeaders:          h.opts.RespectForwardedHeaders,
		NetworkTimeout:                   h.opts.NetworkTimeout,
		AcquireLockTimeout:               h.opts.AcquireLockTimeout,
		GracefulRequestCompletionTimeout: h.opts.GracefulRequestCompletionTimeout,
	}
	routed, err := tusd.NewHandler(cfg)
	if err != nil {
		return fmt.Errorf("tus: build handler: %w", err)
	}
	h.routed = routed
	h.unrouted = routed.UnroutedHandler
	// tusd validate() normalizes BasePath (adds slashes); keep ours in sync.
	h.opts.BasePath = cfg.BasePath
	return nil
}

// wireLifecycle starts the bounded completion-drain goroutine on OnStart and
// cancels + waits for it on OnStop, also sweeping expired scratch best-effort.
func (h *Handler) wireLifecycle(lc fx.Lifecycle) {
	lc.Append(fx.Hook{
		OnStart: func(startCtx context.Context) error {
			// Derive from startCtx so values propagate but detach its cancel —
			// the drain goroutine outlives OnStart and is stopped via OnStop.
			ctx, cancel := context.WithCancel(context.WithoutCancel(startCtx))
			h.drainCancel = cancel
			go h.drainCompletions(ctx)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if h.drainCancel != nil {
				h.drainCancel()
			}
			h.waitDrain(ctx)
			h.sweepExpired(ctx)
			return nil
		},
	})
}

// drainCompletions consumes tusd's CompleteUploads channel and dispatches the
// registered OnComplete callbacks. FinishUpload already fired the hooks inline;
// this loop logs completions and keeps the channel from blocking the handler.
func (h *Handler) drainCompletions(ctx context.Context) {
	defer close(h.drainDone)
	ch := h.unrouted.CompleteUploads
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-ch:
			h.log.InfoContext(ctx, "tus: upload complete",
				"id", ev.Upload.ID, "size", ev.Upload.Size)
		}
	}
}

// waitDrain blocks until the drain goroutine exits or ctx is done.
func (h *Handler) waitDrain(ctx context.Context) {
	select {
	case <-h.drainDone:
	case <-ctx.Done():
		h.log.WarnContext(ctx, "tus: drain did not stop before shutdown deadline")
	}
}

// sweepExpired removes in-progress scratch entries older than the configured
// upload expiry. Best-effort and bounded to the OnStop context.
func (h *Handler) sweepExpired(ctx context.Context) {
	ids, err := h.scratch.Expired(ctx, h.clk.Now(), h.opts.UploadExpiry)
	if err != nil {
		h.log.WarnContext(ctx, "tus: expiry sweep failed", "err", err)
		return
	}
	for _, id := range ids {
		entry, getErr := h.scratch.Get(ctx, id)
		if getErr != nil {
			continue
		}
		if termErr := entry.Terminate(ctx); termErr != nil {
			h.log.WarnContext(ctx, "tus: expire scratch failed", "id", id, "err", termErr)
		}
	}
}

// Options selects and tunes the tus endpoint. Config keys live under the
// "storage.tus" prefix.
type Options struct {
	Enabled                          bool          `koanf:"enabled"`
	BasePath                         string        `koanf:"base_path"`
	MaxSize                          int64         `koanf:"max_size"`
	KeyPrefix                        string        `koanf:"key_prefix"`
	Scratch                          string        `koanf:"scratch"`
	ScratchDir                       string        `koanf:"scratch_dir"`
	UploadExpiry                     time.Duration `koanf:"upload_expiry"`
	DisableDownload                  bool          `koanf:"disable_download"`
	DisableTermination               bool          `koanf:"disable_termination"`
	DisableConcatenation             bool          `koanf:"disable_concatenation"`
	RespectForwardedHeaders          bool          `koanf:"respect_forwarded_headers"`
	NetworkTimeout                   time.Duration `koanf:"network_timeout"`
	AcquireLockTimeout               time.Duration `koanf:"acquire_lock_timeout"`
	GracefulRequestCompletionTimeout time.Duration `koanf:"graceful_completion_timeout"`
}

func defaultOptions() Options {
	return Options{
		Enabled:                          false,
		BasePath:                         "/files/",
		MaxSize:                          0,
		KeyPrefix:                        "uploads/",
		Scratch:                          "local",
		ScratchDir:                       defaultScratchDir(),
		UploadExpiry:                     24 * time.Hour,
		DisableDownload:                  true,
		DisableTermination:               false,
		DisableConcatenation:             true,
		RespectForwardedHeaders:          false,
		NetworkTimeout:                   60 * time.Second,
		AcquireLockTimeout:               20 * time.Second,
		GracefulRequestCompletionTimeout: 10 * time.Second,
	}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("storage.tus", &opts); err != nil {
		return Options{}, fmt.Errorf("tus: load options: %w", err)
	}
	return opts, nil
}

// defaultScratchDir is the per-host in-progress upload root.
func defaultScratchDir() string {
	return filepath.Join(os.TempDir(), "golusoris-tus")
}

// Module provides *tus.Handler to the fx graph. Requires storage.Bucket,
// *slog.Logger, clock.Clock and *config.Config. Mounting is app-driven.
var Module = fx.Module(
	"golusoris.storage.tus",
	fx.Provide(loadOptions),
	fx.Provide(newHandler),
)
