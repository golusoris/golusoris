// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// Notify spawns a named function per element rather than a closure; the
// unbounded shape is identical.
func Notify(addrs []string, send func(string)) {
	for _, addr := range addrs {
		go send(addr)
	}
}
