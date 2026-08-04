package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ciel/im/internal/model"
)

// GroupMessageRepo handles group message persistence.
type GroupMessageRepo struct {
	pool *pgxpool.Pool
}

// NewGroupMessageRepo creates a GroupMessageRepo backed by the given pool.
func NewGroupMessageRepo(pool *pgxpool.Pool) *GroupMessageRepo {
	return &GroupMessageRepo{pool: pool}
}

// Insert stores a new group message and populates the server-generated fields
// (id, created_at). The caller provides groupID, senderID, content, contentType.
func (r *GroupMessageRepo) Insert(ctx context.Context, msg *model.GroupMessage) error {
	const query = `
		INSERT INTO group_messages (group_id, sender_id, content, content_type)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at
	`
	return r.pool.QueryRow(ctx, query,
		msg.GroupID, msg.SenderID, msg.Content, msg.ContentType,
	).Scan(&msg.ID, &msg.CreatedAt)
}

// FindByGroup returns paginated group message history, newest first.
// cursor is a created_at value used for cursor-based pagination.
func (r *GroupMessageRepo) FindByGroup(ctx context.Context, groupID string, before time.Time, limit int) ([]model.GroupMessage, error) {
	const query = `
		SELECT id, group_id, sender_id, content, content_type, created_at, recalled_at
		FROM group_messages
		WHERE group_id = $1 AND created_at < $2
		ORDER BY created_at DESC, id DESC
		LIMIT $3
	`
	rows, err := r.pool.Query(ctx, query, groupID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []model.GroupMessage
	for rows.Next() {
		var m model.GroupMessage
		if err := rows.Scan(&m.ID, &m.GroupID, &m.SenderID, &m.Content,
			&m.ContentType, &m.CreatedAt, &m.RecalledAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}
