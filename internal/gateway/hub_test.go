package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/ciel/im/internal/ws"
)

// newTestClient creates a Client for testing without a real WebSocket connection.
// The conn field is nil — this is safe because Hub tests only exercise the
// registry (register/unregister/Find/IsOnline/SendToUser), none of which
// touch the underlying connection.
func newTestClient(hub *Hub, userID, connID string) *Client {
	return NewClient(hub, nil, userID, connID)
}

// startHub starts Hub.Run() in a goroutine and returns a cancel function
// to stop it. The caller should defer cancel().
func startHub(t *testing.T, hub *Hub) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	return cancel
}

// waitRun gives Hub.Run() a scheduling window to process pending channel
// operations. The buffered channels make sends non-blocking, but Run() still
// needs CPU time to update the clients map.
func waitRun() {
	time.Sleep(10 * time.Millisecond)
}

// --- Register & Find ---

func TestHub_RegisterAndFind(t *testing.T) {
	hub := NewHub(nil)
	cancel := startHub(t, hub)
	defer cancel()

	client1 := newTestClient(hub, "user-1", "conn-a")
	hub.register <- client1
	waitRun()

	found := hub.Find("user-1")
	if len(found) != 1 {
		t.Fatalf("expected 1 client, got %d", len(found))
	}
	if found[0].userID != "user-1" {
		t.Errorf("expected userID 'user-1', got '%s'", found[0].userID)
	}
	if found[0].connID != "conn-a" {
		t.Errorf("expected connID 'conn-a', got '%s'", found[0].connID)
	}
}

func TestHub_RegisterMultiple_SameUser(t *testing.T) {
	hub := NewHub(nil)
	cancel := startHub(t, hub)
	defer cancel()

	hub.register <- newTestClient(hub, "user-1", "conn-a")
	hub.register <- newTestClient(hub, "user-1", "conn-b")
	waitRun()

	found := hub.Find("user-1")
	if len(found) != 2 {
		t.Fatalf("expected 2 clients for same user, got %d", len(found))
	}
}

func TestHub_RegisterMultiple_DifferentUsers(t *testing.T) {
	hub := NewHub(nil)
	cancel := startHub(t, hub)
	defer cancel()

	hub.register <- newTestClient(hub, "user-1", "conn-a")
	hub.register <- newTestClient(hub, "user-2", "conn-b")
	waitRun()

	if len(hub.Find("user-1")) != 1 {
		t.Error("user-1 should have 1 client")
	}
	if len(hub.Find("user-2")) != 1 {
		t.Error("user-2 should have 1 client")
	}
}

func TestHub_Find_NotFound(t *testing.T) {
	hub := NewHub(nil)
	cancel := startHub(t, hub)
	defer cancel()

	found := hub.Find("nonexistent")
	if found != nil {
		t.Errorf("expected nil for nonexistent user, got %v", found)
	}
}

// --- Unregister ---

func TestHub_Unregister(t *testing.T) {
	hub := NewHub(nil)
	cancel := startHub(t, hub)
	defer cancel()

	client := newTestClient(hub, "user-1", "conn-a")
	hub.register <- client
	waitRun()

	hub.unregister <- client
	waitRun()

	if hub.Find("user-1") != nil {
		t.Error("expected no clients after unregister")
	}
}

func TestHub_Unregister_LastConnectionClearsUser(t *testing.T) {
	hub := NewHub(nil)
	cancel := startHub(t, hub)
	defer cancel()

	clientA := newTestClient(hub, "user-1", "conn-a")
	clientB := newTestClient(hub, "user-1", "conn-b")
	hub.register <- clientA
	hub.register <- clientB
	waitRun()

	// Remove one connection — user should still have one left.
	hub.unregister <- clientA
	waitRun()

	if !hub.IsOnline("user-1") {
		t.Error("user-1 should still be online after removing one connection")
	}

	// Remove the last connection — user should be gone entirely.
	hub.unregister <- clientB
	waitRun()

	if hub.IsOnline("user-1") {
		t.Error("user-1 should be offline after removing last connection")
	}
	if hub.Find("user-1") != nil {
		t.Error("Find should return nil when user has no connections")
	}
}

func TestHub_Unregister_Nonexistent(t *testing.T) {
	hub := NewHub(nil)
	cancel := startHub(t, hub)
	defer cancel()

	client := newTestClient(hub, "user-1", "conn-a")
	// Unregister without registering first — should not panic.
	hub.unregister <- client
	waitRun()

	if hub.Find("user-1") != nil {
		t.Error("Find should still return nil")
	}
}

// --- IsOnline ---

func TestHub_IsOnline(t *testing.T) {
	hub := NewHub(nil)
	cancel := startHub(t, hub)
	defer cancel()

	if hub.IsOnline("user-1") {
		t.Error("user should not be online before registration")
	}

	hub.register <- newTestClient(hub, "user-1", "conn-a")
	waitRun()

	if !hub.IsOnline("user-1") {
		t.Error("user should be online after registration")
	}
}

// --- SendToUser ---

func TestHub_SendToUser(t *testing.T) {
	hub := NewHub(nil)
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
	hub.SendToUser("user-1", env)

	// Message should be on the client's send channel.
	select {
	case data := <-client.send:
		if len(data) == 0 {
			t.Error("expected non-empty message data")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected message on send channel, timed out")
	}
}

func TestHub_SendToUser_MultipleConnections(t *testing.T) {
	hub := NewHub(nil)
	cancel := startHub(t, hub)
	defer cancel()

	clientA := newTestClient(hub, "user-1", "conn-a")
	clientB := newTestClient(hub, "user-1", "conn-b")
	hub.register <- clientA
	hub.register <- clientB
	waitRun()

	env := ws.MustEnvelope(ws.TypeMessageNew, ws.MessageNewPayload{
		ID:      "msg-1",
		From:    "user-2",
		Content: "hello",
	})
	hub.SendToUser("user-1", env)

	// Both connections should receive the message.
	for i, c := range []*Client{clientA, clientB} {
		select {
		case <-c.send:
			// ok
		case <-time.After(100 * time.Millisecond):
			t.Errorf("client %d (%s) did not receive message", i, c.connID)
		}
	}
}

func TestHub_SendToUser_OfflineUser(t *testing.T) {
	hub := NewHub(nil)
	cancel := startHub(t, hub)
	defer cancel()

	// Send to a user with no connections — should not panic.
	env := ws.MustEnvelope(ws.TypeMessageNew, ws.MessageNewPayload{
		ID:      "msg-1",
		From:    "user-2",
		Content: "hello",
	})
	hub.SendToUser("offline-user", env)
	// Test passes if no panic occurs.
}

// --- Run shutdown ---

func TestHub_Run_Shutdown(t *testing.T) {
	hub := NewHub(nil)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		hub.Run(ctx)
		close(done)
	}()

	// Register a client to verify Run is processing.
	hub.register <- newTestClient(hub, "user-1", "conn-a")
	waitRun()

	// Cancel the context — Run should return.
	cancel()

	select {
	case <-done:
		// Run exited cleanly.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Hub.Run did not exit after context cancellation")
	}
}
