package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// init registers Go runtime metrics (goroutines, GC pauses, memory, etc.)
// and process metrics (CPU, file descriptors). These are free observability
// signals provided by the Prometheus client library.
//
// Uses a try-register pattern: some libraries (e.g., promhttp, promauto) may
// already register these collectors. We register only if not already present.
//
// In production, watch:
//   - go_goroutines: sustained growth → goroutine leak
//   - go_memstats_heap_inuse_bytes: steady increase → memory leak
//   - go_gc_duration_seconds: spikes → allocation pressure
func init() {
	tryRegister(collectors.NewGoCollector())
	tryRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
}

// tryRegister calls prometheus.Register. If the collector is already
// registered (e.g., by another library), the error is silently ignored.
// Any other registration error causes a panic — those are programming bugs.
func tryRegister(c prometheus.Collector) {
	err := prometheus.Register(c)
	if err == nil {
		return
	}
	if _, ok := err.(prometheus.AlreadyRegisteredError); ok {
		return
	}
	panic(err)
}

// HTTP metrics follow the RED method: Rate, Errors, Duration.
//
// Rate & Errors are tracked together in http_requests_total with a status label.
// 5xx responses are errors; PromQL can compute error rate with:
//
//	rate(http_requests_total{status=~"5.."}[1m])
//
// Duration is a Histogram so we can compute P50/P95/P99 percentiles:
//
//	histogram_quantile(0.99, rate(http_request_duration_seconds_bucket[1m]))
var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests processed.",
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "http_request_duration_seconds",
			Help: "Duration of HTTP requests in seconds.",
			// Buckets from 5ms to 10s — covers fast DB reads to slow uploads.
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"method", "path"},
	)
)

// Metrics returns a Gin middleware that records HTTP request counts and duration.
//
// It uses c.FullPath() for the path label, which returns the route template
// (e.g., "/api/v1/users/:id") — not the concrete URL. This avoids unbounded
// label cardinality from user IDs, message IDs, etc.
//
// Placement: add early in the middleware chain, before auth, so all requests
// are counted including 401s.
func Metrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// Process the request through the remaining handlers.
		c.Next()

		duration := time.Since(start).Seconds()
		method := c.Request.Method
		path := c.FullPath()
		// Fallback for routes that don't match a template (e.g., 404s).
		if path == "" {
			path = c.Request.URL.Path
		}
		status := strconv.Itoa(c.Writer.Status())

		httpRequestsTotal.WithLabelValues(method, path, status).Inc()
		httpRequestDuration.WithLabelValues(method, path).Observe(duration)
	}
}
