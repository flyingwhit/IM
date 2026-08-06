package kafka

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Kafka producer metrics.
//
// These track the fire-and-forget publish path. Since Kafka publishes are
// async (the DB is the source of truth), a high error rate here means
// downstream consumers (search index, analytics) are missing data, but
// message delivery itself is unaffected.
//
// Alert thresholds to consider:
//   - kafka_publish_errors_total rate > 0: investigate Kafka broker health
//   - kafka_publish_total flat while ws_messages_received_total rises:
//     producer may be disabled (no brokers configured)
var (
	kafkaPublishTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "kafka_publish_total",
			Help: "Total number of Kafka publish attempts (fire-and-forget).",
		},
	)

	kafkaPublishErrors = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "kafka_publish_errors_total",
			Help: "Total number of failed Kafka publish attempts.",
		},
	)
)
