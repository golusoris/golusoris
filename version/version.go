// Package version exposes the binary's build metadata as a typed [Info],
// sourced from ldflags -X overrides when present, otherwise from
// runtime/debug.ReadBuildInfo's vcs.* settings. Apps fx.Supply it (via
// [Module]) for /healthz payloads, server-info responses, and structured-log
// attributes, instead of hand-rolling a per-binary version string.
//
// Stamp a release version at build time:
//
//	go build -ldflags "-X github.com/golusoris/golusoris/version.version=1.2.3"
//
// With no ldflags, Version falls back to the VCS tag (or "(devel)") and
// Revision/Time/Dirty come from the embedded build info.
package version

import (
	"runtime"
	"runtime/debug"

	"go.uber.org/fx"
)

// Overridable at build time via -ldflags "-X .../version.<name>=<value>".
var (
	version  = ""
	revision = ""
	buildAt  = ""
)

// Info is the binary's build metadata.
type Info struct {
	Version  string `json:"version"`  // ldflags -X, else VCS tag, else "(devel)"
	Revision string `json:"revision"` // git commit (vcs.revision)
	Time     string `json:"time"`     // commit/build time (vcs.time)
	Dirty    bool   `json:"dirty"`    // uncommitted changes at build (vcs.modified)
	Go       string `json:"go"`       // runtime.Version()
}

// Read assembles [Info] from ldflags overrides, filling any gaps from the
// embedded build info (runtime/debug).
func Read() Info {
	info := Info{
		Version:  version,
		Revision: revision,
		Time:     buildAt,
		Go:       runtime.Version(),
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	if info.Version == "" {
		info.Version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if info.Revision == "" {
				info.Revision = s.Value
			}
		case "vcs.time":
			if info.Time == "" {
				info.Time = s.Value
			}
		case "vcs.modified":
			info.Dirty = s.Value == "true"
		}
	}
	return info
}

// String renders a compact one-line summary, e.g. "1.2.3+abc123def456-dirty".
func (i Info) String() string {
	v := i.Version
	if v == "" {
		v = "unknown"
	}
	if i.Revision != "" {
		rev := i.Revision
		if len(rev) > 12 {
			rev = rev[:12]
		}
		v += "+" + rev
	}
	if i.Dirty {
		v += "-dirty"
	}
	return v
}

// Module provides the build [Info] into the fx graph.
var Module = fx.Module(
	"golusoris.version",
	fx.Provide(Read),
)
