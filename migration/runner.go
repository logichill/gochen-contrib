package migration

import (
	"context"
	"strings"

	_ "gochen-contrib/data/db/driver"
	gomigrate "gochen-runtime/db/migrate"
	"gochen-runtime/db/sql/stdsql"
	"gochen/db"
	"gochen/errors"
)

// Config 描述项目执行 migration 所需的配置。
type Config struct {
	// Database 允许调用方传入已经打开的 gochen 数据库。
	Database db.IDatabase
	// DBConfig 在 Database 为空时用于打开数据库。
	DBConfig db.DBConfig
	// Driver 和 DSN 是 DBConfig.Driver / DBConfig.Database 的快捷配置。
	Driver string
	DSN    string
	// MigrationType 用于区分 schema/demo 等独立迁移链；为空时使用 gochen 默认 schema。
	MigrationType string
	// Source 允许调用方传入 embed 或自定义 migration 来源。
	Source gomigrate.ISource
	// SourceDir 在 Source 为空时用于创建本地文件来源。
	SourceDir string
	// Options 会透传给 gochen-runtime/db/migrate.NewRunner。
	Options []gomigrate.RunnerOption
}

// Runner 持有 migration runner，并在数据库由本包打开时负责关闭它。
type Runner struct {
	database db.IDatabase
	runner   *gomigrate.Runner
	closeDB  bool
}

// NewRunner 根据项目传入的配置创建 migration runner。
func NewRunner(ctx context.Context, cfg Config) (*Runner, error) {
	source, err := migrationSource(cfg)
	if err != nil {
		return nil, err
	}
	database, closeDB, err := migrationDatabase(ctx, cfg)
	if err != nil {
		return nil, err
	}
	options := append([]gomigrate.RunnerOption{}, cfg.Options...)
	if strings.TrimSpace(cfg.MigrationType) != "" {
		options = append(options, gomigrate.WithMigrationType(cfg.MigrationType))
	}
	runner, err := gomigrate.NewRunner(database, source, options...)
	if err != nil {
		if closeDB {
			_ = database.Close()
		}
		return nil, err
	}
	return &Runner{database: database, runner: runner, closeDB: closeDB}, nil
}

// Up 确保所有待执行 migration 已应用；ErrNoChange 视为成功。
func Up(ctx context.Context, cfg Config) error {
	runner, err := NewRunner(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = runner.Close() }()
	if err := runner.Up(ctx); err != nil && !errors.Is(err, gomigrate.ErrNoChange) {
		return err
	}
	return nil
}

// Up 执行所有待执行 migration。
func (r *Runner) Up(ctx context.Context) error {
	if r == nil || r.runner == nil {
		return errors.NewCode(errors.InvalidInput, "migration runner is nil")
	}
	return r.runner.Up(ctx)
}

// Migrate 迁移到指定版本。
func (r *Runner) Migrate(ctx context.Context, version uint64) error {
	if r == nil || r.runner == nil {
		return errors.NewCode(errors.InvalidInput, "migration runner is nil")
	}
	return r.runner.Migrate(ctx, version)
}

// Steps 按指定步数向上或向下迁移。
func (r *Runner) Steps(ctx context.Context, steps int) error {
	if r == nil || r.runner == nil {
		return errors.NewCode(errors.InvalidInput, "migration runner is nil")
	}
	return r.runner.Steps(ctx, steps)
}

// Version 返回当前版本和 dirty 状态。
func (r *Runner) Version(ctx context.Context) (uint64, bool, error) {
	if r == nil || r.runner == nil {
		return 0, false, errors.NewCode(errors.InvalidInput, "migration runner is nil")
	}
	return r.runner.Version(ctx)
}

// Status 返回当前 migration 状态。
func (r *Runner) Status(ctx context.Context) (gomigrate.Status, error) {
	if r == nil || r.runner == nil {
		return gomigrate.Status{}, errors.NewCode(errors.InvalidInput, "migration runner is nil")
	}
	return r.runner.Status(ctx)
}

// Force 设置当前 migration 版本并清理 dirty 状态。
func (r *Runner) Force(ctx context.Context, version uint64) error {
	if r == nil || r.runner == nil {
		return errors.NewCode(errors.InvalidInput, "migration runner is nil")
	}
	return r.runner.Force(ctx, version)
}

// Close 仅在数据库由本包打开时关闭连接。
func (r *Runner) Close() error {
	if r == nil || !r.closeDB || r.database == nil {
		return nil
	}
	return r.database.Close()
}

func migrationSource(cfg Config) (gomigrate.ISource, error) {
	if cfg.Source != nil {
		return cfg.Source, nil
	}
	if strings.TrimSpace(cfg.SourceDir) == "" {
		return nil, errors.NewCode(errors.InvalidInput, "migration source dir cannot be empty")
	}
	return gomigrate.NewFileSource(cfg.SourceDir)
}

func migrationDatabase(ctx context.Context, cfg Config) (db.IDatabase, bool, error) {
	if cfg.Database != nil {
		return cfg.Database, false, nil
	}
	dbConfig := cfg.DBConfig
	if strings.TrimSpace(cfg.Driver) != "" {
		dbConfig.Driver = strings.TrimSpace(cfg.Driver)
	}
	if strings.TrimSpace(cfg.DSN) != "" {
		dbConfig.Database = strings.TrimSpace(cfg.DSN)
	}
	if strings.TrimSpace(dbConfig.Driver) == "" {
		dbConfig.Driver = "sqlite"
	}
	if strings.TrimSpace(dbConfig.Database) == "" {
		return nil, false, errors.NewCode(errors.InvalidInput, "migration database dsn cannot be empty")
	}
	database, err := stdsql.NewWithContext(ctx, dbConfig)
	if err != nil {
		return nil, false, err
	}
	return database, true, nil
}
