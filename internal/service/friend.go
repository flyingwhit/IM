package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ciel/im/internal/model"
	"github.com/ciel/im/internal/repository/postgres"
)

// FriendService handles friend relationship business logic.
type FriendService struct {
	friendRepo *postgres.FriendRepo
	userRepo   *postgres.UserRepo
}

func NewFriendService(friendRepo *postgres.FriendRepo, userRepo *postgres.UserRepo) *FriendService {
	return &FriendService{friendRepo: friendRepo, userRepo: userRepo}
}

// SendRequest sends a friend request from userID to targetID.
func (s *FriendService) SendRequest(ctx context.Context, userID, targetID string) (*model.Friend, error) {
	// Prevent self-friending
	if userID == targetID {
		return nil, model.NewAppError(model.ErrInvalidInput, "cannot send friend request to yourself")
	}

	// Verify target exists
	if _, err := s.userRepo.FindByID(ctx, targetID); err != nil {
		return nil, err
	}

	// Check for existing relationship in either direction
	// (pending, accepted, or blocked)
	existing, err := s.friendRepo.FindByUserAndFriend(ctx, userID, targetID)
	if err == nil && existing != nil {
		return nil, model.NewAppError(model.ErrConflict, "friend relationship already exists")
	}
	// Also check reverse direction
	existing, err = s.friendRepo.FindByUserAndFriend(ctx, targetID, userID)
	if err == nil && existing != nil {
		return nil, model.NewAppError(model.ErrConflict, "friend relationship already exists")
	}

	f := &model.Friend{
		UserID:   userID,
		FriendID: targetID,
		Status:   model.FriendStatusPending,
	}
	if err := s.friendRepo.Create(ctx, f); err != nil {
		return nil, fmt.Errorf("create friend request: %w", err)
	}
	return f, nil
}

// AcceptRequest accepts a pending friend request.
// It updates the existing record and creates the reverse record
// so both users can query their friend lists efficiently.
func (s *FriendService) AcceptRequest(ctx context.Context, requestID, userID string) (*model.Friend, error) {
	f, err := s.friendRepo.FindByID(ctx, requestID)
	if err != nil {
		return nil, err
	}

	// Only the target of the request can accept it
	if f.FriendID != userID {
		return nil, model.NewAppError(model.ErrForbidden, "you cannot accept this request")
	}

	if f.Status != model.FriendStatusPending {
		return nil, model.NewAppError(model.ErrInvalidInput, "request is not pending")
	}

	// Wrap the two writes in a transaction so both or neither succeed.
	err = postgres.RunTx(ctx, s.friendRepo.Pool(), func(tx pgx.Tx) error {
		if err := s.friendRepo.UpdateStatusTx(ctx, tx, f.ID, model.FriendStatusAccepted); err != nil {
			return fmt.Errorf("update request status: %w", err)
		}
		reverse := &model.Friend{
			UserID:   f.FriendID,
			FriendID: f.UserID,
			Status:   model.FriendStatusAccepted,
		}
		if err := s.friendRepo.CreateTx(ctx, tx, reverse); err != nil {
			return fmt.Errorf("create reverse friend record: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	f.Status = model.FriendStatusAccepted

	return f, nil
}

// RejectRequest rejects a pending friend request by deleting it.
func (s *FriendService) RejectRequest(ctx context.Context, requestID, userID string) error {
	f, err := s.friendRepo.FindByID(ctx, requestID)
	if err != nil {
		return err
	}

	if f.FriendID != userID {
		return model.NewAppError(model.ErrForbidden, "you cannot reject this request")
	}

	if f.Status != model.FriendStatusPending {
		return model.NewAppError(model.ErrInvalidInput, "request is not pending")
	}

	return s.friendRepo.Delete(ctx, f.ID)
}

// RemoveFriend removes a friend relationship (both directions).
func (s *FriendService) RemoveFriend(ctx context.Context, friendshipID, userID string) error {
	f, err := s.friendRepo.FindByID(ctx, friendshipID)
	if err != nil {
		return err
	}

	// Only a party to the friendship can remove it
	if f.UserID != userID && f.FriendID != userID {
		return model.NewAppError(model.ErrForbidden, "not your friendship")
	}

	// Delete both records in a transaction.
	reverse, reverseErr := s.friendRepo.FindByUserAndFriend(ctx, f.FriendID, f.UserID)

	return postgres.RunTx(ctx, s.friendRepo.Pool(), func(tx pgx.Tx) error {
		if err := s.friendRepo.DeleteTx(ctx, tx, f.ID); err != nil {
			return err
		}
		if reverseErr == nil && reverse != nil {
			if err := s.friendRepo.DeleteTx(ctx, tx, reverse.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListFriends returns the user's accepted friends with pagination.
func (s *FriendService) ListFriends(ctx context.Context, userID string, offset, limit int) ([]model.FriendWithUser, error) {
	return s.friendRepo.ListFriends(ctx, userID, offset, limit)
}

// ListPendingRequests returns incoming pending friend requests with pagination.
func (s *FriendService) ListPendingRequests(ctx context.Context, userID string, offset, limit int) ([]model.FriendWithUser, error) {
	return s.friendRepo.ListPendingRequests(ctx, userID, offset, limit)
}
