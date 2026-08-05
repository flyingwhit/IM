package gateway

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/ciel/im/internal/broker"
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

	// broker handles cross-instance message routing via Redis Pub/Sub.
	// When nil (tests, single-instance deployment), cross-instance routing
	// is disabled and the Hub operates in standalone mode.
	broker *broker.Broker
}

// NewHub creates a Hub ready to run.
// presence may be nil — the Hub works without Redis, but online status
// will not be persisted externally.
// broker may be nil — when nil, cross-instance routing is disabled and
// the Hub operates in standalone mode.
func NewHub(presence *redisrepo.PresenceRepo, b *broker.Broker) *Hub {
	return &Hub{
		clients:    make(map[string]map[string]*Client),
		register:   make(chan *Client, 64),
		unregister: make(chan *Client, 64),
		presence:   presence,
		broker:     b,
	}
}

// Run starts the Hub event loop. It blocks until ctx is done, so it
// should be launched in its own goroutine.
//
// All register/unregister mutations happen on this single goroutine,
// eliminating data races on the clients map without holding a lock
// during channel operations.
//
// If a broker is configured, Run also subscribes to the cross-instance
// delivery channel. Incoming cross-instance messages are delivered to
// locally connected users.
func (h *Hub) Run(ctx context.Context) {
	// Start cross-instance message subscriber.
	if h.broker != nil {
		h.broker.Subscribe(ctx, broker.DefaultChannel, h.handleCrossInstanceMessage)
	}

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
// It checks local connections first (fast path), then falls back to
// Redis presence for cross-instance awareness. A user connected to
// any gateway instance is considered online.
func (h *Hub) IsOnline(userID string) bool {
	// Fast path: check local connections (no network call).
	h.mu.RLock()
	if len(h.clients[userID]) > 0 {
		h.mu.RUnlock()
		return true
	}
	h.mu.RUnlock()

	// Slow path: check Redis for users on other instances.
	if h.presence != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		online, err := h.presence.IsOnline(ctx, userID)
		if err != nil {
			return false // assume offline on error
		}
		return online
	}
	return false
}

// SendToUser delivers a message envelope to all active connections of a user.
// It marshals once and sends the same bytes to every connection via sendRaw,
// avoiding duplicate JSON encoding work when a user has multiple connections.
//
// Each send is non-blocking: if a client's send buffer is full, the message
// is dropped for that connection (the message is already persisted in DB).
//
// If a broker is configured, SendToUser also publishes the message for
// cross-instance delivery. Other gateway instances pick it up and deliver
// to locally connected users. The source instance skips its own publications
// to avoid double-delivery.
func (h *Hub) SendToUser(userID string, env *ws.Envelope) {
	data, err := json.Marshal(env)
	if err != nil {
		log.Printf("hub: marshal error for user %s: %v", userID, err)
		return
	}

	// 1. Local delivery.
	for _, c := range h.Find(userID) {
		c.sendRaw(data)
	}

	// 2. Cross-instance delivery via broker.
	// Even if the user was found and delivered locally, we publish —
	// the user may have connections on other instances (multi-device).
	if h.broker != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := h.broker.Publish(ctx, broker.DefaultChannel, userID, data); err != nil {
			log.Printf("hub: broker publish error for user %s: %v", userID, err)
		}
	}
}

// handleCrossInstanceMessage delivers a message from another gateway instance
// to a locally connected user. It is the callback for broker.Subscribe.
//
// The source instance already delivered locally — we only need to deliver
// to our local connections. The broker already filters out messages from
// this instance, so we don't double-deliver.
func (h *Hub) handleCrossInstanceMessage(msg *broker.CrossMessage) {
	h.mu.RLock()
	conns := h.clients[msg.TargetUser]
	if len(conns) == 0 {
		h.mu.RUnlock()
		return
	}
	// Copy client pointers while holding the read lock, then deliver outside.
	clients := make([]*Client, 0, len(conns))
	for _, c := range conns {
		clients = append(clients, c)
	}
	h.mu.RUnlock()

	for _, c := range clients {
		c.sendRaw(msg.Envelope)
	}
}
