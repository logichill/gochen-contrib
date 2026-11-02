//go:build integration

package gormorm

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	gormdb "gochen-contrib/data/db/gorm"
	ormcontract "gochen-runtime/db/orm/contracttest"
	"gochen-runtime/db/orm/repo"
	"gochen/db/orm"
	"gochen/testkit"
	"gochen/testkit/contracttest"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	gormmysql "gorm.io/driver/mysql"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestExternalRepositoryContracts 在独立测试命名空间中复用 ORM 和维度契约。
// DSN 对应账号须有创建/清理 MySQL 数据库或 PostgreSQL schema 的权限。
func TestExternalRepositoryContracts(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			env := "GOCHEN_MYSQL_DSN"
			if dialect == "postgres" {
				env = "GOCHEN_POSTGRES_DSN"
			}
			dsn := os.Getenv(env)
			if dsn == "" {
				if os.Getenv("GOCHEN_REQUIRE_DATABASE_TESTS") == "1" {
					t.Fatal("database DSN is required for this verification run")
				}
				t.Skip("set " + env + " to run repository contracts")
			}
			t.Run("ORM", func(t *testing.T) {
				ormcontract.Run(t, func(t *testing.T) orm.IOrm {
					return newExternalContractORM(t, dialect, dsn)
				})
			})
			t.Run("Dimensions", func(t *testing.T) {
				contracttest.RunDimensions(t, func(t *testing.T) contracttest.Repository {
					engine := newExternalContractORM(t, dialect, dsn)
					if err := engine.db.Table("documents").AutoMigrate(&contracttest.Document{}); err != nil {
						t.Fatal(err)
					}
					r, err := repo.NewRepo[*contracttest.Document, int64](engine, "documents",
						repo.WithIDGenerator[*contracttest.Document, int64](testkit.NewInt64Sequence(1)),
						repo.WithResourceContract[*contracttest.Document, int64](contracttest.Contract()),
						repo.WithIsolation[*contracttest.Document, int64](repo.IsolationCols{Column: "tenant"}),
						repo.WithResourceKind[*contracttest.Document, int64]("document"))
					if err != nil {
						t.Fatal(err)
					}
					return r
				})
			})
		})
	}
}

func newExternalContractORM(t *testing.T, dialect, dsn string) *Orm {
	t.Helper()
	config := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	var adminDialector gorm.Dialector
	if dialect == "mysql" {
		adminDialector = gormmysql.Open(dsn)
	} else {
		adminDialector = gormpostgres.Open(dsn)
	}
	admin, err := gorm.Open(adminDialector, config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := adminSQL.Close(); err != nil {
			t.Error(err)
		}
	})
	name := fmt.Sprintf("gochen_contract_%d", time.Now().UnixNano())
	kind, dropSuffix := "DATABASE", ""
	if dialect == "postgres" {
		kind, dropSuffix = "SCHEMA", " CASCADE"
	}
	if err := admin.Exec("CREATE " + kind + " " + name).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP " + kind + " " + name + dropSuffix).Error; err != nil {
			t.Error(err)
		}
	})
	var connection *sql.DB
	var target gorm.Dialector
	if dialect == "mysql" {
		cfg, err := mysqldriver.ParseDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
		cfg.DBName = name
		connector, err := mysqldriver.NewConnector(cfg)
		if err != nil {
			t.Fatal(err)
		}
		connection = sql.OpenDB(connector)
		target = gormmysql.New(gormmysql.Config{Conn: connection})
	} else {
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			t.Fatal(err)
		}
		cfg.RuntimeParams["search_path"] = name
		connection = stdlib.OpenDB(*cfg)
		target = gormpostgres.New(gormpostgres.Config{Conn: connection})
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})
	gdb, err := gorm.Open(target, config)
	if err != nil {
		t.Fatal(err)
	}
	database, err := gormdb.New(gdb)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewFromDatabase(database)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}
