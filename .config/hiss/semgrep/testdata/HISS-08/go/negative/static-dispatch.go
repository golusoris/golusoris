// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// Handler is a statically linked capability: the implementation is chosen by
// the compiler and the linker, never at run time.
type Handler interface{ Handle(string) error }

// Dispatch calls a capability supplied by the caller.
func Dispatch(h Handler, arg string) error {
	return h.Handle(arg)
}
