package gormorm

import (
	"context"
	"testing"

	gormdb "gochen-contrib/data/db/gorm"
	"gochen-runtime/db/orm/repo"
	"gochen/db/orm"
	"gochen/domain/spec"

	"gorm.io/gorm"
)

type bindBudgetEntity struct {
	ID   int64 `entity:"id"`
	Name string
}

func (e *bindBudgetEntity) GetID() int64       { return e.ID }
func (e *bindBudgetEntity) GetVersion() uint64 { return 0 }

func TestRepositoryBindBudgetWithEmptyGORMPredicate(t *testing.T) {
	for _, tc := range []struct {
		name, expr string
		args       []any
		wantRows   int
	}{
		{"empty_after_scalar", "name = ? AND id IN ?", []any{"x", []int64{}}, 0},
		{"empty_before_scalar", "id IN ? AND name = ?", []any{[]int64{}, "x"}, 0},
		{"parenthesized_empty_after", "name = ? AND id IN (?)", []any{"x", []int64{}}, 0},
		{"parenthesized_empty_before", "id IN (?) AND name = ?", []any{[]int64{}, "x"}, 0},
		{"nonempty", "name = ? AND id IN ?", []any{"x", []int64{1, 2}}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := setupDB(t)
			if err := raw.Create(&[]user{{ID: 1, Name: "x"}, {ID: 2, Name: "x"}}).Error; err != nil {
				t.Fatal(err)
			}
			wrapped, err := gormdb.New(raw, gormdb.WithMaxBindParameters(100))
			if err != nil {
				t.Fatal(err)
			}
			engine, err := NewFromDatabase(wrapped)
			if err != nil {
				t.Fatal(err)
			}
			r, err := repo.NewRepo[*bindBudgetEntity, int64](engine, "users", repo.WithReadPredicate[*bindBudgetEntity, int64](func(context.Context) (orm.Condition, error) { return orm.Condition{Expr: tc.expr, Args: tc.args}, nil }), repo.WithoutVersioning[*bindBudgetEntity, int64]())
			if err != nil {
				t.Fatal(err)
			}
			queries := 0
			if err := raw.Callback().Query().After("gorm:query").Register("assert_bind_budget", func(q *gorm.DB) {
				queries++
				if n := len(q.Statement.Vars); n > 100 {
					t.Errorf("actual GORM query binds=%d exceeds limit100", n)
				}
			}); err != nil {
				t.Fatal(err)
			}
			ids := make([]int64, 100)
			for i := range ids {
				ids[i] = int64(i + 1)
			}
			items, err := r.ListByIds(t.Context(), ids, spec.All())
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != tc.wantRows {
				t.Fatalf("got%d rows want%d", len(items), tc.wantRows)
			}
			if queries < 2 {
				t.Errorf("expected batch split, got%d queries", queries)
			}
		})
	}
}
