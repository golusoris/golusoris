// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gpio

import (
	"testing"

	periphgpio "periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/gpio/gpioreg"
	"periph.io/x/conn/v3/i2c"
	"periph.io/x/conn/v3/i2c/i2creg"
	"periph.io/x/conn/v3/spi"
	"periph.io/x/conn/v3/spi/spireg"
)

const typedNilPinName = "GOLUSORIS_TYPED_NIL_PIN"

type typedNilPin struct{ periphgpio.PinIO }

func (*typedNilPin) Name() string { return typedNilPinName }

type typedNilI2CBus struct{ i2c.BusCloser }

type typedNilSPIPort struct{ spi.PortCloser }

//nolint:paralleltest // mutates periph's process-global pin registry.
func TestOpenPinRejectsTypedNilRegistryEntry(t *testing.T) {
	var pin *typedNilPin
	if err := gpioreg.Register(pin); err != nil {
		t.Fatalf("register typed-nil pin: %v", err)
	}
	t.Cleanup(func() {
		if err := gpioreg.Unregister(typedNilPinName); err != nil {
			t.Errorf("unregister typed-nil pin: %v", err)
		}
	})

	if _, err := OpenPin(typedNilPinName); err == nil {
		t.Fatal("OpenPin accepted a typed-nil registry entry")
	}
}

//nolint:paralleltest // mutates periph's process-global I2C registry.
func TestOpenI2CRejectsTypedNilRegistryEntry(t *testing.T) {
	const (
		busNumber = 32001
		busName   = "/dev/i2c-32001"
	)
	if err := i2creg.Register(busName, nil, -1, func() (i2c.BusCloser, error) {
		var bus *typedNilI2CBus
		return bus, nil
	}); err != nil {
		t.Fatalf("register typed-nil I2C bus: %v", err)
	}
	t.Cleanup(func() {
		if err := i2creg.Unregister(busName); err != nil {
			t.Errorf("unregister typed-nil I2C bus: %v", err)
		}
	})

	if _, err := OpenI2C(busNumber); err == nil {
		t.Fatal("OpenI2C accepted a typed-nil registry entry")
	}
}

//nolint:paralleltest // mutates periph's process-global SPI registry.
func TestOpenSPIRejectsTypedNilRegistryEntry(t *testing.T) {
	const portName = "/dev/spidev-golusoris-typed-nil"
	if err := spireg.Register(portName, nil, -1, func() (spi.PortCloser, error) {
		var port *typedNilSPIPort
		return port, nil
	}); err != nil {
		t.Fatalf("register typed-nil SPI port: %v", err)
	}
	t.Cleanup(func() {
		if err := spireg.Unregister(portName); err != nil {
			t.Errorf("unregister typed-nil SPI port: %v", err)
		}
	})

	if _, err := OpenSPI(portName); err == nil {
		t.Fatal("OpenSPI accepted a typed-nil registry entry")
	}
}
