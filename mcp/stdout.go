package mcp

import (
	"fmt"
	"io"
	"os"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// stdin is the reader the stdio transport reads JSON-RPC from. It is a package
// var so internal tests can substitute a pipe; production reads os.Stdin.
var stdin io.ReadCloser = os.Stdin

// newStdioTransport builds the stdio transport over the given reader/writer.
// Indirected through a var so internal tests can drive the transport with an
// in-memory pair instead of the real stdin/stdout.
var newStdioTransport = func(r io.ReadCloser, w io.WriteCloser) sdkmcp.Transport {
	return &sdkmcp.IOTransport{Reader: r, Writer: w}
}

// stdoutRedirect pins the real stdout for the MCP stdio transport and replaces
// the process-global os.Stdout with a pipe whose contents are copied to stderr.
// This guarantees JSON-RPC framing on the genuine stdout while any stray
// fmt.Println / library write in app code lands on stderr instead of corrupting
// the protocol.
type stdoutRedirect struct {
	// real is the genuine stdout captured before redirection; the transport
	// writes JSON-RPC frames here.
	real *os.File
	// writeEnd is the pipe write end installed as os.Stdout.
	writeEnd *os.File
	// done closes when the copy goroutine has fully drained the pipe.
	done chan struct{}
	// sink is where redirected stray output goes (os.Stderr in production;
	// overridable in tests).
	sink io.Writer
}

// installStdoutRedirect captures os.Stdout, swaps in a pipe that drains to
// sink, and returns the redirect handle plus the pinned real stdout writer.
// Callers must call Close to restore os.Stdout. A nil sink defaults to stderr.
func installStdoutRedirect(sink io.Writer) (*stdoutRedirect, *os.File, error) {
	if sink == nil {
		sink = os.Stderr
	}
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		return nil, nil, fmt.Errorf("mcp: stdout redirect pipe: %w", err)
	}
	realOut := os.Stdout
	os.Stdout = writeEnd

	r := &stdoutRedirect{
		real:     realOut,
		writeEnd: writeEnd,
		done:     make(chan struct{}),
		sink:     sink,
	}
	go r.drain(readEnd)
	return r, realOut, nil
}

// drain copies redirected stdout writes to the sink until the write end is
// closed, then signals completion.
func (r *stdoutRedirect) drain(readEnd *os.File) {
	defer close(r.done)
	_, _ = io.Copy(r.sink, readEnd)
	_ = readEnd.Close()
}

// Close restores the original os.Stdout, closes the pipe write end, and waits
// for the drain goroutine to finish. Idempotent-safe for a single deferred
// call. It does NOT close the pinned real stdout — the process keeps it.
func (r *stdoutRedirect) Close() error {
	if r == nil {
		return nil
	}
	os.Stdout = r.real
	if err := r.writeEnd.Close(); err != nil {
		return fmt.Errorf("mcp: close stdout redirect: %w", err)
	}
	<-r.done
	return nil
}
