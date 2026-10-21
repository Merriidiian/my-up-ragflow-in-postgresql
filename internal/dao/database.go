//
//  Copyright 2026 The InfiniFlow Authors. All Rights Reserved.
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.
//

package dao

import (
	"context"
	"fmt"
	"ragflow/internal/common"
	"ragflow/internal/entity"
	"strings"

	"ragflow/internal/server"

	"go.uber.org/zap"

	"gorm.io/gorm"
)

var DB *gorm.DB

// InitDB initialize database connection
func InitDB(ctx context.Context, migrateDB bool) error {
	if server.GetConfig().DatabaseType() != "postgres" {
		return fmt.Errorf("metadata runtime requires PostgreSQL; use pg_migrate for offline MySQL import")
	}
	return initPostgresDB(ctx, migrateDB)
}

// GetDB get database instance
func GetDB() *gorm.DB {
	return DB
}

// autoMigrateSafely runs AutoMigrate and ignores duplicate index errors
// This handles cases where indexes already exist (e.g., created by Python backend)
func autoMigrateSafely(ctx context.Context, db *gorm.DB, model interface{}) error {
	//err := db.Debug().AutoMigrate(model) // to print debug info
	err := db.WithContext(ctx).AutoMigrate(model)
	if err == nil {
		return nil
	}

	// Check if error is MySQL duplicate index error (Error 1061)
	errStr := err.Error()
	if strings.Contains(errStr, "Error 1061") && strings.Contains(errStr, "Duplicate key name") {
		common.Warn("Index already exists, skipping", zap.String("error", errStr))
		return nil
	}

	if strings.Contains(errStr, "Error 1060") && strings.Contains(errStr, "Duplicate column name") {
		common.Warn("Column already exists, skipping", zap.String("error", errStr))
		return nil
	}

	if strings.Contains(errStr, "Error 1050") && strings.Contains(errStr, "Table") {
		common.Warn("Table already exists, skipping", zap.String("error", errStr))
		return nil
	}

	if strings.Contains(errStr, "Error 1091") && strings.Contains(errStr, "Can't DROP") {
		common.Warn("Index/column already dropped, skipping", zap.String("error", errStr))
		return nil
	}

	if strings.Contains(errStr, "Error 1138") && strings.Contains(errStr, "Invalid use of NULL") {
		common.Warn("NULL value in existing rows, skipping migration change", zap.String("error", errStr))
		return nil
	}

	return err
}

// autoMigrateRuntimeModels ensures the Go-exclusive runtime tables exist. The
// manual migrations run as the standalone --migrate action, so a server-mode
// process never runs them itself.
func autoMigrateRuntimeModels(ctx context.Context, db *gorm.DB) error {
	goRuntimeModels := []interface{}{
		&entity.IngestionTask{},
		&entity.IngestionTaskLog{},
		&entity.MemoryTask{},
		&entity.ConversationMessage{},
		&entity.ConversationReference{},
		&entity.API4ConversationMessage{},
		&entity.API4ConversationReference{},
	}
	for _, m := range goRuntimeModels {
		if err := autoMigrateSafely(ctx, db, m); err != nil {
			tableName := fmt.Sprintf("%T", m)
			if named, ok := m.(interface{ TableName() string }); ok {
				tableName = named.TableName()
			}
			return fmt.Errorf("failed to auto-migrate runtime table %s: %w", tableName, err)
		}
	}
	return nil
}

func metadataModels() []interface{} {
	return []interface{}{
		&entity.User{},
		&entity.Tenant{},
		&entity.UserTenant{},
		&entity.File{},
		&entity.File2Document{},
		&entity.TenantLLM{},
		&entity.Chat{},
		&entity.ChatChannel{},
		&entity.ChatSession{},
		&entity.ConversationMessage{},
		&entity.ConversationReference{},
		&entity.Task{},
		&entity.APIToken{},
		&entity.API4Conversation{},
		&entity.API4ConversationMessage{},
		&entity.API4ConversationReference{},
		&entity.Knowledgebase{},
		&entity.InvitationCode{},
		&entity.Document{},
		&entity.UserCanvas{},
		&entity.CanvasTemplate{},
		&entity.UserCanvasVersion{},
		&entity.LLMFactories{},
		&entity.LLM{},
		&entity.TenantLangfuse{},
		&entity.SystemSettings{},
		&entity.Connector{},
		&entity.Connector2Kb{},
		&entity.SyncLogs{},
		&entity.MCPServer{},
		&entity.Memory{},
		&entity.MemoryTask{},
		&entity.Search{},
		&entity.PipelineOperationLog{},
		&entity.EvaluationDataset{},
		&entity.EvaluationCase{},
		&entity.EvaluationRun{},
		&entity.EvaluationResult{},
		&entity.TimeRecord{},
		&entity.License{},
		&entity.SkillSearchConfig{},
		&entity.SkillSpace{},
		&entity.TenantModelInstance{},
		&entity.TenantModel{},
		&entity.TenantModelProvider{},
		&entity.IngestionTask{},
		&entity.IngestionTaskLog{},
		&entity.FileCommit{},
		&entity.FileCommitItem{},
		&entity.KnowledgeCompileDataset{},
		&entity.WikiDocumentDirty{},
		// Knowledge-compile compilation templates and their groups. The Go
		// KnowledgeCompilerComponent resolves a compilation_template (or group)
		// from these tables at runtime, so the Go side must guarantee they exist.
		&entity.CompilationTemplate{},
		&entity.CompilationTemplateGroup{},
	}

}
