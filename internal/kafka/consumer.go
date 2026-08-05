package kafka

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/segmentio/kafka-go"
)

// ConsumerConfig holds configuration for the Kafka consumer.
type ConsumerConfig struct {
	// Brokers is a list of bootstrap servers.
	Brokers []string
	// Topic is the Kafka topic to consume from.
	Topic string
	// GroupID is the consumer group identifier.
	GroupID string
}

// Consumer reads message events from Kafka.
type Consumer struct {
	reader *kafka.Reader
}

// EventHandler is a callback for each consumed message event.
// Return an error to signal the consumer should stop (fatal error).
type EventHandler func(ctx context.Context, event *MessageEvent) error

// NewConsumer creates a Kafka consumer. If cfg.Brokers is empty,
// returns nil — Kafka integration is disabled.
func NewConsumer(cfg ConsumerConfig) *Consumer {
	if len(cfg.Brokers) == 0 {
		log.Println("kafka: consumer disabled (no brokers configured)")
		return nil
	}

	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: cfg.Brokers,
		Topic:   cfg.Topic,
		GroupID: cfg.GroupID,
		// StartOffset: when a new group joins, start from the latest.
		// Existing groups resume from their last committed offset.
		StartOffset: kafka.LastOffset,
		// CommitInterval controls how often offsets are committed.
		// At-least-once semantics: message may be processed more than once.
		CommitInterval: time.Second,
		// MaxBytes caps the size of each fetch — prevents memory bloat.
		MaxBytes: 10e6, // 10 MB
	})

	log.Printf("kafka: consumer started (topic=%s, group=%s)", cfg.Topic, cfg.GroupID)
	return &Consumer{reader: r}
}

// Run starts a blocking consume loop. It returns when ctx is cancelled
// or handler returns a fatal error.
//
// Offsets are committed automatically after each message is processed.
// If the process crashes between processing and commit, the message
// will be re-delivered — handlers should be idempotent.
func (c *Consumer) Run(ctx context.Context, handler EventHandler) error {
	if c == nil {
		<-ctx.Done()
		return nil
	}

	defer func() {
		log.Println("kafka: consumer stopped")
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil // normal shutdown
			}
			log.Printf("kafka: fetch error: %v", err)
			continue
		}

		var event MessageEvent
		if err := json.Unmarshal(msg.Value, &event); err != nil {
			log.Printf("kafka: parse error (offset=%d): %v", msg.Offset, err)
			// Commit bad messages so we don't get stuck.
			c.reader.CommitMessages(ctx, msg)
			continue
		}

		if err := handler(ctx, &event); err != nil {
			log.Printf("kafka: handler error for %s: %v", event.MessageID, err)
			// Don't commit — retry on next poll.
			continue
		}

		// Commit the offset after successful processing.
		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			log.Printf("kafka: commit error: %v", err)
		}
	}
}

// Close shuts down the consumer. Call during graceful shutdown.
func (c *Consumer) Close() error {
	if c == nil {
		return nil
	}
	log.Println("kafka: closing consumer")
	return c.reader.Close()
}
