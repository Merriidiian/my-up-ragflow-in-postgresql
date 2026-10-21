package dbcompat

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// AcquireSessionLock owns a separate physical PostgreSQL session. A cancelled
// caller cannot strand the lock in the application's shared connection pool.
func AcquireSessionLock(ctx context.Context, dsn, key string) (func() error, error) {
	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(0)
	conn, err := pool.Conn(ctx)
	if err != nil {
		pool.Close()
		return nil, err
	}
	closeSession := func() { conn.Close(); pool.Close() }
	digest := sha256.Sum256([]byte(key))
	id := int64(binary.BigEndian.Uint64(digest[:8]))
	for {
		var acquired bool
		if err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", id).Scan(&acquired); err != nil {
			closeSession()
			return nil, err
		}
		if acquired {
			break
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			closeSession()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return func() error {
		defer closeSession()
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var released bool
		if err := conn.QueryRowContext(releaseCtx, "SELECT pg_advisory_unlock($1)", id).Scan(&released); err != nil {
			return err
		}
		if !released {
			return errors.New("PostgreSQL session lock was not held")
		}
		return nil
	}, nil
}
