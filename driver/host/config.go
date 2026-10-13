package host

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// DefaultPage is the page an instance with no "page" lives on.
const DefaultPage = "main"

// EveryPage is the "page" value of an instance whose keys show on every
// page, such as the pager.
const EveryPage = "*"

// Config is the plugin host's config file.
type Config struct {
	// Addr is host:port of macropadd's plugin server. Empty means
	// 127.0.0.1:plugin.DefaultPort.
	Addr string `json:"addr,omitempty"`
	// StartPage is the page that shows first. Empty means the first page.
	StartPage string `json:"startPage,omitempty"`
	// Plugins is one entry per instance.
	Plugins []InstanceConfig `json:"plugins"`
}

// InstanceConfig is one plugin instance.
type InstanceConfig struct {
	// ID names the instance in logs. Unique.
	ID string `json:"id"`
	// Use names the plugin: a registered Factory.Name.
	Use string `json:"use"`
	// Page is the page the instance's keys show on. Empty means
	// DefaultPage; EveryPage means every page.
	Page string `json:"page,omitempty"`
	// Keys is the physical keys the instance owns on its page.
	Keys []int `json:"keys,omitempty"`
	// Config is the plugin's own settings, decoded by Host.Config.
	Config json.RawMessage `json:"config,omitempty"`
}

func (c InstanceConfig) page() string {
	if c.Page == "" {
		return DefaultPage
	}
	return c.Page
}

// LoadConfig reads the config file at path. It does not validate it; call
// Validate.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return ParseConfig(data)
}

// ParseConfig decodes a config file's bytes. An unknown field is an
// error.
func ParseConfig(data []byte) (Config, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

// Pages lists every page the config names, in order of first use. An
// instance on EveryPage adds none. A config with no page at all has the
// one page DefaultPage.
func (c Config) Pages() []string {
	var pages []string
	seen := map[string]bool{}
	for _, p := range c.Plugins {
		pg := p.page()
		if pg == EveryPage || seen[pg] {
			continue
		}
		seen[pg] = true
		pages = append(pages, pg)
	}
	if len(pages) == 0 {
		pages = []string{DefaultPage}
	}
	return pages
}

// Validate checks c against the registered plugins and returns every
// problem it finds, joined, or nil.
func (c Config) Validate(factories []Factory) error {
	var errs []error
	known := map[string]bool{}
	for _, f := range factories {
		known[f.Name] = true
	}

	ids := map[string]bool{}
	// owner[page][key] is the id that first claimed key on page.
	owner := map[string]map[int]string{}
	for i, p := range c.Plugins {
		name := p.ID
		if name == "" {
			name = fmt.Sprintf("plugins[%d]", i)
			errs = append(errs, fmt.Errorf("%s: no id", name))
		} else if ids[p.ID] {
			errs = append(errs, fmt.Errorf("%s: duplicate id", name))
		}
		ids[p.ID] = true

		if !known[p.Use] {
			errs = append(errs, fmt.Errorf("%s: unknown plugin %q (see --list)", name, p.Use))
		}

		pg := p.page()
		if owner[pg] == nil {
			owner[pg] = map[int]string{}
		}
		for _, k := range p.Keys {
			if k < 0 || k >= NumKeys {
				errs = append(errs, fmt.Errorf("%s: key %d is outside 0..%d", name, k, NumKeys-1))
				continue
			}
			if prev, ok := owner[pg][k]; ok {
				errs = append(errs, fmt.Errorf("%s: key %d on page %q is already owned by %s", name, k, pg, prev))
				continue
			}
			owner[pg][k] = name
		}
	}

	// A key on every page conflicts with that key on any other page.
	for k, every := range owner[EveryPage] {
		for pg, keys := range owner {
			if pg == EveryPage {
				continue
			}
			if other, ok := keys[k]; ok {
				errs = append(errs, fmt.Errorf("%s: key %d on page %q is already owned by %s on every page", other, k, pg, every))
			}
		}
	}

	if c.StartPage != "" {
		found := false
		for _, pg := range c.Pages() {
			found = found || pg == c.StartPage
		}
		if !found {
			errs = append(errs, fmt.Errorf("startPage %q is not a page any plugin is on", c.StartPage))
		}
	}
	return errors.Join(errs...)
}
