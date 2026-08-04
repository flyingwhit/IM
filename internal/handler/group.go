package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ciel/im/internal/middleware"
	"github.com/ciel/im/internal/model"
	"github.com/ciel/im/internal/service"
)

// GroupHandler handles group-related HTTP endpoints.
type GroupHandler struct {
	groupService   *service.GroupService
	messageService *service.MessageService
}

// NewGroupHandler creates a GroupHandler.
func NewGroupHandler(groupService *service.GroupService, messageService *service.MessageService) *GroupHandler {
	return &GroupHandler{groupService: groupService, messageService: messageService}
}

// createGroupRequest is the JSON body for POST /api/v1/groups.
type createGroupRequest struct {
	Name      string   `json:"name"`
	MemberIDs []string `json:"member_ids"`
}

// Create handles POST /api/v1/groups.
func (h *GroupHandler) Create(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)

	var req createGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	group, err := h.groupService.Create(c.Request.Context(), req.Name, userID, req.MemberIDs)
	if err != nil {
		c.JSON(errorStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, group)
}

// Get handles GET /api/v1/groups/:id.
func (h *GroupHandler) Get(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)
	groupID := c.Param("id")

	group, err := h.groupService.GetGroup(c.Request.Context(), groupID, userID)
	if err != nil {
		c.JSON(errorStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, group)
}

// UpdateName handles PUT /api/v1/groups/:id.
func (h *GroupHandler) UpdateName(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)
	groupID := c.Param("id")

	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	if err := h.groupService.UpdateName(c.Request.Context(), groupID, userID, req.Name); err != nil {
		c.JSON(errorStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Delete handles DELETE /api/v1/groups/:id.
func (h *GroupHandler) Delete(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)
	groupID := c.Param("id")

	if err := h.groupService.Delete(c.Request.Context(), groupID, userID); err != nil {
		c.JSON(errorStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ListMyGroups handles GET /api/v1/groups.
func (h *GroupHandler) ListMyGroups(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)
	offset, limit := parsePagination(c)

	groupIDs, err := h.groupService.ListGroups(c.Request.Context(), userID, offset, limit)
	if err != nil {
		c.JSON(errorStatus(err), gin.H{"error": err.Error()})
		return
	}
	if groupIDs == nil {
		groupIDs = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"group_ids": groupIDs})
}

// addMembersRequest is the JSON body for POST /api/v1/groups/:id/members.
type addMembersRequest struct {
	UserIDs []string `json:"user_ids"`
}

// AddMembers handles POST /api/v1/groups/:id/members.
func (h *GroupHandler) AddMembers(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)
	groupID := c.Param("id")

	var req addMembersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if len(req.UserIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_ids is required"})
		return
	}

	if err := h.groupService.AddMembers(c.Request.Context(), groupID, userID, req.UserIDs); err != nil {
		c.JSON(errorStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// RemoveMember handles DELETE /api/v1/groups/:id/members/:uid.
func (h *GroupHandler) RemoveMember(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)
	groupID := c.Param("id")
	targetID := c.Param("uid")

	if err := h.groupService.RemoveMember(c.Request.Context(), groupID, userID, targetID); err != nil {
		c.JSON(errorStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ListMembers handles GET /api/v1/groups/:id/members.
func (h *GroupHandler) ListMembers(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)
	groupID := c.Param("id")

	members, err := h.groupService.ListMembers(c.Request.Context(), groupID, userID)
	if err != nil {
		c.JSON(errorStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"members": members})
}

// GetMessages handles GET /api/v1/groups/:id/messages.
func (h *GroupHandler) GetMessages(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)
	groupID := c.Param("id")

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

	msgs, err := h.groupService.GetGroupMessages(c.Request.Context(), groupID, userID, cursor, limit)
	if err != nil {
		c.JSON(errorStatus(err), gin.H{"error": err.Error()})
		return
	}

	var nextCursor string
	if len(msgs) > 0 {
		oldest := msgs[len(msgs)-1]
		nextCursor = string(model.NewCursor(oldest.CreatedAt))
	}

	c.JSON(http.StatusOK, gin.H{
		"messages":    msgs,
		"next_cursor": nextCursor,
	})
}

