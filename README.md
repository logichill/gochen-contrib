# gochen-contrib

`gochen-contrib` 是 Gochen 框架官方的**第三方开源生态驱动与外设适配器集合（Ecosystem Adapters）**。

当业务需要选用非 Go 标准库的重量级开源组件（如 Gin、GORM、Redis、OpenTelemetry、Prometheus 等）时，由本项目提供开箱即用且严格遵循 `gochen` 规范的适配实现。

---

## 架构定位

```
┌─────────────────────────────────────────────────────────────┐
│ 1. gochen (Core 契约)                                       │
│    - 领域建模、用例编排、事件溯源、总线与端口抽象 (零外部依赖)       │
└──────────────────────────────┬──────────────────────────────┘
                               │
         ┌─────────────────────┴─────────────────────┐
         ▼                                           ▼
┌──────────────────────────────┐            ┌──────────────────────────────┐
│ 2. gochen-runtime (标准运行时)│            │ 3. gochen-contrib (生态外设)  │
│ - 官方默认、一等公民生产运行时  │            │ - 满足非标准库技术栈选型需求 │
│ - 纯 Go 标准库打造 (Zero Bloat)│            │ - 按需引入第三方外设驱动     │
│ - host, nethttp, sql, migrate│            │ - gin, gorm, redis, otel     │
└──────────────────────────────┘            └──────────────────────────────┘
```

---

## 适配器目录与能力清单

| 能力域 | 包路径 | 目标契约 | 说明 |
| :--- | :--- | :--- | :--- |
| **HTTP** | `gochen-contrib/http/gin` | `gochen/httpx.IServer` | 基于 Gin 引擎实现标准 HTTP 服务容器，支持路由清单提取与无缝注入 `host.Run`。 |
| **Database Drivers** | `gochen-contrib/data/db/driver` | `database/sql/driver` | 集中注册 MySQL (`go-sql-driver/mysql`)、PostgreSQL (`lib/pq`)、SQLite 驱动。 |
| **Database** | `gochen-contrib/data/db/gorm` | `gochen/db.IDatabase` | 基于 GORM 提供最小 `IDatabase` 适配器（Query/Exec/Tx/Raw）。 |
| **ORM** | `gochen-contrib/data/orm/gorm` | `gochen/db/orm.IOrm` | 基于 GORM 完整实现 `IOrm`、`IModel`，支持与 `ormrepo.NewRepo` 协同。 |
| **Distributed Lock** | `gochen-contrib/lock/redis` | `gochen/process/lock.ILockProvider` | 基于 Redis 的安全分布式锁驱动（基于随机 Token 与原子 Lua 释放）。 |
| **Migration** | `gochen-contrib/migration` | CLI & Drop & Runner | 封装通用交互确认 CLI、外键级联 Drop 以及多数据库驱动绑定。 |
| **Tracing** | `gochen-contrib/observe/otel` | `gochen/observe.ITracer` | OpenTelemetry 分布式链路追踪导出与上下文传播适配器。 |
| **Metrics** | `gochen-contrib/observe/prometheus` | `gochen/observe.IMetrics` | Prometheus Client 物理指标采集与导出适配器。 |

---

## 快速使用示例

### 1. 将 Gin 服务注入 Host
```go
import (
    gogin "gochen-contrib/http/gin"
    gochenhttp "gochen-runtime/http"
    "gochen-runtime/host"
    hostconfig "gochen-runtime/host/config"
)

server, err := gogin.New(&gochenhttp.WebConfig{
    Host: "0.0.0.0",
    Port: 8080,
    Mode: "release",
})
if err != nil {
    log.Fatal(err)
}

host.Run(ctx,
    hostconfig.WithHTTPServer(server),
    // ...
)
```

### 2. 将 GORM 注入通用 Repository
```go
import (
    gormorm "gochen-contrib/data/orm/gorm"
    ormrepo "gochen-runtime/db/orm/repo"
    "gorm.io/gorm"
)

gormDB, err := gorm.Open(...)
ormAdapter, err := gormorm.New(gormDB)

repo, err := ormrepo.NewRepo[*Order, int64](ormAdapter, "orders")
```

---

## 本地开发与测试

```bash
# 运行全量单元测试
go test ./... -count=1
```
