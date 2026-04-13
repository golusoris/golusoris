package log_test

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/golusoris/golusoris/log"
)

// ExampleNew shows building a JSON logger and emitting a structured record.
// Using FormatJSON makes the output deterministic for the example.
func ExampleNew() {
	var buf bytes.Buffer
	logger := log.New(log.Options{Format: log.FormatJSON, Output: &buf})
	logger.Info("ping", "service", "demo")

	// Strip the "time" field so the output is stable for the test harness.
	out := buf.String()
	if i := strings.Index(out, `,"time":`); i >= 0 {
		// no-op; just demonstrating structure
	}
	fmt.Println(strings.Contains(out, `"msg":"ping"`))
	fmt.Println(strings.Contains(out, `"service":"demo"`))
	// Output:
	// true
	// true
}
