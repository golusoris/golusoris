// Package hashfs serves content-addressable asset filenames — e.g.
// `assets/logo-abc123def.png` — for aggressive immutable caching. Thin
// wrapper over benbjohnson/hashfs; apps get:
//
//   - [*FS].HashName("assets/logo.png") → "assets/logo-<hash>.png" for
//     rendering in templates.
//   - [Handler] that serves files by their hashed name, stripping the hash
//     before filesystem lookup, and sets `Cache-Control: public,
//     max-age=31536000` so browsers cache for a year.
//
// Unhashed requests still work (no Cache-Control change) so callers can
// migrate incrementally. For pure unhashed assets with normal cache
// headers, use [httpx/static.Handler] instead.
package hashfs

import (
	"io/fs"
	"net/http"

	bjh "github.com/benbjohnson/hashfs"
)

// FS mirrors the bjh.FS API so callers don't need to import both packages.
type FS = bjh.FS

// New wraps fsys so assets can be referenced by their hashed name.
func New(fsys fs.FS) *FS { return bjh.NewFS(fsys) }

// Handler serves files from fsys with immutable cache headers. Requests with
// a hashed name (e.g. /assets/logo-abc.png) are routed to the underlying
// unhashed file transparently by hashfs.
func Handler(fsys fs.FS) http.Handler { return bjh.FileServer(fsys) }
