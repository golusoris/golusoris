// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package httpcache

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	defaultSecretParams     = []string{"api_key", "apikey", "access_token", "token", "pin", "password", "client_secret"}
	defaultSecretHeaders    = []string{"Authorization", "Proxy-Authorization", "Cookie", "X-Api-Key", "Api-Key", "X-Auth-Token"}
	defaultCredentialFields = []string{"access_token", "refresh_token", "id_token", "token", "authToken", "apikey", "api_key", "password", "client_secret", "secret", "pin"}
	defaultAuthPaths        = []string{"login", "logout", "oauth", "oauth2", "token", "tokens", "auth", "authorize", "pin", "pins", "session", "sessions"}
)

func extendedSet(defaults, extra []string) map[string]struct{} {
	set := make(map[string]struct{}, len(defaults)+len(extra))
	for _, items := range [][]string{defaults, extra} {
		for _, item := range items {
			set[strings.ToLower(item)] = struct{}{}
		}
	}
	return set
}

func credentialPattern(extra []string) *regexp.Regexp {
	fields := make([]string, 0, len(defaultCredentialFields)+len(extra))
	for field := range extendedSet(defaultCredentialFields, extra) {
		fields = append(fields, regexp.QuoteMeta(field))
	}
	names := "(?:" + strings.Join(fields, "|") + ")"
	return regexp.MustCompile(`(?i)("` + names + `"\s*:|<` + names + `[\s>/]|(^|[\s?&])` + names + `\s*=)`)
}

func (c *Cache) applyPolicy(resp *http.Response, body []byte, host HostOptions, oversized bool) {
	resp.Header = resp.Header.Clone()
	if resp.Header == nil {
		resp.Header = make(http.Header)
	}
	resp.Header.Del(syntheticHeader)
	if oversized || c.sensitiveResponse(resp) || c.credentials.Match(body) {
		resp.Header.Set("Cache-Control", "no-store")
		resp.Header.Del("Expires")
	}
	c.cleanVary(resp.Header)
	if !hasDirective(resp.Header, "no-store") && host.TTL > 0 && storableStatus(resp.StatusCode) {
		overrideTTL(resp.Header, host.TTL)
	}
	addStale(resp.Header, "stale-if-error", host.StaleIfError)
	addStale(resp.Header, "stale-while-revalidate", host.StaleWhileRevalidate)
}

func (c *Cache) sensitiveResponse(resp *http.Response) bool {
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusProxyAuthRequired:
		return true
	}
	for name := range resp.Header {
		if _, secret := c.secretHeaders[strings.ToLower(name)]; secret {
			return true
		}
		switch strings.ToLower(name) {
		case "set-cookie", "www-authenticate", "proxy-authenticate", "authentication-info":
			return true
		}
	}
	return false
}

func (c *Cache) cleanVary(header http.Header) {
	names := strings.Split(strings.Join(header.Values("Vary"), ","), ",")
	kept := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if _, secret := c.secretHeaders[strings.ToLower(name)]; !secret && name != "" {
			kept = append(kept, name)
		}
	}
	header.Del("Vary")
	if len(kept) > 0 {
		header.Set("Vary", strings.Join(kept, ", "))
	}
}

func cacheDirectives(line string) []string {
	directives := make([]string, 0, strings.Count(line, ",")+1)
	start := 0
	quoted, escaped := false, false
	for i := range len(line) {
		switch {
		case escaped:
			escaped = false
		case line[i] == '\\' && quoted:
			escaped = true
		case line[i] == '"':
			quoted = !quoted
		case line[i] == ',' && !quoted:
			directives = append(directives, line[start:i])
			start = i + 1
		}
	}
	return append(directives, line[start:])
}

func hasDirective(header http.Header, name string) bool {
	for _, line := range header.Values("Cache-Control") {
		for _, directive := range cacheDirectives(line) {
			key, _, _ := strings.Cut(strings.TrimSpace(directive), "=")
			if strings.EqualFold(strings.TrimSpace(key), name) {
				return true
			}
		}
	}
	return false
}

func storableStatus(code int) bool { return code >= 200 && code < 400 || code == http.StatusNotFound }

func overrideTTL(header http.Header, ttl time.Duration) {
	kept := make([]string, 0)
	for _, line := range header.Values("Cache-Control") {
		for _, directive := range cacheDirectives(line) {
			key, _, _ := strings.Cut(strings.TrimSpace(directive), "=")
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "max-age", "s-maxage", "no-cache", "must-revalidate", "proxy-revalidate", "":
			default:
				kept = append(kept, strings.TrimSpace(directive))
			}
		}
	}
	kept = append(kept, "max-age="+strconv.FormatInt(int64(ttl/time.Second), 10))
	header.Set("Cache-Control", strings.Join(kept, ", "))
	header.Del("Expires")
	header.Del("Pragma")
}

func addStale(header http.Header, directive string, duration time.Duration) {
	if duration <= 0 || hasDirective(header, directive) {
		return
	}
	value := directive + "=" + strconv.FormatInt(int64(duration/time.Second), 10)
	if current := strings.Join(header.Values("Cache-Control"), ", "); current != "" {
		value = current + ", " + value
	}
	header.Set("Cache-Control", value)
}
