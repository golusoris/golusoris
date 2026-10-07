// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package httpoptions_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/golusoris/golusoris/ai/tiny/serve/internal/httpoptions"
)

func TestNormalize(t *testing.T) {
	t.Parallel()
	defaults := httpoptions.Defaults{
		Endpoint:         "http://127.0.0.1:8501",
		RequestTimeout:   30 * time.Second,
		MaxResponseBytes: 256 << 10,
	}
	callerClient := &http.Client{Timeout: 5 * time.Second}
	tests := []struct {
		name         string
		endpoint     string
		client       *http.Client
		maxBytes     int64
		wantEndpoint string
		wantTimeout  time.Duration
		wantMax      int64
	}{
		{
			name:         "defaults",
			wantEndpoint: defaults.Endpoint,
			wantTimeout:  defaults.RequestTimeout,
			wantMax:      defaults.MaxResponseBytes,
		},
		{
			name:         "caller values",
			endpoint:     "https://inference.example",
			client:       callerClient,
			maxBytes:     1024,
			wantEndpoint: "https://inference.example",
			wantTimeout:  5 * time.Second,
			wantMax:      1024,
		},
		{
			name:         "non-positive values default",
			client:       &http.Client{Timeout: -time.Second},
			maxBytes:     -1,
			wantEndpoint: defaults.Endpoint,
			wantTimeout:  defaults.RequestTimeout,
			wantMax:      defaults.MaxResponseBytes,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			endpoint, client, maxBytes := httpoptions.Normalize(
				tt.endpoint,
				tt.client,
				tt.maxBytes,
				defaults,
			)
			if endpoint != tt.wantEndpoint {
				t.Errorf("Endpoint = %q, want %q", endpoint, tt.wantEndpoint)
			}
			if client == nil || client.Timeout != tt.wantTimeout {
				t.Errorf("HTTPClient timeout = %v, want %v", client, tt.wantTimeout)
			}
			if maxBytes != tt.wantMax {
				t.Errorf("MaxResponseBytes = %d, want %d", maxBytes, tt.wantMax)
			}
			if tt.client != nil && client == tt.client {
				t.Error("Normalize returned caller-owned HTTP client")
			}
		})
	}
}
