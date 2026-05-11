package ws

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"gossh/internal/domain"

	"github.com/gorilla/websocket"
)

type Hub struct {
	logger   *slog.Logger
	upgrader websocket.Upgrader

	mu      sync.RWMutex
	clients map[string]map[*websocket.Conn]string
}

func NewHub(logger *slog.Logger, _ int) *Hub {
	return &Hub{
		logger: logger.With("component", "ws-hub"),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool { return true },
		},
		clients: make(map[string]map[*websocket.Conn]string),
	}
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request, roomID, peer string, onDisconnect func()) (joined bool) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Warn("websocket upgrade failed", "err", err)
		return false
	}
	h.add(roomID, conn, peer)

	go func() {
		defer h.remove(roomID, conn)
		defer conn.Close()
		defer func() {
			if onDisconnect != nil {
				onDisconnect()
			}
		}()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	return true
}

func (h *Hub) Broadcast(roomID string, event domain.RoomEvent) {
	payload, err := json.Marshal(event)
	if err != nil {
		h.logger.Warn("marshal websocket event failed", "err", err)
		return
	}

	h.mu.RLock()
	roomClients := h.clients[roomID]
	connections := make([]*websocket.Conn, 0, len(roomClients))
	for conn := range roomClients {
		connections = append(connections, conn)
	}
	h.mu.RUnlock()

	for _, conn := range connections {
		if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			h.remove(roomID, conn)
			_ = conn.Close()
		}
	}
}

func (h *Hub) OnlineCount(roomID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients[roomID])
}

func (h *Hub) add(roomID string, conn *websocket.Conn, peer string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[roomID]; !ok {
		h.clients[roomID] = make(map[*websocket.Conn]string)
	}
	h.clients[roomID][conn] = peer
}

func (h *Hub) remove(roomID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if roomClients, ok := h.clients[roomID]; ok {
		delete(roomClients, conn)
		if len(roomClients) == 0 {
			delete(h.clients, roomID)
		}
	}
}
