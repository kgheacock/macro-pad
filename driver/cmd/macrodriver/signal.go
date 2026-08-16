package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/kgheacock/macro-pad/driver/api"
	"github.com/kgheacock/macro-pad/driver/plugin"
)

// signalNameForEvent translates a hook's event name into the signal
// vocabulary driver/plugin/protocol.go defines. See task 0013's Goals:
// a Claude Code Notification hook broadcasts processWaiting, and a Stop
// hook broadcasts processDone. Codex CLI's own notify mechanism is a
// non-goal — see the task spec.
func signalNameForEvent(event string) (string, bool) {
	switch event {
	case "notification":
		return plugin.SignalProcessWaiting, true
	case "stop":
		return plugin.SignalProcessDone, true
	default:
		return "", false
	}
}

// runSignal implements `macrodriver signal`: it opens a short-lived
// plugin connection, sends one signal message, and exits. Reaching a
// daemon that is not running, or one with no plugin currently listening
// for the broadcast, is a documented v1 limitation — see task 0013's
// Risks — not an error this command treats specially beyond reporting
// the dial failure.
func runSignal(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("signal", flag.ContinueOnError)
	fs.SetOutput(stderr)
	key := fs.Int("key", 0, "0-based key index the signal concerns")
	event := fs.String("event", "", "hook event name: notification or stop")
	addr := fs.String("addr", fmt.Sprintf("127.0.0.1:%d", plugin.DefaultPort), "host:port of the running macropadd plugin server")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	name, ok := signalNameForEvent(*event)
	if !ok {
		fmt.Fprintf(stderr, "macrodriver signal: unrecognized --event %q (want notification or stop)\n", *event)
		return 2
	}

	conn, err := api.Dial(*addr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer conn.Close()

	if err := conn.Signal(*key, name); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
