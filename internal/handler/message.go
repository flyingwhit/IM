package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ciel/im/internal/middleware"
	"github.com/ciel/im/internal/model"
	"github.com/ciel/im/internal/service"
)

// MessageHandler handles message-related HTTP endpoints.
type MessageHandler struct {
	messageService *service.MessageService
}

// NewMessageHandler creates a MessageHandler.
func NewMessageHandler(messageService *service.MessageService) *MessageHandler {
	return &MessageHandler{messageService: messageService}
}

// GetConversation handles GET /api/v1/messages?peer=<user_id>&before=<cursor>&limit=50.
//
// Returns a paginated list of messages between the authenticated user and the peer.
// Messages are returned newest-first for display as a chat history.
//
// Query parameters:
//   - peer (required): the other user's ID
//   - before (optional): cursor for pagination (RFC 3339Nano timestamp)
//   - limit (optional): max messages to return, default 50, max 100
func (h *MessageHandler) GetConversation(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)

	peer := c.Query("peer")
	if peer == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "peer query parameter is required"})
		return
	}
	if peer == userID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot query conversation with yourself"})
		return
	}

	// Parse cursor (defaults to now = fetch latest messages).
	var cursor model.Cursor
	if before := c.Query("before"); before != "" {
		cursor = model.Cursor(before)
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n <= 0 || n > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and 100"})
			return
		}
		limit = n
	}

	messages, err := h.messageService.GetConversation(userID, peer, cursor, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch messages"})
		return
	}

	// Build cursor for next page.
	var nextCursor string
	if len(messages) > 0 {
		oldest := messages[len(messages)-1]
		nextCursor = string(model.NewCursor(oldest.CreatedAt))
	}

	c.JSON(http.StatusOK, gin.H{
		"messages":    messages,
		"next_cursor": nextCursor,
	})
}
