package gogin

import (
	"context"
	"net/http"

	gochenhttp "gochen-runtime/http"
	"gochen/errors"

	"github.com/gin-gonic/gin"
)

// Start 启动服务。
func (s *Server) Start(addr string) error {
	s.lifecycleMu.Lock()
	if s.stopped {
		s.lifecycleMu.Unlock()
		return http.ErrServerClosed
	}
	if s.server != nil {
		s.lifecycleMu.Unlock()
		return errors.NewCode(errors.Conflict, "http server is already starting or running")
	}
	srv, err := gochenhttp.NewHTTPServer(s.config, addr, s.engine)
	if err != nil {
		s.lifecycleMu.Unlock()
		return err
	}
	s.server = srv
	s.lifecycleMu.Unlock()
	defer func() {
		s.lifecycleMu.Lock()
		s.server = nil
		s.lifecycleMu.Unlock()
	}()

	if s.config.TLSEnabled {
		return srv.ListenAndServeTLS(s.config.CertFile, s.config.KeyFile)
	}
	return srv.ListenAndServe()
}

// Stop 停止服务。
func (s *Server) Stop(ctx context.Context) error {
	if ctx == nil {
		return errors.NewCode(errors.InvalidInput, "ctx is nil")
	}
	s.lifecycleMu.Lock()
	s.stopped = true
	srv := s.server
	s.lifecycleMu.Unlock()
	if srv == nil {
		return nil
	}
	if err := srv.Shutdown(ctx); err != nil {
		_ = srv.Close()
		return err
	}
	return nil
}

// Engine 返回底层 *gin.Engine，仅供 gin 适配层使用。
func (s *Server) Engine() *gin.Engine { return s.engine }

// Handler 返回底层 http.Handler，仅供桥接层使用。
func (s *Server) Handler() http.Handler { return s.engine }
