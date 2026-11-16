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
	maxBytes         = 1 << 20
	maxNodes         = 65536
	maxScalarBytes   = 2 * maxBytes
	maxResponseBytes = 8 * maxBytes
)

var errResponseBound = errors.New("workflow parser: response byte bound exceeded")

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
	if err = ctx.Err(); err != nil {
		return fmt.Errorf("workflow parser: output context: %w", err)
	}
	encoded, err := marshalResponse(document)
	if errors.Is(err, errResponseBound) {
		encoded, err = marshalResponse(response{Error: errResponseBound.Error()})
	}
	if err != nil {
		return fmt.Errorf("workflow parser: serialize result: %w", err)
	}
	if _, err = encoded.WriteTo(out); err != nil {
		return fmt.Errorf("workflow parser: encode result: %w", err)
	}
	return nil
}

func appendResponse(buffer *bytes.Buffer, piece []byte) error {
	if len(piece) > maxResponseBytes-buffer.Len() {
		return errResponseBound
	}
	if _, err := buffer.Write(piece); err != nil {
		return fmt.Errorf("workflow parser: buffer response: %w", err)
	}
	return nil
}

func marshalResponse(document response) (*bytes.Buffer, error) {
	envelope, err := json.Marshal(response{Scalars: document.Scalars[:0], Error: document.Error})
	if err != nil {
		return nil, fmt.Errorf("workflow parser: marshal envelope: %w", err)
	}
	// Fixed capacity prevents buffer growth from allocating beyond the wire bound.
	buffer := bytes.NewBuffer(make([]byte, 0, maxResponseBytes))
	if document.Scalars == nil {
		return buffer, appendResponse(buffer, append(envelope, '\n'))
	}
	const prefix = `{"scalars":[`
	if err := appendResponse(buffer, []byte(prefix)); err != nil {
		return nil, err
	}
	for i, item := range document.Scalars[:min(len(document.Scalars), maxNodes)] {
		piece, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("workflow parser: marshal scalar: %w", err)
		}
		if i > 0 {
			if err := appendResponse(buffer, []byte(",")); err != nil {
				return nil, err
			}
		}
		if err := appendResponse(buffer, piece); err != nil {
			return nil, err
		}
	}
	if err := appendResponse(buffer, append(envelope[len(prefix):], '\n')); err != nil {
		return nil, err
	}
	return buffer, nil
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
