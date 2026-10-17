// Command macro_streamer reads JSON log lines on standard input and shows
// their status on one key of the macro pad, through the macropadd daemon.
// See task 0057 and the "Layers" section of driver/README.md.
//
//	CHANNEL=github macro_streamer < build.log.jsonl
//
// CHANNEL is the id of the key. A line's "level" sets its status. A line
// with a "child" field updates that child of the key instead. A line may
// carry a "count".
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/kgheacock/macro-pad/driver/layers"
	"github.com/kgheacock/macro-pad/driver/plugin"
)

// maxLineBytes bounds one input line. A longer line is a bad line.
const maxLineBytes = 1 << 20

// closeWait bounds how long run waits for the daemon to end the connection.
const closeWait = 2 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdin, os.Stderr))
}

// logLine is the part of a JSON log line this command reads. Every other
// field is ignored.
type logLine struct {
	Level string `json:"level"`
	Child string `json:"child"`
	Count *int   `json:"count"`
}

// statusForLevel maps a log level to a layer status.
func statusForLevel(level string) (string, error) {
	switch strings.ToLower(level) {
	case "error", "fatal", "panic":
		return layers.StatusError, nil
	case "warn", "warning":
		return layers.StatusWarn, nil
	case "done", "success", "ok":
		return layers.StatusDone, nil
	case "info", "debug", "trace", "running":
		return layers.StatusRunning, nil
	case "":
		return "", errors.New("no level")
	}
	return "", fmt.Errorf("unknown level %q", level)
}

// node is the last values sent for one key or child. A setLayerKey message
// carries the full values, so a line that sets only a status resends the
// count the last line set.
type node struct {
	status string
	count  int
}

func run(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stderr io.Writer) int {
	fs := flag.NewFlagSet("macro_streamer", flag.ContinueOnError)
	fs.SetOutput(stderr)
	port := fs.Int("port", plugin.DefaultPort, "TCP port macropadd listens on, on 127.0.0.1")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	channel := getenv("CHANNEL")
	if channel == "" {
		fmt.Fprintln(stderr, "macro_streamer: CHANNEL is empty; set it to the id of the key to update")
		return 2
	}
	initial := strings.ToUpper(getenv("STATE"))
	if !layers.ValidStatus(initial) {
		fmt.Fprintf(stderr, "macro_streamer: STATE %q is not a status; use RUNNING, DONE, ERROR, WARN, or leave it empty\n", getenv("STATE"))
		return 2
	}

	url := fmt.Sprintf("ws://127.0.0.1:%d", *port)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		fmt.Fprintf(stderr, "macro_streamer: connect %s: %v\n", url, err)
		return 1
	}
	defer conn.Close()
	// The daemon sends every client each key change. Read and drop them,
	// or the daemon disconnects a client that falls behind.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	// The channel's icon is assets/<channel>.png. A missing file shows the
	// status color alone.
	iconRef := channel + ".png"
	send := func(id, parent string, n node) error {
		ref := ""
		if parent == "" {
			ref = iconRef
		}
		return conn.WriteJSON(plugin.Message{
			Kind: plugin.KindSetLayerKey,
			SetLayerKey: &plugin.SetLayerKeyPayload{
				ID: id, Parent: parent, Status: n.status, Count: n.count, IconRef: ref,
			},
		})
	}

	key := node{status: initial}
	children := make(map[string]node)
	if err := send(channel, "", key); err != nil {
		fmt.Fprintln(stderr, "macro_streamer: send:", err)
		return 1
	}

	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for lineNo := 1; sc.Scan(); lineNo++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var ln logLine
		if err := json.Unmarshal([]byte(text), &ln); err != nil {
			fmt.Fprintf(stderr, "macro_streamer: line %d skipped: %v\n", lineNo, err)
			continue
		}
		status, err := statusForLevel(ln.Level)
		if err != nil {
			fmt.Fprintf(stderr, "macro_streamer: line %d skipped: %v\n", lineNo, err)
			continue
		}
		if ln.Count != nil && *ln.Count < 0 {
			fmt.Fprintf(stderr, "macro_streamer: line %d skipped: negative count %d\n", lineNo, *ln.Count)
			continue
		}

		id, parent, n := channel, "", key
		if ln.Child != "" {
			id, parent, n = ln.Child, channel, children[ln.Child]
		}
		n.status = status
		if ln.Count != nil {
			n.count = *ln.Count
		}
		if err := send(id, parent, n); err != nil {
			fmt.Fprintln(stderr, "macro_streamer: send:", err)
			return 1
		}
		if parent == "" {
			key = n
		} else {
			children[id] = n
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		fmt.Fprintln(stderr, "macro_streamer: read:", err)
		return 1
	}

	// Closing with unread data in the socket can reset the connection and
	// lose messages the daemon has not read yet. So send a close frame and
	// wait for the daemon's close frame, which follows every message
	// before it.
	conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(closeWait))
	select {
	case <-readDone:
	case <-time.After(closeWait):
	}
	return 0
}
