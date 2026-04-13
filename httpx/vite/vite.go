// Package vite reads Vite's build manifest.json and resolves entry points to
// hashed asset URLs. No Go runtime dependencies beyond stdlib; apps `vite
// build` in their frontend pipeline and this package teaches Go templates +
// API responses how to find the hashed files.
//
// Manifest schema: https://vite.dev/guide/backend-integration.html
package vite

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"sync"
)

// Entry is a single manifest entry. Not all fields are always populated;
// check nilness before using.
type Entry struct {
	File           string   `json:"file"`
	Src            string   `json:"src,omitempty"`
	IsEntry        bool     `json:"isEntry,omitempty"`
	IsDynamicEntry bool     `json:"isDynamicEntry,omitempty"`
	CSS            []string `json:"css,omitempty"`
	Assets         []string `json:"assets,omitempty"`
	Imports        []string `json:"imports,omitempty"`
	DynamicImports []string `json:"dynamicImports,omitempty"`
}

// Manifest is a parsed Vite manifest.json, keyed by entry source path
// (e.g. "src/main.tsx").
type Manifest struct {
	mu      sync.RWMutex
	entries map[string]Entry
}

// NewFromBytes parses raw manifest JSON.
func NewFromBytes(b []byte) (*Manifest, error) {
	var entries map[string]Entry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("vite: parse manifest: %w", err)
	}
	return &Manifest{entries: entries}, nil
}

// NewFromFile loads manifest.json from a file path.
func NewFromFile(path string) (*Manifest, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- caller-supplied manifest path is trusted (build artifact)
	if err != nil {
		return nil, fmt.Errorf("vite: read manifest: %w", err)
	}
	return NewFromBytes(b)
}

// NewFromFS loads manifest.json from a filesystem (e.g. embed.FS).
func NewFromFS(fsys fs.FS, path string) (*Manifest, error) {
	b, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("vite: read manifest: %w", err)
	}
	return NewFromBytes(b)
}

// Entry returns the manifest entry for src or (zero, false).
func (m *Manifest) Entry(src string) (Entry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[src]
	return e, ok
}

// File returns the hashed built filename for src (e.g. "assets/main-abc123.js").
// Returns "" if the entry is missing.
func (m *Manifest) File(src string) string {
	e, ok := m.Entry(src)
	if !ok {
		return ""
	}
	return e.File
}

// CSS returns all CSS files associated with src, including CSS from its
// transitive JS imports (manifest-reported). Useful for emitting
// <link rel="stylesheet"> tags server-side.
func (m *Manifest) CSS(src string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return collectCSS(m.entries, src, map[string]bool{})
}

func collectCSS(entries map[string]Entry, src string, seen map[string]bool) []string {
	if seen[src] {
		return nil
	}
	seen[src] = true
	e, ok := entries[src]
	if !ok {
		return nil
	}
	out := append([]string(nil), e.CSS...)
	for _, imp := range e.Imports {
		out = append(out, collectCSS(entries, imp, seen)...)
	}
	return out
}
