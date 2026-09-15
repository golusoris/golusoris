// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import "plugin"

// Load maps a shared object chosen at run time into the process and calls a
// symbol no compiler, linter or SBOM ever saw.
func Load(path string) error {
	pl, err := plugin.Open(path)
	if err != nil {
		return err
	}
	_, err = pl.Lookup("Handler")
	return err
}
