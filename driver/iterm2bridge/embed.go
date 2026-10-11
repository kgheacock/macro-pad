// Package iterm2bridge carries the Python helper that talks to iTerm2's
// Python API for driver/session. The script ships inside the Go binary so
// the driver needs no install path for it. See task 0015.
package iterm2bridge

import _ "embed"

// Script is the source of bridge.py.
//
//go:embed bridge.py
var Script string
