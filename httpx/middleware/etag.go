// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package middleware

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/felixge/httpsnoop"
)

const defaultETagMaxBodyBytes = 1 << 20

// etagRecorder buffers a bounded response. Crossing the bound or using a
// streaming interface switches it permanently to transparent passthrough.
type etagRecorder struct {
	responseWriter http.ResponseWriter
	buf            bytes.Buffer
	finalHeader    http.Header
	status         int
	maxBytes       int
	passthrough    bool
	flushErr       error
}

func newETagRecorder(writer http.ResponseWriter, maxBytes int) (*etagRecorder, http.ResponseWriter) {
	recorder := &etagRecorder{responseWriter: writer, maxBytes: maxBytes}
	hooks := httpsnoop.Hooks{
		WriteHeader: recorder.writeHeaderHook,
		Write:       recorder.writeHook,
		Flush:       recorder.flushHook(writer),
		FlushError:  recorder.flushErrorHook,
		Hijack:      recorder.hijackHook,
		ReadFrom:    recorder.readFromHook,
	}
	return recorder, httpsnoop.Wrap(writer, hooks)
}

func (e *etagRecorder) writeHeaderHook(next httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
	return func(status int) {
		if status < 100 || status > 599 {
			next(status)
			return
		}
		if status != http.StatusSwitchingProtocols && status < http.StatusOK {
			next(status)
			return
		}
		if status == http.StatusSwitchingProtocols {
			e.status = status
			e.finalHeader = e.responseWriter.Header().Clone()
			e.passthrough = true
			next(status)
			return
		}
		if e.status == 0 {
			e.status = status
			e.finalHeader = e.responseWriter.Header().Clone()
		}
	}
}

func (e *etagRecorder) writeHook(next httpsnoop.WriteFunc) httpsnoop.WriteFunc {
	return func(body []byte) (int, error) {
		if e.passthrough {
			return next(body)
		}
		e.commitOK()
		if len(body) <= e.maxBytes-e.buf.Len() {
			return e.buf.Write(body)
		}
		if err := e.enablePassthrough(); err != nil {
			return 0, err
		}
		return next(body)
	}
}

func (e *etagRecorder) flushHook(writer http.ResponseWriter) func(httpsnoop.FlushFunc) httpsnoop.FlushFunc {
	return func(_ httpsnoop.FlushFunc) httpsnoop.FlushFunc {
		return func() {
			if err := e.enablePassthrough(); err != nil {
				e.flushErr = err
				return
			}
			if err := http.NewResponseController(writer).Flush(); err != nil {
				e.flushErr = err
			}
		}
	}
}

func (e *etagRecorder) flushErrorHook(next httpsnoop.FlushErrorFunc) httpsnoop.FlushErrorFunc {
	return func() error {
		if err := e.enablePassthrough(); err != nil {
			e.flushErr = err
			return err
		}
		err := next()
		if err != nil {
			e.flushErr = err
		}
		return err
	}
}

func (e *etagRecorder) hijackHook(next httpsnoop.HijackFunc) httpsnoop.HijackFunc {
	return func() (net.Conn, *bufio.ReadWriter, error) {
		if err := e.prepareHijack(); err != nil {
			return nil, nil, err
		}
		conn, readWriter, err := next()
		if err != nil {
			return nil, nil, fmt.Errorf("httpx/middleware: hijack ETag response: %w", err)
		}
		return conn, readWriter, nil
	}
}

func (e *etagRecorder) prepareHijack() error {
	if !e.passthrough && e.status == 0 && e.buf.Len() == 0 {
		e.passthrough = true
		return nil
	}
	return e.enablePassthrough()
}

func (e *etagRecorder) readFromHook(_ httpsnoop.ReadFromFunc) httpsnoop.ReadFromFunc {
	return func(reader io.Reader) (int64, error) {
		written, err := io.Copy(responseWriterFunc(e.writeHook(e.responseWriter.Write)), reader)
		if err != nil {
			return written, fmt.Errorf("httpx/middleware: copy ETag response: %w", err)
		}
		return written, nil
	}
}

func (e *etagRecorder) enablePassthrough() error {
	if e.passthrough {
		return nil
	}
	e.passthrough = true
	e.commitOK()
	e.restoreFinalHeader()
	e.responseWriter.WriteHeader(e.status)
	if e.buf.Len() == 0 {
		return nil
	}
	_, err := e.responseWriter.Write(e.buf.Bytes())
	e.buf.Reset()
	return err //nolint:wrapcheck // ResponseWriter passthrough
}

func (e *etagRecorder) commitOK() {
	if e.status == 0 {
		e.status = http.StatusOK
		e.finalHeader = e.responseWriter.Header().Clone()
	}
}

func (e *etagRecorder) restoreFinalHeader() {
	header := e.responseWriter.Header()
	clear(header)
	for key, values := range e.finalHeader {
		header[key] = slices.Clone(values)
	}
}

type responseWriterFunc func([]byte) (int, error)

func (write responseWriterFunc) Write(body []byte) (int, error) { return write(body) }

// ETag computes a weak ETag for bounded GET responses. Responses larger than
// 1 MiB and handlers that flush or hijack bypass ETag without unbounded memory.
func ETag(next http.Handler) http.Handler {
	return ETagWithLimit(defaultETagMaxBodyBytes)(next)
}

// ETagWithLimit returns ETag middleware with a byte bound. A nonpositive bound
// disables buffering and passes responses through unchanged.
func ETagWithLimit(maxBodyBytes int) Middleware {
	return func(next http.Handler) http.Handler {
		if maxBodyBytes <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serveETag(w, r, next, maxBodyBytes)
		})
	}
}

func serveETag(w http.ResponseWriter, r *http.Request, next http.Handler, maxBodyBytes int) {
	if r.Method != http.MethodGet {
		next.ServeHTTP(w, r)
		return
	}
	recorder, wrapped := newETagRecorder(w, maxBodyBytes)
	next.ServeHTTP(wrapped, r)
	if recorder.passthrough || recorder.flushErr != nil {
		return
	}

	recorder.commitOK()
	if recorder.status != http.StatusOK {
		if err := recorder.enablePassthrough(); err != nil {
			return
		}
		return
	}

	etag := recorder.finalHeader.Get("ETag")
	if etag == "" {
		sum := sha256.Sum256(recorder.buf.Bytes())
		etag = `W/"` + hex.EncodeToString(sum[:16]) + `"`
		recorder.finalHeader.Set("ETag", etag)
	}
	recorder.restoreFinalHeader()
	if matchesETag(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if err := recorder.enablePassthrough(); err != nil {
		return
	}
}

func matchesETag(condition, current string) bool {
	for candidate := range strings.SplitSeq(condition, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || weakETagEqual(candidate, current) {
			return true
		}
	}
	return false
}

func weakETagEqual(left, right string) bool {
	left = strings.TrimPrefix(left, "W/")
	right = strings.TrimPrefix(right, "W/")
	if len(left) < 2 || left[0] != '"' || left[len(left)-1] != '"' {
		return false
	}
	if len(right) < 2 || right[0] != '"' || right[len(right)-1] != '"' {
		return false
	}
	return left == right
}
