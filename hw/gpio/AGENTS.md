# Agent guide — hw/gpio/

GPIO, SPI, and I²C helpers over periph.io. Stateless utility — **no fx wiring**.
Own go.mod sub-module; import directly: `github.com/golusoris/golusoris/hw/gpio`.

## API

```go
gpio.Init()                          // load host drivers — call once at startup

pin, err := gpio.OpenPin("GPIO17")   // by name
pin.High(); pin.Low(); pin.Read()

bus, err := gpio.OpenI2C(1)          // /dev/i2c-1
bus.Tx(0x48, write, read); bus.Close()

port, err := gpio.OpenSPI("/dev/spidev0.0")
conn, err := port.Connect(speedHz, mode, bits); port.Close()
```

## Why periph.io/x/conn

Pure-Go peripheral I/O with a driver registry that auto-detects the host
(Raspberry Pi, BeagleBone, etc.) at `Init` time.

## Notes

- `Init` must run before any Open call — it populates the driver registry.
- Separate go.mod because the Linux peripheral drivers are irrelevant on
  non-embedded targets.
- `OpenPin` opens for output; `Dir`/`Out`/`In` are exported for callers that
  branch on direction.
- Bus/port handles wrap closers — `Close` them; not safe for concurrent Tx.
