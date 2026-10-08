// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package fileuri converts absolute local paths to and from RFC 8089 file
// URIs on every platform, including Windows drive-letter paths.
package fileuri

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// FromPath returns the file URI for an absolute local path. Windows drive
// paths gain the leading slash RFC 8089 requires: C:\data\x -> file:///C:/data/x.
func FromPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("fileuri: path %q is not absolute", path)
	}
	return fromAbsolute(path, filepath.Separator), nil
}

// ToPath returns the local path named by a file URI's path component. The
// caller owns scheme, host, and absoluteness policy.
func ToPath(uri *url.URL) string {
	return toLocal(uri.Path, filepath.Separator)
}

func fromAbsolute(path string, separator rune) string {
	slashed := strings.ReplaceAll(path, string(separator), "/")
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

func toLocal(uriPath string, separator rune) string {
	if separator == '/' {
		return uriPath
	}
	if hasDrivePrefix(uriPath) {
		uriPath = uriPath[1:]
	}
	return strings.ReplaceAll(uriPath, "/", string(separator))
}

// hasDrivePrefix reports the RFC 8089 Appendix E.2 form "/C:".
func hasDrivePrefix(uriPath string) bool {
	if len(uriPath) < 3 || uriPath[0] != '/' || uriPath[2] != ':' {
		return false
	}
	letter := uriPath[1]
	return ('a' <= letter && letter <= 'z') || ('A' <= letter && letter <= 'Z')
}
