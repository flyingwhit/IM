package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ciel/im/internal/broker"
	"github.com/ciel/im/internal/ws"
)

// --- Cross-instance delivery ---

func TestHub_HandleCrossInstanceMessage_DeliversToTarget(t *testing.T) {
	hub := NewHub(nil, nil)
	cancel := startHub(t, hub)
	defer cancel()

	client := newTestClient(hub, "user-1", "conn-a")
	hub.register <- client
	waitRun()

	env := ws.MustEnvelope(ws.TypeMessageNew, ws.MessageNewPayload{
		ID:      "msg-1",
		From:    "user-2",
		Content: "cross-instance hello",
	})
	data, _ := json.Marshal(env)

	msg := &broker.CrossMessage{
		SourceInstance: "gw-other",
		TargetUser:     "user-1",
		Envelope:       data,
	}
	hub.handleCrossInstanceMessage(msg)

	// Message should be delivered to the local client.
	select {
	case <-client.send:
		// ok
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected cross-instance message on client send channel")
	}
}

func TestHub_HandleCrossInstanceMessage_UnknownUser(t *testing.T) {
	hub := NewHub(nil, nil)
	cancel := startHub(t, hub)
	defer cancel()

	env := ws.MustEnvelope(ws.TypeMessageNew, ws.MessageNewPayload{
		ID:      "msg-1",
		From:    "user-2",
		Content: "hello",
	})
	data, _ := json.Marshal(env)

	msg := &broker.CrossMessage{
		SourceInstance: "gw-other",
		TargetUser:     "nonexistent",
		Envelope:       data,
	}
	// Should not panic when user is not connected.
	hub.handleCrossInstanceMessage(msg)
}

// TestHub_SendToUser_ClosedChannelRace verifies that SendToUser does not panic
// when a client disconnects (its send channel is closed) during delivery.
// This is a regression test for a race condition between SendToUser and
// client unregistration.
func TestHub_SendToUser_ClosedChannelRace(t *testing.T) {
	hub := NewHub(nil, nil)
	cancel := startHub(t, hub)
	defer cancel()

	client := newTestClient(hub, "user-1", "conn-a")
	hub.register <- client
	waitRun()

	env := ws.MustEnvelope(ws.TypeMessageNew, ws.MessageNewPayload{
		ID:      "msg-1",
		From:    "user-2",
		Content: "hello",
	})

	// Simulate client disconnect: close the send channel first
	// (as readPump defers close(c.send)), then trigger SendToUser.
	close(client.send)

	// This should NOT panic. If it panics, the test will fail via the
	// testing framework's recovery mechanism.
	hub.SendToUser("user-1", env)
}

// TestHub_HandleCrossInstanceMessage_ClosedChannelRace is the cross-instance
// variant of the closed-channel race test.
func TestHub_HandleCrossInstanceMessage_ClosedChannelRace(t *testing.T) {
	hub := NewHub(nil, nil)
	cancel := startHub(t, hub)
	defer cancel()

	client := newTestClient(hub, "user-1", "conn-a")
	hub.register <- client
	waitRun()

	env := ws.MustEnvelope(ws.TypeMessageNew, ws.MessageNewPayload{
		ID:      "msg-1",
		From:    "user-2",
		Content: "hello",
	})
	data, _ := json.Marshal(env)

	// Simulate client disconnect: close the send channel.
	close(client.send)

	msg := &broker.CrossMessage{
		SourceInstance: "gw-other",
		TargetUser:     "user-1",
		Envelope:       data,
	}
	// This should NOT panic.
	hub.handleCrossInstanceMessage(msg)
}
