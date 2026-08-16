package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	hid "github.com/sstallion/go-hid"
	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

// keyStateReportID matches firmware/boot.py's KEY_STATE_REPORT_ID. It
// prefixes every HID output report Device sends.
const keyStateReportID = 1

// deviceQueueSize bounds how many decoded device→host messages Device
// holds before ReadMessage drains them. It matches emulatorQueueSize.
const deviceQueueSize = 32

// defaultPollInterval paces Open's retries when Options does not set a
// faster one.
const defaultPollInterval = 250 * time.Millisecond

// ErrAmbiguousDevice is returned by Open when more than one attached
// device matches Options.VendorID and Options.ProductID, and
// Options.SerialNumber does not name which one to use.
var ErrAmbiguousDevice = errors.New("transport: more than one matching device attached, set Options.SerialNumber")

// errNoDevice signals that no candidate matched on one discovery attempt.
// open treats it as a reason to retry, not a reason to stop.
var errNoDevice = errors.New("transport: no matching device found")

// Options configures which device Open looks for.
type Options struct {
	// VendorID and ProductID identify the macro pad's USB device
	// descriptor. Open never falls back to a volume listing to find the
	// device.
	VendorID  uint16
	ProductID uint16

	// SerialNumber picks one device out of several that share VendorID
	// and ProductID. It may be left empty when only one such device is
	// expected to be attached; Open then returns ErrAmbiguousDevice if
	// more than one is.
	SerialNumber string

	// pollInterval paces Open's retries while ctx is still open. Tests
	// set it low; production callers leave it at its zero value, which
	// open treats as defaultPollInterval.
	pollInterval time.Duration
}

// hidCandidate is what discovery learns about one attached HID device
// that might be the macro pad.
type hidCandidate struct {
	SerialNumber string
}

// hidBackend discovers and opens the HID side of the macro pad. The
// production implementation, hidapiBackend, wraps
// github.com/sstallion/go-hid; tests substitute a fake so Open's retry
// and matching logic runs with no board attached.
type hidBackend interface {
	// list returns one hidCandidate per attached HID device matching vid
	// and pid.
	list(vid, pid uint16) ([]hidCandidate, error)
	// open opens the HID device matching vid, pid, and serialNumber for
	// writing key-state output reports.
	open(vid, pid uint16, serialNumber string) (io.WriteCloser, error)
}

// serialBackend discovers and opens the CDC side of the macro pad. The
// production implementation, serialPortBackend, wraps go.bug.st/serial.
type serialBackend interface {
	// findPort returns the name of the CDC port carrying serialNumber,
	// or errNoDevice if none is attached yet.
	findPort(serialNumber string) (string, error)
	// open opens the named CDC port for reading device→host frames and
	// writing host→device ones — see "Set custom glyph" in
	// docs/wire-protocol.md.
	open(portName string) (io.ReadWriteCloser, error)
}

type hidapiBackend struct{}

func (hidapiBackend) list(vid, pid uint16) ([]hidCandidate, error) {
	var candidates []hidCandidate
	err := hid.Enumerate(vid, pid, func(info *hid.DeviceInfo) error {
		candidates = append(candidates, hidCandidate{SerialNumber: info.SerialNbr})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return candidates, nil
}

func (hidapiBackend) open(vid, pid uint16, serialNumber string) (io.WriteCloser, error) {
	return hid.Open(vid, pid, serialNumber)
}

type serialPortBackend struct{}

func (serialPortBackend) findPort(serialNumber string) (string, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return "", err
	}
	for _, p := range ports {
		if p.IsUSB && p.SerialNumber == serialNumber {
			return p.Name, nil
		}
	}
	return "", errNoDevice
}

func (serialPortBackend) open(portName string) (io.ReadWriteCloser, error) {
	// CDC ACM ignores the requested baud rate, but go.bug.st/serial
	// rejects a Mode with no DataBits set.
	return serial.Open(portName, &serial.Mode{BaudRate: 115200, DataBits: 8})
}

// Device is a Transport that talks to a real macro pad over HID and CDC
// serial, using the operating system's own class drivers. Open discovers
// and connects it. See docs/wire-protocol.md for the byte layout it reads
// and writes.
type Device struct {
	hid    io.WriteCloser
	serial io.ReadWriteCloser

	msgQueue chan Message
}

var _ Transport = (*Device)(nil)

// Open finds the macro pad matching opts and returns a Device connected
// to it. Discovery matches opts.VendorID and opts.ProductID against
// attached HID devices, then correlates the match to a CDC serial port by
// USB serial number — never by a volume listing. Open retries until ctx
// is done, so a caller can survive the reboot that flashing new firmware
// causes.
func Open(ctx context.Context, opts Options) (*Device, error) {
	return open(ctx, opts, hidapiBackend{}, serialPortBackend{})
}

func open(ctx context.Context, opts Options, hidB hidBackend, serialB serialBackend) (*Device, error) {
	interval := opts.pollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}

	for {
		d, err := tryOpen(opts, hidB, serialB)
		if err == nil {
			return d, nil
		}
		if errors.Is(err, ErrAmbiguousDevice) {
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf(
				"transport: no device with vendor ID %#04x and product ID %#04x found: %w",
				opts.VendorID, opts.ProductID, ctx.Err(),
			)
		case <-time.After(interval):
		}
	}
}

func tryOpen(opts Options, hidB hidBackend, serialB serialBackend) (*Device, error) {
	candidates, err := hidB.list(opts.VendorID, opts.ProductID)
	if err != nil {
		return nil, err
	}
	serialNumber, err := matchOne(candidates, opts.SerialNumber)
	if err != nil {
		return nil, err
	}

	portName, err := serialB.findPort(serialNumber)
	if err != nil {
		return nil, err
	}

	hidConn, err := hidB.open(opts.VendorID, opts.ProductID, serialNumber)
	if err != nil {
		return nil, err
	}
	serialConn, err := serialB.open(portName)
	if err != nil {
		hidConn.Close()
		return nil, err
	}

	return newDevice(hidConn, serialConn), nil
}

// matchOne picks the one HID candidate Open should connect to. When want
// is empty and exactly one candidate is present, that candidate matches.
// When more than one candidate is present, want must name the one to use.
func matchOne(candidates []hidCandidate, want string) (string, error) {
	if want != "" {
		for _, c := range candidates {
			if c.SerialNumber == want {
				return want, nil
			}
		}
		return "", errNoDevice
	}
	switch len(candidates) {
	case 0:
		return "", errNoDevice
	case 1:
		return candidates[0].SerialNumber, nil
	default:
		return "", ErrAmbiguousDevice
	}
}

func newDevice(hidConn io.WriteCloser, serialConn io.ReadWriteCloser) *Device {
	d := &Device{
		hid:      hidConn,
		serial:   serialConn,
		msgQueue: make(chan Message, deviceQueueSize),
	}
	go d.receiveMessages()
	return d
}

// receiveMessages decodes frames from the CDC stream until it errors,
// which happens once Close releases d.serial. It matches Emulator's
// receiveMessages so both Transport implementations share the same
// read-to-channel shape.
func (d *Device) receiveMessages() {
	defer close(d.msgQueue)
	for {
		msg, err := readMessage(d.serial)
		if err != nil {
			return
		}
		d.msgQueue <- msg
	}
}

// SendKeyState implements Transport. It writes one HID output report
// carrying ks, encoded per docs/wire-protocol.md and prefixed by
// firmware/boot.py's KEY_STATE_REPORT_ID.
func (d *Device) SendKeyState(ks KeyState) error {
	var buf bytes.Buffer
	buf.WriteByte(keyStateReportID)
	if err := encodeKeyState(&buf, ks); err != nil {
		return err
	}
	_, err := d.hid.Write(buf.Bytes())
	return err
}

// SendCustomGlyph implements Transport. It writes a framed Set custom
// glyph message to the device over the same CDC connection ReadMessage
// reads from — the two directions are independent byte streams on one
// full-duplex serial port, so a concurrent ReadMessage is unaffected.
func (d *Device) SendCustomGlyph(keyIndex byte, pixels []byte) error {
	payload, err := encodeCustomGlyph(keyIndex, pixels)
	if err != nil {
		return err
	}
	return writeFrame(d.serial, MessageTypeSetCustomGlyph, payload)
}

// ReadMessage implements Transport. It decodes one frame from the CDC
// stream using the same readMessage the emulator uses, so both paths
// share one framing implementation. ReadMessage does not route by type;
// the caller distinguishes Event from AudioChunk on Message.Type.
func (d *Device) ReadMessage() (Message, error) {
	msg, ok := <-d.msgQueue
	if !ok {
		return Message{}, io.EOF
	}
	return msg, nil
}

// Close implements Transport. It releases both the HID and CDC handles.
// A ReadMessage call blocked on the CDC stream returns io.EOF.
func (d *Device) Close() error {
	herr := d.hid.Close()
	serr := d.serial.Close()
	if herr != nil {
		return herr
	}
	return serr
}
