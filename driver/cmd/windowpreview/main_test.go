package main

import (
	"bytes"
	"context"
	"image"
	"strings"
	"testing"

	"github.com/kgheacock/macro-pad/driver/preview"
)

type fakeCapturer struct{ windows []preview.Window }

func (f fakeCapturer) Windows() ([]preview.Window, error) { return f.windows, nil }
func (f fakeCapturer) Focused() (preview.WindowID, error) { return 0, nil }
func (f fakeCapturer) Capture(id preview.WindowID, w, h int) (image.Image, error) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	return img, nil
}

type fakeSink struct{ keys []int }

func (s *fakeSink) SetCustomGlyph(key int, _ []byte) error {
	s.keys = append(s.keys, key)
	return nil
}

func withFakes(t *testing.T, c preview.Capturer) *fakeSink {
	t.Helper()
	sink := &fakeSink{}
	oldCap, oldDial := newCapturer, dial
	newCapturer = func() (preview.Capturer, error) { return c, nil }
	dial = func(string) (preview.Sink, func() error, error) { return sink, func() error { return nil }, nil }
	t.Cleanup(func() { newCapturer, dial = oldCap, oldDial })
	return sink
}

func TestList(t *testing.T) {
	withFakes(t, fakeCapturer{windows: []preview.Window{{ID: 42, App: "Safari", Title: "Docs", OnScreen: true}}})
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"--list"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "#42") || !strings.Contains(out.String(), "Safari") {
		t.Errorf("got %q", out.String())
	}
}

func TestMapPushesKey(t *testing.T) {
	sink := withFakes(t, fakeCapturer{windows: []preview.Window{{ID: 42, App: "Safari", OnScreen: true}}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Run refreshes once, then sees ctx done.
	var out, errOut bytes.Buffer
	if code := run(ctx, []string{"--map", "3=Safari"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if len(sink.keys) != 1 || sink.keys[0] != 3 {
		t.Errorf("pushed keys %v, want [3]", sink.keys)
	}
}

func TestNoMapIsUsageError(t *testing.T) {
	withFakes(t, fakeCapturer{})
	var out, errOut bytes.Buffer
	if code := run(context.Background(), nil, &out, &errOut); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

func TestBadMap(t *testing.T) {
	withFakes(t, fakeCapturer{})
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"--map", "Safari"}, &out, &errOut); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}
