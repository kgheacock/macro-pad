package layers

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// An id tile (task 0094) is one or two lines of identifier on a key in a child
// layer, such as PROJ-123 over #42, with 0, 1, or 2 emoji under them. It is an
// opaque white tile with the key color in a strip across the bottom, as an
// emoji tile is (task 0082), so the status color shows with or without an
// emoji. Both lines are black Go Bold and share one size.
const (
	// MaxIconLines is how many lines one key shows. Each line is at most
	// MaxIconCaptionRunes runes.
	MaxIconLines = 2

	// linesMaxW is the widest a line may be.
	linesMaxW = 120

	// Without an emoji the lines are centred in y linesTop to linesBottomPlain,
	// above the strip. With an emoji they are centred in y linesTop to
	// linesBottomEmoji, and the emoji take y 57 to 105 under them.
	linesTop         = 2
	linesBottomPlain = 104
	linesBottomEmoji = 56

	// linesGap is the space between the two lines, in pixels.
	linesGap = 2

	// idHeroPx is the hero when lines are present, centred at (idHeroX, idHeroY)
	// when it is alone; with a sticker it is idHeroPairPx, centred at
	// (idHeroPairX, idHeroY). The sticker is idStickerPx, centred at
	// (idStickerX, idStickerY), with the outline of stickerOutline.
	idHeroPx     = 46
	idHeroX      = 64
	idHeroPairPx = 44
	idHeroPairX  = 76
	idHeroY      = 82
	idStickerPx  = 28
	idStickerX   = 28
	idStickerY   = 90
)

// The sizes the lines may take, largest first, in pixels.
var (
	linesSizesPlain = []int{28, 24, 22, 20, 18, 16, 14}
	linesSizesEmoji = []int{22, 20, 18, 16, 14}
)

// ValidateIconLines accepts no lines, or one or two lines that are each 1 to
// MaxIconCaptionRunes runes of valid UTF-8 with something to show and no control
// character, the line break included.
func ValidateIconLines(lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	if len(lines) > MaxIconLines {
		return fmt.Errorf("iconLines has %d lines, at most %d", len(lines), MaxIconLines)
	}
	for i, l := range lines {
		if !utf8.ValidString(l) {
			return fmt.Errorf("iconLines[%d] is not valid UTF-8", i)
		}
		n := utf8.RuneCountInString(l)
		if n == 0 {
			return fmt.Errorf("iconLines[%d] is empty", i)
		}
		if n > MaxIconCaptionRunes {
			return fmt.Errorf("iconLines[%d] has %d runes, at most %d", i, n, MaxIconCaptionRunes)
		}
		for _, r := range l {
			if unicode.IsControl(r) {
				return fmt.Errorf("iconLines[%d] has the control character %U", i, r)
			}
		}
		if strings.TrimSpace(l) == "" {
			return errors.New("iconLines has a line with nothing to show")
		}
	}
	return nil
}

// linesPlan is where the lines of an id tile go.
type linesPlan struct {
	size  int // pixels, the same for every line
	texts []string
	tops  []int // y of the top of each line box
	faces font.Face
}

// planLines picks the largest size at which every line fits linesMaxW wide and
// all the lines fit the band, then cuts a line that is still too wide at the
// smallest size. The caller closes set.
func planLines(set *faceSet, lines []string, withEmoji bool) (linesPlan, error) {
	sizes, bottom := linesSizesPlain, linesBottomPlain
	if withEmoji {
		sizes, bottom = linesSizesEmoji, linesBottomEmoji
	}
	texts := make([]string, len(lines))
	for i, l := range lines {
		texts[i] = replaceMissing(set.font, strings.TrimSpace(l))
	}
	band := bottom - linesTop
	pick := len(sizes) - 1
	for i, px := range sizes {
		face, err := set.face(px)
		if err != nil {
			return linesPlan{}, err
		}
		fits := lineHeight(face)*len(texts)+linesGap*(len(texts)-1) <= band
		for _, s := range texts {
			if textWidth(face, s) > linesMaxW {
				fits = false
			}
		}
		if fits {
			pick = i
			break
		}
	}
	face, err := set.face(sizes[pick])
	if err != nil {
		return linesPlan{}, err
	}
	plan := linesPlan{size: sizes[pick], faces: face}
	h := linesGap * (len(texts) - 1)
	for i, s := range texts {
		texts[i] = fitWidth(face, s, linesMaxW)
		h += lineHeight(face)
	}
	plan.texts = texts
	y := linesTop + (band-h)/2
	for range texts {
		plan.tops = append(plan.tops, y)
		y += lineHeight(face) + linesGap
	}
	return plan, nil
}

// drawIconLines fills img with the id tile: the lines in black, the emoji at
// refs, if any, under them, and the strip in keyColor. lines and refs are
// already valid.
func drawIconLines(img *image.NRGBA, assetsDir string, refs, lines []string, keyColor uint16) error {
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)

	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return err
	}
	set := newFaceSet(f)
	defer set.close()
	plan, err := planLines(set, lines, len(refs) > 0)
	if err != nil {
		return err
	}

	var spots []emojiSpot
	switch len(refs) {
	case 1:
		spots = []emojiSpot{{idHeroX, idHeroY, idHeroPx}}
	case 2:
		spots = []emojiSpot{{idHeroPairX, idHeroY, idHeroPairPx}, {idStickerX, idStickerY, idStickerPx}}
	}
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
	// The strip goes last: the outline of the sticker reaches into it.
	strip := image.Rect(0, emojiStripY, transport.CustomGlyphWidth, transport.CustomGlyphHeight)
	draw.Draw(img, strip, image.NewUniform(rgb565ToNRGBA(keyColor)), image.Point{}, draw.Src)

	m := plan.faces.Metrics()
	for i, s := range plan.texts {
		d := font.Drawer{
			Dst:  img,
			Src:  image.NewUniform(textName),
			Face: plan.faces,
			Dot: fixed.Point26_6{
				X: fixed.I(transport.CustomGlyphWidth/2 - textWidth(plan.faces, s)/2),
				Y: fixed.I(plan.tops[i]) + m.Ascent,
			},
		}
		d.DrawString(s)
	}
	return nil
}
