//go:build darwin

package preview

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework CoreGraphics -weak_framework ScreenCaptureKit
#include <stdlib.h>
#include "capture_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"image"
	"unsafe"
)

type darwinCapturer struct{}

// NewCapturer returns the ScreenCaptureKit capturer. It needs macOS 14 and
// the Screen Recording permission; without the permission it asks macOS
// to show its prompt once and returns ErrPermission.
func NewCapturer() (Capturer, error) {
	if C.pv_available() == 0 {
		return nil, ErrUnsupported
	}
	if C.pv_preflight() == 0 {
		C.pv_request()
		return nil, ErrPermission
	}
	return darwinCapturer{}, nil
}

func (darwinCapturer) Windows() ([]Window, error) {
	var list *C.pv_window
	var n C.int
	if C.pv_windows(&list, &n) != 0 {
		return nil, errors.New("preview: CGWindowListCopyWindowInfo failed")
	}
	defer C.pv_free_windows(list, n)
	cs := unsafe.Slice(list, int(n))
	out := make([]Window, len(cs))
	for i, w := range cs {
		out[i] = Window{
			ID:       WindowID(w.id),
			App:      C.GoString(w.app),
			Title:    C.GoString(w.title),
			OnScreen: w.on_screen != 0,
		}
	}
	return out, nil
}

func (darwinCapturer) Focused() (WindowID, error) {
	return WindowID(C.pv_focused()), nil
}

func (darwinCapturer) Capture(id WindowID, maxW, maxH int) (image.Image, error) {
	var pix *C.uint8_t
	var w, h C.int
	var cerr *C.char
	if C.pv_capture(C.uint32_t(id), C.int(maxW), C.int(maxH), &pix, &w, &h, &cerr) != 0 {
		msg := C.GoString(cerr)
		C.free(unsafe.Pointer(cerr))
		return nil, fmt.Errorf("preview: %s", msg)
	}
	defer C.free(unsafe.Pointer(pix))
	img := image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
	copy(img.Pix, unsafe.Slice((*byte)(unsafe.Pointer(pix)), int(w)*int(h)*4))
	return img, nil
}
