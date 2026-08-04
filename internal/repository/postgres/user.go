package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ciel/im/internal/model"
)

// UserRepo handles user persistence in PostgreSQL.
type UserRepo struct {
	pool *pgxpool.Pool
}

func NewUserRepo(pool *pgxpool.Pool) *UserRepo {
	return &UserRepo{pool: pool}
}

// Create inserts a new user and returns the created user with generated fields.
func (r *UserRepo) Create(ctx context.Context, user *model.User) error {
	const query = `
		INSERT INTO users (username, email, password_hash, nickname)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at
	`
	err := r.pool.QueryRow(ctx, query,
		user.Username, user.Email, user.PasswordHash, user.Nickname,
	).Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		// pgx maps unique violations to this error
		if isDuplicate(err) {
			return model.NewAppError(model.ErrDuplicate, "username or email already exists")
		}
		return err
	}
	return nil
}

// FindByID returns a user by their ID.
func (r *UserRepo) FindByID(ctx context.Context, id string) (*model.User, error) {
	const query = `
		SELECT id, username, email, password_hash, nickname, avatar_url, created_at, updated_at
		FROM users WHERE id = $1
	`
	user := &model.User{}
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&user.ID, &user.Username, &user.Email, &user.PasswordHash,
		&user.Nickname, &user.AvatarURL, &user.CreatedAt, &user.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, model.NewAppError(model.ErrNotFound, "user not found")
	}
	if err != nil {
		return nil, err
	}
	return user, nil
}

// FindByUsername returns a user by username for login lookup.
func (r *UserRepo) FindByUsername(ctx context.Context, username string) (*model.User, error) {
	const query = `
		SELECT id, username, email, password_hash, nickname, avatar_url, created_at, updated_at
		FROM users WHERE username = $1
	`
	user := &model.User{}
	err := r.pool.QueryRow(ctx, query, username).Scan(
		&user.ID, &user.Username, &user.Email, &user.PasswordHash,
		&user.Nickname, &user.AvatarURL, &user.CreatedAt, &user.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, model.NewAppError(model.ErrNotFound, "user not found")
	}
	if err != nil {
		return nil, err
	}
	return user, nil
}

// Update updates a user's mutable fields (nickname, avatar_url).
func (r *UserRepo) Update(ctx context.Context, user *model.User) error {
	const query = `
		UPDATE users SET nickname = $1, avatar_url = $2, updated_at = NOW()
		WHERE id = $3
	`
	_, err := r.pool.Exec(ctx, query, user.Nickname, user.AvatarURL, user.ID)
	return err
}
