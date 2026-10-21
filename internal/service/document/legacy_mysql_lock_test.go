// Historical MySQL lock regressions; not part of the PostgreSQL runtime.
package document

import (
	"context"
	"database/sql"
	"fmt"
	"ragflow/internal/common"
	"time"
)

// acquireMySQLSyncDocumentLock owns a named lock on one dedicated connection
// until its release callback runs. It holds no row lock or SQL transaction.
func acquireMySQLSyncDocumentLock(ctx context.Context, pool *sql.DB, key string) (func(), error) {
	lockCtx, cancel := context.WithTimeout(ctx, cleanupBatchTimeout)
	defer cancel()
	conn, err := pool.Conn(lockCtx)
	if err != nil {
		return nil, err
	}
	var granted sql.NullInt64
	err = conn.QueryRowContext(lockCtx, "SELECT GET_LOCK(?, ?)", key, int64(cleanupBatchTimeout/time.Second)).Scan(&granted)
	if err != nil || !granted.Valid || granted.Int64 != 1 {
		_ = conn.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("could not acquire sync document lock %s", key)
	}
	return func() {
		defer conn.Close()
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupBatchTimeout)
		defer cancel()
		var released sql.NullInt64
		if err := conn.QueryRowContext(releaseCtx, "SELECT RELEASE_LOCK(?)", key).Scan(&released); err != nil || !released.Valid || released.Int64 != 1 {
			common.Warn(fmt.Sprintf("release sync document lock %s failed: %v", key, err))
		}
	}, nil
}
