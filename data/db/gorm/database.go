package gormdb

import (
	"context"
	"database/sql"

	core "gochen/db"
	"gochen/db/dialect"
	"gochen/errors"

	"gorm.io/gorm"
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

var _ core.IDatabase = (*Database)(nil)
var _ core.IDialectNameProvider = (*Database)(nil)
var _ core.ISavepointCapabilityProvider = (*Database)(nil)
var _ core.IBindParameterLimitProvider = (*Database)(nil)
