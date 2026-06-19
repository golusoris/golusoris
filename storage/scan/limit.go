package scan

import (
	"errors"
	"io"
)

// errLimitExceeded is the internal signal a limitReader emits once the byte cap
// is exceeded. Scan maps it to the public ErrTooLarge so the streaming path and
// the (rare) known-size path report the same error.
var errLimitExceeded = errors.New("storage/scan: limit reader cap exceeded")

// limitReader caps the bytes drawn from an underlying reader. Unlike
// io.LimitReader (which signals EOF at the cap, silently truncating), it returns
// errLimitExceeded so an over-cap upload is rejected rather than scanned partial.
type limitReader struct {
	r         io.Reader
	remaining int64
}

// newLimitReader wraps r so reads past limit bytes fail with errLimitExceeded.
func newLimitReader(r io.Reader, limit int64) *limitReader {
	return &limitReader{r: r, remaining: limit}
}

// Read enforces the cap: it allows the reader to deliver up to remaining+1 bytes
// so crossing the limit is detected, then returns errLimitExceeded.
func (l *limitReader) Read(p []byte) (int, error) {
	if l.remaining < 0 {
		return 0, errLimitExceeded
	}
	// Permit one extra byte so a stream exactly at the cap reads clean, but one
	// over trips the guard on the next read.
	if int64(len(p)) > l.remaining+1 {
		p = p[:l.remaining+1]
	}
	n, err := l.r.Read(p)
	l.remaining -= int64(n)
	if l.remaining < 0 {
		return n, errLimitExceeded
	}
	return n, err //nolint:wrapcheck // io.EOF must propagate verbatim to readers
}
