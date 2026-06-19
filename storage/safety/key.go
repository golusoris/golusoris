package safety

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// ErrUnsafeKey is returned when an object key fails validation.
var ErrUnsafeKey = errors.New("storage/safety: unsafe object key")

// winReserved are Windows device names rejected regardless of host OS so a key
// validated on Linux stays safe if a backend later opens it on Windows.
var winReserved = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
	"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {},
	"COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {},
	"LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
}

// CleanKey validates and normalizes an object key to a forward-slash,
// traversal-free, local path. It rejects "..", absolute/UNC keys, null bytes,
// control chars, Windows-reserved names, and keys longer than maxLen. The
// normalized result always satisfies [filepath.IsLocal] and contains no "..".
func CleanKey(key string, maxLen int) (string, error) {
	if err := lexicalReject(key, maxLen); err != nil {
		return "", err
	}
	clean := path.Clean(key)
	if err := postCleanReject(clean); err != nil {
		return "", err
	}
	if !filepath.IsLocal(clean) {
		return "", fmt.Errorf("%w: %q escapes root", ErrUnsafeKey, key)
	}
	return clean, nil
}

// lexicalReject screens the raw key before normalization.
func lexicalReject(key string, maxLen int) error {
	if key == "" {
		return fmt.Errorf("%w: empty", ErrUnsafeKey)
	}
	if maxLen > 0 && len(key) > maxLen {
		return fmt.Errorf("%w: length %d exceeds max %d", ErrUnsafeKey, len(key), maxLen)
	}
	if strings.ContainsRune(key, '\x00') {
		return fmt.Errorf("%w: null byte", ErrUnsafeKey)
	}
	if strings.ContainsRune(key, '\\') {
		return fmt.Errorf("%w: backslash (UNC/Windows separator)", ErrUnsafeKey)
	}
	if strings.HasPrefix(key, "/") {
		return fmt.Errorf("%w: absolute key", ErrUnsafeKey)
	}
	if hasControlRune(key) {
		return fmt.Errorf("%w: control character", ErrUnsafeKey)
	}
	// Trailing space/dot is silently stripped by Windows, an aliasing hazard.
	if last := key[len(key)-1]; last == ' ' || last == '.' && !strings.HasSuffix(key, "..") {
		return fmt.Errorf("%w: trailing space or dot", ErrUnsafeKey)
	}
	return nil
}

// postCleanReject screens the normalized key for residual traversal and
// reserved segment names.
func postCleanReject(clean string) error {
	if clean == "." || clean == ".." {
		return fmt.Errorf("%w: %q", ErrUnsafeKey, clean)
	}
	if clean == "" || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("%w: traversal", ErrUnsafeKey)
	}
	for _, seg := range strings.Split(clean, "/") {
		if isWinReserved(seg) {
			return fmt.Errorf("%w: reserved segment %q", ErrUnsafeKey, seg)
		}
	}
	return nil
}

// MustBeLocal is the lexical gate reusable by the local storage backend before
// any os.Root open. It returns nil only when key is a clean, local, slash key.
func MustBeLocal(key string) error {
	if key == "" {
		return fmt.Errorf("%w: empty", ErrUnsafeKey)
	}
	if !filepath.IsLocal(filepath.FromSlash(key)) {
		return fmt.Errorf("%w: %q is not local", ErrUnsafeKey, key)
	}
	if strings.Contains(key, "..") || strings.ContainsRune(key, '\x00') {
		return fmt.Errorf("%w: %q", ErrUnsafeKey, key)
	}
	return nil
}

// hasControlRune reports whether s contains an ASCII control character.
func hasControlRune(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// isWinReserved reports whether seg is a Windows device name, with or without
// an extension (e.g. "CON", "nul.txt").
func isWinReserved(seg string) bool {
	base := seg
	if i := strings.IndexByte(seg, '.'); i >= 0 {
		base = seg[:i]
	}
	_, ok := winReserved[strings.ToUpper(base)]
	return ok
}
