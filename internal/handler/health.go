package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	kafkapkg "github.com/ciel/im/internal/kafka"
)

// HealthHandler provides liveness and readiness endpoints.
//
// Kubernetes probes:
//   - liveness:  GET /health    → did the process crash? Restart if fail.
//   - readiness: GET /health/ready → can it serve traffic? Remove from Service if fail.
//
// Readiness checks DB, Redis, and Kafka with a 2s timeout per probe so a
// slow dependency doesn't cascade into an HTTP timeout (server has 10s
// ReadTimeout/WriteTimeout).
type HealthHandler struct {
	db    *pgxpool.Pool
	redis *redis.Client
	kafka *kafkapkg.Producer
}

// NewHealthHandler creates a HealthHandler. All dependencies may be nil —
// nil dependencies are skipped in readiness checks.
func NewHealthHandler(db *pgxpool.Pool, redis *redis.Client, kafka *kafkapkg.Producer) *HealthHandler {
	return &HealthHandler{db: db, redis: redis, kafka: kafka}
}

// Check handles GET /health (liveness probe).
// Returns 200 as long as the process is alive — no dependency checks.
func (h *HealthHandler) Check(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Ready handles GET /health/ready (readiness probe).
// Pings PostgreSQL, Redis, and checks Kafka error rate.
// Returns 503 if any dependency is unhealthy.
func (h *HealthHandler) Ready(c *gin.Context) {
	checks := make(map[string]string)
	healthy := true

	// Use a short timeout so one slow dependency doesn't cause the
	// readiness probe itself to time out. 2s is well within the
	// server's 10s WriteTimeout.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	// PostgreSQL — a simple ping validates the connection pool.
	if h.db != nil {
		if err := h.db.Ping(ctx); err != nil {
			checks["postgres"] = fmt.Sprintf("unhealthy: %v", err)
			healthy = false
		} else {
			checks["postgres"] = "healthy"
		}
	}

	// Redis — PING command validates connectivity.
	if h.redis != nil {
		if err := h.redis.Ping(ctx).Err(); err != nil {
			checks["redis"] = fmt.Sprintf("unhealthy: %v", err)
			healthy = false
		} else {
			checks["redis"] = "healthy"
		}
	}

	// Kafka — we can't send a test message without polluting the topic,
	// so we check whether the producer exists and whether it has recent
	// errors. A nil producer means Kafka is intentionally disabled.
	if h.kafka != nil {
		errs, total := h.kafka.Stats()
		if total > 0 && float64(errs)/float64(total) > 0.5 {
			checks["kafka"] = fmt.Sprintf("degraded: %d/%d errors", errs, total)
			// Degraded, not unhealthy — Kafka is not on the critical path.
		} else {
			checks["kafka"] = "healthy"
		}
	}

	status := http.StatusOK
	if !healthy {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{"status": map[bool]string{true: "ok", false: "degraded"}[healthy], "checks": checks})
}
