// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tflite

import (
	"net/http"
	"testing"
	"time"
)

func TestNewPredictorClonesAndBoundsHTTPClient(t *testing.T) {
	t.Parallel()
	injected := &http.Client{Transport: http.DefaultTransport}
	predictor := NewPredictor(Options{HTTPClient: injected})
	if predictor.opts.HTTPClient == injected {
		t.Fatal("NewPredictor retained caller-owned client")
	}
	if injected.Timeout != 0 || predictor.opts.HTTPClient.Timeout != defaultRequestTimeout {
		t.Fatalf("predictor/source timeout = %s/%s", predictor.opts.HTTPClient.Timeout, injected.Timeout)
	}
	if predictor.opts.HTTPClient.Transport != injected.Transport {
		t.Fatal("injected transport was not preserved")
	}

	const customTimeout = 23 * time.Second
	custom := NewPredictor(Options{HTTPClient: &http.Client{Timeout: customTimeout}})
	if custom.opts.HTTPClient.Timeout != customTimeout {
		t.Fatalf("custom timeout = %s, want %s", custom.opts.HTTPClient.Timeout, customTimeout)
	}
	defaults := NewPredictor(Options{})
	if defaults.opts.HTTPClient.Timeout != defaultRequestTimeout {
		t.Fatalf("default timeout = %s, want %s", defaults.opts.HTTPClient.Timeout, defaultRequestTimeout)
	}
}
