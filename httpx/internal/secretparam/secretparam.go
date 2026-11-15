// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package secretparam shares secret query parameter defaults and name matching.
package secretparam

import "strings"

// Defaults returns a fresh copy of the shared secret parameter names.
func Defaults() []string {
	return []string{"api_key", "apikey", "access_token", "token", "pin", "password", "client_secret", "x-plex-token", "refresh_token", "id_token"}
}

// CanonicalName folds case and removes underscores and hyphens for matching.
func CanonicalName(name string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(name, "_", ""), "-", ""))
}

// Set combines defaults and extra names into a canonical lookup set.
func Set(defaults, extra []string) map[string]struct{} {
	set := make(map[string]struct{}, len(defaults)+len(extra))
	for _, items := range [][]string{defaults, extra} {
		for _, item := range items {
			set[CanonicalName(item)] = struct{}{}
		}
	}
	return set
}
