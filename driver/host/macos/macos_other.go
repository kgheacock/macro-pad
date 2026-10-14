//go:build !darwin || !cgo

package macos

func permissions() PermissionState { return PermissionState{} }

func selectedText() (string, error) { return "", ErrUnsupported }

func frontWindowID() WindowID { return 0 }
