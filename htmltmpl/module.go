package htmltmpl

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
)

// Options configures the renderer; koanf-bound under the "htmltmpl" prefix.
//
//	htmltmpl.dir            = "templates"
//	htmltmpl.patterns       = ["**/*.gohtml"]
//	htmltmpl.layouts        = "layouts"
//	htmltmpl.partials       = "partials"
//	htmltmpl.default_layout = "base"   # "" disables auto-wrapping
//	htmltmpl.delims         = ["{{","}}"]
//	htmltmpl.hot_reload     = true     # omit => dev:true / prod:false from env
//	htmltmpl.strict         = false    # true => Option("missingkey=error")
type Options struct {
	// Dir is the template root used when no fs.FS is injected (os.DirFS(Dir)).
	Dir string `koanf:"dir"`
	// Patterns are globs relative to the root; ** spans path segments.
	Patterns []string `koanf:"patterns"`
	// Layouts is the subdir holding layout templates (informational; the tree
	// is flat-keyed by file path).
	Layouts string `koanf:"layouts"`
	// Partials is the subdir holding partials/includes (informational).
	Partials string `koanf:"partials"`
	// DefaultLayout names the layout Render wraps pages in; "" disables it.
	DefaultLayout string `koanf:"default_layout"`
	// Delims overrides the action delimiters; default {"{{","}}"}.
	Delims [2]string `koanf:"delims"`
	// HotReload, when non-nil, forces re-parse on every render (true) or never
	// (false). Nil derives from APP_ENV: "dev"/"development" => true, else false.
	HotReload *bool `koanf:"hot_reload"`
	// Strict sets Option("missingkey=error") so a missing map key fails the
	// render instead of rendering "<no value>".
	Strict bool `koanf:"strict"`
}

func defaultOptions() Options {
	return Options{
		Dir:           "templates",
		Patterns:      []string{"**/*.gohtml"},
		Layouts:       "layouts",
		Partials:      "partials",
		DefaultLayout: "",
		Delims:        [2]string{"{{", "}}"},
	}
}

// withDefaults fills any zero fields left by a partial config so the Renderer
// never operates on an empty pattern set or empty delimiters.
func (o Options) withDefaults() Options {
	d := defaultOptions()
	if o.Dir == "" {
		o.Dir = d.Dir
	}
	if len(o.Patterns) == 0 {
		o.Patterns = d.Patterns
	}
	if o.Layouts == "" {
		o.Layouts = d.Layouts
	}
	if o.Partials == "" {
		o.Partials = d.Partials
	}
	if o.Delims[0] == "" || o.Delims[1] == "" {
		o.Delims = d.Delims
	}
	return o
}

// hotReload resolves the tri-state HotReload: explicit value wins; otherwise it
// derives from APP_ENV (dev => true, prod-safe default false).
func (o Options) hotReload() bool {
	if o.HotReload != nil {
		return *o.HotReload
	}
	switch strings.ToLower(os.Getenv("APP_ENV")) {
	case "dev", "development", "local":
		return true
	default:
		return false
	}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("htmltmpl", &opts); err != nil {
		return Options{}, fmt.Errorf("htmltmpl: load options: %w", err)
	}
	return opts, nil
}

// rendererParams are the fx inputs for [newRenderer]. FS and Funcs are optional:
// without an injected fs.FS the renderer falls back to os.DirFS(opts.Dir);
// without a FuncProvider only the curated default funcs apply.
type rendererParams struct {
	fx.In

	Opts   Options
	Logger *slog.Logger
	Clock  clock.Clock
	FS     fs.FS        `optional:"true"`
	Funcs  FuncProvider `optional:"true"`
}

// newRenderer builds the Renderer for fx, falling back to os.DirFS when no
// fs.FS is supplied.
func newRenderer(p rendererParams) (*Renderer, error) {
	fsys := p.FS
	if fsys == nil {
		dir := p.Opts.withDefaults().Dir
		fsys = os.DirFS(dir)
	}
	r, err := New(p.Opts, p.Logger, p.Clock, fsys, p.Funcs)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// Module provides a *htmltmpl.Renderer to the fx graph. It requires *config.Config,
// *slog.Logger and clock.Clock from golusoris.Core; an fs.FS and a FuncProvider
// are optional (apps fx.Supply an embed.FS and/or a sprout-backed provider).
var Module = fx.Module(
	"golusoris.htmltmpl",
	fx.Provide(loadOptions),
	fx.Provide(newRenderer),
)
