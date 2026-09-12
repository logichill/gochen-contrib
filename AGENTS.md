# AGENTS 准则（Gochen Contrib）

本仓库为 Gochen Contrib（module `gochen-contrib`），是 Gochen 官方的第三方外设与开源生态驱动适配器集（Gin, GORM, Redis, OTel, Prometheus, MySQL/Postgres 驱动注册与 Migration CLI）。

- 本仓库 `gochen-contrib`（Contrib）：生态适配器实现；局部约定见本文件与 `.agents/CONTEXT.md`
- `../gochen`（Core，module `gochen`）：领域、应用、认证、事件、消息、策略、流程与 HTTP 抽象
- `../gochen-runtime`（Runtime，module `gochen-runtime`）：Core 契约的标准 Go 运行时——SQL 实现、net/http server、host 容器、di、api/rest
- 开发规范 `SPEC.md`、文档门户 `docs/` 与架构设计记录统一位于 `gochen` 仓库，**本仓库同样受其约束**

## 通用准则

- 这是 golang 项目，需要在遵守 `gochen` 仓库 `SPEC.md` 的基础上遵守 golang 编码规范
- 执行任何重构都不要保留兼容层代码，保持项目的干净清爽
- 执行代码创建/修改后要执行 go fmt
- 默认验证：本仓库内执行 `go build ./... && go vet ./... && go test -count=1 ./...`
- 禁止把验证退化为 workspace 级一把梭——那会掩盖单仓库依赖不变量

## 命名铁律

- **repo 名 = module path = 目录名**，三者必须一致（当前：`gochen-contrib`）。
- module path **禁止使用 Go 标准库顶级包名**。
- 生态内 module 一律 `gochen` 或 `gochen-*` 前缀，平级 dotless。

## 模块边界与依赖

- 依赖硬规则：只允许 `gochen-contrib → gochen` 与 `gochen-contrib → gochen-runtime` 单向依赖，禁止任何反向引用。Core 与 Runtime 任何文件（含测试与示例）禁止 import `gochen-contrib`。
- **落在 Contrib 的职责**：第三方开源外设驱动与适配器（Gin HTTP server、GORM IDatabase/IOrm、Redis 分布式锁、OTel 追踪、Prometheus 指标、MySQL/PostgreSQL 驱动注册与 Drop/CLI migration）。
- **不得落在 Contrib 的职责**：通用运行时宿主容器（Host/DI/Bootstrap）、标准库 net/http server、基础 migration runner 与 SQL 分词 review——这些属于 `gochen-runtime`；领域契约与端口抽象属于 `gochen`。
- 开发态由 workspace 级 `go.work` 组合；发布态必须改为真实依赖版本，不依赖本地 replace。

## 顶级目录与包

`data/db/driver data/db/gorm data/db/gorm/factory data/orm/gorm http/gin lock/redis migration observe/otel observe/prometheus`

`data/db/gorm` 只适配 GORM 连接或接受显式注入的 Dialector；`data/db/gorm/factory` 独立承载按配置选择 MySQL/PostgreSQL/SQLite 驱动的便捷入口，避免包装已有连接时强制引入全部驱动。

新增目录必须先说明架构理由。

## 下游项目

- ../alife
- ../ems
- ../erp
- ../gochen-iam
- ../gochen-llm
- ../gochen-workflow
