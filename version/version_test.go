package version_test

import (
	"testing"

	"github.com/golusoris/golusoris/version"
)

func TestReadPopulatesGo(t *testing.T) {
	t.Parallel()
	// Under `go test` ReadBuildInfo succeeds; Go is always set, and Version
	// falls back to the module version (non-empty: a tag or "(devel)").
	info := version.Read()
	if info.Go == "" {
		t.Error("Go version must be populated from runtime.Version()")
	}
	if info.Version == "" {
		t.Error("Version must fall back to build-info, not stay empty")
	}
}

func TestInfoString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   version.Info
		want string
	}{
		{"version only", version.Info{Version: "1.2.3"}, "1.2.3"},
		{"with revision (truncated to 12)", version.Info{Version: "1.2.3", Revision: "abcdef0123456789"}, "1.2.3+abcdef012345"},
		{"dirty", version.Info{Version: "1.2.3", Revision: "abc", Dirty: true}, "1.2.3+abc-dirty"},
		{"empty version", version.Info{}, "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.in.String(); got != c.want {
				t.Errorf("String() = %q, want %q", got, c.want)
			}
		})
	}
}
