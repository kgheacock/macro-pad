// Package session holds the backends that let a key reach a terminal
// session: text goes in through SendKeys, and the session's lifecycle
// comes back as plugin signals. See tasks 0011 and 0015.
package session

// Session is a terminal session a key is bound to. A backend (iTerm2
// today, tmux with task 0011) satisfies it, so key actions do not depend
// on which terminal sits behind the key.
type Session interface {
	// SendKeys writes text into the session exactly as given. A trailing
	// "\n" runs the line.
	SendKeys(text string) error
	// Close stops the backend and releases what it holds. The session
	// itself keeps running.
	Close() error
}
