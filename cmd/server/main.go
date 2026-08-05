package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ciel/im/internal/broker"
	"github.com/ciel/im/internal/config"
	"github.com/ciel/im/internal/gateway"
	"github.com/ciel/im/internal/handler"
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
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx := context.Background()

	// PostgreSQL
	pool, err := postgres.NewPool(ctx, cfg.DB.DSN())
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	log.Println("connected to PostgreSQL")

	// Redis
	redisClient, err := redisrepo.NewClient(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	defer redisClient.Close()
	log.Println("connected to Redis")

	// Repositories
	userRepo := postgres.NewUserRepo(pool)
	friendRepo := postgres.NewFriendRepo(pool)
	messageRepo := postgres.NewMessageRepo(pool)
	groupRepo := postgres.NewGroupRepo(pool)
	groupMessageRepo := postgres.NewGroupMessageRepo(pool)
	sessionRepo := redisrepo.NewSessionRepo(redisClient)
	presenceRepo := redisrepo.NewPresenceRepo(redisClient)

	// Cross-instance message broker (Redis Pub/Sub).
	// Enables multi-gateway routing: messages published by one instance
	// are received by all instances and delivered to local connections.
	msgBroker := broker.New(redisClient, cfg.Gateway.InstanceID)

	// WebSocket Hub (manages all active connections)
	hub := gateway.NewHub(presenceRepo, msgBroker)
	go hub.Run(context.Background())

	// Services
	authService := service.NewAuthService(userRepo, sessionRepo, cfg.JWT)
	friendService := service.NewFriendService(friendRepo, userRepo)
	messageService := service.NewMessageService(messageRepo, friendRepo, groupRepo, groupMessageRepo, hub)
	groupService := service.NewGroupService(groupRepo, userRepo, groupMessageRepo)

	// Wire Hub callbacks so WebSocket frames are dispatched to MessageService.
	hub.OnMessage = messageService.HandleIncomingMessage
	hub.OnConnect = messageService.DeliverOfflineMessages

	// Handlers
	authHandler := handler.NewAuthHandler(authService)
	userHandler := handler.NewUserHandler(userRepo)
	friendHandler := handler.NewFriendHandler(friendService)
	healthHandler := handler.NewHealthHandler()
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

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// serverErr channel lets the server goroutine report unexpected
	// errors back to the main goroutine for clean shutdown.
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("server listening on %s:%s", cfg.Server.Host, cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// Block until either a signal or a server error arrives.
	select {
	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	case <-quit:
		log.Println("shutting down...")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	return nil
}
