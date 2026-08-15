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

// maxClientsCloseReason is sent to a client rejected for being past
// maxClients.
const maxClientsCloseReason = "too many clients connected"

// wsConn is the subset of *websocket.Conn the server needs. Tests
// substitute a fake so a stalled or slow client can be simulated
// deterministically, with no dependency on OS socket buffering.
type wsConn interface {
	ReadJSON(v any) error
	WriteJSON(v any) error
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

// client is one connected plugin.
type client struct {
	conn wsConn
	send chan Message

	// drops counts messages dropped because send was full. It is only
	// ever touched from Server.broadcast, itself only ever called from
	// Server.dispatchLoop, so no lock guards it.
	drops int

	closeOnce sync.Once
}

// Server bridges one transport.Transport to any number of local WebSocket
// clients: it fans out every decoded transport.Event to each connected
// client as JSON, and turns each client's setKeyState message into one
// transport.Transport.SendKeyState call.
type Server struct {
	dev transport.Transport

	upgrader websocket.Upgrader

	mu      sync.Mutex
	clients map[*client]struct{}
}

// NewServer creates a Server bridging dev, and starts the goroutine that
// dispatches dev's events to connected clients. Call Run to accept
// connections.
func NewServer(dev transport.Transport) *Server {
	s := &Server{
		dev:     dev,
		clients: make(map[*client]struct{}),
		upgrader: websocket.Upgrader{
			// The localhost bind and maxClients are this server's only
			// access control (see the task spec's Non-goals); a browser
			// tab's Origin header is not checked on top of that.
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
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
	c := &client{conn: conn, send: make(chan Message, clientQueueSize)}
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
		c.conn.Close()
	})
}

// readPump decodes JSON messages from c until its connection errors or
// closes, turning each setKeyState message into one SendKeyState call.
func (s *Server) readPump(c *client) {
	defer s.removeClient(c)
	for {
		var msg Message
		if err := c.conn.ReadJSON(&msg); err != nil {
			return
		}
		if msg.Kind != KindSetKeyState {
			continue
		}
		ks, err := msg.SetKeyState.toKeyState()
		if err != nil {
			continue
		}
		s.dev.SendKeyState(ks)
	}
}

// writePump delivers c's queued messages to its connection in order,
// until the queue is closed by removeClient or a write fails.
func (s *Server) writePump(c *client) {
	for msg := range c.send {
		if err := c.conn.WriteJSON(msg); err != nil {
			s.removeClient(c)
			return
		}
	}
}

// dispatchLoop reads decoded messages from s.dev until it errors, which
// happens once the transport is closed, and broadcasts every Event to
// connected clients.
func (s *Server) dispatchLoop() {
	for {
		msg, err := s.dev.ReadMessage()
		if err != nil {
			return
		}
		if msg.Type != transport.MessageTypeEvent {
			continue
		}
		s.broadcast(eventMessage(msg.Event))
	}
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
