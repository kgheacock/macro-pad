// Package claudestatus is task 0013's reference plugin, run by the plugin
// host: each of its keys turns amber and blinks while a Claude Code
// session waits on input, and solid green once it is done. It reacts to
// the processWaiting and processDone signals that `macrodriver signal`
// broadcasts from a Claude Code hook; see driver/README.md's "Signal
// vocabulary" section for the settings.json example.
package claudestatus

import (
	"context"

	"github.com/kgheacock/macro-pad/driver/host"
	"github.com/kgheacock/macro-pad/driver/plugin"
)

// Factory registers the plugin as "claude-status".
var Factory = host.Factory{
	Name:        "claude-status",
	Description: "a key blinks amber while Claude Code waits on you, and turns green when it is done",
	New:         func() host.Plugin { return &Plugin{} },
}

// Plugin is one claude-status instance. It takes no config.
type Plugin struct{}

// states maps each signal the plugin reacts to onto the named state it
// shows.
var states = map[string]string{
	plugin.SignalProcessWaiting: "Waiting",
	plugin.SignalProcessDone:    "Done",
}

func (p *Plugin) Start(ctx context.Context, h host.Host) error {
	for sig, state := range states {
		state := state
		h.OnSignal(sig, func(key int, _ plugin.SignalPayload) {
			if err := h.Key(key).SetState(state); err != nil {
				h.Log("key %d: %v", key, err)
				return
			}
			h.Log("key %d -> %s", key, state)
		})
	}
	return nil
}
