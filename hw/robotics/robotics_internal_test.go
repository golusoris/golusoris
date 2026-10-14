// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package robotics

import (
	"errors"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"testing"
	"time"

	"gobot.io/x/gobot/v2"
)

// TestMasterRunsRobotWorkUntilInterrupt is not parallel: it sends os.Interrupt
// to the test process, which every signal subscriber receives.
//
//nolint:paralleltest // os.Interrupt reaches every test in the process.
func TestMasterRunsRobotWorkUntilInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows: a process cannot send os.Interrupt to itself")
	}
	// Our own subscription keeps an interrupt sent before Start subscribes
	// from terminating the test binary.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, os.Interrupt)
	t.Cleanup(func() { signal.Stop(guard) })

	m := NewMaster()
	worked := make(chan struct{})
	m.AddRobot(NewRobot("worker-bot", func() { close(worked) }))
	done := make(chan error, 1)
	go func() { done <- m.Start() }()

	select {
	case <-worked:
	case <-time.After(10 * time.Second):
		t.Fatal("robot work did not run after Start")
	}
	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("find own process: %v", err)
	}
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for range 200 { // 200 x 50ms = 10s
		if err := self.Signal(os.Interrupt); err != nil {
			t.Fatalf("send interrupt: %v", err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			if m.m.Running() {
				t.Fatal("master still running after interrupt")
			}
			return
		case <-tick.C:
		}
	}
	t.Fatal("Start did not return after interrupt")
}

func TestMasterStopWithoutStart(t *testing.T) {
	t.Parallel()
	if err := NewMaster().Stop(); err != nil {
		t.Fatalf("Stop() on an unstarted master error = %v", err)
	}
}

var errConnect = errors.New("connect refused")

// failingConn fails to connect, so Start returns before it waits for a signal.
type failingConn struct{}

func (failingConn) Name() string    { return "failing" }
func (failingConn) SetName(string)  {}
func (failingConn) Connect() error  { return errConnect }
func (failingConn) Finalize() error { return nil }

func TestMasterStartWrapsRobotError(t *testing.T) {
	t.Parallel()
	m := NewMaster()
	m.AddRobot(gobot.NewRobot("broken-bot", []gobot.Connection{failingConn{}}))

	err := m.Start()
	if !errors.Is(err, errConnect) || !strings.HasPrefix(err.Error(), "robotics: start") {
		t.Fatalf("Start() error = %v, want robotics: start wrapping %v", err, errConnect)
	}
}
