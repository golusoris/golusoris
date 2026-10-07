// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package httpoptions normalizes shared Tiny inference HTTP options.
package httpoptions

import (
	"net/http"
	"time"

	httpclient "github.com/golusoris/golusoris/httpx/client"
)

// Defaults defines one adapter's bounded HTTP defaults.
type Defaults struct {
	Endpoint         string
	RequestTimeout   time.Duration
	MaxResponseBytes int64
}

// Normalize applies defaults and returns a caller-safe client clone.
func Normalize(
	endpoint string,
	client *http.Client,
	maxResponseBytes int64,
	defaults Defaults,
) (string, *http.Client, int64) {
	if endpoint == "" {
		endpoint = defaults.Endpoint
	}
	client = httpclient.CloneBounded(client, defaults.RequestTimeout)
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaults.MaxResponseBytes
	}
	return endpoint, client, maxResponseBytes
}
