// Offline migration utility. DSNs are supplied through private environment only.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"os/signal"
	"ragflow/internal/dao"
	"ragflow/internal/dbcompat"
	"syscall"
)

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if os.Getenv("RAGFLOW_IMPORT_OFFLINE") != "yes" {
		return fmt.Errorf("stop source/target services and set RAGFLOW_IMPORT_OFFLINE=yes")
	}
	sourceDSN, targetDSN := os.Getenv("RAGFLOW_IMPORT_MYSQL_DSN"), os.Getenv("RAGFLOW_IMPORT_POSTGRES_DSN")
	if sourceDSN == "" || targetDSN == "" {
		return fmt.Errorf("both import DSN environment variables are required")
	}
	config := &gorm.Config{TranslateError: true, Logger: logger.Default.LogMode(logger.Silent)}
	src, err := gorm.Open(mysql.Open(sourceDSN), config)
	if err != nil {
		return fmt.Errorf("cannot connect source MySQL")
	}
	sourcePool, _ := src.DB()
	defer sourcePool.Close()
	dst, err := dbcompat.Open(targetDSN, config)
	if err != nil {
		return fmt.Errorf("cannot connect target PostgreSQL")
	}
	targetPool, _ := dst.DB()
	defer targetPool.Close()
	release, err := dbcompat.AcquireSessionLock(ctx, targetDSN, "vsm-ragflow-postgres-schema")
	if err != nil {
		return err
	}
	defer release()
	if err = dao.PreparePostgresMetadata(ctx, dst); err != nil {
		return err
	}
	report, err := dao.ImportMySQLMetadata(ctx, src, dst)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
