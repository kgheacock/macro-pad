package host

// Bus topics the plugins in this repo publish or subscribe to. A topic is
// any string; these are the ones with a documented payload.
const (
	// TopicWindowWillHide is published by a switcher just before it
	// raises another window or switches desktop. Payload: the
	// macos.Window being hidden. A previewer snapshots it, so its key
	// keeps a picture from just before the window was hidden.
	TopicWindowWillHide = "window.willHide"
	// TopicSelectionCaptured is published by the host's selection
	// capture (task 0053), which defines its payload.
	TopicSelectionCaptured = "selection.captured"
)
