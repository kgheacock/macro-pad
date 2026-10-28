package layers

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
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

// An emoji icon (task 0073) is one or two pictures from assets/emoji/ with an
// optional caption of one line. Task 0082 draws it on an opaque white tile: the
// caption across the top, the first emoji as a big hero, the second as a
// smaller sticker over the hero's bottom left, and the key color as a strip
// across the bottom. A top-level key keeps task 0073's layout, the pictures
// side by side on the white round with the caption above them, which reads
// better beside the logos of the other top keys. The inbox sends a
// repository's emoji and a session's emoji, with the folder name as the
// caption.
const (
	// MaxIconEmoji is how many emoji one key shows.
	MaxIconEmoji = 2
	// MaxIconCaptionRunes bounds iconText when it is the caption of emoji.
	MaxIconCaptionRunes = 16
	// IconEmojiDir is the folder under assets/ that every iconEmoji ref is in.
	IconEmojiDir = "emoji/"

	// The strip of the key color covers y emojiStripY to the bottom.
	emojiStripY = 106

	// The caption is centred at y captionY, at most captionMaxW pixels wide.
	captionY    = 14
	captionMaxW = 120

	// On the round (task 0073), two emoji are roundPairPx wide, centred at x
	// roundPairX0 and roundPairX1; one emoji is roundOnePx wide, centred at x
	// 64. They are centred at y roundYCaption under a caption, and at
	// roundYAlone without one. The caption is centred at y roundCaptionY, at
	// most roundCaptionMaxW pixels wide: the round is narrow at the top.
	roundPairPx      = 48
	roundPairX0      = 38
	roundPairX1      = 90
	roundOnePx       = 64
	roundYCaption    = 74
	roundYAlone      = 64
	roundCaptionY    = 30
	roundCaptionMaxW = 84

	// stickerOutline is the width of the white outline around the sticker.
	stickerOutline = 3

	// emojiArtAlpha is the alpha above which a pixel is part of a picture's
	// art, when the picture is cropped to it.
	emojiArtAlpha = 0x08
)

// emojiSpot is where one picture goes: the longer side of its art is size
// pixels, centred at (x, y).
type emojiSpot struct{ x, y, size int }

// emojiLayout is where the pictures go, for a caption or none, and one emoji
// or two. With two, the first spot is the hero and the second the sticker.
var emojiLayout = map[bool][][]emojiSpot{
	true: {
		{{56, 66, 80}},
		{{62, 64, 80}, {24, 82, 40}},
	},
	false: {
		{{60, 56, 100}},
		{{70, 50, 96}, {28, 78, 48}},
	},
}

// captionSizes and roundCaptionSizes are the sizes a caption may take, largest
// first, in pixels, on the tile and on the round.
var (
	captionSizes      = []int{22, 20, 18, 16, 14}
	roundCaptionSizes = []int{20, 18, 16, 14, 12}
)

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

// drawIconEmoji fills img with the tile, the strip in keyColor, the emoji at
// refs, and caption above them. refs and caption are already valid.
func drawIconEmoji(img *image.NRGBA, assetsDir string, refs []string, caption string, keyColor uint16) error {
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	strip := image.Rect(0, emojiStripY, transport.CustomGlyphWidth, transport.CustomGlyphHeight)
	draw.Draw(img, strip, image.NewUniform(rgb565ToNRGBA(keyColor)), image.Point{}, draw.Src)
	spots := emojiLayout[caption != ""][len(refs)-1]
	for i, ref := range refs {
		icon, err := loadIcon(assetsDir, ref)
		if err != nil {
			return err
		}
		art, at := scaleArt(icon, spots[i])
		if i > 0 {
			drawOutline(img, art, at, stickerOutline)
		}
		draw.Draw(img, art.Bounds().Add(at), art, image.Point{}, draw.Over)
	}
	if caption != "" {
		return drawCaption(img, caption, captionY, captionMaxW, captionSizes)
	}
	return nil
}

// drawIconEmojiRound draws a top-level key's emoji icon as task 0073 did: the
// round over keyColor, the emoji at refs side by side, and caption above them.
// refs and caption are already valid.
func drawIconEmojiRound(img *image.NRGBA, assetsDir string, refs []string, caption string, keyColor uint16) error {
	drawIconRound(img, keyColor)
	y := roundYAlone
	if caption != "" {
		y = roundYCaption
	}
	size, xs := roundOnePx, []int{64}
	if len(refs) == 2 {
		size, xs = roundPairPx, []int{roundPairX0, roundPairX1}
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
		return drawCaption(img, caption, roundCaptionY, roundCaptionMaxW, roundCaptionSizes)
	}
	return nil
}

// artBounds is the box of the pixels of icon with alpha above emojiArtAlpha,
// or the whole icon when it has none.
func artBounds(icon image.Image) image.Rectangle {
	var box image.Rectangle
	b := icon.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := icon.At(x, y).RGBA(); a>>8 > emojiArtAlpha {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	if box.Empty() {
		return b
	}
	return box
}

// scaleArt crops icon to its art and scales it so that the longer side is
// spot.size. It returns the scaled art and where its top left goes.
func scaleArt(icon image.Image, spot emojiSpot) (*image.RGBA, image.Point) {
	box := artBounds(icon)
	w, h := spot.size, spot.size
	if box.Dx() > box.Dy() {
		h = max(1, spot.size*box.Dy()/box.Dx())
	} else {
		w = max(1, spot.size*box.Dx()/box.Dy())
	}
	// Scale into a premultiplied image, so the edges do not darken.
	art := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(art, art.Bounds(), icon, box, xdraw.Src, nil)
	return art, image.Pt(spot.x-w/2, spot.y-h/2)
}

// drawOutline paints white on img where art, placed at at, grown by r pixels,
// covers it. The edge of the outline is blended with what is under it.
func drawOutline(img *image.NRGBA, art *image.RGBA, at image.Point, r int) {
	ab := art.Bounds()
	area := ab.Add(at).Inset(-r).Intersect(img.Bounds())
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			cover := 0.0
			for dy := -r; dy <= r && cover < 1; dy++ {
				for dx := -r; dx <= r; dx++ {
					d := math.Hypot(float64(dx), float64(dy))
					if d > float64(r)+0.5 {
						continue
					}
					p := image.Pt(x+dx, y+dy).Sub(at)
					if !p.In(ab) {
						continue
					}
					a := float64(art.RGBAAt(p.X, p.Y).A) / 0xFF
					if d > float64(r)-0.5 {
						a *= float64(r) + 0.5 - d
					}
					cover = math.Max(cover, a)
				}
			}
			if cover == 0 {
				continue
			}
			under := img.NRGBAAt(x, y)
			mix := func(v uint8) uint8 { return uint8(math.Round(0xFF*cover + float64(v)*(1-cover))) }
			img.SetNRGBA(x, y, color.NRGBA{R: mix(under.R), G: mix(under.G), B: mix(under.B), A: 0xFF})
		}
	}
}

// loadIcon reads the 128x128 PNG at ref under assetsDir. An emoji ref draws
// from the emoji font instead, when there is one with its picture (task 0074).
func loadIcon(assetsDir, ref string) (image.Image, error) {
	if err := ValidateIconRef(ref); err != nil {
		return nil, err
	}
	if icon := fontIcon(ref); icon != nil {
		return icon, nil
	}
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

// drawCaption draws one line of black text, centred at y, in the largest of
// sizes that fits maxW, cut with "…" at the smallest.
func drawCaption(img *image.NRGBA, text string, y, maxW int, sizes []int) error {
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return err
	}
	text = replaceMissing(f, strings.TrimSpace(text))
	var face font.Face
	for i, px := range sizes {
		if face != nil {
			face.Close()
		}
		face, err = opentype.NewFace(f, &opentype.FaceOptions{Size: float64(px), DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return err
		}
		if textWidth(face, text) <= maxW || i == len(sizes)-1 {
			break
		}
	}
	defer face.Close()
	text = fitWidth(face, text, maxW)
	m := face.Metrics()
	top := y - lineHeight(face)/2
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
