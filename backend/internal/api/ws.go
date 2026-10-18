package api

// WebSocket protocol for async SQL Lab queries.
//
// Endpoint
//
//	GET /api/v1/ws
//
// Authentication (first match wins)
//
//	1. Authorization: Bearer <access token>
//	2. ?token=<access token>   (browsers cannot set headers on a WebSocket)
//	3. access_token_cookie cookie
//
// Server -> client frames (JSON, all events are user scoped; user_id 0 is a
// broadcast that every client receives):
//
//	{"type":"connected","user_id":3}
//	{"type":"query_update","query_id":12,"status":"running","row_count":0,"duration_ms":0,"user_id":3}
//	{"type":"query_update","query_id":12,"status":"success","row_count":42,"duration_ms":17,"user_id":3}
//	{"type":"query_result","query_id":12,"status":"success","rowcount":42,"columns":[{"column_name":"id","name":"id","type":"INTEGER","is_dttm":false}],"results_path":"/api/v1/sqllab/12/results","user_id":3}
//	{"type":"error","query_id":12,"status":"failed","message":"syntax error at or near ...","user_id":3}
//	{"type":"pong","ts":1712345678901}
//	{"type":"subscribed","query_id":12}
//
// Client -> server frames (JSON):
//
//	{"type":"subscribe","query_id":12}   // acknowledges interest in a query id
//	{"type":"ping"}                      // answered with {"type":"pong"}
//
// The server sends a WebSocket ping every 30s and enforces a 10s write
// deadline; clients that stop reading are dropped. Status values mirror the
// query table: pending, running, success, failed, cancelled.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"
	"superset/internal/workers"
)

const (
	wsWriteWait  = 10 * time.Second
	wsPongWait   = 60 * time.Second
	wsPingPeriod = 30 * time.Second
	wsMaxMessage = 8 << 10
	wsCookieName = "access_token_cookie"
)

var wsUpgrader = websocket.Upgrader{
	HandshakeTimeout: 10 * time.Second,
	ReadBufferSize:   1024,
	WriteBufferSize:  1024,
	// The REST API is consumed by the separately hosted SPA, so origin
	// checking is delegated to the JWT check in authenticateWS.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// wsHub is the process-wide connection registry. RegisterWS is called once
// from RegisterAll, so a single hub is enough.
var (
	wsHubMu sync.Mutex
	wsHub   *workers.Hub
)

// RegisterWS mounts the async query WebSocket endpoints under /api/v1.
func RegisterWS(r chi.Router, deps *Deps) {
	hub := wsHubFor(deps)
	s := &wsAPI{deps: deps, hub: hub}
	r.Get("/ws", s.handle)
	r.Get("/ws/info", s.info)
}

// wsHubFor lazily builds the hub and starts its event loop.
func wsHubFor(deps *Deps) *workers.Hub {
	wsHubMu.Lock()
	defer wsHubMu.Unlock()
	if wsHub != nil {
		return wsHub
	}
	bus, err := workers.NewBus(deps.Config.RedisURL)
	if err != nil {
		deps.Log.Warn("ws: event bus degraded to in-process", zap.Error(err))
		bus, _ = workers.NewBus("")
	}
	hub := workers.NewHub(bus, deps.Log)
	go func() {
		if err := hub.Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
			deps.Log.Warn("ws: hub stopped", zap.Error(err))
		}
	}()
	wsHub = hub
	return hub
}

type wsAPI struct {
	deps *Deps
	hub  *workers.Hub
}

// publishLocalEvent delivers an event straight to connections held by this
// process; it is the fallback used when no event bus is available.
func publishLocalEvent(userID int, event map[string]any) {
	wsHubMu.Lock()
	hub := wsHub
	wsHubMu.Unlock()
	if hub == nil {
		return
	}
	ev := make(workers.Event, len(event)+1)
	for k, v := range event {
		ev[k] = v
	}
	ev["user_id"] = userID
	hub.Broadcast(ev)
}

type wsInbound struct {
	Type    string `json:"type"`
	QueryID int    `json:"query_id"`
}

func (s *wsAPI) info(w http.ResponseWriter, r *http.Request) {
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result": map[string]any{"connected_clients": s.hub.Count()},
	})
}

func (s *wsAPI) handle(w http.ResponseWriter, r *http.Request) {
	user, err := s.authenticateWS(r)
	if err != nil {
		server.WriteError(w, apperrors.Unauthorized("Invalid or missing token"))
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote an HTTP error response.
		return
	}
	defer conn.Close()

	client := s.hub.Register(conn, user.ID)
	defer s.hub.Unregister(client)

	connected, _ := json.Marshal(map[string]any{"type": "connected", "user_id": user.ID})
	client.Send(connected)

	go s.writePump(client)
	s.readPump(client)
}

// authenticateWS resolves the caller from the bearer header, the token query
// parameter or the session cookie, in that order.
func (s *wsAPI) authenticateWS(r *http.Request) (*security.AuthUser, error) {
	raw := wsBearerToken(r)
	if raw == "" {
		raw = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if raw == "" {
		if c, err := r.Cookie(wsCookieName); err == nil {
			raw = c.Value
		}
	}
	if raw == "" {
		return nil, errors.New("missing credentials")
	}
	claims, err := security.ParseToken(s.deps.Config.SecretKey, raw, security.TokenTypeAccess)
	if err != nil {
		return nil, err
	}
	var u models.User
	if err := s.deps.DB.WithContext(r.Context()).First(&u, claims.UserID).Error; err != nil {
		return nil, err
	}
	if !u.Active {
		return nil, errors.New("user is inactive")
	}
	return security.ToModel(&u, nil), nil
}

func wsBearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// readPump processes client frames until the socket closes.
func (s *wsAPI) readPump(c *workers.Conn) {
	socket := c.Socket()
	socket.SetReadLimit(wsMaxMessage)
	_ = socket.SetReadDeadline(time.Now().Add(wsPongWait))
	socket.SetPongHandler(func(string) error {
		return socket.SetReadDeadline(time.Now().Add(wsPongWait))
	})

	for {
		_, data, err := socket.ReadMessage()
		if err != nil {
			return
		}
		_ = socket.SetReadDeadline(time.Now().Add(wsPongWait))

		var msg wsInbound
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "ping":
			frame, _ := json.Marshal(map[string]any{
				"type": "pong",
				"ts":   time.Now().UnixMilli(),
			})
			c.Send(frame)
		case "subscribe":
			frame, _ := json.Marshal(map[string]any{
				"type":     "subscribed",
				"query_id": msg.QueryID,
			})
			c.Send(frame)
		}
	}
}

// writePump serialises frames for one connection and drives the heartbeat.
func (s *wsAPI) writePump(c *workers.Conn) {
	socket := c.Socket()
	ticker := time.NewTicker(wsPingPeriod)
	defer ticker.Stop()

	for {
		select {
		case frame, ok := <-c.Out():
			_ = socket.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if !ok {
				_ = socket.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := socket.WriteMessage(websocket.TextMessage, frame); err != nil {
				_ = socket.Close()
				return
			}
		case <-ticker.C:
			_ = socket.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := socket.WriteMessage(websocket.PingMessage, nil); err != nil {
				_ = socket.Close()
				return
			}
		}
	}
}
