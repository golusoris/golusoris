// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package astx

import (
	"fmt"

	"golang.org/x/mod/modfile"
)

// MaxGoModSize bounds a go.mod read (1 MiB — real files are a few KiB).
const MaxGoModSize int64 = 1 << 20

// GoMod is the subset of go.mod that tooling reads.
type GoMod struct {
	Module  string        `json:"module"`
	Go      string        `json:"go,omitempty"`
	Require []Requirement `json:"require,omitempty"`
}

// Requirement is one require directive.
type Requirement struct {
	Path     string `json:"path"`
	Version  string `json:"version"`
	Indirect bool   `json:"indirect,omitempty"`
}

// ParseGoMod reads and parses the go.mod at path with golang.org/x/mod, the
// same parser the go command uses — no hand-rolled line scanning.
func ParseGoMod(path string) (*GoMod, error) {
	data, err := ReadFileBounded(path, MaxGoModSize)
	if err != nil {
		return nil, err
	}
	f, err := modfile.ParseLax(path, data, nil)
	if err != nil {
		return nil, fmt.Errorf("astx: parse %s: %w", path, err)
	}
	out := &GoMod{}
	if f.Module != nil {
		out.Module = f.Module.Mod.Path
	}
	if f.Go != nil {
		out.Go = f.Go.Version
	}
	out.Require = make([]Requirement, 0, len(f.Require))
	for _, r := range f.Require {
		out.Require = append(out.Require, Requirement{Path: r.Mod.Path, Version: r.Mod.Version, Indirect: r.Indirect})
	}
	return out, nil
}

// Direct returns the non-indirect requirements.
func (g *GoMod) Direct() []Requirement {
	out := make([]Requirement, 0, len(g.Require))
	for _, r := range g.Require {
		if !r.Indirect {
			out = append(out, r)
		}
	}
	return out
}
