package gormdb

import (
	"context"
	stdlog "log"
	"os"
	"strings"
	"time"

	core "gochen/db"
	"gochen/db/dialect"
	"gochen/errors"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open 使用调用方选择的 Dialector 创建并探测连接；cfg 只提供连接池和参数预算配置。
func Open(ctx context.Context, dialector gorm.Dialector, cfg core.DBConfig, optFns ...Option) (*Database, error) {
	if dialector == nil {
		return nil, errors.NewCode(errors.InvalidInput, "gorm dialector cannot be nil")
	}
	if ctx == nil {
		return nil, errors.NewCode(errors.InvalidInput, "ctx cannot be nil")
	}
	opts := defaultOptions()
	for _, apply := range optFns {
		if apply != nil {
			apply(&opts)
		}
	}

	maxBind := cfg.MaxBindParameters
	if opts.maxBindParams > 0 {
		maxBind = opts.maxBindParams
	}
	if err := dialect.ValidateMaxBindParameters(maxBind); err != nil {
		return nil, errors.Wrap(err, errors.InvalidInput, "invalid max bind parameters configuration")
	}

	gormCfg := buildGORMConfig(opts)
	gdb, err := gorm.Open(dialector, gormCfg)
	if err != nil {
		return nil, errors.Wrap(err, errors.Database, "open gorm db failed")
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, errors.Wrap(err, errors.Database, "get sql.DB failed")
	}

	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetime) * time.Second)
	}
	if cfg.ConnMaxIdleTime > 0 {
		sqlDB.SetConnMaxIdleTime(time.Duration(cfg.ConnMaxIdleTime) * time.Second)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, errors.Wrap(err, errors.Database, "failed to ping database")
	}

	return &Database{db: gdb, maxBindParams: maxBind}, nil
}

func buildGORMConfig(opts options) *gorm.Config {
	return &gorm.Config{
		Logger: logger.New(
			stdlog.New(os.Stdout, "\r\n", stdlog.LstdFlags),
			logger.Config{
				SlowThreshold:             opts.logger.SlowThreshold,
				IgnoreRecordNotFoundError: opts.logger.IgnoreRecordNotFound,
				LogLevel:                  parseGORMLogLevel(opts.logger.Level),
				Colorful:                  false,
			},
		),
	}
}

func parseGORMLogLevel(raw string) logger.LogLevel {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "silent":
		return logger.Silent
	case "error":
		return logger.Error
	case "warn", "":
		return logger.Warn
	case "info":
		return logger.Info
	default:
		return logger.Warn
	}
}
