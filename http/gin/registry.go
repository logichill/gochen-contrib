package gogin

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"gochen/httpx"
)

type routeKey struct {
	method string
	path   string
}

type routeRegistry struct {
	mu     sync.Mutex
	counts map[routeKey]int
}

func newRouteRegistry() *routeRegistry {
	return &routeRegistry{counts: make(map[routeKey]int, 64)}
}

func (r *routeRegistry) tryReserve(method, path string) bool {
	method = strings.TrimSpace(strings.ToUpper(method))
	path = normalizeRoutePath(path)
	if method == "" || path == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := routeKey{method: method, path: path}
	if r.counts[k] > 0 {
		r.counts[k]++
		return true
	}
	r.counts[k] = 1
	return false
}

func (r *routeRegistry) rollback(method, path string) {
	method = strings.TrimSpace(strings.ToUpper(method))
	path = normalizeRoutePath(path)
	if method == "" || path == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := routeKey{method: method, path: path}
	if r.counts[k] <= 1 {
		delete(r.counts, k)
	} else {
		r.counts[k]--
	}
}

func (r *routeRegistry) markConflict(method, path string) {
	method = strings.TrimSpace(strings.ToUpper(method))
	path = normalizeRoutePath(path)
	if method == "" || path == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := routeKey{method: method, path: path}
	if r.counts[k] < 1 {
		r.counts[k] = 2
	} else {
		r.counts[k]++
	}
}

func (r *routeRegistry) conflicts() []httpx.RouteConflict {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]httpx.RouteConflict, 0)
	for k, c := range r.counts {
		if c > 1 {
			out = append(out, httpx.RouteConflict{Method: k.method, Path: k.path, Count: c})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return out[i].Method < out[j].Method
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// duplicateRoutePath 从 Gin 内部 panic 消息中提取冲突路径。
//
// 注意：该文案契约来自 gin/tree.go（"handlers are already registered for path '...'"）。
// 升级 Gin 版本时需复核此匹配逻辑；若 Gin 调整报错文案，行为将安全退化为启动期直接 panic，
// 且 routes_test.go 中的单元测试会立即捕获此变化。
func duplicateRoutePath(r any) (string, bool) {
	if r == nil {
		return "", false
	}
	s, ok := r.(string)
	if !ok {
		s = fmt.Sprint(r)
	}
	const prefix = "handlers are already registered for path '"
	const suffix = "'"
	if strings.HasPrefix(s, prefix) && strings.HasSuffix(s, suffix) {
		path := strings.TrimSuffix(strings.TrimPrefix(s, prefix), suffix)
		return path, true
	}
	return "", false
}

// RouteConflicts 返回当前注册路由中存在冲突（method+path 重复）的列表。
//
// 语义说明：
// - 为支持 host 的 FailFastOnRouteConflicts 结构化治理，Server 在发生重复注册时会捕获 Gin 的重复路由 panic 并计入冲突表（首个 handler 生效）；
// - 非 host 托管的单机使用场景下，重复注册不再直接中断崩溃，调用方如需校验冲突应显式检查 RouteConflicts()；
// - 通配符冲突（如 /users/:id 与 /users/:name）及非法 HTTP 方法等其他严重错误仍会保持原生 panic fail-fast。
func (s *Server) RouteConflicts() []httpx.RouteConflict {
	if s == nil || s.reg == nil {
		return nil
	}
	return s.reg.conflicts()
}

func normalizeRoutePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || prefix == "/" {
		return ""
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	return strings.TrimSuffix(prefix, "/")
}

func normalizeRoutePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func joinRoutePrefix(parentPrefix string, childPrefix string) string {
	parentPrefix = normalizeRoutePrefix(parentPrefix)
	childPrefix = normalizeRoutePrefix(childPrefix)
	if parentPrefix == "" {
		return childPrefix
	}
	if childPrefix == "" {
		return parentPrefix
	}
	return parentPrefix + childPrefix
}

func joinRoutePath(prefix string, path string) string {
	prefix = normalizeRoutePrefix(prefix)
	path = normalizeRoutePath(path)
	if prefix == "" {
		return path
	}
	if path == "" || path == "/" {
		return prefix
	}
	return prefix + path
}

var _ httpx.IServer = (*Server)(nil)
var _ httpx.IRouteRegistry = (*Server)(nil)
var _ httpx.IContext = (*ginContext)(nil)
