package dao

import (
	"context"
	"encoding/json"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"ragflow/internal/entity"
	"strings"
	"testing"
)

func TestMySQLToPostgresMetadata(t *testing.T) {
	dsn := os.Getenv("VSM_MYSQL_IMPORT_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable MySQL fixture database")
	}
	src, err := gorm.Open(mysql.Open(dsn), &gorm.Config{TranslateError: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := src.DB()
	defer pool.Close()
	// Only a fresh dedicated test database is accepted. Never reset an input DB.
	var existing int64
	if err = src.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema=DATABASE()").Scan(&existing).Error; err != nil || existing != 0 {
		t.Fatalf("fixture database must be empty: %d %v", existing, err)
	}
	for _, model := range metadataModels() {
		if err = src.AutoMigrate(model); err != nil {
			t.Fatal(err)
		}
	}
	if !src.Migrator().HasColumn(&entity.CanvasTemplate{}, "parser_ids") {
		if err = src.Exec("ALTER TABLE canvas_template ADD COLUMN parser_ids text").Error; err != nil {
			t.Fatal(err)
		}
	}
	// IDs, NULL, Russian JSON, booleans, timestamps, FK history and sequences.
	admin := false
	user := entity.User{ID: "import-user", Nickname: "Пользователь", Email: "migration@local.test", IsSuperuser: &admin}
	if err = src.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	name := "Русская модель"
	llm := entity.TenantLLM{ID: 41, TenantID: user.ID, LLMFactory: "Ollama", LLMName: &name}
	if err = src.Create(&llm).Error; err != nil {
		t.Fatal(err)
	}
	chat := entity.ChatSession{ID: "import-session", DialogID: "import-chat", Message: json.RawMessage(`[{"id":"q1","role":"user","content":"Русский вопрос"},{"id":"a1","role":"assistant","content":"Ответ"}]`), Reference: json.RawMessage(`[{"chunks":[{"id":"source-1","content":"Русский источник"}]}]`)}
	if err = NewChatSessionDAO().Create(context.Background(), src, &chat); err != nil {
		t.Fatal(err)
	}
	target := isolatedPostgres(t)
	if err = runPostgresMigrations(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	report, err := ImportMySQLMetadata(context.Background(), src, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(report) != len(metadataModels()) || report["user"].Rows != 1 || report["conversation_messages"].Rows != 2 {
		// Use the entity's table name rather than relying on pluralization.
		var history int64
		target.Model(&entity.ConversationMessage{}).Count(&history)
		if len(report) != len(metadataModels()) || report["user"].Rows != 1 || history != 2 {
			t.Fatalf("incomplete report: %v, history=%d", report, history)
		}
	}
	stored, err := NewChatSessionDAO().GetByID(context.Background(), target, chat.ID)
	if err != nil || !strings.Contains(string(stored.Message), "Русский вопрос") || !strings.Contains(string(stored.Reference), "Русский источник") {
		t.Fatalf("imported conversation/citations: %v", err)
	}
	next := entity.TenantLLM{TenantID: "another", LLMFactory: "Ollama", LLMName: &name}
	if err = target.Create(&next).Error; err != nil || next.ID != 42 {
		t.Fatalf("sequence: %d %v", next.ID, err)
	}
	if _, err = ImportMySQLMetadata(context.Background(), src, target); err == nil {
		t.Fatal("accepted populated target")
	}
	if path := os.Getenv("VSM_MYSQL_IMPORT_TEST_REPORT"); path != "" {
		data, _ := json.MarshalIndent(report, "", "  ")
		if err = os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// A source key collision must roll back every target row, including tables
	// copied before tenant_llm, without changing the source.
	duplicate := entity.TenantLLM{TenantID: llm.TenantID, LLMFactory: llm.LLMFactory, LLMName: llm.LLMName}
	if err = src.Create(&duplicate).Error; err != nil {
		t.Fatal(err)
	}
	empty := isolatedPostgres(t)
	if err = runPostgresMigrations(context.Background(), empty); err != nil {
		t.Fatal(err)
	}
	if _, err = ImportMySQLMetadata(context.Background(), src, empty); err == nil {
		t.Fatal("accepted duplicate source")
	}
	var count int64
	empty.Model(&entity.User{}).Count(&count)
	if count != 0 {
		t.Fatal("partial import committed")
	}
}
