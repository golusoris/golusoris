package nats_test

import (
	"testing"

	"github.com/golusoris/golusoris/pubsub/nats"
)

// Integration tests require a running NATS server and are skipped here.
// The package surface is verified at build time.

func TestPackage_compiles(_ *testing.T) {
	// Verify the package exports are reachable.
	_ = nats.Module
}
