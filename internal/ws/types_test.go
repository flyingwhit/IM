package ws

import (
	"encoding/json"
	"testing"
)

func TestNewEnvelope(t *testing.T) {
	payload := MessageSendPayload{
		To:      "user-456",
		Content: "hello",
	}
	env, err := NewEnvelope(TypeMessageSend, payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Type != TypeMessageSend {
		t.Errorf("expected type %s, got %s", TypeMessageSend, env.Type)
	}
	if env.Payload == nil {
		t.Error("expected non-nil payload")
	}

	// Verify the payload can be unmarshaled back
	var decoded MessageSendPayload
	if err := json.Unmarshal(env.Payload, &decoded); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if decoded.To != payload.To {
		t.Errorf("expected To=%s, got %s", payload.To, decoded.To)
	}
	if decoded.Content != payload.Content {
		t.Errorf("expected Content=%s, got %s", payload.Content, decoded.Content)
	}
}

func TestNewEnvelope_NilPayload(t *testing.T) {
	env, err := NewEnvelope(TypePing, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Type != TypePing {
		t.Errorf("expected type %s, got %s", TypePing, env.Type)
	}
	if env.Payload != nil {
		t.Errorf("expected nil payload, got %s", string(env.Payload))
	}
}

func TestMustEnvelope(t *testing.T) {
	env := MustEnvelope(TypePong, nil)
	if env.Type != TypePong {
		t.Errorf("expected type %s, got %s", TypePong, env.Type)
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	// Simulate: server creates an envelope, marshals to JSON (writePump),
	// client unmarshals it back.
	original := MessageNewPayload{
		ID:          "msg-123",
		From:        "user-abc",
		Content:     "hello world",
		ContentType: "text",
		CreatedAt:   "2026-08-01T10:00:00Z",
	}
	env, _ := NewEnvelope(TypeMessageNew, original)

	// Marshal to JSON (simulates writePump sending over the wire)
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	// Unmarshal (simulates client receiving)
	var received Envelope
	if err := json.Unmarshal(data, &received); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if received.Type != TypeMessageNew {
		t.Errorf("expected type %s, got %s", TypeMessageNew, received.Type)
	}

	var decoded MessageNewPayload
	if err := json.Unmarshal(received.Payload, &decoded); err != nil {
		t.Fatalf("payload unmarshal error: %v", err)
	}
	if decoded.ID != original.ID {
		t.Errorf("expected ID=%s, got %s", original.ID, decoded.ID)
	}
	if decoded.From != original.From {
		t.Errorf("expected From=%s, got %s", original.From, decoded.From)
	}
}

func TestRecallRoundTrip(t *testing.T) {
	// Simulate recall request and notification round-trip.
	recallPayload := MessageRecallPayload{MessageID: "msg-abc"}
	req, _ := NewEnvelope(TypeMessageRecall, recallPayload)
	data, _ := json.Marshal(req)

	var received Envelope
	if err := json.Unmarshal(data, &received); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if received.Type != TypeMessageRecall {
		t.Errorf("expected %s, got %s", TypeMessageRecall, received.Type)
	}
	var decoded MessageRecallPayload
	if err := json.Unmarshal(received.Payload, &decoded); err != nil {
		t.Fatalf("payload unmarshal error: %v", err)
	}
	if decoded.MessageID != "msg-abc" {
		t.Errorf("expected msg-abc, got %s", decoded.MessageID)
	}
}

func TestMessageTypeConstants(t *testing.T) {
	// Ensure message type constants are distinct — a typo could make two
	// types equal, which would break type-switch dispatch in message handlers.
	seen := make(map[MessageType]bool)
	types := []MessageType{
		TypeMessageSend,
		TypeMessageRecall,
		TypePing,
		TypeMessageNew,
		TypeMessageAck,
		TypeMessageRecalled,
		TypePong,
		TypeError,
	}
	for _, mt := range types {
		if seen[mt] {
			t.Errorf("duplicate message type: %s", mt)
		}
		seen[mt] = true
	}
}
