package contract

import (
	"context"
	"database/sql"
	"io"
)

// Reader is the read half of a Unit: queries run inside the caller's
// consistent read snapshot.
type Reader interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Unit is the transaction-scoped authority handed to handlers and owner
// methods. It is valid only inside its callback: no goroutines, no retained
// handles, no network/model calls, no secret-store access, no filesystem
// streaming and no subprocess work. Emit appends state-correlated evidence
// to the storage outbox in the same transaction.
type Unit interface {
	Reader
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	Actor() Actor
	Scope() Scope
	Generation() int64
	ReadOnly() bool
	Emit(ctx context.Context, event Event) error
}

// Ownership is the exclusive installation lock held by one local controller.
// Lost closes when ownership is lost or released; admission stops then.
type Ownership interface {
	Held() bool
	Lost() <-chan struct{}
	Close() error
}

// Migration is one owner-scoped schema step. SQL runs inside the owner's
// table namespace; SHA256 pins the migration body.
type Migration struct {
	Owner   string
	Version int64
	SQL     string
	SHA256  Digest
}

// Database is the storage owner's transactional surface. Write performs ONE
// transaction callback attempt with no automatic retry; busy exhaustion
// returns a controller_unavailable fault (retryable).
type Database interface {
	StartGeneration(ctx context.Context) (int64, error)
	Generation(ctx context.Context) (int64, error)
	Read(ctx context.Context, actor Actor, scope Scope, fn func(Unit) error) error
	Write(ctx context.Context, actor Actor, scope Scope, fn func(Unit) error) error
	Migrate(ctx context.Context, migrations []Migration) error
	Events(ctx context.Context, after int64, limit int) ([]Event, error)
	Backup(ctx context.Context, w io.Writer) error
	Close() error
}
