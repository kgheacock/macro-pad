package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// axScript reads the focused element's selected text through Accessibility.
// It prints nothing when the app does not expose it, which Chrome, Electron
// apps, and VS Code often do not.
const axScript = `
tell application "System Events"
	try
		set p to first application process whose frontmost is true
		set el to value of attribute "AXFocusedUIElement" of p
		set t to value of attribute "AXSelectedText" of el
		if t is missing value then return ""
		return t
	on error
		return ""
	end try
end tell`

// copyScript is the fallback, in JavaScript for Automation so it can read
// the pasteboard's change count. It saves every item on the pasteboard,
// posts Cmd+C, waits up to 250 ms for the count to move, reads the text,
// and writes the saved items back. A copy with no selection moves nothing,
// and the pasteboard is left as it was.
const copyScript = `
ObjC.import('AppKit');
function run() {
	const pb = $.NSPasteboard.generalPasteboard;
	const saved = [];
	const items = pb.pasteboardItems;
	for (let i = 0; i < items.count; i++) {
		const item = items.objectAtIndex(i);
		const types = item.types;
		const pairs = [];
		for (let j = 0; j < types.count; j++) {
			const type = types.objectAtIndex(j);
			const data = item.dataForType(type);
			if (!data.isNil()) pairs.push([type, data]);
		}
		saved.push(pairs);
	}
	const before = pb.changeCount;
	Application('System Events').keystroke('c', { using: 'command down' });
	let text = '';
	for (let i = 0; i < 25; i++) {
		delay(0.01);
		if (pb.changeCount !== before) {
			const s = pb.stringForType($.NSPasteboardTypeString);
			text = s.isNil() ? '' : ObjC.unwrap(s);
			break;
		}
	}
	if (pb.changeCount !== before) {
		pb.clearContents;
		const restore = $.NSMutableArray.array;
		for (const pairs of saved) {
			const item = $.NSPasteboardItem.alloc.init;
			for (const [type, data] of pairs) item.setDataForType(data, type);
			restore.addObject(item);
		}
		pb.writeObjects(restore);
	}
	return text;
}`

// captureSelection is task 0053's capture, done with osascript until the
// plugin host carries it: Accessibility first, then Cmd+C with the
// pasteboard restored. Both need the Accessibility grant for the process
// that runs this binary. Off macOS, osascript is missing and it errors.
func captureSelection(ctx context.Context) (string, error) {
	if text, err := osascript(ctx, "AppleScript", axScript); err != nil {
		return "", err
	} else if strings.TrimSpace(text) != "" {
		return text, nil
	}
	return osascript(ctx, "JavaScript", copyScript)
}

// osascript runs script in lang and returns what it prints, less the
// trailing newline osascript adds.
func osascript(ctx context.Context, lang, script string) (string, error) {
	cmd := exec.CommandContext(ctx, "osascript", "-l", lang, "-")
	cmd.Stdin = strings.NewReader(script)
	var errOut strings.Builder
	cmd.Stderr = &errOut
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("osascript: %w: %s", err, strings.TrimSpace(errOut.String()))
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}
