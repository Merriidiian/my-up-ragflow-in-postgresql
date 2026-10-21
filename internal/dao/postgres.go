// PostgreSQL support maintained by VSM AI on top of RAGFlow v1.0.0-rc2.
package dao

import (
	"context"
	"fmt"
	"time"

	"ragflow/internal/dbcompat"
	"ragflow/internal/entity"
	"ragflow/internal/entity/models"
	"ragflow/internal/server"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const postgresSchemaVersion = "vsm-ragflow-rc2-pg-3"
const postgresSecondSchemaVersion = "vsm-ragflow-rc2-pg-2"
const postgresPreviousSchemaVersion = "vsm-ragflow-rc2-pg-1"

type pgSchemaVersion struct {
	Version string `gorm:"column:version;primaryKey;size:64"`
}

func (pgSchemaVersion) TableName() string { return "vsm_pg_schema" }

func initPostgresDB(ctx context.Context, migrate bool) error {
	config := server.GetConfig().GetPostgresConfig()
	var err error
	// Silent logger avoids rendering bound provider credentials into SQL logs.
	DB, err = dbcompat.Open(config.DSN(), &gorm.Config{TranslateError: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return fmt.Errorf("connect PostgreSQL metadata: %w", err)
	}
	pool, err := DB.DB()
	if err != nil {
		return err
	}
	pool.SetMaxOpenConns(config.MaxConnections)
	pool.SetMaxIdleConns(config.MaxConnections)
	pool.SetConnMaxLifetime(time.Duration(config.StaleTimeout) * time.Second)
	if err = pool.PingContext(ctx); err != nil {
		pool.Close()
		return fmt.Errorf("ping PostgreSQL metadata: %w", err)
	}
	if migrate {
		lockCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		release, lockErr := dbcompat.AcquireSessionLock(lockCtx, config.DSN(), "vsm-ragflow-postgres-schema")
		if lockErr != nil {
			return lockErr
		}
		err = runPostgresMigrations(ctx, DB)
		releaseErr := release()
		if err != nil {
			return err
		}
		if releaseErr != nil {
			return releaseErr
		}
	} else {
		if err = verifyPostgresSchema(ctx, DB); err != nil {
			return err
		}
	}
	if err = SeedCanvasTemplates(ctx, DB); err != nil {
		return fmt.Errorf("seed PostgreSQL canvas templates: %w", err)
	}
	if err = models.InitProviderManager("conf/models"); err != nil {
		return fmt.Errorf("load model providers: %w", err)
	}
	modelProviderManager = models.GetProviderManager()
	return nil
}

// Migrations run under the dedicated advisory lock and one DDL transaction.
// Existing schemas must match the previous contract before any update is made.
func runPostgresMigrations(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Migrator().HasTable(&pgSchemaVersion{}) {
			version, err := readPostgresSchemaVersion(ctx, tx)
			if err != nil {
				return err
			}
			if err = verifyPostgresContract(ctx, tx, version); err != nil {
				return err
			}
			if version == postgresSchemaVersion {
				return nil
			}
			if version == postgresPreviousSchemaVersion {
				if err = upgradePostgresV2(ctx, tx); err != nil {
					return err
				}
			}
			if err = upgradePostgresV3(ctx, tx); err != nil {
				return err
			}
			if err = tx.Model(&pgSchemaVersion{}).Where("version = ?", version).Update("version", postgresSchemaVersion).Error; err != nil {
				return err
			}
		} else {
			if err := createPostgresV1(ctx, tx); err != nil {
				return err
			}
			if err := upgradePostgresV2(ctx, tx); err != nil {
				return err
			}
			if err := upgradePostgresV3(ctx, tx); err != nil {
				return err
			}
			if err := tx.Create(&pgSchemaVersion{Version: postgresSchemaVersion}).Error; err != nil {
				return err
			}
		}
		return verifyPostgresSchema(ctx, tx)
	})
}

func createPostgresV1(ctx context.Context, tx *gorm.DB) error {
	for _, model := range postgresLegacyModels() {
		if tx.Migrator().HasTable(model) {
			return fmt.Errorf("unversioned PostgreSQL metadata schema: explicit migration required")
		}
	}
	for _, model := range postgresLegacyModels() {
		if err := tx.AutoMigrate(model); err != nil {
			return fmt.Errorf("migrate PostgreSQL model %T: %w", model, err)
		}
	}
	// Upstream deliberately excludes this column from AutoMigrate.
	statements := []string{
		"ALTER TABLE ingestion_task ADD COLUMN IF NOT EXISTS pipeline_log_id varchar(32)",
		"ALTER TABLE canvas_template ADD COLUMN IF NOT EXISTS parser_ids text",
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_user_email_ci ON "user" (lower(email))`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_kb_tenant_name_ci ON knowledgebase (tenant_id, lower(name)) WHERE status = '1'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_user_canvas_user_category_title ON user_canvas (user_id, canvas_category, lower(title))`,
		`CREATE INDEX IF NOT EXISTS idx_conversation_dialog_updated ON conversation (dialog_id, update_time, id)`,
		`CREATE INDEX IF NOT EXISTS idx_api_4_conversation_dialog_updated ON api_4_conversation (dialog_id, update_time, id)`,
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("PostgreSQL schema constraints: %w", err)
		}
	}
	if err := tx.AutoMigrate(&pgSchemaVersion{}); err != nil {
		return err
	}
	return nil
}

func upgradePostgresV2(ctx context.Context, tx *gorm.DB) error {
	// Mirror the case-insensitive legacy MySQL key, retaining distinct NULLs.
	// Duplicates abort the transaction; no row is deleted or silently merged.
	return tx.WithContext(ctx).Exec(`CREATE UNIQUE INDEX uk_tenant_llm ON tenant_llm
 (lower(tenant_id), lower(llm_factory), lower(llm_name))`).Error
}

func readPostgresSchemaVersion(ctx context.Context, db *gorm.DB) (string, error) {
	if !db.WithContext(ctx).Migrator().HasTable(&pgSchemaVersion{}) {
		return "", fmt.Errorf("PostgreSQL metadata schema missing: run --migrate first")
	}
	var versions []pgSchemaVersion
	if err := db.WithContext(ctx).Find(&versions).Error; err != nil {
		return "", err
	}
	if len(versions) != 1 || (versions[0].Version != postgresSchemaVersion && versions[0].Version != postgresPreviousSchemaVersion && versions[0].Version != postgresSecondSchemaVersion) {
		return "", fmt.Errorf("unsupported PostgreSQL metadata schema version")
	}
	return versions[0].Version, nil
}

func verifyPostgresSchema(ctx context.Context, db *gorm.DB) error {
	version, err := readPostgresSchemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if version != postgresSchemaVersion {
		return fmt.Errorf("PostgreSQL schema update required: run --migrate")
	}
	return verifyPostgresContract(ctx, db, version)
}

func postgresLegacyModels() []interface{} {
	var result []interface{}
	for _, model := range metadataModels() {
		if _, ok := model.(*entity.SkillSpace); !ok {
			result = append(result, model)
		}
	}
	return result
}

func upgradePostgresV3(ctx context.Context, tx *gorm.DB) error {
	// Upstream omitted SkillSpace from its model registry; preserve that feature.
	return tx.WithContext(ctx).AutoMigrate(&entity.SkillSpace{})
}
