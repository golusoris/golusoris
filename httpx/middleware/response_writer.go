// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package middleware

import (
	"bufio"
	"io"
	"net"
	"net/http"

	"github.com/felixge/httpsnoop"
)

// trackingWriter records final response state. The handler receives the
// httpsnoop wrapper returned with it, so optional interfaces exactly match the
// underlying writer instead of being fabricated by this state holder.
type trackingWriter struct {
	status   int
	bytes    int
	flushErr error
}

func newTrackingWriter(writer http.ResponseWriter) (*trackingWriter, http.ResponseWriter) {
	state := &trackingWriter{}
	hooks := httpsnoop.Hooks{
		WriteHeader: state.writeHeaderHook,
		Write:       state.writeHook,
		Flush:       state.flushHook(writer),
		FlushError:  state.flushErrorHook,
		Hijack:      state.hijackHook,
		ReadFrom:    state.readFromHook,
	}
	return state, httpsnoop.Wrap(writer, hooks)
}

func (w *trackingWriter) writeHeaderHook(next httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
	return func(status int) {
		next(status)
		if (status == http.StatusSwitchingProtocols || status >= http.StatusOK) && w.status == 0 {
			w.status = status
		}
	}
}

func (w *trackingWriter) writeHook(next httpsnoop.WriteFunc) httpsnoop.WriteFunc {
	return func(body []byte) (int, error) {
		n, err := next(body)
		if w.status == 0 {
			w.status = http.StatusOK
		}
		w.bytes += n
		return n, err
	}
}

func (w *trackingWriter) flushHook(writer http.ResponseWriter) func(httpsnoop.FlushFunc) httpsnoop.FlushFunc {
	return func(_ httpsnoop.FlushFunc) httpsnoop.FlushFunc {
		return func() {
			w.commitOK()
			if err := http.NewResponseController(writer).Flush(); err != nil {
				w.flushErr = err
			}
		}
	}
}

func (w *trackingWriter) flushErrorHook(next httpsnoop.FlushErrorFunc) httpsnoop.FlushErrorFunc {
	return func() error {
		w.commitOK()
		err := next()
		if err != nil {
			w.flushErr = err
		}
		return err
	}
}

func (w *trackingWriter) hijackHook(next httpsnoop.HijackFunc) httpsnoop.HijackFunc {
	return func() (net.Conn, *bufio.ReadWriter, error) {
		conn, readWriter, err := next()
		if err == nil && w.status == 0 {
			w.status = http.StatusSwitchingProtocols
		}
		return conn, readWriter, err
	}
}

func (w *trackingWriter) readFromHook(next httpsnoop.ReadFromFunc) httpsnoop.ReadFromFunc {
	return func(reader io.Reader) (int64, error) {
		w.commitOK()
		n, err := next(reader)
		w.bytes += int(n)
		return n, err
	}
}

func (w *trackingWriter) commitOK() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
}

func (w *trackingWriter) committed() bool { return w.status != 0 }
