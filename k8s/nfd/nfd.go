// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package nfd publishes node labels through the Node Feature Discovery
// (NFD) local feature source: it writes feature files into the directory
// nfd-worker reads (features.d), so a node agent can advertise which
// backends and devices its node offers without RBAC on Node objects.
//
// File format follows NFD v0.19.0 (docs/usage/customization-guide.md,
// "Local feature source"; source/local/local.go): one `<key>=<value>` line
// per label, `#` comment lines, and an optional `# +expiry-time=<RFC3339>`
// directive after which the labels are ignored. Files are capped at 64 KiB,
// dot files are ignored, and writers must create a temporary file and
// rename it into place. Label rules mirror NFD's pkg/apis/nfd/validate:
// keys must carry a DNS-subdomain prefix, the kubernetes.io namespace is
// denied except feature.node.kubernetes.io and profile.node.kubernetes.io
// (and their subdomains), and values are Kubernetes label values.
//
// [WriteFeatureFile] and [WriteFeatureFileUntil] are stateless; [Module]
// rewrites the file on start and on a refresh period so the expiry keeps
// moving while the process lives.
package nfd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/validate/content"
)

// DefaultDir is the features.d directory nfd-worker reads.
const DefaultDir = "/etc/kubernetes/node-feature-discovery/features.d"

// MaxFileSize is NFD's feature file limit (local.MaxFeatureFileSize); larger
// files are ignored by nfd-worker.
const MaxFileSize = 65536

// maxNameLen keeps the dot-prefixed temp name (name plus a random suffix)
// under the common 255-byte file name limit.
const maxNameLen = 200

// fileMode lets nfd-worker read the file whatever user it runs as.
const fileMode os.FileMode = 0o644

// Label namespaces NFD accepts inside the denied kubernetes.io namespace.
const (
	featureNS = "feature.node.kubernetes.io"
	profileNS = "profile.node.kubernetes.io"
)

var (
	// ErrInvalidName reports a feature file name nfd-worker would skip or
	// that escapes the features directory.
	ErrInvalidName = errors.New("nfd: invalid feature file name")
	// ErrInvalidLabel reports a label key or value NFD would reject.
	ErrInvalidLabel = errors.New("nfd: invalid label")
	// ErrTooLarge reports a rendered file above [MaxFileSize].
	ErrTooLarge = errors.New("nfd: feature file exceeds the 64 KiB NFD limit")
)

// WriteFeatureFile atomically writes labels to dir/name without an expiry.
func WriteFeatureFile(dir, name string, labels map[string]string) error {
	return WriteFeatureFileUntil(dir, name, labels, time.Time{})
}

// WriteFeatureFileUntil atomically writes labels to dir/name with a
// `# +expiry-time` directive set to expiry; a zero expiry omits it. The file
// is written to a dot-prefixed temporary in dir and renamed into place, so
// nfd-worker never reads a partial file.
func WriteFeatureFileUntil(dir, name string, labels map[string]string, expiry time.Time) error {
	if err := validateName(name); err != nil {
		return err
	}
	body, err := render(labels, expiry)
	if err != nil {
		return err
	}
	return writeAtomic(dir, name, body)
}

// validateName checks that name is a single, non-hidden path element.
func validateName(name string) error {
	switch {
	case name == "", len(name) > maxNameLen:
		return fmt.Errorf("%w: length %d not in 1..%d", ErrInvalidName, len(name), maxNameLen)
	case strings.HasPrefix(name, "."):
		return fmt.Errorf("%w: %q is a dot file, which nfd-worker ignores", ErrInvalidName, name)
	case strings.ContainsAny(name, `/\`) || filepath.Base(name) != name:
		return fmt.Errorf("%w: %q is not a single path element", ErrInvalidName, name)
	}
	return nil
}

// ValidateLabel checks key and value against NFD's label rules.
func ValidateLabel(key, value string) error {
	if errs := content.IsPrefixedLabelKey(key); len(errs) > 0 {
		return fmt.Errorf("%w: key %q: %s", ErrInvalidLabel, key, strings.Join(errs, "; "))
	}
	ns, _, _ := strings.Cut(key, "/")
	if deniedNamespace(ns) {
		return fmt.Errorf("%w: key %q: namespace %q is reserved", ErrInvalidLabel, key, ns)
	}
	if errs := content.IsLabelValue(value); len(errs) > 0 {
		return fmt.Errorf("%w: value of %q: %s", ErrInvalidLabel, key, strings.Join(errs, "; "))
	}
	return nil
}

func deniedNamespace(ns string) bool {
	if ns != "kubernetes.io" && !strings.HasSuffix(ns, ".kubernetes.io") {
		return false
	}
	allowed := ns == featureNS || ns == profileNS ||
		strings.HasSuffix(ns, "."+featureNS) || strings.HasSuffix(ns, "."+profileNS)
	return !allowed
}

// render returns the feature file body for labels, keys sorted, with an
// expiry directive first when expiry is non-zero.
func render(labels map[string]string, expiry time.Time) ([]byte, error) {
	keys := make([]string, 0, len(labels))
	for k, v := range labels {
		if err := ValidateLabel(k, v); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var buf bytes.Buffer
	if !expiry.IsZero() {
		buf.WriteString("# +expiry-time=" + expiry.UTC().Format(time.RFC3339) + "\n")
	}
	for _, k := range keys {
		buf.WriteString(k + "=" + labels[k] + "\n")
	}
	if buf.Len() > MaxFileSize {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, buf.Len())
	}
	return buf.Bytes(), nil
}

func writeAtomic(dir, name string, body []byte) error {
	tmp, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return fmt.Errorf("nfd: create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if err = fillTemp(tmp, body); err == nil {
		err = osRenamer().replace(tmpPath, filepath.Join(dir, name))
	}
	if err != nil {
		if rmErr := os.Remove(tmpPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return errors.Join(err, fmt.Errorf("nfd: remove temp file: %w", rmErr))
		}
	}
	return err
}

// fillTemp writes, syncs, sets the mode of and closes f.
func fillTemp(f *os.File, body []byte) error {
	_, err := f.Write(body)
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Chmod(fileMode)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("nfd: write temp file: %w", err)
	}
	return nil
}
