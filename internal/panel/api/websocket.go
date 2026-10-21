package api

import (
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// The websocket carries the state to the open pages: the panel pushes it at
// every change, and the page answers with a ping to keep the connection
// alive.
type hub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]bool
}

// newHub returns an empty hub.
func newHub() *hub {
	return &hub{clients: map[*websocket.Conn]bool{}}
}

// send tells every open page something.
func (h *hub) send(message any) {
	h.mu.Lock()
	clients := make([]*websocket.Conn, 0, len(h.clients))
	for conn := range h.clients {
		clients = append(clients, conn)
	}
	h.mu.Unlock()
	for _, conn := range clients {
		if err := conn.WriteJSON(message); err != nil {
			conn.Close()
			h.drop(conn)
		}
	}
}

// drop forgets a page.
func (h *hub) drop(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, conn)
}

// the upgrader of the websocket: the panel is served over the same origin,
// and the page comes from the panel itself.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// websocket is the connection the pages listen on.
func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil || !s.auth.Verify(sessionToken(r)) {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.hub.mu.Lock()
	s.hub.clients[conn] = true
	s.hub.mu.Unlock()
	defer func() {
		s.hub.drop(conn)
		conn.Close()
	}()
	// the page gets the state at once
	conn.WriteJSON(map[string]any{"type": "state", "state": s.fullState()})
	for {
		// the keepalive pings of the page
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

// Broadcast tells the open pages that something changed.
func (s *Server) Broadcast(message map[string]any) {
	if message == nil {
		s.hub.send(map[string]any{"type": "state", "state": s.fullState()})
		return
	}
	if message["type"] == "state" {
		s.hub.send(map[string]any{"type": "state", "state": s.fullState()})
		return
	}
	s.hub.send(message)
}
