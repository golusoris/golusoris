// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
)

type underscoreConfig struct {
	Grace       time.Duration `koanf:"max_connection_age_grace"`
	Host        string        `koanf:"host"`
	FileOnly    string        `koanf:"file_only"`
	DefaultOnly string        `koanf:"default_only"`
}

func newEnvConfig(t *testing.T, opts config.Options) *config.Config {
	t.Helper()
	c, err := config.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func decodeEnvConfig(t *testing.T, c *config.Config, path string, into any) {
	t.Helper()
	if err := c.Unmarshal(path, into); err != nil {
		t.Fatalf("Unmarshal(%q): %v", path, err)
	}
}

func TestUnmarshalUnderscorePaths(t *testing.T) {
	t.Setenv("APP_X_MAX_CONNECTION_AGE_GRACE", "7s")
	c := newEnvConfig(t, config.Options{EnvPrefix: "APP_"})
	var scoped underscoreConfig
	decodeEnvConfig(t, c, "x", &scoped)
	var root struct {
		X *underscoreConfig `koanf:"x"`
	}
	decodeEnvConfig(t, c, "", &root)
	if scoped.Grace != 7*time.Second || root.X == nil || root.X.Grace != 7*time.Second {
		t.Fatalf("scoped = %+v, nested = %+v; want grace 7s", scoped, root.X)
	}
	if c.Exists("x.max_connection_age_grace") || c.String("x.max.connection.age.grace") != "7s" {
		t.Fatal("automatic struct mapping changed the stored tree")
	}
}

func TestUnmarshalUnderscorePrecedence(t *testing.T) {
	t.Setenv("APP_X_MAX_CONNECTION_AGE_GRACE", "7s")
	t.Setenv("APP_X_HOST", "env-host")
	path := writeFile(t, filepath.Join(t.TempDir(), "cfg.yaml"),
		"x:\n  max_connection_age_grace: 1s\n  host: file-host\n  file_only: retained\n")
	secrets := t.TempDir()
	writeFile(t, filepath.Join(secrets, "x.max_connection_age_grace"), "3s")
	c := newEnvConfig(t, config.Options{EnvPrefix: "APP_", Files: []string{path}, SecretDirs: []string{secrets}})
	s := underscoreConfig{DefaultOnly: "default"}
	decodeEnvConfig(t, c, "x", &s)
	if s.Grace != 7*time.Second || s.Host != "env-host" || s.FileOnly != "retained" || s.DefaultOnly != "default" {
		t.Fatalf("decoded = %+v; want env grace/host and retained file/default", s)
	}
}

func TestUnmarshalUnderscoreSnapshot(t *testing.T) {
	t.Setenv("APP_X_MAX_CONNECTION_AGE_GRACE", "7s")
	t.Setenv("APP_X_HOST", "old-host")
	c := newEnvConfig(t, config.Options{EnvPrefix: "APP_"})
	t.Setenv("APP_X_MAX_CONNECTION_AGE_GRACE", "9s")
	t.Setenv("APP_X_HOST", "new-host")
	var s underscoreConfig
	decodeEnvConfig(t, c, "x", &s)
	if s.Grace != 7*time.Second || s.Host != "old-host" {
		t.Fatalf("snapshot = %+v; want grace 7s, old-host", s)
	}
}

func TestUnmarshalUnderscoreDisabled(t *testing.T) {
	t.Setenv("APP_X_MAX_CONNECTION_AGE_GRACE", "7s")
	t.Setenv("X_MAX_CONNECTION_AGE_GRACE", "8s")
	path := writeFile(t, filepath.Join(t.TempDir(), "cfg.yaml"), "x:\n  max_connection_age_grace: 1s\n")
	for _, opts := range []config.Options{
		{EnvPrefix: "APP_", DisableEnv: true, Files: []string{path}},
		{Files: []string{path}},
	} {
		c := newEnvConfig(t, opts)
		var s underscoreConfig
		decodeEnvConfig(t, c, "x", &s)
		if s.Grace != time.Second {
			t.Fatalf("options = %+v: grace = %v, want file value 1s", opts, s.Grace)
		}
	}
}

// EmbeddedUnderscore exercises exported embedded configuration fields.
type EmbeddedUnderscore struct {
	Grace    time.Duration `koanf:"max_connection_age_grace"`
	FileOnly string        `koanf:"file_only"`
}

func TestUnmarshalUnderscoreStructRules(t *testing.T) {
	t.Setenv("CFGSTRUCT_MAX_CONNECTION_AGE_GRACE", "7s")
	t.Setenv("CFGSTRUCT_GROUP_NAME_MAX_CONNECTION_AGE_GRACE", "8s")
	t.Setenv("CFGSTRUCT_EMBEDDEDUNDERSCORE_MAX_CONNECTION_AGE_GRACE", "9s")
	t.Setenv("CFGSTRUCT_FALLBACK_GROUP_VALUENAME", "untagged")
	t.Setenv("CFGSTRUCT_IGNORED_FIELD", "ignored")
	c := newEnvConfig(t, config.Options{EnvPrefix: "CFGSTRUCT_", Delimiter: "/"})
	var squashed struct {
		*EmbeddedUnderscore `koanf:",squash"`
		Group               *underscoreConfig          `koanf:"group_name"`
		Fallback            struct{ ValueName string } `koanf:"fallback_group"`
		Ignored             string                     `koanf:"-"`
		hidden              string                     `koanf:"ignored_field"`
	}
	decodeEnvConfig(t, c, "", &squashed)
	if squashed.EmbeddedUnderscore == nil || squashed.Grace != 7*time.Second ||
		squashed.Group == nil || squashed.Group.Grace != 8*time.Second || squashed.Fallback.ValueName != "untagged" {
		t.Fatalf("squash/nested pointer/fallback field names: %+v", squashed)
	}
	if squashed.Ignored != "" || squashed.hidden != "" {
		t.Fatal("ignored or unexported field was changed")
	}
	var embedded struct{ EmbeddedUnderscore }
	decodeEnvConfig(t, c, "", &embedded)
	if embedded.Grace != 9*time.Second {
		t.Fatalf("unsquashed embedding grace = %v, want 9s", embedded.Grace)
	}
}

func TestUnmarshalUnderscoreCompoundKeys(t *testing.T) {
	t.Setenv("CFGCOMPOUND_MAX_CONNECTION_AGE_GRACE", "7s")
	c := newEnvConfig(t, config.Options{
		EnvPrefix: "CFGCOMPOUND_", CompoundKeys: []string{"max_connection_age_grace"},
	})
	var s underscoreConfig
	decodeEnvConfig(t, c, "", &s)
	if s.Grace != 7*time.Second || c.String("max_connection_age_grace") != "7s" {
		t.Fatalf("explicit compound mapping changed: %+v", s)
	}
	c = newEnvConfig(t, config.Options{
		EnvPrefix: "CFGCOMPOUND_", CompoundKeys: []string{"max.connection_age_grace"},
	})
	s.Grace = time.Second
	decodeEnvConfig(t, c, "", &s)
	if s.Grace != time.Second {
		t.Fatal("inferred path overrode explicit compound destination")
	}
}

func TestUnmarshalUnderscoreHooksAndErrors(t *testing.T) {
	t.Setenv("CFGHOOK_RETRY_TIMES", "a,b")
	t.Setenv("CFGHOOK_MAX_CONNECTION_AGE_GRACE", "invalid-duration")
	c := newEnvConfig(t, config.Options{EnvPrefix: "CFGHOOK_"})
	var s struct {
		Values []string `koanf:"retry_times"`
	}
	decodeEnvConfig(t, c, "", &s)
	if strings.Join(s.Values, ",") != "a,b" {
		t.Fatalf("slice hook = %v, want [a b]", s.Values)
	}
	var invalid underscoreConfig
	if err := c.Unmarshal("", &invalid); err == nil || !strings.Contains(err.Error(), "config: unmarshal env") {
		t.Fatalf("invalid env duration error = %v", err)
	}
}

func TestUnmarshalUnderscoreEmptyValue(t *testing.T) {
	t.Setenv("CFGEMPTY_FILE_ONLY", "")
	c := newEnvConfig(t, config.Options{EnvPrefix: "CFGEMPTY_"})
	s := underscoreConfig{FileOnly: "default"}
	decodeEnvConfig(t, c, "", &s)
	if s.FileOnly != "" {
		t.Fatalf("empty env did not override default: %q", s.FileOnly)
	}
}

func TestUnmarshalUnderscoreFileEnvSuffix(t *testing.T) {
	secret := writeFile(t, filepath.Join(t.TempDir(), "grace"), "7s")
	t.Setenv("CFGINDIRECT_X_MAX_CONNECTION_AGE_GRACE_FILE", secret)
	path := writeFile(t, filepath.Join(t.TempDir(), "cfg.yaml"), "x:\n  max_connection_age_grace: 1s\n")
	c := newEnvConfig(t, config.Options{EnvPrefix: "CFGINDIRECT_", FileEnvSuffix: "_FILE", Files: []string{path}})
	var s underscoreConfig
	decodeEnvConfig(t, c, "x", &s)
	if s.Grace != time.Second || c.String("x.max.connection.age.grace") != "7s" {
		t.Fatalf("inferred *_FILE mapping changed existing behavior: %+v", s)
	}
}

func TestUnmarshalUnderscoreRecursiveType(t *testing.T) {
	t.Setenv("CFGCYCLE_FILE_ONLY", "env")
	type node struct {
		Next  *node  `koanf:"next"`
		Value string `koanf:"file_only"`
	}
	c := newEnvConfig(t, config.Options{EnvPrefix: "CFGCYCLE_"})
	var s node
	if err := c.Unmarshal("", &s); err == nil || !strings.Contains(err.Error(), "32 levels") {
		t.Fatalf("recursive destination error = %v, want depth bound", err)
	}
}

func nestedEnvDestination(levels int) (any, string) {
	typ := reflect.TypeFor[underscoreConfig]()
	for i := 1; i < levels; i++ {
		typ = reflect.StructOf([]reflect.StructField{{Name: "Child", Type: typ, Tag: `koanf:"n"`}})
	}
	return reflect.New(typ).Interface(), strings.Repeat("N_", levels-1) + "MAX_CONNECTION_AGE_GRACE"
}

func TestUnmarshalUnderscoreDepthBoundary(t *testing.T) {
	dest, name := nestedEnvDestination(32)
	tooDeep, deepName := nestedEnvDestination(33)
	t.Setenv("CFGDEPTH_"+name, "7s")
	t.Setenv("CFGDEPTH_"+deepName, "8s")
	c := newEnvConfig(t, config.Options{EnvPrefix: "CFGDEPTH_"})
	decodeEnvConfig(t, c, "", dest)
	value := reflect.ValueOf(dest).Elem()
	for i := 1; i < 32; i++ {
		value = value.Field(0)
	}
	if got := value.FieldByName("Grace").Int(); got != int64(7*time.Second) {
		t.Fatalf("depth 32 grace = %d, want 7s", got)
	}
	if err := c.Unmarshal("", tooDeep); err == nil || !strings.Contains(err.Error(), "32 levels") {
		t.Fatalf("depth 33 error = %v, want depth bound", err)
	}
}

func TestUnmarshalUnderscoreFieldBoundary(t *testing.T) {
	t.Setenv("CFGFIELDS_VALUE_0", "7")
	for _, count := range []int{4096, 4097} {
		fields := make([]reflect.StructField, count)
		for i := range fields {
			fields[i] = reflect.StructField{
				Name: fmt.Sprintf("Value%d", i), Type: reflect.TypeFor[int](),
				Tag: reflect.StructTag(fmt.Sprintf(`koanf:"value_%d"`, i)),
			}
		}
		dest := reflect.New(reflect.StructOf(fields))
		c := newEnvConfig(t, config.Options{EnvPrefix: "CFGFIELDS_"})
		err := c.Unmarshal("", dest.Interface())
		if count == 4097 {
			if err == nil || !strings.Contains(err.Error(), "4096 fields") {
				t.Fatalf("4097 fields error = %v, want field bound", err)
			}
			continue
		}
		if err != nil || dest.Elem().Field(0).Int() != 7 {
			t.Fatalf("4096 fields: first value = %d, error = %v", dest.Elem().Field(0).Int(), err)
		}
	}
}

func watchEnvConfig(t *testing.T, path string) (*config.Config, <-chan struct{}) {
	t.Helper()
	var c *config.Config
	app := fxtest.New(t, config.Module,
		fx.Replace(config.Options{EnvPrefix: "CFGWATCH_", Files: []string{path}, Watch: true}),
		fx.Populate(&c),
	)
	changed := make(chan struct{}, 16)
	c.OnChange(func() { changed <- struct{}{} })
	app.RequireStart()
	t.Cleanup(func() { app.RequireStop() })
	return c, changed
}

func waitEnvReload(t *testing.T, c *config.Config, changed <-chan struct{}, wantFile string) underscoreConfig {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for range 32 {
		select {
		case <-changed:
			var s underscoreConfig
			decodeEnvConfig(t, c, "x", &s)
			if s.FileOnly == wantFile {
				return s
			}
		case <-timeout:
			t.Fatal("timed out waiting for file reload")
		}
	}
	t.Fatal("too many file reload events without expected value")
	return underscoreConfig{}
}

func TestUnmarshalUnderscoreWatchReload(t *testing.T) {
	t.Setenv("CFGWATCH_X_MAX_CONNECTION_AGE_GRACE", "7s")
	path := writeFile(t, filepath.Join(t.TempDir(), "cfg.yaml"),
		"x:\n  max_connection_age_grace: 1s\n  file_only: before\n")
	c, changed := watchEnvConfig(t, path)
	t.Setenv("CFGWATCH_X_MAX_CONNECTION_AGE_GRACE", "9s")
	var s underscoreConfig
	decodeEnvConfig(t, c, "x", &s)
	if s.Grace != 7*time.Second {
		t.Fatalf("before reload grace = %v, want 7s", s.Grace)
	}
	writeFile(t, path, "x:\n  max_connection_age_grace: 2s\n  file_only: after\n")
	s = waitEnvReload(t, c, changed, "after")
	if s.Grace != 9*time.Second || s.FileOnly != "after" {
		t.Fatalf("after reload = %+v, want env 9s and updated file", s)
	}
}

func TestUnmarshalUnderscoreReloadRemovesEnv(t *testing.T) {
	t.Setenv("CFGWATCH_X_MAX_CONNECTION_AGE_GRACE", "7s")
	path := writeFile(t, filepath.Join(t.TempDir(), "cfg.yaml"),
		"x:\n  max_connection_age_grace: 1s\n  file_only: before\n")
	c, changed := watchEnvConfig(t, path)
	if err := os.Unsetenv("CFGWATCH_X_MAX_CONNECTION_AGE_GRACE"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "x:\n  max_connection_age_grace: 3s\n  file_only: removed\n")
	s := waitEnvReload(t, c, changed, "removed")
	if s.Grace != 3*time.Second {
		t.Fatalf("removed env remained in snapshot: grace = %v, want file 3s", s.Grace)
	}
}

func TestUnmarshalUnderscoreLeafKinds(t *testing.T) {
	t.Setenv("APP_L_EXTRA_HEADERS", "not-a-map")
	t.Setenv("APP_L_ALLOWED_HOSTS", "a,b")
	c := newEnvConfig(t, config.Options{EnvPrefix: "APP_"})
	var s struct {
		ExtraHeaders map[string]string `koanf:"extra_headers"`
		AllowedHosts []string          `koanf:"allowed_hosts"`
	}
	decodeEnvConfig(t, c, "l", &s)
	if s.ExtraHeaders != nil || !reflect.DeepEqual(s.AllowedHosts, []string{"a", "b"}) {
		t.Fatalf("leaf overrides = %+v, want slice from env and map untouched", s)
	}
}
