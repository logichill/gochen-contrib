// Package gormfactory 提供可选的多驱动配置入口；基础 GORM 适配包不依赖本包。
package gormfactory

import (
	"context"
	"fmt"
	"strings"

	gormdb "gochen-contrib/data/db/gorm"
	core "gochen/db"
	"gochen/errors"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// NewFromConfig 根据配置选择数据库驱动，并应用连接池与适配器选项。
func NewFromConfig(ctx context.Context, cfg core.DBConfig, optFns ...gormdb.Option) (core.IDatabase, error) {
	if ctx == nil {
		return nil, errors.NewCode(errors.InvalidInput, "ctx cannot be nil")
	}
	driver := strings.TrimSpace(cfg.Driver)
	if driver == "" {
		driver = "sqlite"
	}

	var dialector gorm.Dialector
	switch strings.ToLower(driver) {
	case "mysql":
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=%t&loc=%s",
			cfg.Username,
			cfg.Password,
			cfg.Host,
			cfg.Port,
			cfg.Database,
			cfg.Charset,
			cfg.ParseTime,
			cfg.Location,
		)
		dialector = mysql.Open(dsn)
	case "postgres", "postgresql":
		sslmode := "require"
		if cfg.Options != nil {
			if mode, ok := cfg.Options["sslmode"].(string); ok && mode != "" {
				sslmode = mode
			}
		}

		dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
			cfg.Host,
			cfg.Port,
			cfg.Username,
			cfg.Password,
			cfg.Database,
			sslmode,
		)

		if sslmode != "disable" && cfg.Options != nil {
			if sslrootcert, ok := cfg.Options["sslrootcert"].(string); ok && sslrootcert != "" {
				dsn += fmt.Sprintf(" sslrootcert=%s", sslrootcert)
			}
			if sslcert, ok := cfg.Options["sslcert"].(string); ok && sslcert != "" {
				dsn += fmt.Sprintf(" sslcert=%s", sslcert)
			}
			if sslkey, ok := cfg.Options["sslkey"].(string); ok && sslkey != "" {
				dsn += fmt.Sprintf(" sslkey=%s", sslkey)
			}
		}
		dialector = postgres.Open(dsn)
	case "sqlite", "sqlite3":
		dsn := strings.TrimSpace(cfg.Database)
		if dsn == "" {
			return nil, errors.NewCode(errors.InvalidInput, "database name or DSN cannot be empty")
		}
		dialector = gormsqlite.Open(dsn)
	default:
		return nil, errors.NewCode(errors.InvalidInput, "unsupported database driver").WithContext("driver", driver)
	}

	database, err := gormdb.Open(ctx, dialector, cfg, optFns...)
	if err != nil {
		return nil, err
	}
	return database, nil
}

// NewFromDSN 根据驱动名称和 DSN 创建数据库适配器。
func NewFromDSN(ctx context.Context, driver, dsn string, optFns ...gormdb.Option) (core.IDatabase, error) {
	if ctx == nil {
		return nil, errors.NewCode(errors.InvalidInput, "ctx cannot be nil")
	}
	driver = strings.TrimSpace(driver)
	if driver == "" {
		driver = "sqlite"
	}
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, errors.NewCode(errors.InvalidInput, "dsn cannot be empty")
	}

	var dialector gorm.Dialector
	switch strings.ToLower(driver) {
	case "mysql":
		dialector = mysql.Open(dsn)
	case "postgres", "postgresql":
		dialector = postgres.Open(dsn)
	case "sqlite", "sqlite3":
		dialector = gormsqlite.Open(dsn)
	default:
		return nil, errors.NewCode(errors.InvalidInput, "unsupported database driver").WithContext("driver", driver)
	}

	database, err := gormdb.Open(ctx, dialector, core.DBConfig{}, optFns...)
	if err != nil {
		return nil, err
	}
	return database, nil
}
