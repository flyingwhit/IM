// Package kafka provides Kafka producer and consumer for the message
// persistence pipeline.
//
// Architecture (Phase 4):
//
//	Gateway ── DB write (sync) ──► PostgreSQL
//	        └── Kafka produce (async) ──► Worker consume ──► (future: search index, analytics)
//
// The producer is fire-and-forget: failures are logged but never block
// the message send path. The DB is the source of truth; Kafka events
// are an optimization for downstream consumers.
package kafka

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/segmentio/kafka-go"
)

// MessageEvent is the envelope for Kafka message events.
type MessageEvent struct {
	// Type is the event type: "message.sent", "group.message.sent".
	Type string `json:"type"`
	// MessageID is the UUID of the persisted message.
	MessageID string `json:"message_id"`
	// SenderID is the user who sent the message.
	SenderID string `json:"sender_id"`
	// ReceiverID is the target user (empty for group messages).
	ReceiverID string `json:"receiver_id,omitempty"`
	// GroupID is the target group (empty for private messages).
	GroupID string `json:"group_id,omitempty"`
	// Content is the message text.
	Content string `json:"content"`
	// ContentType is "text" or "image" etc.
	ContentType string `json:"content_type"`
	// Timestamp is when the message was created.
	Timestamp time.Time `json:"timestamp"`
}

// Producer publishes message events to Kafka.
// It is safe for concurrent use.
type Producer struct {
	writer       *kafka.Writer
	publishErrs  atomic.Int64 // count of failed publishes since start
	publishCount atomic.Int64 // total publish attempts since start
}

// ProducerConfig holds configuration for the Kafka producer.
type ProducerConfig struct {
	// Brokers is a list of bootstrap servers.
	Brokers []string
	// Topic is the Kafka topic to publish to.
	Topic string
}

// NewProducer creates a Kafka producer. If cfg.Brokers is empty,
// returns nil — Kafka integration is disabled.
func NewProducer(cfg ProducerConfig) *Producer {
	if len(cfg.Brokers) == 0 {
		slog.Info("kafka: producer disabled (no brokers configured)")
		return nil
	}

	w := &kafka.Writer{
		Addr:         kafka.TCP(cfg.Brokers...),
		Topic:        cfg.Topic,
		Balancer:     &kafka.LeastBytes{},
		BatchTimeout: 10 * time.Millisecond,
		// BatchSize=1 means each write is immediately sent.
		// For higher throughput, increase this but accept latency.
		BatchSize: 1,
		// RequiredAcks=1 waits for leader ack (not all replicas).
		// Good balance between durability and latency.
		RequiredAcks: kafka.RequireOne,
		// Compression reduces network bytes at a modest CPU cost.
		Compression: kafka.Snappy,
	}

	slog.Info("kafka: producer connected", "brokers", cfg.Brokers, "topic", cfg.Topic)
	return &Producer{writer: w}
}

// Publish sends a message event to Kafka. It is fire-and-forget:
// errors are logged, not returned. The caller is the hot message-send
// path — Kafka failures must not block delivery.
func (p *Producer) Publish(ctx context.Context, event *MessageEvent) {
	if p == nil {
		return
	}

	data, err := json.Marshal(event)
	if err != nil {
		slog.Error("kafka: marshal event", "msg_id", event.MessageID, "err", err)
		return
	}

	// Use the message ID as the key for partitioning.
	// Messages from the same sender will be ordered within a partition.
	p.publishCount.Add(1)
	err = p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(event.SenderID),
		Value: data,
	})
	if err != nil {
		p.publishErrs.Add(1)
		// Log but don't propagate — the message is already in PostgreSQL.
		slog.Warn("kafka: publish failed", "msg_id", event.MessageID, "err", err)
	}
}

// Stats returns the number of failed and total publish attempts.
// These can be exposed as Prometheus metrics or health-check endpoints.
func (p *Producer) Stats() (errors int64, total int64) {
	if p == nil {
		return 0, 0
	}
	return p.publishErrs.Load(), p.publishCount.Load()
}

// Close flushes and closes the producer. Call during graceful shutdown.
func (p *Producer) Close() error {
	if p == nil {
		return nil
	}
	slog.Info("kafka: closing producer")
	return p.writer.Close()
}
