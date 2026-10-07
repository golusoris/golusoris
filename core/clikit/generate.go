// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package clikit

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

// MaxGeneratedFiles bounds the directory walk of [CheckDrift].
const MaxGeneratedFiles = 8192

// ErrDrift is returned by [CheckDrift] when a directory no longer matches
// the generated files.
var ErrDrift = errors.New("clikit: generated files drifted")

// errTooManyFiles stops the [CheckDrift] walk at [MaxGeneratedFiles].
var errTooManyFiles = errors.New("clikit: drift: directory exceeds MaxGeneratedFiles")

// Generate renders root's completion script for every shell in [Shells] and
// its [ManPages], keyed by slash-separated paths relative to an output
// directory (completions/<shell>/..., man/man<section>/...). Output is
// byte-reproducible for a given tree and opts.
func Generate(root *cobra.Command, opts ManOptions) (map[string][]byte, error) {
	files, err := ManPages(root, opts)
	if err != nil {
		return nil, err
	}
	for _, shell := range Shells() {
		var buf bytes.Buffer
		if werr := WriteCompletion(&buf, root, shell); werr != nil {
			return nil, werr
		}
		files[completionPath(root.Name(), shell)] = buf.Bytes()
	}
	return files, nil
}

// WriteFiles writes files below dir, creating parent directories. Names are
// slash-separated and must stay inside dir.
func WriteFiles(dir string, files map[string][]byte) error {
	for _, name := range sortedNames(files) {
		path, err := localPath(dir, name)
		if err != nil {
			return err
		}
		// #nosec G301 -- man and completion directories are world-readable by design
		if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
			return fmt.Errorf("clikit: write %s: %w", name, mkErr)
		}
		// #nosec G306 -- man pages and completion scripts are world-readable by design
		if wErr := os.WriteFile(path, files[name], 0o644); wErr != nil {
			return fmt.Errorf("clikit: write %s: %w", name, wErr)
		}
	}
	return nil
}

// CheckDrift returns nil when dir holds exactly files, byte for byte.
// Otherwise the error wraps [ErrDrift] and names every stale, missing and
// unexpected file, so a CI step can fail on a checked-in copy that no longer
// matches [Generate]. dir is treated as owned by the generator.
func CheckDrift(dir string, files map[string][]byte) error {
	var stale, missing []string
	for _, name := range sortedNames(files) {
		path, err := localPath(dir, name)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(path) // #nosec G304 -- localPath keeps name inside dir
		switch {
		case errors.Is(err, fs.ErrNotExist):
			missing = append(missing, name)
		case err != nil:
			return fmt.Errorf("clikit: drift: %w", err)
		case !bytes.Equal(got, files[name]):
			stale = append(stale, name)
		}
	}
	extra, err := unexpectedFiles(dir, files)
	if err != nil {
		return err
	}
	if len(stale)+len(missing)+len(extra) == 0 {
		return nil
	}
	return fmt.Errorf("%w in %s: %s", ErrDrift, dir, driftSummary(stale, missing, extra))
}

func unexpectedFiles(dir string, files map[string][]byte) ([]string, error) {
	var extra []string
	seen := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if seen++; seen > MaxGeneratedFiles {
			return errTooManyFiles
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return fmt.Errorf("clikit: drift: %w", relErr)
		}
		if _, ok := files[filepath.ToSlash(rel)]; !ok {
			extra = append(extra, filepath.ToSlash(rel))
		}
		return nil
	})
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("clikit: drift: %w", err)
	}
	return extra, nil
}

func driftSummary(stale, missing, extra []string) string {
	parts := make([]string, 0, 3)
	for _, group := range []struct {
		label string
		names []string
	}{{"stale", stale}, {"missing", missing}, {"unexpected", extra}} {
		if len(group.names) > 0 {
			parts = append(parts, group.label+": "+strings.Join(group.names, ", "))
		}
	}
	return strings.Join(parts, "; ")
}

func localPath(dir, name string) (string, error) {
	rel := filepath.FromSlash(name)
	if name == "" || strings.Contains(name, `\`) || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("clikit: file name %q is not a local slash path", name)
	}
	return filepath.Join(dir, rel), nil
}

func sortedNames(files map[string][]byte) []string { return slices.Sorted(maps.Keys(files)) }
