// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import "testing"

func TestFormatRedisConnectionUsesAddressAndTLSURL(t *testing.T) {
	t.Parallel()

	address, connectionURL := formatRedisConnection("cache.example.invalid")
	if address != "cache.example.invalid:6379" {
		t.Errorf("address = %q, want cache.example.invalid:6379", address)
	}
	if connectionURL != "rediss://cache.example.invalid:6379" {
		t.Errorf("URL = %q, want rediss://cache.example.invalid:6379", connectionURL)
	}
}
