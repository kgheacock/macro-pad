// Package pager is the plugin host's page switcher. A click on its key
// shows the next page, and the key shows the current page's name and its
// number. Put it on page "*" so its key shows on every page.
package pager

import (
	"context"
	"fmt"
	"slices"

	"github.com/kgheacock/macro-pad/driver/host"
	"github.com/kgheacock/macro-pad/driver/host/keyimg"
)

// Factory registers the plugin as "pager".
var Factory = host.Factory{
	Name:        "pager",
	Description: "a click on its key shows the next page of keys",
	New:         func() host.Plugin { return &Plugin{} },
}

// Config is the pager's config.
type Config struct {
	// Pages is the cycle, in order. Empty means every page.
	Pages []string `json:"pages,omitempty"`
	// Click is the click that moves on: "single" (default), "double", or
	// "long".
	Click string `json:"click,omitempty"`
}

var clicks = map[string]host.ClickKind{"": host.Single, "single": host.Single, "double": host.Double, "long": host.Long}

// Plugin is one pager instance.
type Plugin struct {
	h     host.Host
	pages []string
}

func (p *Plugin) Start(ctx context.Context, h host.Host) error {
	var cfg Config
	if err := h.Config(&cfg); err != nil {
		return err
	}
	kind, ok := clicks[cfg.Click]
	if !ok {
		return fmt.Errorf("click %q: want single, double, or long", cfg.Click)
	}
	all := h.Pages()
	p.h, p.pages = h, all
	if len(cfg.Pages) > 0 {
		for _, pg := range cfg.Pages {
			if !slices.Contains(all, pg) {
				return fmt.Errorf("page %q: no plugin is on it", pg)
			}
		}
		p.pages = cfg.Pages
	}
	if len(h.Keys()) == 0 {
		return fmt.Errorf("needs a key")
	}

	h.OnClick(kind, func(int) { p.next() })
	h.OnPageChange(func(string) { p.draw() })
	p.draw()
	return nil
}

// next shows the page after the current one in the cycle. From a page
// outside the cycle, it shows the cycle's first page.
func (p *Plugin) next() {
	i := slices.Index(p.pages, p.h.Page())
	if err := p.h.ShowPage(p.pages[(i+1)%len(p.pages)]); err != nil {
		p.h.Log("%v", err)
	}
}

// draw shows the current page's name and number on every pager key.
func (p *Plugin) draw() {
	page := p.h.Page()
	i := slices.Index(p.pages, page)
	label := page
	bg := keyimg.Color{}
	if i >= 0 {
		label = fmt.Sprintf("%s\n%d/%d", page, i+1, len(p.pages))
		bg = keyimg.Palette[i%len(keyimg.Palette)].Dim(0.4)
	}
	img := keyimg.Text(label, keyimg.MustHex("#FFFFFF"), bg)
	for _, k := range p.h.Keys() {
		if err := p.h.Key(k).SetImage(img); err != nil {
			p.h.Log("key %d: %v", k, err)
		}
	}
}
