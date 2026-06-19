package scan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/baruwa-enterprise/clamd"

	"github.com/golusoris/golusoris/clock"
)

// clamdStatusOK is the clamd status token for a clean scan; clamdStatusFound is
// the token when a signature fires.
const (
	clamdStatusOK    = "OK"
	clamdStatusFound = "FOUND"
)

// instreamFunc is the seam over clamd's streaming scan. Defaulting to the real
// *clamd.Client.ScanReader, it lets unit tests feed canned responses + errors
// through the full verdict-mapping path without a live socket.
type instreamFunc func(ctx context.Context, r io.Reader) ([]*clamd.Response, error)

// pingFunc is the seam over clamd's reachability probe.
type pingFunc func(ctx context.Context) (bool, error)

// clamdScanner is the production backend; it wraps the clamd client behind two
// function seams so the mapping logic is unit-testable.
type clamdScanner struct {
	instream instreamFunc
	ping     pingFunc
	logger   *slog.Logger
	clock    clock.Clock
	maxSize  int64
}

// NewClamdScanner builds a clamd-backed [Scanner] from already-resolved
// [ClamdOptions] (MaxSize in bytes). Apps usually get a Scanner via [Module];
// use this for direct construction in tests or bespoke wiring.
func NewClamdScanner(opts ClamdOptions, logger *slog.Logger, clk clock.Clock) (Scanner, error) {
	return newClamdScanner(opts, logger, clk)
}

// newClamdScanner builds a clamd-backed Scanner from already-validated options.
func newClamdScanner(
	opts ClamdOptions,
	logger *slog.Logger,
	clk clock.Clock,
) (*clamdScanner, error) {
	client, err := clamd.NewClient(opts.Network, opts.Address)
	if err != nil {
		return nil, fmt.Errorf("storage/scan: build clamd client: %w", err)
	}
	client.SetConnTimeout(opts.ConnTimeout)
	client.SetCmdTimeout(opts.CmdTimeout)
	client.SetConnRetries(opts.ConnRetries)
	client.SetConnSleep(opts.ConnSleep)

	return &clamdScanner{
		instream: client.ScanReader,
		ping:     client.Ping,
		logger:   logger,
		clock:    clk,
		maxSize:  opts.MaxSize,
	}, nil
}

// Scan streams r to clamd and maps the structured response to a Verdict.
func (s *clamdScanner) Scan(ctx context.Context, r io.Reader) (Verdict, error) {
	if s.maxSize > 0 {
		r = newLimitReader(r, s.maxSize)
	}
	responses, err := s.instream(ctx, r)
	if err != nil {
		if errors.Is(err, errLimitExceeded) {
			return Verdict{}, fmt.Errorf("storage/scan: %w", ErrTooLarge)
		}
		return Verdict{}, fmt.Errorf("storage/scan: instream scan: %w: %w", ErrUnavailable, err)
	}
	return mapResponses(responses)
}

// ScanStrict folds an infected verdict into an ErrInfected-wrapping error.
func (s *clamdScanner) ScanStrict(ctx context.Context, r io.Reader) error {
	v, err := s.Scan(ctx, r)
	if err != nil {
		return err
	}
	if !v.Clean {
		return fmt.Errorf("storage/scan: %w: %s", ErrInfected, v.Signature)
	}
	return nil
}

// Ping probes daemon reachability, wrapping any failure as ErrUnavailable.
func (s *clamdScanner) Ping(ctx context.Context) error {
	ok, err := s.ping(ctx)
	if err != nil {
		return fmt.Errorf("storage/scan: ping: %w: %w", ErrUnavailable, err)
	}
	if !ok {
		return fmt.Errorf("storage/scan: ping: %w: no PONG", ErrUnavailable)
	}
	return nil
}

// mapResponses folds clamd's []*Response into a single Verdict. clamd returns
// one line per scanned item; INSTREAM yields exactly one, but we defensively
// treat ANY FOUND line as infected and require an explicit OK to be clean.
func mapResponses(responses []*clamd.Response) (Verdict, error) {
	if len(responses) == 0 {
		return Verdict{}, fmt.Errorf("storage/scan: empty clamd response: %w", ErrUnavailable)
	}
	for _, resp := range responses {
		if resp == nil {
			continue
		}
		if resp.Status == clamdStatusFound {
			return Verdict{Clean: false, Signature: resp.Signature, Raw: resp.Raw}, nil
		}
	}
	first := responses[0]
	if first.Status != clamdStatusOK {
		return Verdict{}, fmt.Errorf(
			"storage/scan: unexpected clamd status %q: %w", first.Status, ErrUnavailable,
		)
	}
	return Verdict{Clean: true, Raw: first.Raw}, nil
}
