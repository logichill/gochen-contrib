package gormdb

import (
	"context"
	"database/sql"
	"fmt"
	stdlog "log"
	"os"
	"strings"
	"time"

	"gochen-runtime/db/sql/safeident"
	core "gochen/db"
	"gochen/db/dialect"
	"gochen/errors"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Database 是 GORM 的 gochen/db.IDatabase 适配器。
//
// 说明：
// - 该适配器仅提供 gochen 所需的最小能力（Query/Exec/Tx/Ping/Close/Raw）；
// - 更复杂的 ORM 能力建议使用 gochen-contrib/data/orm/gorm 适配器。
type Database struct {
	db            *gorm.DB
	maxBindParams int
}

// LoggerConfig 定义 GORM SQL 日志配置。
type LoggerConfig struct {
	Level                string
	SlowThreshold        time.Duration
	IgnoreRecordNotFound bool
}

type options struct {
	logger        LoggerConfig
	maxBindParams int
}

// Option 定义数据库适配器可选项。
type Option func(*options)

// WithLoggerConfig 设置 GORM SQL 日志配置。
func WithLoggerConfig(cfg LoggerConfig) Option {
	return func(opts *options) {
		opts.logger = cfg
	}
}

// WithMaxBindParameters 设置单条语句占位参数上限。
func WithMaxBindParameters(limit int) Option {
	return func(opts *options) {
		opts.maxBindParams = limit
	}
}

func defaultOptions() options {
	return options{
		logger: LoggerConfig{
			Level:                "info",
			SlowThreshold:        200 * time.Millisecond,
			IgnoreRecordNotFound: false,
		},
	}
}

// New 创建Database。
func New(db *gorm.DB, optFns ...Option) (*Database, error) {
	if db == nil {
		return nil, errors.NewCode(errors.InvalidInput, "gorm db cannot be nil")
	}
	opts := defaultOptions()
	for _, apply := range optFns {
		if apply != nil {
			apply(&opts)
		}
	}
	if err := dialect.ValidateMaxBindParameters(opts.maxBindParams); err != nil {
		return nil, errors.Wrap(err, errors.InvalidInput, "invalid max bind parameters configuration")
	}
	return &Database{db: db, maxBindParams: opts.maxBindParams}, nil
}

// NewFromConfig 创建从配置。
func NewFromConfig(ctx context.Context, cfg core.DBConfig, optFns ...Option) (core.IDatabase, error) {
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

// NewFromDSN 创建从DSN。
func NewFromDSN(ctx context.Context, driver, dsn string, optFns ...Option) (core.IDatabase, error) {
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

	opts := defaultOptions()
	for _, apply := range optFns {
		if apply != nil {
			apply(&opts)
		}
	}
	if err := dialect.ValidateMaxBindParameters(opts.maxBindParams); err != nil {
		return nil, errors.Wrap(err, errors.InvalidInput, "invalid max bind parameters configuration")
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

	gormCfg := buildGORMConfig(opts)
	gdb, err := gorm.Open(dialector, gormCfg)
	if err != nil {
		return nil, errors.Wrap(err, errors.Database, "open gorm db failed")
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, errors.Wrap(err, errors.Database, "get sql.DB failed")
	}

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, errors.Wrap(err, errors.Database, "failed to ping database")
	}

	return &Database{db: gdb, maxBindParams: opts.maxBindParams}, nil
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

// Query 处理查询。
func (g *Database) Query(ctx context.Context, query string, args ...any) (core.IRows, error) {
	sqlRows, err := g.db.WithContext(ctx).Raw(query, args...).Rows()
	if err != nil {
		return nil, err
	}
	return &dbRows{rows: sqlRows}, nil
}

// QueryRow 处理查询行。
func (g *Database) QueryRow(ctx context.Context, query string, args ...any) core.IRow {
	sqlRow := g.db.WithContext(ctx).Raw(query, args...).Row()
	return &dbRow{row: sqlRow}
}

// Exec 处理Exec。
func (g *Database) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	res := g.db.WithContext(ctx).Exec(query, args...)
	if res.Error != nil {
		return nil, res.Error
	}
	return &dbResult{rowsAffected: res.RowsAffected}, nil
}

// Begin 处理Begin。
func (g *Database) Begin(ctx context.Context) (core.ITransaction, error) { return g.BeginTx(ctx, nil) }

// BeginTx 处理Begin事务。
func (g *Database) BeginTx(ctx context.Context, opts *sql.TxOptions) (core.ITransaction, error) {
	tx := g.db.WithContext(ctx).Begin(opts)
	if tx.Error != nil {
		return nil, tx.Error
	}
	return &transaction{db: tx, maxBindParams: g.maxBindParams}, nil
}

// Ping 处理探测。
func (g *Database) Ping(ctx context.Context) error {
	sqlDB, err := g.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// Close 关闭当前资源。
func (g *Database) Close() error {
	sqlDB, err := g.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// GormDB 返回底层 GORM 连接，仅供适配层使用。
func (g *Database) GormDB() *gorm.DB {
	if g == nil {
		return nil
	}
	return g.db
}

// DialectName 返回Dialect名称。
func (g *Database) DialectName() string {
	if g == nil {
		return ""
	}
	return dialectName(g.db)
}

// SupportsSavepoints 返回当前方言是否支持 savepoints。
func (g *Database) SupportsSavepoints() bool {
	if g == nil {
		return false
	}
	return dialect.FromDatabase(g).SupportsSavepoints()
}

// MaxBindParameters 返回单条语句绑定的占位参数上限。
func (g *Database) MaxBindParameters() int {
	if g == nil {
		return dialect.DefaultMaxBindParameters
	}
	return dialect.ResolveMaxBindParameters(g.maxBindParams)
}

func dialectName(db *gorm.DB) string {
	if db == nil || db.Dialector == nil {
		return ""
	}
	return db.Dialector.Name()
}

type transaction struct {
	db            *gorm.DB
	maxBindParams int
}

// NewTransactionDatabase 返回绑定到既有 GORM 事务的数据库适配器。
//
// 说明：返回值的 Close 为 no-op，避免 session.Database() 的清理调用误关底层连接池。
func NewTransactionDatabase(tx *gorm.DB, optFns ...Option) core.IDatabase {
	if tx == nil {
		return nil
	}
	opts := defaultOptions()
	for _, apply := range optFns {
		if apply != nil {
			apply(&opts)
		}
	}
	maxBind := opts.maxBindParams
	if err := dialect.ValidateMaxBindParameters(maxBind); err != nil {
		maxBind = dialect.DefaultMaxBindParameters
	}
	return &transaction{db: tx, maxBindParams: maxBind}
}

// Query 处理查询。
func (t *transaction) Query(ctx context.Context, query string, args ...any) (core.IRows, error) {
	sqlRows, err := t.db.WithContext(ctx).Raw(query, args...).Rows()
	if err != nil {
		return nil, err
	}
	return &dbRows{rows: sqlRows}, nil
}

// QueryRow 处理查询行。
func (t *transaction) QueryRow(ctx context.Context, query string, args ...any) core.IRow {
	sqlRow := t.db.WithContext(ctx).Raw(query, args...).Row()
	return &dbRow{row: sqlRow}
}

// Exec 处理Exec。
func (t *transaction) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	res := t.db.WithContext(ctx).Exec(query, args...)
	if res.Error != nil {
		return nil, res.Error
	}
	return &dbResult{rowsAffected: res.RowsAffected}, nil
}

// Begin 处理Begin。
func (t *transaction) Begin(ctx context.Context) (core.ITransaction, error) {
	return nil, errors.NewCode(errors.Unsupported, "nested transactions are not supported")
}

// BeginTx 处理Begin事务。
func (t *transaction) BeginTx(ctx context.Context, opts *sql.TxOptions) (core.ITransaction, error) {
	_ = ctx
	_ = opts
	return nil, errors.NewCode(errors.Unsupported, "nested transactions are not supported")
}

// Ping 处理探测。
func (t *transaction) Ping(ctx context.Context) error { return nil }

// Close 关闭当前资源。
func (t *transaction) Close() error { return nil }

// GormDB 返回事务绑定的底层 GORM 连接，仅供适配层使用。
func (t *transaction) GormDB() *gorm.DB {
	if t == nil {
		return nil
	}
	return t.db
}

// DialectName 返回事务继承的数据库方言名称。
func (t *transaction) DialectName() string {
	if t == nil {
		return ""
	}
	return dialectName(t.db)
}

// SupportsSavepoints 返回当前事务是否支持 savepoints。
func (t *transaction) SupportsSavepoints() bool {
	if t == nil {
		return false
	}
	return dialect.FromDatabase(t).SupportsSavepoints()
}

// MaxBindParameters 把绑定参数上限透传进事务。
func (t *transaction) MaxBindParameters() int {
	if t == nil {
		return dialect.DefaultMaxBindParameters
	}
	return dialect.ResolveMaxBindParameters(t.maxBindParams)
}

// CreateSavepoint 创建指定名称的 savepoint。
func (t *transaction) CreateSavepoint(ctx context.Context, name string) error {
	if t == nil || t.db == nil {
		return errors.NewCode(errors.InvalidInput, "transaction is nil")
	}
	if err := validateSavepointName(name); err != nil {
		return err
	}
	return t.db.WithContext(ctx).SavePoint(name).Error
}

// RollbackToSavepoint 回滚到指定 savepoint。
func (t *transaction) RollbackToSavepoint(ctx context.Context, name string) error {
	if t == nil || t.db == nil {
		return errors.NewCode(errors.InvalidInput, "transaction is nil")
	}
	if err := validateSavepointName(name); err != nil {
		return err
	}
	return t.db.WithContext(ctx).RollbackTo(name).Error
}

// ReleaseSavepoint 释放指定 savepoint。
func (t *transaction) ReleaseSavepoint(ctx context.Context, name string) error {
	if t == nil || t.db == nil {
		return errors.NewCode(errors.InvalidInput, "transaction is nil")
	}
	if err := validateSavepointName(name); err != nil {
		return err
	}
	dName := dialect.Name(t.DialectName())
	switch dName {
	case dialect.NamePostgres, dialect.NameSQLite, dialect.NameMySQL:
		res := t.db.WithContext(ctx).Exec("RELEASE SAVEPOINT " + name)
		return res.Error
	default:
		return nil
	}
}

func validateSavepointName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, ".") || !safeident.IsSafeIdentifier(name) {
		return errors.NewCode(errors.InvalidInput, "invalid savepoint name").
			WithContext("savepoint", name)
	}
	return nil
}

// Commit 提交当前事务。
func (t *transaction) Commit() error { return t.db.Commit().Error }

// Rollback 回滚当前事务。
func (t *transaction) Rollback() error { return t.db.Rollback().Error }

type dbRows struct{ rows *sql.Rows }

// Next 推进到下一项并返回是否成功。
func (r *dbRows) Next() bool { return r.rows.Next() }

// Scan 把当前结果写入目标对象。
func (r *dbRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }

// Close 关闭当前资源。
func (r *dbRows) Close() error { return r.rows.Close() }

// Err 处理Err。
func (r *dbRows) Err() error { return r.rows.Err() }

// Columns 处理Columns。
func (r *dbRows) Columns() ([]string, error) { return r.rows.Columns() }

// ColumnTypes 处理Column类型列表。
func (r *dbRows) ColumnTypes() ([]*sql.ColumnType, error) { return r.rows.ColumnTypes() }

type dbRow struct{ row *sql.Row }

// Scan 把当前结果写入目标对象。
func (r *dbRow) Scan(dest ...any) error { return r.row.Scan(dest...) }

// Err 处理Err。
func (r *dbRow) Err() error { return r.row.Err() }

type dbResult struct {
	rowsAffected int64
}

// LastInsertId 返回最后插入的主键。
func (r *dbResult) LastInsertId() (int64, error) {
	return 0, errors.NewCode(errors.Unsupported, "LastInsertId not supported")
}

// RowsAffected 返回受影响的行数。
func (r *dbResult) RowsAffected() (int64, error) { return r.rowsAffected, nil }

var _ core.IDatabase = (*Database)(nil)
var _ core.IDialectNameProvider = (*Database)(nil)
var _ core.ISavepointCapabilityProvider = (*Database)(nil)
var _ core.IBindParameterLimitProvider = (*Database)(nil)

var _ core.ITransaction = (*transaction)(nil)
var _ core.IDialectNameProvider = (*transaction)(nil)
var _ core.ISavepointCapabilityProvider = (*transaction)(nil)
var _ core.ISavepointTransaction = (*transaction)(nil)
var _ core.IBindParameterLimitProvider = (*transaction)(nil)
