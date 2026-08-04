package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/ciel/im/internal/service"
)

const (
	maxDevice = 3
)

// Upgrader configures the WebSocket upgrade handshake.
// CheckOrigin allows all origins during development — in production,
// this should be restricted to known domains.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // TODO: restrict in production
	},
}

// Handler upgrades HTTP connections to WebSocket and manages client lifecycle.
//
// Authentication: the client must pass a valid JWT access token as a query
// parameter: GET /ws?token=<jwt>. WebSocket doesn't support custom headers,
// and while Sec-WebSocket-Protocol can be abused for auth, query params are
// the simplest and most widely supported approach.
type Handler struct {
	authService *service.AuthService
	hub         *Hub
}

// NewHandler creates a WebSocket upgrade handler.
func NewHandler(authService *service.AuthService, hub *Hub) *Handler {
	return &Handler{
		authService: authService,
		hub:         hub,
	}
}

// Handle upgrades an HTTP connection to WebSocket.
//
// Flow:
//  1. Extract and validate JWT from query parameter
//  2. Upgrade HTTP → WebSocket
//  3. Create Client with unique connection ID
//  4. Register with Hub
//  5. Start readPump and writePump goroutines
//
// The handler returns immediately after starting the goroutines — the
// connection lifecycle is managed entirely by readPump/writePump.
func (h *Handler) Handle(c *gin.Context) {
	// Extract token from query string
	token := c.Query("token")
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing token query parameter"})
		return
	}

	// Validate the JWT access token (reuses Phase 1 auth logic)
	userID, err := h.authService.ValidateAccessToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
		return
	}

	connect := h.hub.Find(userID)
	if len(connect) >= maxDevice {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many devices"})
		return
	}

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("ws upgrade error: %v", err)
		// Response already sent by upgrader at this point
		return
	}

	// Generate a unique connection ID for this connection.
	// UUIDs allow distinguishing multiple connections from the same user.
	connID, err := generateConnID()
	if err != nil {
		log.Printf("ws connid error: %v", err)
		conn.Close()
		return
	}

	client := NewClient(h.hub, conn, userID, connID)

	// Register with Hub — this must happen before starting goroutines
	// so the client is findable as soon as readPump receives messages.
	h.hub.register <- client

	// Notify connection established (e.g., deliver offline messages).
	if h.hub.OnConnect != nil {
		go h.hub.OnConnect(userID)
	}

	// Launch goroutines. writePump must be launched first so it's
	// ready to receive messages before readPump starts processing.
	go client.writePump()
	go client.readPump()

	log.Printf("ws: user %s connected (conn=%s)", userID, connID)
}

// generateConnID creates a random hex connection identifier.
func generateConnID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
