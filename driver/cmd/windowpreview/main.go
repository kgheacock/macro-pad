// Command windowpreview shows a live thumbnail of a window on each mapped
// key. It captures each window with ScreenCaptureKit every few seconds,
// scales it to 128x128, and sends it through macropadd's setCustomGlyph
// message. A minimized window, or one on another desktop, keeps the last
// snapshot taken of it, dimmed with an amber strip. See task 0054 and
// driver/README.md's "Window previews" section.
//
// It needs macOS 14 and the Screen Recording permission.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/kgheacock/macro-pad/driver/api"
	"github.com/kgheacock/macro-pad/driver/plugin"
	"github.com/kgheacock/macro-pad/driver/preview"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// Seams for tests.
var (
	newCapturer = preview.NewCapturer
	dial        = func(addr string) (preview.Sink, func() error, error) {
		c, err := api.Dial(addr)
		if err != nil {
			return nil, nil, err
		}
		return c, c.Close, nil
	}
)

type targetList []preview.Target

func (l *targetList) String() string { return fmt.Sprint(*l) }

func (l *targetList) Set(s string) error {
	t, err := preview.ParseTarget(s)
	if err != nil {
		return err
	}
	*l = append(*l, t)
	return nil
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("windowpreview", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", fmt.Sprintf("127.0.0.1:%d", plugin.DefaultPort), "host:port of the running macropadd plugin server")
	var targets targetList
	fs.Var(&targets, "map", "KEY=SELECTOR, repeatable. SELECTOR is App, App:Title (Title is a substring), or #WindowID")
	list := fs.Bool("list", false, "print the window list and exit")
	opt := preview.Options{}
	fs.DurationVar(&opt.Interval, "interval", 0, "how often to capture every mapped window (default 5s)")
	fs.DurationVar(&opt.FocusPoll, "focus-poll", 0, "how often to check which window has focus (default 500ms)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	capt, err := newCapturer()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	if *list {
		windows, err := capt.Windows()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		for _, w := range windows {
			state := "hidden"
			if w.OnScreen {
				state = "on screen"
			}
			fmt.Fprintf(stdout, "#%-6d %-24s %-9s %s\n", w.ID, w.App, state, w.Title)
		}
		return 0
	}

	if len(targets) == 0 {
		fmt.Fprintln(stderr, "windowpreview: give at least one --map KEY=SELECTOR (see --list)")
		return 2
	}

	sink, closeFn, err := dial(*addr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeFn()

	opt.Logf = func(format string, args ...any) { fmt.Fprintf(stderr, format+"\n", args...) }
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = fmt.Sprintf("%d=%+v", t.Key, t.Selector)
	}
	fmt.Fprintf(stdout, "windowpreview: connected to %s, showing %s\n", *addr, strings.Join(names, ", "))
	preview.New(capt, sink, targets, opt).Run(ctx)
	return 0
}
