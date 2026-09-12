package gormorm

import (
	"testing"

	"gochen/db/dialect"
	"gochen/db/orm"
)

func TestBuildJoinExprQuotesEveryCondition(t *testing.T) {
	join := orm.InnerJoin("group", "select", orm.On("items.id", "select.id"), orm.On("items.order", "select.order"))
	for _, tc := range []struct {
		name string
		want string
	}{
		{"sqlite", `INNER JOIN "group" AS "select" ON "items"."id" = "select"."id" AND "items"."order" = "select"."order"`},
		{"postgres", `INNER JOIN "group" AS "select" ON "items"."id" = "select"."id" AND "items"."order" = "select"."order"`},
		{"mysql", "INNER JOIN `group` AS `select` ON `items`.`id` = `select`.`id` AND `items`.`order` = `select`.`order`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildJoinExpr(dialect.New(tc.name), join)
			if err != nil || got != tc.want {
				t.Fatalf("JOIN = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
