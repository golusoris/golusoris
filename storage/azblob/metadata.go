// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package azblob

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// toMetadata rejects names Azure refuses (C# identifiers only) before any
// block is staged, so an invalid Put fails without uploading the body.
func toMetadata(in map[string]string) (map[string]*string, error) {
	if len(in) == 0 {
		return nil, nil //nolint:nilnil // nil map means "no metadata" to the SDK.
	}
	out := make(map[string]*string, len(in))
	for name, value := range in {
		if !validMetadataName(name) {
			return nil, fmt.Errorf("storage/azblob: metadata name %q is not a C# identifier", name)
		}
		out[strings.ToLower(name)] = new(value)
	}
	return out, nil
}

// fromMetadata lowercases names: HTTP header canonicalisation already
// rewrote their case on the way back.
func fromMetadata(in map[string]*string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for name, value := range in {
		out[strings.ToLower(name)] = deref(value)
	}
	return out
}

func validMetadataName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		if !identifierStart(c) && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func identifierStart(c rune) bool {
	return c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func etagString(etag *azcore.ETag) string {
	if etag == nil {
		return ""
	}
	return string(*etag)
}

// deref returns *p, or the zero value for a nil pointer.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
