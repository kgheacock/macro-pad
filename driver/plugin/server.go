// Package plugin implements a local WebSocket API that lets third-party
// processes react to macro pad events and set key state, without opening
// transport.Device or transport.Emulator themselves. See task 0028 for the
// design.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// DefaultPort is the TCP port Run binds on 127.0.0.1 when a caller has no
// reason to pick a different one.
const DefaultPort = 8765

// maxClients bounds how many plugins may be connected at once. Server
// rejects a connection past this cap instead of growing without limit.
const maxClients = 16

// clientQueueSize bounds how many undelivered event messages one client's
// send queue holds. It matches deviceQueueSize in driver/transport. A
// full queue drops the next message rather than blocking the dispatch
// loop.
const clientQueueSize = 32

// maxDrops is how many dropped messages one client tolerates before the
// server disconnects it. A client hitting this cap is not reading fast
// enough to keep up, and holding its queue open no longer serves it.
const maxDrops = 8

// audioQueueSize bounds how many undelivered audio frames one subscribed
// client's audio queue holds. It is sized independently of
// clientQueueSize, on its own channel, so subscribing to audio never
// shrinks the headroom clientQueueSize gives that client's JSON messages,
// and a client that never subscribes never has an audio frame queued for
// it at all. See task 0031.
const audioQueueSize = 8

// maxClientsCloseReason is sent to a client rejected for being past
// maxClients.
const maxClientsCloseReason = "too many clients connected"

// wsConn is the subset of *websocket.Conn the server needs. Tests
// substitute a fake so a stalled or slow client can be simulated
// deterministically, with no dependency on OS socket buffering.
type wsConn interface {
	ReadJSON(v any) error
	WriteJSON(v any) error
	WriteMessage(messageType int, data []byte) error
	WriteClose(code int, reason string) error
	Close() error
}

// gorillaConn adapts *websocket.Conn to wsConn.
type gorillaConn struct {
	*websocket.Conn
}

// writeControlTimeout bounds how long a close control frame write may
// block a client's connection teardown.
const writeControlTimeout = time.Second

func (c gorillaConn) WriteClose(code int, reason string) error {
	deadline := time.Now().Add(writeControlTimeout)
	return c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), deadline)
}

// Injector lets a Server apply a client's injectEvent message to the
// underlying transport, simulating a press or release with no board
// attached. Only *transport.Emulator implements it — transport.Device
// does not — so a real-hardware Server can never be misconfigured to
// accept one at compile time. See task 0029.
type Injector interface {
	InjectEvent(transport.Event) error
}

// client is one connected plugin.
type client struct {
	conn      wsConn
	send      chan Message
	audioSend chan []byte

	// drops counts messages dropped because send or audioSend was full.
	// It is only ever touched from Server.broadcast and
	// Server.broadcastAudio, themselves only ever called from
	// Server.dispatchLoop, so no lock guards it.
	drops int

	// audioSubscribed says whether this client currently receives
	// audio frames. Server.broadcastAudio (dispatchLoop's goroutine)
	// reads it and this client's own readPump goroutine writes it, so
	// it needs atomic access instead of the lock-free convention drops
	// relies on.
	audioSubscribed atomic.Bool

	closeOnce sync.Once
}

// Server bridges one transport.Transport to any number of local WebSocket
// clients: it fans out every decoded transport.Event to each connected
// client as JSON, and turns each client's setKeyState message into one
// transport.Transport.SendKeyState call.
type Server struct {
	dev      transport.Transport
	injector Injector
	resolver *clickResolver

	upgrader websocket.Upgrader

	mu      sync.Mutex
	clients map[*client]struct{}
}

// NewServer creates a Server bridging dev, and starts the goroutine that
// dispatches dev's events to connected clients. Call Run to accept
// connections.
//
// injector, when not nil, lets a client's injectEvent message simulate a
// press or release with no board attached — see task 0029. A
// real-hardware Server passes nil, so it drops any injectEvent message it
// receives.
func NewServer(dev transport.Transport, injector Injector) *Server {
	s := &Server{
		dev:      dev,
		injector: injector,
		clients:  make(map[*client]struct{}),
		upgrader: websocket.Upgrader{
			// The localhost bind and maxClients are this server's only
			// access control (see the task spec's Non-goals); a browser
			// tab's Origin header is not checked on top of that.
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
	s.resolver = newClickResolver(func(keyIndex byte, name string) {
		s.Broadcast(signalMessage(keyIndex, name))
	})
	go s.dispatchLoop()
	return s
}

// Run opens a WebSocket server bound to 127.0.0.1:port and serves it
// until ctx is done. It always binds loopback-only, regardless of port,
// so this API cannot be pointed at a non-local interface by mistake.
func (s *Server) Run(ctx context.Context, port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("plugin: listen: %w", err)
	}

	httpServer := &http.Server{Handler: s}
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(ln) }()

	select {
	case <-ctx.Done():
		httpServer.Close()
		<-errCh
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// ServeHTTP implements http.Handler. It upgrades the request to a
// WebSocket connection and registers it as a client.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.addClient(gorillaConn{conn})
}

// addClient registers conn as a new client and starts its read and write
// pumps, unless the server is already at maxClients, in which case it
// sends a close message carrying a reason and returns nil.
func (s *Server) addClient(conn wsConn) *client {
	s.mu.Lock()
	if len(s.clients) >= maxClients {
		s.mu.Unlock()
		conn.WriteClose(websocket.CloseTryAgainLater, maxClientsCloseReason)
		conn.Close()
		return nil
	}
	c := &client{
		conn:      conn,
		send:      make(chan Message, clientQueueSize),
		audioSend: make(chan []byte, audioQueueSize),
	}
	s.clients[c] = struct{}{}
	s.mu.Unlock()

	go s.writePump(c)
	go s.readPump(c)
	return c
}

// removeClient unregisters c and releases its connection. It is safe to
// call more than once, and from more than one goroutine: only the first
// call has any effect.
func (s *Server) removeClient(c *client) {
	c.closeOnce.Do(func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.mu.Unlock()
		close(c.send)
		close(c.audioSend)
		c.conn.Close()
	})
}

// readPump decodes JSON messages from c until its connection errors or
// closes, turning each setKeyState message into one SendKeyState call
// (and rebroadcasting it to every client, so an observer sees it without
// reading it back off the device) and each injectEvent message into one
// Injector.InjectEvent call.
func (s *Server) readPump(c *client) {
	defer s.removeClient(c)
	for {
		var msg Message
		if err := c.conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Kind {
		case KindSetKeyState:
			ks, err := msg.SetKeyState.toKeyState()
			if err != nil {
				continue
			}
			s.dev.SendKeyState(ks)
			s.broadcast(msg)
		case KindSetCustomGlyph:
			keyIndex, pixels, err := msg.SetCustomGlyph.toPixels()
			if err != nil {
				continue
			}
			s.dev.SendCustomGlyph(keyIndex, pixels)
			s.broadcast(msg)
		case KindInjectEvent:
			if s.injector == nil {
				continue
			}
			ev, err := msg.InjectEvent.toEvent()
			if err != nil {
				continue
			}
			ev.Timestamp = uint64(time.Now().UnixMicro())
			s.injector.InjectEvent(ev)
		case KindSignal:
			if msg.Signal == nil {
				continue
			}
			s.Broadcast(msg)
		case KindSubscribeAudio:
			c.audioSubscribed.Store(true)
		case KindUnsubscribeAudio:
			c.audioSubscribed.Store(false)
		}
	}
}

// writePump delivers c's queued messages and audio frames to its
// connection in the order they arrive, until removeClient closes both
// queues or a write fails.
func (s *Server) writePump(c *client) {
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			if err := c.conn.WriteJSON(msg); err != nil {
				s.removeClient(c)
				return
			}
		case frame, ok := <-c.audioSend:
			if !ok {
				return
			}
			if err := c.conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				s.removeClient(c)
				return
			}
		}
	}
}

// dispatchLoop reads decoded messages from s.dev until it errors, which
// happens once the transport is closed, and broadcasts every Event to
// connected clients and every AudioChunk to subscribed ones. Every Event
// also feeds s.resolver, which may itself broadcast a resolved
// click-pattern signal.
func (s *Server) dispatchLoop() {
	for {
		msg, err := s.dev.ReadMessage()
		if err != nil {
			return
		}
		switch msg.Type {
		case transport.MessageTypeEvent:
			s.broadcast(eventMessage(msg.Event))
			s.resolver.handleEvent(msg.Event)
		case transport.MessageTypeAudioChunk:
			s.broadcastAudio(msg.AudioChunk)
		}
	}
}

// Broadcast delivers m to every connected client, exactly like a device
// event or a rebroadcast setKeyState/setCustomGlyph message. An
// in-process observer — task 0016's OSC 133 scanner, task 0015's iTerm2
// bridge — calls this directly to emit a signal, with no need to dial its
// own daemon over a socket. See task 0013.
func (s *Server) Broadcast(m Message) {
	s.broadcast(m)
}

// broadcast delivers m to every connected client's send queue without
// blocking: a full queue drops m for that client instead of stalling
// delivery to the rest. A client whose queue has dropped maxDrops
// messages is disconnected.
func (s *Server) broadcast(m Message) {
	var toRemove []*client

	s.mu.Lock()
	for c := range s.clients {
		select {
		case c.send <- m:
		default:
			c.drops++
			if c.drops >= maxDrops {
				toRemove = append(toRemove, c)
			}
		}
	}
	s.mu.Unlock()

	for _, c := range toRemove {
		s.removeClient(c)
	}
}

// broadcastAudio delivers c's raw bytes, framed by audioFrame, to every
// subscribed client's audioSend without blocking: a full queue drops the
// frame for that client instead of stalling delivery to the rest. A
// client whose queue has dropped maxDrops messages — audio or otherwise
// — is disconnected, exactly like broadcast. A client that never
// subscribed never receives a frame and never touches audioSend at all.
func (s *Server) broadcastAudio(chunk transport.AudioChunk) {
	frame := audioFrame(chunk)
	var toRemove []*client

	s.mu.Lock()
	for c := range s.clients {
		if !c.audioSubscribed.Load() {
			continue
		}
		select {
		case c.audioSend <- frame:
		default:
			c.drops++
			if c.drops >= maxDrops {
				toRemove = append(toRemove, c)
			}
		}
	}
	s.mu.Unlock()

	for _, c := range toRemove {
		s.removeClient(c)
	}
}

// audioFrame builds the binary frame layout a subscribed client receives
// for one transport.AudioChunk: the stream ID byte, the raw PCM bytes,
// then a final-chunk byte — the same layout driver/transport's wire
// protocol uses to encode transport.MessageTypeAudioChunk.
func audioFrame(c transport.AudioChunk) []byte {
	buf := make([]byte, 0, 2+len(c.PCM))
	buf = append(buf, c.StreamID)
	buf = append(buf, c.PCM...)
	final := byte(0)
	if c.Final {
		final = 1
	}
	return append(buf, final)
}
