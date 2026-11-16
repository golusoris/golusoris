// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Command workflow_actor_parser decodes one workflow YAML document into bounded scalar and expression facts.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"go.yaml.in/yaml/v4"
)

const (
	maxBytes       = 1 << 20
	maxNodes       = 65536
	maxScalarBytes = 2 * maxBytes
)

// JSON keys are the Python policy protocol; rename on both sides together.
type scalar struct {
	Path    []string   `json:"path"`
	Text    string     `json:"text"`
	Line    int        `json:"line"`
	KeyLine int        `json:"key_line"`
	Key     bool       `json:"key"`
	Alias   bool       `json:"alias"`
	Style   yaml.Style `json:"style"`
	Refs    []string   `json:"refs"`
	Calls   []string   `json:"calls"`
	Error   string     `json:"error"`
}

type response struct {
	Scalars []scalar `json:"scalars"`
	Error   string   `json:"error"`
}

func parseDocument(data []byte) response {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		return response{Error: fmt.Sprintf("workflow parser: decode YAML: %v", err)}
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return response{Error: "workflow parser: exactly one YAML document required"}
	}
	scalars, err := flatten(&doc)
	if err != nil {
		return response{Error: err.Error()}
	}
	return response{Scalars: scalars}
}

func run(ctx context.Context, in io.Reader, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("workflow parser: input context: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(in, maxBytes+1))
	if err != nil {
		return fmt.Errorf("workflow parser: read input: %w", err)
	}
	if len(data) > maxBytes {
		return errors.New("workflow parser: input exceeds byte bound")
	}
	document := parseDocument(data)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("workflow parser: output context: %w", err)
	}
	if err := json.NewEncoder(out).Encode(document); err != nil {
		return fmt.Errorf("workflow parser: encode result: %w", err)
	}
	return nil
}

// exitCode runs with all defers settled before main calls os.Exit.
func exitCode(logger *slog.Logger) int {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() {
		if err := errors.Join(os.Stdin.Close(), os.Stdout.Close()); err != nil {
			logger.Error("workflow parser: close timed out I/O", "error", err)
		}
	})
	defer stop()
	if err := run(ctx, os.Stdin, os.Stdout); err != nil {
		logger.Error("workflow parser", "error", err)
		return 3
	}
	return 0
}

func main() {
	os.Exit(exitCode(slog.New(slog.NewTextHandler(os.Stderr, nil))))
}
