package inertia

import (
	"fmt"
	"io/fs"
	"log/slog"

	gonertia "github.com/romsar/gonertia/v3"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Options is unmarshalled from cfg under the "inertia" prefix.
type Options struct {
	// RootTemplate is the path (within RootFS, or on disk) to the root HTML
	// shell containing the {{ .inertia }} and {{ .inertiaHead }} placeholders.
	RootTemplate string `koanf:"root_template"`
	// Version pins the asset version string directly. When empty, ManifestPath
	// derives it from the vite manifest checksum (the 409 reload handshake).
	Version string `koanf:"version"`
	// ManifestPath is the vite manifest used for asset-version derivation when
	// Version is empty.
	ManifestPath string `koanf:"manifest_path"`
	// ContainerID overrides the root DOM element id (default "app").
	ContainerID string `koanf:"container_id"`
	// EncryptHistory enables Inertia's global history encryption.
	EncryptHistory bool `koanf:"encrypt_history"`
	// SSR configures the optional Node server-side-rendering sidecar.
	SSR SSROptions `koanf:"ssr"`
}

// SSROptions configures the optional server-side-rendering sidecar.
type SSROptions struct {
	// Enabled turns on server-side rendering via the Node SSR sidecar.
	Enabled bool `koanf:"enabled"`
	// URL is the SSR sidecar render endpoint.
	URL string `koanf:"url"`
}

// RootFS is the optional fx-injected filesystem holding the root template and
// manifest (the app's embed.FS). When the zero value is supplied (FS nil), the
// module reads RootTemplate and ManifestPath from disk instead.
type RootFS struct{ fs.FS }

func defaultOptions() Options {
	return Options{
		RootTemplate: "web/root.html",
		ManifestPath: "web/dist/.vite/manifest.json",
		ContainerID:  "app",
		SSR: SSROptions{
			URL: "http://127.0.0.1:13714",
		},
	}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("inertia", &opts); err != nil {
		return Options{}, fmt.Errorf("httpx/inertia: load options: %w", err)
	}
	return opts, nil
}

// buildOptions assembles the gonertia option list from opts and the optional
// RootFS. Version pinning wins over manifest-checksum derivation.
func buildOptions(opts Options, logger *slog.Logger, rootFS RootFS) []gonertia.Option {
	gopts := []gonertia.Option{
		gonertia.WithLogger(slogLogger{l: logger}),
		gonertia.WithContainerID(opts.ContainerID),
	}
	if opts.EncryptHistory {
		gopts = append(gopts, gonertia.WithEncryptHistory())
	}
	if opts.SSR.Enabled {
		gopts = append(gopts, gonertia.WithSSR(opts.SSR.URL))
	}
	switch {
	case opts.Version != "":
		gopts = append(gopts, gonertia.WithVersion(opts.Version))
	case opts.ManifestPath != "" && rootFS.FS != nil:
		gopts = append(gopts, gonertia.WithVersionFromFileFS(rootFS.FS, opts.ManifestPath))
	case opts.ManifestPath != "":
		gopts = append(gopts, gonertia.WithVersionFromFile(opts.ManifestPath))
	}
	return gopts
}

// inertiaParams are the fx inputs for newInertia. RootFS is optional — fx
// supplies the zero value when no embed.FS is wired, and the module falls back
// to on-disk reads for the template and manifest.
type inertiaParams struct {
	fx.In

	Opts   Options
	Logger *slog.Logger
	RootFS RootFS `optional:"true"`
}

// newInertia builds *Inertia from opts, selecting fs.FS vs disk constructors
// and adapting *slog.Logger to gonertia's Logger interface.
func newInertia(p inertiaParams) (*Inertia, error) {
	gopts := buildOptions(p.Opts, p.Logger, p.RootFS)

	var (
		i   *Inertia
		err error
	)
	if p.RootFS.FS != nil {
		i, err = gonertia.NewFromFileFS(p.RootFS.FS, p.Opts.RootTemplate, gopts...)
	} else {
		i, err = gonertia.NewFromFile(p.Opts.RootTemplate, gopts...)
	}
	if err != nil {
		return nil, fmt.Errorf("httpx/inertia: build adapter: %w", err)
	}

	p.Logger.Debug(
		"httpx/inertia: started",
		slog.String("root_template", p.Opts.RootTemplate),
		slog.String("container_id", p.Opts.ContainerID),
		slog.Bool("encrypt_history", p.Opts.EncryptHistory),
		slog.Bool("ssr", p.Opts.SSR.Enabled),
		slog.Bool("embed_fs", p.RootFS.FS != nil),
	)
	return i, nil
}

// Module provides *inertia.Inertia to the fx graph. It mounts no routes — the
// app installs i.Middleware on its chi router and calls i.Render in handlers.
var Module = fx.Module(
	"golusoris.httpx.inertia",
	fx.Provide(loadOptions),
	fx.Provide(newInertia),
)

// NewForTest builds an *Inertia from explicit options without fx, for tests.
// rootFS may be the zero RootFS to read the template from disk.
func NewForTest(opts Options, logger *slog.Logger, rootFS RootFS) (*Inertia, error) {
	return newInertia(inertiaParams{Opts: opts, Logger: logger, RootFS: rootFS})
}
