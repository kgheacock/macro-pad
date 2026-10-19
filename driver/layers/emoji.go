package layers

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"strings"
	"unicode/utf8"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// An emoji icon (task 0073) is one or two pictures from assets/emoji/ side by
// side on the white round, with an optional caption of one line above them.
// The inbox sends a folder's emoji and a session's emoji, with the folder name
// as the caption.
const (
	// MaxIconEmoji is how many emoji one key shows.
	MaxIconEmoji = 2
	// MaxIconCaptionRunes bounds iconText when it is the caption of emoji.
	MaxIconCaptionRunes = 16
	// IconEmojiDir is the folder under assets/ that every iconEmoji ref is in.
	IconEmojiDir = "emoji/"

	// Two emoji are emojiPairPx wide, centred at x emojiPairX0 and
	// emojiPairX1; one emoji is emojiOnePx wide, centred at x 64.
	emojiPairPx = 48
	emojiPairX0 = 38
	emojiPairX1 = 90
	emojiOnePx  = 64
	// The emoji are centred at y emojiYCaption under a caption, and at
	// emojiYAlone without one.
	emojiYCaption = 74
	emojiYAlone   = 64

	// The caption is centred at y captionY, at most captionMaxW pixels wide:
	// the round is narrow at the top.
	captionY    = 30
	captionMaxW = 84
)

// captionSizes are the sizes a caption may take, largest first, in pixels.
var captionSizes = []int{20, 18, 16, 14, 12}

// ValidateIconEmoji accepts no refs, or up to MaxIconEmoji refs that are each a
// valid iconRef under IconEmojiDir.
func ValidateIconEmoji(refs []string) error {
	if len(refs) > MaxIconEmoji {
		return fmt.Errorf("iconEmoji has %d refs, at most %d", len(refs), MaxIconEmoji)
	}
	for _, ref := range refs {
		if err := ValidateIconRef(ref); err != nil {
			return err
		}
		if !strings.HasPrefix(ref, IconEmojiDir) || ref == IconEmojiDir || strings.Contains(ref, "\n") {
			return fmt.Errorf("iconEmoji %q is not a file under assets/%s", ref, IconEmojiDir)
		}
	}
	return nil
}

// validateIconCaption checks iconText when it goes over emoji: one line of at
// most MaxIconCaptionRunes runes.
func validateIconCaption(text string) error {
	if text == "" {
		return nil
	}
	if strings.Contains(text, "\n") {
		return errors.New("iconText over iconEmoji is one line")
	}
	if n := utf8.RuneCountInString(text); n > MaxIconCaptionRunes {
		return fmt.Errorf("iconText over iconEmoji has %d runes, at most %d", n, MaxIconCaptionRunes)
	}
	return nil
}

// joinEmoji and splitEmoji turn refs into one comparable string and back, for
// view and frameKey. A ref never holds a line break (ValidateIconEmoji).
func joinEmoji(refs []string) string { return strings.Join(refs, "\n") }

func splitEmoji(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// drawIconEmoji draws the round over keyColor, the emoji at refs, and caption
// above them. refs and caption are already valid.
func drawIconEmoji(img *image.NRGBA, assetsDir string, refs []string, caption string, keyColor uint16) error {
	drawIconRound(img, keyColor)
	y := emojiYAlone
	if caption != "" {
		y = emojiYCaption
	}
	size, xs := emojiOnePx, []int{64}
	if len(refs) == 2 {
		size, xs = emojiPairPx, []int{emojiPairX0, emojiPairX1}
	}
	for i, ref := range refs {
		icon, err := loadIcon(assetsDir, ref)
		if err != nil {
			return err
		}
		// Scale into a premultiplied image, so the edges do not darken.
		small := image.NewRGBA(image.Rect(0, 0, size, size))
		xdraw.CatmullRom.Scale(small, small.Bounds(), icon, icon.Bounds(), xdraw.Src, nil)
		at := image.Pt(xs[i]-size/2, y-size/2)
		draw.Draw(img, small.Bounds().Add(at), small, image.Point{}, draw.Over)
	}
	if caption != "" {
		return drawCaption(img, caption)
	}
	return nil
}

// loadIcon reads the 128x128 PNG at ref under assetsDir.
func loadIcon(assetsDir, ref string) (image.Image, error) {
	p, err := assetPath(assetsDir, ref)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	icon, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	b := icon.Bounds()
	if b.Dx() != transport.CustomGlyphWidth || b.Dy() != transport.CustomGlyphHeight {
		return nil, fmt.Errorf("%s: %w: got %dx%d", ref, transport.ErrInvalidGlyphSize, b.Dx(), b.Dy())
	}
	return icon, nil
}

// drawCaption draws one line of black text, centred at y captionY, in the
// largest of captionSizes that fits captionMaxW, cut with "…" at the smallest.
func drawCaption(img *image.NRGBA, text string) error {
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return err
	}
	text = replaceMissing(f, strings.TrimSpace(text))
	var face font.Face
	for i, px := range captionSizes {
		if face != nil {
			face.Close()
		}
		face, err = opentype.NewFace(f, &opentype.FaceOptions{Size: float64(px), DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return err
		}
		if textWidth(face, text) <= captionMaxW || i == len(captionSizes)-1 {
			break
		}
	}
	defer face.Close()
	text = fitWidth(face, text, captionMaxW)
	m := face.Metrics()
	top := captionY - lineHeight(face)/2
	d := font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(textName),
		Face: face,
		Dot: fixed.Point26_6{
			X: fixed.I(transport.CustomGlyphWidth/2 - textWidth(face, text)/2),
			Y: fixed.I(top) + m.Ascent,
		},
	}
	d.DrawString(text)
	return nil
}
