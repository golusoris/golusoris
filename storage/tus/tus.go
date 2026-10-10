// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
//	err := h.OnComplete("scan.enqueue.v1", func(ctx context.Context, c tus.CompletedUpload) error {
//	    return jobs.Enqueue(ctx, scanJob{Key: c.Key})
//	})
//
// Completion delivery is at-least-once; key durable side effects by upload ID.
//
// Scratch is node-local ("local"): a resumed PATCH must reach the same replica.
// Run single-replica or with sticky sessions until a distributed scratch lands.
// CORS for tus's custom headers is delegated to httpx/cors, not configured here.
package tus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	tusd "github.com/tus/tusd/v2/pkg/handler"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
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

const (
	maxCompletionCallbacks = 64
	maxCallbackIDBytes     = 128
)

type completionCallback struct {
	id string
	fn completionFn
}

// Handler is the mountable tus component. It is an http.Handler covering the
// whole tus sub-tree and also exposes Mount for explicit chi wiring.
type Handler struct {
	routed     *tusd.Handler
	unrouted   *tusd.UnroutedHandler
	store      *bucketStore
	scratch    scratchStore
	opts       Options
	log        *slog.Logger
	clk        clock.Clock
	locker     *uploadLocker
	mu         sync.RWMutex
	callbacks  []completionCallback
	deliveryMu sync.Mutex

	drainCancel context.CancelFunc
	drainDone   chan struct{}
	drainStep   atomic.Pointer[drainStep]
}

// drainStep is what the completion drain is doing, so a stop that times out
// can name the call it is blocked in.
type drainStep struct {
	op string
	id string
}

func (h *Handler) setDrainStep(op, id string) { h.drainStep.Store(&drainStep{op: op, id: id}) }

func (h *Handler) currentDrainStep() drainStep {
	if step := h.drainStep.Load(); step != nil {
		return *step
	}
	return drainStep{op: "not started"}
}

func (s drainStep) String() string {
	if s.id == "" {
		return s.op
	}
	return s.op + " " + s.id
}

// BasePath returns the URL prefix the handler routes under (e.g. "/files/").
func (h *Handler) BasePath() string { return h.opts.BasePath }

// ServeHTTP lets the Handler be mounted directly: r.Mount(h.BasePath(), h).
// tusd's routed mux matches on the path relative to BasePath, so the public
// prefix is stripped before delegating.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.opts.Enabled {
		http.NotFound(w, r)
		return
	}
	h.stripped().ServeHTTP(w, r)
}

// Mount registers the tus routes on r under the handler's BasePath via chi, so
// app middleware (auth, ratelimit) still wraps them. The routed tusd handler
// sees paths relative to BasePath; tusd keeps BasePath for Location headers.
func (h *Handler) Mount(r chi.Router) {
	if !h.opts.Enabled {
		return
	}
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

// OnComplete registers a stable callback ID delivered after FinishUpload
// durably records the Bucket object. Successful IDs are checkpointed so
// deployment-time callback reordering cannot replay them.
func (h *Handler) OnComplete(id string, fn func(context.Context, CompletedUpload) error) error {
	if err := validateCallbackID(id); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("tus: completion callback %q is nil", id)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.callbacks) >= maxCompletionCallbacks {
		return fmt.Errorf("tus: completion callback limit %d reached", maxCompletionCallbacks)
	}
	for _, callback := range h.callbacks {
		if callback.id == id {
			return fmt.Errorf("tus: duplicate completion callback id %q", id)
		}
	}
	h.callbacks = append(h.callbacks, completionCallback{id: id, fn: fn})
	return nil
}

func validateCallbackID(id string) error {
	if id == "" {
		return errors.New("tus: completion callback id is empty")
	}
	if len(id) > maxCallbackIDBytes {
		return fmt.Errorf("tus: completion callback id exceeds %d bytes", maxCallbackIDBytes)
	}
	for _, char := range id {
		if unicode.IsControl(char) || unicode.IsSpace(char) {
			return fmt.Errorf("tus: completion callback id %q contains whitespace or control", id)
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
	h := &Handler{
		opts:      p.Opts,
		log:       p.Logger,
		clk:       p.Clock,
		drainDone: make(chan struct{}),
	}
	if !p.Opts.Enabled {
		p.Logger.Debug("tus: module disabled (storage.tus.enabled=false)")
		return h, nil
	}
	if p.Opts.Scratch != "local" {
		return nil, fmt.Errorf("tus: unsupported scratch backend %q", p.Opts.Scratch)
	}
	if p.Opts.UploadExpiry <= 0 {
		return nil, errors.New("tus: upload expiry must be positive")
	}
	if p.Opts.ExpirySweepInterval <= 0 {
		return nil, errors.New("tus: expiry sweep interval must be positive")
	}
	if p.Opts.MaxSize <= 0 || p.Opts.MaxSize == math.MaxInt64 {
		return nil, errors.New("tus: max size must be positive and less than MaxInt64")
	}
	scratch, err := newLocalScratch(p.Opts.ScratchDir)
	if err != nil {
		return nil, err
	}
	h.scratch = scratch
	h.store = newBucketStore(scratch, p.Bucket, defaultKeyFunc(p.Opts.KeyPrefix), p.Logger, h.deliverCompletion)
	if err = h.buildTusd(); err != nil {
		return nil, errors.Join(err, scratch.Close())
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
	h.locker = newUploadLocker()
	composer.UseLocker(h.locker) // node-local lock; single-replica scope

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
	// The drain goroutine outlives OnStart; OnStop cancels and joins it.
	ctx, cancel := context.WithCancel(context.Background())
	h.drainCancel = cancel
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go h.drainCompletions(ctx)
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			cancel()
			return h.stop(stopCtx)
		},
	})
}

func (h *Handler) stop(ctx context.Context) error {
	if h.drainCancel != nil {
		h.drainCancel()
	}
	if err := h.waitDrain(ctx); err != nil {
		return err
	}
	h.sweepExpired(ctx)
	if closer, ok := h.scratch.(interface{ CloseContext(context.Context) error }); ok {
		if err := closer.CloseContext(ctx); err != nil {
			return fmt.Errorf("tus: close scratch: %w", err)
		}
	} else if closer, ok := h.scratch.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			return fmt.Errorf("tus: close scratch: %w", err)
		}
	}
	return nil
}

// drainCompletions consumes tusd notifications and periodically retries durable
// callbacks plus expired scratch cleanup. One worker bounds callback fan-out.
func (h *Handler) drainCompletions(ctx context.Context) {
	defer close(h.drainDone)
	ticker := h.clk.NewTicker(h.opts.ExpirySweepInterval)
	defer ticker.Stop()
	h.retryCompletions(ctx)
	ch := h.unrouted.CompleteUploads
	for ctx.Err() == nil {
		h.setDrainStep("wait for work", "")
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			h.log.InfoContext(ctx, "tus: upload complete",
				"id", ev.Upload.ID, "size", ev.Upload.Size)
			h.attemptCompletion(ctx, ev.Upload.ID)
		case <-ticker.Chan():
			h.retryCompletions(ctx)
			h.setDrainStep("sweep expired uploads", "")
			h.sweepExpired(ctx)
		}
	}
}

func (h *Handler) retryCompletions(ctx context.Context) {
	h.setDrainStep("list completions", "")
	ids, err := h.scratch.CompletionIDs(ctx, maxMaintenanceBatch)
	if err != nil {
		h.log.WarnContext(ctx, "tus: list durable completions failed", "err", err)
		return
	}
	for _, id := range ids {
		h.attemptCompletion(ctx, id)
	}
}

func (h *Handler) attemptCompletion(ctx context.Context, id string) {
	h.setDrainStep("complete upload", id)
	unlock, ok := h.tryMaintenanceLock(id)
	if !ok {
		return
	}
	defer unlock()
	var err error
	if h.store == nil {
		err = h.deliverCompletion(ctx, id)
	} else {
		err = h.store.ResumeCompletion(ctx, id)
	}
	if err != nil {
		h.log.WarnContext(ctx, "tus: durable completion failed", "id", id, "err", err)
	}
}

func (h *Handler) tryMaintenanceLock(id string) (func(), bool) {
	if h.locker == nil {
		return func() {}, true
	}
	return h.locker.tryMaintenanceLock(id)
}

func (h *Handler) deliverCompletion(ctx context.Context, id string) error {
	h.deliveryMu.Lock()
	defer h.deliveryMu.Unlock()
	record, err := h.scratch.Completion(ctx, id)
	if errors.Is(err, tusd.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load durable completion: %w", err)
	}
	if record.normalizedStage() != completionStagePersisted {
		return fmt.Errorf("completion %s is not persisted", id)
	}
	if err = h.runCallbacks(ctx, &record); err != nil {
		return err
	}
	if err = h.scratch.RemoveUpload(ctx, id); err != nil {
		return fmt.Errorf("cleanup completion scratch: %w", err)
	}
	if err = h.scratch.DeleteCompletion(ctx, id); err != nil {
		return fmt.Errorf("acknowledge completion: %w", err)
	}
	return nil
}

func (h *Handler) runCallbacks(ctx context.Context, record *completionRecord) error {
	if err := record.validate(); err != nil {
		return err
	}
	h.mu.RLock()
	cbs := append([]completionCallback(nil), h.callbacks...)
	h.mu.RUnlock()
	completed := make(map[string]struct{}, len(record.CompletedCallbackIDs))
	for _, id := range record.CompletedCallbackIDs {
		completed[id] = struct{}{}
	}
	for _, callback := range cbs {
		if _, done := completed[callback.id]; done {
			continue
		}
		if err := callback.fn(ctx, record.Upload.completed()); err != nil {
			return fmt.Errorf("callback %q: %w", callback.id, err)
		}
		record.CompletedCallbackIDs = append(record.CompletedCallbackIDs, callback.id)
		completed[callback.id] = struct{}{}
		if err := h.scratch.SaveCompletion(ctx, *record); err != nil {
			return fmt.Errorf("checkpoint callback %q: %w", callback.id, err)
		}
	}
	return nil
}

// waitDrain blocks until the drain goroutine exits or ctx is done.
func (h *Handler) waitDrain(ctx context.Context) error {
	select {
	case <-h.drainDone:
		return nil
	case <-ctx.Done():
		step := h.currentDrainStep()
		h.log.WarnContext(ctx, "tus: drain did not stop before shutdown deadline",
			"step", step.op, "upload_id", step.id)
		return fmt.Errorf("tus: wait for completion drain during %s: %w", step, ctx.Err())
	}
}

// sweepExpired removes in-progress scratch entries older than the configured
// upload expiry. Best-effort and bounded to the OnStop context.
func (h *Handler) sweepExpired(ctx context.Context) {
	now := h.clk.Now()
	ids, err := h.scratch.Expired(ctx, now, h.opts.UploadExpiry)
	if err != nil {
		h.log.WarnContext(ctx, "tus: expiry sweep failed", "err", err)
		return
	}
	for _, id := range ids {
		unlock, ok := h.tryMaintenanceLock(id)
		if !ok {
			continue
		}
		expired, expiryErr := h.scratch.IsExpired(ctx, id, now, h.opts.UploadExpiry)
		if expiryErr != nil && !errors.Is(expiryErr, tusd.ErrNotFound) {
			h.log.WarnContext(ctx, "tus: recheck expiry failed", "id", id, "err", expiryErr)
		}
		if expiryErr == nil && expired {
			if termErr := h.scratch.RemoveUpload(ctx, id); termErr != nil {
				h.log.WarnContext(ctx, "tus: expire scratch failed", "id", id, "err", termErr)
			}
		}
		unlock()
		if ctx.Err() != nil {
			return
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
	ExpirySweepInterval              time.Duration `koanf:"expiry_sweep_interval"`
	DisableDownload                  bool          `koanf:"disable_download"`
	DisableTermination               bool          `koanf:"disable_termination"`
	DisableConcatenation             bool          `koanf:"disable_concatenation"`
	RespectForwardedHeaders          bool          `koanf:"respect_forwarded_headers"`
	NetworkTimeout                   time.Duration `koanf:"network_timeout"`
	AcquireLockTimeout               time.Duration `koanf:"acquire_lock_timeout"`
	GracefulRequestCompletionTimeout time.Duration `koanf:"graceful_completion_timeout"`
}

const defaultMaxSize int64 = 5 << 30

func defaultOptions() Options {
	return Options{
		Enabled:                          false,
		BasePath:                         "/files/",
		MaxSize:                          defaultMaxSize,
		KeyPrefix:                        "uploads/",
		Scratch:                          "local",
		ScratchDir:                       "",
		UploadExpiry:                     24 * time.Hour,
		ExpirySweepInterval:              15 * time.Minute,
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

// Module provides *tus.Handler to the fx graph. Requires storage.Bucket,
// *slog.Logger, clock.Clock and *config.Config. Mounting is app-driven.
var Module = fx.Module(
	"golusoris.storage.tus",
	fx.Provide(loadOptions),
	fx.Provide(newHandler),
)
