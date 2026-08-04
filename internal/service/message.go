package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ciel/im/internal/model"
	"github.com/ciel/im/internal/repository/postgres"
	"github.com/ciel/im/internal/ws"
)

// messageStore is the subset of postgres.MessageRepo that MessageService needs.
// Defined as an unexported interface so tests can inject fakes.
type messageStore interface {
	Insert(ctx context.Context, msg *model.Message) error
	FindConversation(ctx context.Context, userA, userB string, before time.Time, limit int) ([]model.Message, error)
	FindUndelivered(ctx context.Context, userID string) ([]model.Message, error)
	UpdateStatus(ctx context.Context, msgID string, status model.MessageStatus) error
}

// friendChecker is the subset of postgres.FriendRepo that MessageService needs.
type friendChecker interface {
	FindByUserAndFriend(ctx context.Context, userID, friendID string) (*model.Friend, error)
}

const (
	// maxContentLength limits message content to prevent abuse.
	// 10000 chars is ~10 KB — enough for long messages without enabling
	// large-payload attacks.
	maxContentLength = 10000

	// defaultContentType is used when the client doesn't specify one.
	defaultContentType = "text"
)

// MessageService handles message business logic: validation, persistence,
// and routing to active WebSocket connections.
//
// It depends on the Hub for routing but uses it through the MessageRouter
// interface rather than the concrete gateway.Hub type. This avoids a
// circular import between the service and gateway packages.
type MessageService struct {
	messageRepo messageStore
	friendRepo  friendChecker
	router      MessageRouter
}

// NewMessageService creates a MessageService.
// The repo parameters are concrete types from the postgres package,
// but the service stores them as narrow interfaces for testability.
func NewMessageService(
	messageRepo *postgres.MessageRepo,
	friendRepo *postgres.FriendRepo,
	router MessageRouter,
) *MessageService {
	return &MessageService{
		messageRepo: messageRepo,
		friendRepo:  friendRepo,
		router:      router,
	}
}

// HandleIncomingMessage processes a raw WebSocket message from a connected user.
// It is intended to be set as Hub.OnMessage callback.
//
// Flow:
//  1. Parse the envelope (must be message.send)
//  2. Validate content
//  3. Check friendship
//  4. Persist to DB
//  5. Route to receiver via Hub (if online)
//  6. Send ACK back to sender
func (s *MessageService) HandleIncomingMessage(senderID string, raw []byte) {
	ctx := context.Background()

	// 1. Parse
	var env ws.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		log.Printf("msg: parse error from user %s: %v", senderID, err)
		s.sendError(senderID, "parse_error", "invalid message format")
		return
	}

	if env.Type != ws.TypeMessageSend {
		log.Printf("msg: unexpected type %s from user %s", env.Type, senderID)
		return
	}

	var payload ws.MessageSendPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		log.Printf("msg: payload parse error from user %s: %v", senderID, err)
		s.sendError(senderID, "parse_error", "invalid payload")
		return
	}

	// 2. Validate
	if err := s.validate(&payload); err != nil {
		s.sendError(senderID, "invalid_message", err.Error())
		return
	}

	// 3. Check friendship (skip for self-messages)
	if senderID != payload.To {
		if err := s.checkFriendship(senderID, payload.To); err != nil {
			s.sendError(senderID, "not_friends", err.Error())
			return
		}
	}

	// 4. Persist
	msg := &model.Message{
		SenderID:    senderID,
		ReceiverID:  payload.To,
		Content:     payload.Content,
		ContentType: payload.ContentType,
	}
	// Use a background context: even if the WebSocket connection drops
	// during this call, we want the message persisted.
	if err := s.messageRepo.Insert(ctx, msg); err != nil {
		log.Printf("msg: insert error: %v", err)
		s.sendError(senderID, "server_error", "failed to save message")
		return
	}

	log.Printf("msg: %s from %s to %s", truncate(msg.ID), senderID, payload.To)

	// 5. Route to receiver
	delivered := s.deliverToUser(payload.To, msg)

	// 6. Send ACK
	status := model.MessageStatusSent
	if delivered {
		status = model.MessageStatusDelivered
		// Update status in DB (fire-and-forget — failure is non-critical,
		// the message will be re-delivered on next connection anyway).
		if err := s.messageRepo.UpdateStatus(ctx, msg.ID, status); err != nil {
			log.Printf("msg: update status error: %v", err)
		}
	}

	s.sendAck(senderID, msg.ID, status)
}

// DeliverOfflineMessages finds undelivered messages for a user and pushes them
// through their WebSocket connections. Called after a new connection is established.
//
// Messages are delivered in chronological order (oldest first). After successful
// delivery, status is updated to 'delivered'.
func (s *MessageService) DeliverOfflineMessages(userID string) {
	ctx := context.Background()
	msgs, err := s.messageRepo.FindUndelivered(ctx, userID)
	if err != nil {
		log.Printf("msg: find undelivered error for user %s: %v", userID, err)
		return
	}

	if len(msgs) == 0 {
		return
	}

	log.Printf("msg: delivering %d offline messages to user %s", len(msgs), userID)

	for _, msg := range msgs {
		s.deliverToUser(userID, &msg)
		// Update status — use background context.
		if err := s.messageRepo.UpdateStatus(ctx, msg.ID, model.MessageStatusDelivered); err != nil {
			log.Printf("msg: update status error for %s: %v", truncate(msg.ID), err)
		}
	}
}

// GetConversation returns paginated message history between two users.
func (s *MessageService) GetConversation(userA, userB string, before model.Cursor, limit int) ([]model.Message, error) {
	ctx := context.Background()
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.messageRepo.FindConversation(ctx, userA, userB, before.Time(), limit)
}

// validate checks that a send request is well-formed.
func (s *MessageService) validate(p *ws.MessageSendPayload) error {
	p.To = strings.TrimSpace(p.To)
	if p.To == "" {
		return errors.New("receiver is required")
	}

	p.Content = strings.TrimSpace(p.Content)
	if p.Content == "" {
		return errors.New("content is empty")
	}
	if len(p.Content) > maxContentLength {
		return fmt.Errorf("content too long (max %d characters)", maxContentLength)
	}

	if p.ContentType == "" {
		p.ContentType = defaultContentType
	}

	return nil
}

// checkFriendship verifies that two users are friends.
func (s *MessageService) checkFriendship(userA, userB string) error {
	friendship, err := s.friendRepo.FindByUserAndFriend(context.Background(), userA, userB)
	if err != nil {
		// Check if it's a "not found" error — they're not friends.
		var appErr *model.AppError
		if errors.As(err, &appErr) && errors.Is(appErr.Err, model.ErrNotFound) {
			return errors.New("you can only send messages to friends")
		}
		return fmt.Errorf("check friendship: %w", err)
	}

	if friendship.Status != model.FriendStatusAccepted {
		return errors.New("you can only send messages to friends")
	}
	return nil
}

// deliverToUser sends a message.new envelope to all active connections of a user.
// Returns true if the user is online (at least one connection is active).
func (s *MessageService) deliverToUser(userID string, msg *model.Message) bool {
	if !s.router.IsOnline(userID) {
		return false
	}

	env := ws.MustEnvelope(ws.TypeMessageNew, ws.MessageNewPayload{
		ID:          msg.ID,
		From:        msg.SenderID,
		Content:     msg.Content,
		ContentType: msg.ContentType,
		CreatedAt:   msg.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	})
	s.router.SendToUser(userID, env)
	return true
}

// sendAck sends a delivery acknowledgement back to the sender.
func (s *MessageService) sendAck(userID, msgID string, status model.MessageStatus) {
	env := ws.MustEnvelope(ws.TypeMessageAck, ws.MessageAckPayload{
		ID:     msgID,
		Status: string(status),
	})
	s.router.SendToUser(userID, env)
}

// sendError sends an error envelope to a user.
func (s *MessageService) sendError(userID, code, message string) {
	env := ws.MustEnvelope(ws.TypeError, ws.ErrorPayload{
		Code:    code,
		Message: message,
	})
	s.router.SendToUser(userID, env)
}

// truncate returns the first 8 characters of s for log previews.
// Safe for strings shorter than 8 characters.
func truncate(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}
