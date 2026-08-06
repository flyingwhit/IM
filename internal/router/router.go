package router

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ciel/im/internal/gateway"
	"github.com/ciel/im/internal/handler"
	"github.com/ciel/im/internal/middleware"
	"github.com/ciel/im/internal/service"
)

// Setup configures all routes and returns a Gin engine.
func Setup(
	authHandler *handler.AuthHandler,
	userHandler *handler.UserHandler,
	friendHandler *handler.FriendHandler,
	healthHandler *handler.HealthHandler,
	presenceHandler *handler.PresenceHandler,
	messageHandler *handler.MessageHandler,
	groupHandler *handler.GroupHandler,
	authService *service.AuthService,
	wsHandler *gateway.Handler,
) *gin.Engine {
	r := gin.Default()

	// HTTP metrics middleware — tracks RED (Rate/Errors/Duration)
	// for all requests. Placed before CORS and auth so every request
	// is counted (including 401s and CORS preflight).
	r.Use(middleware.Metrics())

	// CORS — allow cross-origin requests from the test client.
	// The test client runs on file:// or varying localhost ports.
	r.Use(middleware.CORS())

	// Prometheus metrics endpoint — infrastructure, no auth.
	// Prometheus scrapes this at regular intervals (typically 15s).
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Health check — no auth, no version prefix (infrastructure endpoint).
	r.GET("/health", healthHandler.Check)

	// WebSocket — no version prefix (protocol upgrade, not a REST API).
	// Auth is handled inside the handler via query parameter token.
	r.GET("/ws", wsHandler.Handle)

	api := r.Group("/api/v1")
	{
		// Public endpoints (no auth required)
		auth := api.Group("/auth")
		{
			auth.POST("/register", authHandler.Register)
			auth.POST("/login", authHandler.Login)
			auth.POST("/refresh", authHandler.Refresh)
			auth.POST("/logout", authHandler.Logout)
		}

		// Protected endpoints (auth required)
		protected := api.Group("")
		protected.Use(middleware.AuthRequired(authService))
		{
			users := protected.Group("/users")
			{
				users.GET("/me", userHandler.GetProfile)
				users.PUT("/me", userHandler.UpdateProfile)
				users.GET("/:id", userHandler.GetUser)
				users.GET("/:id/online", presenceHandler.GetOnlineStatus)
			}

			friends := protected.Group("/friends")
			{
				friends.POST("/requests", friendHandler.SendRequest)
				friends.GET("", friendHandler.ListFriends)
				friends.GET("/requests", friendHandler.ListPendingRequests)
				friends.PUT("/requests/:id/accept", friendHandler.AcceptRequest)
				friends.PUT("/requests/:id/reject", friendHandler.RejectRequest)
				friends.DELETE("/:id", friendHandler.RemoveFriend)
			}

			messages := protected.Group("/messages")
			{
				messages.GET("", messageHandler.GetConversation)
				messages.POST("", messageHandler.SendMessage)
			}

			groups := protected.Group("/groups")
			{
				groups.POST("", groupHandler.Create)
				groups.GET("", groupHandler.ListMyGroups)
				groups.GET("/:id", groupHandler.Get)
				groups.PUT("/:id", groupHandler.UpdateName)
				groups.DELETE("/:id", groupHandler.Delete)
				groups.POST("/:id/members", groupHandler.AddMembers)
				groups.DELETE("/:id/members/:uid", groupHandler.RemoveMember)
				groups.GET("/:id/members", groupHandler.ListMembers)
				groups.GET("/:id/messages", groupHandler.GetMessages)
			}
		}
	}

	return r
}
