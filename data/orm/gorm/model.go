package gormorm

import (
	"context"
	"database/sql"
	stdErrors "errors"
	"fmt"
	"strings"

	"gochen-runtime/db/sql/safeident"
	"gochen/contextx"
	"gochen/db"
	"gochen/db/dialect"
	"gochen/db/orm"
	"gochen/errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type model struct {
	db           *gorm.DB
	meta         *orm.ModelMeta
	capabilities orm.Capabilities
}

// Meta 处理Meta。
func (m *model) Meta() *orm.ModelMeta { return m.meta }

// Capabilities 处理Capabilities。
func (m *model) Capabilities() orm.Capabilities { return m.capabilities }

// Dialect 暴露当前 GORM model 的数据库方言，供通用 repo 安全 quote 动态列名。
func (m *model) Dialect() dialect.IDialect {
	if m == nil || m.db == nil || m.db.Dialector == nil {
		return dialect.New("")
	}
	return dialect.New(m.db.Name())
}

// First 处理First。
func (m *model) First(ctx context.Context, dest any, opts ...orm.QueryOption) error {
	db, err := m.apply(ctx, orm.CollectQueryOptions(opts...))
	if err != nil {
		return err
	}
	if err := db.First(dest).Error; err != nil {
		return convertError(err)
	}
	return nil
}

// Find 查找数据。
func (m *model) Find(ctx context.Context, dest any, opts ...orm.QueryOption) error {
	db, err := m.apply(ctx, orm.CollectQueryOptions(opts...))
	if err != nil {
		return err
	}
	if err := db.Find(dest).Error; err != nil {
		return convertError(err)
	}
	return nil
}

// Count 统计数据。
//
// 契约实现规范：
// - 忽略 Limit 和 Offset，返回不受分页影响的匹配记录总数或组数；
// - 忽略 Select 投影与 OrderBy，避免因特定列为 NULL 或排序开销影响统计结果；
// - 当指定 GroupBy 时，以子查询形式统计满足分组条件的组数（Number of groups）。
func (m *model) Count(ctx context.Context, opts ...orm.QueryOption) (int64, error) {
	qo := orm.CollectQueryOptions(opts...)
	if len(qo.GroupBy) > 0 {
		subQuery, err := m.apply(ctx, orm.QueryOptions{
			Where:   qo.Where,
			Joins:   qo.Joins,
			GroupBy: qo.GroupBy,
		})
		if err != nil {
			return 0, err
		}
		var count int64
		if err := m.db.WithContext(ensureContext(ctx)).Table("(?) AS _gochen_count", subQuery.Select("1")).Count(&count).Error; err != nil {
			return 0, convertError(err)
		}
		return count, nil
	}

	db, err := m.apply(ctx, orm.QueryOptions{
		Where: qo.Where,
		Joins: qo.Joins,
	})
	if err != nil {
		return 0, err
	}
	var count int64
	if err := db.Select("*").Count(&count).Error; err != nil {
		return 0, convertError(err)
	}
	return count, nil
}

// Create 创建记录。
func (m *model) Create(ctx context.Context, entities ...any) error {
	if len(entities) == 0 {
		return nil
	}
	db := m.db.WithContext(ensureContext(ctx))
	if m.meta != nil && m.meta.Table != "" {
		db = db.Table(m.meta.Table)
	}
	create := func(tx *gorm.DB) error {
		for _, entity := range entities {
			if entity == nil {
				return errors.NewCode(errors.InvalidInput, "entity cannot be nil")
			}
			if err := tx.Create(entity).Error; err != nil {
				return convertError(err)
			}
		}
		return nil
	}
	if len(entities) == 1 {
		return create(db)
	}
	return db.Transaction(create)
}

// Save 保存数据。
func (m *model) Save(ctx context.Context, entity any, opts ...orm.QueryOption) error {
	db, err := m.apply(ctx, orm.CollectQueryOptions(opts...))
	if err != nil {
		return err
	}
	if err := db.Select("*").Omit("id", clause.Associations).Updates(entity).Error; err != nil {
		return convertError(err)
	}
	return nil
}

// SaveWithResult 保存带结果。
func (m *model) SaveWithResult(ctx context.Context, entity any, opts ...orm.QueryOption) (sql.Result, error) {
	db, err := m.apply(ctx, orm.CollectQueryOptions(opts...))
	if err != nil {
		return nil, err
	}
	res := db.Select("*").Omit("id", clause.Associations).Updates(entity)
	if res.Error != nil {
		return nil, convertError(res.Error)
	}
	return &result{rowsAffected: res.RowsAffected}, nil
}

// UpdateValues 更新值集合。
func (m *model) UpdateValues(ctx context.Context, values map[string]any, opts ...orm.QueryOption) error {
	db, err := m.apply(ctx, orm.CollectQueryOptions(opts...))
	if err != nil {
		return err
	}
	if err := db.Updates(values).Error; err != nil {
		return convertError(err)
	}
	return nil
}

// UpdateValuesWithResult 更新值集合并带结果。
func (m *model) UpdateValuesWithResult(ctx context.Context, values map[string]any, opts ...orm.QueryOption) (sql.Result, error) {
	db, err := m.apply(ctx, orm.CollectQueryOptions(opts...))
	if err != nil {
		return nil, err
	}
	res := db.Updates(values)
	if res.Error != nil {
		return nil, convertError(res.Error)
	}
	return &result{rowsAffected: res.RowsAffected}, nil
}

// Delete 删除数据。
func (m *model) Delete(ctx context.Context, opts ...orm.QueryOption) error {
	db, err := m.apply(ctx, orm.CollectQueryOptions(opts...))
	if err != nil {
		return err
	}
	if m.meta == nil {
		return errors.NewCode(errors.InvalidInput, "orm model meta cannot be nil")
	}
	target := m.meta.NewModel()
	if target == nil {
		return errors.NewCode(errors.InvalidInput, "orm model meta has no model prototype/factory")
	}
	if err := db.Delete(target).Error; err != nil {
		return convertError(err)
	}
	return nil
}

// DeleteWithResult 删除数据。
func (m *model) DeleteWithResult(ctx context.Context, opts ...orm.QueryOption) (sql.Result, error) {
	db, err := m.apply(ctx, orm.CollectQueryOptions(opts...))
	if err != nil {
		return nil, err
	}
	if m.meta == nil {
		return nil, errors.NewCode(errors.InvalidInput, "orm model meta cannot be nil")
	}
	target := m.meta.NewModel()
	if target == nil {
		return nil, errors.NewCode(errors.InvalidInput, "orm model meta has no model prototype/factory")
	}
	res := db.Delete(target)
	if res.Error != nil {
		return nil, convertError(res.Error)
	}
	return &result{rowsAffected: res.RowsAffected}, nil
}

// Association 处理Association。
func (m *model) Association(owner any, name string) orm.IAssociation {
	return &association{db: m.db, owner: owner, name: name}
}

// apply 应用配置。
func (m *model) apply(ctx context.Context, qo orm.QueryOptions) (*gorm.DB, error) {
	db := m.db.WithContext(ensureContext(ctx))
	if m.meta != nil {
		// 优先使用显式 Table，避免将 model 实例引入 GORM 的隐式主键 WHERE 推导。
		if m.meta.Table != "" {
			db = db.Table(m.meta.Table)
		} else if model := m.meta.NewModel(); model != nil {
			// 仅在未提供 Table 时，才回退到 Model 推导表名/Schema。
			db = db.Model(model)
		}
	}

	for _, cond := range qo.Where {
		db = db.Where(cond.Expr, cond.Args...)
	}
	d := m.Dialect()
	for _, join := range qo.Joins {
		expr, err := buildJoinExpr(d, join)
		if err != nil {
			return nil, err
		}
		db = db.Joins(expr)
	}
	for _, preload := range qo.Preload {
		db = db.Preload(preload)
	}
	if len(qo.OrderBy) > 0 {
		orderBy := clause.OrderBy{Columns: make([]clause.OrderByColumn, 0, len(qo.OrderBy))}
		for _, order := range qo.OrderBy {
			if order.Column == "" {
				continue
			}
			if !safeident.IsSafeIdentifier(order.Column) {
				return nil, errors.NewCode(errors.InvalidInput, "unsafe order by column").WithContext("column", order.Column)
			}
			orderBy.Columns = append(orderBy.Columns, clause.OrderByColumn{
				Column: clause.Column{Name: order.Column},
				Desc:   order.Desc,
			})
		}
		if len(orderBy.Columns) > 0 {
			db = db.Clauses(orderBy)
		}
	}
	if len(qo.GroupBy) > 0 {
		groupBy := clause.GroupBy{Columns: make([]clause.Column, 0, len(qo.GroupBy))}
		for _, group := range qo.GroupBy {
			if group == "" {
				continue
			}
			if !safeident.IsSafeIdentifier(group) {
				return nil, errors.NewCode(errors.InvalidInput, "unsafe group by column").WithContext("column", group)
			}
			groupBy.Columns = append(groupBy.Columns, clause.Column{Name: group})
		}
		if len(groupBy.Columns) > 0 {
			db = db.Clauses(groupBy)
		}
	}
	if len(qo.Select) > 0 || len(qo.SelectRaw) > 0 {
		selects := make([]string, 0, len(qo.Select)+len(qo.SelectRaw))
		columns := make([]clause.Column, 0, len(qo.Select)+len(qo.SelectRaw))
		for _, selectColumn := range qo.Select {
			selectColumn = strings.TrimSpace(selectColumn)
			if selectColumn == "" {
				continue
			}
			if selectColumn != "*" && !safeident.IsSafeIdentifier(selectColumn) {
				return nil, errors.NewCode(errors.InvalidInput, "unsafe select column").WithContext("column", selectColumn)
			}
			selects = append(selects, selectColumn)
			columns = append(columns, clause.Column{Name: selectColumn, Raw: selectColumn == "*"})
		}
		for _, selectExpr := range qo.SelectRaw {
			selectExpr = strings.TrimSpace(selectExpr)
			if selectExpr == "" {
				continue
			}
			selects = append(selects, selectExpr)
			columns = append(columns, clause.Column{Name: selectExpr, Raw: true})
		}
		if len(selects) > 0 {
			// 更新操作依赖原始字段列表；查询投影通过 SELECT 子句处理标识符引用。
			db = db.Select(selects).Clauses(clause.Select{Distinct: db.Statement.Distinct, Columns: columns})
		}
	}
	if qo.Limit > 0 {
		db = db.Limit(qo.Limit)
	}
	if qo.Offset > 0 {
		db = db.Offset(qo.Offset)
	}
	if qo.ForUpdate {
		db = db.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	return db, nil
}

// buildJoinExpr 构造JoinExpr。
func buildJoinExpr(d dialect.IDialect, j orm.Join) (string, error) {
	var joinType string
	switch j.Type {
	case orm.JoinInner:
		joinType = "INNER"
	case orm.JoinLeft:
		joinType = "LEFT"
	case orm.JoinRight:
		joinType = "RIGHT"
	default:
		return "", errors.NewCode(errors.InvalidInput, "invalid join type").WithContext("type", string(j.Type))
	}

	table := strings.TrimSpace(j.Table)
	if table == "" {
		return "", errors.NewCode(errors.InvalidInput, "join table cannot be empty")
	}
	if !safeident.IsSafeIdentifier(table) {
		return "", errors.NewCode(errors.InvalidInput, "unsafe join table").WithContext("table", table)
	}
	alias := strings.TrimSpace(j.Alias)
	if alias != "" {
		if strings.Contains(alias, ".") {
			return "", errors.NewCode(errors.InvalidInput, "join alias cannot contain dot").WithContext("alias", alias)
		}
		if !safeident.IsSafeIdentifier(alias) {
			return "", errors.NewCode(errors.InvalidInput, "unsafe join alias").WithContext("alias", alias)
		}
	}

	if len(j.On) == 0 {
		return "", errors.NewCode(errors.InvalidInput, "join on cannot be empty").WithContext("table", table)
	}
	conditions := make([]string, 0, len(j.On))
	for i := range j.On {
		left := strings.TrimSpace(j.On[i].Left)
		right := strings.TrimSpace(j.On[i].Right)
		if left == "" || right == "" {
			return "", errors.NewCode(errors.InvalidInput, "join on cannot be empty").WithContext("table", table)
		}
		if !safeident.IsSafeIdentifier(left) {
			return "", errors.NewCode(errors.InvalidInput, "unsafe join on left").WithContext("left", left)
		}
		if !safeident.IsSafeIdentifier(right) {
			return "", errors.NewCode(errors.InvalidInput, "unsafe join on right").WithContext("right", right)
		}
		conditions = append(conditions, d.QuoteIdentifier(left)+" = "+d.QuoteIdentifier(right))
	}

	target := d.QuoteIdentifier(table)
	if alias != "" {
		target = fmt.Sprintf("%s AS %s", d.QuoteIdentifier(table), d.QuoteIdentifier(alias))
	}

	return fmt.Sprintf("%s JOIN %s ON %s", joinType, target, strings.Join(conditions, " AND ")), nil
}

// ensureContext 确保上下文。
func ensureContext(ctx context.Context) context.Context {
	if ctx == nil {
		return contextx.Background()
	}
	return ctx
}

// convertError 转换错误。
func convertError(err error) error {
	if stdErrors.Is(err, gorm.ErrRecordNotFound) {
		return errors.NewCode(errors.NotFound, "record not found")
	}
	if stdErrors.Is(err, gorm.ErrDuplicatedKey) || db.IsUniqueViolation(err) {
		return errors.Wrap(err, errors.Conflict, "record already exists")
	}
	return err
}

type result struct {
	rowsAffected int64
}

// LastInsertId 返回最后插入的主键。
func (r *result) LastInsertId() (int64, error) {
	return 0, errors.NewCode(errors.Unsupported, "LastInsertId not supported")
}

// RowsAffected 返回受影响的行数。
func (r *result) RowsAffected() (int64, error) { return r.rowsAffected, nil }

var _ orm.IModel = (*model)(nil)
var _ orm.IModelWithResult = (*model)(nil)
