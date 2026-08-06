// Package broker provides cross-instance message routing via Redis Pub/Sub.
//
// When multiple gateway instances are deployed, each instance has its own
// in-memory Hub. A sender on instance A can't reach a receiver on instance B
// through the Hub alone. The broker solves this: after persisting a message,
// the source instance publishes to Redis. All instances (including the source)
// receive the publication and try local delivery.
//
// The source instance is tracked via InstanceID to avoid double-delivery
// — the source already delivered locally before publishing.
package broker

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/redis/go-redis/v9"
)

// DefaultChannel is the shared Redis Pub/Sub channel for cross-instance
// message delivery. All gateway instances subscribe to this channel.
const DefaultChannel = "im:deliver"

// CrossMessage is published to Redis when a message needs cross-instance delivery.
// Short field names keep the wire overhead low.
type CrossMessage struct {
	// SourceInstance is the InstanceID of the gateway that published this message.
	// Receivers skip delivery if they are the source (already delivered locally).
	SourceInstance string `json:"si"`
	// TargetUser is the intended recipient. Only gateways that have this user
	// connected locally deliver the envelope.
	TargetUser string `json:"tu"`
	// Envelope is the pre-marshaled WebSocket message to deliver.
	Envelope json.RawMessage `json:"env"`
}

// Handler is called for each cross-instance message received.
// It receives the parsed CrossMessage and the raw JSON bytes.
// The handler should not block — launch a goroutine for long work.
type Handler func(msg *CrossMessage)

// Broker wraps Redis Pub/Sub for cross-instance message routing.
// It is safe for concurrent use.
type Broker struct {
	client     *redis.Client
	instanceID string
	mu         sync.Mutex
	subs       map[string]*redis.PubSub // track subscriptions for cleanup
}

// New creates a Broker. instanceID should be unique per gateway instance.
func New(client *redis.Client, instanceID string) *Broker {
	return &Broker{
		client:     client,
		instanceID: instanceID,
		subs:       make(map[string]*redis.PubSub),
	}
}

// Publish sends a cross-instance message to the given channel.
// The envelope should already be marshaled JSON (the whole WebSocket envelope).
// Callers are responsible for retry logic.
func (b *Broker) Publish(ctx context.Context, channel string, targetUser string, envelope []byte) error {
	msg := CrossMessage{
		SourceInstance: b.instanceID,
		TargetUser:     targetUser,
		Envelope:       envelope,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, channel, data).Err()
}

// Subscribe starts a goroutine that listens on the given channel and calls
// handler for each message. It blocks until ctx is cancelled, then cleans up.
//
// handler is called synchronously from the subscriber goroutine. If your
// handler does I/O, spawn a goroutine inside it to avoid backpressure on
// the Redis subscription.
func (b *Broker) Subscribe(ctx context.Context, channel string, handler Handler) {
	sub := b.client.Subscribe(ctx, channel)

	b.mu.Lock()
	b.subs[channel] = sub
	b.mu.Unlock()

	go func() {
		defer func() {
			b.mu.Lock()
			delete(b.subs, channel)
			b.mu.Unlock()
			sub.Close()
			slog.Info("broker: unsubscribed", "channel", channel)
		}()

		ch := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				var cm CrossMessage
				if err := json.Unmarshal([]byte(msg.Payload), &cm); err != nil {
					slog.Warn("broker: parse error", "channel", channel, "err", err)
					continue
				}
				// Skip self-delivery: the source instance already delivered
				// locally before publishing to the broker.
				if cm.SourceInstance == b.instanceID {
					continue
				}
				handler(&cm)
			}
		}
	}()

	slog.Info("broker: subscribed", "channel", channel, "instance", b.instanceID)
}

// InstanceID returns the broker's instance identifier.
func (b *Broker) InstanceID() string {
	return b.instanceID
}

// Close unsubscribes all active subscriptions. It does not close the Redis
// client — that is owned by the caller.
func (b *Broker) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for channel, sub := range b.subs {
		if err := sub.Close(); err != nil {
			slog.Warn("broker: close sub error", "channel", channel, "err", err)
		}
		delete(b.subs, channel)
	}
	return nil
}
