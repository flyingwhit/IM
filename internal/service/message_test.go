package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ciel/im/internal/model"
	"github.com/ciel/im/internal/ws"
)

// --- Test doubles ---

// fakeMessageStore implements messageStore for testing.
type fakeMessageStore struct {
	mu       sync.Mutex
	messages []model.Message
	insertFn func(msg *model.Message) error
}

func (r *fakeMessageStore) Insert(_ context.Context, msg *model.Message) error {
	if r.insertFn != nil {
		return r.insertFn(msg)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	msg.ID = "msg-" + string(rune(len(r.messages)+'a'))
	msg.CreatedAt = time.Now()
	r.messages = append(r.messages, *msg)
	return nil
}

func (r *fakeMessageStore) FindConversation(_ context.Context, _, _ string, _ time.Time, _ int) ([]model.Message, error) {
	return nil, nil
}

func (r *fakeMessageStore) FindUndelivered(_ context.Context, _ string) ([]model.Message, error) {
	return nil, nil
}

func (r *fakeMessageStore) UpdateStatus(_ context.Context, _ string, _ model.MessageStatus) error {
	return nil
}

// fakeFriendChecker implements friendChecker for testing.
type fakeFriendChecker struct {
	friendships map[string]model.FriendStatus // key: "userA:userB"
}

func newFakeFriendChecker() *fakeFriendChecker {
	return &fakeFriendChecker{friendships: make(map[string]model.FriendStatus)}
}

func (r *fakeFriendChecker) addFriend(userA, userB string) {
	r.friendships[userA+":"+userB] = model.FriendStatusAccepted
	r.friendships[userB+":"+userA] = model.FriendStatusAccepted
}

func (r *fakeFriendChecker) FindByUserAndFriend(_ context.Context, userID, friendID string) (*model.Friend, error) {
	status, ok := r.friendships[userID+":"+friendID]
	if !ok {
		return nil, model.NewAppError(model.ErrNotFound, "not found")
	}
	return &model.Friend{Status: status}, nil
}

// fakeRouter implements MessageRouter for testing.
type fakeRouter struct {
	mu     sync.Mutex
	sent   map[string][]*ws.Envelope // userID → envelopes
	online map[string]bool
}

func newFakeRouter() *fakeRouter {
	return &fakeRouter{
		sent:   make(map[string][]*ws.Envelope),
		online: make(map[string]bool),
	}
}

func (r *fakeRouter) SendToUser(userID string, env *ws.Envelope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent[userID] = append(r.sent[userID], env)
}

func (r *fakeRouter) IsOnline(userID string) bool {
	return r.online[userID]
}

func (r *fakeRouter) lastEnvelope(userID string) *ws.Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	envs := r.sent[userID]
	if len(envs) == 0 {
		return nil
	}
	return envs[len(envs)-1]
}

func (r *fakeRouter) envelopes(userID string) []*ws.Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sent[userID]
}

// --- Helpers ---

func makeMessageSend(to, content string) []byte {
	env := ws.MustEnvelope(ws.TypeMessageSend, ws.MessageSendPayload{
		To:      to,
		Content: content,
	})
	data, _ := json.Marshal(env)
	return data
}

// mustMessageService creates a MessageService with fake dependencies.
// Uses unexported fields directly for test injection.
func mustMessageService(router *fakeRouter, friends *fakeFriendChecker) *MessageService {
	return &MessageService{
		messageRepo: &fakeMessageStore{},
		friendRepo:  friends,
		router:      router,
	}
}

// --- Tests ---

func TestMessageService_HandleIncomingMessage_Success(t *testing.T) {
	friends := newFakeFriendChecker()
	friends.addFriend("alice", "bob")
	router := newFakeRouter()
	router.online["bob"] = true
	svc := mustMessageService(router, friends)

	raw := makeMessageSend("bob", "Hello Bob!")
	svc.HandleIncomingMessage("alice", raw)

	// Bob should receive message.new.
	bobMsg := router.lastEnvelope("bob")
	if bobMsg == nil {
		t.Fatal("bob should have received a message")
	}
	if bobMsg.Type != ws.TypeMessageNew {
		t.Errorf("bob should get message.new, got %s", bobMsg.Type)
	}

	// Alice should receive ACK with status=delivered.
	aliceMsg := router.lastEnvelope("alice")
	if aliceMsg == nil {
		t.Fatal("alice should have received an ACK")
	}
	if aliceMsg.Type != ws.TypeMessageAck {
		t.Errorf("alice should get message.ack, got %s", aliceMsg.Type)
	}

	var ack ws.MessageAckPayload
	if err := json.Unmarshal(aliceMsg.Payload, &ack); err != nil {
		t.Fatalf("unmarshal ack: %v", err)
	}
	if ack.Status != string(model.MessageStatusDelivered) {
		t.Errorf("ack status = %s, want %s", ack.Status, model.MessageStatusDelivered)
	}
}

func TestMessageService_HandleIncomingMessage_ReceiverOffline(t *testing.T) {
	friends := newFakeFriendChecker()
	friends.addFriend("alice", "bob")
	router := newFakeRouter()
	// bob is NOT online
	svc := mustMessageService(router, friends)

	raw := makeMessageSend("bob", "Hello?")
	svc.HandleIncomingMessage("alice", raw)

	// Bob should NOT receive message.new.
	if envs := router.envelopes("bob"); len(envs) > 0 {
		t.Error("bob is offline, should not receive message")
	}

	// Alice should receive ACK with status=sent.
	aliceMsg := router.lastEnvelope("alice")
	if aliceMsg == nil {
		t.Fatal("alice should have received an ACK")
	}
	var ack ws.MessageAckPayload
	json.Unmarshal(aliceMsg.Payload, &ack)
	if ack.Status != string(model.MessageStatusSent) {
		t.Errorf("ack status = %s, want %s", ack.Status, model.MessageStatusSent)
	}
}

func TestMessageService_HandleIncomingMessage_NotFriends(t *testing.T) {
	friends := newFakeFriendChecker()
	// alice and bob are NOT friends
	router := newFakeRouter()
	svc := mustMessageService(router, friends)

	raw := makeMessageSend("bob", "Hello stranger!")
	svc.HandleIncomingMessage("alice", raw)

	// Alice should receive error.
	errMsg := router.lastEnvelope("alice")
	if errMsg == nil {
		t.Fatal("alice should have received an error")
	}
	if errMsg.Type != ws.TypeError {
		t.Errorf("expected error type, got %s", errMsg.Type)
	}

	var errPayload ws.ErrorPayload
	json.Unmarshal(errMsg.Payload, &errPayload)
	if errPayload.Code != "not_friends" {
		t.Errorf("error code = %s, want not_friends", errPayload.Code)
	}
}

func TestMessageService_HandleIncomingMessage_SelfMessage(t *testing.T) {
	friends := newFakeFriendChecker()
	router := newFakeRouter()
	router.online["alice"] = true
	svc := mustMessageService(router, friends)

	raw := makeMessageSend("alice", "Note to self")
	svc.HandleIncomingMessage("alice", raw)

	// Self-messages skip friend check — should succeed.
	ack := router.lastEnvelope("alice")
	if ack == nil || ack.Type != ws.TypeMessageAck {
		t.Fatal("self-message should succeed without friendship")
	}
}

func TestMessageService_HandleIncomingMessage_EmptyContent(t *testing.T) {
	friends := newFakeFriendChecker()
	friends.addFriend("alice", "bob")
	router := newFakeRouter()
	svc := mustMessageService(router, friends)

	raw := makeMessageSend("bob", "   ")
	svc.HandleIncomingMessage("alice", raw)

	errMsg := router.lastEnvelope("alice")
	if errMsg == nil || errMsg.Type != ws.TypeError {
		t.Fatal("empty content should produce an error")
	}

	var errPayload ws.ErrorPayload
	json.Unmarshal(errMsg.Payload, &errPayload)
	if errPayload.Code != "invalid_message" {
		t.Errorf("error code = %s, want invalid_message", errPayload.Code)
	}
}

func TestMessageService_HandleIncomingMessage_ContentTooLong(t *testing.T) {
	friends := newFakeFriendChecker()
	friends.addFriend("alice", "bob")
	router := newFakeRouter()
	svc := mustMessageService(router, friends)

	longContent := strings.Repeat("x", maxContentLength+1)
	raw := makeMessageSend("bob", longContent)
	svc.HandleIncomingMessage("alice", raw)

	errMsg := router.lastEnvelope("alice")
	if errMsg == nil || errMsg.Type != ws.TypeError {
		t.Fatal("too-long content should produce an error")
	}
}

func TestMessageService_HandleIncomingMessage_InsertError(t *testing.T) {
	friends := newFakeFriendChecker()
	friends.addFriend("alice", "bob")
	router := newFakeRouter()
	svc := &MessageService{
		messageRepo: &fakeMessageStore{
			insertFn: func(msg *model.Message) error {
				return errors.New("db down")
			},
		},
		friendRepo: friends,
		router:     router,
	}

	raw := makeMessageSend("bob", "hello")
	svc.HandleIncomingMessage("alice", raw)

	errMsg := router.lastEnvelope("alice")
	if errMsg == nil || errMsg.Type != ws.TypeError {
		t.Fatal("insert error should produce server_error")
	}
}

func TestMessageService_HandleIncomingMessage_InvalidJSON(t *testing.T) {
	friends := newFakeFriendChecker()
	router := newFakeRouter()
	svc := mustMessageService(router, friends)

	svc.HandleIncomingMessage("alice", []byte("not json"))

	errMsg := router.lastEnvelope("alice")
	if errMsg == nil || errMsg.Type != ws.TypeError {
		t.Fatal("invalid JSON should produce parse_error")
	}
}

func TestMessageService_DeliverOfflineMessages(t *testing.T) {
	friends := newFakeFriendChecker()
	router := newFakeRouter()
	router.online["bob"] = true

	svc := &MessageService{
		messageRepo: &fakeMessageStore{},
		friendRepo:  friends,
		router:      router,
	}

	// No undelivered messages — should not send anything.
	svc.DeliverOfflineMessages("bob")
	if envs := router.envelopes("bob"); len(envs) > 0 {
		t.Error("no undelivered messages, should not send anything")
	}
}

func TestMessageService_Validate(t *testing.T) {
	svc := mustMessageService(newFakeRouter(), newFakeFriendChecker())

	tests := []struct {
		name    string
		payload ws.MessageSendPayload
		wantErr bool
	}{
		{"empty receiver", ws.MessageSendPayload{To: "", Content: "hi"}, true},
		{"empty content", ws.MessageSendPayload{To: "bob", Content: ""}, true},
		{"whitespace content", ws.MessageSendPayload{To: "bob", Content: "  "}, true},
		{"too long", ws.MessageSendPayload{To: "bob", Content: strings.Repeat("x", maxContentLength+1)}, true},
		{"valid", ws.MessageSendPayload{To: "bob", Content: "hello"}, false},
		{"default content_type", ws.MessageSendPayload{To: "bob", Content: "hi", ContentType: ""}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.payload
			err := svc.validate(&p)
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

// Compile-time interface satisfaction checks.
var _ messageStore = (*fakeMessageStore)(nil)
var _ friendChecker = (*fakeFriendChecker)(nil)
var _ MessageRouter = (*fakeRouter)(nil)
