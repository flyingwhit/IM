package gateway

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	redisrepo "github.com/ciel/im/internal/repository/redis"
	"github.com/ciel/im/internal/ws"
)

// Hub manages all active WebSocket connections.
//
// It is the central registry: clients register on connect, unregister on
// disconnect, and Find locates all connections for a given user for message
// routing.
//
// Concurrency:
//   - register/unregister go through channels serialized by Run()
//   - Find uses a RWMutex so concurrent reads don't block each other
//   - The RWMutex only protects the map pointer swap in register/unregister,
//     not the channel operations
type Hub struct {
	// clients maps userID → connID → Client.
	clients map[string]map[string]*Client

	register   chan *Client
	unregister chan *Client

	mu sync.RWMutex

	// presence tracks online/offline state in Redis.
	// May be nil (tests, or when Redis is unavailable).
	presence *redisrepo.PresenceRepo

	// OnMessage is called when a client sends a message.send frame.
	// The first argument is the sender's userID, the second is the raw
	// JSON message bytes. Set externally by the composition root.
	OnMessage func(userID string, raw []byte)

	// OnConnect is called after a new WebSocket connection is registered
	// with the Hub. Use it for offline message delivery or other
	// connection-time logic. Set externally by the composition root.
	OnConnect func(userID string)
}

// NewHub creates a Hub ready to run.
// presence may be nil — the Hub works without Redis, but online status
// will not be persisted externally.
func NewHub(presence *redisrepo.PresenceRepo) *Hub {
	return &Hub{
		clients:    make(map[string]map[string]*Client),
		register:   make(chan *Client, 64),
		unregister: make(chan *Client, 64),
		presence:   presence,
	}
}

// Run starts the Hub event loop. It blocks until ctx is done, so it
// should be launched in its own goroutine.
//
// All register/unregister mutations happen on this single goroutine,
// eliminating data races on the clients map without holding a lock
// during channel operations.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case client := <-h.register:
			h.mu.Lock()
			isFirst := h.clients[client.userID] == nil
			if isFirst {
				h.clients[client.userID] = make(map[string]*Client)
			}
			h.clients[client.userID][client.connID] = client
			h.mu.Unlock()

			if isFirst && h.presence != nil {
				// Bound Redis calls so a slow/broken Redis doesn't
				// stall the Hub event loop.
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				if err := h.presence.SetOnline(ctx, client.userID); err != nil {
					log.Printf("hub: set online for user %s: %v", client.userID, err)
				}
				cancel()
			}
			log.Printf("hub: client %s registered for user %s", client.connID, client.userID)

		case client := <-h.unregister:
			h.mu.Lock()
			var becameOffline bool
			if conns, ok := h.clients[client.userID]; ok {
				delete(conns, client.connID)
				if len(conns) == 0 {
					delete(h.clients, client.userID)
					becameOffline = true
				}
			}
			h.mu.Unlock()

			if becameOffline && h.presence != nil {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				if err := h.presence.SetOffline(ctx, client.userID); err != nil {
					log.Printf("hub: set offline for user %s: %v", client.userID, err)
				}
				cancel()
			}
			log.Printf("hub: client %s unregistered for user %s", client.connID, client.userID)
		}
	}
}

func (h *Hub) OnlineCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *Hub) RefreshPresence(userID string) {
	if h.presence == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.presence.Refresh(ctx, userID); err != nil {
		log.Printf("hub: refresh presence for user %s: %v", userID, err)
	}
}

// Find returns all active connections for a user.
// Returns nil if the user has no active connections.
func (h *Hub) Find(userID string) []*Client {
	h.mu.RLock()
	defer h.mu.RUnlock()

	conns := h.clients[userID]
	if len(conns) == 0 {
		return nil
	}

	result := make([]*Client, 0, len(conns))
	for _, c := range conns {
		result = append(result, c)
	}
	return result
}

// IsOnline returns whether a user has at least one active connection.
func (h *Hub) IsOnline(userID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients[userID]) > 0
}

// SendToUser delivers a message envelope to all active connections of a user.
// It marshals once and sends the same bytes to every connection via sendRaw,
// avoiding duplicate JSON encoding work when a user has multiple connections.
//
// Each send is non-blocking: if a client's send buffer is full, the message
// is dropped for that connection (the message is already persisted in DB).
func (h *Hub) SendToUser(userID string, env *ws.Envelope) {
	data, err := json.Marshal(env)
	if err != nil {
		log.Printf("hub: marshal error for user %s: %v", userID, err)
		return
	}

	for _, c := range h.Find(userID) {
		c.sendRaw(data)
	}
}
