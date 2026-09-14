// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package log_test

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/golusoris/golusoris/core/log"
)

// ExampleNew shows building a JSON logger and emitting a structured record.
// FormatJSON keeps the output deterministic for the example harness.
func ExampleNew() {
	var buf bytes.Buffer
	logger := log.New(log.Options{Format: log.FormatJSON, Output: &buf})
	logger.Info("ping", "service", "demo")

	out := buf.String()
	fmt.Println(strings.Contains(out, `"msg":"ping"`))
	fmt.Println(strings.Contains(out, `"service":"demo"`))
	// Output:
	// true
	// true
}
