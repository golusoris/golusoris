// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package lowkeydev pins the Lowkey Vault test images. It lives in this
// module, not in root internal/testimages, so the module's tests need no
// root golusoris release.
package lowkeydev

// Immutable test images; Renovate updates them (renovate.json test-image
// manager), and images_test.go keeps RyukImage equal to the root pin.
const (
	// LowkeyVaultImage is the Azure Key Vault emulator the signer tests run
	// against.
	LowkeyVaultImage = "nagyesta/lowkey-vault:7.3.112@sha256:2636ad677e0e9b98f16b82f36dae16748bcb0d3eee7c9ac6d357f9e9a233be3a"
	// RyukImage is the testcontainers resource reaper, as in
	// internal/testimages.
	RyukImage = "testcontainers/ryuk:0.14.0@sha256:7c1a8a9a47c780ed0f983770a662f80deb115d95cce3e2daa3d12115b8cd28f0"
)
