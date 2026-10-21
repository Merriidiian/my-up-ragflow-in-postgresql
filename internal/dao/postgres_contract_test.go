package dao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/url"
	"os"
	"ragflow/internal/dbcompat"
	"ragflow/internal/entity"
	"reflect"
	"strings"
	"testing"
	"time"
)

func isolatedPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("VSM_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL test database")
	}
	base, err := dbcompat.Open(dsn, &gorm.Config{TranslateError: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := base.DB()
	name := fmt.Sprintf("contract_%d", time.Now().UnixNano())
	if err = base.Exec("CREATE SCHEMA " + name).Error; err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	db, err := dbcompat.Open(u.String(), &gorm.Config{TranslateError: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	scoped, _ := db.DB()
	t.Cleanup(func() { scoped.Close(); base.Exec("DROP SCHEMA " + name + " CASCADE"); pool.Close() })
	return db
}

// Explicit maintenance action only; regular tests never update golden contracts.
func TestExportPostgresContract(t *testing.T) {
	target := os.Getenv("VSM_PG_EXPORT_CONTRACT")
	if target == "" {
		t.Skip("explicit contract generation only")
	}
	db := isolatedPostgres(t)
	if err := createPostgresV1(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := upgradePostgresV2(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := upgradePostgresV3(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	objects, err := postgresSchemaObjects(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(objects, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(target, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUpgradeAndDrift(t *testing.T) {
	ctx := context.Background()
	db := isolatedPostgres(t)
	if err := createPostgresV1(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&pgSchemaVersion{Version: postgresPreviousSchemaVersion}).Error; err != nil {
		t.Fatal(err)
	}
	if err := verifyPostgresSchema(ctx, db); err == nil {
		t.Fatal("old version accepted without migration")
	}
	name := "Русская модель"
	row := entity.TenantLLM{TenantID: "tenant", LLMFactory: "Ollama", LLMName: &name}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := runPostgresMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	duplicate := entity.TenantLLM{TenantID: "TENANT", LLMFactory: "ollama", LLMName: &name}
	if err := db.Create(&duplicate).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("legacy CI key: %v", err)
	}
	if err := db.First(&row, "id = ?", row.ID).Error; err != nil {
		t.Fatal("upgrade lost data:", err)
	}
	for _, ddl := range []string{
		"DROP INDEX idx_ingestion_task_document_id",
		"ALTER TABLE document DROP COLUMN name",
		"ALTER TABLE document ALTER COLUMN size DROP DEFAULT",
		"ALTER TABLE document ALTER COLUMN name TYPE text",
		"ALTER TABLE ingestion_task ALTER COLUMN document_id DROP NOT NULL",
		"DROP TABLE compilation_template",
	} {
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(ddl).Error; err != nil {
				return err
			}
			if err := verifyPostgresSchema(ctx, tx); err == nil {
				t.Errorf("accepted drift: %s", ddl)
			}
			if err := runPostgresMigrations(ctx, tx); err == nil {
				t.Errorf("migration blessed drift: %s", ddl)
			}
			return errors.New("rollback test drift")
		})
		if err == nil {
			t.Fatal("expected rollback")
		}
	}
	if err := verifyPostgresSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUpgradeDuplicateRollback(t *testing.T) {
	db := isolatedPostgres(t)
	ctx := context.Background()
	if err := createPostgresV1(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&pgSchemaVersion{Version: postgresPreviousSchemaVersion}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := db.Exec("INSERT INTO tenant_llm (tenant_id,llm_factory,llm_name) VALUES (?,?,?)", "t", "Ollama", "duplicate").Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := runPostgresMigrations(ctx, db); err == nil {
		t.Fatal("silently accepted duplicates")
	}
	version, err := readPostgresSchemaVersion(ctx, db)
	if err != nil || version != postgresPreviousSchemaVersion {
		t.Fatalf("version advanced after failure: %s %v", version, err)
	}
	if db.Migrator().HasIndex("tenant_llm", "uk_tenant_llm") {
		t.Fatal("partial DDL committed")
	}
}

// Exercise every metadata model on real PG, including tables outside the core
// chat path. Values are derived from the shipped contract; this checks native
// INSERT/RETURNING, types, defaults, primary keys and FK ordering on PG.
func TestPostgresEveryMetadataModelRoundtrip(t *testing.T) {
	db := isolatedPostgres(t)
	ctx := context.Background()
	if err := runPostgresMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	var objects []pgSchemaObject
	if err := json.Unmarshal(postgresContractV3, &objects); err != nil {
		t.Fatal(err)
	}
	cols := map[string][]pgSchemaObject{}
	for _, o := range objects {
		if o.Kind == "column" {
			cols[o.Table] = append(cols[o.Table], o)
		}
	}
	for _, model := range metadataModels() {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(model); err != nil {
			t.Fatal(err)
		}
		table := stmt.Schema.Table
		t.Run(table, func(t *testing.T) {
			values := map[string]any{}
			for _, c := range cols[table] {
				var fieldPrimary, auto bool
				if f := stmt.Schema.LookUpField(c.Name); f != nil {
					fieldPrimary = f.PrimaryKey
					auto = f.AutoIncrement
				}
				if auto {
					continue
				}
				parts := strings.Split(c.Definition, "|")
				typ := parts[0]
				required := strings.Contains(c.Definition, "null=false|default=|")
				if !required && !fieldPrimary {
					continue
				}
				switch {
				case typ == "boolean":
					values[c.Name] = false
				case typ == "bigint" || typ == "integer" || typ == "smallint":
					values[c.Name] = int64(1)
				case typ == "double precision" || typ == "real" || strings.HasPrefix(typ, "numeric"):
					values[c.Name] = float64(1)
				case typ == "bytea":
					values[c.Name] = []byte("[]")
				case strings.Contains(typ, "timestamp"):
					values[c.Name] = time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
				default:
					value := "pg-all"
					if strings.HasPrefix(typ, "character varying(1)") {
						value = "1"
					}
					if f := stmt.Schema.LookUpField(c.Name); f != nil {
						typ := f.FieldType
						for typ.Kind() == reflect.Pointer {
							typ = typ.Elem()
						}
						if typ.Kind() == reflect.Map {
							value = "{}"
						}
						if typ.Kind() == reflect.Slice {
							value = "[]"
						}
					}
					values[c.Name] = value
				}
			}
			if err := db.Model(model).Create(values).Error; err != nil {
				t.Fatal("insert:", err)
			}
			var count int64
			if err := db.Model(model).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("read: %d %v", count, err)
			}
			loaded := reflect.New(reflect.TypeOf(model).Elem()).Interface()
			if err := db.First(loaded).Error; err != nil {
				t.Fatalf("read columns: %v", err)
			}
		})
	}
	if err := verifyPostgresSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
}

// Prevent another persistent entity (such as SkillSpace) being silently left
// out of the startup schema registry. DTOs do not declare TableName methods.
func TestPostgresRegistryCoversPersistentEntities(t *testing.T) {
	registered := map[string]bool{}
	for _, m := range metadataModels() {
		registered[reflect.TypeOf(m).Elem().Name()] = true
	}
	files, err := parser.ParseDir(token.NewFileSet(), "../entity", func(info os.FileInfo) bool { return !strings.HasSuffix(info.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range files {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || fn.Name.Name != "TableName" {
					continue
				}
				receiver := fn.Recv.List[0].Type
				if star, ok := receiver.(*ast.StarExpr); ok {
					receiver = star.X
				}
				if name, ok := receiver.(*ast.Ident); ok && !registered[name.Name] {
					t.Errorf("persistent entity missing from metadata registry: %s", name.Name)
				}
			}
		}
	}
}

// A historical MySQL data-migration marker is not a PostgreSQL schema contract.
// Preserve its value for imported installations, but never let it repair or
// approve an unknown PG catalogue/version.
func TestPostgresLegacyMigrationMarkerIsIndependent(t *testing.T) {
	db := isolatedPostgres(t)
	ctx := context.Background()
	if err := runPostgresMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	if got, err := GetDatabaseMigrationVersion(ctx, db); err != nil || got != "" {
		t.Fatalf("fresh legacy marker: %q %v", got, err)
	}
	if err := setDatabaseMigrationVersion(ctx, db, "v9.9.9"); err != nil {
		t.Fatal(err)
	}
	if err := runPostgresMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	if got, err := GetDatabaseMigrationVersion(ctx, db); err != nil || got != "v9.9.9" {
		t.Fatalf("legacy marker changed: %q %v", got, err)
	}
	if err := verifyPostgresSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&pgSchemaVersion{}).Where("version = ?", postgresSchemaVersion).Update("version", "unknown-pg-version").Error; err != nil {
		t.Fatal(err)
	}
	if err := verifyPostgresSchema(ctx, db); err == nil {
		t.Fatal("legacy marker approved an unknown PG schema")
	}
	if err := runPostgresMigrations(ctx, db); err == nil {
		t.Fatal("legacy marker repaired an unknown PG schema")
	}
}
