package plugin

import (
	"sync"
	"time"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// longPressThreshold is how long a key must stay pressed, measured by the
// device's own event timestamps, to resolve as a long press instead of a
// short click. See task 0013's Risks: this is a fixed threshold, not
// adaptive to a person's actual pressing speed.
const longPressThreshold = 500 * time.Millisecond

// doublePressWindow is how long clickResolver waits, in wall-clock time,
// after a short click's release before deciding it was a single press, not
// the first half of a double press. Wall-clock time, not an event
// timestamp, because a second press that never arrives has no timestamp to
// measure against.
const doublePressWindow = 400 * time.Millisecond

// keyClickState tracks one key's in-progress click resolution.
type keyClickState struct {
	pressed        bool
	pressTimestamp uint64 // microseconds, from the press event that started the current hold

	// awaitingSecond is true once a short click's release has started the
	// doublePressWindow timer, waiting to see whether a second press
	// follows. pendingTimer fires singlePress if it doesn't.
	awaitingSecond bool
	pendingTimer   *time.Timer
}

// clickResolver watches every key's raw press/release events and calls
// emit with SignalSinglePress, SignalDoublePress, or SignalLongPress once
// a pair resolves to one. It holds no reference to a Server, so it is
// tested with no WebSocket connection.
type clickResolver struct {
	mu    sync.Mutex
	state map[byte]*keyClickState
	emit  func(keyIndex byte, name string)
}

func newClickResolver(emit func(keyIndex byte, name string)) *clickResolver {
	return &clickResolver{
		state: make(map[byte]*keyClickState),
		emit:  emit,
	}
}

// handleEvent feeds one decoded press or release into the resolver. Call
// it for every transport.Event dispatchLoop reads, in order.
func (r *clickResolver) handleEvent(ev transport.Event) {
	r.mu.Lock()
	st, ok := r.state[ev.KeyIndex]
	if !ok {
		st = &keyClickState{}
		r.state[ev.KeyIndex] = st
	}

	switch ev.Type {
	case transport.EventPress:
		st.pressTimestamp = ev.Timestamp
		st.pressed = true
		if st.pendingTimer != nil {
			// A second press arrived inside the double-press window: this
			// pair, not the pending timer, now decides single vs. double.
			st.pendingTimer.Stop()
			st.pendingTimer = nil
		}
		r.mu.Unlock()

	case transport.EventRelease:
		if !st.pressed {
			r.mu.Unlock()
			return
		}
		st.pressed = false
		duration := time.Duration(ev.Timestamp-st.pressTimestamp) * time.Microsecond
		wasAwaitingSecond := st.awaitingSecond
		st.awaitingSecond = false
		keyIndex := ev.KeyIndex

		if duration >= longPressThreshold {
			r.mu.Unlock()
			r.emit(keyIndex, SignalLongPress)
			return
		}
		if wasAwaitingSecond {
			r.mu.Unlock()
			r.emit(keyIndex, SignalDoublePress)
			return
		}

		st.awaitingSecond = true
		st.pendingTimer = time.AfterFunc(doublePressWindow, func() {
			r.mu.Lock()
			s := r.state[keyIndex]
			fire := s != nil && s.awaitingSecond
			if fire {
				s.awaitingSecond = false
				s.pendingTimer = nil
			}
			r.mu.Unlock()
			if fire {
				r.emit(keyIndex, SignalSinglePress)
			}
		})
		r.mu.Unlock()
	}
}
