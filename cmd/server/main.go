package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ciel/im/internal/broker"
	"github.com/ciel/im/internal/config"
	"github.com/ciel/im/internal/gateway"
	"github.com/ciel/im/internal/handler"
	kafkapkg "github.com/ciel/im/internal/kafka"
	"github.com/ciel/im/internal/logging"
	"github.com/ciel/im/internal/repository/postgres"
	redisrepo "github.com/ciel/im/internal/repository/redis"
	"github.com/ciel/im/internal/router"
	"github.com/ciel/im/internal/service"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Initialize structured logging before anything else.
	// After this, all packages use slog instead of log.Printf.
	logging.Init(cfg.Log.Level, cfg.Log.Format)
	slog.Info("starting", "mode", cfg.Server.Mode)

	// Print config warnings (e.g., default secrets in production).
	for _, w := range cfg.Validate() {
		slog.Warn("config: " + w)
	}

	switch cfg.Server.Mode {
	case "worker":
		return runWorker(cfg)
	case "gateway":
		return runGateway(cfg) // future: WebSocket-only
	case "api":
		return runAPI(cfg) // future: REST-only
	default:
		return runAll(cfg)
	}
}

// runAll starts all components: HTTP server, WebSocket hub, Kafka producer.
// This is the default mode for development and single-instance deployments.
func runAll(cfg *config.Config) error {
	ctx := context.Background()

	// PostgreSQL
	pool, err := postgres.NewPool(ctx, cfg.DB.DSN())
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	slog.Info("connected to PostgreSQL")

	// Redis
	redisClient, err := redisrepo.NewClient(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	defer redisClient.Close()
	slog.Info("connected to Redis")

	// Repositories
	userRepo := postgres.NewUserRepo(pool)
	friendRepo := postgres.NewFriendRepo(pool)
	messageRepo := postgres.NewMessageRepo(pool)
	groupRepo := postgres.NewGroupRepo(pool)
	groupMessageRepo := postgres.NewGroupMessageRepo(pool)
	sessionRepo := redisrepo.NewSessionRepo(redisClient)
	presenceRepo := redisrepo.NewPresenceRepo(redisClient)

	// Cross-instance message broker (Redis Pub/Sub).
	msgBroker := broker.New(redisClient, cfg.Gateway.InstanceID)
	defer msgBroker.Close()

	// WebSocket Hub — runs until the hub context is canceled during shutdown.
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()
	hub := gateway.NewHub(presenceRepo, msgBroker)
	go hub.Run(hubCtx)

	// Kafka producer (optional — disabled if KAFKA_BROKERS is empty).
	kafkaProducer := kafkapkg.NewProducer(kafkapkg.ProducerConfig{
		Brokers: splitBrokers(cfg.Kafka.Brokers),
		Topic:   cfg.Kafka.TopicMessages,
	})
	if kafkaProducer != nil {
		defer kafkaProducer.Close()
	}

	// Services
	authService := service.NewAuthService(userRepo, sessionRepo, cfg.JWT)
	friendService := service.NewFriendService(friendRepo, userRepo)
	messageService := service.NewMessageService(messageRepo, friendRepo, groupRepo, groupMessageRepo, hub, kafkaProducer)
	groupService := service.NewGroupService(groupRepo, userRepo, groupMessageRepo)

	// Wire Hub callbacks
	hub.OnMessage = messageService.HandleIncomingMessage
	hub.OnConnect = messageService.DeliverOfflineMessages

	// Handlers
	authHandler := handler.NewAuthHandler(authService)
	userHandler := handler.NewUserHandler(userRepo)
	friendHandler := handler.NewFriendHandler(friendService)
	healthHandler := handler.NewHealthHandler(pool, redisClient, kafkaProducer)
	wsHandler := gateway.NewHandler(authService, hub)
	presenceHandler := handler.NewPresenceHandler(hub, presenceRepo)
	messageHandler := handler.NewMessageHandler(messageService)
	groupHandler := handler.NewGroupHandler(groupService, messageService)

	// Router
	r := router.Setup(authHandler, userHandler, friendHandler, healthHandler, presenceHandler, messageHandler, groupHandler, authService, wsHandler)

	// HTTP server
	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%s", cfg.Server.Host, cfg.Server.Port),
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// ─── Graceful Shutdown ───────────────────────────────────
	//
	// Order matters — each step depends on the previous:
	//
	//   1. Hub         — stop accepting new deliveries, unsubscribe broker.
	//                    Must stop before HTTP server so in-flight WS writes
	//                    don't target closing connections.
	//   2. HTTP server — drain in-flight requests (5s grace period).
	//   3. Deferred     — broker → kafka → redis → postgres (LIFO order).
	//
	// A 30s safety net prevents hanging forever if a component's Close()
	// blocks (e.g., network partition during DB connection drain).

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("server listening", "host", cfg.Server.Host, "port", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	case <-quit:
		slog.Info("shutting down...")
	}

	// Safety net: force-exit after 30s regardless of state.
	// Normal shutdown takes <1s; this catches stuck Close() calls.
	shutdownDone := make(chan struct{})
	go func() {
		// Step 1: Stop Hub event loop.
		hubCancel()
		slog.Info("hub stopped")

		// Step 2: Drain HTTP requests.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("http shutdown error", "err", err)
		} else {
			slog.Info("http server stopped")
		}

		// Step 3: Deferred cleanups run when runAll returns.
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
		slog.Info("shutdown complete")
	case <-time.After(30 * time.Second):
		slog.Warn("shutdown timed out after 30s — forcing exit")
	}
	return nil
}

// runWorker starts a Kafka consumer that processes message events.
// It connects to PostgreSQL and Kafka, then blocks until a shutdown signal.
//
// Currently the handler logs events — future work includes writing to
// search indexes, analytics pipelines, or async notification dispatch.
func runWorker(cfg *config.Config) error {
	ctx := context.Background()

	// PostgreSQL (for future: write-through processing)
	pool, err := postgres.NewPool(ctx, cfg.DB.DSN())
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	slog.Info("worker: connected to PostgreSQL")
	_ = pool // used by future handler logic

	brokers := splitBrokers(cfg.Kafka.Brokers)
	if len(brokers) == 0 {
		return fmt.Errorf("KAFKA_BROKERS is required for worker mode")
	}

	consumer := kafkapkg.NewConsumer(kafkapkg.ConsumerConfig{
		Brokers: brokers,
		Topic:   cfg.Kafka.TopicMessages,
		GroupID: cfg.Kafka.ConsumerGroup,
	})
	if consumer == nil {
		return fmt.Errorf("failed to create Kafka consumer")
	}
	defer consumer.Close()

	// Handle shutdown gracefully.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		<-quit
		slog.Info("worker: shutting down...")
		cancel()
	}()

	slog.Info("worker: consuming", "topic", cfg.Kafka.TopicMessages, "group", cfg.Kafka.ConsumerGroup)

	// handler processes each message event. Currently logs — future work
	// includes indexing, analytics, and async notification dispatch.
	handler := func(ctx context.Context, event *kafkapkg.MessageEvent) error {
		slog.Debug("worker: received event", "type", event.Type, "msg_id", event.MessageID, "sender", event.SenderID)
		// Future: write to Elasticsearch, update analytics counters, etc.
		return nil
	}

	return consumer.Run(workerCtx, handler)
}

// runGateway starts only WebSocket and message routing components.
// REST endpoints (auth, friends, groups) are not available in this mode.
// TODO: implement in a future iteration.
func runGateway(cfg *config.Config) error {
	_ = cfg
	return fmt.Errorf("gateway mode not yet implemented — use 'all' mode for now")
}

// runAPI starts only REST endpoints. WebSocket is not available.
// Useful for scaling the API tier independently from WebSocket gateways.
// TODO: implement in a future iteration.
func runAPI(cfg *config.Config) error {
	_ = cfg
	return fmt.Errorf("api mode not yet implemented — use 'all' mode for now")
}

// splitBrokers splits a comma-separated broker list into a slice.
// Empty strings return nil, which disables Kafka integration.
func splitBrokers(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
