package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ciel/im/internal/model"
)

// FriendRepo handles friend relationship persistence.
type FriendRepo struct {
	pool *pgxpool.Pool
}

func NewFriendRepo(pool *pgxpool.Pool) *FriendRepo {
	return &FriendRepo{pool: pool}
}

// Pool exposes the underlying connection pool for transaction use.
func (r *FriendRepo) Pool() *pgxpool.Pool {
	return r.pool
}

// Create inserts a new friend relationship (e.g. a friend request).
func (r *FriendRepo) Create(ctx context.Context, friend *model.Friend) error {
	const query = `
		INSERT INTO friends (user_id, friend_id, status)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, updated_at
	`
	err := r.pool.QueryRow(ctx, query,
		friend.UserID, friend.FriendID, friend.Status,
	).Scan(&friend.ID, &friend.CreatedAt, &friend.UpdatedAt)

	if isDuplicate(err) {
		return model.NewAppError(model.ErrDuplicate, "friend request already exists")
	}
	return err
}

// FindByID returns a friend relationship by ID.
func (r *FriendRepo) FindByID(ctx context.Context, id string) (*model.Friend, error) {
	const query = `
		SELECT id, user_id, friend_id, status, created_at, updated_at
		FROM friends WHERE id = $1
	`
	f := &model.Friend{}
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&f.ID, &f.UserID, &f.FriendID, &f.Status, &f.CreatedAt, &f.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, model.NewAppError(model.ErrNotFound, "friend relationship not found")
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

// FindByUserAndFriend looks up a relationship between two specific users.
func (r *FriendRepo) FindByUserAndFriend(ctx context.Context, userID, friendID string) (*model.Friend, error) {
	const query = `
		SELECT id, user_id, friend_id, status, created_at, updated_at
		FROM friends WHERE user_id = $1 AND friend_id = $2
	`
	f := &model.Friend{}
	err := r.pool.QueryRow(ctx, query, userID, friendID).Scan(
		&f.ID, &f.UserID, &f.FriendID, &f.Status, &f.CreatedAt, &f.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, model.NewAppError(model.ErrNotFound, "relationship not found")
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

// UpdateStatus changes the status of a friend relationship.
func (r *FriendRepo) UpdateStatus(ctx context.Context, id string, status model.FriendStatus) error {
	const query = `UPDATE friends SET status = $1, updated_at = NOW() WHERE id = $2`
	_, err := r.pool.Exec(ctx, query, status, id)
	return err
}

// Delete removes a friend relationship.
func (r *FriendRepo) Delete(ctx context.Context, id string) error {
	const query = `DELETE FROM friends WHERE id = $1`
	_, err := r.pool.Exec(ctx, query, id)
	return err
}

// DeleteTx is like Delete but runs on an existing transaction.
func (r *FriendRepo) DeleteTx(ctx context.Context, tx pgx.Tx, id string) error {
	const query = `DELETE FROM friends WHERE id = $1`
	_, err := tx.Exec(ctx, query, id)
	return err
}

// ListFriends returns accepted friends for a user with pagination.
// It JOINs the users table to return friend info directly.
func (r *FriendRepo) ListFriends(ctx context.Context, userID string, offset, limit int) ([]model.FriendWithUser, error) {
	const query = `
		SELECT f.id, f.user_id, f.friend_id, f.status, f.created_at, f.updated_at,
			   u.id, u.username, u.email, u.password_hash, u.nickname, u.avatar_url, u.created_at, u.updated_at
		FROM friends f
		JOIN users u ON u.id = f.friend_id
		WHERE f.user_id = $1 AND f.status = 'accepted'
		ORDER BY u.username
		LIMIT $2 OFFSET $3
	`
	return r.queryFriends(ctx, query, userID, limit, offset)
}

// ListPendingRequests returns pending friend requests received by the user,
// with pagination.
func (r *FriendRepo) ListPendingRequests(ctx context.Context, userID string, offset, limit int) ([]model.FriendWithUser, error) {
	const query = `
		SELECT f.id, f.user_id, f.friend_id, f.status, f.created_at, f.updated_at,
			   u.id, u.username, u.email, u.password_hash, u.nickname, u.avatar_url, u.created_at, u.updated_at
		FROM friends f
		JOIN users u ON u.id = f.user_id
		WHERE f.friend_id = $1 AND f.status = 'pending'
		ORDER BY f.created_at DESC
		LIMIT $2 OFFSET $3
	`
	return r.queryFriends(ctx, query, userID, limit, offset)
}

// UpdateStatusTx is like UpdateStatus but runs on an existing transaction.
func (r *FriendRepo) UpdateStatusTx(ctx context.Context, tx pgx.Tx, id string, status model.FriendStatus) error {
	const query = `UPDATE friends SET status = $1, updated_at = NOW() WHERE id = $2`
	_, err := tx.Exec(ctx, query, status, id)
	return err
}

// CreateTx is like Create but runs on an existing transaction.
func (r *FriendRepo) CreateTx(ctx context.Context, tx pgx.Tx, friend *model.Friend) error {
	const query = `
		INSERT INTO friends (user_id, friend_id, status)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, updated_at
	`
	err := tx.QueryRow(ctx, query,
		friend.UserID, friend.FriendID, friend.Status,
	).Scan(&friend.ID, &friend.CreatedAt, &friend.UpdatedAt)

	if isDuplicate(err) {
		return model.NewAppError(model.ErrDuplicate, "friend request already exists")
	}
	return err
}

// queryFriends runs a query that returns FriendWithUser rows.
func (r *FriendRepo) queryFriends(ctx context.Context, query string, args ...any) ([]model.FriendWithUser, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []model.FriendWithUser
	for rows.Next() {
		var fw model.FriendWithUser
		err := rows.Scan(
			&fw.ID, &fw.UserID, &fw.FriendID, &fw.Status, &fw.CreatedAt, &fw.UpdatedAt,
			&fw.FriendUser.ID, &fw.FriendUser.Username, &fw.FriendUser.Email,
			&fw.FriendUser.PasswordHash, &fw.FriendUser.Nickname, &fw.FriendUser.AvatarURL,
			&fw.FriendUser.CreatedAt, &fw.FriendUser.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, fw)
	}
	return result, rows.Err()
}
