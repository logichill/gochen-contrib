package driver

import "strings"

// NormalizeSQLiteDSN 规范化 SQLite DSN。
//
// 若是纯文件路径（非 :memory:、非 file: 前缀、不含 query 参数），将其转换为 file: URI，
// 并追加标准推荐 pragma 参数（cache=shared, journal_mode=WAL, busy_timeout=5000, synchronous=NORMAL）。
func NormalizeSQLiteDSN(dsn string) string {
	s := strings.TrimSpace(dsn)
	if s == "" {
		return ""
	}

	// 若是纯路径（非 :memory: / 非 file: / 非含 query 参数），转成 file: URI。
	if !strings.HasPrefix(s, ":") && !strings.Contains(s, "?") && !strings.HasPrefix(s, "file:") {
		s = "file:" + s
	}

	// 追加查询参数分隔符
	if !strings.Contains(s, "?") {
		s += "?"
	} else if !strings.HasSuffix(s, "&") && !strings.HasSuffix(s, "?") {
		s += "&"
	}

	// 追加推荐 pragmas
	s += "cache=shared&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	return s
}
