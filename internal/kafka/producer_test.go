package kafka

import (
	"context"
	"testing"
)

func TestProducer_NilSafety(t *testing.T) {
	// Publish on a nil producer should not panic.
	var p *Producer
	p.Publish(context.TODO(), &MessageEvent{
		Type:      "message.sent",
		MessageID: "msg-1",
		SenderID:  "alice",
	})

	// Stats on nil should return zeros.
	errs, total := p.Stats()
	if errs != 0 || total != 0 {
		t.Errorf("nil Stats = (%d, %d), want (0, 0)", errs, total)
	}
}

func TestProducer_Close_Nil(t *testing.T) {
	var p *Producer
	if err := p.Close(); err != nil {
		t.Errorf("nil Close: %v", err)
	}
}

func TestConsumer_NilSafety(t *testing.T) {
	// Run on a nil consumer should not panic; it should block until ctx done.
	// We use an already-canceled context so it returns immediately.
	var c *Consumer
	// We can't test Run easily without real Kafka, but we verify
	// that Close on nil doesn't panic.
	if err := c.Close(); err != nil {
		t.Errorf("nil Close: %v", err)
	}
}

func TestProducerConfig_Defaults(t *testing.T) {
	// Empty brokers should produce a nil producer.
	cfg := ProducerConfig{Brokers: nil, Topic: "test"}
	p := NewProducer(cfg)
	if p != nil {
		t.Error("expected nil producer for empty brokers")
	}

	// Stats should work on nil.
	errs, total := p.Stats()
	if errs != 0 || total != 0 {
		t.Errorf("Stats = (%d, %d), want (0, 0)", errs, total)
	}
}

func TestConsumerConfig_Defaults(t *testing.T) {
	cfg := ConsumerConfig{Brokers: nil, Topic: "test", GroupID: "g1"}
	c := NewConsumer(cfg)
	if c != nil {
		t.Error("expected nil consumer for empty brokers")
	}
}

func TestMessageEvent_Fields(t *testing.T) {
	evt := MessageEvent{
		Type:        "message.sent",
		MessageID:   "msg-abc",
		SenderID:    "alice",
		ReceiverID:  "bob",
		Content:     "hello",
		ContentType: "text",
	}

	if evt.Type != "message.sent" {
		t.Errorf("Type = %s", evt.Type)
	}
	if evt.MessageID != "msg-abc" {
		t.Errorf("MessageID = %s", evt.MessageID)
	}
	if evt.GroupID != "" {
		t.Errorf("GroupID should be empty for private message")
	}
}
