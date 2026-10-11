// Command setkeyrepro measures how many setKeyState messages macropadd
// applies. It sends one message on each of its own WebSocket connections,
// a fixed time apart, and after each one checks keys.json for the color it
// sent. It prints one line for each message and `applied N/M` at the end.
// See tasks/ongoing/0050-daemon-drops-setkeystate-messages.md.
//
// Run macropadd against the real board first, and keep its output: its
// setKeyState lines show where each message waited.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/websocket"

	"github.com/kgheacock/macro-pad/driver/plugin"
)

// storedKeys is the part of keys.json this command reads.
type storedKeys struct {
	Keys []struct {
		KeyIndex byte   `json:"key_index"`
		Color    uint16 `json:"color"`
	} `json:"keys"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// pollGap is how often run reads keys.json while it waits for a message to
// show.
const pollGap = 100 * time.Millisecond

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("setkeyrepro", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", fmt.Sprintf("ws://127.0.0.1:%d", plugin.DefaultPort), "WebSocket URL of macropadd")
	stateDir := fs.String("state-dir", defaultStateDir(), "macropadd's state directory, where keys.json is")
	key := fs.Uint("key", 4, "key index to send to")
	count := fs.Int("count", 50, "number of messages to send")
	interval := fs.Duration("interval", 5*time.Second, "time between messages; also how long each one has to show in keys.json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *stateDir == "" {
		fmt.Fprintln(stderr, "setkeyrepro: no state directory; pass -state-dir")
		return 2
	}
	statePath := filepath.Join(*stateDir, "keys.json")

	applied := 0
	for i := 0; i < *count; i++ {
		// Each message has its own color, so a message that shows in
		// keys.json is that message and not an older one.
		color := uint16(0x0100 + i)
		start := time.Now()
		if err := send(*url, plugin.SetKeyStatePayload{KeyIndex: byte(*key), Color: color, Blink: false}); err != nil {
			fmt.Fprintf(stdout, "%2d/%d color=%#04x send failed: %v\n", i+1, *count, color, err)
		} else if after, ok := waitForColor(statePath, byte(*key), color, *interval); ok {
			applied++
			fmt.Fprintf(stdout, "%2d/%d color=%#04x applied after %s\n", i+1, *count, color, after.Round(time.Millisecond))
		} else {
			fmt.Fprintf(stdout, "%2d/%d color=%#04x NOT applied within %s\n", i+1, *count, color, *interval)
		}
		if i < *count-1 {
			time.Sleep(time.Until(start.Add(*interval)))
		}
	}
	fmt.Fprintf(stdout, "applied %d/%d\n", applied, *count)
	return 0
}

// send opens one connection, writes one setKeyState, and closes it.
func send(url string, p plugin.SetKeyStatePayload) error {
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.WriteJSON(plugin.Message{Kind: plugin.KindSetKeyState, SetKeyState: &p})
}

// waitForColor polls statePath until key shows color, or timeout passes. It
// reports how long it waited.
func waitForColor(statePath string, key byte, color uint16, timeout time.Duration) (time.Duration, bool) {
	start := time.Now()
	for {
		if shows(statePath, key, color) {
			return time.Since(start), true
		}
		if time.Since(start) >= timeout {
			return 0, false
		}
		time.Sleep(pollGap)
	}
}

func shows(statePath string, key byte, color uint16) bool {
	data, err := os.ReadFile(statePath)
	if err != nil {
		return false
	}
	var stored storedKeys
	if json.Unmarshal(data, &stored) != nil {
		return false
	}
	for _, k := range stored.Keys {
		if k.KeyIndex == key && k.Color == color {
			return true
		}
	}
	return false
}

// defaultStateDir is the directory macropadd uses unless told otherwise.
func defaultStateDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "macro-pad")
}
