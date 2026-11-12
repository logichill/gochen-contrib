# next 候选对照验证

本分支只增加 typed Module 与 GORM / Gin / net/http 的联合回归，并把现有数据库 CI 固定到同批上游提交；没有重写 Redis、OTel、Prometheus、驱动工厂或生产适配逻辑。

固定依赖：

- Core：[c17d2d2df1ac18e328e4060b18529328c058ff62](https://github.com/logichill/gochen/commit/c17d2d2df1ac18e328e4060b18529328c058ff62)
- Runtime：[fbd30eabded69bfd352a859a985b4159dc7937d2](https://github.com/logichill/gochen-runtime/commit/fbd30eabded69bfd352a859a985b4159dc7937d2)

保留原有 dotless module path 和同级开发态 replace，不把 `v0.0.0` 称作已发布版本。三个仓库平级检出，Core/Runtime 分别 checkout 上面 SHA，Contrib checkout 本候选的提交后，在本仓库独立运行：

```sh
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test -count=1 ./...
GOWORK=off go test -race -count=1 ./...
```

本轮云端以上检查全部通过。`http/gin/typed_module_test.go` 是外部消费包，使用公开事务/审计端口，分别启动 Gin 与 net/http 的真实 HTTP 测试服务器，连接同样的 GORM 内存 SQLite 仓储，验证 CRUD、401/403、版本冲突、实际入口清单、普通 Go 调用的授权和外部数据库关闭责任。

本地 MySQL/PostgreSQL 测试明确跳过：没有提供 `GOCHEN_MYSQL_DSN` / `GOCHEN_POSTGRES_DSN`。SQLite 不能证明这两种真实后端的保存点/数据库语义；现有 `.github/workflows/database-contracts.yml` 会针对固定上游 commit 使用独立 MySQL/PostgreSQL 测试服务。远端 CI 是否成功以本提交的实际运行状态为准，不把“已配置”写成“已通过”。golangci-lint 未执行，云环境未安装该工具。

没有连接生产数据库、执行迁移、发布版本、修改业务项目或合并 main。源码 breaking change 和下游实际接入点见 [Core 迁移说明](https://github.com/logichill/gochen/blob/c17d2d2df1ac18e328e4060b18529328c058ff62/docs/guides/core-next-migration.md)。
