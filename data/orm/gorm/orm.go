package gormorm

import (
	"context"
	"database/sql"

	"gochen-contrib/data/db/gorm"
	"gochen/contextx"
	"gochen/db"
	"gochen/db/orm"
	"gochen/errors"

	"gorm.io/gorm"
)

// Orm 定义Orm。
type Orm struct {
	db           *gorm.DB
	database     db.IDatabase
	capabilities orm.Capabilities
}

// New 创建Orm。
func New(db *gorm.DB) (*Orm, error) {
	if db == nil {
		return nil, errors.NewCode(errors.InvalidInput, "gorm db cannot be nil")
	}
	return &Orm{
		db: db,
		capabilities: orm.NewCapabilities(
			orm.CapabilityBasicCRUD,
			orm.CapabilityQuery,
			orm.CapabilityPreload,
			orm.CapabilityAssociationWrite,
			orm.CapabilityBatchWrite,
			orm.CapabilityTransaction,
			orm.CapabilityOptimisticLock,
		),
	}, nil
}

// NewFromDatabase 创建从Database。
func NewFromDatabase(db db.IDatabase) (*Orm, error) {
	if db == nil {
		return nil, errors.NewCode(errors.InvalidInput, "database cannot be nil")
	}
	gdb, ok := gormdb.DBOf(db)
	if !ok || gdb == nil {
		return nil, errors.NewCode(errors.InvalidInput, "database is not backed by *gorm.DB")
	}
	o, err := New(gdb)
	if err != nil {
		return nil, err
	}
	o.database = db
	return o, nil
}

// Capabilities 处理Capabilities。
func (g *Orm) Capabilities() orm.Capabilities { return g.capabilities }

// WithContext 处理带上下文。
func (g *Orm) WithContext(ctx context.Context) orm.IOrm {
	if ctx == nil {
		ctx = contextx.Background()
	}
	return &Orm{db: g.db.WithContext(ctx), database: g.database, capabilities: g.capabilities}
}

// Model 处理模型。
func (g *Orm) Model(meta *orm.ModelMeta) (orm.IModel, error) {
	if meta == nil {
		return nil, errors.NewCode(errors.InvalidInput, "orm model meta cannot be nil")
	}
	resolvedMeta, err := resolveModelMeta(g.db, meta)
	if err != nil {
		return nil, err
	}
	return &model{db: g.db, meta: resolvedMeta, capabilities: g.capabilities}, nil
}

// Begin 处理Begin。
func (g *Orm) Begin(ctx context.Context) (orm.IOrmSession, error) { return g.BeginTx(ctx, nil) }

// BeginTx 处理Begin事务。
func (g *Orm) BeginTx(ctx context.Context, opts *sql.TxOptions) (orm.IOrmSession, error) {
	if ctx == nil {
		ctx = contextx.Background()
	}
	tx := g.db.WithContext(ctx).Begin(opts)
	if tx.Error != nil {
		return nil, tx.Error
	}
	var optFns []gormdb.Option
	if p, ok := g.database.(db.IBindParameterLimitProvider); ok {
		optFns = append(optFns, gormdb.WithMaxBindParameters(p.MaxBindParameters()))
	}
	return &session{
		Orm:         Orm{db: tx, database: gormdb.NewTransactionDatabase(tx, optFns...), capabilities: g.capabilities},
		afterCommit: contextx.NewAfterCommitDispatcher(),
	}, nil
}

// Database 处理Database。
func (g *Orm) Database() db.IDatabase { return g.database }

// NamingConvention 返回当前 ORM 实例的列名命名规范。
func (g *Orm) NamingConvention() db.NamingConvention {
	if g == nil || g.db == nil || g.db.NamingStrategy == nil {
		return db.DefaultNamingConvention()
	}
	namer := g.db.NamingStrategy
	return db.NamingConvention{
		TenantColumn:    namer.ColumnName("", "TenantID"),
		ScopeColumn:     namer.ColumnName("", "ManagedScopeID"),
		OwnerIDColumn:   namer.ColumnName("", "OwnerID"),
		VersionColumn:   namer.ColumnName("", "Version"),
		CreatedAtColumn: namer.ColumnName("", "CreatedAt"),
		CreatedByColumn: namer.ColumnName("", "CreatedBy"),
		UpdatedAtColumn: namer.ColumnName("", "UpdatedAt"),
		UpdatedByColumn: namer.ColumnName("", "UpdatedBy"),
		DeletedAtColumn: namer.ColumnName("", "DeletedAt"),
		DeletedByColumn: namer.ColumnName("", "DeletedBy"),
	}.WithDefaults()
}

var _ orm.IOrm = (*Orm)(nil)
var _ db.INamingConventionProvider = (*Orm)(nil)
