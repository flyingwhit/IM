package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// isDuplicate checks if the error is a PostgreSQL unique constraint violation (code 23505).
func isDuplicate(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
