package id_test

import (
	"fmt"

	"github.com/golusoris/golusoris/id"
)

// ExampleNew shows obtaining the default generator and producing both flavors.
func ExampleNew() {
	g := id.New()
	u := g.NewUUID()
	k := g.NewKSUID()
	fmt.Println("uuid version:", u.Version())
	fmt.Println("ksuid len:", len(k.String()))
	// Output:
	// uuid version: VERSION_7
	// ksuid len: 27
}
