package crypto_test

import (
	"bytes"
	"fmt"

	"github.com/golusoris/golusoris/crypto"
)

// ExampleHashPassword demonstrates hashing + verifying a password.
func ExampleHashPassword() {
	hash, _ := crypto.HashPassword("hunter2")
	ok, _, _ := crypto.VerifyPassword("hunter2", hash)
	fmt.Println("match:", ok)
	// Output: match: true
}

// ExampleSeal demonstrates AES-GCM round-trip with a random 256-bit key.
func ExampleSeal() {
	key, _ := crypto.RandomBytes(32)
	sealed, _ := crypto.Seal(key, []byte("the launch codes"))
	out, _ := crypto.Open(key, sealed)
	fmt.Println(bytes.Equal(out, []byte("the launch codes")))
	// Output: true
}
