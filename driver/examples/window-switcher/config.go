package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// numKeys is the macro pad's key count. A binding outside 0..numKeys-1
// is rejected at load time.
const numKeys = 6

// maxDesktop is the highest desktop number the Ctrl+N shortcut reaches.
// macOS offers "Switch to Desktop N" shortcuts for 1 through 9 only.
const maxDesktop = 9

// Binding is one key's target. Exactly one of Desktop or App is set.
// Window narrows App to the first of its windows whose title contains
// Window; empty means the app's frontmost window.
type Binding struct {
	Key     int    `json:"key"`
	Desktop int    `json:"desktop,omitempty"`
	App     string `json:"app,omitempty"`
	Window  string `json:"window,omitempty"`
	// Color is the key's color as "#RRGGBB". Empty picks a default
	// from the key index.
	Color string `json:"color,omitempty"`

	color uint16 // Color as RGB565, filled in by validate
}

// Config is the window switcher's JSON config file.
type Config struct {
	// Trigger is the event that switches: "press" (the raw press, no
	// delay), or one of the server's resolved signals "singlePress",
	// "doublePress", "longPress". Empty means "press".
	Trigger string    `json:"trigger,omitempty"`
	Keys    []Binding `json:"keys"`
}

// defaultColors gives each key a distinct color when its binding names
// none.
var defaultColors = [numKeys]string{"#3D5CFF", "#12D84A", "#FF8C00", "#B04CFF", "#00C8D7", "#FF3D7F"}

// defaultConfig binds key N to desktop N+1, the setup that needs no
// config file: six desktops and macOS's Ctrl+1..6 shortcuts.
func defaultConfig() Config {
	cfg := Config{Trigger: "press"}
	for i := 0; i < numKeys; i++ {
		cfg.Keys = append(cfg.Keys, Binding{Key: i, Desktop: i + 1})
	}
	return cfg
}

// loadConfig reads and validates the config file at path.
func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return parseConfig(data)
}

func parseConfig(data []byte) (Config, error) {
	var cfg Config
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

var validTriggers = map[string]bool{"press": true, "singlePress": true, "doublePress": true, "longPress": true}

func (c *Config) validate() error {
	if c.Trigger == "" {
		c.Trigger = "press"
	}
	if !validTriggers[c.Trigger] {
		return fmt.Errorf("config: trigger %q: want press, singlePress, doublePress, or longPress", c.Trigger)
	}
	if len(c.Keys) == 0 {
		return errors.New("config: no keys bound")
	}
	seen := map[int]bool{}
	for i := range c.Keys {
		b := &c.Keys[i]
		if b.Key < 0 || b.Key >= numKeys {
			return fmt.Errorf("config: key %d: want 0 to %d", b.Key, numKeys-1)
		}
		if seen[b.Key] {
			return fmt.Errorf("config: key %d bound twice", b.Key)
		}
		seen[b.Key] = true
		switch {
		case b.Desktop != 0 && b.App != "":
			return fmt.Errorf("config: key %d: set desktop or app, not both", b.Key)
		case b.Desktop != 0:
			if b.Desktop < 1 || b.Desktop > maxDesktop {
				return fmt.Errorf("config: key %d: desktop %d: want 1 to %d", b.Key, b.Desktop, maxDesktop)
			}
			if b.Window != "" {
				return fmt.Errorf("config: key %d: window needs app", b.Key)
			}
		case b.App != "":
		default:
			return fmt.Errorf("config: key %d: set desktop or app", b.Key)
		}
		if err := b.fillColor(); err != nil {
			return err
		}
	}
	return nil
}

func (b *Binding) fillColor() error {
	hex := b.Color
	if hex == "" {
		hex = defaultColors[b.Key]
	}
	col, err := parseHexColor(hex)
	if err != nil {
		return fmt.Errorf("config: key %d: %w", b.Key, err)
	}
	b.color = col
	return nil
}

// parseHexColor turns "#RRGGBB" into RGB565, the key state's color
// format (docs/wire-protocol.md).
func parseHexColor(s string) (uint16, error) {
	if len(s) != 7 || s[0] != '#' {
		return 0, fmt.Errorf("color %q: want #RRGGBB", s)
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return 0, fmt.Errorf("color %q: want #RRGGBB", s)
	}
	r, g, b := uint16(v>>16&0xFF), uint16(v>>8&0xFF), uint16(v&0xFF)
	return r>>3<<11 | g>>2<<5 | b>>3, nil
}

// dim returns col at a quarter of its brightness, for keys that are not
// the active target.
func dim(col uint16) uint16 {
	r, g, b := col>>11&0x1F, col>>5&0x3F, col&0x1F
	return (r>>2)<<11 | (g>>2)<<5 | b>>2
}

// describe names b's target for log lines.
func (b Binding) describe() string {
	switch {
	case b.Desktop != 0:
		return fmt.Sprintf("desktop %d", b.Desktop)
	case b.Window != "":
		return fmt.Sprintf("%s window %q", b.App, b.Window)
	default:
		return b.App
	}
}
