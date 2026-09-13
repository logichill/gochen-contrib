package gogin

import (
	"gochen/httpx"
)

func (s *Server) registerRoute(method, path string, handler httpx.Handler) {
	normPath := httpx.NormalizeRoutePath(path)
	if s.reg.tryReserve(method, normPath) {
		return
	}
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if conflictPath, ok := duplicateRoutePath(r); ok {
			if conflictPath != normPath {
				s.reg.rollback(method, normPath)
			}
			s.reg.markConflict(method, conflictPath)
			return
		}
		s.reg.rollback(method, normPath)
		panic(r)
	}()
	s.engine.Handle(method, path, s.wrapHandler(handler))
}

// GET 注册 GET 路由。
func (s *Server) GET(path string, handler httpx.Handler) httpx.IServer {
	s.registerRoute("GET", path, handler)
	return s
}

// POST 注册 POST 路由。
func (s *Server) POST(path string, handler httpx.Handler) httpx.IServer {
	s.registerRoute("POST", path, handler)
	return s
}

// PUT 注册 PUT 路由。
func (s *Server) PUT(path string, handler httpx.Handler) httpx.IServer {
	s.registerRoute("PUT", path, handler)
	return s
}

// DELETE 注册 DELETE 路由。
func (s *Server) DELETE(path string, handler httpx.Handler) httpx.IServer {
	s.registerRoute("DELETE", path, handler)
	return s
}

// PATCH 注册 PATCH 路由。
func (s *Server) PATCH(path string, handler httpx.Handler) httpx.IServer {
	s.registerRoute("PATCH", path, handler)
	return s
}

// HEAD 注册 HEAD 路由。
func (s *Server) HEAD(path string, handler httpx.Handler) httpx.IServer {
	s.registerRoute("HEAD", path, handler)
	return s
}

// OPTIONS 注册 OPTIONS 路由。
func (s *Server) OPTIONS(path string, handler httpx.Handler) httpx.IServer {
	s.registerRoute("OPTIONS", path, handler)
	return s
}

// Group 创建继承当前中间件的路由组。
func (s *Server) Group(prefix string) httpx.IRouteGroup {
	group := s.engine.Group(prefix)
	return &routeGroup{group: group, server: s, prefix: httpx.NormalizeRoutePrefix(prefix)}
}

// Use 追加 Gin 全局中间件，覆盖后续路由注册以及 NoRoute/NoMethod。
func (s *Server) Use(middleware ...httpx.Middleware) httpx.IServer {
	for _, mw := range middleware {
		if mw != nil {
			s.engine.Use(s.wrapMiddleware(mw))
		}
	}
	return s
}
