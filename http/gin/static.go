package gogin

import (
	"net/http"
	"net/url"
	"strings"

	gochenhttp "gochen-runtime/http"
	"gochen/httpx"

	"github.com/gin-gonic/gin"
)

// Static 挂载静态目录，并应用共享的路径安全规则。
func (s *Server) Static(prefix, root string) httpx.IServer {
	p := normalizeRoutePath(prefix)
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	fullPattern := p + "*filepath"

	dupGET := s.reg.tryReserve("GET", fullPattern)
	dupHEAD := s.reg.tryReserve("HEAD", fullPattern)
	if dupGET || dupHEAD {
		if !dupGET {
			s.reg.rollback("GET", fullPattern)
		}
		if !dupHEAD {
			s.reg.rollback("HEAD", fullPattern)
		}
		return s
	}

	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if conflictPath, ok := duplicateRoutePath(r); ok {
			s.reg.rollback("GET", fullPattern)
			s.reg.rollback("HEAD", fullPattern)
			s.reg.markConflict("GET", conflictPath)
			s.reg.markConflict("HEAD", conflictPath)
			return
		}
		s.reg.rollback("GET", fullPattern)
		s.reg.rollback("HEAD", fullPattern)
		panic(r)
	}()
	handler := gin.WrapH(http.StripPrefix(strings.TrimSuffix(p, "/"), gochenhttp.NewStaticHandler(root)))
	s.engine.GET(fullPattern, handler)
	s.engine.HEAD(fullPattern, handler)
	if base := strings.TrimSuffix(p, "/"); base != "" {
		redirect := func(ctx httpx.IContext) error {
			request := ctx.(*ginContext).Request()
			location := url.URL{Path: p, RawQuery: request.URL.RawQuery}
			ctx.SetHeader("Location", location.String())
			return ctx.String(http.StatusMovedPermanently, "")
		}
		s.registerRoute(http.MethodGet, base, redirect)
		s.registerRoute(http.MethodHead, base, redirect)
	}
	return s
}

// ServeStatic 将一个显式指定的文件挂载到 GET/HEAD 路由。
func (s *Server) ServeStatic(path, root string) {
	p := normalizeRoutePath(path)
	dupGET := s.reg.tryReserve("GET", p)
	dupHEAD := s.reg.tryReserve("HEAD", p)
	if dupGET || dupHEAD {
		if !dupGET {
			s.reg.rollback("GET", p)
		}
		if !dupHEAD {
			s.reg.rollback("HEAD", p)
		}
		return
	}

	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if conflictPath, ok := duplicateRoutePath(r); ok {
			s.reg.rollback("GET", p)
			s.reg.rollback("HEAD", p)
			s.reg.markConflict("GET", conflictPath)
			s.reg.markConflict("HEAD", conflictPath)
			return
		}
		s.reg.rollback("GET", p)
		s.reg.rollback("HEAD", p)
		panic(r)
	}()
	handler := gin.WrapH(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if w.Header().Get("X-Content-Type-Options") == "" {
			w.Header().Set("X-Content-Type-Options", "nosniff")
		}
		http.ServeFile(w, r, root)
	}))
	s.engine.GET(path, handler)
	s.engine.HEAD(path, handler)
}
