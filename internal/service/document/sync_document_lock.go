package document

import (
	"context"
	"fmt"

	"ragflow/internal/common"
	"ragflow/internal/dao"
	"ragflow/internal/dbcompat"
	"ragflow/internal/server"
	"ragflow/internal/service"
)

// acquireSyncDocumentLock serializes sync side effects across PostgreSQL sessions,
// including for documents that have not been inserted yet. SQLite uses the
// process lock only for the default in-memory unit fixtures.
func acquireSyncDocumentLock(ctx context.Context, docID string) (func(), error) {
	key := "sync-document:" + service.Hash128(docID)
	if dao.DB.Dialector.Name() == "postgres" {
		lockCtx, cancel := context.WithTimeout(ctx, cleanupBatchTimeout)
		defer cancel()
		unlock, err := dbcompat.AcquireSessionLock(lockCtx, server.GetConfig().GetPostgresConfig().DSN(), key)
		if err != nil {
			return nil, err
		}
		return func() {
			if err := unlock(); err != nil {
				common.Warn("release PostgreSQL document lock failed")
			}
		}, nil
	}
	if dao.DB.Dialector.Name() == "sqlite" {
		// SQLite is used by the in-memory service unit fixtures. Its lock key
		// must differ from the parse lock acquired by StartParseDocuments.
		return lockDocumentParse(key), nil
	}
	return nil, fmt.Errorf("sync document lock requires PostgreSQL")
}
