package pkga

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

// TestHoldReaper starts one container so this process becomes a Ryuk client,
// then exits. Ryuk's reconnection timer starts when this process disconnects.
func TestHoldReaper(t *testing.T) {
	ctx := context.Background()
	c, err := testcontainers.Run(ctx, "alpine:3.20", testcontainers.WithCmd("sleep", "300"))
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Logf("HARNESS pkga session=%s container=%s", testcontainers.SessionID()[:12], c.GetContainerID()[:12])
}
