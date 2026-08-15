package transport

import (
	"io"
	"sync"
)

// fanoutQueueSize bounds how many undelivered messages one subscriber's
// queue holds. It matches deviceQueueSize.
const fanoutQueueSize = 32

// Fanout lets more than one consumer read the same Transport's
// device→host messages. Transport.ReadMessage supports exactly one
// caller — Device and Emulator each drain one internal channel, not
// duplicate it — so Fanout's Run is that one caller, and hands every
// message it reads to each subscriber's own queue instead.
type Fanout struct {
	t Transport

	mu   sync.Mutex
	subs map[*fanoutSub]struct{}
}

type fanoutSub struct {
	ch     chan Message
	closed bool // guarded by Fanout.mu; set before ch is closed, checked to avoid a double close
}

// NewFanout creates a Fanout reading from t. Call Run, in its own
// goroutine, to start delivering messages to subscribers.
func NewFanout(t Transport) *Fanout {
	return &Fanout{t: t, subs: make(map[*fanoutSub]struct{})}
}

// Subscribe returns a Transport backed by f: its ReadMessage delivers
// every message Run reads from here on, from this subscriber's own
// bounded queue; SendKeyState forwards to the underlying Transport. A
// subscriber that reads slower than messages arrive drops the oldest
// undelivered message rather than blocking Run or any other subscriber —
// the same non-blocking-per-consumer policy driver/plugin.Server uses
// for its own clients. Close unsubscribes; it does not close the
// underlying Transport, which other subscribers may still be reading.
func (f *Fanout) Subscribe() Transport {
	s := &fanoutSub{ch: make(chan Message, fanoutQueueSize)}
	f.mu.Lock()
	f.subs[s] = struct{}{}
	f.mu.Unlock()
	return &fanoutReader{fanout: f, sub: s, t: f.t}
}

// unsubscribe removes s, so Run stops trying to deliver to it, and closes
// s's queue so a ReadMessage call blocked on it returns io.EOF. It is
// safe to call more than once, and safe to race with Run observing the
// underlying Transport close at the same time.
func (f *Fanout) unsubscribe(s *fanoutSub) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.subs, s)
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
}

// Run reads from the underlying Transport until it errors, which happens
// once the Transport is closed, delivering each message to every current
// subscriber. It then closes every subscriber's queue, so a blocked
// ReadMessage call on any of them returns io.EOF. Call it once.
func (f *Fanout) Run() {
	for {
		msg, err := f.t.ReadMessage()
		if err != nil {
			f.closeAll()
			return
		}

		f.mu.Lock()
		for s := range f.subs {
			select {
			case s.ch <- msg:
			default:
				// The subscriber's queue is full: it is not keeping
				// up. Dropping here, instead of blocking, keeps one
				// slow subscriber from stalling delivery to the rest.
			}
		}
		f.mu.Unlock()
	}
}

func (f *Fanout) closeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for s := range f.subs {
		s.closed = true
		close(s.ch)
	}
	f.subs = make(map[*fanoutSub]struct{})
}

// fanoutReader implements Transport for one Fanout subscriber.
type fanoutReader struct {
	fanout *Fanout
	sub    *fanoutSub
	t      Transport
}

var _ Transport = (*fanoutReader)(nil)

func (r *fanoutReader) SendKeyState(ks KeyState) error {
	return r.t.SendKeyState(ks)
}

func (r *fanoutReader) ReadMessage() (Message, error) {
	msg, ok := <-r.sub.ch
	if !ok {
		return Message{}, io.EOF
	}
	return msg, nil
}

// Close unsubscribes from the Fanout. It does not close the underlying
// Transport, which other subscribers may still be reading; the Fanout's
// owner closes that once, separately.
func (r *fanoutReader) Close() error {
	r.fanout.unsubscribe(r.sub)
	return nil
}
