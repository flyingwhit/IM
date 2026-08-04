package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ciel/im/internal/model"
)

// MessageRepo handles message persistence.
type MessageRepo struct {
	pool *pgxpool.Pool
}

// NewMessageRepo creates a MessageRepo backed by the given pool.
func NewMessageRepo(pool *pgxpool.Pool) *MessageRepo {
	return &MessageRepo{pool: pool}
}

// Insert stores a new message and returns the server-generated fields
// (id, created_at). The caller provides senderID, receiverID, content,
// contentType, and the repo fills in ID, CreatedAt, and sets Status to sent.
func (r *MessageRepo) Insert(ctx context.Context, msg *model.Message) error {
	const query = `
			INSERT INTO messages (sender_id, receiver_id, content, content_type, status)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id, created_at
		`
	return r.pool.QueryRow(ctx, query,
		msg.SenderID, msg.ReceiverID, msg.Content, msg.ContentType, model.MessageStatusSent,
	).Scan(&msg.ID, &msg.CreatedAt)
}

// FindByID returns a single message by its ID.
// Returns ErrNotFound if no message exists with the given ID.
func (r *MessageRepo) FindByID(ctx context.Context, msgID string) (*model.Message, error) {
	const query = `
		SELECT id, sender_id, receiver_id, content, content_type, status, created_at, recalled_at
		FROM messages
		WHERE id = $1
	`
	var m model.Message
	err := r.pool.QueryRow(ctx, query, msgID).Scan(
		&m.ID, &m.SenderID, &m.ReceiverID, &m.Content,
		&m.ContentType, &m.Status, &m.CreatedAt, &m.RecalledAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, model.NewAppError(model.ErrNotFound, "message not found")
		}
		return nil, err
	}
	return &m, nil
}

// FindConversation returns messages between two users, newest first.
// cursor is a created_at value used for cursor-based pagination.
// limit caps the number of returned messages.
func (r *MessageRepo) FindConversation(ctx context.Context, userA, userB string, before time.Time, limit int) ([]model.Message, error) {
	const query = `
			SELECT id, sender_id, receiver_id, content, content_type, status, created_at, recalled_at
			FROM messages
			WHERE ((sender_id = $1 AND receiver_id = $2)
				OR (sender_id = $2 AND receiver_id = $1))
			  AND created_at < $3
			ORDER BY created_at DESC, id DESC
			LIMIT $4
		`
	rows, err := r.pool.Query(ctx, query, userA, userB, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []model.Message
	for rows.Next() {
		var m model.Message
		if err := rows.Scan(&m.ID, &m.SenderID, &m.ReceiverID, &m.Content,
			&m.ContentType, &m.Status, &m.CreatedAt, &m.RecalledAt); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// FindUndelivered returns messages sent to a user that have not been delivered
// and have not been recalled. Recalled messages are excluded — there's no point
// delivering a message that the sender already took back.
//
// Results are ordered oldest-first so they can be delivered in chronological order.
func (r *MessageRepo) FindUndelivered(ctx context.Context, userID string) ([]model.Message, error) {
	const query = `
		SELECT id, sender_id, receiver_id, content, content_type, status, created_at, recalled_at
		FROM messages
		WHERE receiver_id = $1 AND status = $2 AND recalled_at IS NULL
		ORDER BY created_at ASC
	`
	rows, err := r.pool.Query(ctx, query, userID, model.MessageStatusSent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []model.Message
	for rows.Next() {
		var m model.Message
		if err := rows.Scan(&m.ID, &m.SenderID, &m.ReceiverID, &m.Content,
			&m.ContentType, &m.Status, &m.CreatedAt, &m.RecalledAt); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// UpdateStatus changes the delivery status of a message.
func (r *MessageRepo) UpdateStatus(ctx context.Context, msgID string, status model.MessageStatus) error {
	const query = `UPDATE messages SET status = $1 WHERE id = $2`
	_, err := r.pool.Exec(ctx, query, status, msgID)
	return err
}

// UpdateRecall marks a message as recalled by setting recalled_at to now.
// Returns the number of rows affected (0 means the message was not found).
func (r *MessageRepo) UpdateRecall(ctx context.Context, msgID string) error {
	const query = `UPDATE messages SET recalled_at = NOW() WHERE id = $1 AND recalled_at IS NULL`
	tag, err := r.pool.Exec(ctx, query, msgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return model.NewAppError(model.ErrNotFound, "message not found or already recalled")
	}
	return nil
}

// FindRecalledAfterDelivered returns messages that were delivered to the user
// and then recalled within a recent window. Called on reconnect to push
// recall notifications for messages the client has already displayed.
func (r *MessageRepo) FindRecalledAfterDelivered(ctx context.Context, userID string, since time.Time) ([]model.Message, error) {
	const query = `
		SELECT id, sender_id, receiver_id, content, content_type, status, created_at, recalled_at
		FROM messages
		WHERE receiver_id = $1 AND status = $2 AND recalled_at IS NOT NULL AND recalled_at > $3
		ORDER BY recalled_at ASC
	`
	rows, err := r.pool.Query(ctx, query, userID, model.MessageStatusDelivered, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []model.Message
	for rows.Next() {
		var m model.Message
		if err := rows.Scan(&m.ID, &m.SenderID, &m.ReceiverID, &m.Content,
			&m.ContentType, &m.Status, &m.CreatedAt, &m.RecalledAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// UpdateStatusTx is like UpdateStatus but runs on an existing transaction.
func (r *MessageRepo) UpdateStatusTx(ctx context.Context, tx pgx.Tx, msgID string, status model.MessageStatus) error {
	const query = `UPDATE messages SET status = $1 WHERE id = $2`
	_, err := tx.Exec(ctx, query, status, msgID)
	return err
}

// InsertTx is like Insert but runs on an existing transaction.
func (r *MessageRepo) InsertTx(ctx context.Context, tx pgx.Tx, msg *model.Message) error {
	const query = `
		INSERT INTO messages (sender_id, receiver_id, content, content_type, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at
	`
	return tx.QueryRow(ctx, query,
		msg.SenderID, msg.ReceiverID, msg.Content, msg.ContentType, model.MessageStatusSent,
	).Scan(&msg.ID, &msg.CreatedAt)
}
