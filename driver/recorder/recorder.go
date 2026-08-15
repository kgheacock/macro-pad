// Package recorder writes every device→host message read from a
// transport.Transport to one JSONL flight-recorder file: a header line
// naming the estimated device/host clock offset, followed by one JSON
// object per message, each carrying the host's wall-clock arrival time
// and, where the message has one, the device's monotonic timestamp.
//
// See tasks/ongoing/0025-trace-ring-buffer-flight-recorder.md for the
// design decision.
package recorder

import (
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// estimatorName identifies the clock-offset estimator in the header line.
// It is the minimum of host_arrival - device_us over every sample seen —
// the sample with the least one-way delay, which bounds the offset from
// one side and only improves as more records arrive.
const estimatorName = "min_one_way_delay"

// header is the file's first line, naming the estimated offset between
// the device's monotonic clock and the host's wall clock.
type header struct {
	// OffsetUs, added to a message's DeviceUs, estimates the host's
	// Unix time in microseconds at which the device's clock read 0.
	OffsetUs  int64  `json:"offset_us"`
	Samples   int    `json:"samples"`
	Estimator string `json:"estimator"`
}

// line is one decoded device→host message, in the JSON shape a message
// of its Type populates. Fields that do not apply to Type are omitted.
type line struct {
	Type     string    `json:"type"`
	HostTime time.Time `json:"host_time"`
	DeviceUs *uint64   `json:"device_us,omitempty"`

	KeyIndex *byte   `json:"key_index,omitempty"`
	Event    string  `json:"event,omitempty"`
	Code     *byte   `json:"code,omitempty"`
	Payload  *uint16 `json:"payload,omitempty"`
	StreamID *byte   `json:"stream_id,omitempty"`
	Length   *int    `json:"length,omitempty"`
	Final    *bool   `json:"final,omitempty"`
	Nonce    *byte   `json:"nonce,omitempty"`
}

// Recorder wraps a transport.Transport, buffering every device→host
// message it reads, with the host's wall-clock arrival time, until Close
// writes them to the io.Writer passed to New.
type Recorder struct {
	t   transport.Transport
	w   io.Writer
	now func() time.Time

	lines []line

	haveOffset  bool
	minOffsetUs int64
	samples     int
}

// New returns a Recorder that reads from t and writes to w once Close
// runs.
func New(t transport.Transport, w io.Writer) *Recorder {
	return &Recorder{t: t, w: w, now: time.Now}
}

// Run calls t.ReadMessage in a loop, buffering each message with its host
// arrival time, until ReadMessage returns an error. It returns nil when
// that error is io.EOF — t.Close during the capture window — and the
// error itself otherwise.
func (r *Recorder) Run() error {
	for {
		msg, err := r.t.ReadMessage()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		r.append(msg, r.now())
	}
}

func (r *Recorder) append(msg transport.Message, hostTime time.Time) {
	l := line{Type: typeName(msg.Type), HostTime: hostTime}

	switch msg.Type {
	case transport.MessageTypeEvent:
		l.KeyIndex = byteRef(msg.Event.KeyIndex)
		if msg.Event.Type == transport.EventPress {
			l.Event = "press"
		} else {
			l.Event = "release"
		}
		l.DeviceUs = uint64Ref(msg.Event.Timestamp)
		r.observe(msg.Event.Timestamp, hostTime)
	case transport.MessageTypeTrace:
		l.Code = byteRef(byte(msg.Trace.Code))
		l.KeyIndex = byteRef(msg.Trace.Key)
		l.Payload = uint16Ref(msg.Trace.Payload)
		l.DeviceUs = uint64Ref(msg.Trace.Timestamp)
		r.observe(msg.Trace.Timestamp, hostTime)
	case transport.MessageTypeAudioChunk:
		l.StreamID = byteRef(msg.AudioChunk.StreamID)
		l.Length = intRef(len(msg.AudioChunk.PCM))
		l.Final = boolRef(msg.AudioChunk.Final)
	case transport.MessageTypePong:
		l.Nonce = byteRef(msg.Pong.Nonce)
	}

	r.lines = append(r.lines, l)
}

// observe folds one (deviceUs, hostTime) sample into the running offset
// estimate: the minimum of hostTime - deviceUs seen so far.
func (r *Recorder) observe(deviceUs uint64, hostTime time.Time) {
	offset := hostTime.UnixMicro() - int64(deviceUs)
	if !r.haveOffset || offset < r.minOffsetUs {
		r.minOffsetUs = offset
	}
	r.haveOffset = true
	r.samples++
}

// Close writes the header line, naming the estimated clock offset, then
// one JSON line per message Run buffered, in the order they arrived, to
// the writer passed to New. It does not close t or an underlying file;
// the caller owns both.
func (r *Recorder) Close() error {
	enc := json.NewEncoder(r.w)
	if err := enc.Encode(header{
		OffsetUs:  r.minOffsetUs,
		Samples:   r.samples,
		Estimator: estimatorName,
	}); err != nil {
		return err
	}
	for _, l := range r.lines {
		if err := enc.Encode(l); err != nil {
			return err
		}
	}
	return nil
}

func typeName(t transport.MessageType) string {
	switch t {
	case transport.MessageTypeEvent:
		return "event"
	case transport.MessageTypeAudioChunk:
		return "audio_chunk"
	case transport.MessageTypePong:
		return "pong"
	case transport.MessageTypeTrace:
		return "trace"
	default:
		return "unknown"
	}
}

func byteRef(b byte) *byte       { return &b }
func uint16Ref(v uint16) *uint16 { return &v }
func uint64Ref(v uint64) *uint64 { return &v }
func intRef(v int) *int          { return &v }
func boolRef(v bool) *bool       { return &v }
