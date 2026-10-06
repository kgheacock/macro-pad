package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// stateFileName is the file in a state directory that holds every key's
// key state. A custom glyph's pixels live beside it, one raw file per key,
// named by glyphFileName.
const stateFileName = "keys.json"

func glyphFileName(keyIndex byte) string {
	return fmt.Sprintf("glyph-%d.bin", keyIndex)
}

// storedKey is one key's key state as keys.json holds it. It carries no
// protocol version: a state is what the key shows, and loading stamps the
// current ProtocolVersion on it.
type storedKey struct {
	KeyIndex byte   `json:"key_index"`
	Color    uint16 `json:"color"`
	EmojiID  byte   `json:"emoji_id"`
	Blink    bool   `json:"blink"`
}

type storedState struct {
	Keys []storedKey `json:"keys"`
}

// stateStore keeps what Reconnecting remembers in a directory on the host,
// so a restart of the daemon or a reboot of the host loses nothing. Host
// disk does not wear like the board's flash, so it writes on every change.
//
// A write goes to a temporary file and is renamed over the old one, so a
// crash leaves the old file or the new one and never half of either.
type stateStore struct {
	dir string
}

// load reads the directory. A missing directory or file is no state. A
// glyph file that is missing or of the wrong length is named in the
// returned warnings, and its key loses the glyph. A keys.json that does not
// parse is an error.
func (st *stateStore) load() (keys map[byte]KeyState, glyphs map[byte][]byte, warnings []string, err error) {
	keys = make(map[byte]KeyState)
	glyphs = make(map[byte][]byte)

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
	for _, k := range stored.Keys {
		keys[k.KeyIndex] = KeyState{
			KeyIndex: k.KeyIndex,
			Version:  ProtocolVersion,
			Color:    k.Color,
			EmojiID:  k.EmojiID,
			Blink:    k.Blink,
		}
	}

	for key, ks := range keys {
		if ks.EmojiID != CustomGlyphSentinelEmojiID {
			continue
		}
		name := glyphFileName(key)
		pixels, err := os.ReadFile(filepath.Join(st.dir, name))
		switch {
		case errors.Is(err, os.ErrNotExist):
			warnings = append(warnings, name+" is missing")
		case err != nil:
			warnings = append(warnings, fmt.Sprintf("%s: %v", name, err))
		case len(pixels) != CustomGlyphPixelsSize:
			warnings = append(warnings, fmt.Sprintf("%s has %d bytes, want %d", name, len(pixels), CustomGlyphPixelsSize))
		default:
			glyphs[key] = pixels
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

// saveKeys writes keys.json with every key's state, in key order.
func (st *stateStore) saveKeys(keys map[byte]KeyState) error {
	stored := storedState{Keys: []storedKey{}}
	for _, ks := range keys {
		stored.Keys = append(stored.Keys, storedKey{KeyIndex: ks.KeyIndex, Color: ks.Color, EmojiID: ks.EmojiID, Blink: ks.Blink})
	}
	sort.Slice(stored.Keys, func(i, j int) bool { return stored.Keys[i].KeyIndex < stored.Keys[j].KeyIndex })
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	return st.writeFile(stateFileName, append(data, '\n'))
}

// saveGlyph writes one key's glyph as its raw pixels.
func (st *stateStore) saveGlyph(keyIndex byte, pixels []byte) error {
	return st.writeFile(glyphFileName(keyIndex), pixels)
}

// removeGlyph deletes one key's glyph file, if it has one.
func (st *stateStore) removeGlyph(keyIndex byte) error {
	err := os.Remove(filepath.Join(st.dir, glyphFileName(keyIndex)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
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
	return nil
}
