package dbcompat

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestNormalizeSQL(t *testing.T) {
	cases := map[string]string{
		"SELECT `id` FROM `user` WHERE `email` = $1":                                 `SELECT "id" FROM "user" WHERE "email" = $1`,
		"SELECT user.id FROM user JOIN user_tenant ON user.id = user_tenant.user_id": `SELECT "user".id FROM "user" JOIN user_tenant ON "user".id = user_tenant.user_id`,
		"SELECT 'user `id`', \"user\", $1 -- user `x`\n/* user `y` */":               "SELECT 'user `id`', \"user\", $1 -- user `x`\n/* user `y` */",
		"SELECT $$ user `x` $$, $tag$user `y`$tag$, `a\"b`":                          "SELECT $$ user `x` $$, $tag$user `y`$tag$, \"a\"\"b\"",
		"SELECT 'it''s `literal`', current_user, user_id":                            "SELECT 'it''s `literal`', current_user, user_id",
	}
	for input, want := range cases {
		if got := NormalizeSQL(input); got != want {
			t.Errorf("normalize %q: got %q, want %q", input, got, want)
		}
	}
}

type testJSON map[string]interface{}

func (j testJSON) Value() (driver.Value, error) { return json.Marshal(j) }
func (j *testJSON) Scan(value interface{}) error {
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, j)
	case string:
		return json.Unmarshal([]byte(v), j)
	case nil:
		*j = nil
		return nil
	default:
		return errors.New("invalid JSON value")
	}
}
func (j testJSON) GormDataType() string { return "longtext" }

type compatibilityRecord struct {
	ID      string    `gorm:"primaryKey;size:32"`
	Payload testJSON  `gorm:"type:longtext;not null"`
	Value   string    `gorm:"type:longtext"`
	Created time.Time `gorm:"type:datetime"`
}

func (compatibilityRecord) TableName() string { return "vsm_compat_test" }

func testDB(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	dsn := os.Getenv("VSM_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("VSM_PG_TEST_DSN is required for real PostgreSQL integration tests")
	}
	db, err := Open(dsn, &gorm.Config{TranslateError: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	return db, dsn
}

func TestPostgresMigrationCRUDAndRollback(t *testing.T) {
	db, _ := testDB(t)
	if err := db.Migrator().DropTable(&compatibilityRecord{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Migrator().DropTable(&compatibilityRecord{}) })
	if err := db.AutoMigrate(&compatibilityRecord{}); err != nil {
		t.Fatal(err)
	}
	payload := testJSON{"text": "Русский текст с `кавычками` и user", "nested": map[string]interface{}{"n": 3.0}}
	record := compatibilityRecord{ID: "initial", Payload: payload, Value: "literal ' ` user", Created: time.Now().UTC().Truncate(time.Second)}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&compatibilityRecord{}); err != nil {
		t.Fatal("second migration:", err)
	}
	var found compatibilityRecord
	if err := db.Raw("SELECT * FROM `vsm_compat_test` WHERE `id` = ?", record.ID).Scan(&found).Error; err != nil {
		t.Fatal(err)
	}
	if found.Value != record.Value || found.Payload["text"] != payload["text"] || !found.Created.Equal(record.Created) {
		t.Fatalf("roundtrip differs: %#v", found)
	}
	aborted := errors.New("abort transaction")
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE `vsm_compat_test` SET `value` = ? WHERE `id` = ?", "changed", "initial").Error; err != nil {
			return err
		}
		return aborted
	})
	if !errors.Is(err, aborted) {
		t.Fatal(err)
	}
	if err := db.First(&found, "id = ?", "initial").Error; err != nil {
		t.Fatal(err)
	}
	if found.Value != record.Value {
		t.Fatal("transaction was not rolled back")
	}
	if err := db.Create(&record).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("expected duplicate key, got %v", err)
	}
	row := map[string]interface{}{"id": "map-insert", "payload": `{"text":"Вставка map"}`, "value": "map"}
	if err := db.Model(&compatibilityRecord{}).Create(row).Error; err != nil {
		t.Fatal("map insert with explicit schema:", err)
	}
	if err := db.Where("id = ?", "map-insert").Delete(&compatibilityRecord{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&record).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&compatibilityRecord{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("delete failed: %d %v", count, err)
	}
}

func TestPostgresLockContentionAndCancelledOwner(t *testing.T) {
	_, dsn := testDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	unlock, err := AcquireSessionLock(ctx, dsn, "vsm-compat-lock-test")
	if err != nil {
		t.Fatal(err)
	}
	waiter, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	if _, err = AcquireSessionLock(waiter, dsn, "vsm-compat-lock-test"); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("lock should time out, got %v", err)
	}
	cancel()
	if err = unlock(); err != nil {
		t.Fatal("cancelled owner release:", err)
	}
	retry, stopRetry := context.WithTimeout(context.Background(), time.Second)
	defer stopRetry()
	unlock, err = AcquireSessionLock(retry, dsn, "vsm-compat-lock-test")
	if err != nil {
		t.Fatal("lock leaked:", err)
	}
	if err = unlock(); err != nil {
		t.Fatal(err)
	}
}
