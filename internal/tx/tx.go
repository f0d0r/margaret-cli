package tx

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/f0d0r/margaret-tools/internal/db"
)

type txKey struct{}

// injectTx embeds a transaction-bound Queries into ctx so downstream code
// can pick it up with QueryFrom instead of using the default Queries.
func injectTx(ctx context.Context, q *db.Queries) context.Context {
	return context.WithValue(ctx, txKey{}, q)
}

// QueryFrom returns the transaction-bound Queries from ctx when one exists,
// otherwise it returns defaultQueries. This lets code transparently participate
// in an ongoing transaction without knowing whether one is active.
func QueryFrom(ctx context.Context, defaultQueries *db.Queries) *db.Queries {
	if q, ok := ctx.Value(txKey{}).(*db.Queries); ok {
		return q
	}
	return defaultQueries
}

// IsInTx reports whether ctx carries an active database transaction
// injected by WithTx or injectTx.
func IsInTx(ctx context.Context) bool {
	_, ok := ctx.Value(txKey{}).(*db.Queries)
	return ok
}

// WithTx executes fn inside a database transaction. A transaction-bound
// Queries is injected into the context so fn and any code it calls can
// use QueryFrom to stay on the same transaction.
//
// When ctx already carries an active transaction (as reported by IsInTx),
// WithTx does not start a new one but simply runs fn(ctx) directly. This
// prevents nested transactions and the associated deadlock risk with
// single-writer databases such as SQLite.
func WithTx(ctx context.Context, sqldb *sql.DB, q *db.Queries, fn func(context.Context) error) error {
	if IsInTx(ctx) {
		return fn(ctx)
	}

	tx, err := sqldb.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			err := tx.Rollback()
			if err != nil {
				slog.ErrorContext(ctx, "rollback transaction", "error", err)
			}
			panic(p)
		}
	}()

	qtx := q.WithTx(tx)
	txCtx := injectTx(ctx, qtx)

	if err := fn(txCtx); err != nil {
		rErr := tx.Rollback()
		if rErr != nil {
			slog.ErrorContext(ctx, "rollback transaction", "error", rErr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}
