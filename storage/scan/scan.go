// Package scan scans untrusted upload bytes for malware via ClamAV's clamd
// daemon (over TCP or a unix socket) and maps the result to a typed [Verdict].
//
// It sits in the trust boundary for user-supplied bytes, so it is
// security-critical. The module deliberately does NOT mutate storage.Bucket;
// callers compose it — scan before Put, or scan on Get for legacy data:
//
//	v, err := scanner.Scan(ctx, r)
//	if err != nil { /* scanner unavailable — fail closed */ }
//	if !v.Clean { /* reject: v.Signature names the detection */ }
//
// Or, for a one-shot guard, fold the infected verdict into an error:
//
//	if err := scanner.ScanStrict(ctx, r); err != nil {
//	    if errors.Is(err, scan.ErrInfected) { /* reject */ }
//	    // else: ErrUnavailable — fail closed
//	}
//
// A [Scanner] is safe for concurrent use by multiple goroutines.
package scan

import (
	"context"
	"errors"
	"io"
)

// Sentinel errors for the verdict + transport split. Callers use errors.Is to
// decide fail-open vs fail-closed and to distinguish a detection from an outage.
var (
	// ErrInfected wraps a detection: returned by ScanStrict when a signature
	// fires. Scan reports the same condition via Verdict.Clean == false.
	ErrInfected = errors.New("storage/scan: malware detected")
	// ErrUnavailable wraps any clamd transport/daemon failure (dial, timeout,
	// stream-limit-exceeded), distinct from a clean/infected verdict.
	ErrUnavailable = errors.New("storage/scan: scanner unavailable")
	// ErrTooLarge wraps the pre-stream size guard: the reader exceeds the
	// configured max_size (mirrors clamd StreamMaxLength) and is rejected
	// before a connection is dialed.
	ErrTooLarge = errors.New("storage/scan: stream exceeds max size")
)

// Verdict is the typed outcome of a scan.
type Verdict struct {
	// Clean is true iff clamd returned OK / no signature.
	Clean bool
	// Signature is the malware name when !Clean (e.g. "Eicar-Test-Signature");
	// "" when Clean.
	Signature string
	// Raw is the raw clamd response line, retained for audit logging.
	Raw string
}

// Scanner scans untrusted bytes for malware. Implementations MUST be safe for
// concurrent use by multiple goroutines.
type Scanner interface {
	// Scan streams r to the backend and returns a typed Verdict. Transport
	// failures return (Verdict{}, err) wrapping ErrUnavailable; an infected
	// file is a successful scan with Clean=false and a nil error.
	Scan(ctx context.Context, r io.Reader) (Verdict, error)
	// ScanStrict is Scan but folds an infected verdict into an error wrapping
	// ErrInfected — ergonomic for scan-before-Put guards.
	ScanStrict(ctx context.Context, r io.Reader) error
	// Ping reports daemon reachability (used by the health probe).
	Ping(ctx context.Context) error
}
