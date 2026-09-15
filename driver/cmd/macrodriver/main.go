// Command macrodriver is the CLI companion to macropadd: it reaches a
// running daemon's plugin WebSocket server to trigger a one-shot action,
// rather than holding a plugin connection open itself. Its first
// subcommand, signal, lets a Claude Code hook broadcast a signal — see
// driver/README.md's "Signal vocabulary" and task 0013. Its emoji
// subcommand sends a Unicode emoji character to a key — see task 0034.
// Its html subcommand renders a static HTML file to a key — see task
// 0037.
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "macrodriver: expected a subcommand (signal, emoji, html)")
		return 2
	}
	switch args[0] {
	case "signal":
		return runSignal(args[1:], stdout, stderr)
	case "emoji":
		return runEmoji(args[1:], stdout, stderr)
	case "html":
		return runHtml(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "macrodriver: unknown subcommand %q\n", args[0])
		return 2
	}
}
