package gormorm

import (
	"context"

	"gochen/db/orm"

	"gorm.io/gorm"
)

type association struct {
	db    *gorm.DB
	owner any
	name  string
}

// Name 返回名称。
func (a *association) Name() string { return a.name }

// Owner 处理Owner。
func (a *association) Owner() any { return a.owner }

// Append 处理Append。
func (a *association) Append(ctx context.Context, targets ...any) error {
	if err := a.db.WithContext(ensureContext(ctx)).Model(a.owner).Association(a.name).Append(targets...); err != nil {
		return convertError(err)
	}
	return nil
}

// Replace 替换配置。
func (a *association) Replace(ctx context.Context, targets ...any) error {
	if err := a.db.WithContext(ensureContext(ctx)).Model(a.owner).Association(a.name).Replace(targets...); err != nil {
		return convertError(err)
	}
	return nil
}

// Delete 删除数据。
func (a *association) Delete(ctx context.Context, targets ...any) error {
	if err := a.db.WithContext(ensureContext(ctx)).Model(a.owner).Association(a.name).Delete(targets...); err != nil {
		return convertError(err)
	}
	return nil
}

// Clear 处理Clear。
func (a *association) Clear(ctx context.Context) error {
	if err := a.db.WithContext(ensureContext(ctx)).Model(a.owner).Association(a.name).Clear(); err != nil {
		return convertError(err)
	}
	return nil
}

var _ orm.IAssociation = (*association)(nil)
