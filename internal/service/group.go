package service

import (
	"context"
	"errors"

	"github.com/ciel/im/internal/model"
	"github.com/ciel/im/internal/repository/postgres"
)

// groupStore is the subset of postgres.GroupRepo that GroupService needs.
type groupStore interface {
	Create(ctx context.Context, name string, ownerID string) (*model.Group, error)
	FindByID(ctx context.Context, groupID string) (*model.Group, error)
	UpdateName(ctx context.Context, groupID, name string) error
	Delete(ctx context.Context, groupID string) error
	IsMember(ctx context.Context, groupID, userID string) (bool, error)
	GetMemberRole(ctx context.Context, groupID, userID string) (model.GroupRole, error)
	ListMembers(ctx context.Context, groupID string) ([]model.GroupMember, error)
	ListGroupIDs(ctx context.Context, userID string, offset, limit int) ([]string, error)
	AddMember(ctx context.Context, groupID, userID string) error
	RemoveMember(ctx context.Context, groupID, userID string) error
}

// userExistence is the subset of postgres.UserRepo needed to validate new members.
type userExistence interface {
	FindByID(ctx context.Context, id string) (*model.User, error)
}

// GroupService handles group creation, membership, and lifecycle.
type GroupService struct {
	groupRepo   groupStore
	userRepo    userExistence
	messageRepo *postgres.GroupMessageRepo // used for group message history
}

// NewGroupService creates a GroupService.
func NewGroupService(
	groupRepo *postgres.GroupRepo,
	userRepo *postgres.UserRepo,
	messageRepo *postgres.GroupMessageRepo,
) *GroupService {
	return &GroupService{
		groupRepo:   groupRepo,
		userRepo:    userRepo,
		messageRepo: messageRepo,
	}
}

// Create creates a new group with the owner as the first member. Initial members
// (if any) are added in the same operation.
func (s *GroupService) Create(ctx context.Context, name, ownerID string, memberIDs []string) (*model.Group, error) {
	// Validate members exist and are unique before creating the group.
	unique := make(map[string]bool)
	for _, id := range memberIDs {
		if id == ownerID {
			continue // owner is already added
		}
		if unique[id] {
			continue
		}
		unique[id] = true
		if _, err := s.userRepo.FindByID(ctx, id); err != nil {
			var appErr *model.AppError
			if errors.As(err, &appErr) && errors.Is(appErr.Err, model.ErrNotFound) {
				return nil, model.NewAppError(model.ErrNotFound, "user "+id+" not found")
			}
			return nil, err
		}
	}

	g, err := s.groupRepo.Create(ctx, name, ownerID)
	if err != nil {
		return nil, err
	}

	for id := range unique {
		if err := s.groupRepo.AddMember(ctx, g.ID, id); err != nil {
			// Best-effort: if adding a member fails, the group was already
			// created. Log would go here in production.
			continue
		}
	}
	return g, nil
}

// GetGroup returns group info if the requesting user is a member.
func (s *GroupService) GetGroup(ctx context.Context, groupID, userID string) (*model.Group, error) {
	if err := s.mustBeMember(ctx, groupID, userID); err != nil {
		return nil, err
	}
	return s.groupRepo.FindByID(ctx, groupID)
}

// UpdateName changes the group name. Only the owner can rename.
func (s *GroupService) UpdateName(ctx context.Context, groupID, userID, name string) error {
	if err := s.mustBeOwner(ctx, groupID, userID); err != nil {
		return err
	}
	return s.groupRepo.UpdateName(ctx, groupID, name)
}

// Delete removes a group. Only the owner can disband.
func (s *GroupService) Delete(ctx context.Context, groupID, userID string) error {
	if err := s.mustBeOwner(ctx, groupID, userID); err != nil {
		return err
	}
	return s.groupRepo.Delete(ctx, groupID)
}

// AddMembers adds users to a group. Any existing member can invite.
func (s *GroupService) AddMembers(ctx context.Context, groupID, requestorID string, userIDs []string) error {
	if err := s.mustBeMember(ctx, groupID, requestorID); err != nil {
		return err
	}

	// Validate all users exist before adding any.
	for _, id := range userIDs {
		if _, err := s.userRepo.FindByID(ctx, id); err != nil {
			var appErr *model.AppError
			if errors.As(err, &appErr) && errors.Is(appErr.Err, model.ErrNotFound) {
				return model.NewAppError(model.ErrNotFound, "user "+id+" not found")
			}
			return err
		}
	}

	for _, id := range userIDs {
		if err := s.groupRepo.AddMember(ctx, groupID, id); err != nil {
			var appErr *model.AppError
			if errors.As(err, &appErr) && errors.Is(appErr.Err, model.ErrDuplicate) {
				continue // already a member — not an error
			}
			return err
		}
	}
	return nil
}

// RemoveMember removes a user from a group. The owner can remove anyone;
// any member can remove themselves (leave group). The owner cannot leave —
// they must transfer ownership or disband the group first.
func (s *GroupService) RemoveMember(ctx context.Context, groupID, requestorID, targetID string) error {
	// Self-removal (leave group) is allowed for non-owner members.
	if requestorID == targetID {
		role, err := s.groupRepo.GetMemberRole(ctx, groupID, requestorID)
		if err != nil {
			var appErr *model.AppError
			if errors.As(err, &appErr) && errors.Is(appErr.Err, model.ErrNotFound) {
				return model.NewAppError(model.ErrNotFound, "not a member of this group")
			}
			return err
		}
		if role == model.GroupRoleOwner {
			return model.NewAppError(model.ErrForbidden,
				"owner cannot leave the group; transfer ownership or disband the group first")
		}
		return s.groupRepo.RemoveMember(ctx, groupID, targetID)
	}

	// Removing someone else requires owner role.
	if err := s.mustBeOwner(ctx, groupID, requestorID); err != nil {
		return err
	}
	return s.groupRepo.RemoveMember(ctx, groupID, targetID)
}

// ListGroups returns group IDs that the user belongs to.
func (s *GroupService) ListGroups(ctx context.Context, userID string, offset, limit int) ([]string, error) {
	return s.groupRepo.ListGroupIDs(ctx, userID, offset, limit)
}

// ListMembers returns all members of a group. Requestor must be a member.
func (s *GroupService) ListMembers(ctx context.Context, groupID, userID string) ([]model.GroupMember, error) {
	if err := s.mustBeMember(ctx, groupID, userID); err != nil {
		return nil, err
	}
	return s.groupRepo.ListMembers(ctx, groupID)
}

// GetGroupMessages returns paginated group message history.
func (s *GroupService) GetGroupMessages(ctx context.Context, groupID, userID string, before model.Cursor, limit int) ([]model.GroupMessage, error) {
	if err := s.mustBeMember(ctx, groupID, userID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	msgs, err := s.messageRepo.FindByGroup(ctx, groupID, before.Time(), limit)
	if err != nil {
		return nil, err
	}
	// Mask content of recalled messages (same pattern as private messages).
	for i := range msgs {
		if msgs[i].RecalledAt != nil {
			msgs[i].Content = ""
		}
	}
	return msgs, nil
}

// mustBeMember checks that a user is a member of a group.
func (s *GroupService) mustBeMember(ctx context.Context, groupID, userID string) error {
	isMember, err := s.groupRepo.IsMember(ctx, groupID, userID)
	if err != nil {
		return err
	}
	if !isMember {
		return model.NewAppError(model.ErrForbidden, "not a member of this group")
	}
	return nil
}

// mustBeOwner checks that a user is the owner of a group.
func (s *GroupService) mustBeOwner(ctx context.Context, groupID, userID string) error {
	role, err := s.groupRepo.GetMemberRole(ctx, groupID, userID)
	if err != nil {
		var appErr *model.AppError
		if errors.As(err, &appErr) && errors.Is(appErr.Err, model.ErrNotFound) {
			return model.NewAppError(model.ErrForbidden, "not a member of this group")
		}
		return err
	}
	if role != model.GroupRoleOwner {
		return model.NewAppError(model.ErrForbidden, "only the group owner can perform this action")
	}
	return nil
}
