// Historical upstream MySQL migration regressions; excluded from the PG runtime.
package dao

import (
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/migrator"
	"gorm.io/gorm/schema"
)

// migrationAwareDialector hands out a namedIndexMigrator instead of the stock
// one.
//
// Both wrappers embed the concrete driver types, not gorm's interfaces. Embedding
// gorm.Dialector or gorm.Migrator promotes only the methods those interfaces
// declare, and gorm reaches capabilities such as ErrorTranslator,
// SavePointerDialectorInterface and BuildIndexOptionsInterface by type
// assertion on db.Dialector and db.Migrator() instead of through the
// interfaces. The storage driver exposes twenty migrator methods beyond
// gorm.Migrator, so forwarding them by hand loses one on every upgrade --
// usually surfacing as a runtime panic mid-migration.
type migrationAwareDialector struct {
	*mysql.Dialector
}

// The capabilities gorm discovers by type assertion on db.Dialector.
var (
	_ gorm.Dialector                     = migrationAwareDialector{}
	_ gorm.ErrorTranslator               = migrationAwareDialector{}
	_ gorm.SavePointerDialectorInterface = migrationAwareDialector{}
)

func newMigrationAwareDialector(dsn string) gorm.Dialector {
	base := mysql.Open(dsn)
	if dialector, ok := base.(*mysql.Dialector); ok {
		return migrationAwareDialector{Dialector: dialector}
	}
	return base
}

func (d migrationAwareDialector) Migrator(db *gorm.DB) gorm.Migrator {
	migrator, ok := d.Dialector.Migrator(db).(mysql.Migrator)
	if !ok {
		// An unsupported driver shape degrades to the stock behaviour rather
		// than panicking: a foreign migrator means we get the redundant drops
		// back, which is what shipped before this wrapper existed.
		return d.Dialector.Migrator(db)
	}
	return namedIndexMigrator{Migrator: migrator}
}

// namedIndexMigrator leaves uniqueness to the named indexes declared with
// uniqueIndex tags and created by the manual migrations.
type namedIndexMigrator struct {
	mysql.Migrator
}

var _ migrator.BuildIndexOptionsInterface = namedIndexMigrator{}

// MigrateColumnUnique drops a unique constraint only once it exists. The stock
// implementation equates "this column carries some single-column UNIQUE index"
// with "this column carries a UNIQUE constraint", derives the matching default
// name (uni_<table>_<column>) and drops it. Our named indexes are created as
// indexes and never under that name, so the DROP always targets a missing
// object and MySQL answers 1091 -- once per column, on every startup. The
// add-constraint branch is left alone: it names its own object, so it cannot
// hit the same mismatch.
// phantomUniqueDrop reports whether the stock migrator is about to drop a
// unique constraint this schema never created. See MigrateColumnUnique.
func phantomUniqueDrop(field *schema.Field, columnType gorm.ColumnType) bool {
	unique, _ := columnType.Unique()
	return unique && !field.Unique
}

func (m namedIndexMigrator) MigrateColumnUnique(dst interface{}, field *schema.Field, columnType gorm.ColumnType) error {
	if phantomUniqueDrop(field, columnType) {
		return nil
	}
	return m.Migrator.MigrateColumnUnique(dst, field, columnType)
}
