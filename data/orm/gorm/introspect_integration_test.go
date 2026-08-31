package gormorm

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	gormdb "gochen-contrib/data/db/gorm"
	"gochen-runtime/db/schema/introspect"

	gormmysql "gorm.io/driver/mysql"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestMySQLIntrospectorIntegration(t *testing.T) {
	dsn := os.Getenv("GOCHEN_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set GOCHEN_MYSQL_DSN to run MySQL introspection integration test")
	}

	database := openGormIntegrationDatabase(t, gormmysql.Open(dsn))
	tableName := integrationTableName("mysql")
	execIntegrationSQL(t, database, fmt.Sprintf("DROP TABLE IF EXISTS `%s`", tableName))
	t.Cleanup(func() {
		_, _ = database.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS `%s`", tableName))
	})
	execIntegrationSQL(t, database, fmt.Sprintf("CREATE TABLE `%s` (id BIGINT PRIMARY KEY AUTO_INCREMENT, email VARCHAR(128) NOT NULL)", tableName))
	execIntegrationSQL(t, database, fmt.Sprintf("CREATE UNIQUE INDEX `%s_email_idx` ON `%s` (email)", tableName, tableName))

	got, err := introspect.NewMySQL().Inspect(context.Background(), database)
	if err != nil {
		t.Fatalf("Inspect MySQL: %v", err)
	}
	table, ok := got.FindTable("", tableName)
	if !ok {
		t.Fatalf("expected table %s", tableName)
	}
	if _, ok := table.FindIndex(tableName + "_email_idx"); !ok {
		t.Fatalf("expected email index, got %#v", table.Indexes)
	}
}

func TestPostgresIntrospectorIntegrationSkipsExpressionIndexes(t *testing.T) {
	dsn := os.Getenv("GOCHEN_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GOCHEN_POSTGRES_DSN to run Postgres introspection integration test")
	}

	database := openGormIntegrationDatabase(t, gormpostgres.Open(dsn))
	tableName := integrationTableName("postgres")
	execIntegrationSQL(t, database, fmt.Sprintf(`DROP TABLE IF EXISTS "%s"`, tableName))
	t.Cleanup(func() {
		_, _ = database.Exec(context.Background(), fmt.Sprintf(`DROP TABLE IF EXISTS "%s"`, tableName))
	})
	execIntegrationSQL(t, database, fmt.Sprintf(`CREATE TABLE "%s" (id BIGSERIAL PRIMARY KEY, email VARCHAR(128) NOT NULL, tenant_id BIGINT NOT NULL)`, tableName))
	execIntegrationSQL(t, database, fmt.Sprintf(`CREATE UNIQUE INDEX "%s_mixed_expr_idx" ON "%s" (lower(email), tenant_id)`, tableName, tableName))
	execIntegrationSQL(t, database, fmt.Sprintf(`CREATE INDEX "%s_tenant_idx" ON "%s" (tenant_id)`, tableName, tableName))

	got, err := introspect.NewPostgres().Inspect(context.Background(), database)
	if err != nil {
		t.Fatalf("Inspect Postgres: %v", err)
	}
	table, ok := got.FindTable("", tableName)
	if !ok {
		t.Fatalf("expected table %s", tableName)
	}
	if _, ok := table.FindIndex(tableName + "_tenant_idx"); !ok {
		t.Fatalf("expected simple tenant index, got %#v", table.Indexes)
	}
	if _, ok := table.FindIndex(tableName + "_mixed_expr_idx"); ok {
		t.Fatalf("expected mixed expression index to be skipped, got %#v", table.Indexes)
	}
}

func openGormIntegrationDatabase(t *testing.T, dialector gorm.Dialector) *gormdb.Database {
	t.Helper()
	gdb, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	database, err := gormdb.New(gdb)
	if err != nil {
		t.Fatalf("gormdb.New: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	return database
}

func execIntegrationSQL(t *testing.T, database *gormdb.Database, sql string) {
	t.Helper()
	if _, err := database.Exec(context.Background(), sql); err != nil {
		t.Fatalf("Exec %q: %v", sql, err)
	}
}

func integrationTableName(prefix string) string {
	return fmt.Sprintf("gochen_%s_introspect_%d", prefix, time.Now().UnixNano())
}
