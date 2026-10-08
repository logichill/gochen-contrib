package prometheusmetrics

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestMetrics_RegisterAndObserve(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewWith(reg, reg)

	if err := m.RegisterCounter("test_counter", "help", []string{"b", "a"}); err != nil {
		t.Fatalf("RegisterCounter: %v", err)
	}
	if err := m.RegisterGauge("test_gauge", "help", []string{"k"}); err != nil {
		t.Fatalf("RegisterGauge: %v", err)
	}
	if err := m.RegisterHistogram("test_hist", "help", []string{"k"}, nil); err != nil {
		t.Fatalf("RegisterHistogram: %v", err)
	}

	// 未注册的指标应为 no-op
	m.Counter("not_exist", 1, nil)
	m.Gauge("not_exist", 1, nil)
	m.Histogram("not_exist", 1, nil)

	m.Counter("test_counter", 2, map[string]string{"a": "1", "b": "2", "ignored": "x"})
	m.Gauge("test_gauge", 3.14, map[string]string{"k": "v"})
	m.Histogram("test_hist", 7, map[string]string{"k": "v"})

	// Handler 输出中应包含我们的指标名（无需强依赖 Prometheus exposition 细节）。
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, "test_counter") {
		t.Fatalf("expected exposition contains test_counter, got: %s", body)
	}
	if !strings.Contains(body, "test_gauge") {
		t.Fatalf("expected exposition contains test_gauge, got: %s", body)
	}
	if !strings.Contains(body, "test_hist") {
		t.Fatalf("expected exposition contains test_hist, got: %s", body)
	}
	// 仅指标名出现不足以捕获“观测值/标签过滤”回归，这里断言样本行本身。
	for _, want := range []string{
		`test_counter{a="1",b="2"} 2`,
		`test_gauge{k="v"} 3.14`,
		`test_hist_count{k="v"} 1`,
		`test_hist_sum{k="v"} 7`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected exposition contains %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, "not_exist") {
		t.Fatalf("unregistered metric should be a no-op, got: %s", body)
	}
	if strings.Contains(body, `ignored=`) {
		t.Fatalf("unregistered label should be filtered out, got: %s", body)
	}
}
