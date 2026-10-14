package claudestatus

import (
	"testing"

	"github.com/kgheacock/macro-pad/driver/api"
	"github.com/kgheacock/macro-pad/driver/host"
	"github.com/kgheacock/macro-pad/driver/host/hosttest"
	"github.com/kgheacock/macro-pad/driver/plugin"
)

func named(t *testing.T, name string) host.KeyState {
	t.Helper()
	color, blink, ok := api.NamedState(name)
	if !ok {
		t.Fatalf("no named state %q", name)
	}
	return host.KeyState{Color: color, Blink: blink}
}

func TestReactsToProcessWaitingAndDone(t *testing.T) {
	h := hosttest.New(t, 0)
	if err := h.Start(&Plugin{}); err != nil {
		t.Fatal(err)
	}

	h.Press(0) // a raw event: ignored
	h.Signal(0, plugin.SignalSinglePress)
	if got := h.KeyState(0); got.Color != 0 || got.Blink {
		t.Fatalf("after unrelated input, key 0 = %+v, want untouched", got)
	}

	h.Signal(0, plugin.SignalProcessWaiting)
	if got, want := h.KeyState(0), named(t, "Waiting"); got.Color != want.Color || got.Blink != want.Blink {
		t.Errorf("after processWaiting, key 0 = %+v, want %+v", got, want)
	}

	h.Signal(0, plugin.SignalProcessDone)
	if got, want := h.KeyState(0), named(t, "Done"); got.Color != want.Color || got.Blink != want.Blink {
		t.Errorf("after processDone, key 0 = %+v, want %+v", got, want)
	}
}

func TestIgnoresKeysItDoesNotOwn(t *testing.T) {
	h := hosttest.New(t, 0)
	if err := h.Start(&Plugin{}); err != nil {
		t.Fatal(err)
	}
	h.Signal(1, plugin.SignalProcessWaiting)
	if got := h.KeyState(1); got.Color != 0 {
		t.Errorf("key 1 = %+v, want untouched", got)
	}
}
