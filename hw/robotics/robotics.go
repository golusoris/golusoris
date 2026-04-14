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
	"gobot.io/x/gobot/v2"
)

// Master wraps gobot.Master for managing multiple robots.
type Master struct {
	m *gobot.Master
}

// NewMaster creates a new gobot Master.
func NewMaster() *Master {
	return &Master{m: gobot.NewMaster()}
}

// AddRobot registers a robot with the master.
func (m *Master) AddRobot(r *gobot.Robot) { m.m.AddRobot(r) }

// Start starts the master and all registered robots (blocks until stopped).
func (m *Master) Start() error { return m.m.Start() }

// Stop stops the master and all robots.
func (m *Master) Stop() error { return m.m.Stop() }

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
