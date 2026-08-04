// Package ws defines the WebSocket message protocol between client and server.
//
// All messages use JSON encoding. The envelope format is:
//
//	{"type": "<message_type>", "payload": {...}}
//
// This is intentionally simple — a learning project benefits from debuggable
// protocols. Protobuf or msgpack would be more efficient but harder to inspect.
package ws

import "encoding/json"

// MessageType identifies the kind of WebSocket message.
type MessageType string

const (
	// Client → Server
	TypeMessageSend   MessageType = "message.send"   // send a private message
	TypeMessageRecall MessageType = "message.recall"  // recall (撤回) a sent message
	TypePing          MessageType = "ping"            // heartbeat

	// Server → Client
	TypeMessageNew      MessageType = "message.new"      // incoming message from another user
	TypeMessageAck      MessageType = "message.ack"      // delivery confirmation
	TypeMessageRecalled MessageType = "message.recalled" // notification that a message was recalled
	TypePong            MessageType = "pong"             // heartbeat response
	TypeError           MessageType = "error"            // server-side error
)

// Envelope is the top-level structure for every WebSocket message.
// Payload is kept as RawMessage so handlers can unmarshal into the
// appropriate type based on the Type field.
type Envelope struct {
	Type    MessageType     `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// --- Client → Server payloads ---

// MessageSendPayload is sent by a client to deliver a private message.
type MessageSendPayload struct {
	To          string `json:"to"`                     // receiver user ID
	Content     string `json:"content"`                // message body
	ContentType string `json:"content_type,omitempty"` // "text" (default), "image", "file"
}

// MessageRecallPayload is sent by a client to recall a previously sent message.
type MessageRecallPayload struct {
	MessageID string `json:"message_id"` // ID of the message to recall
}

// --- Server → Client payloads ---

// MessageNewPayload is sent to the receiver when a new message arrives.
type MessageNewPayload struct {
	ID          string `json:"id"`
	From        string `json:"from"` // sender user ID
	Content     string `json:"content"`
	ContentType string `json:"content_type,omitempty"`
	CreatedAt   string `json:"created_at"` // ISO 8601
}

// MessageAckPayload confirms delivery status back to the sender.
type MessageAckPayload struct {
	ID     string `json:"id"`
	Status string `json:"status"` // "delivered" or "error"
}

// MessageRecalledPayload is broadcast to both sender and receiver when a
// message is successfully recalled.
type MessageRecalledPayload struct {
	MessageID  string `json:"message_id"`
	RecalledAt string `json:"recalled_at"` // ISO 8601
}

// ErrorPayload carries server-side error details.
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// --- Helpers ---

// NewEnvelope marshals a payload into an Envelope.
func NewEnvelope(t MessageType, payload any) (*Envelope, error) {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	return &Envelope{Type: t, Payload: raw}, nil
}

// MustEnvelope is like NewEnvelope but panics on error.
// Only use when the payload is known to be marshalable (e.g. static strings).
func MustEnvelope(t MessageType, payload any) *Envelope {
	e, err := NewEnvelope(t, payload)
	if err != nil {
		panic(err)
	}
	return e
}
