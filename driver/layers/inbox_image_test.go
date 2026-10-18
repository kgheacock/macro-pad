package layers

import (
	"os"
	"testing"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// inboxFixtures holds the channel picture that inbox/src/png.js draws. Its
// own test compares the pixels; this one checks that the board's decoder and
// the layer renderer accept it (task 0066).
const inboxFixtures = "../../inbox/test/fixtures"

func TestInboxChannelImageDecodes(t *testing.T) {
	data, err := os.ReadFile(inboxFixtures + "/channel-macro-pad.driver.png")
	if err != nil {
		t.Fatal(err)
	}
	pixels, err := transport.DecodePNGToRGB565(data)
	if err != nil {
		t.Fatalf("DecodePNGToRGB565: %v", err)
	}
	if want := transport.CustomGlyphWidth * transport.CustomGlyphHeight * 2; len(pixels) != want {
		t.Errorf("%d bytes of pixels, want %d", len(pixels), want)
	}

	// The layer draws it on a key, with a count pip.
	if _, err := renderGlyph(inboxFixtures, "channel-macro-pad.driver.png", 3, StatusColor(StatusWarn)); err != nil {
		t.Errorf("renderGlyph: %v", err)
	}
}
