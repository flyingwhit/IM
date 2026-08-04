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
	FindByID(ctx context.Context, msgID string) (*model.Message, error)
	FindConversation(ctx context.Context, userA, userB string, before time.Time, limit int) ([]model.Message, error)
	FindUndelivered(ctx context.Context, userID string) ([]model.Message, error)
	FindRecalledAfterDelivered(ctx context.Context, userID string, since time.Time) ([]model.Message, error)
	UpdateStatus(ctx context.Context, msgID string, status model.MessageStatus) error
	UpdateRecall(ctx context.Context, msgID string) error
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

	// recallWindow is the maximum time after sending that a message can be recalled.
	// 2 minutes is the industry standard (WeChat, Slack).
	recallWindow = 2 * time.Minute

	// recallNotificationWindow is how far back we look for recalled messages
	// on reconnect. A user who was offline for longer than this won't get
	// message.recalled push notifications — but their conversation history
	// (GetConversation) always reflects the current state.
	recallNotificationWindow = 5 * time.Minute

	// recalledPlaceholder replaces the content of recalled messages in API responses.
	recalledPlaceholder = ""
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
// It dispatches by envelope type:
//   - message.send   → handleSend
//   - message.recall → handleRecall
func (s *MessageService) HandleIncomingMessage(senderID string, raw []byte) {
	// 1. Parse envelope
	var env ws.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		log.Printf("msg: parse error from user %s: %v", senderID, err)
		s.sendError(senderID, "parse_error", "invalid message format")
		return
	}

	switch env.Type {
	case ws.TypeMessageSend:
		s.handleSend(senderID, env)
	case ws.TypeMessageRecall:
		s.handleRecall(senderID, env)
	default:
		log.Printf("msg: unexpected type %s from user %s", env.Type, senderID)
	}
}

// SendMessage persists and routes a message. It's the shared core used by
// both the WebSocket handler (handleSend) and the REST handler (SendMessage).
//
// Returns the persisted message. Delivers to receiver via WebSocket if online.
func (s *MessageService) SendMessage(ctx context.Context, senderID, to, content, contentType string) (*model.Message, error) {
	payload := &ws.MessageSendPayload{To: to, Content: content, ContentType: contentType}

	// 1. Validate
	if err := s.validate(payload); err != nil {
		return nil, model.NewAppError(model.ErrInvalidInput, err.Error())
	}

	// 2. Check friendship (skip for self-messages)
	if senderID != to {
		if err := s.checkFriendship(senderID, to); err != nil {
			return nil, model.NewAppError(model.ErrForbidden, err.Error())
		}
	}

	// 3. Persist
	msg := &model.Message{
		SenderID:    senderID,
		ReceiverID:  to,
		Content:     content,
		ContentType: contentType,
	}
	if err := s.messageRepo.Insert(ctx, msg); err != nil {
		return nil, fmt.Errorf("insert message: %w", err)
	}
	msg.Status = model.MessageStatusSent

	log.Printf("msg: %s from %s to %s", truncate(msg.ID), senderID, to)

	// 4. Route to receiver
	if s.deliverToUser(to, msg) {
		msg.Status = model.MessageStatusDelivered
		if err := s.messageRepo.UpdateStatus(ctx, msg.ID, model.MessageStatusDelivered); err != nil {
			log.Printf("msg: update status error: %v", err)
		}
	}

	return msg, nil
}

// handleSend processes a message.send WebSocket envelope.
//
// Flow:
//  1. Parse the payload
//  2. Call SendMessage (validate, check friendship, persist, route)
//  3. Send ACK back to sender via WebSocket
func (s *MessageService) handleSend(senderID string, env ws.Envelope) {
	var payload ws.MessageSendPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		log.Printf("msg: payload parse error from user %s: %v", senderID, err)
		s.sendError(senderID, "parse_error", "invalid payload")
		return
	}

	msg, err := s.SendMessage(context.Background(), senderID, payload.To, payload.Content, payload.ContentType)
	if err != nil {
		var appErr *model.AppError
		if errors.As(err, &appErr) {
			switch {
			case errors.Is(appErr.Err, model.ErrInvalidInput):
				s.sendError(senderID, "invalid_message", appErr.Message)
			case errors.Is(appErr.Err, model.ErrForbidden):
				s.sendError(senderID, "not_friends", appErr.Message)
			default:
				s.sendError(senderID, "server_error", appErr.Message)
			}
			return
		}
		s.sendError(senderID, "server_error", "failed to send message")
		return
	}

	// ACK status comes from SendMessage, which already determined
	// whether the receiver was online at delivery time. Using msg.Status
	// avoids a TOCTOU race between delivery and this check.
	s.sendAck(senderID, msg.ID, msg.Status)
}

// handleRecall processes a message.recall envelope.
//
// Flow:
//  1. Parse the payload
//  2. Look up the message from DB
//  3. Authorize (must be the sender)
//  4. Check time limit (2 minutes)
//  5. Update recalled_at in DB
//  6. Broadcast message.recalled to sender and receiver
func (s *MessageService) handleRecall(senderID string, env ws.Envelope) {
	ctx := context.Background()

	var payload ws.MessageRecallPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		log.Printf("recall: payload parse error from user %s: %v", senderID, err)
		s.sendError(senderID, "parse_error", "invalid recall payload")
		return
	}

	if payload.MessageID == "" {
		s.sendError(senderID, "invalid_message", "message_id is required")
		return
	}

	// 2. Look up the message
	msg, err := s.messageRepo.FindByID(ctx, payload.MessageID)
	if err != nil {
		var appErr *model.AppError
		if errors.As(err, &appErr) && errors.Is(appErr.Err, model.ErrNotFound) {
			s.sendError(senderID, "message_not_found", "message does not exist")
			return
		}
		log.Printf("recall: find message error: %v", err)
		s.sendError(senderID, "server_error", "failed to look up message")
		return
	}

	// 3. Authorize: only the sender can recall
	if msg.SenderID != senderID {
		s.sendError(senderID, "not_sender", "you can only recall your own messages")
		return
	}

	// 4. Check if already recalled (idempotent)
	if msg.RecalledAt != nil {
		// Already recalled — return success without updating again.
		s.sendRecallNotification(senderID, msg.ReceiverID, msg.ID, *msg.RecalledAt)
		return
	}

	// 5. Check time limit
	if time.Since(msg.CreatedAt) > recallWindow {
		s.sendError(senderID, "recall_time_exceeded",
			fmt.Sprintf("messages can only be recalled within %v", recallWindow))
		return
	}

	// 6. Persist the recall
	if err := s.messageRepo.UpdateRecall(ctx, msg.ID); err != nil {
		var appErr *model.AppError
		if errors.As(err, &appErr) && errors.Is(appErr.Err, model.ErrNotFound) {
			s.sendError(senderID, "message_not_found", "message does not exist")
			return
		}
		log.Printf("recall: update error for msg %s: %v", truncate(msg.ID), err)
		s.sendError(senderID, "server_error", "failed to recall message")
		return
	}

	now := time.Now()
	log.Printf("recall: msg %s recalled by %s", truncate(msg.ID), senderID)

	// 7. Broadcast to both parties
	s.sendRecallNotification(senderID, msg.ReceiverID, msg.ID, now)
}

// sendRecallNotification broadcasts message.recalled to both the sender
// and the receiver.
func (s *MessageService) sendRecallNotification(senderID, receiverID, msgID string, recalledAt time.Time) {
	env := ws.MustEnvelope(ws.TypeMessageRecalled, ws.MessageRecalledPayload{
		MessageID:  msgID,
		RecalledAt: recalledAt.Format("2006-01-02T15:04:05Z07:00"),
	})
	// Send to sender (all devices) — so other devices of the sender update UI.
	s.router.SendToUser(senderID, env)
	// Send to receiver (if online) — so receiver sees the recall immediately.
	if s.router.IsOnline(receiverID) {
		s.router.SendToUser(receiverID, env)
	}
}

// DeliverOfflineMessages delivers undelivered messages and recall notifications
// to a user who just connected. Called from Hub.OnConnect.
//
// Two-phase delivery:
//  1. Find messages with status='sent' — these were never delivered. Push them
//     as message.new and mark them delivered.
//  2. Find messages that were delivered but recalled while the user was offline.
//     Push message.recalled notifications so the client can update its local cache.
func (s *MessageService) DeliverOfflineMessages(userID string) {
	ctx := context.Background()

	// 1. Deliver messages that were never delivered.
	msgs, err := s.messageRepo.FindUndelivered(ctx, userID)
	if err != nil {
		log.Printf("msg: find undelivered error for user %s: %v", userID, err)
		return
	}
	if len(msgs) > 0 {
		log.Printf("msg: delivering %d offline messages to user %s", len(msgs), userID)
		for _, msg := range msgs {
			s.deliverToUser(userID, &msg)
			if err := s.messageRepo.UpdateStatus(ctx, msg.ID, model.MessageStatusDelivered); err != nil {
				log.Printf("msg: update status error for %s: %v", truncate(msg.ID), err)
			}
		}
	}

	// 2. Push recall notifications for delivered messages that were
	//    recalled while the user was offline.
	since := time.Now().Add(-recallNotificationWindow)
	recalled, err := s.messageRepo.FindRecalledAfterDelivered(ctx, userID, since)
	if err != nil {
		log.Printf("msg: find recalled notifications error for user %s: %v", userID, err)
		return
	}
	if len(recalled) > 0 {
		log.Printf("msg: pushing %d recall notifications to user %s", len(recalled), userID)
		for _, msg := range recalled {
			if msg.RecalledAt != nil {
				// Only notify the reconnecting user (receiver). The sender
					// already received the recall notification when they recalled
					// the message. Using a targeted send avoids a duplicate
					// push to the sender.
					s.sendRecallToUser(userID, msg.ID, *msg.RecalledAt)
			}
		}
	}
}

// GetConversation returns paginated message history between two users.
// Recalled messages have their content replaced with an empty string —
// the client checks the recalled_at field to display a placeholder.
func (s *MessageService) GetConversation(userA, userB string, before model.Cursor, limit int) ([]model.Message, error) {
	ctx := context.Background()
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	msgs, err := s.messageRepo.FindConversation(ctx, userA, userB, before.Time(), limit)
	if err != nil {
		return nil, err
	}
	// Mask content of recalled messages. We keep the original in the DB
	// for audit purposes but never expose it through the API.
	for i := range msgs {
		if msgs[i].RecalledAt != nil {
			msgs[i].Content = recalledPlaceholder
		}
	}
	return msgs, nil
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

	content := msg.Content
	var recalledAt string
	if msg.RecalledAt != nil {
		content = recalledPlaceholder
		recalledAt = msg.RecalledAt.Format("2006-01-02T15:04:05Z07:00")
	}

	env := ws.MustEnvelope(ws.TypeMessageNew, ws.MessageNewPayload{
		ID:          msg.ID,
		From:        msg.SenderID,
		Content:     content,
		ContentType: msg.ContentType,
		CreatedAt:   msg.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		RecalledAt:  recalledAt,
	})
	s.router.SendToUser(userID, env)
	return true
}

// sendRecallToUser sends a message.recalled notification to a single user.
// Unlike sendRecallNotification (which broadcasts to both parties), this
// targets only one user. Used when delivering recall notifications during
// reconnection — the sender already received the notification when they
// originally recalled the message.
func (s *MessageService) sendRecallToUser(userID, msgID string, recalledAt time.Time) {
	env := ws.MustEnvelope(ws.TypeMessageRecalled, ws.MessageRecalledPayload{
		MessageID:  msgID,
		RecalledAt: recalledAt.Format("2006-01-02T15:04:05Z07:00"),
	})
	s.router.SendToUser(userID, env)
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
