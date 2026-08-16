// Command claude-status is task 0013's reference plugin: it stays
// connected to macropadd's plugin WebSocket server and reacts to
// processWaiting and processDone signal broadcasts by calling SetState,
// so a key blinks amber while a Claude Code session waits on input and
// turns solid green once it's done. See driver/README.md's "Signal
// vocabulary" section for the worked Claude Code settings.json hook
// example that drives this with `macrodriver signal`.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/kgheacock/macro-pad/driver/api"
	"github.com/kgheacock/macro-pad/driver/plugin"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("claude-status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", fmt.Sprintf("127.0.0.1:%d", plugin.DefaultPort), "host:port of the running macropadd plugin server")
	key := fs.Int("key", 0, "0-based key index this plugin reflects Claude Code's status onto")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	conn, err := api.Dial(*addr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer conn.Close()

	fmt.Fprintf(stdout, "claude-status: connected to %s, watching key %d\n", *addr, *key)
	return watch(conn, *key, stdout, stderr)
}

// signalConn is the subset of *api.Conn watch needs. A test substitutes a
// fake, so the signal-to-state reaction is checked with no live
// macropadd process or WebSocket connection.
type signalConn interface {
	ReadMessage() (plugin.Message, error)
	SetState(key int, state string, color *uint16) error
}

// watch blocks reading messages from conn until it errors, reacting to
// every processWaiting and processDone signal named for key. Any other
// message — an event, another key's signal, a setKeyState rebroadcast —
// is ignored.
func watch(conn signalConn, key int, stdout, stderr io.Writer) int {
	for {
		msg, err := conn.ReadMessage()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if msg.Kind != plugin.KindSignal || msg.Signal == nil || int(msg.Signal.KeyIndex) != key {
			continue
		}

		var state string
		switch msg.Signal.Name {
		case plugin.SignalProcessWaiting:
			state = "Waiting"
		case plugin.SignalProcessDone:
			state = "Done"
		default:
			continue
		}

		if err := conn.SetState(key, state, nil); err != nil {
			fmt.Fprintln(stderr, err)
			continue
		}
		fmt.Fprintf(stdout, "claude-status: key %d -> %s\n", key, state)
	}
}
