# PostgreSQL SSL 配置示例

## 环境变量配置

```bash
# 数据库基本配置
DB_DRIVER=postgres
DB_HOST=localhost
DB_PORT=5432
DB_DATABASE=mydb
DB_USERNAME=myuser
DB_PASSWORD=mypassword

# SSL 配置
DB_SSL_MODE=require              # disable, allow, prefer, require, verify-ca, verify-full
DB_SSL_ROOT_CERT=/path/to/ca-cert.pem
DB_SSL_CERT=/path/to/client-cert.pem
DB_SSL_KEY=/path/to/client-key.pem
```

## 代码配置示例

```go
import (
    "gochen/db"
    gormdb "gochen-contrib/data/db/gorm"
)

// 方式 1: 使用 Options 字段
cfg := db.DBConfig{
    Driver:   "postgres",
    Host:     "localhost",
    Port:     5432,
    Database: "mydb",
    Username: "myuser",
    Password: "mypassword",
    Options: map[string]any{
        "sslmode":     "require",                    // SSL 模式
        "sslrootcert": "/path/to/ca-cert.pem",      // CA 证书
        "sslcert":     "/path/to/client-cert.pem",  // 客户端证书
        "sslkey":      "/path/to/client-key.pem",   // 客户端密钥
    },
}

database, err := gormdb.NewFromConfig(ctx, cfg)
if err != nil {
    log.Fatal(err)
}
```

## SSL 模式说明

| 模式 | 说明 | 安全性 |
|------|------|--------|
| `disable` | 不使用 SSL | ❌ 低 |
| `allow` | 优先不加密，服务器要求时才加密 | ⚠️ 低 |
| `prefer` | 优先加密，服务器不支持时降级 | ⚠️ 中 |
| `require` | 必须加密，但不验证服务器证书 | ✅ 中 |
| `verify-ca` | 必须加密，验证服务器证书 | ✅ 高 |
| `verify-full` | 必须加密，验证证书和主机名 | ✅✅ 最高 |

## 生产环境推荐配置

```bash
# 最低要求
DB_SSL_MODE=require

# 推荐配置（如果有 CA 证书）
DB_SSL_MODE=verify-ca
DB_SSL_ROOT_CERT=/etc/ssl/certs/postgresql-ca.pem

# 最安全配置（双向 TLS）
DB_SSL_MODE=verify-full
DB_SSL_ROOT_CERT=/etc/ssl/certs/postgresql-ca.pem
DB_SSL_CERT=/etc/ssl/certs/postgresql-client.pem
DB_SSL_KEY=/etc/ssl/private/postgresql-client-key.pem
```

## 开发环境配置

```bash
# 本地开发可以禁用 SSL
DB_SSL_MODE=disable
```

## 向后兼容性

- 如果不设置 `Options["sslmode"]`，默认使用 `require` 模式
- 旧代码不受影响，但建议显式配置 SSL 模式
- 如需禁用 SSL（仅开发环境），显式设置 `sslmode=disable`

## 迁移指南

### 从旧版本升级

**之前**（硬编码 sslmode=disable）:
```go
cfg := db.DBConfig{
    Driver:   "postgres",
    Host:     "localhost",
    // ...
}
// 自动使用 sslmode=disable
```

**现在**（默认 sslmode=require）:
```go
cfg := db.DBConfig{
    Driver:   "postgres",
    Host:     "localhost",
    // ...
}
// 默认使用 sslmode=require

// 如需禁用（仅开发环境）:
cfg.Options = map[string]any{
    "sslmode": "disable",
}
```

## 故障排查

### 错误: "SSL is not enabled on the server"

**原因**: 服务器不支持 SSL，但客户端要求 SSL

**解决方案**:
```bash
# 开发环境：禁用 SSL
DB_SSL_MODE=disable

# 生产环境：启用服务器 SSL 支持
# 编辑 postgresql.conf:
ssl = on
ssl_cert_file = '/path/to/server.crt'
ssl_key_file = '/path/to/server.key'
```

### 错误: "certificate verify failed"

**原因**: 服务器证书验证失败

**解决方案**:
```bash
# 方案 1: 提供正确的 CA 证书
DB_SSL_MODE=verify-ca
DB_SSL_ROOT_CERT=/path/to/correct-ca.pem

# 方案 2: 降级到 require（不验证证书）
DB_SSL_MODE=require
```

## 安全建议

1. ✅ **生产环境必须启用 SSL**
   - 最低使用 `require` 模式
   - 推荐使用 `verify-ca` 或 `verify-full`

2. ✅ **保护证书文件**
   ```bash
   chmod 600 /etc/ssl/private/postgresql-client-key.pem
   chown app:app /etc/ssl/private/postgresql-client-key.pem
   ```

3. ✅ **定期更新证书**
   - 设置证书过期提醒
   - 使用自动化工具（如 cert-manager）

4. ⚠️ **开发环境可以禁用 SSL**
   - 但要确保不会误用到生产环境
   - 使用环境变量区分配置

---

**更新日期**: 2026-01-31
**版本**: gochen-contrib v1.0.0
