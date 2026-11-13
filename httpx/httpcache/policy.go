// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package httpcache

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	defaultSecretParams     = []string{"api_key", "apikey", "access_token", "token", "pin", "password", "client_secret", "x-plex-token", "refresh_token", "id_token"}
	defaultSecretHeaders    = []string{"Authorization", "Proxy-Authorization", "Cookie", "X-Api-Key", "Api-Key", "X-Auth-Token", "X-Plex-Token"}
	defaultCredentialFields = []string{"access_token", "refresh_token", "id_token", "token", "authToken", "apikey", "api_key", "password", "client_secret", "secret", "pin", "x-plex-token"}
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
	for _, items := range [][]string{defaultCredentialFields, extra} {
		for _, field := range items {
			fields = append(fields, credentialNamePattern(field))
		}
	}
	names := "(?:" + strings.Join(fields, "|") + ")"
	return regexp.MustCompile(`(?i)("` + names + `"\s*:|<` + names + `[\s>/]|(^|[\s?&])` + names + `\s*=)`)
}

func noStore(header http.Header) {
	if !hasDirective(header, "no-store") {
		directives := append(header.Values("Cache-Control"), "no-store")
		header.Set("Cache-Control", strings.Join(directives, ", "))
	}
	header.Del("Expires")
}

func (c *Cache) applyPolicy(resp *http.Response, body []byte, host HostOptions, oversized bool) {
	c.applyHeaders(resp, host)
	c.applyBody(resp, body, oversized)
}

func (c *Cache) applyHeaders(resp *http.Response, host HostOptions) {
	resp.Header = resp.Header.Clone()
	if resp.Header == nil {
		resp.Header = make(http.Header)
	}
	resp.Header.Del(syntheticHeader)
	if c.nonStorable(resp, host) {
		noStore(resp.Header)
	}
	c.cleanVary(resp.Header)
	if !hasDirective(resp.Header, "no-store") && host.TTL > 0 {
		overrideTTL(resp.Header, host.TTL)
	}
	addStale(resp.Header, "stale-if-error", host.StaleIfError)
	addStale(resp.Header, "stale-while-revalidate", host.StaleWhileRevalidate)
}

func (c *Cache) nonStorable(resp *http.Response, host HostOptions) bool {
	if resp.Request != nil && resp.Request.Method != http.MethodGet && resp.Request.Method != http.MethodHead {
		return true
	}
	if !storableStatus(resp.StatusCode) || c.credentialedResponse(resp, host) || c.sensitiveResponse(resp) {
		return true
	}
	switch contentEncoding(resp.Header) {
	case "", "identity", "gzip", "x-gzip":
		return hasDirective(resp.Header, "no-store")
	default:
		return true
	}
}

func (c *Cache) applyBody(resp *http.Response, body []byte, oversized bool) {
	if oversized || (!bodylessResponse(resp) && c.sensitiveBody(resp.Header, body)) {
		noStore(resp.Header)
	}
}

func bodylessResponse(resp *http.Response) bool {
	return (resp.Request != nil && resp.Request.Method == http.MethodHead) ||
		resp.StatusCode == http.StatusNotModified || resp.StatusCode == http.StatusNoContent ||
		(resp.StatusCode >= http.StatusContinue && resp.StatusCode < http.StatusOK)
}

func (c *Cache) sensitiveResponse(resp *http.Response) bool {
	if c.sensitiveLocation(resp.Header) {
		return true
	}
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

func (c *Cache) sensitiveLocation(header http.Header) bool {
	for _, name := range [...]string{"Location", "Content-Location"} {
		for _, value := range header.Values(name) {
			target, err := url.Parse(value)
			if err != nil {
				return true
			}
			if target.User != nil || c.secretQuery(target.RawQuery) || c.secretQuery(target.EscapedFragment()) {
				return true
			}
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
	kept = append(kept, "max-age="+durationSeconds(ttl))
	header.Set("Cache-Control", strings.Join(kept, ", "))
	header.Del("Expires")
	header.Del("Pragma")
}

func addStale(header http.Header, directive string, duration time.Duration) {
	if duration <= 0 || hasDirective(header, directive) {
		return
	}
	value := directive + "=" + durationSeconds(duration)
	if current := strings.Join(header.Values("Cache-Control"), ", "); current != "" {
		value = current + ", " + value
	}
	header.Set("Cache-Control", value)
}

func (c *Cache) credentialedResponse(resp *http.Response, host HostOptions) bool {
	if host.Shared || resp.Request == nil {
		return false
	}
	state, ok := stateOf(resp.Request)
	if !ok || !state.credentialed {
		return false
	}
	for name := range strings.SplitSeq(strings.Join(resp.Header.Values("Vary"), ","), ",") {
		if _, secret := c.secretHeaders[strings.ToLower(strings.TrimSpace(name))]; secret {
			return true
		}
	}
	return !hasDirective(resp.Header, "public") && !hasDirective(resp.Header, "s-maxage") && !hasDirective(resp.Header, "must-revalidate")
}

func contentEncoding(header http.Header) string {
	return strings.ToLower(strings.TrimSpace(strings.Join(header.Values("Content-Encoding"), ",")))
}

func (c *Cache) sensitiveBody(header http.Header, body []byte) bool {
	decoded, ok := c.decodeBody(header, body)
	return !ok || c.credentials.Match(decoded) || c.encodedCredentials(header, decoded)
}

func canonicalName(name string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(name, "_", ""), "-", ""))
}

func canonicalSet(defaults, extra []string) map[string]struct{} {
	set := extendedSet(defaults, extra)
	result := make(map[string]struct{}, len(set))
	for name := range set {
		result[canonicalName(name)] = struct{}{}
	}
	return result
}

func (c *Cache) encodedCredentials(header http.Header, body []byte) bool {
	mediaType, _, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil {
		return false
	}
	switch {
	case mediaType == "application/x-www-form-urlencoded":
		values, parseErr := url.ParseQuery(string(body))
		if parseErr != nil {
			return true
		}
		for key := range values {
			if _, secret := c.fields[canonicalName(key)]; secret {
				return true
			}
		}
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		if bytes.Contains(body, []byte(`\u`)) {
			return c.jsonCredentials(body)
		}
	}
	return false
}

type jsonScope struct{ object, key bool }

func (c *Cache) jsonCredentials(body []byte) bool {
	if !json.Valid(body) {
		return true
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	stack := make([]jsonScope, 0)
	for range len(body) + 1 {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return false
		}
		if err != nil {
			return true
		}
		var sensitive bool
		stack, sensitive = c.jsonToken(token, stack)
		if sensitive {
			return true
		}
	}
	return true
}

func (c *Cache) jsonToken(token json.Token, stack []jsonScope) ([]jsonScope, bool) {
	if delim, ok := token.(json.Delim); ok && (delim == '}' || delim == ']') {
		return stack[:len(stack)-1], false
	}
	if len(stack) > 0 && stack[len(stack)-1].object {
		if c.jsonField(token, &stack[len(stack)-1]) {
			return stack, true
		}
	}
	if delim, ok := token.(json.Delim); ok {
		stack = append(stack, jsonScope{object: delim == '{', key: delim == '{'})
	}
	return stack, false
}

func (c *Cache) jsonField(token json.Token, scope *jsonScope) bool {
	key := scope.key
	scope.key = !scope.key
	if !key {
		return false
	}
	name, ok := token.(string)
	if !ok {
		return false
	}
	_, secret := c.fields[canonicalName(name)]
	return secret
}

func (c *Cache) decodeBody(header http.Header, body []byte) ([]byte, bool) {
	switch contentEncoding(header) {
	case "", "identity":
		return body, true
	case "gzip", "x-gzip":
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, false
		}
		decoded, readErr := io.ReadAll(io.LimitReader(reader, c.opts.MaxBodyBytes+1))
		closeErr := reader.Close()
		return decoded, readErr == nil && closeErr == nil && int64(len(decoded)) <= c.opts.MaxBodyBytes
	default:
		return nil, false
	}
}

func credentialNamePattern(field string) string {
	words := make([]string, 0, strings.Count(field, "_")+strings.Count(field, "-")+1)
	start := 0
	for i := range len(field) {
		if field[i] == '_' || field[i] == '-' {
			words = append(words, regexp.QuoteMeta(field[start:i]))
			start = i + 1
		} else if i > start && field[i-1] >= 'a' && field[i-1] <= 'z' && field[i] >= 'A' && field[i] <= 'Z' {
			words = append(words, regexp.QuoteMeta(field[start:i]))
			start = i
		}
	}
	words = append(words, regexp.QuoteMeta(field[start:]))
	return strings.Join(words, "[_-]?")
}

func durationSeconds(duration time.Duration) string {
	seconds := duration / time.Second
	if duration%time.Second != 0 {
		seconds++
	}
	return strconv.FormatInt(int64(seconds), 10)
}
