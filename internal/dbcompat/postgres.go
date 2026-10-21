// Package dbcompat adapts RAGFlow's metadata schema and identifier quoting to PostgreSQL.
package dbcompat

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/migrator"
	"gorm.io/gorm/schema"
)

// Open uses PostgreSQL's native driver, bind parameters, RETURNING and migrations.
// Only legacy identifier spelling is normalized; SQL values remain bound parameters.
func Open(dsn string, config *gorm.Config) (*gorm.DB, error) {
	base := postgres.New(postgres.Config{DSN: dsn}).(*postgres.Dialector)
	return gorm.Open(dialect{Dialector: base}, config)
}

type dialect struct{ *postgres.Dialector }

func (d dialect) Initialize(db *gorm.DB) error {
	if err := d.Dialector.Initialize(db); err != nil {
		return err
	}
	pool, ok := db.ConnPool.(*sql.DB)
	if !ok {
		return fmt.Errorf("PostgreSQL adapter requires a database/sql pool")
	}
	db.ConnPool = &rootPool{rewritingPool: rewritingPool{pool}, db: pool}
	return nil
}

func (d dialect) DataTypeOf(field *schema.Field) string {
	switch strings.ToLower(string(field.DataType)) {
	case "longtext", "mediumtext", "tinytext":
		return "text"
	case "datetime", "datetime(6)":
		return "timestamptz"
	case "double":
		return "double precision"
	case "blob", "longblob", "mediumblob":
		return "bytea"
	case "tinyint", "tinyint(1)":
		if field.FieldType.Kind().String() == "bool" {
			return "boolean"
		}
		return "smallint"
	}
	return d.Dialector.DataTypeOf(field)
}

func (d dialect) Migrator(db *gorm.DB) gorm.Migrator {
	return postgres.Migrator{Migrator: migrator.Migrator{Config: migrator.Config{
		DB: db, Dialector: d, CreateIndexAfterCreateTable: true,
	}}}
}

type rewritingPool struct{ gorm.ConnPool }

func (p rewritingPool) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return p.ConnPool.PrepareContext(ctx, NormalizeSQL(query))
}
func (p rewritingPool) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return p.ConnPool.ExecContext(ctx, NormalizeSQL(query), args...)
}
func (p rewritingPool) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return p.ConnPool.QueryContext(ctx, NormalizeSQL(query), args...)
}
func (p rewritingPool) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return p.ConnPool.QueryRowContext(ctx, NormalizeSQL(query), args...)
}

type rootPool struct {
	rewritingPool
	db *sql.DB
}

func (p *rootPool) GetDBConn() (*sql.DB, error) { return p.db, nil }
func (p *rootPool) Ping() error                 { return p.db.Ping() }
func (p *rootPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &txPool{rewritingPool: rewritingPool{tx}, tx: tx}, nil
}

type txPool struct {
	rewritingPool
	tx *sql.Tx
}

func (p *txPool) Commit() error   { return p.tx.Commit() }
func (p *txPool) Rollback() error { return p.tx.Rollback() }

var _ gorm.GetDBConnector = (*rootPool)(nil)
var _ gorm.ConnPoolBeginner = (*rootPool)(nil)
var _ gorm.TxCommitter = (*txPool)(nil)
