// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package localstackdev

// Immutable test images; Renovate updates them (renovate.json test-image
// manager), and images_test.go keeps RyukImage equal to the root pin.
const (
	// LocalStackImage is the last LocalStack release that starts without an
	// auth token (2026.03 and later need one); renovate.json holds it on 4.x.
	LocalStackImage = "localstack/localstack:4.14.0@sha256:3ebc37595918b8accb852f8048fef2aff047d465167edd655528065b07bc364a"
	// RyukImage is the testcontainers resource reaper, as in
	// internal/testimages.
	RyukImage = "testcontainers/ryuk:0.14.0@sha256:7c1a8a9a47c780ed0f983770a662f80deb115d95cce3e2daa3d12115b8cd28f0"
)
