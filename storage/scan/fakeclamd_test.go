package scan_test

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
)

// fakeConfig configures the fake clamd server's canned replies.
type fakeConfig struct {
	// instreamReply is the line returned after a full INSTREAM (no trailing \n).
	instreamReply string
	// pingReply is returned for a PING command (no trailing \n).
	pingReply string
}

// fakeClamd is a minimal clamd server speaking just enough of the wire protocol
// (INSTREAM chunk framing + PING) to exercise the real baruwa client end-to-end
// without Docker. It is NOT a clamd reimplementation — only the framing the
// client uses is honoured.
type fakeClamd struct {
	ln  net.Listener
	cfg fakeConfig
	wg  sync.WaitGroup
}

// startFakeClamd binds a loopback listener, serves connections until the test
// ends, and registers cleanup.
func startFakeClamd(t *testing.T, cfg fakeConfig) *fakeClamd {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fake clamd: listen: %v", err)
	}
	f := &fakeClamd{ln: ln, cfg: cfg}
	f.wg.Add(1)
	go f.serve()
	t.Cleanup(func() {
		_ = ln.Close()
		f.wg.Wait()
	})
	return f
}

func (f *fakeClamd) addr() string { return f.ln.Addr().String() }

func (f *fakeClamd) serve() {
	defer f.wg.Done()
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return // listener closed
		}
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			defer conn.Close()
			f.handle(conn)
		}()
	}
}

// handle reads one command (the client dials a fresh conn per command), serves
// the canned reply, and closes.
func (f *fakeClamd) handle(conn net.Conn) {
	r := bufio.NewReader(conn)
	cmd, err := r.ReadString('\n')
	if err != nil {
		return
	}
	cmd = strings.TrimRight(strings.TrimPrefix(cmd, "n"), "\n")
	switch cmd {
	case "PING":
		_, _ = io.WriteString(conn, f.cfg.pingReply+"\n")
	case "INSTREAM":
		if err := drainInstream(r); err != nil {
			return
		}
		_, _ = io.WriteString(conn, f.cfg.instreamReply+"\n")
	}
}

// drainInstream consumes INSTREAM chunks (4-byte big-endian length prefix +
// data) until the zero-length terminator, mirroring clamd's framing.
func drainInstream(r *bufio.Reader) error {
	var lenBuf [4]byte
	for {
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			return err
		}
		n := binary.BigEndian.Uint32(lenBuf[:])
		if n == 0 {
			return nil // terminator
		}
		if n > maxChunk {
			return errors.New("fake clamd: chunk too large")
		}
		if _, err := io.CopyN(io.Discard, r, int64(n)); err != nil {
			return err
		}
	}
}

// maxChunk bounds a single declared chunk to a sane size so a malformed length
// prefix can't make the fake server allocate/read unbounded data.
const maxChunk = 1 << 20
