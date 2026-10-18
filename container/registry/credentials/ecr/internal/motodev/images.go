// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package motodev pins the Moto test images. It lives in this
// module, not in root internal/testimages, so the module's tests need no
// root golusoris release.
package motodev

// Immutable test images; Renovate updates them (renovate.json test-image
// manager), and images_test.go keeps RyukImage equal to the root pin.
const (
	// MotoImage is the AWS API emulator for container credential and SDK
	// integration tests.
	MotoImage = "motoserver/moto:5.2.4@sha256:120b1589c6df7e35d2092935f6ff724486bb2c4189d0e36ce795baba1400ffd2"
	// RyukImage is the testcontainers resource reaper, as in
	// internal/testimages.
	RyukImage = "testcontainers/ryuk:0.14.0@sha256:7c1a8a9a47c780ed0f983770a662f80deb115d95cce3e2daa3d12115b8cd28f0"
)
