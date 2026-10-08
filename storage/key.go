// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// ErrUnsafeKey is returned when an object key fails validation.
var ErrUnsafeKey = errors.New("storage: unsafe object key")

const (
	// MaxKeyBytes is the backend-neutral object-key byte limit.
	MaxKeyBytes     = 1024
	localTempPrefix = ".golusoris-put-"
	localTempSuffix = ".tmp"
)

var windowsReservedNames = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
	"CONIN$": {}, "CONOUT$": {},
	"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {},
	"COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"COM¹": {}, "COM²": {}, "COM³": {},
	"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {},
	"LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
	"LPT¹": {}, "LPT²": {}, "LPT³": {},
}

// CleanKey validates a canonical object key as a local slash path.
func CleanKey(key string, maxLen int) (string, error) {
	if err := rejectKeyLexically(key, maxLen); err != nil {
		return "", err
	}
	clean := path.Clean(key)
	if clean != key {
		return "", fmt.Errorf("%w: %q is not canonical", ErrUnsafeKey, key)
	}
	if err := rejectCleanKey(clean); err != nil {
		return "", err
	}
	if !filepath.IsLocal(clean) {
		return "", fmt.Errorf("%w: %q escapes root", ErrUnsafeKey, key)
	}
	return clean, nil
}

// MustBeLocal rejects keys that are not already local slash paths.
func MustBeLocal(key string) error {
	clean, err := CleanKey(key, MaxKeyBytes)
	if err != nil {
		return err
	}
	if clean != key {
		return fmt.Errorf("%w: %q is not canonical", ErrUnsafeKey, key)
	}
	return nil
}

// CleanListPrefix validates a [ListOptions.Prefix]: empty is valid, and a
// trailing slash survives canonicalisation.
func CleanListPrefix(prefix string) (string, error) {
	if prefix == "" {
		return "", nil
	}
	if err := rejectKeyLength(prefix, MaxKeyBytes); err != nil {
		return "", err
	}
	trailingSlash := strings.HasSuffix(prefix, "/")
	candidate := strings.TrimSuffix(prefix, "/")
	clean, err := CleanKey(candidate, MaxKeyBytes)
	if err != nil {
		return "", err
	}
	if trailingSlash {
		return clean + "/", nil
	}
	return clean, nil
}

func rejectKeyLexically(key string, maxLen int) error {
	if key == "" {
		return fmt.Errorf("%w: empty", ErrUnsafeKey)
	}
	if err := rejectKeyLength(key, maxLen); err != nil {
		return err
	}
	if err := rejectKeyCharacters(key); err != nil {
		return err
	}
	return rejectKeySegments(key)
}

func rejectKeyLength(key string, maxLen int) error {
	if maxLen > 0 && len(key) > maxLen {
		return fmt.Errorf("%w: length %d exceeds max %d", ErrUnsafeKey, len(key), maxLen)
	}
	return nil
}

func rejectKeyCharacters(key string) error {
	if strings.ContainsRune(key, '\x00') {
		return fmt.Errorf("%w: null byte", ErrUnsafeKey)
	}
	if strings.ContainsRune(key, '\\') {
		return fmt.Errorf("%w: backslash (UNC/Windows separator)", ErrUnsafeKey)
	}
	if strings.ContainsAny(key, `<>:"|?*`) {
		return fmt.Errorf("%w: Windows-invalid or alternate-data-stream character", ErrUnsafeKey)
	}
	if strings.HasPrefix(key, "/") {
		return fmt.Errorf("%w: absolute key", ErrUnsafeKey)
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: control character", ErrUnsafeKey)
		}
	}
	return nil
}

func rejectKeySegments(key string) error {
	for segment := range strings.SplitSeq(key, "/") {
		if segment == "" {
			return fmt.Errorf("%w: empty path segment", ErrUnsafeKey)
		}
		if segment == "." || segment == ".." {
			return fmt.Errorf("%w: dot path segment %q", ErrUnsafeKey, segment)
		}
		if strings.HasSuffix(segment, " ") || strings.HasSuffix(segment, ".") {
			return fmt.Errorf("%w: segment %q has trailing space or dot", ErrUnsafeKey, segment)
		}
		if isLocalTempName(segment) {
			return fmt.Errorf("%w: segment %q uses reserved staging namespace", ErrUnsafeKey, segment)
		}
		if isLocalMetadataName(segment) {
			return fmt.Errorf("%w: segment %q uses reserved metadata namespace", ErrUnsafeKey, segment)
		}
		if isLocalControlName(segment) {
			return fmt.Errorf("%w: segment %q uses reserved control namespace", ErrUnsafeKey, segment)
		}
	}
	return nil
}

func isLocalTempName(name string) bool {
	return strings.HasPrefix(name, localTempPrefix) && strings.HasSuffix(name, localTempSuffix)
}

func rejectCleanKey(clean string) error {
	if clean == "." || clean == ".." {
		return fmt.Errorf("%w: %q", ErrUnsafeKey, clean)
	}
	if clean == "" || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("%w: traversal", ErrUnsafeKey)
	}
	for segment := range strings.SplitSeq(clean, "/") {
		base, _, _ := strings.Cut(segment, ".")
		if _, reserved := windowsReservedNames[strings.ToUpper(base)]; reserved {
			return fmt.Errorf("%w: reserved segment %q", ErrUnsafeKey, segment)
		}
	}
	return nil
}
