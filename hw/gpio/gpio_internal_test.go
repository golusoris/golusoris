// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gpio

import (
	"errors"
	"strings"
	"testing"

	periphgpio "periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/gpio/gpioreg"
	"periph.io/x/conn/v3/i2c"
	"periph.io/x/conn/v3/i2c/i2creg"
	"periph.io/x/conn/v3/i2c/i2ctest"
	"periph.io/x/conn/v3/physic"
	"periph.io/x/conn/v3/spi"
	"periph.io/x/conn/v3/spi/spireg"
	"periph.io/x/conn/v3/spi/spitest"
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

//nolint:paralleltest // initializes periph's process-global driver registry.
func TestInit(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v, want nil without host drivers", err)
	}
}

// levelPin keeps the last driven level; periph gpiotest.Pin would add clockwork to go.sum.
type levelPin struct {
	periphgpio.PinIO
	level periphgpio.Level
}

func (p *levelPin) Out(l periphgpio.Level) error {
	p.level = l
	return nil
}

func (p *levelPin) Read() periphgpio.Level { return p.level }

func TestPinHighLowRead(t *testing.T) {
	t.Parallel()
	pin := Pin{p: &levelPin{}}
	if err := pin.High(); err != nil {
		t.Fatalf("High() error = %v, want nil", err)
	}
	if got := pin.Read(); got != periphgpio.High {
		t.Fatalf("Read() after High() = %v, want High", got)
	}
	if err := pin.Low(); err != nil {
		t.Fatalf("Low() error = %v, want nil", err)
	}
	if got := pin.Read(); got != periphgpio.Low {
		t.Fatalf("Read() after Low() = %v, want Low", got)
	}
}

type errorPin struct {
	periphgpio.PinIO
	err error
}

func (p *errorPin) Out(_ periphgpio.Level) error { return p.err }

func TestPinOutputErrors(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("output failed")
	pin := Pin{p: &errorPin{err: wantErr}}
	checkHardwareError(t, pin.High(), wantErr, "gpio: high")
	checkHardwareError(t, pin.Low(), wantErr, "gpio: low")
}

func TestI2CBusTx(t *testing.T) {
	t.Parallel()
	bus := I2CBus{bus: &i2ctest.Playback{
		Ops:       []i2ctest.IO{{Addr: 0x40, W: []byte{0x01}, R: []byte{0xAB}}},
		DontPanic: true,
	}}
	read := make([]byte, 1)
	if err := bus.Tx(0x40, []byte{0x01}, read); err != nil {
		t.Fatalf("Tx() error = %v, want nil", err)
	}
	if read[0] != 0xAB {
		t.Fatalf("Tx() read = %#x, want 0xAB", read[0])
	}
	err := bus.Tx(0x40, []byte{0x01}, read)
	if err == nil || !strings.HasPrefix(err.Error(), "gpio: i2c tx 0x40") {
		t.Fatalf("exhausted Tx() error = %v, want gpio: i2c tx 0x40 prefix", err)
	}
	if closeErr := bus.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v, want nil", closeErr)
	}
}

type errorI2CBus struct {
	i2c.BusCloser
	err error
}

func (b *errorI2CBus) Close() error { return b.err }

func TestI2CBusCloseError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("I2C close failed")
	bus := I2CBus{bus: &errorI2CBus{err: wantErr}}
	checkHardwareError(t, bus.Close(), wantErr, "gpio: i2c close")
}

func TestSPIPortConnectClose(t *testing.T) {
	t.Parallel()
	port := SPIPort{port: &spitest.Playback{}}
	connection, err := port.Connect(1_000_000, spi.Mode0, 8)
	if err != nil {
		t.Fatalf("Connect() error = %v, want nil", err)
	}
	if connection == nil {
		t.Fatal("Connect() = nil, want connection")
	}
	second, secondErr := port.Connect(1_000_000, spi.Mode0, 8)
	if secondErr == nil || !strings.HasPrefix(secondErr.Error(), "gpio: spi connect") {
		t.Fatalf("second Connect() error = %v, want gpio: spi connect prefix", secondErr)
	}
	if second != nil {
		t.Fatalf("second Connect() = %v, want nil", second)
	}
	if closeErr := port.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v, want nil", closeErr)
	}
}

type errorSPIPort struct {
	spi.PortCloser
	err error
}

func (p *errorSPIPort) Connect(_ physic.Frequency, _ spi.Mode, _ int) (spi.Conn, error) {
	return nil, p.err
}

func (p *errorSPIPort) Close() error { return p.err }

func TestSPIPortErrors(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("SPI failed")
	port := SPIPort{port: &errorSPIPort{err: wantErr}}
	connection, err := port.Connect(1_000_000, spi.Mode0, 8)
	checkHardwareError(t, err, wantErr, "gpio: spi connect")
	if connection != nil {
		t.Fatalf("Connect() = %v, want nil on error", connection)
	}
	checkHardwareError(t, port.Close(), wantErr, "gpio: spi close")
}

func checkHardwareError(t *testing.T, err, want error, prefix string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapping %v", err, want)
	}
	if !strings.HasPrefix(err.Error(), prefix) {
		t.Fatalf("error = %v, want prefix %q", err, prefix)
	}
}
