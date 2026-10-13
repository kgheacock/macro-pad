package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// desktopKeyCodes maps a desktop number to the macOS virtual key code of
// the digit key that, with Control held, switches to it. The digit row's
// key codes are not in order (kVK_ANSI_1 through kVK_ANSI_9 in
// Carbon's Events.h).
var desktopKeyCodes = [maxDesktop + 1]int{0, 18, 19, 20, 21, 23, 22, 26, 28, 25}

// switchScript is one osascript run: AppleScript source lines, each
// passed with -e, and the arguments its run handler receives. App and
// window names travel as arguments, never spliced into the source, so a
// title with a quote in it cannot break or change the script.
type switchScript struct {
	lines []string
	args  []string
}

// scriptFor builds the AppleScript that brings b's target forward.
func scriptFor(b Binding) switchScript {
	switch {
	case b.Desktop != 0:
		// Needs the "Switch to Desktop N" shortcut turned on in System
		// Settings > Keyboard > Keyboard Shortcuts > Mission Control.
		return switchScript{lines: []string{
			`tell application "System Events" to key code ` + strconv.Itoa(desktopKeyCodes[b.Desktop]) + ` using control down`,
		}}
	case b.Window == "":
		// activate also switches to the Space the app's window is on.
		return switchScript{
			lines: []string{
				`on run argv`,
				`tell application (item 1 of argv) to activate`,
				`end run`,
			},
			args: []string{b.App},
		}
	default:
		// AXRaise brings the matching window above the app's others;
		// activating first makes the app frontmost and moves to its
		// Space. Needs Accessibility permission.
		return switchScript{
			lines: []string{
				`on run argv`,
				`set appName to item 1 of argv`,
				`set titlePart to item 2 of argv`,
				`tell application appName to activate`,
				`tell application "System Events"`,
				`set procs to (every process whose name is appName)`,
				`if procs is {} then set procs to (every process whose displayed name is appName)`,
				`if procs is {} then error "no running process named " & appName`,
				`tell item 1 of procs`,
				`set matches to (every window whose name contains titlePart)`,
				`if matches is {} then error "no " & appName & " window title contains " & quoted form of titlePart`,
				`perform action "AXRaise" of item 1 of matches`,
				`end tell`,
				`end tell`,
				`end run`,
			},
			args: []string{b.App, b.Window},
		}
	}
}

// argv is the osascript command line for s.
func (s switchScript) argv() []string {
	argv := []string{"osascript"}
	for _, l := range s.lines {
		argv = append(argv, "-e", l)
	}
	return append(argv, s.args...)
}

// runner runs one switch. A test substitutes a recorder, so the plugin's
// dispatch is checked on any OS with no osascript.
type runner interface {
	Run(s switchScript) error
}

type osascriptRunner struct{}

func (osascriptRunner) Run(s switchScript) error {
	argv := s.argv()
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("osascript: %w", err)
		}
		return fmt.Errorf("osascript: %w: %s", err, msg)
	}
	return nil
}
