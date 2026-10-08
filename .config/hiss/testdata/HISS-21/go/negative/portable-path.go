// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package p

import "path/filepath"

// PortablePath delegates separator choice to the current host.
func PortablePath(base, name string) string {
	return filepath.Join(base, name)
}
