package driver

import (
	"net/url"
	"strings"
)

// NormalizeSQLiteDSN 规范化 SQLite DSN。
//
// 若是纯文件路径（非 :memory:、非 file: 前缀、不含 query 参数），将其转换为 file: URI，
// 并追加 journal_mode=WAL、busy_timeout=5000、synchronous=NORMAL。
// 文件库默认使用私有缓存，避免共享缓存的表锁阻塞 WAL 并发读写；内存库默认共享缓存。
// 可写事务默认 BEGIN IMMEDIATE，避免先读后写时升级锁失败；只读事务仍保持并发读取。
//
// 只读 DSN（mode=ro/readonly 或 immutable）不追加 journal_mode(WAL)：切换 WAL 需要写权限，
// 只读连接上执行会以 SQLITE_READONLY 失败；调用方已显式给出 journal_mode pragma 时同样不追加。
func NormalizeSQLiteDSN(dsn string) string {
	s := strings.TrimSpace(dsn)
	if s == "" {
		return ""
	}

	// 若是纯路径（非 :memory: / 非 file: / 非含 query 参数），转成 file: URI。
	if !strings.HasPrefix(s, ":") && !strings.Contains(s, "?") && !strings.HasPrefix(s, "file:") {
		s = "file:" + s
	}
	base, rawQuery, _ := strings.Cut(s, "?")
	query, _ := url.ParseQuery(rawQuery)

	// 追加查询参数分隔符
	if !strings.Contains(s, "?") {
		s += "?"
	} else if !strings.HasSuffix(s, "&") && !strings.HasSuffix(s, "?") {
		s += "&"
	}

	if !query.Has("cache") && (base == ":memory:" || base == "file::memory:" || query.Get("mode") == "memory") {
		s += "cache=shared&"
	}
	if !query.Has("_txlock") {
		s += "_txlock=immediate&"
	}
	if !isReadOnlySQLiteQuery(query) && !hasJournalModePragma(query) {
		s += "_pragma=journal_mode(WAL)&"
	}
	s += "_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	return s
}

// isReadOnlySQLiteQuery 判断查询参数是否表达只读语义（SQLite URI 参数约定）。
func isReadOnlySQLiteQuery(query url.Values) bool {
	mode := strings.ToLower(strings.TrimSpace(query.Get("mode")))
	if mode == "ro" || mode == "readonly" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(query.Get("immutable"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// hasJournalModePragma 判断调用方是否已显式指定 journal_mode pragma。
func hasJournalModePragma(query url.Values) bool {
	for _, pragma := range query["_pragma"] {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(pragma)), "journal_mode") {
			return true
		}
	}
	return false
}
