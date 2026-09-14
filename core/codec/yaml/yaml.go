// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package yaml is the framework's single YAML codec. It wraps go.yaml.in/yaml/v3
// (the maintained successor of gopkg.in/yaml.v3 that koanf already pins) so
// every app and tool in the fleet shares one pinned parser instead of each
// requiring its own. Decoding is bounded by [MaxDocumentSize] and strict by
// default — unknown fields are an error, which is what manifests and lockfiles
// want. Set [Options.Lenient] for forgiving reads.
package yaml

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	yamlv3 "go.yaml.in/yaml/v3"
)

// MaxDocumentSize bounds a single decode (Power-of-10 rule 2: no unbounded
// input). Manifests are KiB-scale; 8 MiB leaves generous headroom.
const MaxDocumentSize int64 = 8 << 20

// DefaultIndent is the indent used when encoding (yaml.v3 itself defaults to 4).
const DefaultIndent = 2

// ErrTooLarge is returned when the input exceeds the configured size limit.
var ErrTooLarge = errors.New("yaml: document exceeds size limit")

// Options tunes a codec. The zero value is strict decoding, [DefaultIndent]
// encoding, and the [MaxDocumentSize] input bound.
type Options struct {
	// Lenient allows unknown fields when decoding into structs.
	Lenient bool
	// Indent overrides DefaultIndent for encoding (0 = default).
	Indent int
	// MaxSize overrides MaxDocumentSize for decoding (0 = default).
	MaxSize int64
}

func (o Options) indent() int {
	if o.Indent > 0 {
		return o.Indent
	}
	return DefaultIndent
}

func (o Options) maxSize() int64 {
	if o.MaxSize > 0 {
		return o.MaxSize
	}
	return MaxDocumentSize
}

// Marshal encodes v as a YAML document with [DefaultIndent].
func Marshal(v any) ([]byte, error) { return Options{}.Marshal(v) }

// Marshal encodes v as a YAML document.
func (o Options) Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := o.Encode(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Encode writes v as a YAML document to w.
func (o Options) Encode(w io.Writer, v any) error {
	enc := yamlv3.NewEncoder(w)
	enc.SetIndent(o.indent())
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("yaml: encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("yaml: close encoder: %w", err)
	}
	return nil
}

// Unmarshal decodes data into v, rejecting unknown fields.
func Unmarshal(data []byte, v any) error { return Options{}.Unmarshal(data, v) }

// UnmarshalLenient decodes data into v, ignoring unknown fields.
func UnmarshalLenient(data []byte, v any) error {
	return Options{Lenient: true}.Unmarshal(data, v)
}

// Unmarshal decodes data into v.
func (o Options) Unmarshal(data []byte, v any) error {
	if int64(len(data)) > o.maxSize() {
		return fmt.Errorf("%w: %d bytes > %d", ErrTooLarge, len(data), o.maxSize())
	}
	return o.Decode(bytes.NewReader(data), v)
}

// Decode reads one YAML document from r into v. Reads stop at MaxSize+1 bytes
// so an oversized stream fails closed instead of being buffered whole. An
// empty document leaves v untouched, matching yaml.v3 Unmarshal.
func (o Options) Decode(r io.Reader, v any) error {
	limit := o.maxSize()
	data, err := io.ReadAll(&io.LimitedReader{R: r, N: limit + 1})
	if err != nil {
		return fmt.Errorf("yaml: read: %w", err)
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("%w: more than %d bytes", ErrTooLarge, limit)
	}
	dec := yamlv3.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(!o.Lenient)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("yaml: decode: %w", err)
	}
	return nil
}

// ReadFile decodes the YAML file at path into v (strict).
func ReadFile(path string, v any) error { return Options{}.ReadFile(path, v) }

// ReadFile decodes the YAML file at path into v.
func (o Options) ReadFile(path string, v any) error {
	f, err := os.Open(path) //nolint:gosec // G304: manifest path is caller-controlled by design // #nosec G304
	if err != nil {
		return fmt.Errorf("yaml: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if err := o.Decode(f, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// WriteFile encodes v and writes it to path atomically (temp file + rename in
// the same directory) with perm, so a crash never leaves a half-written
// manifest behind.
func WriteFile(path string, v any, perm os.FileMode) error {
	return Options{}.WriteFile(path, v, perm)
}

// WriteFile encodes v and writes it to path atomically with perm.
func (o Options) WriteFile(path string, v any, perm os.FileMode) error {
	data, err := o.Marshal(v)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("yaml: create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if err := writeAndClose(tmp, data, perm); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("yaml: rename %s: %w", path, err)
	}
	return nil
}

func writeAndClose(f *os.File, data []byte, perm os.FileMode) error {
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("yaml: write %s: %w", f.Name(), err)
	}
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return fmt.Errorf("yaml: chmod %s: %w", f.Name(), err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("yaml: close %s: %w", f.Name(), err)
	}
	return nil
}
