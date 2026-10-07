// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jsonschema_test

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/jsonschema"
	"github.com/golusoris/golusoris/testutil/snapshot"
)

type fixtureRetry struct {
	Attempts int           `koanf:"attempts"`
	Initial  time.Duration `koanf:"initial"`
}

type fixtureTLS struct {
	CertFile string `koanf:"cert_file"`
}

type fixtureCommon struct {
	Name string `koanf:"name" jsonschema_description:"Service name."`
}

// fixtureConfig covers every shape a module config uses: hoisted embedded
// struct, durations, scalars, slices, maps, nested and pointer structs, and
// a code-only koanf:"-" field whose func type the reflector cannot render.
type fixtureConfig struct {
	fixtureCommon

	DSN     string            `koanf:"dsn"`
	Timeout time.Duration     `koanf:"connect_timeout"`
	Enabled bool              `koanf:"enabled"`
	Ratio   float64           `koanf:"ratio"`
	Hosts   []string          `koanf:"hosts"`
	Labels  map[string]string `koanf:"labels"`
	Retry   fixtureRetry      `koanf:"retry"`
	TLS     *fixtureTLS       `koanf:"tls"`
	Hook    func()            `koanf:"-"`
}

func fixtureDefaults() fixtureConfig {
	return fixtureConfig{
		fixtureCommon: fixtureCommon{Name: "svc"},
		Timeout:       5 * time.Second,
		Enabled:       true,
		Ratio:         0.5,
		Hosts:         []string{"a", "b"},
		Retry:         fixtureRetry{Attempts: 3, Initial: 50 * time.Millisecond},
	}
}

func generateFixture(t *testing.T) []byte {
	t.Helper()
	doc, err := jsonschema.GenerateConfig(fixtureDefaults(), jsonschema.ConfigOptions{
		EnvPrefix: "APP_", Title: "fixture", ID: "https://example.test/values.schema.json",
	})
	if err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	return doc
}

func TestGenerateConfig_Golden(t *testing.T) {
	t.Parallel()
	snapshot.Match(t, string(generateFixture(t)))
}

func TestGenerateConfig_ValidatesHelmValues(t *testing.T) {
	t.Parallel()
	sch, err := jsonschema.Compile("values.schema.json", generateFixture(t))
	if err != nil {
		t.Fatalf("Compile generated schema: %v", err)
	}
	valid := []string{
		`{}`,
		`{"name":"svc","connect_timeout":"5s","enabled":true,"ratio":0.5,"hosts":["a"],"labels":{"team":"x"},
		  "retry":{"attempts":3,"initial":"50ms"},"tls":{"cert_file":"/etc/tls/tls.crt"}}`,
	}
	for _, doc := range valid {
		if err = sch.Validate([]byte(doc)); err != nil {
			t.Errorf("valid values rejected: %v\n%s", err, doc)
		}
	}
	invalid := map[string]string{
		"unknown key":        `{"dsnn":"x"}`,
		"unknown nested key": `{"retry":{"attempt":3}}`,
		"duration number":    `{"connect_timeout":5}`,
		"duration no unit":   `{"connect_timeout":"5"}`,
		"wrong scalar type":  `{"enabled":"yes"}`,
		"code-only field":    `{"Hook":"x"}`,
	}
	for name, doc := range invalid {
		if sch.Validate([]byte(doc)) == nil {
			t.Errorf("%s accepted: %s", name, doc)
		}
	}
}

func TestGenerateConfig_DefaultsAndEnvNames(t *testing.T) {
	t.Parallel()
	var doc struct {
		Properties map[string]struct {
			Default     any    `json:"default"`
			Description string `json:"description"`
			Properties  map[string]struct {
				Default     any    `json:"default"`
				Description string `json:"description"`
			} `json:"properties"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(generateFixture(t), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Properties["connect_timeout"].Default != "5s" || doc.Properties["retry"].Properties["initial"].Default != "50ms" {
		t.Errorf("duration defaults = %v, %v", doc.Properties["connect_timeout"].Default,
			doc.Properties["retry"].Properties["initial"].Default)
	}
	if _, ok := doc.Properties["dsn"]; !ok || doc.Properties["dsn"].Default != nil {
		t.Errorf("empty string default must be omitted, got %v", doc.Properties["dsn"].Default)
	}
	if got := doc.Properties["tls"].Properties["cert_file"]; got.Default != nil ||
		!strings.Contains(got.Description, `APP_TLS_CERT_FILE (declare config.Options.CompoundKeys entry "tls.cert_file")`) {
		t.Errorf("nil pointer leaf = %+v", got)
	}
	if got := doc.Properties["name"].Description; got != "Service name. Env: APP_NAME." {
		t.Errorf("hoisted field description = %q", got)
	}
	if got := doc.Properties["hosts"].Description; !strings.HasSuffix(got, "Comma-separated.") {
		t.Errorf("slice description = %q", got)
	}
	if len(doc.Required) != 0 {
		t.Errorf("required = %v, want none", doc.Required)
	}
}

func TestGenerateConfig_NoEnvPrefixOmitsEnvNames(t *testing.T) {
	t.Parallel()
	doc, err := jsonschema.GenerateConfig(&fixtureConfig{}, jsonschema.ConfigOptions{})
	if err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	if strings.Contains(string(doc), "Env:") {
		t.Fatal("env names emitted without EnvPrefix")
	}
}

type recursiveConfig struct {
	Child *recursiveConfig `koanf:"child"`
}

type unsupportedConfig struct {
	Hook func() `koanf:"hook"`
}

func TestGenerateConfig_Rejects(t *testing.T) {
	t.Parallel()
	var nilConfig *fixtureConfig
	for name, v := range map[string]any{"nil": nil, "nil pointer": nilConfig, "not a struct": 42} {
		if _, err := jsonschema.GenerateConfig(v, jsonschema.ConfigOptions{}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for name, v := range map[string]any{"recursive": recursiveConfig{}, "func field": unsupportedConfig{}} {
		_, err := jsonschema.GenerateConfig(v, jsonschema.ConfigOptions{})
		var unsupported *jsonschema.ErrUnsupportedType
		if !errors.As(err, &unsupported) {
			t.Errorf("%s: error = %v, want ErrUnsupportedType", name, err)
		}
	}
}

// TestDurationPatternMatchesParseDuration keeps the schema pattern and
// time.ParseDuration in agreement, including the boundary spellings.
func TestDurationPatternMatchesParseDuration(t *testing.T) {
	t.Parallel()
	pattern := regexp.MustCompile(jsonschema.DurationPattern)
	for _, s := range []string{
		"0", "-0", "+5s", "5s", "1h30m", "-1.5h", "300ms", "2us", "1µs", "1μs", "3ns", ".5s", "1.s", "1h0m0s",
		"", "5", "5 s", "1d", "s", "1.5", ".s", "5S", "--5s",
	} {
		_, err := time.ParseDuration(s)
		if pattern.MatchString(s) != (err == nil) {
			t.Errorf("%q: pattern match %v, ParseDuration error %v", s, pattern.MatchString(s), err)
		}
	}
}
