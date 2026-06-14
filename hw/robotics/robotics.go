// Package robotics provides helpers for robotic platforms using gobot.
//
// This is a separate go.mod sub-module because gobot pulls CGO adapters for
// various hardware platforms (Arduino, Raspberry Pi, drones).
// Import directly: github.com/golusoris/golusoris/hw/robotics
//
// Usage:
//
//	master := robotics.NewMaster()
//	master.AddRobot(robotics.NewRobot("my-robot", func() {
//	    // robot work function
//	}))
//	master.Start()
package robotics

import (
	"fmt"

	"gobot.io/x/gobot/v2"
)

// Master wraps gobot.Manager for managing multiple robots.
type Master struct {
	m *gobot.Manager
}

// NewMaster creates a new gobot Manager.
func NewMaster() *Master {
	return &Master{m: gobot.NewManager()}
}

// AddRobot registers a robot with the master.
func (m *Master) AddRobot(r *gobot.Robot) { m.m.AddRobot(r) }

// Start starts the master and all registered robots (blocks until stopped).
func (m *Master) Start() error {
	if err := m.m.Start(); err != nil {
		return fmt.Errorf("robotics: start: %w", err)
	}
	return nil
}

// Stop stops the master and all robots.
func (m *Master) Stop() error {
	if err := m.m.Stop(); err != nil {
		return fmt.Errorf("robotics: stop: %w", err)
	}
	return nil
}

// Robot is a re-export of gobot.Robot.
type Robot = gobot.Robot

// NewRobot creates a gobot robot with the given name and work function.
func NewRobot(name string, work func()) *gobot.Robot {
	return gobot.NewRobot(name, []gobot.Connection{}, []gobot.Device{}, work)
}

// Connection is gobot.Connection.
type Connection = gobot.Connection

// Device is gobot.Device.
type Device = gobot.Device
