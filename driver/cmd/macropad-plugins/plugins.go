package main

import (
	"github.com/kgheacock/macro-pad/driver/host"
	"github.com/kgheacock/macro-pad/driver/plugins/claudestatus"
	"github.com/kgheacock/macro-pad/driver/plugins/pager"
)

// registry is every plugin this binary carries. To add a plugin, import
// its package and add its Factory here.
var registry = []host.Factory{
	claudestatus.Factory,
	pager.Factory,
}
