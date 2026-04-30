package ws

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type Hub struct {
	upgrader websocket.Upgrader
	mu       sync.RWMutex
	clients  map[*websocket.Conn]struct{}
}

func NewHub() *Hub {
	return &Hub{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		clients: make(map[*websocket.Conn]struct{}),
	}
}

func (h *Hub) Handle(c *gin.Context) {
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	h.mu.Lock()
	h.clients[conn] = struct{}{}
	h.mu.Unlock()

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			h.mu.Lock()
			delete(h.clients, conn)
			h.mu.Unlock()
			_ = conn.Close()
			return
		}
	}
}

func (h *Hub) Broadcast(v any) {
	body, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.mu.RLock()
	bad := make([]*websocket.Conn, 0)
	for conn := range h.clients {
		if err := conn.WriteMessage(websocket.TextMessage, body); err != nil {
			bad = append(bad, conn)
		}
	}
	h.mu.RUnlock()

	if len(bad) == 0 {
		return
	}
	h.mu.Lock()
	for _, conn := range bad {
		_ = conn.Close()
		delete(h.clients, conn)
	}
	h.mu.Unlock()
}
