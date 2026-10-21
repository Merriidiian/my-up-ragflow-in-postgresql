package dao

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

type MetadataImportTable struct {
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}

// ImportMySQLMetadata copies the selected rc2 Go metadata schema only. Both
// services must be stopped: this snapshot does not migrate S3/Infinity/queues.
// The source is read-only; target changes and sequence resets are one transaction.
// A populated target, missing columns, invalid values or mismatched hashes abort.
func ImportMySQLMetadata(ctx context.Context, source, target *gorm.DB) (map[string]MetadataImportTable, error) {
	if source.Dialector.Name() != "mysql" || target.Dialector.Name() != "postgres" {
		return nil, fmt.Errorf("import requires MySQL source and PostgreSQL target")
	}
	report := map[string]MetadataImportTable{}
	err := source.WithContext(ctx).Transaction(func(src *gorm.DB) error {
		return target.WithContext(ctx).Transaction(func(dst *gorm.DB) error {
			if err := verifyPostgresSchema(ctx, dst); err != nil {
				return err
			}
			var contract []pgSchemaObject
			if err := json.Unmarshal(postgresContractV3, &contract); err != nil {
				return err
			}
			columns := map[string][]pgSchemaObject{}
			for _, o := range contract {
				if o.Kind == "column" && o.Table != "vsm_pg_schema" {
					columns[o.Table] = append(columns[o.Table], o)
				}
			}
			// Lock every target table before checking emptiness, so an accidental
			// concurrent API write cannot be overwritten or mixed with imported rows.
			for _, model := range metadataModels() {
				stmt := &gorm.Statement{DB: dst}
				if err := stmt.Parse(model); err != nil {
					return err
				}
				table := stmt.Schema.Table
				if err := dst.Exec("LOCK TABLE " + pgImportQuote(table) + " IN ACCESS EXCLUSIVE MODE").Error; err != nil {
					return err
				}
				var count int64
				if err := dst.Table(table).Count(&count).Error; err != nil {
					return err
				}
				if count != 0 {
					return fmt.Errorf("target table %s is not empty", table)
				}
			}
			for _, model := range metadataModels() {
				stmt := &gorm.Statement{DB: dst}
				if err := stmt.Parse(model); err != nil {
					return err
				}
				table := stmt.Schema.Table
				specs := columns[table]
				if len(specs) == 0 {
					return fmt.Errorf("missing import contract for %s", table)
				}
				if table == "skill_spaces" && !src.Migrator().HasTable(model) {
					report[table] = MetadataImportTable{Rows: 0, SHA256: importTableHash(nil)}
					continue
				}
				sourceCols, err := src.Migrator().ColumnTypes(table)
				if err != nil {
					return fmt.Errorf("source schema %s: %w", table, err)
				}
				available := map[string]bool{}
				for _, c := range sourceCols {
					available[c.Name()] = true
				}
				var names, pgNames, marks, order []string
				for _, o := range specs {
					if !available[o.Name] {
						return fmt.Errorf("source %s.%s missing; migrate the source with the selected upstream rc2 first", table, o.Name)
					}
					delete(available, o.Name)
					names = append(names, "`"+o.Name+"`")
					pgNames = append(pgNames, pgImportQuote(o.Name))
					marks = append(marks, "?")
				}
				// name_ci is a generated MySQL helper replaced by a PG expression index.
				delete(available, "name_ci")
				if len(available) > 0 {
					return fmt.Errorf("source %s has unsupported extra columns", table)
				}
				for _, f := range stmt.Schema.PrimaryFields {
					order = append(order, f.DBName)
				}
				if len(order) == 0 {
					return fmt.Errorf("missing primary key for %s", table)
				}
				mysqlOrder := []string{}
				pgOrder := []string{}
				for _, c := range order {
					mysqlOrder = append(mysqlOrder, "`"+c+"`")
					pgOrder = append(pgOrder, pgImportQuote(c))
				}
				rows, err := src.Raw("SELECT " + strings.Join(names, ",") + " FROM `" + table + "` ORDER BY " + strings.Join(mysqlOrder, ",")).Rows()
				if err != nil {
					return fmt.Errorf("read source %s: %w", table, err)
				}
				// Hash rows independently and sort digests: collation order differs between
				// MySQL and PG, and neither row contents nor credentials belong in reports.
				var hashes []string
				var count int64
				for rows.Next() {
					values := make([]any, len(specs))
					pointers := make([]any, len(specs))
					for i := range values {
						pointers[i] = &values[i]
					}
					if err = rows.Scan(pointers...); err != nil {
						rows.Close()
						return err
					}
					for i := range values {
						values[i], err = normalizeImportValue(values[i], specs[i].Definition)
						if err != nil {
							rows.Close()
							return fmt.Errorf("invalid import value %s.%s: %w", table, specs[i].Name, err)
						}
					}
					digest, err := importRowHash(values)
					if err != nil {
						rows.Close()
						return err
					}
					hashes = append(hashes, digest)
					if err = dst.Exec("INSERT INTO "+pgImportQuote(table)+" ("+strings.Join(pgNames, ",")+") VALUES ("+strings.Join(marks, ",")+")", values...).Error; err != nil {
						rows.Close()
						return fmt.Errorf("insert target %s: %w", table, err)
					}
					count++
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				verify, err := dst.Raw("SELECT " + strings.Join(pgNames, ",") + " FROM " + pgImportQuote(table) + " ORDER BY " + strings.Join(pgOrder, ",")).Rows()
				if err != nil {
					return err
				}
				var actual []string
				for verify.Next() {
					values := make([]any, len(specs))
					pointers := make([]any, len(specs))
					for i := range values {
						pointers[i] = &values[i]
					}
					if err = verify.Scan(pointers...); err != nil {
						verify.Close()
						return err
					}
					for i := range values {
						values[i], err = normalizeImportValue(values[i], specs[i].Definition)
						if err != nil {
							verify.Close()
							return err
						}
					}
					hash, err := importRowHash(values)
					if err != nil {
						verify.Close()
						return err
					}
					actual = append(actual, hash)
				}
				err = verify.Err()
				verify.Close()
				if err != nil {
					return err
				}
				expectedHash, actualHash := importTableHash(hashes), importTableHash(actual)
				if len(actual) != len(hashes) || expectedHash != actualHash {
					return fmt.Errorf("import verification failed for %s", table)
				}
				report[table] = MetadataImportTable{Rows: count, SHA256: actualHash}
				for _, f := range stmt.Schema.PrimaryFields {
					if !f.AutoIncrement {
						continue
					}
					// ALTER SEQUENCE RESTART is transactional; setval is not.
					// format quotes the catalog-owned sequence name, not user input.
					query := `SELECT format('ALTER SEQUENCE %s RESTART WITH %s',pg_get_serial_sequence(?,?),COALESCE(MAX(` + pgImportQuote(f.DBName) + `),0)+1) FROM ` + pgImportQuote(table)
					var restart string
					if err = dst.Raw(query, table, f.DBName).Scan(&restart).Error; err != nil {
						return err
					}
					if err = dst.Exec(restart).Error; err != nil {
						return err
					}
				}
			}
			return verifyPostgresSchema(ctx, dst)
		}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	return report, nil
}

func pgImportQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func normalizeImportValue(v any, definition string) (any, error) {
	if v == nil {
		return nil, nil
	}
	typ := strings.Split(definition, "|")[0]
	if t, ok := v.(time.Time); ok {
		if t.IsZero() {
			return nil, fmt.Errorf("zero MySQL date is not a PostgreSQL timestamp")
		}
		return t.UTC(), nil
	}
	if b, ok := v.([]byte); ok && typ != "bytea" {
		v = string(b)
	}
	if typ == "boolean" {
		switch x := v.(type) {
		case bool:
			return x, nil
		case int64:
			if x == 0 || x == 1 {
				return x == 1, nil
			}
		case string:
			if x == "0" || x == "1" {
				return x == "1", nil
			}
		}
		return nil, fmt.Errorf("invalid boolean")
	}
	if typ == "bigint" || typ == "integer" || typ == "smallint" {
		switch x := v.(type) {
		case int64:
			return x, nil
		case string:
			return strconv.ParseInt(x, 10, 64)
		}
	}
	if typ == "double precision" || typ == "real" {
		switch x := v.(type) {
		case float64:
			return x, nil
		case int64:
			return float64(x), nil
		case string:
			return strconv.ParseFloat(x, 64)
		}
	}
	return v, nil
}
func importRowHash(values []any) (string, error) {
	b, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:]), nil
}
func importTableHash(rows []string) string {
	sort.Strings(rows)
	s := sha256.Sum256([]byte(strings.Join(rows, "\n")))
	return hex.EncodeToString(s[:])
}

// PreparePostgresMetadata is used by the offline importer before any API starts.
func PreparePostgresMetadata(ctx context.Context, db *gorm.DB) error {
	return runPostgresMigrations(ctx, db)
}
