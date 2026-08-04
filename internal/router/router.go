package router

import (
	"github.com/gin-gonic/gin"

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
	authService *service.AuthService,
	wsHandler *gateway.Handler,
) *gin.Engine {
	r := gin.Default()

	// CORS — allow cross-origin requests from the test client.
	// The test client runs on file:// or varying localhost ports.
	r.Use(middleware.CORS())

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
			}
		}
	}

	return r
}
