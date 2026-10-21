package layers

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/f64"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// Task 0074: an emoji ref draws from the host's color emoji font, Apple Color
// Emoji on a Mac, at run time. Apple's pictures may not be copied into the
// repo, so nothing is written to disk: the font holds each emoji as PNG strikes
// (the sbix table), and the driver fits the largest one into the round as
// tools/emoji/build_emoji_assets.mjs fits a Fluent picture. A ref the font has
// no picture for, or a host with no font, draws the committed Fluent file.

// AppleColorEmoji is where macOS keeps its color emoji font.
const AppleColorEmoji = "/System/Library/Fonts/Apple Color Emoji.ttc"

// An emoji picture is scaled so that its farthest pixel of alpha emojiSolid or
// more lies emojiRadius px from the centre, as in tools/emoji.
const (
	emojiRadius = 50
	emojiSolid  = 0x80
)

var emojiFont struct {
	mu   sync.Mutex
	path string
	font *colorFont
	err  error
	// icons holds each ref drawn from the font, or nil when the font has none.
	icons map[string]image.Image
}

// UseEmojiFont makes an emoji ref draw from the color emoji font at path, and
// from assets/emoji/ when the font has no picture for it. An empty path, or a
// font that cannot be read, draws only from assets/emoji/. Tests leave it
// unset, so their goldens stay Fluent.
func UseEmojiFont(path string) {
	emojiFont.mu.Lock()
	defer emojiFont.mu.Unlock()
	if emojiFont.font != nil {
		emojiFont.font.f.Close()
	}
	emojiFont.path, emojiFont.font, emojiFont.err, emojiFont.icons = path, nil, nil, nil
	if path == "" {
		return
	}
	emojiFont.font, emojiFont.err = openColorFont(path)
	emojiFont.icons = map[string]image.Image{}
}

// EmojiFontErr is why the emoji font set by UseEmojiFont cannot be read, or
// nil.
func EmojiFontErr() error {
	emojiFont.mu.Lock()
	defer emojiFont.mu.Unlock()
	return emojiFont.err
}

// fontIcon returns the 128x128 picture of an emoji ref from the emoji font, or
// nil when there is no font or it has no picture for ref.
func fontIcon(ref string) image.Image {
	if !strings.HasPrefix(ref, IconEmojiDir) {
		return nil
	}
	emojiFont.mu.Lock()
	defer emojiFont.mu.Unlock()
	if emojiFont.font == nil {
		return nil
	}
	if icon, ok := emojiFont.icons[ref]; ok {
		return icon
	}
	var icon image.Image
	if r, ok := refRune(ref); ok {
		if strike, err := emojiFont.font.picture(r); err == nil {
			icon = fitEmoji(strike)
		}
	}
	emojiFont.icons[ref] = icon
	return icon
}

// refRune is the code point an emoji ref names: emoji/1f98a.png, or
// emoji/2693-fe0f.png with the variation selector, which the font's cmap does
// not need. A sequence of more than one emoji is not drawn from the font.
func refRune(ref string) (rune, bool) {
	name := strings.TrimSuffix(path.Base(ref), ".png")
	parts := strings.Split(name, "-")
	if len(parts) == 2 && parts[1] == "fe0f" {
		parts = parts[:1]
	}
	if len(parts) != 1 {
		return 0, false
	}
	n, err := strconv.ParseUint(parts[0], 16, 32)
	if err != nil || n > 0x10FFFF {
		return 0, false
	}
	return rune(n), true
}

// fitEmoji scales and centres src on a 128x128 canvas so that its farthest
// solid pixel lies emojiRadius px from the centre.
func fitEmoji(src image.Image) image.Image {
	b := src.Bounds()
	solid := func(x, y int) bool {
		_, _, _, a := src.At(x, y).RGBA()
		return a >= emojiSolid<<8
	}
	x0, y0, x1, y1 := b.Max.X, b.Max.Y, b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if solid(x, y) {
				x0, y0 = min(x0, x), min(y0, y)
				x1, y1 = max(x1, x), max(y1, y)
			}
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, transport.CustomGlyphWidth, transport.CustomGlyphHeight))
	if x1 < x0 {
		return dst
	}
	cx, cy := float64(x0+x1+1)/2, float64(y0+y1+1)/2
	far := 0.0
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if solid(x, y) {
				far = math.Max(far, math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy))
			}
		}
	}
	s := emojiRadius / math.Max(far, 1)
	c := float64(transport.CustomGlyphWidth) / 2
	// Scale into a premultiplied image, so the edges do not darken.
	m := f64.Aff3{s, 0, c - s*cx, 0, s, c - s*cy}
	xdraw.CatmullRom.Transform(dst, m, src, b, xdraw.Src, nil)
	return dst
}

// colorFont reads emoji pictures from the sbix table of a TrueType font or
// the first font of a collection, without loading the file: Apple Color Emoji
// is about 180 MB.
type colorFont struct {
	f *os.File
	// groups is the cmap: code point ranges, each from its first glyph.
	groups []cmapGroup
	// strikes are the offsets of the sbix strikes in the file, largest first.
	strikes   []int64
	numGlyphs int
}

type cmapGroup struct {
	start, end, glyph uint32
}

func openColorFont(name string) (*colorFont, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	cf, err := readColorFont(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return cf, nil
}

func readColorFont(f *os.File) (*colorFont, error) {
	r := &fontReader{f: f}
	base := int64(0)
	if r.tag(0) == "ttcf" {
		// A collection: the first font is as good as any for emoji.
		base = int64(r.u32(12))
	}
	tables := map[string]int64{}
	n := int(r.u16(base + 4))
	for i := 0; i < n; i++ {
		rec := base + 12 + int64(i)*16
		tables[r.tag(rec)] = int64(r.u32(rec + 8))
	}
	for _, t := range []string{"cmap", "maxp", "sbix"} {
		if _, ok := tables[t]; !ok && r.err == nil {
			return nil, fmt.Errorf("no %s table", t)
		}
	}
	cf := &colorFont{f: f, numGlyphs: int(r.u16(tables["maxp"] + 4))}

	cmap := tables["cmap"]
	for i := 0; i < int(r.u16(cmap+2)); i++ {
		rec := cmap + 4 + int64(i)*8
		platform, encoding := r.u16(rec), r.u16(rec+2)
		sub := cmap + int64(r.u32(rec+4))
		// Format 12 covers the code points past U+FFFF that most emoji are.
		if (platform == 3 && encoding == 10 || platform == 0) && r.u16(sub) == 12 {
			ng := int(r.u32(sub + 12))
			if ng > 1<<20 {
				return nil, errors.New("cmap too large")
			}
			buf := r.bytes(sub+16, ng*12)
			for g := 0; g+12 <= len(buf); g += 12 {
				cf.groups = append(cf.groups, cmapGroup{
					binary.BigEndian.Uint32(buf[g:]),
					binary.BigEndian.Uint32(buf[g+4:]),
					binary.BigEndian.Uint32(buf[g+8:]),
				})
			}
			break
		}
	}
	if len(cf.groups) == 0 && r.err == nil {
		return nil, errors.New("no format 12 cmap")
	}

	sbix := tables["sbix"]
	ppem := map[int64]uint16{}
	for i := 0; i < int(r.u32(sbix+4)) && i < 64; i++ {
		strike := sbix + int64(r.u32(sbix+8+int64(i)*4))
		ppem[strike] = r.u16(strike)
		cf.strikes = append(cf.strikes, strike)
	}
	sort.Slice(cf.strikes, func(i, j int) bool { return ppem[cf.strikes[i]] > ppem[cf.strikes[j]] })
	if len(cf.strikes) == 0 && r.err == nil {
		return nil, errors.New("sbix has no strikes")
	}
	return cf, r.err
}

// glyph returns the glyph of r, or 0.
func (cf *colorFont) glyph(r rune) uint32 {
	c := uint32(r)
	lo, hi := 0, len(cf.groups)
	for lo < hi {
		mid := (lo + hi) / 2
		g := cf.groups[mid]
		switch {
		case c < g.start:
			hi = mid
		case c > g.end:
			lo = mid + 1
		default:
			return g.glyph + c - g.start
		}
	}
	return 0
}

// picture decodes the PNG of r in the largest strike that has one. Pillow,
// through FreeType, reads only PNG strikes, and drew Apple Color Emoji at 96
// px in task 0034; a larger strike in another format is passed over.
func (cf *colorFont) picture(r rune) (image.Image, error) {
	g := cf.glyph(r)
	if g == 0 {
		return nil, fmt.Errorf("no glyph for U+%04X", r)
	}
	var err error
	for _, strike := range cf.strikes {
		var img image.Image
		if img, err = cf.strikePicture(strike, g, r); err == nil {
			return img, nil
		}
	}
	return nil, err
}

// strikePicture decodes the PNG of glyph g, for rune r, in one strike.
func (cf *colorFont) strikePicture(strike int64, g uint32, r rune) (image.Image, error) {
	fr := &fontReader{f: cf.f}
	// A 'dupe' record points at another glyph; follow a few.
	for hop := 0; hop < 4; hop++ {
		if int(g) >= cf.numGlyphs {
			return nil, fmt.Errorf("glyph %d out of range", g)
		}
		at := strike + 4 + int64(g)*4
		start, end := int64(fr.u32(at)), int64(fr.u32(at+4))
		if fr.err != nil {
			return nil, fr.err
		}
		if end-start <= 8 || end-start > 4<<20 {
			return nil, fmt.Errorf("no picture for U+%04X", r)
		}
		data := fr.bytes(strike+start, int(end-start))
		if fr.err != nil {
			return nil, fr.err
		}
		switch string(data[4:8]) {
		case "png ":
			return png.Decode(bytes.NewReader(data[8:]))
		case "dupe":
			g = uint32(binary.BigEndian.Uint16(data[8:]))
		default:
			return nil, fmt.Errorf("U+%04X is %q, not png", r, data[4:8])
		}
	}
	return nil, fmt.Errorf("U+%04X: too many dupes", r)
}

// fontReader reads big-endian fields at offsets, keeping the first error.
type fontReader struct {
	f   *os.File
	err error
}

func (r *fontReader) bytes(at int64, n int) []byte {
	buf := make([]byte, n)
	if r.err == nil {
		if _, err := r.f.ReadAt(buf, at); err != nil {
			r.err = fmt.Errorf("read %d bytes at %d: %w", n, at, err)
		}
	}
	return buf
}

func (r *fontReader) u16(at int64) uint16 { return binary.BigEndian.Uint16(r.bytes(at, 2)) }
func (r *fontReader) u32(at int64) uint32 { return binary.BigEndian.Uint32(r.bytes(at, 4)) }
func (r *fontReader) tag(at int64) string { return string(r.bytes(at, 4)) }
