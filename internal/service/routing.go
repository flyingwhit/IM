package service

import "github.com/ciel/im/internal/ws"

// MessageRouter abstracts the WebSocket routing layer for MessageService.
// It is implemented by gateway.Hub through Go's structural typing —
// gateway.Hub does not need to import this package.
type MessageRouter interface {
	// SendToUser delivers an envelope to all active connections of a user.
	// If the user has no connections, this is a no-op.
	SendToUser(userID string, env *ws.Envelope)

	// IsOnline returns whether a user has at least one active connection.
	IsOnline(userID string) bool
}
