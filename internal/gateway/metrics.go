package gateway

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// WebSocket metrics.
//
// Connection metrics tell you about user activity:
//   - ws_connections_active: how many users are connected right now.
//     Sudden drop → possible crash or network partition.
//   - ws_connections_total: lifetime connections. Used with rate() to see
//     connect/disconnect frequency (surge → possible reconnect storm).
//
// Message metrics tell you about system throughput:
//   - ws_messages_received_total: inbound from clients. Spikes may indicate
//     a chatty client or abuse.
//   - ws_messages_sent_total: outbound to clients. High value without
//     corresponding inbound → offline message delivery or broadcast.
//
// All counters are registered with promauto, so they appear on /metrics
// automatically without manual registration.
var (
	wsConnectionsActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "ws_connections_active",
			Help: "Current number of active WebSocket connections.",
		},
	)

	wsConnectionsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "ws_connections_total",
			Help: "Total number of WebSocket connections established since start.",
		},
	)

	wsMessagesReceived = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "ws_messages_received_total",
			Help: "Total number of WebSocket messages received from clients.",
		},
	)

	wsMessagesSent = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "ws_messages_sent_total",
			Help: "Total number of WebSocket messages sent to clients.",
		},
	)
)
