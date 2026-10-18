package workers

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// Conn is one registered WebSocket client. Outgoing frames are queued on a
// buffered channel that the HTTP handler's write pump drains.
type Conn struct {
	socket *websocket.Conn
	UserID int

	out  chan []byte
	once sync.Once
}

// Send queues a pre-encoded frame. It never blocks: a client that cannot keep
// up is dropped by the write pump when its queue fills.
func (c *Conn) Send(frame []byte) bool {
	if c == nil {
		return false
	}
	select {
	case c.out <- frame:
		return true
	default:
		return false
	}
}

// Out exposes the outbound frame channel.
func (c *Conn) Out() <-chan []byte { return c.out }

// Socket exposes the underlying gorilla connection.
func (c *Conn) Socket() *websocket.Conn { return c.socket }

// Close terminates the client. It is safe to call multiple times.
func (c *Conn) Close() {
	c.once.Do(func() { close(c.out) })
}

// Hub tracks live WebSocket connections and routes events by user id.
type Hub struct {
	bus *Bus
	log *zap.Logger

	mu      sync.RWMutex
	clients map[*Conn]struct{}
}

// NewHub builds a hub that reads events from bus. log may be nil.
func NewHub(bus *Bus, log *zap.Logger) *Hub {
	return &Hub{bus: bus, log: log, clients: make(map[*Conn]struct{})}
}

// Register adds a connection for userID and returns its handle.
func (h *Hub) Register(conn *websocket.Conn, userID int) *Conn {
	c := &Conn{socket: conn, UserID: userID, out: make(chan []byte, 32)}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c
}

// Unregister removes a connection from the routing table.
func (h *Hub) Unregister(c *Conn) {
	if c == nil {
		return
	}
	h.mu.Lock()
	_, ok := h.clients[c]
	delete(h.clients, c)
	h.mu.Unlock()
	if ok {
		c.Close()
	}
}

// Count returns the number of live connections.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Broadcast encodes event and delivers it to every matching connection. An
// event with user_id 0 goes to all clients; otherwise only the owner receives
// it.
func (h *Hub) Broadcast(event Event) {
	if h == nil || event == nil {
		return
	}
	raw, err := json.Marshal(event)
	if err != nil {
		if h.log != nil {
			h.log.Warn("ws: marshal event", zap.Error(err))
		}
		return
	}
	target := eventUserID(event)

	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if target != BroadcastUserID && c.UserID != target {
			continue
		}
		if !c.Send(raw) {
			if h.log != nil {
				h.log.Warn("ws: client queue full, dropping frame", zap.Int("user_id", c.UserID))
			}
		}
	}
}

// Run subscribes to the bus and dispatches events until ctx is cancelled. It
// is intended to run in its own goroutine.
func (h *Hub) Run(ctx context.Context) error {
	if h == nil || h.bus == nil {
		return errors.New("workers: hub has no event bus")
	}
	events, cancel, err := h.bus.Subscribe(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			h.Broadcast(ev)
		}
	}
}

// eventUserID extracts the numeric user_id from a decoded event.
func eventUserID(event Event) int {
	switch v := event["user_id"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	}
	return BroadcastUserID
}
