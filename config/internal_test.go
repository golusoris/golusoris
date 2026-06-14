package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParserFor_yaml(t *testing.T) {
	t.Parallel()
	if parserFor("config.yaml") == nil {
		t.Error("expected non-nil parser for .yaml")
	}
	if parserFor("config.yml") == nil {
		t.Error("expected non-nil parser for .yml")
	}
}

func TestParserFor_json(t *testing.T) {
	t.Parallel()
	if parserFor("config.json") == nil {
		t.Error("expected non-nil parser for .json")
	}
}

func TestParserFor_unknown(t *testing.T) {
	t.Parallel()
	if parserFor("config.toml") != nil {
		t.Error("expected nil parser for .toml")
	}
}

func TestCompoundLookup(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		opts Options
		want map[string]string
	}{
		{
			name: "none declared returns nil",
			opts: Options{Delimiter: "."},
			want: nil,
		},
		{
			name: "single leaf compound",
			opts: Options{Delimiter: ".", CompoundKeys: []string{"search.api_key"}},
			want: map[string]string{"SEARCH_API_KEY": "search.api_key"},
		},
		{
			name: "nested compound",
			opts: Options{Delimiter: ".", CompoundKeys: []string{"auth.oidc.issuer_url"}},
			want: map[string]string{"AUTH_OIDC_ISSUER_URL": "auth.oidc.issuer_url"},
		},
		{
			name: "custom delimiter",
			opts: Options{Delimiter: "/", CompoundKeys: []string{"search/api_key"}},
			want: map[string]string{"SEARCH_API_KEY": "search/api_key"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := compoundLookup(tt.opts)
			if len(got) != len(tt.want) {
				t.Fatalf("compoundLookup() = %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("compoundLookup()[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestEnvTransform(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		opts    Options
		envKey  string
		wantKey string
	}{
		{
			name:    "default splits every underscore",
			opts:    Options{EnvPrefix: "APP_", Delimiter: "."},
			envKey:  "APP_SEARCH_API_KEY",
			wantKey: "search.api.key",
		},
		{
			name:    "declared compound preserves leaf underscore",
			opts:    Options{EnvPrefix: "APP_", Delimiter: ".", CompoundKeys: []string{"search.api_key"}},
			envKey:  "APP_SEARCH_API_KEY",
			wantKey: "search.api_key",
		},
		{
			name:    "non-declared var still splits when compounds present",
			opts:    Options{EnvPrefix: "APP_", Delimiter: ".", CompoundKeys: []string{"search.api_key"}},
			envKey:  "APP_DB_HOST",
			wantKey: "db.host",
		},
		{
			name:    "nested compound preserves only leaf underscore",
			opts:    Options{EnvPrefix: "APP_", Delimiter: ".", CompoundKeys: []string{"auth.oidc.issuer_url"}},
			envKey:  "APP_AUTH_OIDC_ISSUER_URL",
			wantKey: "auth.oidc.issuer_url",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fn := envTransform(tt.opts)
			gotKey, gotVal := fn(tt.envKey, "v")
			if gotKey != tt.wantKey {
				t.Errorf("envTransform key = %q, want %q", gotKey, tt.wantKey)
			}
			if gotVal != "v" {
				t.Errorf("envTransform value = %v, want \"v\"", gotVal)
			}
		})
	}
}

func TestFire_invokesListeners(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(path, []byte("x: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{Files: []string{path}})
	if err != nil {
		t.Fatal(err)
	}
	var called int
	c.OnChange(func() { called++ })
	c.fire()
	c.fire()
	if called != 2 {
		t.Errorf("fire called listeners %d times, want 2", called)
	}
}
