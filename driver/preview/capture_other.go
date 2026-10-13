//go:build !darwin

package preview

// NewCapturer returns ErrUnsupported: window capture uses ScreenCaptureKit,
// which only macOS has.
func NewCapturer() (Capturer, error) {
	return nil, ErrUnsupported
}
