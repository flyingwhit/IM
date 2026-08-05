package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ciel/im/internal/ws"
)

func TestClient_Send(t *testing.T) {
	hub := NewHub(nil, nil)
	client := newTestClient(hub, "user-1", "conn-a")

	env := ws.MustEnvelope(ws.TypeMessageNew, ws.MessageNewPayload{
		ID:      "msg-1",
		From:    "user-2",
		Content: "hello",
	})
	client.Send(env)

	// Verify the message was enqueued on the send channel.
	select {
	case data := <-client.send:
		// Verify it's valid JSON with the correct fields.
		var received ws.Envelope
		if err := json.Unmarshal(data, &received); err != nil {
			t.Fatalf("failed to unmarshal sent data: %v", err)
		}
		if received.Type != ws.TypeMessageNew {
			t.Errorf("expected type %s, got %s", ws.TypeMessageNew, received.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected message on send channel, timed out")
	}
}

func TestClient_sendRaw(t *testing.T) {
	hub := NewHub(nil, nil)
	client := newTestClient(hub, "user-1", "conn-a")

	raw := []byte(`{"type":"pong"}`)
	client.sendRaw(raw)

	select {
	case data := <-client.send:
		if string(data) != string(raw) {
			t.Errorf("expected '%s', got '%s'", string(raw), string(data))
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected raw data on send channel, timed out")
	}
}

func TestClient_Send_BufferFull(t *testing.T) {
	hub := NewHub(nil, nil)
	client := newTestClient(hub, "user-1", "conn-a")

	env := ws.MustEnvelope(ws.TypeMessageNew, ws.MessageNewPayload{
		ID:      "msg",
		From:    "user-2",
		Content: "x",
	})

	// Fill the buffer to capacity (sendBufferSize = 256).
	for i := 0; i < sendBufferSize; i++ {
		client.Send(env)
	}

	// Buffer is now full. This send should be dropped (non-blocking).
	// It must not block — if it did, the test would hang.
	client.Send(env)

	// Drain the buffer — should be exactly sendBufferSize messages.
	// The overflow message should have been silently dropped.
	count := 0
	for {
		select {
		case <-client.send:
			count++
		default:
			goto drained
		}
	}
drained:
	if count != sendBufferSize {
		t.Errorf("expected %d messages (buffer capacity), got %d (overflow message was not dropped)",
			sendBufferSize, count)
	}
}
