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
	s += "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	return s
}
