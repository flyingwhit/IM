package model

import "time"

// FriendStatus represents the state of a friend relationship.
type FriendStatus string

const (
	FriendStatusPending  FriendStatus = "pending"
	FriendStatusAccepted FriendStatus = "accepted"
	FriendStatusBlocked  FriendStatus = "blocked"
)

// Friend represents a relationship between two users.
type Friend struct {
	ID        string       `json:"id"`
	UserID    string       `json:"user_id"`
	FriendID  string       `json:"friend_id"`
	Status    FriendStatus `json:"status"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// FriendWithUser includes the friend's user info for API responses.
type FriendWithUser struct {
	Friend
	FriendUser User `json:"friend_user"`
}
