// Package driver 注册 Gochen 生态常用的开源 SQL 驱动（MySQL, PostgreSQL, SQLite）。
//
// 业务应用或适配器可以通过匿名导入该包完成数据库驱动的自动注册：
//
//	import _ "gochen-contrib/data/db/driver"
package driver

import (
	_ "github.com/glebarez/go-sqlite"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
)
