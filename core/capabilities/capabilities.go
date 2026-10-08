// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package capabilities defines the framework's machine-readable capability
// contract: the schema of the repository-root capabilities.yaml that maps
// every golusoris package to the capability keys downstream manifests
// (.needs.yaml) demand, and the third-party modules each package replaces.
// Governance tooling answers "does the framework cover X, and with what?"
// against this file instead of guessing from directory names.
package capabilities

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/golusoris/golusoris/core/codec/yaml"
)

// SchemaVersion is the only `version` this package accepts.
const SchemaVersion = 1

// FileName is the conventional location, relative to the framework repo root.
const FileName = "capabilities.yaml"

// Sentinel errors. Compare with errors.Is.
var (
	ErrSchemaVersion = errors.New("capabilities: unsupported schema version")
	ErrInvalid       = errors.New("capabilities: invalid index")
)

// keyRE is the capability key grammar: `domain.name[.sub]`, lowercase; the
// domain starts with a letter, later elements may start with a digit (media.3d).
var keyRE = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z0-9][a-z0-9_]*)+$`)

// Status is a package's maturity.
type Status string

// Maturity levels.
const (
	StatusStable       Status = "stable"
	StatusBeta         Status = "beta"
	StatusExperimental Status = "experimental"
)

// Package is one framework package and the capabilities it provides.
type Package struct {
	// Import is the full Go import path.
	Import string `yaml:"import" json:"import"`
	// Module is the Go module containing the package; empty means the
	// framework root module.
	Module string `yaml:"module,omitempty" json:"module,omitempty"`
	// Domain groups packages (db, http, auth, …); informational.
	Domain string `yaml:"domain" json:"domain"`
	// Capabilities are the keys this package satisfies, e.g. "db.postgres".
	Capabilities []string `yaml:"capabilities" json:"capabilities"`
	// Description is a one-line purpose.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Status is the maturity level (default stable).
	Status Status `yaml:"status,omitempty" json:"status,omitempty"`
	// Replaces lists third-party module paths this package supersedes, so
	// migration tooling can derive its catalog from the framework itself.
	Replaces []string `yaml:"replaces,omitempty" json:"replaces,omitempty"`
}

// Index is the parsed capabilities.yaml.
type Index struct {
	Version   int       `yaml:"version" json:"version"`
	Framework string    `yaml:"framework" json:"framework"`
	Modules   []string  `yaml:"modules" json:"modules"`
	Packages  []Package `yaml:"packages" json:"packages"`
}

// Parse decodes and validates an index.
func Parse(data []byte) (*Index, error) {
	var idx Index
	if err := yaml.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("capabilities: %w", err)
	}
	if err := idx.Validate(); err != nil {
		return nil, err
	}
	return &idx, nil
}

// Load reads and validates the index at path.
func Load(path string) (*Index, error) {
	var idx Index
	if err := yaml.ReadFile(path, &idx); err != nil {
		return nil, fmt.Errorf("capabilities: %w", err)
	}
	if err := idx.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &idx, nil
}

// Validate enforces the schema invariants: supported version, framework set,
// unique imports under the framework prefix, at least one well-formed key per
// package, known status, and modules referenced by packages declared in
// Modules.
func (idx *Index) Validate() error {
	if idx.Version != SchemaVersion {
		return fmt.Errorf("%w: %d (want %d)", ErrSchemaVersion, idx.Version, SchemaVersion)
	}
	if idx.Framework == "" {
		return fmt.Errorf("%w: framework must be set", ErrInvalid)
	}
	modules := make(map[string]bool, len(idx.Modules)+1)
	modules[idx.Framework] = true
	for _, m := range idx.Modules {
		if m == idx.Framework || !withinModule(m, idx.Framework) {
			return fmt.Errorf("%w: invalid module %q for framework %s", ErrInvalid, m, idx.Framework)
		}
		if modules[m] {
			return fmt.Errorf("%w: duplicate module %s", ErrInvalid, m)
		}
		modules[m] = true
	}
	seen := make(map[string]bool, len(idx.Packages))
	for i := range idx.Packages {
		if err := idx.validatePackage(&idx.Packages[i], seen, modules); err != nil {
			return err
		}
	}
	return nil
}

func (idx *Index) validatePackage(p *Package, seen, modules map[string]bool) error {
	if err := idx.validatePackageIdentity(p, seen, modules); err != nil {
		return err
	}
	seen[p.Import] = true
	if err := validateCapabilities(p); err != nil {
		return err
	}
	if err := validateReplacements(p); err != nil {
		return err
	}
	switch p.Status {
	case "", StatusStable, StatusBeta, StatusExperimental:
		return nil
	default:
		return fmt.Errorf("%w: %s: unknown status %q", ErrInvalid, p.Import, p.Status)
	}
}

func (idx *Index) validatePackageIdentity(p *Package, seen, modules map[string]bool) error {
	owner := p.Module
	if owner == "" {
		owner = idx.Framework
	}
	switch {
	case p.Import == "":
		return fmt.Errorf("%w: package with empty import", ErrInvalid)
	case !withinModule(p.Import, idx.Framework):
		return fmt.Errorf("%w: %s is outside %s", ErrInvalid, p.Import, idx.Framework)
	case seen[p.Import]:
		return fmt.Errorf("%w: duplicate import %s", ErrInvalid, p.Import)
	case len(p.Capabilities) == 0:
		return fmt.Errorf("%w: %s declares no capabilities", ErrInvalid, p.Import)
	case p.Module != "" && !modules[p.Module]:
		return fmt.Errorf("%w: %s references undeclared module %s", ErrInvalid, p.Import, p.Module)
	case owner != mostSpecificModule(p.Import, modules):
		return fmt.Errorf("%w: %s belongs to module %s, not %s", ErrInvalid, p.Import, mostSpecificModule(p.Import, modules), owner)
	}
	return nil
}

func validateCapabilities(p *Package) error {
	capabilities := make(map[string]bool, len(p.Capabilities))
	for _, k := range p.Capabilities {
		if !keyRE.MatchString(k) {
			return fmt.Errorf("%w: %s: malformed capability key %q", ErrInvalid, p.Import, k)
		}
		if capabilities[k] {
			return fmt.Errorf("%w: %s: duplicate capability %q", ErrInvalid, p.Import, k)
		}
		capabilities[k] = true
	}
	return nil
}

func validateReplacements(p *Package) error {
	replacements := make(map[string]bool, len(p.Replaces))
	for _, replacement := range p.Replaces {
		if replacement == "" {
			return fmt.Errorf("%w: %s: empty replacement", ErrInvalid, p.Import)
		}
		if replacements[replacement] {
			return fmt.Errorf("%w: %s: duplicate replacement %q", ErrInvalid, p.Import, replacement)
		}
		replacements[replacement] = true
	}
	return nil
}

func withinModule(importPath, module string) bool {
	return importPath == module || strings.HasPrefix(importPath, module+"/")
}

func mostSpecificModule(importPath string, modules map[string]bool) string {
	owner := ""
	for module := range modules {
		if withinModule(importPath, module) && len(module) > len(owner) {
			owner = module
		}
	}
	return owner
}

// ByCapability maps each key to the sorted import paths providing it.
func (idx *Index) ByCapability() map[string][]string {
	out := make(map[string][]string)
	for _, p := range idx.Packages {
		for _, k := range p.Capabilities {
			out[k] = append(out[k], p.Import)
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

// Covers reports whether at least one package provides key.
func (idx *Index) Covers(key string) bool {
	for _, p := range idx.Packages {
		if slices.Contains(p.Capabilities, key) {
			return true
		}
	}
	return false
}

// Lookup returns the package with the given import path.
func (idx *Index) Lookup(importPath string) (Package, bool) {
	for _, p := range idx.Packages {
		if p.Import == importPath {
			return p, true
		}
	}
	return Package{}, false
}

// Replacements maps every third-party module path listed in Replaces to the
// last framework import that declares it. It preserves the legacy declaration-
// order behavior; new consumers should use ReplacementCandidates.
func (idx *Index) Replacements() map[string]string {
	out := make(map[string]string)
	for _, p := range idx.Packages {
		for _, r := range p.Replaces {
			out[r] = p.Import
		}
	}
	return out
}

// ReplacementCandidates maps every third-party module path listed in Replaces
// to every sorted framework import that supersedes it. Consumers resolve their
// own imports against these keys with longest-prefix matching (see
// astx.Resolve), then choose a provider using the demanded capability rather
// than declaration order.
func (idx *Index) ReplacementCandidates() map[string][]string {
	out := make(map[string][]string)
	for _, p := range idx.Packages {
		for _, r := range p.Replaces {
			out[r] = append(out[r], p.Import)
		}
	}
	for replacement := range out {
		sort.Strings(out[replacement])
	}
	return out
}

// Keys returns every capability key, sorted and de-duplicated.
func (idx *Index) Keys() []string {
	by := idx.ByCapability()
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
