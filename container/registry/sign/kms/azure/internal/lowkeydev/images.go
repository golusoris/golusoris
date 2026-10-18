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
	LowkeyVaultImage = "nagyesta/lowkey-vault:7.3.127@sha256:d92adc63759571b2ef68e84f4fad94e6d9603c1faf9d338a2d90c2302303c952"
	// RyukImage is the testcontainers resource reaper, as in
	// internal/testimages.
	RyukImage = "testcontainers/ryuk:0.14.0@sha256:7c1a8a9a47c780ed0f983770a662f80deb115d95cce3e2daa3d12115b8cd28f0"
)
