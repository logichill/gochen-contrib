package gogin

import (
	"gochen/errors"
	"gochen/httpx"
	"strings"

	"github.com/gin-gonic/gin"
)

// Routes 使用 Gin Engine 的实际注册表，包含预注册与静态路由。
func (s *Server) Routes() []httpx.RegisteredRoute {
	s.registrationMu.Lock()
	defer s.registrationMu.Unlock()
	registered := s.engine.Routes()
	out := make([]httpx.RegisteredRoute, 0, len(registered))
	for _, route := range registered {
		out = append(out, httpx.RegisteredRoute{Method: route.Method, Path: route.Path})
	}
	return out
}

// SealRoutes 封存已完成的真实注册；后续修改保持拒绝并记录错误。
func (s *Server) SealRoutes() error {
	s.registrationMu.Lock()
	defer s.registrationMu.Unlock()
	s.reg.mu.Lock()
	defer s.reg.mu.Unlock()
	if s.reg.sealed {
		s.checkNativeRouteDriftLocked()
		return s.reg.err
	}
	s.reg.sealedRoutes = make(map[routeKey]struct{})
	for _, route := range s.engine.Routes() {
		s.reg.sealedRoutes[routeKey{method: route.Method, path: route.Path}] = struct{}{}
	}
	s.reg.sealed = true
	return s.reg.err
}

// RegistrationError 返回链式注册 API 拒绝修改的错误。
func (s *Server) RegistrationError() error {
	s.registrationMu.Lock()
	defer s.registrationMu.Unlock()
	s.reg.mu.Lock()
	defer s.reg.mu.Unlock()
	if s.reg.sealed {
		s.checkNativeRouteDriftLocked()
	}
	return s.reg.err
}

// checkNativeRouteDriftLocked 检查原生 Engine 注册是否改变封存清单。
func (s *Server) checkNativeRouteDriftLocked() {
	routes := s.engine.Routes()
	if len(routes) != len(s.reg.sealedRoutes) {
		s.reg.err = errors.NewCode(errors.FailedPrecondition, "native route registration changed after sealing; assemble and validate a new server")
		return
	}
	for _, route := range routes {
		if _, ok := s.reg.sealedRoutes[routeKey{method: route.Method, path: route.Path}]; !ok {
			s.reg.err = errors.NewCode(errors.FailedPrecondition, "native route registration changed after sealing; assemble and validate a new server")
			return
		}
	}
}

// RoutePath 返回 Gin 实际注册的完整路径。
func (g *routeGroup) RoutePath(relative string) string {
	return calculateGinPath(g.group.BasePath(), relative)
}

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
