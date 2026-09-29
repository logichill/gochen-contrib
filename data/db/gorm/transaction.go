package gormdb

import (
	"context"
	"database/sql"
	"strings"

	"gochen-runtime/db/sql/safeident"
	core "gochen/db"
	"gochen/db/dialect"
	"gochen/errors"

	"gorm.io/gorm"
)

type transaction struct {
	db            *gorm.DB
	maxBindParams int
	owned         bool
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
	return &transaction{db: tx, maxBindParams: maxBind, owned: false}
}

// Query 处理查询。
func (t *transaction) Query(ctx context.Context, query string, args ...any) (core.IRows, error) {
	if ctx == nil {
		return nil, errors.NewCode(errors.InvalidInput, "ctx is nil")
	}
	if t == nil || t.db == nil {
		return nil, errors.NewCode(errors.InvalidInput, "transaction is nil")
	}
	sqlRows, err := t.db.WithContext(ctx).Raw(query, args...).Rows()
	if err != nil {
		return nil, err
	}
	return &dbRows{rows: sqlRows}, nil
}

// QueryRow 处理查询行。
func (t *transaction) QueryRow(ctx context.Context, query string, args ...any) core.IRow {
	if ctx == nil {
		return &dbRow{err: errors.NewCode(errors.InvalidInput, "ctx is nil")}
	}
	if t == nil || t.db == nil {
		return &dbRow{err: errors.NewCode(errors.InvalidInput, "transaction is nil")}
	}
	q := t.db.WithContext(ctx).Raw(query, args...)
	return &dbRow{row: q.Row(), err: q.Error}
}

// Exec 处理Exec。
func (t *transaction) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if ctx == nil {
		return nil, errors.NewCode(errors.InvalidInput, "ctx is nil")
	}
	if t == nil || t.db == nil {
		return nil, errors.NewCode(errors.InvalidInput, "transaction is nil")
	}
	res := t.db.WithContext(ctx).Exec(query, args...)
	if res.Error != nil {
		return nil, res.Error
	}
	return &dbResult{rowsAffected: res.RowsAffected}, nil
}

// Begin 处理Begin。
func (t *transaction) Begin(ctx context.Context) (core.ITransaction, error) {
	if ctx == nil {
		return nil, errors.NewCode(errors.InvalidInput, "ctx is nil")
	}
	return nil, errors.NewCode(errors.Unsupported, "nested transactions are not supported")
}

// BeginTx 处理Begin事务。
func (t *transaction) BeginTx(ctx context.Context, opts *sql.TxOptions) (core.ITransaction, error) {
	if ctx == nil {
		return nil, errors.NewCode(errors.InvalidInput, "ctx is nil")
	}
	_ = opts
	return nil, errors.NewCode(errors.Unsupported, "nested transactions are not supported")
}

// Ping 处理探测。
func (t *transaction) Ping(ctx context.Context) error {
	if ctx == nil {
		return errors.NewCode(errors.InvalidInput, "ctx is nil")
	}
	return nil
}

// Close 关闭当前资源。
func (t *transaction) Close() error {
	if t == nil || !t.owned || t.db == nil {
		return nil
	}
	if err := t.controlDB().Rollback().Error; err != nil && !errors.Is(err, sql.ErrTxDone) {
		return err
	}
	return nil
}

// controlDB 返回只用于事务控制的临时句柄。
//
// GORM 的 Commit/Rollback 会通过 AddError 修改 *gorm.DB.Error。控制操作的
// 错误不应污染事务句柄，否则后续 Close 或重试会被之前的错误阻断。
func (t *transaction) controlDB() *gorm.DB {
	db := t.db.Session(&gorm.Session{NewDB: true})
	db.Error = nil
	return db
}

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
	normalizedName, err := normalizeSavepointName(name)
	if err != nil {
		return err
	}
	if ctx == nil {
		return errors.NewCode(errors.InvalidInput, "ctx is nil")
	}
	if !t.SupportsSavepoints() {
		return t.db.WithContext(ctx).SavePoint(normalizedName).Error
	}
	return t.db.WithContext(ctx).Exec("SAVEPOINT " + normalizedName).Error
}

// RollbackToSavepoint 回滚到指定 savepoint。
func (t *transaction) RollbackToSavepoint(ctx context.Context, name string) error {
	if t == nil || t.db == nil {
		return errors.NewCode(errors.InvalidInput, "transaction is nil")
	}
	normalizedName, err := normalizeSavepointName(name)
	if err != nil {
		return err
	}
	if ctx == nil {
		return errors.NewCode(errors.InvalidInput, "ctx is nil")
	}
	if !t.SupportsSavepoints() {
		return t.db.WithContext(ctx).RollbackTo(normalizedName).Error
	}
	return t.db.WithContext(ctx).Exec("ROLLBACK TO SAVEPOINT " + normalizedName).Error
}

// ReleaseSavepoint 释放指定 savepoint。
func (t *transaction) ReleaseSavepoint(ctx context.Context, name string) error {
	if t == nil || t.db == nil {
		return errors.NewCode(errors.InvalidInput, "transaction is nil")
	}
	normalizedName, err := normalizeSavepointName(name)
	if err != nil {
		return err
	}
	if ctx == nil {
		return errors.NewCode(errors.InvalidInput, "ctx is nil")
	}
	dName := dialect.Name(t.DialectName())
	switch dName {
	case dialect.NamePostgres, dialect.NameSQLite, dialect.NameMySQL:
		res := t.db.WithContext(ctx).Exec("RELEASE SAVEPOINT " + normalizedName)
		return res.Error
	default:
		return nil
	}
}

func normalizeSavepointName(name string) (string, error) {
	normalized := strings.TrimSpace(name)
	if normalized == "" || strings.Contains(normalized, ".") || !safeident.IsSafeIdentifier(normalized) {
		return "", errors.NewCode(errors.InvalidInput, "invalid savepoint name").
			WithContext("savepoint", normalized)
	}
	return normalized, nil
}

// Commit 提交当前事务。
func (t *transaction) Commit() error { return t.controlDB().Commit().Error }

// Rollback 回滚当前事务。
func (t *transaction) Rollback() error { return t.controlDB().Rollback().Error }

var _ core.ITransaction = (*transaction)(nil)
var _ core.IDialectNameProvider = (*transaction)(nil)
var _ core.ISavepointCapabilityProvider = (*transaction)(nil)
var _ core.ISavepointTransaction = (*transaction)(nil)
var _ core.IBindParameterLimitProvider = (*transaction)(nil)
