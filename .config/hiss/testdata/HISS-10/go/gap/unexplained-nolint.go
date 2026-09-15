// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p suppresses a linter without saying why.
package p

import "os"

// Remove suppresses errcheck with a bare directive and no explanation, so the
// reason the error may be dropped is recorded nowhere. nolintlint is enabled in
// .golangci.yml but runs with require-explanation and require-specific unset,
// so it accepts the bare directive and reports nothing.
func Remove(path string) {
	os.Remove(path) //nolint:errcheck
}
