package handler

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	redisrepo "github.com/ciel/im/internal/repository/redis"
)

// onlineChecker is the subset of gateway.Hub that PresenceHandler needs.
// Using an interface avoids importing the gateway package and follows the
// same pattern as service.MessageRouter.
type onlineChecker interface {
	IsOnline(userID string) bool
}

// PresenceHandler handles online status HTTP endpoints.
type PresenceHandler struct {
	hub          onlineChecker            // local Hub (fast, no network)
	presenceRepo *redisrepo.PresenceRepo  // Redis fallback (multi-instance)
}

// NewPresenceHandler creates a PresenceHandler.
func NewPresenceHandler(hub onlineChecker, presenceRepo *redisrepo.PresenceRepo) *PresenceHandler {
	return &PresenceHandler{hub: hub, presenceRepo: presenceRepo}
}

// GetOnlineStatus handles GET /api/v1/users/:id/online.
//
// It returns whether the specified user is currently online. The lookup
// order is:
//  1. Local Hub (in-memory, always available, no network call)
//  2. Redis presence key (shared state for multi-instance, network call)
//
// If Redis is unreachable, we return false (conservative: assume offline
// when we can't confirm online). This is a graceful degradation — the Hub
// already knows about local connections, and Redis failures are rare.
func (h *PresenceHandler) GetOnlineStatus(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing user id"})
		return
	}

	// 1. Check local Hub first — fastest path.
	if h.hub != nil && h.hub.IsOnline(userID) {
		c.JSON(http.StatusOK, gin.H{"user_id": userID, "online": true})
		return
	}

	// 2. Fall back to Redis — covers multi-instance and cold start.
	if h.presenceRepo != nil {
		online, err := h.presenceRepo.IsOnline(c.Request.Context(), userID)
		if err != nil {
			// Redis unavailable — log and return false rather than 500.
			// The Hub already checked and found nothing, so this is
			// the most accurate answer we can give.
			log.Printf("presence: redis IsOnline error for user %s: %v", userID, err)
			c.JSON(http.StatusOK, gin.H{"user_id": userID, "online": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{"user_id": userID, "online": online})
		return
	}

	// 3. No sources available (shouldn't happen in production).
	c.JSON(http.StatusOK, gin.H{"user_id": userID, "online": false})
}
