package dao

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// Each contract is generated from a fresh isolated schema, reviewed and shipped
// with the patch. The running database can never bless its own schema drift.
//
//go:embed postgres_schema_v3.json
var postgresContractV3 []byte

type pgSchemaObject struct {
	Kind       string `json:"kind"`
	Table      string `json:"table"`
	Name       string `json:"name"`
	Definition string `json:"definition"`
}

func postgresSchemaObjects(ctx context.Context, db *gorm.DB) ([]pgSchemaObject, error) {
	tables := []string{"vsm_pg_schema"}
	for _, model := range metadataModels() {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(model); err != nil {
			return nil, err
		}
		tables = append(tables, stmt.Schema.Table)
	}
	var result []pgSchemaObject
	// Capture types/lengths, defaults, NULL, identities, keys, index expressions,
	// predicates and validity. Sequence names are schema-relative for portability.
	query := `SELECT kind, "table", name, definition FROM (
 SELECT 'column' AS kind, c.relname AS "table", a.attname AS name,
 format_type(a.atttypid,a.atttypmod) || '|null=' || (NOT a.attnotnull)::text ||
 '|default=' || COALESCE(replace(pg_get_expr(d.adbin,d.adrelid),quote_ident(current_schema())||'.',''),'') ||
 '|identity=' || a.attidentity::text || '|generated=' || a.attgenerated::text AS definition
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
 LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum
 WHERE n.nspname=current_schema() AND c.relname IN ?
 UNION ALL
 SELECT 'index',c.relname,ic.relname,
 replace(pg_get_indexdef(i.indexrelid),quote_ident(current_schema())||'.','') || '|valid=' || i.indisvalid::text
 FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid JOIN pg_class ic ON ic.oid=i.indexrelid
 JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=current_schema() AND c.relname IN ?
 UNION ALL
 SELECT 'constraint',c.relname,co.conname,replace(pg_get_constraintdef(co.oid),quote_ident(current_schema())||'.','')
 FROM pg_constraint co JOIN pg_class c ON c.oid=co.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=current_schema() AND c.relname IN ?
 ) objects ORDER BY kind,"table",name`
	err := db.WithContext(ctx).Raw(query, tables, tables, tables).Scan(&result).Error
	return result, err
}

func verifyPostgresContract(ctx context.Context, db *gorm.DB, version string) error {
	data := postgresContractV3

	var expected []pgSchemaObject
	if err := json.Unmarshal(data, &expected); err != nil {
		return fmt.Errorf("invalid embedded PostgreSQL contract: %w", err)
	}
	if len(expected) == 0 {
		return fmt.Errorf("empty PostgreSQL schema contract")
	}
	actual, err := postgresSchemaObjects(ctx, db)
	if err != nil {
		return err
	}
	byKey := map[string]string{}
	key := func(o pgSchemaObject) string { return o.Kind + " " + o.Table + "." + o.Name }
	for _, o := range actual {
		byKey[key(o)] = o.Definition
	}
	var differences []string
	for _, o := range expected {
		if version != postgresSchemaVersion && o.Table == "skill_spaces" {
			continue
		}
		if version == postgresPreviousSchemaVersion && o.Kind == "index" && o.Name == "uk_tenant_llm" {
			continue
		}
		if byKey[key(o)] != o.Definition {
			differences = append(differences, key(o))
		}
		delete(byKey, key(o))
	}
	for k := range byKey {
		differences = append(differences, "unexpected "+k)
	}
	if len(differences) > 0 {
		return fmt.Errorf("PostgreSQL schema drift (%s): %s", version, strings.Join(differences, ", "))
	}
	return nil
}
