package broker

import (
	"encoding/json"
	"testing"
)

func TestCrossMessage_RoundTrip(t *testing.T) {
	env := json.RawMessage(`{"type":"message.new","payload":{"id":"msg-1","from":"alice","content":"hello"}}`)
	original := CrossMessage{
		SourceInstance: "gw-abc123",
		TargetUser:     "bob",
		Envelope:       env,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded CrossMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.SourceInstance != original.SourceInstance {
		t.Errorf("SourceInstance = %s, want %s", decoded.SourceInstance, original.SourceInstance)
	}
	if decoded.TargetUser != original.TargetUser {
		t.Errorf("TargetUser = %s, want %s", decoded.TargetUser, original.TargetUser)
	}
	if string(decoded.Envelope) != string(original.Envelope) {
		t.Errorf("Envelope = %s, want %s", string(decoded.Envelope), string(original.Envelope))
	}
}

func TestCrossMessage_ShortFieldNames(t *testing.T) {
	// CrossMessage uses short JSON field names (si, tu, env) to minimize
	// wire overhead on Redis Pub/Sub. Verify the tags are correct.
	msg := CrossMessage{
		SourceInstance: "gw-01",
		TargetUser:     "user-42",
		Envelope:       json.RawMessage(`{"key":"value"}`),
	}
	data, _ := json.Marshal(msg)

	// The JSON should contain the short field names.
	str := string(data)
	if !json.Valid([]byte(str)) {
		t.Fatal("marshal produced invalid JSON")
	}

	var raw map[string]json.RawMessage
	json.Unmarshal(data, &raw)

	if _, ok := raw["si"]; !ok {
		t.Error("expected short field name 'si' for SourceInstance")
	}
	if _, ok := raw["tu"]; !ok {
		t.Error("expected short field name 'tu' for TargetUser")
	}
	if _, ok := raw["env"]; !ok {
		t.Error("expected short field name 'env' for Envelope")
	}
	// Long names should NOT be present.
	if _, ok := raw["source_instance"]; ok {
		t.Error("should not have 'source_instance' in JSON output")
	}
}

func TestDefaultChannel(t *testing.T) {
	if DefaultChannel != "im:deliver" {
		t.Errorf("DefaultChannel = %s, want im:deliver", DefaultChannel)
	}
}

func TestNewBroker(t *testing.T) {
	b := New(nil, "gw-test")
	if b == nil {
		t.Fatal("New returned nil")
	}
	if b.InstanceID() != "gw-test" {
		t.Errorf("InstanceID = %s, want gw-test", b.InstanceID())
	}
	if len(b.subs) != 0 {
		t.Errorf("expected 0 subscriptions, got %d", len(b.subs))
	}
}

func TestBroker_Close_Empty(t *testing.T) {
	b := New(nil, "gw-test")
	if err := b.Close(); err != nil {
		t.Errorf("Close on empty broker: %v", err)
	}
}
