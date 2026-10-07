// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateStackConfigRequiresPublicTLSInputs(t *testing.T) {
	t.Parallel()
	valid := stackConfig{
		AppImage:       "example.invalid/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Domain:         "app.example.com",
		HostedZoneID:   "ZCALLER",
		CertificateARN: "arn:aws:acm:us-east-1:123456789012:certificate/test",
	}
	if err := validateStackConfig(valid); err != nil {
		t.Fatalf("validateStackConfig(valid) error = %v", err)
	}
	for _, test := range []struct {
		name   string
		field  string
		mutate func(*stackConfig)
	}{
		{name: "domain", field: "domain", mutate: func(cfg *stackConfig) { cfg.Domain = "" }},
		{name: "hosted zone", field: "hostedZoneId", mutate: func(cfg *stackConfig) { cfg.HostedZoneID = "" }},
		{name: "certificate", field: "certificateArn", mutate: func(cfg *stackConfig) { cfg.CertificateARN = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := valid
			test.mutate(&cfg)
			err := validateStackConfig(cfg)
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("validateStackConfig() error = %v, want required %s", err, test.field)
			}
		})
	}
}

func TestRenderContainerUsesRuntimeConfigKeys(t *testing.T) {
	t.Parallel()
	rendered, err := renderContainer(
		"app",
		appConfig{Image: "example.invalid/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Port: 8080, Region: "us-east-1"},
		"arn:db",
		"arn:redis",
		"logs",
	)
	if err != nil {
		t.Fatalf("renderContainer() error = %v", err)
	}

	var definitions []struct {
		Environment []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"environment"`
		Secrets []struct {
			Name      string `json:"name"`
			ValueFrom string `json:"valueFrom"` //nolint:tagliatelle // ECS JSON uses AWS's camelCase field.
		} `json:"secrets"`
	}
	if err = json.Unmarshal([]byte(rendered), &definitions); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(definitions) != 1 {
		t.Fatalf("container count = %d, want 1", len(definitions))
	}

	got := make(map[string]string, len(definitions[0].Secrets))
	for _, secret := range definitions[0].Secrets {
		got[secret.Name] = secret.ValueFrom
	}
	if got["APP_DB_DSN"] != "arn:db" {
		t.Errorf("APP_DB_DSN = %q, want arn:db", got["APP_DB_DSN"])
	}
	if got["APP_CACHE_REDIS_ADDR"] != "arn:redis" {
		t.Errorf("APP_CACHE_REDIS_ADDR = %q, want arn:redis", got["APP_CACHE_REDIS_ADDR"])
	}
	if _, exists := got["APP_CACHE_ADDR"]; exists {
		t.Error("obsolete APP_CACHE_ADDR secret is present")
	}
	environment := make(map[string]string, len(definitions[0].Environment))
	for _, variable := range definitions[0].Environment {
		environment[variable.Name] = variable.Value
	}
	if environment["APP_CACHE_REDIS_TLS"] != "true" {
		t.Errorf("APP_CACHE_REDIS_TLS = %q, want true", environment["APP_CACHE_REDIS_TLS"])
	}
}
