package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// stateFileName is the file in a state directory that holds every key's
// key state. A custom glyph's pixels live beside it, one raw file for each
// distinct glyph, named by the hash of its pixels (glyphFileName).
const stateFileName = "keys.json"

const (
	glyphFilePrefix = "glyph-"
	glyphFileSuffix = ".bin"
)

// glyphRec is one custom glyph: its pixels, and the hash that names its file.
type glyphRec struct {
	hash   string
	pixels []byte
}

func newGlyphRec(pixels []byte) glyphRec {
	sum := sha256.Sum256(pixels)
	return glyphRec{hash: hex.EncodeToString(sum[:]), pixels: pixels}
}

// glyphFileName names the file of a glyph by the hash of its pixels, so the
// same picture is written once, whichever key shows it.
func glyphFileName(hash string) string { return glyphFilePrefix + hash + glyphFileSuffix }

// legacyGlyphFileName is the name of a key's glyph file before task 0061:
// one file for each key, rewritten on every send. load still reads it from a
// state directory that an older daemon wrote.
func legacyGlyphFileName(keyIndex byte) string {
	return fmt.Sprintf("%s%d%s", glyphFilePrefix, keyIndex, glyphFileSuffix)
}

// isHashedGlyphFile reports whether name is a glyph file named by hash.
func isHashedGlyphFile(name string) bool {
	if !strings.HasPrefix(name, glyphFilePrefix) || !strings.HasSuffix(name, glyphFileSuffix) {
		return false
	}
	hash := strings.TrimSuffix(strings.TrimPrefix(name, glyphFilePrefix), glyphFileSuffix)
	return len(hash) == sha256.Size*2
}

// storedKey is one key's key state as keys.json holds it. It carries no
// protocol version: a state is what the key shows, and loading stamps the
// current ProtocolVersion on it. Glyph is the hash of the key's custom
// glyph, when it shows one.
type storedKey struct {
	KeyIndex byte   `json:"key_index"`
	Color    uint16 `json:"color"`
	EmojiID  byte   `json:"emoji_id"`
	Blink    bool   `json:"blink"`
	Glyph    string `json:"glyph,omitempty"`
}

type storedState struct {
	Keys []storedKey `json:"keys"`
}

// stateStore keeps what Reconnecting remembers in a directory on the host,
// so a restart of the daemon or a reboot of the host loses nothing.
//
// A write goes to a temporary file and is renamed over the old one, so a
// crash leaves the old file or the new one and never half of either. Host
// disk does not wear like the board's flash, but a layer switch must not
// write 200 KB, so a glyph file is written once, by the hash of its pixels,
// and keys.json only when a key state changes (task 0061). The caller holds
// its own lock: a stateStore is not safe for concurrent use.
type stateStore struct {
	dir string
	// onDisk is the glyph hashes that have a file in dir.
	onDisk map[string]bool
	// writes counts the files written, by name, for a test.
	writes map[string]int
}

func (st *stateStore) noteWrite(name string) {
	if st.writes == nil {
		st.writes = make(map[string]int)
	}
	st.writes[name]++
}

// load reads the directory. A missing directory or file is no state. A
// glyph file that is missing or of the wrong length is named in the
// returned warnings, and its key loses the glyph. A keys.json that does not
// parse is an error.
func (st *stateStore) load() (keys map[byte]KeyState, glyphs map[byte]glyphRec, warnings []string, err error) {
	keys = make(map[byte]KeyState)
	glyphs = make(map[byte]glyphRec)
	st.onDisk = make(map[string]bool)

	data, err := os.ReadFile(filepath.Join(st.dir, stateFileName))
	if errors.Is(err, os.ErrNotExist) {
		return keys, glyphs, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	var stored storedState
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", stateFileName, err)
	}
	hashes := make(map[byte]string)
	for _, k := range stored.Keys {
		keys[k.KeyIndex] = KeyState{
			KeyIndex: k.KeyIndex,
			Version:  ProtocolVersion,
			Color:    k.Color,
			EmojiID:  k.EmojiID,
			Blink:    k.Blink,
		}
		hashes[k.KeyIndex] = k.Glyph
	}

	for key, ks := range keys {
		if ks.EmojiID != CustomGlyphSentinelEmojiID {
			continue
		}
		name := legacyGlyphFileName(key)
		if hashes[key] != "" {
			name = glyphFileName(hashes[key])
		}
		pixels, err := os.ReadFile(filepath.Join(st.dir, name))
		switch {
		case errors.Is(err, os.ErrNotExist):
			warnings = append(warnings, name+" is missing")
		case err != nil:
			warnings = append(warnings, fmt.Sprintf("%s: %v", name, err))
		case len(pixels) != CustomGlyphPixelsSize:
			warnings = append(warnings, fmt.Sprintf("%s has %d bytes, want %d", name, len(pixels), CustomGlyphPixelsSize))
		default:
			rec := newGlyphRec(pixels)
			if hashes[key] != "" && rec.hash != hashes[key] {
				warnings = append(warnings, fmt.Sprintf("%s does not match its hash", name))
				break
			}
			glyphs[key] = rec
			if hashes[key] != "" {
				st.onDisk[rec.hash] = true
			} else if _, werr := st.saveGlyph(rec); werr != nil {
				// A glyph file of an older daemon, moved to its hashed name,
				// so the next keys.json can name it.
				warnings = append(warnings, fmt.Sprintf("%s: %v", name, werr))
			}
			continue
		}
		// A key that names a glyph it cannot show keeps its color and
		// blink and shows no glyph, as the board treated a lost glyph.
		ks.EmojiID = 0
		keys[key] = ks
	}
	sort.Strings(warnings)
	return keys, glyphs, warnings, nil
}

// prune deletes the glyph files that no key shows. It runs once, when the
// daemon starts: during a run, a glyph file stays, so that a return to a layer
// writes nothing. It never touches a file of an older daemon's naming.
func (st *stateStore) prune(glyphs map[byte]glyphRec) error {
	keep := make(map[string]bool, len(glyphs))
	for _, g := range glyphs {
		keep[glyphFileName(g.hash)] = true
	}
	entries, err := os.ReadDir(st.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var errs []error
	for _, e := range entries {
		if !isHashedGlyphFile(e.Name()) || keep[e.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(st.dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		delete(st.onDisk, strings.TrimSuffix(strings.TrimPrefix(e.Name(), glyphFilePrefix), glyphFileSuffix))
	}
	return errors.Join(errs...)
}

// saveKeys writes keys.json with every key's state, in key order.
func (st *stateStore) saveKeys(keys map[byte]KeyState, glyphs map[byte]glyphRec) error {
	stored := storedState{Keys: []storedKey{}}
	for _, ks := range keys {
		sk := storedKey{KeyIndex: ks.KeyIndex, Color: ks.Color, EmojiID: ks.EmojiID, Blink: ks.Blink}
		if g, ok := glyphs[ks.KeyIndex]; ok && ks.EmojiID == CustomGlyphSentinelEmojiID {
			sk.Glyph = g.hash
		}
		stored.Keys = append(stored.Keys, sk)
	}
	sort.Slice(stored.Keys, func(i, j int) bool { return stored.Keys[i].KeyIndex < stored.Keys[j].KeyIndex })
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	return st.writeFile(stateFileName, append(data, '\n'))
}

// saveGlyph writes a glyph's pixels to the file its hash names, unless that
// file is already there. It reports whether it wrote.
func (st *stateStore) saveGlyph(g glyphRec) (bool, error) {
	if st.onDisk[g.hash] {
		return false, nil
	}
	if err := st.writeFile(glyphFileName(g.hash), g.pixels); err != nil {
		return false, err
	}
	if st.onDisk == nil {
		st.onDisk = make(map[string]bool)
	}
	st.onDisk[g.hash] = true
	return true, nil
}

func (st *stateStore) writeFile(name string, data []byte) error {
	if err := os.MkdirAll(st.dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(st.dir, name+".*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return errors.Join(werr, cerr)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(st.dir, name)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	st.noteWrite(name)
	return nil
}
