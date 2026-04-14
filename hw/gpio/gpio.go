// Package gpio provides GPIO, SPI, and I²C helpers using periph.io.
//
// This is a separate go.mod sub-module because periph.io/x/conn pulls Linux
// CGO drivers that are irrelevant on non-embedded targets.
// Import directly: github.com/golusoris/golusoris/hw/gpio
//
// # Initialization
//
// Call [Init] once at startup; it loads all available host drivers.
//
//	if err := gpio.Init(); err != nil {
//	    log.Fatal(err)
//	}
//
// # GPIO
//
//	pin, err := gpio.OpenPin("GPIO17", gpio.Out)
//	pin.High()
//	pin.Low()
//	pin.Toggle()
//
// # I²C
//
//	bus, err := gpio.OpenI2C(1)   // /dev/i2c-1
//	err = bus.Tx(0x48, write, read)
package gpio

import (
	"fmt"

	"periph.io/x/conn/v3/driver/driverreg"
	"periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/gpio/gpioreg"
	"periph.io/x/conn/v3/i2c"
	"periph.io/x/conn/v3/i2c/i2creg"
	"periph.io/x/conn/v3/spi"
	"periph.io/x/conn/v3/spi/spireg"
)

// Dir is pin direction (In or Out).
type Dir = gpio.Level

const (
	// Out configures a pin as output.
	Out = gpio.High
	// In configures a pin as input (use OpenInputPin).
	In = gpio.Low
)

// Init loads all available host drivers.
// Must be called before any GPIO/SPI/I²C operations.
func Init() error {
	if _, err := driverreg.Init(); err != nil {
		return fmt.Errorf("gpio: init drivers: %w", err)
	}
	return nil
}

// Pin wraps a gpio.PinIO.
type Pin struct {
	p gpio.PinIO
}

// OpenPin opens a named GPIO pin (e.g. "GPIO17") for output.
func OpenPin(name string) (*Pin, error) {
	p := gpioreg.ByName(name)
	if p == nil {
		return nil, fmt.Errorf("gpio: pin %q not found", name)
	}
	return &Pin{p: p}, nil
}

// High drives the pin high.
func (p *Pin) High() error {
	if err := p.p.Out(gpio.High); err != nil {
		return fmt.Errorf("gpio: high: %w", err)
	}
	return nil
}

// Low drives the pin low.
func (p *Pin) Low() error {
	if err := p.p.Out(gpio.Low); err != nil {
		return fmt.Errorf("gpio: low: %w", err)
	}
	return nil
}

// Read returns the current pin level.
func (p *Pin) Read() gpio.Level { return p.p.Read() }

// I2CBus wraps an i2c.BusCloser.
type I2CBus struct {
	bus i2c.BusCloser
}

// OpenI2C opens the I²C bus at busNumber (e.g. 1 for /dev/i2c-1).
func OpenI2C(busNumber int) (*I2CBus, error) {
	b, err := i2creg.Open(fmt.Sprintf("/dev/i2c-%d", busNumber))
	if err != nil {
		return nil, fmt.Errorf("gpio: open i2c %d: %w", busNumber, err)
	}
	return &I2CBus{bus: b}, nil
}

// Tx performs a write then read transaction to addr.
func (b *I2CBus) Tx(addr uint16, write, read []byte) error {
	dev := &i2c.Dev{Bus: b.bus, Addr: addr}
	if err := dev.Tx(write, read); err != nil {
		return fmt.Errorf("gpio: i2c tx 0x%02x: %w", addr, err)
	}
	return nil
}

// Close releases the bus.
func (b *I2CBus) Close() error { return b.bus.Close() }

// SPIPort wraps an spi.PortCloser.
type SPIPort struct {
	port spi.PortCloser
}

// OpenSPI opens an SPI port by name (e.g. "/dev/spidev0.0").
func OpenSPI(portName string) (*SPIPort, error) {
	p, err := spireg.Open(portName)
	if err != nil {
		return nil, fmt.Errorf("gpio: open spi %s: %w", portName, err)
	}
	return &SPIPort{port: p}, nil
}

// Connect returns a full-duplex spi.Conn at the given speed and mode.
func (p *SPIPort) Connect(speedHz int64, mode spi.Mode, bits int) (spi.Conn, error) {
	conn, err := p.port.Connect(speedHz, mode, bits)
	if err != nil {
		return nil, fmt.Errorf("gpio: spi connect: %w", err)
	}
	return conn, nil
}

// Close releases the port.
func (p *SPIPort) Close() error { return p.port.Close() }
