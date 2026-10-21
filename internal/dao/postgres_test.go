package dao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"ragflow/internal/dbcompat"
	"ragflow/internal/entity"
)

func TestPostgresMetadataSchema(t *testing.T) {
	dsn := os.Getenv("VSM_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL test database")
	}
	base, err := dbcompat.Open(dsn, &gorm.Config{TranslateError: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := base.DB()
	defer pool.Close()
	name := fmt.Sprintf("port_%d", time.Now().UnixNano())
	if err = base.Exec("CREATE SCHEMA " + name).Error; err != nil {
		t.Fatal(err)
	}
	defer base.Exec("DROP SCHEMA " + name + " CASCADE")
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	db, err := dbcompat.Open(u.String(), &gorm.Config{TranslateError: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	scoped, _ := db.DB()
	defer scoped.Close()
	ctx := context.Background()
	if err = runPostgresMigrations(ctx, db); err != nil {
		t.Fatal("fresh schema:", err)
	}
	first := entity.User{ID: "user-1", Nickname: "Администратор", Email: "Admin@Local.test"}
	if err = db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	second := entity.User{ID: "user-2", Nickname: "Другой", Email: "admin@local.test"}
	if err = db.Create(&second).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("email CI uniqueness: %v", err)
	}
	var found entity.User
	if err = db.Raw("SELECT user.* FROM user WHERE LOWER(email)=LOWER(?)", "ADMIN@LOCAL.TEST").Scan(&found).Error; err != nil || found.ID != first.ID {
		t.Fatalf("quoted user lookup: %v %s", err, found.ID)
	}
	for _, name := range []string{"Документы", "документы"} {
		record := entity.Knowledgebase{TenantID: "tenant-1", Name: name, CreatedBy: "user-1"}
		// Separate IDs ensure the second constraint failure is the name index.
		if name == "Документы" {
			record.ID = "kb-1"
		} else {
			record.ID = "kb-2"
		}
		err = db.Create(&record).Error
		if name == "Документы" && err != nil {
			t.Fatal(err)
		}
		if name == "документы" && !errors.Is(err, gorm.ErrDuplicatedKey) {
			t.Fatalf("active dataset name uniqueness: %v", err)
		}
	}
	if err = db.Model(&entity.Knowledgebase{}).Where("id = ?", "kb-1").Update("status", "0").Error; err != nil {
		t.Fatal(err)
	}
	replacement := entity.Knowledgebase{ID: "kb-3", TenantID: "tenant-1", Name: "документы", CreatedBy: "user-1"}
	if err = db.Create(&replacement).Error; err != nil {
		t.Fatal("reuse deleted dataset name:", err)
	}
	if err = runPostgresMigrations(ctx, db); err != nil {
		t.Fatal("idempotent migration:", err)
	}
	if err = db.First(&found, "id = ?", first.ID).Error; err != nil {
		t.Fatal("data lost after migration:", err)
	}
	if err = verifyPostgresSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasColumn(&entity.IngestionTask{}, "pipeline_log_id") {
		t.Fatal("missing pipeline_log_id")
	}
	if err = SeedCanvasTemplates(ctx, db); err != nil {
		t.Fatal("seed templates:", err)
	}
	relation := entity.UserTenant{ID: "rel-1", UserID: first.ID, TenantID: "tenant-1", Role: "normal"}
	if err = db.Create(&relation).Error; err != nil {
		t.Fatal(err)
	}
	members, err := NewUserTenantDAO().GetMembersByTenantID(ctx, db, "tenant-1")
	if err != nil || len(members) != 1 {
		t.Fatalf("tenant member SQL: %v", err)
	}
	title := "Агент"
	canvas := entity.UserCanvas{ID: "canvas-1", UserID: first.ID, Title: &title, Tags: " Отчёты, PDF"}
	if err = db.Create(&canvas).Error; err != nil {
		t.Fatal(err)
	}
	var canvases []entity.UserCanvas
	filtered := applyUserCanvasTagFilter(ctx, db, db.Model(&entity.UserCanvas{}), []string{"Отчёты"})
	if err = filtered.Find(&canvases).Error; err != nil || len(canvases) != 1 {
		t.Fatalf("tag SQL: %v", err)
	}
	if _, err = NewAPI4ConversationDAO().Stats(ctx, db, "tenant-1", "2026-01-01", "2026-12-31", nil); err != nil {
		t.Fatal("stats SQL:", err)
	}
	for _, data := range []string{"older", "newer"} {
		if err = db.Create(&entity.TimeRecord{Data: data}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err = NewTimeRecordDAO().DeleteOldest(ctx, db, 1); err != nil {
		t.Fatal(err)
	}
	recent, err := NewTimeRecordDAO().GetRecent(ctx, db, 10)
	if err != nil || len(recent) != 1 || recent[0].Data != "newer" {
		t.Fatalf("oldest deletion: %v", err)
	}
	conv := entity.ChatSession{ID: "session-1", DialogID: "chat-1", Message: json.RawMessage(`[{"id":"q1","role":"user","content":"Русский вопрос"},{"id":"a1","role":"assistant","content":"Ответ"}]`), Reference: json.RawMessage(`[]`)}
	sessions := NewChatSessionDAO()
	if err = sessions.Create(ctx, db, &conv); err != nil {
		t.Fatal("conversation create:", err)
	}
	stored, err := sessions.GetByID(ctx, db, conv.ID)
	if err != nil || !strings.Contains(string(stored.Message), "Русский вопрос") {
		t.Fatalf("conversation history: %v", err)
	}
	if err = sessions.DeleteByID(ctx, db, conv.ID); err != nil {
		t.Fatal("conversation delete:", err)
	}
	var children int64
	if err = db.Model(&entity.ConversationMessage{}).Where("conversation_id = ?", conv.ID).Count(&children).Error; err != nil || children != 0 {
		t.Fatalf("history deletion: %v, %d", err, children)
	}
	if err = db.Migrator().DropTable(&pgSchemaVersion{}); err != nil {
		t.Fatal(err)
	}
	if err = runPostgresMigrations(ctx, db); err == nil {
		t.Fatal("accepted populated unversioned schema")
	}

}
