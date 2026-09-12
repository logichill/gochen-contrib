package gogin

import (
	"testing"

	runtimehttp "gochen-runtime/http"
	"gochen-runtime/http/contracttest"
)

func TestHTTPContract(t *testing.T) {
	contracttest.Run(t, func(cfg *runtimehttp.WebConfig) (contracttest.IServer, error) {
		return New(cfg)
	})
}
