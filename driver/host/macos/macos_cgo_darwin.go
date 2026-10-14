//go:build darwin && cgo

package macos

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreGraphics -framework CoreFoundation
#include <stdlib.h>
#include <ApplicationServices/ApplicationServices.h>
#include <CoreGraphics/CoreGraphics.h>

static int macos_ax_trusted(void) {
	return AXIsProcessTrusted() ? 1 : 0;
}

static int macos_screen_capture_allowed(void) {
	return CGPreflightScreenCaptureAccess() ? 1 : 0;
}

// macos_selected_text returns the focused element's selected text as a
// malloc'd UTF-8 string, or NULL with *err set to the AXError (or -1 for
// a value that is not a string, -2 for a failed conversion).
static char *macos_selected_text(int *err) {
	AXUIElementRef sys = AXUIElementCreateSystemWide();
	CFTypeRef focused = NULL;
	AXError e = AXUIElementCopyAttributeValue(sys, kAXFocusedUIElementAttribute, &focused);
	CFRelease(sys);
	if (e != kAXErrorSuccess || focused == NULL) {
		*err = (int)e;
		return NULL;
	}
	CFTypeRef val = NULL;
	e = AXUIElementCopyAttributeValue((AXUIElementRef)focused, kAXSelectedTextAttribute, &val);
	CFRelease(focused);
	if (e != kAXErrorSuccess || val == NULL) {
		*err = (int)e;
		return NULL;
	}
	if (CFGetTypeID(val) != CFStringGetTypeID()) {
		CFRelease(val);
		*err = -1;
		return NULL;
	}
	CFIndex len = CFStringGetLength((CFStringRef)val);
	CFIndex size = CFStringGetMaximumSizeForEncoding(len, kCFStringEncodingUTF8) + 1;
	char *buf = malloc(size);
	if (buf == NULL || !CFStringGetCString((CFStringRef)val, buf, size, kCFStringEncodingUTF8)) {
		free(buf);
		CFRelease(val);
		*err = -2;
		return NULL;
	}
	CFRelease(val);
	*err = 0;
	return buf;
}
// macos_front_window_id returns the window number of the frontmost
// normal (layer 0) on-screen window, or 0. CGWindowList orders windows
// front to back, and reading a window's number and layer needs no
// permission.
static unsigned int macos_front_window_id(void) {
	CFArrayRef list = CGWindowListCopyWindowInfo(
		kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
	if (list == NULL) {
		return 0;
	}
	unsigned int id = 0;
	CFIndex n = CFArrayGetCount(list);
	for (CFIndex i = 0; i < n; i++) {
		CFDictionaryRef w = (CFDictionaryRef)CFArrayGetValueAtIndex(list, i);
		CFNumberRef layer = (CFNumberRef)CFDictionaryGetValue(w, kCGWindowLayer);
		int l = -1;
		if (layer == NULL || !CFNumberGetValue(layer, kCFNumberIntType, &l) || l != 0) {
			continue;
		}
		CFNumberRef num = (CFNumberRef)CFDictionaryGetValue(w, kCGWindowNumber);
		int v = 0;
		if (num != NULL && CFNumberGetValue(num, kCFNumberIntType, &v)) {
			id = (unsigned int)v;
			break;
		}
	}
	CFRelease(list);
	return id;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

func permissions() PermissionState {
	return PermissionState{
		Supported:       true,
		Accessibility:   C.macos_ax_trusted() == 1,
		ScreenRecording: C.macos_screen_capture_allowed() == 1,
	}
}

func frontWindowID() WindowID {
	return WindowID(C.macos_front_window_id())
}

// AXError values that mean "nothing selected" rather than a failure.
const (
	axErrorAttributeUnsupported = -25205
	axErrorNoValue              = -25212
)

func selectedText() (string, error) {
	var code C.int
	p := C.macos_selected_text(&code)
	if p == nil {
		switch int(code) {
		case axErrorAttributeUnsupported, axErrorNoValue:
			return "", nil
		}
		return "", fmt.Errorf("macos: selected text: AXError %d", int(code))
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p), nil
}
