// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package id_test

import (
	"fmt"

	"github.com/golusoris/golusoris/core/id"
)

// ExampleNew shows obtaining the default generator and producing both flavors.
func ExampleNew() {
	g := id.New()
	u, err := g.NewUUID()
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	k := g.NewKSUID()
	fmt.Println("uuid version:", u.Version())
	fmt.Println("ksuid len:", len(k.String()))
	// Output:
	// uuid version: VERSION_7
	// ksuid len: 27
}
