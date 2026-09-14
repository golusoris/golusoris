// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package errors

import (
	stderrors "errors"
	"fmt"
	"io"
)

// CloseInto closes c and records a failure into *errp when the surrounding
// function has not already failed — the HISS-07-clean replacement for
// `defer func() { _ = c.Close() }()`. Use it with a named error return:
//
//	func read(path string) (err error) {
//	    f, err := os.Open(path)
//	    if err != nil { return err }
//	    defer errors.CloseInto(f, &err, "config: close "+path)
//	    ...
//	}
//
// A close error never masks the primary error; when both fail the primary
// wins and the close error is dropped, matching io.Copy semantics.
func CloseInto(c io.Closer, errp *error, op string) {
	if c == nil {
		return
	}
	cerr := c.Close()
	if cerr == nil || errp == nil || *errp != nil {
		return
	}
	*errp = fmt.Errorf("%s: %w", op, cerr)
}

// CloseJoin closes c and joins any failure onto *errp with errors.Join, for
// callers that must surface both the primary and the close error.
func CloseJoin(c io.Closer, errp *error, op string) {
	if c == nil {
		return
	}
	cerr := c.Close()
	if cerr == nil || errp == nil {
		return
	}
	*errp = stderrors.Join(*errp, fmt.Errorf("%s: %w", op, cerr))
}
