package gogin

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// RouteInfo 定义路由信息。
type RouteInfo struct {
	Method  string
	Path    string
	Handler string
}

type listRoutesOptions struct {
	filter func(path string) bool
}

// ListRoutesOption 定义路由列表可选配置函数。
type ListRoutesOption func(*listRoutesOptions)

// WithPathFilter 设置路径过滤器（返回 true 表示保留）。
func WithPathFilter(filter func(path string) bool) ListRoutesOption {
	return func(o *listRoutesOptions) {
		o.filter = filter
	}
}

// DefaultDocumentableFilter 返回默认可文档化路径过滤函数。
func DefaultDocumentableFilter(apiPrefix string) func(path string) bool {
	return func(path string) bool {
		if path == "" {
			return false
		}
		if apiPrefix != "" && strings.HasPrefix(path, apiPrefix) {
			return true
		}
		switch path {
		case "/health", "/ping", "/live", "/ready":
			return true
		default:
			return false
		}
	}
}

// ListRoutes 从 Gin Engine 或 Gin Server 中提取路由列表。
//
// target 可以是 *gin.Engine 或 EngineProvider（如 *Server）。
func ListRoutes(target any, opts ...ListRoutesOption) []RouteInfo {
	if isNil(target) {
		return nil
	}

	options := listRoutesOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	var engine *gin.Engine
	switch v := target.(type) {
	case *gin.Engine:
		engine = v
	case EngineProvider:
		engine = v.Engine()
	}

	if engine == nil {
		return nil
	}

	ginRoutes := engine.Routes()
	result := make([]RouteInfo, 0, len(ginRoutes))
	for _, r := range ginRoutes {
		if options.filter != nil && !options.filter(r.Path) {
			continue
		}
		result = append(result, RouteInfo{
			Method:  r.Method,
			Path:    r.Path,
			Handler: r.Handler,
		})
	}
	return result
}
