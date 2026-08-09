package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ciel/im/internal/model"
)

// GroupRepo handles group and membership persistence.
type GroupRepo struct {
	pool *pgxpool.Pool
}

// NewGroupRepo creates a GroupRepo backed by the given pool.
func NewGroupRepo(pool *pgxpool.Pool) *GroupRepo {
	return &GroupRepo{pool: pool}
}

// Pool exposes the connection pool for transactional operations.
func (r *GroupRepo) Pool() *pgxpool.Pool {
	return r.pool
}

// Create creates a new group and adds ownerID as the owner member.
// Both operations happen in a single transaction so the group is never
// visible without its owner as a member.
func (r *GroupRepo) Create(ctx context.Context, name string, ownerID string) (*model.Group, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var g model.Group
	err = tx.QueryRow(ctx,
		`INSERT INTO groups (name, owner_id) VALUES ($1, $2) RETURNING id, name, owner_id, created_at`,
		name, ownerID,
	).Scan(&g.ID, &g.Name, &g.OwnerID, &g.CreatedAt)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, $3)`,
		g.ID, ownerID, model.GroupRoleOwner,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &g, nil
}

// FindByID returns a group by its ID.
func (r *GroupRepo) FindByID(ctx context.Context, groupID string) (*model.Group, error) {
	const query = `SELECT id, name, owner_id, created_at FROM groups WHERE id = $1`
	var g model.Group
	err := r.pool.QueryRow(ctx, query, groupID).Scan(&g.ID, &g.Name, &g.OwnerID, &g.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, model.NewAppError(model.ErrNotFound, "group not found")
		}
		return nil, err
	}
	return &g, nil
}

// UpdateName changes the group name. Only the owner should be authorized
// by the caller before invoking this method.
func (r *GroupRepo) UpdateName(ctx context.Context, groupID, name string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE groups SET name = $1 WHERE id = $2`, name, groupID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return model.NewAppError(model.ErrNotFound, "group not found")
	}
	return nil
}

// Delete removes a group by ID. ON DELETE CASCADE handles group_members cleanup.
func (r *GroupRepo) Delete(ctx context.Context, groupID string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, groupID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return model.NewAppError(model.ErrNotFound, "group not found")
	}
	return nil
}

// IsMember checks whether a user is a member of a group.
func (r *GroupRepo) IsMember(ctx context.Context, groupID, userID string) (bool, error) {
	const query = `SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2)`
	var exists bool
	err := r.pool.QueryRow(ctx, query, groupID, userID).Scan(&exists)
	return exists, err
}

// GetMemberRole returns the role of a user in a group. If the user is not a
// member, it returns an empty string and ErrNotFound.
func (r *GroupRepo) GetMemberRole(ctx context.Context, groupID, userID string) (model.GroupRole, error) {
	const query = `SELECT role FROM group_members WHERE group_id = $1 AND user_id = $2`
	var role model.GroupRole
	err := r.pool.QueryRow(ctx, query, groupID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", model.NewAppError(model.ErrNotFound, "not a member")
		}
		return "", err
	}
	return role, nil
}

// ListMembers returns all members of a group.
func (r *GroupRepo) ListMembers(ctx context.Context, groupID string) ([]model.GroupMember, error) {
	const query = `SELECT group_id, user_id, role, joined_at FROM group_members WHERE group_id = $1 ORDER BY joined_at ASC`
	rows, err := r.pool.Query(ctx, query, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []model.GroupMember
	for rows.Next() {
		var m model.GroupMember
		if err := rows.Scan(&m.GroupID, &m.UserID, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// ListGroupIDs returns the IDs of groups a user belongs to.
// Paginated to support users in many groups.
func (r *GroupRepo) ListGroupIDs(ctx context.Context, userID string, offset, limit int) ([]string, error) {
	const query = `
		SELECT group_id FROM group_members
		WHERE user_id = $1
		ORDER BY joined_at DESC
		LIMIT $2 OFFSET $3
	`
	rows, err := r.pool.Query(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// AddMember adds a user to a group. The caller must authorize the addition.
func (r *GroupRepo) AddMember(ctx context.Context, groupID, userID string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, $3)`,
		groupID, userID, model.GroupRoleMember,
	)
	if err != nil {
		if isDuplicate(err) {
			return model.NewAppError(model.ErrDuplicate, "user is already a member")
		}
		return err
	}
	return nil
}

// RemoveMember removes a user from a group.
func (r *GroupRepo) RemoveMember(ctx context.Context, groupID, userID string) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`,
		groupID, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return model.NewAppError(model.ErrNotFound, "not a member")
	}
	return nil
}

// CreateTx creates a group within an existing transaction.
func (r *GroupRepo) CreateTx(ctx context.Context, tx pgx.Tx, name string, ownerID string) (*model.Group, error) {
	var g model.Group
	err := tx.QueryRow(ctx,
		`INSERT INTO groups (name, owner_id) VALUES ($1, $2) RETURNING id, name, owner_id, created_at`,
		name, ownerID,
	).Scan(&g.ID, &g.Name, &g.OwnerID, &g.CreatedAt)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, $3)`,
		g.ID, ownerID, model.GroupRoleOwner,
	)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// AddMemberTx adds a user to a group within an existing transaction.
func (r *GroupRepo) AddMemberTx(ctx context.Context, tx pgx.Tx, groupID, userID string, role model.GroupRole) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, $3)`,
		groupID, userID, role,
	)
	if err != nil {
		if isDuplicate(err) {
			return model.NewAppError(model.ErrDuplicate, "user is already a member")
		}
		return err
	}
	return nil
}

// Ensure DDL-configured defaults work correctly.
var _ = (time.Time{}).IsZero
