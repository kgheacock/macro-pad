package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/kgheacock/macro-pad/driver/iterm2bridge"
	"github.com/kgheacock/macro-pad/driver/plugin"
)

// Signal names this backend adds to the vocabulary in driver/README.md.
// processWaiting and processDone are plugin.SignalProcessWaiting and
// plugin.SignalProcessDone.
const (
	SignalProcessRunning = "processRunning"
	SignalProcessExited  = "processExited"
)

// Named errors from BindITerm2 so a caller can tell the user what to fix.
var (
	// ErrITerm2APIDisabled means iTerm2 is not serving its Python API.
	ErrITerm2APIDisabled = errors.New("session: iTerm2 Python API is disabled; turn on Settings > General > Magic > Enable Python API")
	// ErrITerm2SessionNotFound means iTerm2 has no session with that ID.
	ErrITerm2SessionNotFound = errors.New("session: iTerm2 has no session with that ID")
	// ErrITerm2PackageMissing means the Python helper cannot import iterm2.
	ErrITerm2PackageMissing = errors.New("session: the iterm2 Python package is not installed; run `pip install iterm2`")
	// ErrITerm2Bridge covers every other bridge failure, including a
	// bridge that never answers.
	ErrITerm2Bridge = errors.New("session: iTerm2 bridge failed")
)

// defaultReadyTimeout bounds how long BindITerm2 waits for the bridge to
// answer, so a stuck iTerm2 never hangs the caller.
const defaultReadyTimeout = 10 * time.Second

// Broadcaster is the part of plugin.Server that this package uses.
type Broadcaster interface {
	Broadcast(m plugin.Message)
}

// BridgeLauncher starts one bridge for an iTerm2 session ID and returns
// the pipe to it: Write sends commands, Read yields its JSON lines, Close
// stops it. Tests replace it with an in-memory bridge.
type BridgeLauncher func(ctx context.Context, sessionID string) (io.ReadWriteCloser, error)

// ITerm2Config describes one key's binding to an iTerm2 session.
type ITerm2Config struct {
	// KeyIndex is the key the session's signals are broadcast for.
	KeyIndex byte
	// SessionID is iTerm2's session ID, as in $ITERM_SESSION_ID after the
	// "w0t0p0:" prefix. A human configures it.
	SessionID string
	// Broadcaster receives the lifecycle signals.
	Broadcaster Broadcaster
	// Launch starts the bridge. Nil runs ExecBridge("python3").
	Launch BridgeLauncher
	// ReadyTimeout bounds the wait for the bridge. Zero means 10 seconds.
	ReadyTimeout time.Duration
}

// ITerm2 is a Session bound to an existing iTerm2 session.
type ITerm2 struct {
	cfg  ITerm2Config
	conn io.ReadWriteCloser

	writeMu   sync.Mutex
	closeOnce sync.Once
	done      chan struct{} // closed when the read loop ends
}

var _ Session = (*ITerm2)(nil)

// bridgeLine is one JSON line from the bridge. See bridge.py.
type bridgeLine struct {
	Event   string `json:"event"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

// BindITerm2 starts the bridge for cfg.SessionID and waits until iTerm2
// confirms the session or reports why it cannot. On success, the
// session's shell-integration events are broadcast as signals until Close.
func BindITerm2(ctx context.Context, cfg ITerm2Config) (*ITerm2, error) {
	if cfg.SessionID == "" {
		return nil, errors.New("session: iTerm2 session ID is empty")
	}
	if cfg.Broadcaster == nil {
		return nil, errors.New("session: iTerm2 binding needs a Broadcaster")
	}
	if cfg.Launch == nil {
		cfg.Launch = ExecBridge("python3")
	}
	if cfg.ReadyTimeout == 0 {
		cfg.ReadyTimeout = defaultReadyTimeout
	}

	conn, err := cfg.Launch(ctx, cfg.SessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrITerm2Bridge, err)
	}
	s := &ITerm2{cfg: cfg, conn: conn, done: make(chan struct{})}

	ready := make(chan error, 1)
	go s.readLoop(ready)

	timer := time.NewTimer(cfg.ReadyTimeout)
	defer timer.Stop()
	select {
	case err := <-ready:
		if err != nil {
			s.Close()
			return nil, err
		}
		return s, nil
	case <-timer.C:
		s.Close()
		return nil, fmt.Errorf("%w: no answer within %s", ErrITerm2Bridge, cfg.ReadyTimeout)
	case <-ctx.Done():
		s.Close()
		return nil, ctx.Err()
	}
}

// SendKeys sends text to the bound session through iTerm2's
// async_send_text.
func (s *ITerm2) SendKeys(text string) error {
	line, err := json.Marshal(map[string]string{"op": "send_text", "text": text})
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.conn.Write(append(line, '\n'))
	return err
}

// Close stops the bridge. It is safe to call more than once.
func (s *ITerm2) Close() error {
	var err error
	s.closeOnce.Do(func() {
		err = s.conn.Close()
		<-s.done
	})
	return err
}

// readLoop answers the bind handshake on ready, then broadcasts signals
// until the bridge closes its output.
func (s *ITerm2) readLoop(ready chan<- error) {
	defer close(s.done)
	answered := false
	answer := func(err error) {
		if !answered {
			answered = true
			ready <- err
		}
	}

	scanner := bufio.NewScanner(s.conn)
	for scanner.Scan() {
		var l bridgeLine
		if err := json.Unmarshal(scanner.Bytes(), &l); err != nil {
			continue
		}
		switch l.Event {
		case "ready":
			answer(nil)
		case "error":
			answer(bridgeError(l))
		case "prompt":
			s.signal(plugin.SignalProcessWaiting)
		case "command_start":
			s.signal(SignalProcessRunning)
		case "command_end":
			if l.Status == 0 {
				s.signal(plugin.SignalProcessDone)
			} else {
				s.signal(SignalProcessExited)
			}
		}
	}
	answer(fmt.Errorf("%w: bridge exited before it answered", ErrITerm2Bridge))
}

func (s *ITerm2) signal(name string) {
	s.cfg.Broadcaster.Broadcast(plugin.Message{
		Kind:   plugin.KindSignal,
		Signal: &plugin.SignalPayload{KeyIndex: s.cfg.KeyIndex, Name: name},
	})
}

// bridgeError maps a bridge "error" line to its named error.
func bridgeError(l bridgeLine) error {
	switch l.Code {
	case "api_disabled":
		return fmt.Errorf("%w (%s)", ErrITerm2APIDisabled, l.Message)
	case "session_not_found":
		return fmt.Errorf("%w (%s)", ErrITerm2SessionNotFound, l.Message)
	case "package_missing":
		return ErrITerm2PackageMissing
	}
	return fmt.Errorf("%w: %s", ErrITerm2Bridge, l.Message)
}

// ExecBridge returns a BridgeLauncher that runs the embedded bridge.py
// with the given Python interpreter.
func ExecBridge(python string) BridgeLauncher {
	return func(ctx context.Context, sessionID string) (io.ReadWriteCloser, error) {
		cmd := exec.CommandContext(ctx, python, "-c", iterm2bridge.Script, "--session-id", sessionID)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &execBridge{cmd: cmd, in: stdin, out: stdout}, nil
	}
}

type execBridge struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out io.Reader
}

func (b *execBridge) Read(p []byte) (int, error)  { return b.out.Read(p) }
func (b *execBridge) Write(p []byte) (int, error) { return b.in.Write(p) }

// Close closes the bridge's stdin, which makes it exit, and reaps it.
func (b *execBridge) Close() error {
	b.in.Close()
	done := make(chan struct{})
	go func() { b.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		b.cmd.Process.Kill()
		<-done
	}
	return nil
}
