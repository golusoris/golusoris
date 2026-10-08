// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/knadh/koanf/v2"
)

const (
	// DefaultMaxSecretBytes is the per-file cap when Options.MaxSecretBytes is zero.
	DefaultMaxSecretBytes int64 = 64 << 10
	// maxSecretDirEntries bounds one secret directory listing.
	maxSecretDirEntries = 1024
)

// ErrSecretTooLarge reports a secret file above Options.MaxSecretBytes.
var ErrSecretTooLarge = errors.New("config: secret file exceeds size limit")

// loadSecretDirs layers every SecretDirs entry onto k; later dirs win.
func loadSecretDirs(k *koanf.Koanf, opts Options) error {
	for _, dir := range opts.SecretDirs {
		values, err := readSecretDir(dir, opts)
		if err != nil {
			return err
		}
		for key, value := range values {
			if err = k.Set(key, value); err != nil {
				return fmt.Errorf("config: set secret %q: %w", key, err)
			}
		}
	}
	return nil
}

// readSecretDir maps each regular file in dir to a key. Dot-prefixed
// entries are the Kubernetes volume's ..data plumbing and are skipped; a
// missing dir is skipped like a missing config file.
func readSecretDir(dir string, opts Options) (_ map[string]string, err error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read secret dir %s: %w", dir, err)
	}
	if len(entries) > maxSecretDirEntries {
		return nil, fmt.Errorf("config: secret dir %s holds more than %d entries", dir, maxSecretDirEntries)
	}
	// os.Root follows the volume's symlinks but refuses any that leave dir.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("config: open secret dir %s: %w", dir, err)
	}
	defer closeInto(root, &err, "config: close secret dir "+dir)
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		value, isFile, readErr := readRootFile(root, entry.Name(), opts.maxSecretBytes())
		if readErr != nil {
			return nil, readErr
		}
		if !isFile {
			continue
		}
		key, keyErr := secretKey(entry.Name(), opts.Delimiter)
		if keyErr != nil {
			return nil, keyErr
		}
		values[key] = value
	}
	return values, nil
}

// secretKey validates a file name as a koanf path: no empty segments.
func secretKey(name, delim string) (string, error) {
	if slices.Contains(strings.Split(name, delim), "") {
		return "", fmt.Errorf("config: secret file %q is not a valid key path", name)
	}
	return name, nil
}

// readRootFile reads name below root, bounded by limit; isFile is false
// for a directory, which callers skip. Content is whitespace-trimmed like
// the root secrets package.
func readRootFile(root *os.Root, name string, limit int64) (_ string, isFile bool, err error) {
	f, err := root.Open(name)
	if err != nil {
		return "", false, fmt.Errorf("config: open secret %s: %w", name, err)
	}
	defer closeInto(f, &err, "config: close secret "+name)
	info, err := f.Stat()
	if err != nil {
		return "", false, fmt.Errorf("config: stat secret %s: %w", name, err)
	}
	if info.IsDir() {
		return "", false, nil
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("config: secret %s is not a regular file", name)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return "", false, fmt.Errorf("config: read secret %s: %w", name, err)
	}
	if int64(len(data)) > limit {
		return "", false, fmt.Errorf("%w: %s over %d bytes", ErrSecretTooLarge, name, limit)
	}
	return strings.TrimSpace(string(data)), true, nil
}

// readSecretPath reads one *_FILE target.
func readSecretPath(path string, limit int64) (_ string, err error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("config: open secret dir of %s: %w", path, err)
	}
	defer closeInto(root, &err, "config: close secret dir of "+path)
	value, isFile, err := readRootFile(root, filepath.Base(path), limit)
	if err != nil {
		return "", err
	}
	if !isFile {
		return "", fmt.Errorf("config: secret %s is a directory", path)
	}
	return value, nil
}

// loadFileEnv applies <PREFIX><NAME><FileEnvSuffix>=<path> indirection: the
// file content becomes the key <NAME> maps to. Setting the plain variable
// too is ambiguous and fails.
func loadFileEnv(k *koanf.Koanf, opts Options) error {
	if opts.FileEnvSuffix == "" {
		return nil
	}
	lookup := compoundLookup(opts)
	for _, kv := range os.Environ() {
		name, path, _ := strings.Cut(kv, "=")
		key, base, ok := fileEnvKey(name, opts, lookup)
		if !ok {
			continue
		}
		if _, set := os.LookupEnv(opts.EnvPrefix + base); set {
			return fmt.Errorf("config: both %s%s and %s are set", opts.EnvPrefix, base, name)
		}
		value, err := readSecretPath(path, opts.maxSecretBytes())
		if err != nil {
			return fmt.Errorf("config: %s: %w", name, err)
		}
		if err = k.Set(key, value); err != nil {
			return fmt.Errorf("config: set %q from %s: %w", key, name, err)
		}
	}
	return nil
}

// fileEnvKey reports whether name is a *_FILE indirection and the koanf key
// and prefix-stripped base name it targets. A declared compound key ending
// in the suffix is a plain value (a path-valued setting), not indirection.
func fileEnvKey(name string, opts Options, lookup map[string]string) (key, base string, ok bool) {
	if !strings.HasPrefix(name, opts.EnvPrefix) {
		return "", "", false
	}
	stripped := strings.TrimPrefix(name, opts.EnvPrefix)
	if _, compound := lookup[stripped]; compound {
		return "", "", false
	}
	base, found := strings.CutSuffix(stripped, opts.FileEnvSuffix)
	if !found || base == "" {
		return "", "", false
	}
	return envKey(base, opts, lookup), base, true
}

// closeInto records a close failure unless the read already failed. Local,
// not core/errors: that package would add go-faster/errors to every module
// importing config.
func closeInto(c io.Closer, errp *error, op string) {
	if cerr := c.Close(); cerr != nil && *errp == nil {
		*errp = fmt.Errorf("%s: %w", op, cerr)
	}
}
