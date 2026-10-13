package pager

import (
	"testing"

	"github.com/kgheacock/macro-pad/driver/host"
	"github.com/kgheacock/macro-pad/driver/host/hosttest"
)

func TestClickCyclesPages(t *testing.T) {
	h := hosttest.New(t, 5).SetPages("work", "previews", "music")
	if err := h.Start(&Plugin{}); err != nil {
		t.Fatal(err)
	}
	if h.KeyState(5).PNG == nil {
		t.Fatal("pager key shows no label at start")
	}
	first := h.KeyState(5).PNG

	for _, want := range []string{"previews", "music", "work"} {
		h.Click(5, host.Single)
		if h.Page() != want {
			t.Fatalf("after a click, page = %q, want %q", h.Page(), want)
		}
	}
	h.Click(5, host.Single)
	if string(h.KeyState(5).PNG) == string(first) {
		t.Error("label did not change with the page")
	}
}

func TestConfig_PagesAndClick(t *testing.T) {
	h := hosttest.New(t, 5).SetPages("a", "b", "c").SetConfig(Config{Pages: []string{"a", "c"}, Click: "long"})
	if err := h.Start(&Plugin{}); err != nil {
		t.Fatal(err)
	}
	h.Click(5, host.Single) // not the configured click
	if h.Page() != "a" {
		t.Fatalf("single click moved the page to %q", h.Page())
	}
	h.Click(5, host.Long)
	if h.Page() != "c" {
		t.Errorf("page = %q, want c (b is not in the cycle)", h.Page())
	}
}

func TestConfig_Rejects(t *testing.T) {
	for name, cfg := range map[string]Config{
		"unknown page":  {Pages: []string{"nope"}},
		"unknown click": {Click: "triple"},
	} {
		h := hosttest.New(t, 5).SetPages("a").SetConfig(cfg)
		if err := h.Start(&Plugin{}); err == nil {
			t.Errorf("%s: Start = nil, want an error", name)
		}
	}
}
