package model

import "time"

// GroupRole defines the authority level of a member within a group.
type GroupRole string

const (
	GroupRoleOwner  GroupRole = "owner"
	GroupRoleMember GroupRole = "member"
)

// Group represents a group chat.
type Group struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	OwnerID   string    `json:"owner_id"`
	CreatedAt time.Time `json:"created_at"`
}

// GroupMember represents a user's membership in a group.
// The owner is also recorded as a member with role=owner.
type GroupMember struct {
	GroupID  string    `json:"group_id"`
	UserID   string    `json:"user_id"`
	Role     GroupRole `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

// GroupMessage represents a message sent in a group chat.
// Separate from the private message type because group messages
// have a different receiver model (group vs user) and different
// delivery semantics (broadcast to N members vs push to 1).
type GroupMessage struct {
	ID          string    `json:"id"`
	GroupID     string    `json:"group_id"`
	SenderID    string    `json:"from"`
	Content     string    `json:"content"`
	ContentType string    `json:"content_type"`
	CreatedAt   time.Time `json:"created_at"`
	// RecalledAt is set when the sender recalls the message.
	// nil means the message has not been recalled.
	RecalledAt *time.Time `json:"recalled_at,omitempty"`
}
