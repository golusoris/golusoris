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
	LowkeyVaultImage = "nagyesta/lowkey-vault:7.3.145@sha256:28c94fca06e22e262f8960d71b722ae7e30ecb75ae8fb4b325c66f271768a1b3"
	// RyukImage is the testcontainers resource reaper, as in
	// internal/testimages.
	RyukImage = "testcontainers/ryuk:0.14.0@sha256:7c1a8a9a47c780ed0f983770a662f80deb115d95cce3e2daa3d12115b8cd28f0"
)
