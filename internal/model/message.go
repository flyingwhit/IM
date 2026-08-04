package model

import (
	"time"
)

// Cursor represents a pagination cursor for message history.
// It wraps a timestamp string in RFC 3339 format.
type Cursor string

// Time parses the cursor into a time.Time. If the cursor is empty or invalid,
// it returns the current time (which means "fetch up to now").
func (c Cursor) Time() time.Time {
	if c == "" {
		return time.Now()
	}
	t, err := time.Parse(time.RFC3339Nano, string(c))
	if err != nil {
		return time.Now()
	}
	return t
}

// NewCursor creates a Cursor from a time.Time.
func NewCursor(t time.Time) Cursor {
	return Cursor(t.Format(time.RFC3339Nano))
}

// MessageStatus represents the delivery state of a message.
type MessageStatus string

const (
	// MessageStatusSent means the message is persisted but not yet delivered
	// to the receiver (receiver is offline).
	MessageStatusSent MessageStatus = "sent"

	// MessageStatusDelivered means the message has been written to at least
	// one of the receiver's WebSocket connections.
	MessageStatusDelivered MessageStatus = "delivered"

	// MessageStatusRead means the receiver has explicitly marked the message
	// as read. Reserved for Phase 3.x — not yet implemented.
	MessageStatusRead MessageStatus = "read"
)

// Message represents a private message between two users.
type Message struct {
	ID          string        `json:"id"`
	SenderID    string        `json:"from"`
	ReceiverID  string        `json:"to"`
	Content     string        `json:"content"`
	ContentType string        `json:"content_type"`
	Status      MessageStatus `json:"status"`
	CreatedAt   time.Time     `json:"created_at"`
	// RecalledAt is set when the sender recalls the message.
	// nil means the message has not been recalled.
	RecalledAt *time.Time `json:"recalled_at,omitempty"`
}
